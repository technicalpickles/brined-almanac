use std::path::Path;

use brined_almanac::host_state::{
    CRITICAL_AFTER, CheckResult, Finding, Host, Hosts, Read, Report, STALE_AFTER, Status,
    WARNING_AFTER, findings, read,
};
use time::macros::datetime;
use time::{Duration, OffsetDateTime};

const SPEC_EXAMPLE: &str = include_str!("fixtures/host_state/mac.json");

fn report_with(checks: &str) -> String {
    format!(
        r#"{{"host":"mac","generated_at":"2026-11-10T15:00:00Z","interval_seconds":300,"checks":{checks}}}"#
    )
}

fn check_json(name: &str, status: &str, output: &str) -> String {
    format!(
        r#"{{"name":"{name}","status":{status},"since":"2026-11-10T14:50:00Z","output":"{output}"}}"#
    )
}

#[test]
fn parses_the_spec_example() {
    let report = Report::parse(SPEC_EXAMPLE).unwrap();
    assert_eq!(report.host, "mac");
    assert_eq!(report.checks.len(), 1);
    assert_eq!(report.checks[0].status, Status::Critical);
    assert_eq!(report.checks[0].since, datetime!(2026-11-10 14:50 UTC));
}

#[test]
fn rejects_a_status_outside_0_to_3() {
    let err = Report::parse(&report_with(&format!(
        "[{}]",
        check_json("gh-auth", "4", "x")
    )))
    .unwrap_err();
    assert!(err.contains("status"), "{err}");
}

#[test]
fn rejects_a_check_name_outside_the_alphabet() {
    let err = Report::parse(&report_with(&format!(
        "[{}]",
        check_json("GH Auth", "0", "x")
    )))
    .unwrap_err();
    assert!(err.contains("GH Auth"), "{err}");
}

#[test]
fn rejects_a_duplicate_check_name() {
    let two = format!(
        "[{},{}]",
        check_json("gh-auth", "0", "x"),
        check_json("gh-auth", "2", "y")
    );
    let err = Report::parse(&report_with(&two)).unwrap_err();
    assert!(err.contains("twice"), "{err}");
}

#[test]
fn an_empty_check_list_is_valid() {
    let report = Report::parse(&report_with("[]")).unwrap();
    assert!(report.checks.is_empty());
}

#[test]
fn rejects_unknown_fields() {
    let text = SPEC_EXAMPLE.replacen('{', r#"{"extra": 1,"#, 1);
    assert!(Report::parse(&text).is_err());
}

#[test]
fn rejects_a_time_without_an_offset() {
    let text = SPEC_EXAMPLE.replace("2026-11-10T15:00:00Z", "2026-11-10 15:00");
    let err = Report::parse(&text).unwrap_err();
    assert!(err.contains("generated_at"), "{err}");
}

#[test]
fn cuts_output_to_500_bytes_on_a_char_boundary() {
    // 499 ASCII bytes then a 3-byte character straddling the limit.
    let output = format!("{}€{}", "a".repeat(499), "b".repeat(100));
    assert!(output.len() > 500);
    let report = Report::parse(&report_with(&format!(
        "[{}]",
        check_json("gh-auth", "2", &output)
    )))
    .unwrap();
    let cut = &report.checks[0].output;
    assert!(cut.len() <= 500, "{}", cut.len());
    assert!(std::str::from_utf8(cut.as_bytes()).is_ok());
}

#[test]
fn the_committed_hosts_file_declares_the_mac() {
    let hosts = Hosts::load().unwrap();
    let all: Vec<&Host> = hosts.iter().collect();
    assert_eq!(all.len(), 1);
    assert_eq!(all[0].name, "mac");
    assert_eq!(all[0].project.as_str(), "brined-almanac");
}

#[test]
fn rejects_a_host_declared_twice() {
    let text = r#"
[[host]]
name = "mac"
project = "brined-almanac"

[[host]]
name = "mac"
project = "brined-almanac"
"#;
    let err = Hosts::parse(text).unwrap_err();
    assert!(err.contains("twice"), "{err}");
}

#[test]
fn rejects_a_project_name_taskwarrior_would_refuse() {
    let text = "[[host]]\nname = \"mac\"\nproject = \"Bad Name\"\n";
    let err = Hosts::parse(text).unwrap_err();
    assert!(err.contains("mac"), "{err}");
}

#[test]
fn rejects_a_host_name_that_is_a_path() {
    let text = "[[host]]\nname = \"../x\"\nproject = \"brined-almanac\"\n";
    assert!(Hosts::parse(text).is_err());
}

const NOW: OffsetDateTime = datetime!(2026-11-10 15:00 UTC);

fn mac() -> Host {
    Hosts::load().unwrap().iter().next().unwrap().clone()
}

fn report(generated_at: OffsetDateTime, status: Status, since: OffsetDateTime) -> Read {
    Read::Report(Report {
        host: "mac".into(),
        generated_at,
        interval_seconds: 300,
        checks: vec![CheckResult {
            name: "gh-auth".into(),
            status,
            since,
            output: "GH CRITICAL - nope".into(),
        }],
    })
}

fn fresh(status: Status, since: OffsetDateTime) -> Vec<Finding> {
    findings(
        &mac(),
        &report(datetime!(2026-11-10 14:59 UTC), status, since),
        NOW,
    )
}

#[test]
fn the_thresholds_are_the_documented_ones() {
    assert_eq!(CRITICAL_AFTER, Duration::minutes(10));
    assert_eq!(WARNING_AFTER, Duration::hours(24));
    assert_eq!(STALE_AFTER, Duration::minutes(30));
}

#[test]
fn a_critical_check_files_after_ten_minutes() {
    let found = fresh(Status::Critical, datetime!(2026-11-10 14:50 UTC));
    assert_eq!(found.len(), 1);
    assert!(matches!(found[0], Finding::Check { .. }));
    assert_eq!(found[0].key(), "mac/gh-auth/2026-11-10");
}

#[test]
fn a_critical_check_under_ten_minutes_files_nothing() {
    assert!(fresh(Status::Critical, datetime!(2026-11-10 14:51 UTC)).is_empty());
}

#[test]
fn unknown_files_like_critical() {
    let found = fresh(Status::Unknown, datetime!(2026-11-10 14:50 UTC));
    assert_eq!(found.len(), 1);
    assert!(matches!(found[0], Finding::Check { .. }));
}

#[test]
fn a_warning_files_after_a_day() {
    let found = fresh(Status::Warning, datetime!(2026-11-09 15:00 UTC));
    assert_eq!(found.len(), 1);
    assert_eq!(found[0].key(), "mac/gh-auth/2026-11-09");
}

#[test]
fn a_warning_under_a_day_files_nothing() {
    assert!(fresh(Status::Warning, datetime!(2026-11-09 15:01 UTC)).is_empty());
}

#[test]
fn an_ok_check_files_nothing() {
    assert!(fresh(Status::Ok, datetime!(2026-11-01 00:00 UTC)).is_empty());
}

#[test]
fn a_long_outage_keeps_one_key() {
    let since = datetime!(2026-11-08 23:00 UTC);
    let early = findings(
        &mac(),
        &report(datetime!(2026-11-09 00:59 UTC), Status::Critical, since),
        datetime!(2026-11-09 01:00 UTC),
    );
    let late = fresh(Status::Critical, since);
    assert_eq!(early[0].key(), "mac/gh-auth/2026-11-08");
    assert_eq!(late[0].key(), "mac/gh-auth/2026-11-08");
}

#[test]
fn a_stale_report_files_stale_and_no_checks() {
    let generated = datetime!(2026-11-10 14:29 UTC);
    let found = findings(
        &mac(),
        &report(generated, Status::Critical, datetime!(2026-11-10 14:00 UTC)),
        NOW,
    );
    assert_eq!(found.len(), 1);
    assert!(matches!(
        found[0],
        Finding::Stale { since: Some(s), .. } if s == generated
    ));
    assert_eq!(found[0].key(), "mac/stale/2026-11-10");
}

#[test]
fn a_report_at_thirty_minutes_is_not_stale() {
    let found = findings(
        &mac(),
        &report(
            datetime!(2026-11-10 14:30 UTC),
            Status::Ok,
            datetime!(2026-11-10 14:00 UTC),
        ),
        NOW,
    );
    assert!(found.iter().all(|f| !matches!(f, Finding::Stale { .. })));
}

#[test]
fn a_missing_report_is_stale() {
    let found = findings(&mac(), &Read::Missing, NOW);
    assert_eq!(found.len(), 1);
    assert!(matches!(found[0], Finding::Stale { since: None, .. }));
    assert_eq!(found[0].key(), "mac/stale/missing");
}

#[test]
fn an_invalid_report_is_stale_with_the_error() {
    let read = Read::Invalid {
        modified: datetime!(2026-11-10 14:00 UTC),
        error: "x".into(),
    };
    let found = findings(&mac(), &read, NOW);
    assert_eq!(found.len(), 1);
    assert!(matches!(found[0], Finding::Stale { .. }));
    assert_eq!(found[0].key(), "mac/stale/2026-11-10");
    assert!(found[0].body().contains('x'));
}

#[test]
fn a_since_in_the_future_files_nothing() {
    assert!(fresh(Status::Critical, datetime!(2026-11-10 15:30 UTC)).is_empty());
}

#[test]
fn a_generated_at_in_the_future_is_fresh() {
    let found = findings(
        &mac(),
        &report(
            datetime!(2026-11-10 15:30 UTC),
            Status::Ok,
            datetime!(2026-11-10 14:00 UTC),
        ),
        NOW,
    );
    assert!(found.iter().all(|f| !matches!(f, Finding::Stale { .. })));
}

#[test]
fn the_title_names_check_status_host_and_time() {
    let found = fresh(Status::Critical, datetime!(2026-11-10 14:50 UTC));
    assert_eq!(
        found[0].title(),
        "host-state: gh-auth critical on mac since 2026-11-10 14:50Z"
    );
}

#[test]
fn the_stale_title_names_the_host_and_time() {
    let found = findings(
        &mac(),
        &report(
            datetime!(2026-11-10 14:29 UTC),
            Status::Critical,
            datetime!(2026-11-10 14:00 UTC),
        ),
        NOW,
    );
    assert_eq!(
        found[0].title(),
        "host-state: no report from mac since 2026-11-10 14:29Z"
    );
}

#[test]
fn a_check_body_is_output_status_and_since() {
    let found = fresh(Status::Critical, datetime!(2026-11-10 14:50 UTC));
    assert_eq!(
        found[0].body(),
        "GH CRITICAL - nope\nstatus: critical\nsince: 2026-11-10T14:50:00Z"
    );
}

fn dir_with(file: Option<&str>) -> tempfile::TempDir {
    let dir = tempfile::tempdir().unwrap();
    if let Some(text) = file {
        std::fs::write(dir.path().join("mac.json"), text).unwrap();
    }
    dir
}

fn read_in(dir: &Path) -> Read {
    read(dir, &mac())
}

#[test]
fn reads_a_valid_report() {
    let dir = dir_with(Some(SPEC_EXAMPLE));
    assert!(matches!(read_in(dir.path()), Read::Report(_)));
}

#[test]
fn a_missing_file_is_missing() {
    let dir = dir_with(None);
    assert_eq!(read_in(dir.path()), Read::Missing);
}

#[test]
fn a_truncated_file_is_invalid() {
    let dir = dir_with(Some(r#"{"host":"#));
    assert!(matches!(read_in(dir.path()), Read::Invalid { .. }));
}

#[test]
fn a_report_naming_another_host_is_invalid() {
    let dir = dir_with(Some(&SPEC_EXAMPLE.replace("\"mac\"", "\"lab\"")));
    match read_in(dir.path()) {
        Read::Invalid { error, .. } => {
            assert!(error.contains("lab") && error.contains("mac"), "{error}");
        }
        other => panic!("{other:?}"),
    }
}
