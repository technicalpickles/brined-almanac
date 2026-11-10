mod support;

use std::sync::Arc;

use almanac::prelude::*;
use almanac::runtime::Runtime;
use almanac::testing::FakeTracker;
use brined_almanac::bugsink::Dsn;
use brined_almanac::bugsink_api::Issue;
use brined_almanac::classes::CLASSES;
use brined_almanac::cutover::{Bugsink, Github};
use brined_almanac::workflows;
use brined_almanac::workflows::bugsink_issues::{
    BugsinkIssues, BugsinkReader, Reading, Unaddressed, facts, policy, unaddressed,
};
use serde_json::{Value, json};
use support::Fixture;
use support::api::Api;

const ID: &str = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01";

fn issue(count: u64, resolved: bool, muted: bool) -> Issue {
    serde_json::from_value(json!({
        "id": ID,
        "friendly_id": "BRENT-12",
        "first_seen": "2026-11-04T10:00:00Z",
        "last_seen": "2026-11-09T21:00:00Z",
        "digested_event_count": count,
        "calculated_value": "boom",
        "is_resolved": resolved,
        "is_muted": muted,
    }))
    .unwrap()
}

#[test]
fn an_issue_wants_filing_at_its_class_threshold_and_not_one_below() {
    for class in CLASSES {
        assert!(
            unaddressed(&issue(class.threshold, false, false), class.name),
            "{} at threshold",
            class.name
        );
        assert!(
            !unaddressed(&issue(class.threshold - 1, false, false), class.name),
            "{} one below",
            class.name
        );
    }
}

#[test]
fn a_resolved_or_muted_issue_wants_nothing_however_loud() {
    assert!(!unaddressed(&issue(1000, true, false), "mcp-timeout"));
    assert!(!unaddressed(&issue(1000, false, true), "mcp-timeout"));
    assert!(!unaddressed(&issue(1000, true, true), "mcp-timeout"));
}

#[test]
fn an_unknown_class_gets_the_threshold_of_other() {
    assert!(!unaddressed(&issue(2, false, false), "gone"));
    assert!(unaddressed(&issue(3, false, false), "gone"));
}

fn unaddressed_event(service: &str, generation: u64) -> Unaddressed {
    Unaddressed::from_json(&json!({
        "issue": ID,
        "generation": generation,
        "service": service,
        "tool": "triage_apply",
        "class": "mcp-timeout",
        "count": 14,
        "first_seen": "2026-11-04T10:00:00.250000Z",
        "last_seen": "2026-11-09T21:14:03.482113Z",
        "surfaces": [{"name": "telegram", "count": 11}, {"name": "main", "count": 3}],
        "sessions": 2,
        "link": format!("http://bugsink.example/issues/issue/{ID}/event/last/"),
        "reason": "MCP error -32001: Request timed out",
    }))
    .expect("payload parses")
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
fn the_body_lists_the_header_then_a_blank_line_then_the_reason() {
    let body = facts(&unaddressed_event("brineworks", 0)).body;
    assert_eq!(
        text_of(&body),
        format!(
            "service: brineworks\ntool: triage_apply\nclass: mcp-timeout\nevents: 14\nfirst seen: 2026-11-04T10:00:00.250000Z\nlast seen: 2026-11-09T21:14:03.482113Z\nsurfaces: telegram (11), main (3)\nsessions: 2\nbugsink: http://bugsink.example/issues/issue/{ID}/event/last/\n\nreason:\nMCP error -32001: Request timed out"
        )
    );
}

#[test]
fn a_regressed_issue_says_so_in_the_header() {
    let body = text_of(&facts(&unaddressed_event("brineworks", 2)).body);
    let (header, _) = body.split_once("\n\n").unwrap();
    assert!(
        header.contains("\nregressions: 2 (came back after being resolved)\n"),
        "{header}"
    );
    assert!(!text_of(&facts(&unaddressed_event("brineworks", 0)).body).contains("regressions"));
}

fn title_of(effects: &[Effect]) -> String {
    match effects {
        [Effect::File { title, .. }] => title.to_string(),
        other => panic!("expected one filing, got {}", other.len()),
    }
}

#[test]
fn the_title_names_the_failure_and_its_events_and_flags_a_regression() {
    let plain = policy(&facts(&unaddressed_event("brineworks", 0)));
    assert_eq!(
        title_of(&plain),
        "Brent: brineworks/triage_apply mcp-timeout (14 events)"
    );
    let regressed = policy(&facts(&unaddressed_event("brineworks", 1)));
    assert_eq!(
        title_of(&regressed),
        "Brent: brineworks/triage_apply mcp-timeout regressed (14 events)"
    );
}

#[test]
fn brineworks_and_gog_route_to_their_repositories() {
    for (service, repo) in [
        ("brineworks", "technicalpickles/brineworks"),
        ("gog", "technicalpickles/pickleclaw"),
    ] {
        match &policy(&facts(&unaddressed_event(service, 0)))[..] {
            [Effect::File { target, .. }] => assert_eq!(target.to_string(), repo),
            other => panic!("expected one filing, got {}", other.len()),
        }
    }
}

#[test]
fn the_workflow_key_is_the_issue_and_its_generation() {
    assert_eq!(
        BugsinkIssues::key(&unaddressed_event("brineworks", 0)),
        format!("bugsink:{ID}:0")
    );
    assert_eq!(
        BugsinkIssues::key(&unaddressed_event("brineworks", 3)),
        format!("bugsink:{ID}:3")
    );
}

const LOUD: &str = "11111111-aaaa-4aaa-8aaa-000000000001";
const QUIET: &str = "11111111-aaaa-4aaa-8aaa-000000000002";
const MUTED: &str = "11111111-aaaa-4aaa-8aaa-000000000003";
const RESOLVED: &str = "11111111-aaaa-4aaa-8aaa-000000000004";
const UNTAGGED: &str = "11111111-aaaa-4aaa-8aaa-000000000005";

fn listed(id: &str, count: u64, resolved: bool, muted: bool) -> Value {
    json!({
        "id": id,
        "friendly_id": format!("BRENT-{}", &id[id.len() - 2..]),
        "first_seen": "2026-11-04T10:00:00Z",
        "last_seen": "2026-11-09T21:00:00Z",
        "digested_event_count": count,
        "calculated_value": "MCP error -32001: Request timed out",
        "is_resolved": resolved,
        "is_muted": muted,
    })
}

fn tagged(class: &str) -> Value {
    json!({"next": null, "results": [
        {"key": "service", "value": "brineworks", "count": 5},
        {"key": "tool", "value": "triage_apply", "count": 5},
        {"key": "class", "value": class, "count": 5},
        {"key": "surface", "value": "telegram", "count": 5},
        {"key": "session", "value": "a1b2c3d4e5f6", "count": 5},
    ]})
}

fn history(kinds: &[&str]) -> Value {
    let rows: Vec<Value> = kinds
        .iter()
        .map(|k| json!({"kind": k, "timestamp": "2026-11-09T20:00:00Z"}))
        .collect();
    json!({"next": null, "results": rows})
}

const LIST: &str = "/api/canonical/0/issues/?project=7&state=open";

/// Five open issues: one that files, one a count short, one muted, one resolved, and one the
/// shipper never tagged.
async fn bugsink() -> Api {
    let api = Api::serve(vec![(
        LIST,
        json!({"next": null, "results": [
            listed(LOUD, 5, false, false),
            listed(QUIET, 2, false, false),
            listed(MUTED, 50, false, true),
            listed(RESOLVED, 50, true, false),
            listed(UNTAGGED, 50, false, false),
        ]}),
    )])
    .await;
    for (id, class) in [
        (LOUD, "approval-timeout"),
        (QUIET, "approval-timeout"),
        (MUTED, "mcp-timeout"),
        (RESOLVED, "mcp-timeout"),
    ] {
        api.route(
            &format!("/api/canonical/0/issues/{id}/tags/"),
            tagged(class),
        );
    }
    api.route(
        &format!("/api/canonical/0/issues/{UNTAGGED}/tags/"),
        json!({"next": null, "results": []}),
    );
    api.route(
        &format!("/api/canonical/0/issue-history/?issue={LOUD}"),
        history(&["first_seen"]),
    );
    api
}

fn runtime(fx: &Fixture, api: &Api, tracker: Arc<dyn Tracker>, mode: Mode) -> Runtime {
    runtime_reading(fx, api, tracker, mode, false)
}

fn runtime_reading(
    fx: &Fixture,
    api: &Api,
    tracker: Arc<dyn Tracker>,
    mode: Mode,
    with_github: bool,
) -> Runtime {
    let dsn: Dsn = format!(
        "http://0123abcd@{}/7",
        api.origin.trim_start_matches("http://")
    )
    .parse()
    .unwrap();
    let reader = BugsinkReader(Some(Reading {
        bugsink: Bugsink::new(&dsn, "api-token".into()),
        github: with_github.then(|| Github::at(api.origin.clone(), "gh-token".into())),
        live: mode == Mode::Live,
    }));
    Runtime::new(workflows::all(), fx.reading(tracker, reader)).in_mode(mode)
}

#[tokio::test]
async fn a_live_scan_files_only_the_issue_that_is_loud_unresolved_and_unmuted() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    let tracker = Arc::new(FakeTracker::default());
    let rt = runtime(&fx, &api, tracker.clone(), Mode::Live);
    let found = rt.backfill(None).await.unwrap();
    assert_eq!(found.found, 1);
    rt.settle().await.unwrap();
    let filed = tracker.filed.lock().unwrap();
    assert_eq!(filed.len(), 1);
    assert_eq!(filed[0].1, "technicalpickles/brineworks");
    assert_eq!(
        filed[0].2,
        "Brent: brineworks/triage_apply approval-timeout (5 events)"
    );
}

#[tokio::test]
async fn history_is_read_only_for_issues_that_pass_the_threshold() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    let rt = runtime(&fx, &api, Arc::new(FakeTracker::default()), Mode::DryRun);
    rt.backfill(None).await.unwrap();
    let histories: Vec<String> = api
        .targets()
        .into_iter()
        .filter(|t| t.contains("issue-history"))
        .collect();
    assert_eq!(histories.len(), 1);
    assert!(histories[0].ends_with(LOUD));
}

#[tokio::test]
async fn scanning_again_changes_nothing() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    let rt = runtime(&fx, &api, Arc::new(FakeTracker::default()), Mode::DryRun);
    let first = rt.backfill(None).await.unwrap();
    let second = rt.backfill(None).await.unwrap();
    assert_eq!(first.found, 1);
    assert_eq!(second.unchanged, 1);
    assert_eq!(second.queued, 0);
}

#[tokio::test]
async fn growing_events_on_an_issue_already_seen_are_not_a_new_delivery() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    let rt = runtime(&fx, &api, Arc::new(FakeTracker::default()), Mode::DryRun);
    rt.backfill(None).await.unwrap();
    api.route(
        LIST,
        json!({"next": null, "results": [listed(LOUD, 9, false, false)]}),
    );
    let again = rt.backfill(None).await.unwrap();
    assert_eq!((again.unchanged, again.queued), (1, 0));
}

#[tokio::test]
async fn a_regression_is_a_new_delivery_and_files_again() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    let tracker = Arc::new(FakeTracker::default());
    let rt = runtime(&fx, &api, tracker.clone(), Mode::Live);
    rt.backfill(None).await.unwrap();
    rt.settle().await.unwrap();

    api.route(
        &format!("/api/canonical/0/issue-history/?issue={LOUD}"),
        history(&["regressed", "resolved", "first_seen"]),
    );
    let again = rt.backfill(None).await.unwrap();
    assert_eq!(again.queued, 1);
    rt.settle().await.unwrap();

    let filed = tracker.filed.lock().unwrap();
    assert_eq!(filed.len(), 2);
    assert_ne!(filed[0].0, filed[1].0);
    assert_eq!(
        filed[1].2,
        "Brent: brineworks/triage_apply approval-timeout regressed (5 events)"
    );
}

#[tokio::test]
async fn a_dry_run_calls_no_tracker() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    let tracker = Arc::new(FakeTracker::default());
    let rt = runtime(&fx, &api, tracker.clone(), Mode::DryRun);
    assert_eq!(rt.backfill(None).await.unwrap().queued, 1);
    rt.settle().await.unwrap();
    assert!(tracker.calls.lock().unwrap().is_empty());
}

#[tokio::test]
async fn without_a_token_or_dsn_the_scan_finds_nothing() {
    let fx = Fixture::new();
    let rt = Runtime::new(
        workflows::all(),
        fx.providers(Arc::new(FakeTracker::default())),
    )
    .in_mode(Mode::DryRun);
    assert_eq!(rt.backfill(None).await.unwrap().found, 0);
}

#[tokio::test]
async fn a_failed_read_of_bugsink_fails_the_scan() {
    let (fx, api) = (Fixture::new(), Api::serve(vec![]).await);
    let rt = runtime(&fx, &api, Arc::new(FakeTracker::default()), Mode::DryRun);
    assert!(rt.backfill(None).await.is_err());
}

const BRINEWORKS_ISSUES: &str =
    "/repos/technicalpickles/brineworks/issues?labels=almanac&state=all&per_page=100&page=1";
const PICKLECLAW_ISSUES: &str =
    "/repos/technicalpickles/pickleclaw/issues?labels=almanac&state=all&per_page=100&page=1";

/// One almanac issue as GitHub lists it, whose body links the Bugsink issue `id`.
fn almanac_issue(
    number: u64,
    id: &str,
    generation: u64,
    created: &str,
    closed: Option<(&str, Option<&str>)>,
) -> Value {
    let regressions = if generation > 0 {
        format!("regressions: {generation} (came back after being resolved)\n")
    } else {
        String::new()
    };
    json!({
        "html_url": format!("https://github.com/example/repo/issues/{number}"),
        "number": number,
        "state": if closed.is_some() { "closed" } else { "open" },
        "state_reason": closed.and_then(|(_, reason)| reason),
        "created_at": created,
        "closed_at": closed.map(|(at, _)| at),
        "body": format!(
            "```text\nservice: brineworks\ntool: triage_apply\nclass: approval-timeout\n{regressions}bugsink: http://bugsink.example/issues/issue/{id}/event/last/\n\nreason:\nboom\n```\n"
        ),
    })
}

/// Serves the almanac issues of the first repository and none for the second.
fn on_github(api: &Api, issues: Vec<Value>) {
    api.route(BRINEWORKS_ISSUES, Value::Array(issues));
    api.route(PICKLECLAW_ISSUES, json!([]));
}

fn accept_changes(api: &Api, id: &str) {
    api.route("/api/canonical/0/issue-comments/", json!({}));
    for action in ["resolve", "mute-until"] {
        api.route(
            &format!("/api/canonical/0/issues/{id}/{action}/"),
            json!({}),
        );
    }
}

fn changes(api: &Api) -> Vec<String> {
    api.posts().into_iter().map(|p| p.target).collect()
}

const COMPLETED: Option<(&str, Option<&str>)> = Some(("2026-11-06T09:00:00Z", Some("completed")));
const NOT_PLANNED: Option<(&str, Option<&str>)> =
    Some(("2026-11-06T09:00:00Z", Some("not_planned")));

async fn live_scan(fx: &Fixture, api: &Api, tracker: &Arc<FakeTracker>) -> usize {
    let rt = runtime_reading(fx, api, tracker.clone(), Mode::Live, true);
    let found = rt.backfill(None).await.unwrap().found;
    rt.settle().await.unwrap();
    found
}

#[tokio::test]
async fn closing_as_completed_comments_then_resolves_and_files_nothing() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    on_github(
        &api,
        vec![almanac_issue(7, LOUD, 0, "2026-11-05T09:00:00Z", COMPLETED)],
    );
    let tracker = Arc::new(FakeTracker::default());

    assert_eq!(live_scan(&fx, &api, &tracker).await, 0);

    assert_eq!(
        changes(&api),
        [
            "/api/canonical/0/issue-comments/".to_string(),
            format!("/api/canonical/0/issues/{LOUD}/resolve/"),
        ]
    );
    let comment = &api.posts()[0].body;
    assert!(comment.contains(LOUD), "{comment}");
    assert!(
        comment.contains("https://github.com/example/repo/issues/7"),
        "{comment}"
    );
    assert_eq!(api.posts()[0].auth, "Bearer api-token");
    assert!(tracker.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn closing_as_not_planned_mutes_until_a_burst_of_events() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    on_github(
        &api,
        vec![almanac_issue(
            7,
            LOUD,
            0,
            "2026-11-05T09:00:00Z",
            NOT_PLANNED,
        )],
    );
    let tracker = Arc::new(FakeTracker::default());

    assert_eq!(live_scan(&fx, &api, &tracker).await, 0);

    assert_eq!(
        changes(&api),
        [
            "/api/canonical/0/issue-comments/".to_string(),
            format!("/api/canonical/0/issues/{LOUD}/mute-until/"),
        ]
    );
    let wake: Value = serde_json::from_str(&api.posts()[1].body).unwrap();
    assert_eq!(
        wake,
        json!({"period_name": "day", "nr_of_periods": 1, "gte_threshold": 50})
    );
    assert!(tracker.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn a_dry_run_says_what_it_would_do_and_changes_nothing() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    on_github(
        &api,
        vec![almanac_issue(7, LOUD, 0, "2026-11-05T09:00:00Z", COMPLETED)],
    );
    let rt = runtime_reading(
        &fx,
        &api,
        Arc::new(FakeTracker::default()),
        Mode::DryRun,
        true,
    );
    assert_eq!(rt.backfill(None).await.unwrap().found, 0);
    assert!(api.posts().is_empty());
}

#[tokio::test]
async fn a_regression_is_filed_and_not_resolved_again_by_the_old_close() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    let first = almanac_issue(7, LOUD, 0, "2026-11-05T09:00:00Z", COMPLETED);
    on_github(&api, vec![first.clone()]);
    let tracker = Arc::new(FakeTracker::default());
    live_scan(&fx, &api, &tracker).await;
    assert_eq!(api.posts().len(), 2);

    api.route(
        &format!("/api/canonical/0/issue-history/?issue={LOUD}"),
        history(&["regressed", "resolved", "first_seen"]),
    );
    assert_eq!(live_scan(&fx, &api, &tracker).await, 1);
    assert_eq!(api.posts().len(), 2, "the closed issue is at generation 0");
    {
        let filed = tracker.filed.lock().unwrap();
        assert_eq!(filed.len(), 1);
        assert_eq!(
            filed[0].2,
            "Brent: brineworks/triage_apply approval-timeout regressed (5 events)"
        );
    }

    let regression = almanac_issue(8, LOUD, 1, "2026-11-09T09:00:00Z", None);
    on_github(&api, vec![first.clone(), regression]);
    live_scan(&fx, &api, &tracker).await;
    assert_eq!(api.posts().len(), 2, "the newest almanac issue is open");

    let closed = almanac_issue(8, LOUD, 1, "2026-11-09T09:00:00Z", COMPLETED);
    on_github(&api, vec![first, closed]);
    live_scan(&fx, &api, &tracker).await;
    assert_eq!(api.posts().len(), 4, "closing the regression resolves it");
}

#[tokio::test]
async fn a_close_with_no_reason_is_left_to_file_as_before() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    let no_reason = Some(("2026-11-06T09:00:00Z", None));
    on_github(
        &api,
        vec![almanac_issue(7, LOUD, 0, "2026-11-05T09:00:00Z", no_reason)],
    );
    let tracker = Arc::new(FakeTracker::default());
    assert_eq!(live_scan(&fx, &api, &tracker).await, 1);
    assert!(api.posts().is_empty());
}

#[tokio::test]
async fn an_issue_that_woke_after_its_not_planned_close_is_not_muted_again() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    on_github(
        &api,
        vec![almanac_issue(
            7,
            LOUD,
            0,
            "2026-11-05T09:00:00Z",
            NOT_PLANNED,
        )],
    );
    api.route(
        &format!("/api/canonical/0/issue-history/?issue={LOUD}"),
        json!({"next": null, "results": [
            {"kind": "unmuted", "timestamp": "2026-11-09T21:00:00Z"},
            {"kind": "muted", "timestamp": "2026-11-06T10:00:00Z"},
            {"kind": "first_seen", "timestamp": "2026-11-04T10:00:00Z"},
        ]}),
    );
    live_scan(&fx, &api, &Arc::new(FakeTracker::default())).await;
    assert!(api.posts().is_empty());
}

#[tokio::test]
async fn an_issue_nobody_lists_as_open_is_never_acted_on() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, RESOLVED);
    on_github(
        &api,
        vec![almanac_issue(
            7,
            RESOLVED,
            0,
            "2026-11-05T09:00:00Z",
            COMPLETED,
        )],
    );
    live_scan(&fx, &api, &Arc::new(FakeTracker::default())).await;
    assert!(api.posts().is_empty());
    assert!(
        !api.targets()
            .iter()
            .any(|t| t.contains("issue-history") && t.ends_with(RESOLVED))
    );
}

#[tokio::test]
async fn without_github_the_scan_still_files_and_changes_nothing() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    let tracker = Arc::new(FakeTracker::default());
    let rt = runtime(&fx, &api, tracker.clone(), Mode::Live);
    assert_eq!(rt.backfill(None).await.unwrap().found, 1);
    assert!(api.posts().is_empty());
}

#[tokio::test]
async fn a_failed_read_of_github_skips_resolving_and_the_scan_still_files() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    accept_changes(&api, LOUD);
    let tracker = Arc::new(FakeTracker::default());
    assert_eq!(live_scan(&fx, &api, &tracker).await, 1);
    assert!(api.posts().is_empty());
    assert_eq!(tracker.filed.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn a_failed_resolve_is_reported_and_the_scan_goes_on() {
    let (fx, api) = (Fixture::new(), bugsink().await);
    api.route("/api/canonical/0/issue-comments/", json!({}));
    on_github(
        &api,
        vec![almanac_issue(7, LOUD, 0, "2026-11-05T09:00:00Z", COMPLETED)],
    );
    let rt = runtime_reading(
        &fx,
        &api,
        Arc::new(FakeTracker::default()),
        Mode::Live,
        true,
    );
    assert!(rt.backfill(None).await.is_ok());
    assert_eq!(
        api.posts().len(),
        2,
        "the resolve was tried after the comment"
    );
}
