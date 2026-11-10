use almanac::prelude::*;
use brined_almanac::classes::{CLASSES, class_named, classify, owner_of};

fn slug(raw: &str) -> Slug {
    raw.parse().expect("slug")
}

#[test]
fn classify_maps_each_table_text_to_its_class() {
    let cases = [
        ("MCP error -32001: Request timed out", "mcp-timeout"),
        (
            "FATAL: sorry, too many clients already",
            "pg-too-many-clients",
        ),
        ("Approval timed out", "approval-timeout"),
        ("Unknown tool id: triage_aply", "unknown-tool-id"),
        ("1 validation error for Args", "arg-validation"),
        ("Validation failed for field x", "arg-validation"),
        ("Missing required argument: repo", "arg-validation"),
    ];
    for (text, class) in cases {
        assert_eq!(classify(text).name, class, "{text}");
    }
}

#[test]
fn classify_maps_unknown_text_to_other() {
    assert_eq!(classify("the widget exploded").name, "other");
    assert_eq!(classify("").name, "other");
}

#[test]
fn classify_reads_past_500_chars() {
    let text = format!("{}Approval timed out", "x".repeat(600));
    assert_eq!(classify(&text).name, "approval-timeout");
}

#[test]
fn class_named_falls_back_to_other() {
    let gone = class_named("gone");
    assert_eq!(gone.name, "other");
    assert_eq!(gone.threshold, 3);
    assert_eq!(class_named("mcp-timeout").threshold, 1);
}

#[test]
fn every_class_name_and_owner_is_a_slug() {
    for class in CLASSES {
        assert!(class.name.parse::<Slug>().is_ok(), "{}", class.name);
    }
    for service in ["brineworks", "telegram"] {
        let owner = owner_of(&slug(service));
        assert_eq!(owner.to_string().parse::<Slug>().unwrap(), owner);
    }
    assert_eq!(
        owner_of(&slug("brineworks")).to_string(),
        "technicalpickles/brineworks"
    );
    assert_eq!(
        owner_of(&slug("telegram")).to_string(),
        "technicalpickles/pickleclaw"
    );
}
