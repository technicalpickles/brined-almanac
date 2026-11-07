mod support;

use almanac::prelude::*;
use brined_almanac::transcript::read;
use brined_almanac::workflows::tool_failures::{
    CLASSES, class_named, classify, cluster, owner_of, report,
};
use support::{Failure, Fixture};

fn slug(raw: &str) -> Slug {
    raw.parse().expect("slug")
}

fn errors_of(failures: Vec<Failure>) -> Vec<brined_almanac::transcript::ToolError> {
    let fixture = Fixture::new();
    let mut file = fixture.file("a.jsonl");
    for f in failures {
        file = file.with(f);
    }
    file.write();
    read(fixture.path()).expect("read").errors
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

#[test]
fn twelve_approval_timeouts_on_one_day_are_one_cluster_of_twelve() {
    let errors = errors_of(vec![
        Failure::mcp("brineworks", "triage_apply")
            .error("Approval timed out")
            .times(12),
    ]);
    let clusters = cluster(errors);
    assert_eq!(clusters.len(), 1);
    let (key, c) = &clusters[0];
    assert_eq!(key.class, "approval-timeout");
    assert_eq!(c.count, 12);
    assert_eq!(c.first.as_str(), "2026-11-04T10:00:00Z");
    assert_eq!(c.last.as_str(), "2026-11-04T10:00:11Z");
}

#[test]
fn a_new_day_is_a_new_cluster() {
    let errors = errors_of(vec![
        Failure::mcp("brineworks", "triage_apply")
            .error("Approval timed out")
            .at("2026-11-04T10:00:00Z"),
        Failure::mcp("brineworks", "triage_apply")
            .error("Approval timed out")
            .at("2026-11-05T10:00:00Z"),
    ]);
    let clusters = cluster(errors);
    assert_eq!(clusters.len(), 2);
    assert_ne!(clusters[0].0.day, clusters[1].0.day);
}

#[test]
fn the_delivery_id_matches_the_spec() {
    let errors = errors_of(vec![
        Failure::mcp("brineworks", "triage_apply").error("Approval timed out"),
    ]);
    let clusters = cluster(errors);
    assert_eq!(
        clusters[0].0.delivery_id(),
        "scan:brent/brineworks/triage_apply/approval-timeout/2026-11-04"
    );
}

#[test]
fn report_lists_fields_then_a_blank_line_then_the_sample() {
    let errors = errors_of(vec![
        Failure::mcp("brineworks", "triage_apply")
            .error("Approval timed out")
            .times(2),
    ]);
    let clusters = cluster(errors);
    let (key, c) = &clusters[0];
    let text = serde_json::to_value(report(key, c)).unwrap();
    let text = text.as_str().unwrap();
    assert!(text.starts_with("service: brineworks\n"), "{text}");
    assert!(
        text.contains(
            "\ntool: triage_apply\nclass: approval-timeout\nday: 2026-11-04\ncount: 2\nfirst: 2026-11-04T10:00:00Z\nlast: 2026-11-04T10:00:01Z\n\nsample:\n"
        ),
        "{text}"
    );
    assert!(text.ends_with("Approval timed out'}"), "{text}");
}
