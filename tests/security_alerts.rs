use brined_almanac::workflows::security_alerts::{
    Alert, Severity, digest, overdue, parse_dependabot, parse_secret,
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
