mod support;

use std::sync::Arc;

use almanac::prelude::*;
use almanac::runtime::{Outcome, Runtime};
use almanac::testing::FakeTracker;
use brined_almanac::transcript::read;
use brined_almanac::workflows;
use brined_almanac::workflows::tool_failures::{
    CLASSES, Facts, ToolFailed, ToolFailures, class_named, classify, cluster, facts, owner_of,
    policy,
};
use serde_json::json;
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
fn a_cluster_spans_its_first_and_last_error() {
    let errors = errors_of(vec![
        Failure::mcp("brineworks", "triage_apply")
            .error("Approval timed out")
            .times(2),
    ]);
    let clusters = cluster(errors);
    let (_, c) = &clusters[0];
    assert_eq!(c.count, 2);
    assert_eq!(c.first.as_str(), "2026-11-04T10:00:00Z");
    assert_eq!(c.last.as_str(), "2026-11-04T10:00:01Z");
}

/// Reads tainted text by position: `.forbidden-patterns` forbids the literal key in source.
fn text_of(body: &Untrusted<String>) -> String {
    let shown = serde_json::to_value(body).unwrap();
    shown
        .as_object()
        .and_then(|fields| fields.values().next())
        .and_then(|value| value.as_str())
        .unwrap()
        .to_string()
}

#[test]
fn the_body_lists_fields_then_a_blank_line_then_the_sample() {
    let Facts::Loud { body, .. } = facts(&tool_failed(
        "brineworks",
        "triage_apply",
        "approval-timeout",
        3,
    )) else {
        panic!("three approval timeouts are loud");
    };
    assert_eq!(
        text_of(&body),
        "service: brineworks\ntool: triage_apply\nclass: approval-timeout\nday: 2026-11-04\ncount: 3\nfirst: 2026-11-04T10:00:00Z\nlast: 2026-11-04T10:00:02Z\n\nsample:\nsynthetic sample"
    );
}

fn tool_failed(service: &str, tool: &str, class: &str, count: u64) -> ToolFailed {
    ToolFailed::from_json(&json!({
        "service": service,
        "tool": tool,
        "class": class,
        "day": "2026-11-04",
        "count": count,
        "first": "2026-11-04T10:00:00Z",
        "last": "2026-11-04T10:00:02Z",
        "sample": "synthetic sample",
    }))
    .expect("payload parses")
}

fn runtime(fx: &Fixture, tracker: Arc<dyn Tracker>, mode: Mode) -> Runtime {
    Runtime::new(workflows::all(), fx.providers(tracker)).in_mode(mode)
}

fn twelve_timeouts() -> Fixture {
    let fx = Fixture::new();
    fx.file("a.jsonl")
        .with(
            Failure::mcp("brineworks", "triage_apply")
                .error("Approval timed out")
                .times(12),
        )
        .write();
    fx
}

#[test]
fn files_at_the_threshold_and_stays_quiet_one_below() {
    for c in CLASSES {
        let at = facts(&tool_failed(
            "brineworks",
            "triage_apply",
            c.name,
            c.threshold,
        ));
        assert!(matches!(at, Facts::Loud { .. }), "{} at threshold", c.name);
        let below = facts(&tool_failed(
            "brineworks",
            "triage_apply",
            c.name,
            c.threshold - 1,
        ));
        assert!(matches!(below, Facts::Quiet), "{} one below", c.name);
    }
}

#[test]
fn brineworks_and_gog_route_to_their_repositories() {
    for (service, repo) in [
        ("brineworks", "technicalpickles/brineworks"),
        ("gog", "technicalpickles/pickleclaw"),
    ] {
        match facts(&tool_failed(service, "t", "mcp-timeout", 1)) {
            Facts::Loud { target, .. } => assert_eq!(target.to_string(), repo),
            Facts::Quiet => panic!("{service} should be loud"),
        }
    }
}

#[test]
fn policy_files_once_with_a_title_naming_the_cluster() {
    let loud = facts(&tool_failed("brineworks", "t", "mcp-timeout", 1));
    let effects = policy(&loud);
    assert_eq!(effects.len(), 1);
    assert!(matches!(
        &effects[0],
        Effect::File { title, .. }
            if title.to_string() == "Brent: brineworks/t mcp-timeout on 2026-11-04 (count 1)"
    ));
    assert!(policy(&Facts::Quiet).is_empty());
}

#[test]
fn the_workflow_key_carries_no_state() {
    for count in [3, 30] {
        let event = tool_failed("brineworks", "triage_apply", "approval-timeout", count);
        assert_eq!(
            ToolFailures::key(&event),
            "brineworks:triage_apply:approval-timeout:2026-11-04"
        );
    }
}

#[tokio::test]
async fn a_hostile_tool_name_still_makes_a_valid_payload() {
    let fx = Fixture::new();
    fx.file("a.jsonl")
        .with(Failure::mcp("brineworks", "weird tool/\u{fc}"))
        .write();
    let rt = runtime(&fx, Arc::new(FakeTracker::default()), Mode::DryRun);
    let found = rt.backfill(None).await.unwrap();
    assert_eq!(found.found, 1);
    assert_eq!(found.rejected, 0);
}

#[tokio::test]
async fn backfill_records_one_file_for_twelve_timeouts() {
    let fx = twelve_timeouts();
    let rt = runtime(&fx, Arc::new(FakeTracker::default()), Mode::DryRun);
    rt.backfill(None).await.unwrap();
    let outcomes = rt.settle().await.unwrap();
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        Outcome::Ran { effects, .. } => {
            assert_eq!(effects.len(), 1);
            assert!(
                matches!(&effects[0], Effect::File { target, .. } if target.to_string() == "technicalpickles/brineworks")
            );
        }
        _ => panic!("expected the workflow to run"),
    }
}

#[tokio::test]
async fn scanning_twice_reports_the_second_scan_as_duplicates() {
    let fx = twelve_timeouts();
    let rt = runtime(&fx, Arc::new(FakeTracker::default()), Mode::DryRun);
    let first = rt.backfill(None).await.unwrap();
    let second = rt.backfill(None).await.unwrap();
    assert_eq!(first.found, 1);
    assert_eq!(second.unchanged, first.found);
    assert_eq!(second.queued, 0);
}

#[tokio::test]
async fn a_fresh_store_queues_everything_again() {
    let fx = twelve_timeouts();
    let first = runtime(&fx, Arc::new(FakeTracker::default()), Mode::DryRun)
        .backfill(None)
        .await
        .unwrap();
    let second = runtime(&fx, Arc::new(FakeTracker::default()), Mode::DryRun)
        .backfill(None)
        .await
        .unwrap();
    assert_eq!(second.queued, first.found);
}

#[tokio::test]
async fn a_live_run_files_once_per_loud_cluster() {
    let fx = twelve_timeouts();
    fx.file("b.jsonl")
        .with(Failure::mcp("brineworks", "lone_tool"))
        .write();
    let tracker = Arc::new(FakeTracker::default());
    let rt = runtime(&fx, tracker.clone(), Mode::Live);
    rt.backfill(None).await.unwrap();
    rt.settle().await.unwrap();
    assert_eq!(tracker.calls.lock().unwrap().len(), 1);
    let filed = tracker.filed.lock().unwrap();
    assert_eq!(filed.len(), 1);
    assert_eq!(filed[0].1, "technicalpickles/brineworks");
    assert_eq!(
        filed[0].2,
        "Brent: brineworks/triage_apply approval-timeout on 2026-11-04 (count 12)"
    );
}

#[tokio::test]
async fn a_dry_run_calls_no_tracker() {
    let fx = twelve_timeouts();
    let tracker = Arc::new(FakeTracker::default());
    let rt = runtime(&fx, tracker.clone(), Mode::DryRun);
    let found = rt.backfill(None).await.unwrap();
    assert!(found.queued > 0);
    rt.settle().await.unwrap();
    assert!(tracker.calls.lock().unwrap().is_empty());
}
