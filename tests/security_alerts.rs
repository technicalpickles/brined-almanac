use std::sync::Arc;

use almanac::diag::Error;
use almanac::prelude::*;
use almanac::runtime::{Envelope, Ingest, Origin, Runtime};
use almanac::testing::FakeTracker;
use brined_almanac::host_state::Hosts;
use brined_almanac::routing::Project;
use brined_almanac::tiers::Tiers;
use brined_almanac::workflows;
use brined_almanac::workflows::host_state::HostReports;
use brined_almanac::workflows::security_alerts::{
    Alert, AlertsClient, Coverage, Facts, RepoRead, SecurityFinding, Severity, Tick, alerts_switch,
    digest, facts, findings, overdue, parse_dependabot, parse_secret, policy, repo_missing,
    secret_alerts,
};
use serde_json::{Value, json};
use time::Date;
use time::macros::date;

const DEPENDABOT: &str = include_str!("fixtures/security_alerts/dependabot_alert.json");
const SECRET: &str = include_str!("fixtures/security_alerts/secret_alert.json");

fn dependency(severity: Severity, created: Date) -> Alert {
    Alert::Dependency {
        number: 1,
        severity,
        package: "toml".into(),
        manifest: "package-lock.json".into(),
        ghsa: "GHSA-aaaa-bbbb-cccc".into(),
        patched: Some("5.0.1".into()),
        created,
        url: "https://example.test/1".into(),
    }
}

fn secret(created: Date) -> Alert {
    Alert::Secret {
        number: 2,
        kind: "Token".into(),
        created,
        url: "https://example.test/2".into(),
    }
}

fn fixture(raw: &str) -> Value {
    serde_json::from_str(raw).expect("fixture json")
}

#[test]
fn windows_match_the_spec() {
    let day = date!(2026 - 11 - 08);
    assert_eq!(secret(day).window_days(), 0);
    assert_eq!(dependency(Severity::Critical, day).window_days(), 2);
    assert_eq!(dependency(Severity::High, day).window_days(), 14);
    assert_eq!(dependency(Severity::Medium, day).window_days(), 30);
    assert_eq!(dependency(Severity::Unknown, day).window_days(), 30);
    assert_eq!(dependency(Severity::Low, day).window_days(), 90);
}

#[test]
fn overdue_starts_on_the_last_day_of_the_window() {
    let high = [dependency(Severity::High, date!(2026 - 10 - 25))];
    assert!(overdue(&high, date!(2026 - 11 - 07)).is_empty());
    assert_eq!(overdue(&high, date!(2026 - 11 - 08)).len(), 1);

    let today = date!(2026 - 11 - 08);
    assert_eq!(overdue(&[secret(today)], today).len(), 1);
}

#[test]
fn age_uses_the_utc_date() {
    let mut v = fixture(DEPENDABOT);
    v["created_at"] = json!("2026-10-25T23:30:00-05:00");
    match parse_dependabot(&v).expect("parse") {
        Alert::Dependency { created, .. } => assert_eq!(created, date!(2026 - 10 - 26)),
        other => panic!("not a dependency alert: {other:?}"),
    }
}

#[test]
fn digest_ignores_order_and_changes_with_the_set() {
    let a = vec!["dependabot:1".to_string(), "secret:2".to_string()];
    let b = vec!["secret:2".to_string(), "dependabot:1".to_string()];
    let more = vec![
        "dependabot:1".to_string(),
        "secret:2".to_string(),
        "dependabot:3".to_string(),
    ];
    assert_eq!(digest(&a), digest(&b));
    assert_eq!(digest(&a).len(), 12);
    assert!(digest(&a).chars().all(|c| c.is_ascii_hexdigit()));
    assert_ne!(digest(&a), digest(&more));
}

#[test]
fn an_unknown_severity_is_unknown() {
    assert_eq!(Severity::from_github("moderate"), Severity::Unknown);
    assert_eq!(Severity::from_github("high"), Severity::High);
}

#[test]
fn a_missing_patched_version_says_no_fix_yet() {
    let mut v = fixture(DEPENDABOT);
    v["security_vulnerability"]["first_patched_version"] = Value::Null;
    let line = parse_dependabot(&v)
        .expect("parse")
        .line(date!(2026 - 11 - 08));
    assert!(line.contains("no fix yet"), "{line}");
}

#[test]
fn a_recorded_dependabot_alert_reads_into_a_line() {
    let alert = parse_dependabot(&fixture(DEPENDABOT)).expect("parse");
    assert_eq!(alert.key(), "dependabot:37");
    assert_eq!(
        alert.line(date!(2026 - 11 - 08)),
        "#37 high toml (package-lock.json) GHSA-4qx8-w7c3-pj3p, fixed in 5.0.1, 13 days, \
         https://github.com/technicalpickles/dotfiles/security/dependabot/37"
    );
}

#[test]
fn a_secret_value_never_reaches_the_line() {
    let raw = fixture(SECRET);
    assert_eq!(raw["secret"], "ghp_DO_NOT_LEAK");
    let alert = parse_secret(&raw).expect("parse");
    assert!(!format!("{alert:?}").contains("ghp_DO_NOT_LEAK"));
    let line = alert.line(date!(2026 - 11 - 08));
    assert!(!line.contains("ghp_DO_NOT_LEAK"), "{line}");
    assert_eq!(
        line,
        "#4 secret GitHub Personal Access Token, 1 days, \
         https://github.com/technicalpickles/example/security/secret-scanning/4"
    );
}

#[test]
fn a_secret_without_a_display_name_falls_back_to_its_type() {
    let mut v = fixture(SECRET);
    v.as_object_mut()
        .unwrap()
        .remove("secret_type_display_name");
    let line = parse_secret(&v).expect("parse").line(date!(2026 - 11 - 08));
    assert!(line.contains("github_personal_access_token"), "{line}");
}

#[test]
fn nothing_overdue_is_empty() {
    let today = date!(2026 - 11 - 08);
    let alerts = [
        dependency(Severity::Critical, date!(2026 - 11 - 07)),
        dependency(Severity::Medium, date!(2026 - 10 - 20)),
        dependency(Severity::Low, date!(2026 - 08 - 20)),
    ];
    assert!(overdue(&alerts, today).is_empty());
}

fn repo() -> Slug {
    "technicalpickles/example".parse().unwrap()
}

fn refused(status: u16) -> Error {
    Error::ForgeRefused {
        action: "GET /repos/technicalpickles/example/x".into(),
        status,
    }
}

#[test]
fn vulnerability_alerts_404_is_off() {
    assert!(!alerts_switch(Err(refused(404)), &repo()).unwrap());
}

#[test]
fn vulnerability_alerts_empty_204_is_on() {
    assert!(alerts_switch(Ok(String::new()), &repo()).unwrap());
}

#[test]
fn vulnerability_alerts_403_is_an_error_naming_the_repo() {
    let err = alerts_switch(Err(refused(403)), &repo()).unwrap_err();
    assert!(err.0.contains("technicalpickles/example"), "{}", err.0);
    assert!(err.0.contains("403"), "{}", err.0);
}

#[test]
fn secret_scanning_404_is_off() {
    let got = secret_alerts(Err(refused(404)), &repo()).unwrap();
    assert!(matches!(got, Coverage::Off));
}

#[test]
fn secret_scanning_401_is_an_error_naming_the_repo() {
    let err = secret_alerts(Err(refused(401)), &repo()).unwrap_err();
    assert!(err.0.contains("technicalpickles/example"), "{}", err.0);
    assert!(err.0.contains("401"), "{}", err.0);
}

#[test]
fn secret_scanning_alerts_parse_into_on() {
    let got = secret_alerts(Ok(vec![fixture(SECRET)]), &repo()).unwrap();
    match got {
        Coverage::On(alerts) => assert_eq!(alerts.len(), 1),
        Coverage::Off => panic!("expected On"),
    }
}

#[test]
fn a_missing_repository_names_both_tiers_files() {
    let msg = repo_missing(refused(404), &repo()).0;
    assert!(msg.contains("technicalpickles/example"), "{msg}");
    assert!(
        msg.contains("check tiers.toml and local/tiers.toml"),
        "{msg}"
    );
}

#[test]
fn another_repository_failure_keeps_the_status() {
    let msg = repo_missing(refused(500), &repo()).0;
    assert!(msg.contains("technicalpickles/example"), "{msg}");
    assert!(msg.contains("500"), "{msg}");
}

const OVERDUE_EVENT: &str = include_str!("fixtures/security_alerts/overdue_event.json");
const COVERAGE_EVENT: &str = include_str!("fixtures/security_alerts/coverage_event.json");

fn slug(raw: &str) -> Slug {
    raw.parse().expect("slug")
}

fn numbered(alert: Alert, number: u64) -> Alert {
    match alert {
        Alert::Dependency {
            severity,
            package,
            manifest,
            ghsa,
            patched,
            created,
            url,
            ..
        } => Alert::Dependency {
            number,
            severity,
            package,
            manifest,
            ghsa,
            patched,
            created,
            url,
        },
        other => other,
    }
}

fn read(private: bool, dependabot: Coverage<Alert>, secrets: Coverage<Alert>) -> RepoRead {
    RepoRead {
        private,
        dependabot,
        secrets,
    }
}

fn clean_read() -> RepoRead {
    read(false, Coverage::On(vec![]), Coverage::On(vec![]))
}

const TODAY: Date = date!(2026 - 11 - 08);

#[test]
fn findings_files_one_task_per_repository_with_overdue_alerts() {
    let overdue_high = numbered(dependency(Severity::High, date!(2026 - 10 - 01)), 7);
    let fresh_high = numbered(dependency(Severity::High, date!(2026 - 11 - 07)), 8);
    let reads = vec![
        (
            slug("technicalpickles/dotfiles"),
            read(
                false,
                Coverage::On(vec![overdue_high.clone(), fresh_high]),
                Coverage::On(vec![]),
            ),
        ),
        (slug("technicalpickles/quiet"), clean_read()),
    ];
    let found = findings(&reads, TODAY).unwrap();
    assert_eq!(found.len(), 1);
    let expected = format!(
        "scan:security-alerts/technicalpickles/dotfiles@{}",
        digest(&[overdue_high.key()])
    );
    assert_eq!(found[0].0, expected);
    let event = SecurityFinding::from_json(&found[0].1).expect("payload parses");
    assert_eq!(event.project.to_string(), "dotfiles");
    assert_eq!(event.subject.to_string(), "technicalpickles/dotfiles");
    assert_eq!(event.high, 1);
}

#[test]
fn a_repository_whose_overdue_alerts_were_all_fixed_files_nothing() {
    let fresh = numbered(dependency(Severity::High, date!(2026 - 11 - 07)), 8);
    let reads = vec![(
        slug("technicalpickles/dotfiles"),
        read(false, Coverage::On(vec![fresh]), Coverage::On(vec![])),
    )];
    assert!(findings(&reads, TODAY).unwrap().is_empty());
}

#[test]
fn findings_lists_coverage_gaps_once() {
    let reads = vec![
        (
            slug("technicalpickles/example-private"),
            read(true, Coverage::Off, Coverage::Off),
        ),
        (
            slug("technicalpickles/example-public"),
            read(false, Coverage::On(vec![]), Coverage::Off),
        ),
    ];
    let found = findings(&reads, TODAY).unwrap();
    assert_eq!(found.len(), 1);
    let keys = [
        "dependabot-off:technicalpickles/example-private".to_string(),
        "secret-scanning-off:technicalpickles/example-public".to_string(),
    ];
    assert_eq!(
        found[0].0,
        format!("scan:security-alerts/coverage@{}", digest(&keys))
    );
    let event = SecurityFinding::from_json(&found[0].1).expect("payload parses");
    assert_eq!(event.kind.to_string(), "coverage");
    assert_eq!(event.subject.to_string(), "coverage");
    assert_eq!(event.project.to_string(), "brined-almanac");
    assert_eq!(event.dependabot_off, 1);
    assert_eq!(event.secret_scanning_off, 1);
}

#[test]
fn no_gaps_and_nothing_overdue_is_no_findings() {
    let reads = vec![
        (slug("technicalpickles/dotfiles"), clean_read()),
        (
            slug("technicalpickles/example-private"),
            read(true, Coverage::On(vec![]), Coverage::Off),
        ),
    ];
    assert!(findings(&reads, TODAY).unwrap().is_empty());
}

#[test]
fn overdue_secrets_and_alert_lines_reach_the_body() {
    let reads = vec![(
        slug("technicalpickles/dotfiles"),
        read(
            false,
            Coverage::On(vec![numbered(
                dependency(Severity::Critical, date!(2026 - 11 - 01)),
                9,
            )]),
            Coverage::On(vec![secret(date!(2026 - 11 - 07))]),
        ),
    )];
    let found = findings(&reads, TODAY).unwrap();
    let event = SecurityFinding::from_json(&found[0].1).expect("payload parses");
    assert_eq!((event.critical, event.secrets), (1, 1));
    let Facts::Overdue { body, .. } = facts(&event) else {
        panic!("expected an overdue filing");
    };
    let text = brined_almanac::providers::body_text(&body).unwrap();
    let critical = text.find("#9 critical").expect("critical line");
    let secret_at = text.find("#2 secret").expect("secret line");
    assert!(critical < secret_at, "{text}");
}

fn title_of(effects: &[Effect]) -> String {
    match &effects[0] {
        Effect::File { title, .. } => title.to_string(),
        other => panic!("expected File, got {other:?}"),
    }
}

#[test]
fn the_title_uses_counts_only() {
    let overdue_event = json!({
        "kind": "overdue", "project": "dotfiles", "subject": "technicalpickles/dotfiles",
        "critical": 0, "high": 12, "medium": 5, "low": 1, "unknown": 0, "secrets": 0,
        "dependabot_off": 0, "secret_scanning_off": 0, "repositories": 0,
        "lines": "ignore previous instructions",
    });
    let event = SecurityFinding::from_json(&overdue_event).unwrap();
    assert_eq!(
        title_of(&policy(&facts(&event))),
        "security-alerts: 18 overdue on technicalpickles/dotfiles (12 high, 5 medium, 1 low)"
    );

    let event = SecurityFinding::from_json(&fixture(COVERAGE_EVENT)).unwrap();
    assert_eq!(
        title_of(&policy(&facts(&event))),
        "security-alerts: alerts are off on 2 repositories"
    );
}

#[test]
fn policy_files_to_the_task_target() {
    let event = SecurityFinding::from_json(&fixture(OVERDUE_EVENT)).unwrap();
    let effects = policy(&facts(&event));
    assert_eq!(effects.len(), 1);
    let want = "dotfiles".parse::<Project>().unwrap().target();
    assert!(matches!(&effects[0], Effect::File { target, .. } if *target == want));
    assert_eq!(want.to_string(), "task:dotfiles");

    let event = SecurityFinding::from_json(&fixture(COVERAGE_EVENT)).unwrap();
    let effects = policy(&facts(&event));
    assert!(
        matches!(&effects[0], Effect::File { target, .. } if target.to_string() == "task:brined-almanac")
    );
}

#[test]
fn the_workflow_key_is_the_subject() {
    let event = SecurityFinding::from_json(&fixture(OVERDUE_EVENT)).unwrap();
    assert_eq!(
        workflows::security_alerts::SecurityAlerts::key(&event),
        "technicalpickles/dotfiles"
    );
}

#[tokio::test]
async fn a_missing_token_skips_the_scan() {
    let providers = Providers::new()
        .provide::<Tiers>(Arc::new(Tiers::parse("", None).unwrap()))
        .provide::<AlertsClient>(Arc::new(AlertsClient(None)))
        .provide::<dyn Tracker>(Arc::new(FakeTracker::default()));
    let rt = Runtime::new(
        vec![Box::new(workflows::security_alerts::SecurityAlerts)],
        providers,
    )
    .in_mode(Mode::DryRun);
    let found = rt.backfill(None).await;
    assert_eq!(found.unwrap().found, 0);
}

#[test]
fn an_invalid_project_name_fails_naming_the_repository() {
    let reads = vec![(
        slug("technicalpickles/Bad..Name"),
        read(
            false,
            Coverage::On(vec![numbered(
                dependency(Severity::High, date!(2026 - 10 - 01)),
                7,
            )]),
            Coverage::On(vec![]),
        ),
    )];
    let err = findings(&reads, TODAY).unwrap_err().0;
    assert!(err.contains("technicalpickles/Bad..Name"), "{err}");
}

fn overdue_and_off() -> RepoRead {
    read(
        false,
        Coverage::On(vec![numbered(
            dependency(Severity::High, date!(2026 - 10 - 01)),
            7,
        )]),
        Coverage::Off,
    )
}

#[test]
fn a_failing_repository_is_skipped_and_holds_the_coverage_task() {
    let tick = Tick::new(vec![
        (slug("technicalpickles/dotfiles"), Ok(overdue_and_off())),
        (
            slug("technicalpickles/broken"),
            Err(StepError("technicalpickles/broken: HTTP 502".to_string())),
        ),
    ]);
    assert_eq!(tick.failed.len(), 1);
    assert!(tick.failed[0].0.contains("technicalpickles/broken"));
    let found = tick.findings(TODAY).unwrap();
    assert_eq!(found.len(), 1, "{found:?}");
    assert!(
        found[0]
            .0
            .starts_with("scan:security-alerts/technicalpickles/dotfiles@")
    );
}

#[test]
fn a_full_read_files_the_coverage_task() {
    let tick = Tick::new(vec![(
        slug("technicalpickles/dotfiles"),
        Ok(overdue_and_off()),
    )]);
    let found = tick.findings(TODAY).unwrap();
    assert_eq!(found.len(), 2, "{found:?}");
    assert!(found[1].0.starts_with("scan:security-alerts/coverage@"));
}

#[test]
fn every_repository_failing_fails_the_scan() {
    let tick = Tick::new(vec![
        (
            slug("technicalpickles/a"),
            Err(StepError("technicalpickles/a: HTTP 401".to_string())),
        ),
        (
            slug("technicalpickles/b"),
            Err(StepError("technicalpickles/b: HTTP 401".to_string())),
        ),
    ]);
    let err = tick.findings(TODAY).unwrap_err().0;
    assert!(err.contains("technicalpickles/a"), "{err}");
    assert!(err.contains("technicalpickles/b"), "{err}");
}

#[test]
fn an_unknown_kind_or_project_is_malformed() {
    for (field, value) in [("kind", "other"), ("project", "Not A Project")] {
        let mut payload = fixture(OVERDUE_EVENT);
        payload[field] = json!(value);
        assert!(SecurityFinding::from_json(&payload).is_err(), "{field}");
    }
}

#[test]
fn the_coverage_title_pluralizes() {
    let mut payload = fixture(COVERAGE_EVENT);
    payload["repositories"] = json!(1);
    let event = SecurityFinding::from_json(&payload).unwrap();
    assert_eq!(
        title_of(&policy(&facts(&event))),
        "security-alerts: alerts are off on 1 repository"
    );
}

#[tokio::test]
async fn the_runtime_files_once_per_delivery() {
    let tracker = Arc::new(FakeTracker::default());
    let providers = Providers::new()
        .provide::<Tiers>(Arc::new(Tiers::parse("", None).unwrap()))
        .provide::<AlertsClient>(Arc::new(AlertsClient(None)))
        .provide::<Hosts>(Arc::new(Hosts::load().unwrap()))
        .provide::<HostReports>(Arc::new(HostReports::new(None)))
        .provide::<dyn Tracker>(tracker.clone());
    let rt = Runtime::new(workflows::all(), providers).in_mode(Mode::Live);
    let envelope = || Envelope {
        source: "scan".to_string(),
        delivery_id: "scan:security-alerts/technicalpickles/dotfiles@0123456789ab".to_string(),
        kind: "security.alert.finding".to_string(),
        depth: 0,
        origin: Origin::Scan,
        payload: fixture(OVERDUE_EVENT),
    };
    assert_eq!(rt.ingest(envelope()).await.unwrap(), Ingest::Queued);
    assert_eq!(rt.ingest(envelope()).await.unwrap(), Ingest::Duplicate);
    rt.settle().await.unwrap();
    let filed = tracker.filed.lock().unwrap();
    assert_eq!(filed.len(), 1);
    assert_eq!(filed[0].1, "task:dotfiles");
}
