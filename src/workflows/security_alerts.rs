use std::collections::BTreeSet;
use std::fmt;

use almanac::diag::Error;
use almanac::prelude::*;
use github::client::GithubClient;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use time::format_description::well_known::Rfc3339;
use time::{Date, OffsetDateTime, UtcOffset};

use crate::routing::Project;
use crate::tiers::{Tiers, WATCHED_BY_SECURITY_ALERTS};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Severity {
    Low,
    Medium,
    High,
    Critical,
    Unknown,
}

impl Severity {
    /// GitHub's other labels (`moderate` among them) are `Unknown`, which gets the medium window.
    pub fn from_github(s: &str) -> Severity {
        match s {
            "low" => Severity::Low,
            "medium" => Severity::Medium,
            "high" => Severity::High,
            "critical" => Severity::Critical,
            _ => Severity::Unknown,
        }
    }

    fn name(self) -> &'static str {
        match self {
            Severity::Low => "low",
            Severity::Medium => "medium",
            Severity::High => "high",
            Severity::Critical => "critical",
            Severity::Unknown => "unknown",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Secret,
    Dependency(Severity),
}

/// Days an alert gets before it is overdue. A secret has none.
pub const WINDOWS: &[(Kind, i64)] = &[
    (Kind::Secret, 0),
    (Kind::Dependency(Severity::Critical), 2),
    (Kind::Dependency(Severity::High), 14),
    (Kind::Dependency(Severity::Medium), 30),
    (Kind::Dependency(Severity::Unknown), 30),
    (Kind::Dependency(Severity::Low), 90),
];

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Alert {
    Dependency {
        number: u64,
        severity: Severity,
        package: String,
        manifest: String,
        ghsa: String,
        patched: Option<String>,
        created: Date,
        url: String,
    },
    Secret {
        number: u64,
        kind: String,
        created: Date,
        url: String,
    },
}

impl Alert {
    pub fn key(&self) -> String {
        match self {
            Alert::Dependency { number, .. } => format!("dependabot:{number}"),
            Alert::Secret { number, .. } => format!("secret:{number}"),
        }
    }

    fn kind(&self) -> Kind {
        match self {
            Alert::Dependency { severity, .. } => Kind::Dependency(*severity),
            Alert::Secret { .. } => Kind::Secret,
        }
    }

    fn created(&self) -> Date {
        match self {
            Alert::Dependency { created, .. } | Alert::Secret { created, .. } => *created,
        }
    }

    pub fn window_days(&self) -> i64 {
        let kind = self.kind();
        WINDOWS
            .iter()
            .find(|(k, _)| *k == kind)
            .map(|(_, days)| *days)
            .expect("every kind has a window")
    }

    pub fn line(&self, today: Date) -> String {
        let age = (today - self.created()).whole_days();
        match self {
            Alert::Dependency {
                number,
                severity,
                package,
                manifest,
                ghsa,
                patched,
                url,
                ..
            } => {
                let fix = match patched {
                    Some(version) => format!("fixed in {version}"),
                    None => "no fix yet".to_string(),
                };
                format!(
                    "#{number} {} {package} ({manifest}) {ghsa}, {fix}, {age} days, {url}",
                    severity.name()
                )
            }
            Alert::Secret {
                number, kind, url, ..
            } => format!("#{number} secret {kind}, {age} days, {url}"),
        }
    }
}

/// An alert is overdue on the last day of its window, so a secret is overdue the day it opens.
pub fn overdue(alerts: &[Alert], today: Date) -> Vec<&Alert> {
    alerts
        .iter()
        .filter(|a| (today - a.created()).whole_days() >= a.window_days())
        .collect()
}

/// First 12 hex characters of SHA-256 over the sorted keys joined with newlines.
pub fn digest(keys: &[String]) -> String {
    let mut sorted: Vec<&str> = keys.iter().map(String::as_str).collect();
    sorted.sort_unstable();
    let hash = Sha256::digest(sorted.join("\n").as_bytes());
    hash.iter()
        .take(6)
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

fn text<'a>(v: &'a Value, path: &[&str]) -> Result<&'a str, String> {
    path.iter()
        .try_fold(v, |node, key| node.get(key))
        .and_then(Value::as_str)
        .ok_or_else(|| format!("missing {}", path.join(".")))
}

fn number(v: &Value) -> Result<u64, String> {
    v.get("number")
        .and_then(Value::as_u64)
        .ok_or_else(|| "missing number".to_string())
}

/// The UTC date, so an alert opened late in a western evening ages from the next day.
fn created(v: &Value) -> Result<Date, String> {
    let raw = text(v, &["created_at"])?;
    OffsetDateTime::parse(raw, &Rfc3339)
        .map(|at| at.to_offset(UtcOffset::UTC).date())
        .map_err(|e| format!("created_at {raw:?}: {e}"))
}

pub fn parse_dependabot(v: &Value) -> Result<Alert, String> {
    let patched = v
        .pointer("/security_vulnerability/first_patched_version/identifier")
        .and_then(Value::as_str)
        .map(str::to_string);
    Ok(Alert::Dependency {
        number: number(v)?,
        severity: Severity::from_github(text(v, &["security_advisory", "severity"])?),
        package: text(v, &["dependency", "package", "name"])?.to_string(),
        manifest: text(v, &["dependency", "manifest_path"])?.to_string(),
        ghsa: text(v, &["security_advisory", "ghsa_id"])?.to_string(),
        patched,
        created: created(v)?,
        url: text(v, &["html_url"])?.to_string(),
    })
}

/// Never reads the alert's `secret` field.
pub fn parse_secret(v: &Value) -> Result<Alert, String> {
    let kind = text(v, &["secret_type_display_name"])
        .or_else(|_| text(v, &["secret_type"]))
        .map_err(|_| "missing secret_type_display_name and secret_type".to_string())?;
    Ok(Alert::Secret {
        number: number(v)?,
        kind: kind.to_string(),
        created: created(v)?,
        url: text(v, &["html_url"])?.to_string(),
    })
}

#[derive(Debug)]
pub enum Coverage<A> {
    On(Vec<A>),
    Off,
}

#[derive(Debug)]
pub struct RepoRead {
    pub private: bool,
    pub dependabot: Coverage<Alert>,
    pub secrets: Coverage<Alert>,
}

/// Always provided, because almanac checks a scan's providers at startup. `None` means no token.
pub struct AlertsClient(pub Option<GithubClient>);

fn failed(repo: &Slug, err: &Error) -> StepError {
    StepError(format!("{repo}: {err}"))
}

fn is_404(err: &Error) -> bool {
    matches!(err, Error::ForgeRefused { status: 404, .. })
}

/// Only a 404 means off: Dependabot answers 403 for a missing permission too.
pub fn alerts_switch(result: Result<String, Error>, repo: &Slug) -> Result<bool, StepError> {
    match result {
        Ok(_) => Ok(true),
        Err(e) if is_404(&e) => Ok(false),
        Err(e) => Err(failed(repo, &e)),
    }
}

/// A 404 here means off only because the caller already proved the repository exists.
pub fn secret_alerts(
    result: Result<Vec<Value>, Error>,
    repo: &Slug,
) -> Result<Coverage<Alert>, StepError> {
    match result {
        Ok(items) => parsed(&items, parse_secret, repo).map(Coverage::On),
        Err(e) if is_404(&e) => Ok(Coverage::Off),
        Err(e) => Err(failed(repo, &e)),
    }
}

pub fn repo_missing(err: Error, repo: &Slug) -> StepError {
    if is_404(&err) {
        StepError(format!(
            "{repo} was not found on GitHub: check tiers.toml and local/tiers.toml"
        ))
    } else {
        failed(repo, &err)
    }
}

fn parsed(
    items: &[Value],
    parse: fn(&Value) -> Result<Alert, String>,
    repo: &Slug,
) -> Result<Vec<Alert>, StepError> {
    items
        .iter()
        .map(|item| parse(item).map_err(|e| StepError(format!("{repo}: {e}"))))
        .collect()
}

pub async fn read_repo(client: &GithubClient, repo: &Slug) -> Result<RepoRead, StepError> {
    let found = client
        .get(&format!("/repos/{repo}"))
        .await
        .map_err(|e| repo_missing(e, repo))?;
    let private = found
        .get("private")
        .and_then(Value::as_bool)
        .ok_or_else(|| StepError(format!("{repo} has no private flag in the response")))?;

    let on = alerts_switch(
        client
            .get_text(&format!("/repos/{repo}/vulnerability-alerts"))
            .await,
        repo,
    )?;
    let dependabot = if on {
        let items = client
            .get_all(&format!("/repos/{repo}/dependabot/alerts?state=open"))
            .await
            .map_err(|e| failed(repo, &e))?;
        Coverage::On(parsed(&items, parse_dependabot, repo)?)
    } else {
        Coverage::Off
    };

    let secrets = secret_alerts(
        client
            .get_all(&format!("/repos/{repo}/secret-scanning/alerts?state=open"))
            .await,
        repo,
    )?;
    Ok(RepoRead {
        private,
        dependabot,
        secrets,
    })
}

const COVERAGE_PROJECT: &str = "brined-almanac";

/// Highest severity first, with secrets last.
fn rank(alert: &Alert) -> u8 {
    match alert {
        Alert::Dependency { severity, .. } => match severity {
            Severity::Critical => 0,
            Severity::High => 1,
            Severity::Medium => 2,
            Severity::Unknown => 3,
            Severity::Low => 4,
        },
        Alert::Secret { .. } => 5,
    }
}

fn alert_number(alert: &Alert) -> u64 {
    match alert {
        Alert::Dependency { number, .. } | Alert::Secret { number, .. } => *number,
    }
}

/// A repository name GitHub allows but a project name does not (uppercase, say) files under almanac
/// itself, since the subject still names the repository.
fn project_of(repo: &Slug) -> Slug {
    let name = repo.to_string();
    let name = name.rsplit('/').next().unwrap_or(&name).to_lowercase();
    name.parse::<Project>()
        .unwrap_or_else(|_| COVERAGE_PROJECT.parse().expect("a valid project"))
        .as_str()
        .parse()
        .expect("a project name is a slug")
}

fn overdue_finding(repo: &Slug, alerts: &[Alert], today: Date) -> Option<(String, Value)> {
    let mut late = overdue(alerts, today);
    if late.is_empty() {
        return None;
    }
    late.sort_by_key(|a| (rank(a), alert_number(a)));
    let keys: Vec<String> = late.iter().map(|a| a.key()).collect();
    let count = |wanted: Severity| {
        late.iter()
            .filter(|a| matches!(a, Alert::Dependency { severity, .. } if *severity == wanted))
            .count()
    };
    let secrets = late
        .iter()
        .filter(|a| matches!(a, Alert::Secret { .. }))
        .count();
    let lines: Vec<String> = late.iter().map(|a| a.line(today)).collect();
    let payload = json!({
        "kind": "overdue".parse::<Slug>().expect("slug"),
        "project": project_of(repo),
        "subject": repo,
        "critical": count(Severity::Critical),
        "high": count(Severity::High),
        "medium": count(Severity::Medium),
        "low": count(Severity::Low),
        "unknown": count(Severity::Unknown),
        "secrets": secrets,
        "dependabot_off": 0,
        "secret_scanning_off": 0,
        "repositories": 0,
        "lines": lines.join("\n"),
    });
    Some((
        format!("scan:security-alerts/{repo}@{}", digest(&keys)),
        payload,
    ))
}

fn coverage_finding(reads: &[(Slug, RepoRead)]) -> Option<(String, Value)> {
    let mut keys = vec![];
    let mut lines = vec![];
    let mut repositories = BTreeSet::new();
    let (mut dependabot_off, mut secret_scanning_off) = (0, 0);
    for (repo, read) in reads {
        if matches!(read.dependabot, Coverage::Off) {
            dependabot_off += 1;
            keys.push(format!("dependabot-off:{repo}"));
            lines.push(format!("Dependabot alerts are off: {repo}"));
            repositories.insert(repo.to_string());
        }
        // Secret scanning on a private repository needs a paid plan, so off there is not a gap.
        if matches!(read.secrets, Coverage::Off) && !read.private {
            secret_scanning_off += 1;
            keys.push(format!("secret-scanning-off:{repo}"));
            lines.push(format!("Secret scanning is off: {repo}"));
            repositories.insert(repo.to_string());
        }
    }
    if keys.is_empty() {
        return None;
    }
    let payload = json!({
        "kind": "coverage".parse::<Slug>().expect("slug"),
        "project": COVERAGE_PROJECT.parse::<Slug>().expect("slug"),
        "subject": "coverage".parse::<Slug>().expect("slug"),
        "critical": 0,
        "high": 0,
        "medium": 0,
        "low": 0,
        "unknown": 0,
        "secrets": 0,
        "dependabot_off": dependabot_off,
        "secret_scanning_off": secret_scanning_off,
        "repositories": repositories.len(),
        "lines": lines.join("\n"),
    });
    Some((
        format!("scan:security-alerts/coverage@{}", digest(&keys)),
        payload,
    ))
}

/// One `overdue` entry per repository with something overdue, then one `coverage` entry when
/// anything is off.
pub fn findings(reads: &[(Slug, RepoRead)], today: Date) -> Vec<(String, Value)> {
    let mut out: Vec<(String, Value)> = reads
        .iter()
        .filter_map(|(repo, read)| {
            let mut alerts = vec![];
            for side in [&read.dependabot, &read.secrets] {
                if let Coverage::On(found) = side {
                    alerts.extend(found.iter().cloned());
                }
            }
            overdue_finding(repo, &alerts, today)
        })
        .collect();
    out.extend(coverage_finding(reads));
    out
}

observed! {
    /// One security finding. `lines` carries package names and manifest paths from lockfiles, so
    /// it is tainted. `repositories` is how many distinct repositories a coverage finding names.
    pub struct SecurityFinding {
        pub kind: Slug,
        pub project: Slug,
        pub subject: Slug,
        pub critical: u64,
        pub high: u64,
        pub medium: u64,
        pub low: u64,
        pub unknown: u64,
        pub secrets: u64,
        pub dependabot_off: u64,
        pub secret_scanning_off: u64,
        pub repositories: u64,
        pub lines: Untrusted<String>,
    }
}

impl Event for SecurityFinding {
    const KIND: &'static str = "security.alert.finding";
    fn parse(p: &Payload<'_>) -> Result<Self, Malformed> {
        Ok(Self {
            kind: p.field("/kind")?,
            project: p.field("/project")?,
            subject: p.field("/subject")?,
            critical: p.field("/critical")?,
            high: p.field("/high")?,
            medium: p.field("/medium")?,
            low: p.field("/low")?,
            unknown: p.field("/unknown")?,
            secrets: p.field("/secrets")?,
            dependabot_off: p.field("/dependabot_off")?,
            secret_scanning_off: p.field("/secret_scanning_off")?,
            repositories: p.field("/repositories")?,
            lines: p.text("/lines")?,
        })
    }
}

#[scan]
pub async fn scan(scanner: &Scanner) -> Result<Vec<Scanned<SecurityFinding>>, StepError> {
    let client = scanner.get::<AlertsClient>()?;
    let Some(github) = &client.0 else {
        return Err(StepError(
            "GITHUB_ALERTS_TOKEN is not set; security-alerts needs a read-only alerts token"
                .to_string(),
        ));
    };
    let tiers = scanner.get::<Tiers>()?;
    let mut reads = vec![];
    for repo in tiers.watched(WATCHED_BY_SECURITY_ALERTS) {
        reads.push((repo.clone(), read_repo(github, repo).await?));
    }
    Ok(findings(&reads, OffsetDateTime::now_utc().date())
        .into_iter()
        .map(|(delivery_id, payload)| Scanned::new(delivery_id, payload))
        .collect())
}

clean! {
    pub struct Counts {
        pub critical: u64,
        pub high: u64,
        pub medium: u64,
        pub low: u64,
        pub unknown: u64,
        pub secrets: u64,
    }
}

impl Counts {
    pub fn total(&self) -> u64 {
        self.critical + self.high + self.medium + self.low + self.unknown + self.secrets
    }
}

impl fmt::Display for Counts {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let parts = [
            (self.critical, "critical"),
            (self.high, "high"),
            (self.medium, "medium"),
            (self.unknown, "unknown"),
            (self.low, "low"),
            (self.secrets, "secret"),
        ];
        let shown: Vec<String> = parts
            .iter()
            .filter(|(n, _)| *n > 0)
            .map(|(n, label)| format!("{n} {label}"))
            .collect();
        f.write_str(&shown.join(", "))
    }
}

#[derive(Serialize, Deserialize)]
pub enum Facts {
    Overdue {
        target: Slug,
        subject: Slug,
        counts: Counts,
        body: Untrusted<String>,
    },
    Coverage {
        target: Slug,
        repositories: u64,
        body: Untrusted<String>,
    },
}

/// `task:<project>`, or almanac's own project when the event's project is not a valid name.
fn target_of(project: &Slug) -> Slug {
    project
        .to_string()
        .parse::<Project>()
        .unwrap_or_else(|_| COVERAGE_PROJECT.parse().expect("a valid project"))
        .target()
}

pub fn facts(event: &SecurityFinding) -> Facts {
    let target = target_of(&event.project);
    if event.kind.to_string() == "coverage" {
        return Facts::Coverage {
            target,
            repositories: event.repositories,
            body: event.lines.clone(),
        };
    }
    Facts::Overdue {
        target,
        subject: event.subject.clone(),
        counts: Counts {
            critical: event.critical,
            high: event.high,
            medium: event.medium,
            low: event.low,
            unknown: event.unknown,
            secrets: event.secrets,
        },
        body: event.lines.clone(),
    }
}

#[workflow(
    on = SecurityFinding,
    key = "{subject}",
    intent = "File one task per repository with overdue security alerts, and one for repositories where alerts are off.",
    may = [File],
    runs_per_hour = 1000,
    scan = scan,
    decide = policy,
)]
pub async fn security_alerts(_cx: &Cx, event: &SecurityFinding) -> Result<Facts, Halt> {
    Ok(facts(event))
}

pub fn policy(facts: &Facts) -> Vec<Effect> {
    match facts {
        Facts::Overdue {
            target,
            subject,
            counts,
            body,
        } => vec![Effect::File {
            title: title!(
                "security-alerts: {} overdue on {} ({})",
                counts.total(),
                subject,
                counts
            ),
            target: target.clone(),
            body: body.clone(),
        }],
        Facts::Coverage {
            target,
            repositories,
            body,
        } => vec![Effect::File {
            title: title!(
                "security-alerts: alerts are off on {} repositories",
                repositories
            ),
            target: target.clone(),
            body: body.clone(),
        }],
    }
}
