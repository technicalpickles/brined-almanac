use std::path::{Path, PathBuf};
use std::sync::Mutex;

use almanac::prelude::*;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use time::OffsetDateTime;

use crate::host_state::{
    Episodes, Finding, Hosts, candidates, is_old_report, iso_minute, read, recovered,
    scan_gap_is_trusted,
};
use crate::routing::Project;

/// The directory `HOST_STATE_REPORTS` names, or none when it is unset or empty, plus when this
/// process last scanned and the outages it saw. Always provided, because almanac checks a scan's
/// providers at startup.
pub struct HostReports {
    pub dir: Option<PathBuf>,
    memory: Mutex<(Option<OffsetDateTime>, Episodes)>,
}

impl HostReports {
    pub fn new(dir: Option<PathBuf>) -> Self {
        Self {
            dir,
            memory: Mutex::new((None, Episodes::default())),
        }
    }
}

fn status_name(status: crate::host_state::Status) -> String {
    format!("{status:?}").to_lowercase()
}

/// One pair per finding, the delivery id being the finding's key, and one per check back to ok
/// for longer than the hold. A file in `dir` that no declared host owns is logged and files
/// nothing. `previous` is when this process last scanned.
pub fn scan_findings(
    hosts: &Hosts,
    dir: &Path,
    now: OffsetDateTime,
    previous: Option<OffsetDateTime>,
    episodes: &mut Episodes,
) -> Vec<(String, Value)> {
    let trusted = scan_gap_is_trusted(previous, now);
    let mut out = vec![];
    for host in hosts.iter() {
        let result = read(dir, host);
        if let crate::host_state::Read::Invalid { error, .. } = &result {
            eprintln!("host-state: {error}");
        }
        if !trusted && is_old_report(&result, now) {
            let why = match previous {
                None => "this is the first scan since almanac started".to_string(),
                Some(previous) if previous > now => {
                    format!(
                        "the previous scan at {} is later than now",
                        iso_minute(previous)
                    )
                }
                Some(previous) => format!(
                    "the previous scan was at {}, so the host likely slept",
                    iso_minute(previous)
                ),
            };
            eprintln!(
                "host-state: {}'s report is over 30 minutes old, but {why}; not filing it as stale until the next scan",
                host.name
            );
        }
        let mut found = candidates(host, &result, now, trusted);
        episodes.continue_outages(&mut found, now);
        for finding in found.into_iter().filter(|finding| finding.is_due(now)) {
            let (kind, check, status, since, outage) = match &finding {
                Finding::Check {
                    check,
                    status,
                    since,
                    ..
                } => (
                    "check",
                    Some(check.clone()),
                    Some(status_name(*status)),
                    Some(iso_minute(*since)),
                    format!("{}.{check}", host.name),
                ),
                Finding::Stale { since, .. } => (
                    "stale",
                    None,
                    None,
                    since.map(iso_minute),
                    format!("{}.stale", host.name),
                ),
            };
            let payload = json!({
                "kind": kind,
                "project": host.project.as_str(),
                "subject": finding.key().replace('/', "."),
                "outage": outage,
                "host": host.name,
                "check": check,
                "status": status,
                "since": since,
                "body": finding.body(),
            });
            out.push((finding.key(), payload));
        }
        for check in recovered(&result, now) {
            if episodes.holds(&host.name, &check.name, now) {
                continue;
            }
            let since = iso_minute(check.since);
            let id = format!("{}/{}/ok/{since}", host.name, check.name);
            let payload = json!({
                "kind": "recovered",
                "project": host.project.as_str(),
                "subject": id.replace('/', "."),
                "outage": format!("{}.{}", host.name, check.name),
                "host": host.name,
                "check": check.name,
                "status": "ok",
                "since": since,
                "body": "",
            });
            out.push((id, payload));
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
    /// is tainted. `since` is `YYYY-MM-DDTHH:MMZ`, and a stale finding may have none. `outage` is
    /// `<host>.<check>` or `<host>.stale`, the same for every outage of one check, so a recovery
    /// can close what each of them filed.
    pub struct HostFinding {
        pub kind: Slug,
        pub project: Slug,
        pub subject: Slug,
        pub outage: Slug,
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
        if !matches!(kind.to_string().as_str(), "check" | "stale" | "recovered") {
            return Err(malformed("/kind"));
        }
        let project: Slug = p.field("/project")?;
        if project.to_string().parse::<Project>().is_err() {
            return Err(malformed("/project"));
        }
        let check: Option<Slug> = p.field("/check")?;
        let status: Option<Slug> = p.field("/status")?;
        let since: Option<Slug> = p.field("/since")?;
        if kind.to_string() != "stale" {
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
        if kind.to_string() == "recovered"
            && status.as_ref().map(Slug::to_string).as_deref() != Some("ok")
        {
            return Err(malformed("/status"));
        }
        if since.as_ref().is_some_and(|since| split(since).is_none()) {
            return Err(malformed("/since"));
        }
        Ok(Self {
            kind,
            project,
            subject: p.field("/subject")?,
            outage: p.field("/outage")?,
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
    let now = OffsetDateTime::now_utc();
    let reports = scanner.get::<HostReports>()?;
    let mut memory = reports
        .memory
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    let (previous_scan, episodes) = &mut *memory;
    let previous = previous_scan.replace(now);
    let Some(dir) = &reports.dir else {
        eprintln!("host-state: HOST_STATE_REPORTS is not set, skipping");
        return Ok(vec![]);
    };
    let hosts = scanner.get::<Hosts>()?;
    Ok(scan_findings(&hosts, dir, now, previous, episodes)
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
    Recovered {
        target: Slug,
        outage: Slug,
        host: Slug,
        check: Slug,
        since: Since,
    },
}

/// Splits `2026-11-10T14:50Z` into its date and time, each a slug.
fn split(since: &Slug) -> Option<Since> {
    let text = since.to_string();
    let (date, time) = text.split_once('T')?;
    Some(Since {
        date: date.parse().ok()?,
        time: time.parse().ok()?,
    })
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
            since: event
                .since
                .as_ref()
                .map(|since| split(since).expect("parse checked the since")),
            body: event.body.clone(),
        };
    }
    if event.kind.to_string() == "recovered" {
        return Facts::Recovered {
            target,
            outage: event.outage.clone(),
            host: event.host.clone(),
            check: event.check.clone().expect("parse checked the check"),
            since: split(event.since.as_ref().expect("parse checked the since"))
                .expect("parse checked the since"),
        };
    }
    Facts::Check {
        target,
        host: event.host.clone(),
        check: event.check.clone().expect("parse checked the check"),
        status: event.status.clone().expect("parse checked the status"),
        since: split(event.since.as_ref().expect("parse checked the since"))
            .expect("parse checked the since"),
        body: event.body.clone(),
    }
}

#[workflow(
    on = HostFinding,
    key = "{outage}",
    intent = "File one task for each host check that has been failing long enough, and one when a host's report stops arriving. Close a check's tasks once it has been ok for an hour.",
    may = [File, Resolve],
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
        Facts::Recovered {
            target,
            outage,
            host,
            check,
            since,
        } => vec![Effect::Resolve {
            target: target.clone(),
            item: Item::Filed {
                key: outage.to_string(),
            },
            evidence: title!(
                "host-state: {} ok on {} since {} {}",
                check,
                host,
                since.date,
                since.time
            ),
        }],
    }
}
