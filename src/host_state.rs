use std::collections::HashSet;
use std::path::Path;

use serde::Deserialize;
use time::format_description::well_known::Rfc3339;
use time::macros::format_description;
use time::{Duration, OffsetDateTime};

use crate::routing::Project;

pub const CRITICAL_AFTER: Duration = Duration::minutes(10);
pub const WARNING_AFTER: Duration = Duration::hours(24);
pub const STALE_AFTER: Duration = Duration::minutes(30);
/// A longer gap since the previous scan means the Mac likely slept, so an old report may only be
/// waiting on launchd's next collector run. It must stay above the scan interval
/// (`ALMANAC_SCAN_INTERVAL_MINUTES`, 15 by default), or an old report never files.
pub const WAKE_GAP: Duration = Duration::minutes(20);
/// A finding that comes back within this long of the last scan that saw it continues the same
/// outage, so a flapping check or an unreadable report rewritten every run files one task.
pub const RECOVERY_HOLD: Duration = Duration::hours(1);

const MAX_OUTPUT_BYTES: usize = 500;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status {
    Ok,
    Warning,
    Critical,
    Unknown,
}

impl Status {
    fn name(self) -> &'static str {
        match self {
            Status::Ok => "ok",
            Status::Warning => "warning",
            Status::Critical => "critical",
            Status::Unknown => "unknown",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CheckResult {
    pub name: String,
    pub status: Status,
    pub since: OffsetDateTime,
    pub output: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Report {
    pub host: String,
    pub generated_at: OffsetDateTime,
    pub interval_seconds: u64,
    pub checks: Vec<CheckResult>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawReport {
    host: String,
    generated_at: String,
    interval_seconds: u64,
    checks: Vec<RawCheck>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawCheck {
    name: String,
    status: i64,
    since: String,
    output: String,
}

fn name_is_valid(name: &str) -> bool {
    !name.is_empty()
        && name
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-')
}

fn parse_time(field: &str, text: &str) -> Result<OffsetDateTime, String> {
    OffsetDateTime::parse(text, &Rfc3339)
        .map(|t| t.to_offset(time::UtcOffset::UTC))
        .map_err(|e| format!("{field}: {text:?} is not an RFC 3339 time: {e}"))
}

fn cut_output(output: &str) -> String {
    let mut end = output.len().min(MAX_OUTPUT_BYTES);
    while !output.is_char_boundary(end) {
        end -= 1;
    }
    output[..end].to_string()
}

impl Report {
    pub fn parse(text: &str) -> Result<Report, String> {
        let raw: RawReport =
            serde_json::from_str(text).map_err(|e| format!("host-state report: {e}"))?;
        let generated_at = parse_time("generated_at", &raw.generated_at)?;
        let mut seen = HashSet::new();
        let mut checks = Vec::with_capacity(raw.checks.len());
        for check in raw.checks {
            let name = check.name;
            if !name_is_valid(&name) {
                return Err(format!("check {name:?} is outside [a-z0-9-]"));
            }
            if name == "stale" {
                return Err("check \"stale\" is reserved for the stale finding's key".to_string());
            }
            if !seen.insert(name.clone()) {
                return Err(format!("check {name} appears twice"));
            }
            let status = match check.status {
                0 => Status::Ok,
                1 => Status::Warning,
                2 => Status::Critical,
                3 => Status::Unknown,
                other => {
                    return Err(format!("check {name}: status {other} is outside 0 to 3"));
                }
            };
            let since =
                parse_time("since", &check.since).map_err(|e| format!("check {name}: {e}"))?;
            checks.push(CheckResult {
                name,
                status,
                since,
                output: cut_output(&check.output),
            });
        }
        Ok(Report {
            host: raw.host,
            generated_at,
            interval_seconds: raw.interval_seconds,
            checks,
        })
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Host {
    pub name: String,
    pub project: Project,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RawHost {
    name: String,
    project: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct HostsFile {
    #[serde(default)]
    host: Vec<RawHost>,
}

#[derive(Debug)]
pub struct Hosts(Vec<Host>);

impl Hosts {
    pub fn parse(text: &str) -> Result<Hosts, String> {
        let file: HostsFile = toml::from_str(text).map_err(|e| format!("hosts: {e}"))?;
        let mut hosts: Vec<Host> = Vec::new();
        for raw in file.host {
            let name = raw.name;
            // The name becomes a file name under the reports directory, so it can't be a path.
            if !name_is_valid(&name) {
                return Err(format!("hosts: {name:?} is outside [a-z0-9-]"));
            }
            let project = raw
                .project
                .parse::<Project>()
                .map_err(|e| format!("hosts: {name}: {e}"))?;
            if hosts.iter().any(|h| h.name == name) {
                return Err(format!("hosts: {name} is declared twice"));
            }
            hosts.push(Host { name, project });
        }
        Ok(Hosts(hosts))
    }

    pub fn load() -> Result<Hosts, String> {
        Hosts::parse(include_str!("../hosts.toml"))
    }

    pub fn iter(&self) -> impl Iterator<Item = &Host> {
        self.0.iter()
    }
}

/// What the scan found at `<dir>/<host>.json`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Read {
    Report(Report),
    Missing,
    Invalid {
        modified: OffsetDateTime,
        error: String,
    },
}

pub fn read(dir: &Path, host: &Host) -> Read {
    let path = dir.join(format!("{}.json", host.name));
    let text = match std::fs::read_to_string(&path) {
        Ok(text) => text,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Read::Missing,
        Err(e) => {
            return Read::Invalid {
                modified: modified(&path),
                error: format!("reading {}: {e}", path.display()),
            };
        }
    };
    match Report::parse(&text) {
        Ok(report) if report.host == host.name => Read::Report(report),
        Ok(report) => Read::Invalid {
            modified: modified(&path),
            error: format!(
                "{} names host {:?}, expected {:?}",
                path.display(),
                report.host,
                host.name
            ),
        },
        Err(error) => Read::Invalid {
            modified: modified(&path),
            error,
        },
    }
}

fn modified(path: &Path) -> OffsetDateTime {
    std::fs::metadata(path)
        .and_then(|m| m.modified())
        .map(OffsetDateTime::from)
        .unwrap_or(OffsetDateTime::UNIX_EPOCH)
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Finding {
    Check {
        host: String,
        check: String,
        status: Status,
        since: OffsetDateTime,
        output: String,
    },
    Stale {
        host: String,
        since: Option<OffsetDateTime>,
        reason: String,
    },
}

/// `2026-11-10T14:50Z`, the form of a delivery ID and of the event's `since`.
pub fn iso_minute(t: OffsetDateTime) -> String {
    t.to_offset(time::UtcOffset::UTC)
        .format(format_description!("[year]-[month]-[day]T[hour]:[minute]Z"))
        .expect("a time always formats")
}

fn utc_minute(t: OffsetDateTime) -> String {
    t.to_offset(time::UtcOffset::UTC)
        .format(format_description!("[year]-[month]-[day] [hour]:[minute]Z"))
        .expect("a time always formats")
}

impl Finding {
    pub fn key(&self) -> String {
        match self {
            Finding::Check {
                host, check, since, ..
            } => format!("{host}/{check}/{}", iso_minute(*since)),
            Finding::Stale { host, since, .. } => match since {
                Some(since) => format!("{host}/stale/{}", iso_minute(*since)),
                None => format!("{host}/stale/missing"),
            },
        }
    }

    /// What stays the same across one outage, whatever its `since`.
    fn episode(&self) -> String {
        match self {
            Finding::Check {
                host,
                check,
                status,
                ..
            } => format!("{host}/{check}/{}", status.name()),
            Finding::Stale { host, .. } => format!("{host}/stale"),
        }
    }

    fn since_mut(&mut self) -> Option<&mut OffsetDateTime> {
        match self {
            Finding::Check { since, .. } => Some(since),
            Finding::Stale { since, .. } => since.as_mut(),
        }
    }

    pub fn title(&self) -> String {
        match self {
            Finding::Check {
                host,
                check,
                status,
                since,
                ..
            } => format!(
                "host-state: {check} {} on {host} since {}",
                status.name(),
                utc_minute(*since)
            ),
            Finding::Stale { host, since, .. } => match since {
                Some(since) => {
                    format!(
                        "host-state: no report from {host} since {}",
                        utc_minute(*since)
                    )
                }
                None => format!("host-state: no report from {host}"),
            },
        }
    }

    pub fn body(&self) -> String {
        match self {
            Finding::Check {
                status,
                since,
                output,
                ..
            } => format!(
                "{output}\nstatus: {}\nsince: {}",
                status.name(),
                since
                    .to_offset(time::UtcOffset::UTC)
                    .format(&Rfc3339)
                    .expect("a time always formats")
            ),
            Finding::Stale { reason, .. } => reason.clone(),
        }
    }
}

/// Whether the previous scan in this process ran recently enough that an old report means the
/// collector stopped. A previous scan later than `now` means the clock stepped back.
pub fn scan_gap_is_trusted(previous: Option<OffsetDateTime>, now: OffsetDateTime) -> bool {
    previous.is_some_and(|previous| previous <= now && now - previous <= WAKE_GAP)
}

/// A timestamp ahead of `now` (clock skew) counts as age zero.
fn age(t: OffsetDateTime, now: OffsetDateTime) -> Duration {
    (now - t).max(Duration::ZERO)
}

/// Whether `read` is a report that parsed but was generated more than `STALE_AFTER` ago.
pub fn is_old_report(read: &Read, now: OffsetDateTime) -> bool {
    matches!(read, Read::Report(report) if age(report.generated_at, now) > STALE_AFTER)
}

/// `old_report_may_file` is false when an old report should file nothing for now; missing and
/// invalid reports file regardless.
pub fn findings(
    host: &Host,
    read: &Read,
    now: OffsetDateTime,
    old_report_may_file: bool,
) -> Vec<Finding> {
    let stale = |since, reason| {
        vec![Finding::Stale {
            host: host.name.clone(),
            since,
            reason,
        }]
    };
    match read {
        Read::Missing => stale(None, format!("no report file for {}", host.name)),
        Read::Invalid { modified, error } => stale(Some(*modified), error.clone()),
        Read::Report(_) if is_old_report(read, now) && !old_report_may_file => vec![],
        Read::Report(report) if is_old_report(read, now) => stale(
            Some(report.generated_at),
            format!(
                "last report for {} was generated {}",
                host.name,
                utc_minute(report.generated_at)
            ),
        ),
        Read::Report(report) => report
            .checks
            .iter()
            .filter(|check| {
                let threshold = match check.status {
                    Status::Ok => return false,
                    Status::Warning => WARNING_AFTER,
                    Status::Critical | Status::Unknown => CRITICAL_AFTER,
                };
                age(check.since, now) >= threshold
            })
            .map(|check| Finding::Check {
                host: host.name.clone(),
                check: check.name.clone(),
                status: check.status,
                since: check.since,
                output: check.output.clone(),
            })
            .collect(),
    }
}

#[derive(Debug)]
struct Episode {
    start: OffsetDateTime,
    last_seen: OffsetDateTime,
}

/// The outages recent scans saw, so that a finding back within `RECOVERY_HOLD` keeps its first
/// `since`, and with it its key. It lives in memory, so a restart mid-flap files once more.
#[derive(Debug, Default)]
pub struct Episodes(std::collections::HashMap<String, Episode>);

impl Episodes {
    /// Rewrites each finding's `since` to the start of the outage it continues.
    pub fn continue_outages(&mut self, findings: &mut [Finding], now: OffsetDateTime) {
        self.0
            .retain(|_, episode| now - episode.last_seen <= RECOVERY_HOLD);
        for finding in findings {
            let id = finding.episode();
            let Some(since) = finding.since_mut() else {
                continue;
            };
            let episode = self.0.entry(id).or_insert(Episode {
                start: *since,
                last_seen: now,
            });
            episode.last_seen = now;
            *since = episode.start;
        }
    }
}
