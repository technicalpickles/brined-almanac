use almanac::diag::Error;
use almanac::prelude::{Slug, StepError};
use github::client::GithubClient;
use serde_json::Value;
use sha2::{Digest, Sha256};
use time::format_description::well_known::Rfc3339;
use time::{Date, OffsetDateTime, UtcOffset};

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

pub struct AlertsClient(pub GithubClient);

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
