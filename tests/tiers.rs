use std::path::Path;

use almanac::prelude::*;
use brined_almanac::tiers::{Tier, Tiers, WATCHED_BY_SECURITY_ALERTS};

fn slug(s: &str) -> Slug {
    s.parse().unwrap()
}

const PUBLIC_A: &str = r#"
[[repo]]
name = "technicalpickles/a"
tiers = ["active"]
"#;

#[test]
fn parses_declarations_from_both_files() {
    let local = r#"
[[repo]]
name = "technicalpickles/b"
tiers = ["deployed"]
signal = "runs somewhere"
note = "a note"
"#;
    let tiers = Tiers::parse(PUBLIC_A, Some(local)).unwrap();
    let watched = tiers.watched(&[Tier::Active, Tier::Deployed]);
    assert_eq!(
        watched,
        vec![&slug("technicalpickles/a"), &slug("technicalpickles/b")]
    );
}

#[test]
fn a_repository_in_both_files_is_an_error() {
    let err = Tiers::parse(PUBLIC_A, Some(PUBLIC_A)).unwrap_err();
    assert!(err.contains("technicalpickles/a"), "{err}");
    assert!(err.contains("both"), "{err}");
}

#[test]
fn an_unknown_tier_is_an_error() {
    let public = r#"
[[repo]]
name = "technicalpickles/a"
tiers = ["sleepy"]
"#;
    let err = Tiers::parse(public, None).unwrap_err();
    assert!(err.contains("sleepy"), "{err}");
}

#[test]
fn an_unknown_key_is_an_error() {
    let public = r#"
[[repo]]
name = "technicalpickles/a"
teirs = ["active"]
"#;
    let err = Tiers::parse(public, None).unwrap_err();
    assert!(err.contains("teirs"), "{err}");
}

#[test]
fn an_empty_tier_list_is_an_error() {
    let public = r#"
[[repo]]
name = "technicalpickles/a"
tiers = []
"#;
    let err = Tiers::parse(public, None).unwrap_err();
    assert!(err.contains("technicalpickles/a"), "{err}");
}

#[test]
fn a_name_without_an_owner_is_an_error() {
    let public = r#"
[[repo]]
name = "a"
tiers = ["active"]
"#;
    assert!(Tiers::parse(public, None).is_err());
}

#[test]
fn watched_keeps_a_repository_that_is_dormant_and_deployed() {
    let public = r#"
[[repo]]
name = "technicalpickles/a"
tiers = ["dormant", "deployed"]

[[repo]]
name = "technicalpickles/b"
tiers = ["dormant"]
"#;
    let tiers = Tiers::parse(public, None).unwrap();
    assert_eq!(
        tiers.watched(WATCHED_BY_SECURITY_ALERTS),
        vec![&slug("technicalpickles/a")]
    );
}

#[test]
fn load_with_a_missing_path_is_an_error() {
    let err = Tiers::load(Some(Path::new("/nonexistent/tiers.toml"))).unwrap_err();
    assert!(err.contains("/nonexistent/tiers.toml"), "{err}");
}
