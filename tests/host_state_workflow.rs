use std::collections::BTreeSet;
use std::path::Path;
use std::sync::Arc;
use std::time::SystemTime;

use almanac::prelude::*;
use almanac::runtime::Runtime;
use almanac::testing::FakeTracker;
use brined_almanac::bugsink::BugsinkClient;
use brined_almanac::host_state::{Episodes, Finding, Hosts, Status};
use brined_almanac::providers::host_reports_from;
use brined_almanac::tiers::Tiers;
use brined_almanac::transcript::Transcripts;
use brined_almanac::workflows;
use brined_almanac::workflows::bugsink_issues::BugsinkReader;
use brined_almanac::workflows::host_state::{
    HostFinding, HostReports, facts, policy, scan_findings,
};
use brined_almanac::workflows::security_alerts::AlertsClient;
use serde_json::{Value, json};
use time::format_description::well_known::Rfc3339;
use time::macros::datetime;
use time::{Duration, OffsetDateTime};

const CRITICAL: &str = include_str!("fixtures/host_state/mac.json");

fn hosts() -> Hosts {
    Hosts::parse("[[host]]\nname = \"mac\"\nproject = \"brined-almanac\"\n").unwrap()
}

fn dir_with(files: &[(&str, &str)]) -> tempfile::TempDir {
    let dir = tempfile::tempdir().unwrap();
    for (name, text) in files {
        std::fs::write(dir.path().join(name), text).unwrap();
    }
    dir
}

const NOW: OffsetDateTime = datetime!(2026-11-10 15:00 UTC);

fn scan(dir: &Path) -> Vec<(String, Value)> {
    scan_after(dir, Some(NOW - Duration::minutes(15)))
}

fn scan_after(dir: &Path, previous: Option<OffsetDateTime>) -> Vec<(String, Value)> {
    scan_findings(&hosts(), dir, NOW, previous, &mut Episodes::default())
}

fn stale_report() -> String {
    CRITICAL.replace(
        "\"generated_at\": \"2026-11-10T15:00:00Z\"",
        "\"generated_at\": \"2026-11-10T14:29:00Z\"",
    )
}

fn first_event() -> Value {
    let dir = dir_with(&[("mac.json", CRITICAL)]);
    scan(dir.path()).remove(0).1
}

fn title_of(effects: &[Effect]) -> String {
    match &effects[0] {
        Effect::File { title, .. } => title.to_string(),
        other => panic!("expected File, got {other:?}"),
    }
}

#[test]
fn a_critical_report_scans_to_one_event() {
    let dir = dir_with(&[("mac.json", CRITICAL)]);
    let found = scan(dir.path());
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].0, "mac/gh-auth/2026-11-10T14:50Z");
    assert_eq!(found[0].1["kind"], "check");
    assert_eq!(found[0].1["project"], "brined-almanac");
}

#[test]
fn a_healthy_report_scans_to_nothing() {
    let healthy = CRITICAL.replace("\"status\": 2", "\"status\": 0");
    let dir = dir_with(&[("mac.json", &healthy)]);
    assert!(scan(dir.path()).is_empty());
}

#[test]
fn a_declared_host_with_no_file_scans_to_stale() {
    let dir = dir_with(&[]);
    let found = scan(dir.path());
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].0, "mac/stale/missing");
    assert_eq!(found[0].1["kind"], "stale");
}

#[test]
fn an_undeclared_report_files_nothing() {
    let lab = CRITICAL.replace("\"host\": \"mac\"", "\"host\": \"lab\"");
    let dir = dir_with(&[("mac.json", CRITICAL), ("lab.json", &lab)]);
    let found = scan(dir.path());
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].0, "mac/gh-auth/2026-11-10T14:50Z");
}

#[test]
fn the_event_parses_back() {
    let event = HostFinding::from_json(&first_event()).unwrap();
    assert_eq!(
        title_of(&policy(&facts(&event))),
        "host-state: gh-auth critical on mac since 2026-11-10 14:50Z"
    );
}

#[test]
fn a_payload_with_another_kind_is_malformed() {
    let mut payload = first_event();
    payload["kind"] = json!("other");
    let err = HostFinding::from_json(&payload).unwrap_err();
    assert_eq!(err.pointer, "/kind");
}

#[test]
fn a_payload_with_a_bad_project_is_malformed() {
    let mut payload = first_event();
    payload["project"] = json!("Bad Name");
    let err = HostFinding::from_json(&payload).unwrap_err();
    assert_eq!(err.pointer, "/project");
}

#[test]
fn policy_files_one_task_to_the_project() {
    let event = HostFinding::from_json(&first_event()).unwrap();
    let effects = policy(&facts(&event));
    assert_eq!(effects.len(), 1);
    let finding = Finding::Check {
        host: "mac".into(),
        check: "gh-auth".into(),
        status: Status::Critical,
        since: datetime!(2026-11-10 14:50 UTC),
        output: String::new(),
    };
    match &effects[0] {
        Effect::File { target, title, .. } => {
            assert_eq!(target.to_string(), "task:brined-almanac");
            assert_eq!(title.to_string(), finding.title());
        }
        other => panic!("expected File, got {other:?}"),
    }
}

#[test]
fn a_stale_event_files_the_stale_title() {
    let dir = dir_with(&[]);
    let payload = scan(dir.path()).remove(0).1;
    let event = HostFinding::from_json(&payload).unwrap();
    assert_eq!(
        title_of(&policy(&facts(&event))),
        "host-state: no report from mac"
    );
}

#[test]
fn the_body_stays_untrusted() {
    let hostile = CRITICAL.replace(
        "GH CRITICAL - gh auth status timed out after 20s",
        "`rm` @someone",
    );
    let dir = dir_with(&[("mac.json", &hostile)]);
    let payload = scan(dir.path()).remove(0).1;
    let event = HostFinding::from_json(&payload).unwrap();
    match &policy(&facts(&event))[0] {
        Effect::File { body, .. } => {
            assert_eq!(
                brined_almanac::providers::body_text(body).unwrap(),
                "`rm` @someone\nstatus: critical\nsince: 2026-11-10T14:50:00Z"
            );
        }
        other => panic!("expected File, got {other:?}"),
    }
}

#[test]
fn the_reports_dir_is_none_when_unset_or_empty() {
    assert_eq!(host_reports_from(|_| None).dir, None);
    assert_eq!(host_reports_from(|_| Some(String::new())).dir, None);
    assert_eq!(
        host_reports_from(|_| Some("/host-state".to_string())).dir,
        Some("/host-state".into())
    );
}

#[test]
fn a_since_with_an_empty_half_is_malformed() {
    for since in ["T", "2026-11-10T", "T14:50Z", "2026-11-10 14:50Z"] {
        let mut payload = first_event();
        payload["since"] = json!(since);
        let err = HostFinding::from_json(&payload).unwrap_err();
        assert_eq!(err.pointer, "/since", "{since}");
    }
}

#[test]
fn a_report_naming_another_host_scans_to_one_stale_pair() {
    let other = CRITICAL.replace("\"host\": \"mac\"", "\"host\": \"lab\"");
    let dir = dir_with(&[("mac.json", &other)]);
    let found = scan(dir.path());
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].1["kind"], "stale");
    assert_eq!(found[0].1["host"], "mac");
}

#[test]
fn host_state_is_registered_first() {
    assert_eq!(brined_almanac::workflows::all()[0].name(), "host-state");
}

#[test]
fn a_stale_report_with_no_previous_scan_files_nothing() {
    let dir = dir_with(&[("mac.json", &stale_report())]);
    let found = scan_after(dir.path(), None);
    assert!(found.is_empty(), "{found:?}");
}

#[test]
fn a_stale_report_after_a_scan_fifteen_minutes_ago_files_stale_only() {
    let dir = dir_with(&[("mac.json", &stale_report())]);
    let found = scan_after(dir.path(), Some(NOW - Duration::minutes(15)));
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].0, "mac/stale/2026-11-10T14:29Z");
    assert_eq!(found[0].1["kind"], "stale");
}

#[test]
fn a_stale_report_after_a_twenty_one_minute_gap_files_nothing() {
    let dir = dir_with(&[("mac.json", &stale_report())]);
    let found = scan_after(dir.path(), Some(NOW - Duration::minutes(21)));
    assert!(found.is_empty(), "{found:?}");
}

#[test]
fn a_stale_report_after_a_scan_later_than_now_files_nothing() {
    let dir = dir_with(&[("mac.json", &stale_report())]);
    let found = scan_after(dir.path(), Some(NOW + Duration::minutes(5)));
    assert!(found.is_empty(), "{found:?}");
}

#[test]
fn a_missing_report_files_with_no_previous_scan() {
    let dir = dir_with(&[]);
    let found = scan_after(dir.path(), None);
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].0, "mac/stale/missing");
}

#[test]
fn an_invalid_report_files_with_no_previous_scan() {
    let dir = dir_with(&[("mac.json", "{")]);
    let found = scan_after(dir.path(), None);
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].1["kind"], "stale");
}

#[tokio::test]
async fn a_stale_report_files_on_the_second_scan_in_a_process() {
    let reports = tempfile::tempdir().unwrap();
    let old = CRITICAL.replace("2026-11-10T15:00:00Z", "2000-01-01T00:00:00Z");
    std::fs::write(reports.path().join("mac.json"), old).unwrap();
    let transcripts = tempfile::tempdir().unwrap();
    let providers = Providers::new()
        .provide::<Tiers>(Arc::new(Tiers::parse("", None).unwrap()))
        .provide::<AlertsClient>(Arc::new(AlertsClient(None)))
        .provide::<Hosts>(Arc::new(hosts()))
        .provide::<HostReports>(Arc::new(HostReports::new(Some(
            reports.path().to_path_buf(),
        ))))
        .provide::<BugsinkClient>(Arc::new(BugsinkClient(None)))
        .provide::<BugsinkReader>(Arc::new(BugsinkReader(None)))
        .provide::<Transcripts>(Arc::new(Transcripts(transcripts.path().to_path_buf())))
        .provide::<dyn Tracker>(Arc::new(FakeTracker::default()));
    let rt = Runtime::new(workflows::all(), providers);
    assert_eq!(rt.backfill(None).await.unwrap().found, 0);
    assert_eq!(rt.backfill(None).await.unwrap().found, 1);
}

#[derive(Clone, Copy)]
enum Truth {
    Status(i64),
    Invalid,
    Silent,
}

/// What a replay did to the tracker: each task filed, by outage, and whether it is still open.
#[derive(Default)]
struct Tracked {
    seen: BTreeSet<String>,
    tasks: Vec<(String, bool)>,
    recoveries: Vec<i64>,
    resumes: Vec<i64>,
}

impl Tracked {
    fn filed(&self) -> usize {
        self.tasks.len()
    }

    fn open(&self) -> usize {
        self.tasks.iter().filter(|(_, open)| *open).count()
    }

    /// One new delivery, as almanac would handle it: a duplicate does nothing, a finding files, and
    /// a recovery or a resume closes every open task its outage filed.
    fn deliver(&mut self, id: String, payload: &Value, minute: i64) {
        if !self.seen.insert(id) {
            return;
        }
        let outage = payload["outage"].as_str().unwrap().to_string();
        if payload["status"] == "ok" {
            match payload["kind"].as_str() {
                Some("recovered") => self.recoveries.push(minute),
                _ => self.resumes.push(minute),
            }
            for task in self.tasks.iter_mut().filter(|(o, _)| *o == outage) {
                task.1 = false;
            }
        } else {
            self.tasks.push((outage, true));
        }
    }
}

/// Replays `minutes` of a host whose one check follows `truth`, with the collector writing every
/// 5 minutes as `checks/collect` does and a scan every 15, and returns how many tasks filed.
fn tasks_filed(minutes: i64, truth: impl Fn(i64) -> Truth) -> usize {
    replay(minutes, truth, &[]).filed()
}

/// The replay behind `tasks_filed`, with almanac restarting, and so forgetting its episodes, at
/// each minute in `restarts`.
fn replay(minutes: i64, truth: impl Fn(i64) -> Truth, restarts: &[i64]) -> Tracked {
    let start = datetime!(2026-11-11 08:00 UTC);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("mac.json");
    let rfc = |t: OffsetDateTime| t.format(&Rfc3339).unwrap();
    let mut last: Option<(i64, OffsetDateTime)> = None;
    let mut episodes = Episodes::default();
    let mut tracked = Tracked::default();
    for m in 0..minutes {
        let now = start + Duration::minutes(m);
        if restarts.contains(&m) {
            episodes = Episodes::default();
        }
        if m % 5 == 0 {
            match truth(m) {
                Truth::Status(status) => {
                    let since = match last {
                        Some((s, since)) if s == status => since,
                        _ => now,
                    };
                    last = Some((status, since));
                    let report = json!({
                        "host": "mac",
                        "generated_at": rfc(now),
                        "interval_seconds": 300,
                        "checks": [{"name": "c", "status": status, "since": rfc(since), "output": ""}],
                    });
                    std::fs::write(&path, report.to_string()).unwrap();
                }
                Truth::Invalid => {
                    std::fs::write(&path, "{").unwrap();
                    std::fs::File::options()
                        .write(true)
                        .open(&path)
                        .unwrap()
                        .set_modified(SystemTime::from(now))
                        .unwrap();
                }
                Truth::Silent => {}
            }
        }
        if m % 15 == 7 && m > 15 {
            let previous = Some(now - Duration::minutes(15));
            for (id, payload) in scan_findings(&hosts(), dir.path(), now, previous, &mut episodes) {
                tracked.deliver(id, &payload, m);
            }
        }
    }
    tracked
}

#[test]
fn a_second_outage_the_same_day_files_again() {
    assert_eq!(
        tasks_filed(600, |m| {
            Truth::Status(if (60..110).contains(&m) || (240..300).contains(&m) {
                2
            } else {
                0
            })
        }),
        2
    );
}

#[test]
fn an_outage_across_midnight_files_once() {
    assert_eq!(
        tasks_filed(60 * 40, |m| {
            Truth::Status(if (60..60 * 37).contains(&m) { 2 } else { 0 })
        }),
        1
    );
}

#[test]
fn a_check_flapping_inside_the_hold_files_once() {
    let truth = |m| Truth::Status(if m >= 30 && m % 40 < 30 { 2 } else { 0 });
    assert_eq!(tasks_filed(540, truth), 1);
}

#[test]
fn a_check_wobbling_between_critical_and_unknown_files_once() {
    let truth = |m| {
        Truth::Status(if m < 30 {
            0
        } else if (m / 5) % 2 == 0 {
            2
        } else {
            3
        })
    };
    assert_eq!(tasks_filed(240, truth), 1);
}

#[test]
fn a_check_flapping_faster_than_the_threshold_files_once() {
    let truth = |m| Truth::Status(if m >= 30 && (m / 5) % 2 == 0 { 2 } else { 0 });
    assert_eq!(tasks_filed(420, truth), 1);
}

#[test]
fn a_check_hovering_between_warning_and_critical_files_critical() {
    let truth = |m| {
        Truth::Status(if m < 30 {
            0
        } else if (m / 20) % 2 == 0 {
            1
        } else {
            2
        })
    };
    assert_eq!(tasks_filed(240, truth), 1);
}

#[test]
fn a_warning_that_turns_critical_files_both() {
    let truth = |m| {
        Truth::Status(if m < 60 {
            0
        } else if m < 60 * 31 {
            1
        } else {
            2
        })
    };
    assert_eq!(tasks_filed(60 * 34, truth), 2);
}

#[test]
fn an_unreadable_report_rewritten_every_run_files_once() {
    let truth = |m| {
        if (30..210).contains(&m) {
            Truth::Invalid
        } else {
            Truth::Status(0)
        }
    };
    assert_eq!(tasks_filed(240, truth), 1);
}

#[test]
fn a_collector_that_stops_twice_in_a_day_files_twice() {
    let truth = |m| {
        if (60..120).contains(&m) || (240..300).contains(&m) {
            Truth::Silent
        } else {
            Truth::Status(0)
        }
    };
    assert_eq!(tasks_filed(480, truth), 2);
}

fn outage(from: i64, to: i64) -> impl Fn(i64) -> Truth {
    move |m| Truth::Status(if (from..to).contains(&m) { 2 } else { 0 })
}

#[test]
fn a_check_ok_for_over_the_hold_closes_its_task_once() {
    let tracked = replay(300, outage(60, 110), &[]);
    assert_eq!((tracked.filed(), tracked.open()), (1, 0));
    assert_eq!(
        tracked.recoveries.len(),
        1,
        "one recovery, however many scans see it"
    );
    assert!(tracked.recoveries[0] > 110 + 60, "{:?}", tracked.recoveries);
}

#[test]
fn a_check_ok_for_under_the_hold_stays_open() {
    let tracked = replay(165, outage(60, 110), &[]);
    assert_eq!((tracked.filed(), tracked.open()), (1, 1));
}

#[test]
fn a_flapping_check_closes_only_once_it_settles() {
    let flapping = |m| {
        Truth::Status(if (30..300).contains(&m) && m % 40 < 30 {
            2
        } else {
            0
        })
    };
    let settling = replay(300, flapping, &[]);
    assert_eq!((settling.filed(), settling.open()), (1, 1));
    let settled = replay(480, flapping, &[]);
    assert_eq!((settled.filed(), settled.open()), (1, 0));
}

#[test]
fn a_recovery_after_a_restart_still_closes() {
    let tracked = replay(300, outage(60, 110), &[150]);
    assert_eq!((tracked.filed(), tracked.open()), (1, 0));
}

#[test]
fn a_break_after_a_close_files_a_new_task() {
    let twice = |m| {
        Truth::Status(if (60..110).contains(&m) || m >= 200 {
            2
        } else {
            0
        })
    };
    let tracked = replay(260, twice, &[]);
    assert_eq!((tracked.filed(), tracked.open()), (2, 1));
}

#[test]
fn two_outages_in_a_day_each_close() {
    let tracked = replay(
        600,
        |m| {
            Truth::Status(if (60..110).contains(&m) || (240..300).contains(&m) {
                2
            } else {
                0
            })
        },
        &[],
    );
    assert_eq!(
        (tracked.filed(), tracked.open(), tracked.recoveries.len()),
        (2, 0, 2)
    );
}

fn silent(from: i64, to: i64) -> impl Fn(i64) -> Truth {
    move |m| {
        if (from..to).contains(&m) {
            Truth::Silent
        } else {
            Truth::Status(0)
        }
    }
}

#[test]
fn a_collector_back_for_over_the_hold_closes_its_stale_task_once() {
    let tracked = replay(480, silent(60, 120), &[]);
    assert_eq!((tracked.filed(), tracked.open()), (1, 0));
    let after_silence: Vec<_> = tracked.resumes.iter().filter(|&&m| m > 120).collect();
    assert_eq!(after_silence.len(), 1, "{:?}", tracked.resumes);
    assert!(*after_silence[0] > 120 + 60, "{:?}", tracked.resumes);
}

#[test]
fn a_collector_back_for_under_the_hold_stays_stale() {
    let tracked = replay(170, silent(60, 120), &[]);
    assert_eq!((tracked.filed(), tracked.open()), (1, 1));
}

#[test]
fn a_collector_that_stops_twice_in_a_day_closes_both() {
    let twice = |m| {
        if (60..120).contains(&m) || (240..300).contains(&m) {
            Truth::Silent
        } else {
            Truth::Status(0)
        }
    };
    let tracked = replay(480, twice, &[]);
    assert_eq!((tracked.filed(), tracked.open()), (2, 0));
}

#[test]
fn a_collector_that_stutters_back_closes_nothing_until_it_settles() {
    let stutter = |m| {
        if (60..120).contains(&m) || (150..200).contains(&m) {
            Truth::Silent
        } else {
            Truth::Status(0)
        }
    };
    let settling = replay(270, stutter, &[]);
    assert_eq!((settling.filed(), settling.open()), (2, 2));
    let settled = replay(480, stutter, &[]);
    assert_eq!((settled.filed(), settled.open()), (2, 0));
}

#[test]
fn a_resume_after_a_restart_still_closes() {
    let tracked = replay(480, silent(60, 120), &[150]);
    assert_eq!((tracked.filed(), tracked.open()), (1, 0));
}

#[test]
fn an_unreadable_report_that_is_fixed_closes() {
    let tracked = replay(
        480,
        |m| {
            if (60..120).contains(&m) {
                Truth::Invalid
            } else {
                Truth::Status(0)
            }
        },
        &[],
    );
    assert_eq!((tracked.filed(), tracked.open()), (1, 0));
}

#[test]
fn a_recovered_event_resolves_what_its_outage_filed() {
    let healthy = CRITICAL
        .replace("\"status\": 2", "\"status\": 0")
        .replace("2026-11-10T14:50:00Z", "2026-11-10T13:30:00Z");
    let dir = dir_with(&[("mac.json", &healthy)]);
    let found = scan(dir.path());
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].0, "mac/gh-auth/ok/2026-11-10T13:30Z");
    let event = HostFinding::from_json(&found[0].1).unwrap();
    let effects = policy(&facts(&event));
    let Effect::Resolve {
        target,
        item,
        evidence,
    } = &effects[0]
    else {
        panic!("expected Resolve, got {effects:?}");
    };
    assert_eq!(target.to_string(), "task:brined-almanac");
    assert_eq!(
        item,
        &Item::Filed {
            key: "mac.gh-auth".to_string()
        }
    );
    assert_eq!(
        evidence.to_string(),
        "host-state: gh-auth ok on mac since 2026-11-10 13:30Z"
    );
}

#[test]
fn a_finding_and_its_recovery_share_one_run_key() {
    let filed = first_event();
    assert_eq!(filed["outage"], "mac.gh-auth");
}

fn report(generated: &str, status: i64, since: &str) -> String {
    json!({
        "host": "mac",
        "generated_at": generated,
        "interval_seconds": 300,
        "checks": [{"name": "c", "status": status, "since": since, "output": ""}],
    })
    .to_string()
}

fn kinds_at(dir: &Path, now: OffsetDateTime, episodes: &mut Episodes) -> Vec<String> {
    let previous = Some(now - Duration::minutes(15));
    scan_findings(&hosts(), dir, now, previous, episodes)
        .into_iter()
        .map(|(_, payload)| payload["kind"].as_str().unwrap().to_string())
        .filter(|kind| kind != "resumed")
        .collect()
}

#[test]
fn a_lagging_copy_holds_the_recovery_until_the_last_broken_scan_is_an_hour_old() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("mac.json");
    let mut episodes = Episodes::default();
    let at = |hh: u8, mm: u8| {
        datetime!(2026-11-11 00:00 UTC) + Duration::minutes(i64::from(hh) * 60 + i64::from(mm))
    };
    std::fs::write(
        &path,
        report("2026-11-11T13:35:00Z", 2, "2026-11-11T12:00:00Z"),
    )
    .unwrap();
    assert_eq!(kinds_at(dir.path(), at(14, 0), &mut episodes), ["check"]);
    std::fs::write(
        &path,
        report("2026-11-11T14:40:00Z", 0, "2026-11-11T13:40:00Z"),
    )
    .unwrap();
    assert_eq!(
        kinds_at(dir.path(), at(14, 45), &mut episodes),
        Vec::<String>::new(),
        "ok for over an hour, but the 14:00 scan saw it broken, so a break now would continue that outage"
    );
    std::fs::write(
        &path,
        report("2026-11-11T15:00:00Z", 0, "2026-11-11T13:40:00Z"),
    )
    .unwrap();
    assert_eq!(
        kinds_at(dir.path(), at(15, 5), &mut episodes),
        ["recovered"]
    );
}

#[test]
fn a_recovery_that_is_not_ok_is_malformed() {
    let mut payload = first_event();
    payload["kind"] = json!("recovered");
    let err = HostFinding::from_json(&payload).unwrap_err();
    assert_eq!(err.pointer, "/status");
}

#[test]
fn a_resumed_event_resolves_what_the_hosts_stale_outages_filed() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("mac.json");
    let mut episodes = Episodes::default();
    let first = datetime!(2026-11-11 14:00 UTC);
    let resume = |generated: &str, now: OffsetDateTime, episodes: &mut Episodes| {
        std::fs::write(&path, report(generated, 0, "2026-11-11T09:00:00Z")).unwrap();
        scan_findings(
            &hosts(),
            dir.path(),
            now,
            Some(now - Duration::minutes(15)),
            episodes,
        )
        .into_iter()
        .filter(|(_, payload)| payload["kind"] == "resumed")
        .collect::<Vec<_>>()
    };
    assert!(resume("2026-11-11T13:55:00Z", first, &mut episodes).is_empty());
    assert!(
        resume(
            "2026-11-11T14:55:00Z",
            first + Duration::minutes(60),
            &mut episodes
        )
        .is_empty(),
        "current for exactly the hold is not over it"
    );
    let found = resume(
        "2026-11-11T15:00:00Z",
        first + Duration::minutes(61),
        &mut episodes,
    );
    assert_eq!(found.len(), 1, "{found:?}");
    assert_eq!(found[0].0, "mac/stale/ok/2026-11-11T14:00Z");
    let event = HostFinding::from_json(&found[0].1).unwrap();
    let effects = policy(&facts(&event));
    let Effect::Resolve { item, evidence, .. } = &effects[0] else {
        panic!("expected Resolve, got {effects:?}");
    };
    assert_eq!(
        item,
        &Item::Filed {
            key: "mac.stale".to_string()
        }
    );
    assert_eq!(
        evidence.to_string(),
        "host-state: reports from mac current since 2026-11-11 14:00Z"
    );
}

#[test]
fn a_resumed_event_naming_a_check_is_malformed() {
    let mut payload = first_event();
    payload["kind"] = json!("resumed");
    payload["status"] = json!("ok");
    let err = HostFinding::from_json(&payload).unwrap_err();
    assert_eq!(err.pointer, "/check");
}

#[test]
fn a_finding_from_before_resolve_has_no_outage_so_it_is_malformed() {
    let mut payload = first_event();
    payload.as_object_mut().unwrap().remove("outage");
    let err = HostFinding::from_json(&payload).unwrap_err();
    assert_eq!(err.pointer, "/outage");
}
