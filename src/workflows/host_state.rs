use std::path::{Path, PathBuf};

use almanac::prelude::*;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use time::OffsetDateTime;
use time::macros::format_description;

use crate::host_state::{Finding, Hosts, findings, read};
use crate::routing::Project;

/// The directory `HOST_STATE_REPORTS` names, or none when it is unset or empty. Always provided,
/// because almanac checks a scan's providers at startup.
pub struct HostReports(pub Option<PathBuf>);

fn minute(t: OffsetDateTime) -> String {
    t.to_offset(time::UtcOffset::UTC)
        .format(format_description!("[year]-[month]-[day]T[hour]:[minute]Z"))
        .expect("a time always formats")
}

fn status_name(status: crate::host_state::Status) -> String {
    format!("{status:?}").to_lowercase()
}

/// One pair per finding, the delivery id being the finding's key. A file in `dir` that no declared
/// host owns is logged and files nothing.
pub fn scan_findings(hosts: &Hosts, dir: &Path, now: OffsetDateTime) -> Vec<(String, Value)> {
    let mut out = vec![];
    for host in hosts.iter() {
        let result = read(dir, host);
        if let crate::host_state::Read::Invalid { error, .. } = &result {
            eprintln!("host-state: {error}");
        }
        for finding in findings(host, &result, now) {
            let (kind, check, status, since) = match &finding {
                Finding::Check {
                    check,
                    status,
                    since,
                    ..
                } => (
                    "check",
                    Some(check.clone()),
                    Some(status_name(*status)),
                    Some(minute(*since)),
                ),
                Finding::Stale { since, .. } => ("stale", None, None, since.map(minute)),
            };
            let payload = json!({
                "kind": kind,
                "project": host.project.as_str(),
                "subject": finding.key().replace('/', "."),
                "host": host.name,
                "check": check,
                "status": status,
                "since": since,
                "body": finding.body(),
            });
            out.push((finding.key(), payload));
        }
    }
    if let Ok(entries) = std::fs::read_dir(dir) {
        for entry in entries.flatten() {
            let name = entry.file_name().to_string_lossy().into_owned();
            if !hosts.iter().any(|h| name == format!("{}.json", h.name)) {
                eprintln!("host-state: {name} is not a declared host's report, ignoring");
            }
        }
    }
    out
}

observed! {
    /// One host-state finding. `body` carries a check's output line, which the host wrote, so it
    /// is tainted. `since` is `YYYY-MM-DDTHH:MMZ`, and a stale finding may have none.
    pub struct HostFinding {
        pub kind: Slug,
        pub project: Slug,
        pub subject: Slug,
        pub host: Slug,
        pub check: Option<Slug>,
        pub status: Option<Slug>,
        pub since: Option<Slug>,
        pub body: Untrusted<String>,
    }
}

impl Event for HostFinding {
    const KIND: &'static str = "host.state.finding";
    fn parse(p: &Payload<'_>) -> Result<Self, Malformed> {
        let malformed = |pointer: &str| Malformed {
            pointer: pointer.to_string(),
        };
        let kind: Slug = p.field("/kind")?;
        if !matches!(kind.to_string().as_str(), "check" | "stale") {
            return Err(malformed("/kind"));
        }
        let project: Slug = p.field("/project")?;
        if project.to_string().parse::<Project>().is_err() {
            return Err(malformed("/project"));
        }
        let check: Option<Slug> = p.field("/check")?;
        let status: Option<Slug> = p.field("/status")?;
        let since: Option<Slug> = p.field("/since")?;
        if kind.to_string() == "check" {
            if check.is_none() {
                return Err(malformed("/check"));
            }
            if status.is_none() {
                return Err(malformed("/status"));
            }
            if since.is_none() {
                return Err(malformed("/since"));
            }
        }
        if let Some(since) = &since
            && !since.to_string().contains('T')
        {
            return Err(malformed("/since"));
        }
        Ok(Self {
            kind,
            project,
            subject: p.field("/subject")?,
            host: p.field("/host")?,
            check,
            status,
            since,
            body: p.text("/body")?,
        })
    }
}

#[scan]
pub async fn scan(scanner: &Scanner) -> Result<Vec<Scanned<HostFinding>>, StepError> {
    let Some(dir) = &scanner.get::<HostReports>()?.0 else {
        eprintln!("host-state: HOST_STATE_REPORTS is not set, skipping");
        return Ok(vec![]);
    };
    let hosts = scanner.get::<Hosts>()?;
    Ok(scan_findings(&hosts, dir, OffsetDateTime::now_utc())
        .into_iter()
        .map(|(delivery_id, payload)| Scanned::new(delivery_id, payload))
        .collect())
}

clean! {
    pub struct Since {
        pub date: Slug,
        pub time: Slug,
    }
}

#[derive(Serialize, Deserialize)]
pub enum Facts {
    Check {
        target: Slug,
        host: Slug,
        check: Slug,
        status: Slug,
        since: Since,
        body: Untrusted<String>,
    },
    Stale {
        target: Slug,
        host: Slug,
        since: Option<Since>,
        body: Untrusted<String>,
    },
}

/// Splits `2026-11-10T14:50Z`, which `parse` has checked holds a `T`.
fn split(since: &Slug) -> Since {
    let text = since.to_string();
    let (date, time) = text.split_once('T').expect("parse checked for a T");
    Since {
        date: date.parse().expect("part of a slug is a slug"),
        time: time.parse().expect("part of a slug is a slug"),
    }
}

pub fn facts(event: &HostFinding) -> Facts {
    let target = event
        .project
        .to_string()
        .parse::<Project>()
        .expect("parse checked the project")
        .target();
    if event.kind.to_string() == "stale" {
        return Facts::Stale {
            target,
            host: event.host.clone(),
            since: event.since.as_ref().map(split),
            body: event.body.clone(),
        };
    }
    Facts::Check {
        target,
        host: event.host.clone(),
        check: event.check.clone().expect("parse checked the check"),
        status: event.status.clone().expect("parse checked the status"),
        since: split(event.since.as_ref().expect("parse checked the since")),
        body: event.body.clone(),
    }
}

#[workflow(
    on = HostFinding,
    key = "{subject}",
    intent = "File one task for each host check that has been failing long enough, and one when a host's report stops arriving.",
    may = [File],
    runs_per_hour = 1000,
    scan = scan,
    decide = policy,
)]
pub async fn host_state(_cx: &Cx, event: &HostFinding) -> Result<Facts, Halt> {
    Ok(facts(event))
}

pub fn policy(facts: &Facts) -> Vec<Effect> {
    match facts {
        Facts::Check {
            target,
            host,
            check,
            status,
            since,
            body,
        } => vec![Effect::File {
            title: title!(
                "host-state: {} {} on {} since {} {}",
                check,
                status,
                host,
                since.date,
                since.time
            ),
            target: target.clone(),
            body: body.clone(),
        }],
        Facts::Stale {
            target,
            host,
            since,
            body,
        } => vec![Effect::File {
            title: match since {
                Some(since) => title!(
                    "host-state: no report from {} since {} {}",
                    host,
                    since.date,
                    since.time
                ),
                None => title!("host-state: no report from {}", host),
            },
            target: target.clone(),
            body: body.clone(),
        }],
    }
}
