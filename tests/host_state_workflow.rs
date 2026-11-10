use std::path::Path;
use std::sync::Arc;

use almanac::prelude::*;
use almanac::runtime::Runtime;
use almanac::testing::FakeTracker;
use brined_almanac::bugsink::BugsinkClient;
use brined_almanac::host_state::{Finding, Hosts, Status};
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
    scan_findings(&hosts(), dir, NOW, previous)
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
    assert_eq!(found[0].0, "mac/gh-auth/2026-11-10");
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
    assert_eq!(found[0].0, "mac/gh-auth/2026-11-10");
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
    assert_eq!(found[0].0, "mac/stale/2026-11-10");
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
        .provide::<Hosts>(Arc::new(Hosts::load().unwrap()))
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
