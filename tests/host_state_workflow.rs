use std::path::Path;

use almanac::prelude::*;
use brined_almanac::host_state::{Finding, Hosts, Status};
use brined_almanac::providers::host_reports_from;
use brined_almanac::workflows::host_state::{HostFinding, facts, policy, scan_findings};
use serde_json::{Value, json};
use time::macros::datetime;

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

fn scan(dir: &Path) -> Vec<(String, Value)> {
    scan_findings(&hosts(), dir, datetime!(2026-11-10 15:00 UTC))
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
    assert_eq!(host_reports_from(|_| None).0, None);
    assert_eq!(host_reports_from(|_| Some(String::new())).0, None);
    assert_eq!(
        host_reports_from(|_| Some("/host-state".to_string())).0,
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
