mod support;

use brined_almanac::bugsink::Dsn;
use brined_almanac::bugsink_api::{Issue, Labels, Tag, TurningPoint, generation, labels};
use brined_almanac::cutover::Bugsink;
use serde_json::{Value, json};
use support::api::Api;

const ISSUES: &str = include_str!("fixtures/bugsink/issues.json");
const TAGS: &str = include_str!("fixtures/bugsink/tags.json");
const HISTORY: &str = include_str!("fixtures/bugsink/history.json");

fn page<T: serde::de::DeserializeOwned>(raw: &str) -> Vec<T> {
    let page: Value = serde_json::from_str(raw).expect("fixture json");
    serde_json::from_value(page["results"].clone()).expect("fixture parses")
}

fn tag(key: &str, value: &str, count: u64) -> Tag {
    Tag {
        key: key.into(),
        value: value.into(),
        count,
    }
}

fn turning(kind: &str) -> TurningPoint {
    TurningPoint { kind: kind.into() }
}

#[test]
fn an_issue_reads_the_fields_the_consumer_needs() {
    let issues: Vec<Issue> = page(ISSUES);
    assert_eq!(issues.len(), 2);
    let first = &issues[0];
    assert_eq!(first.id, "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01");
    assert_eq!(first.friendly_id, "BRENT-12");
    assert_eq!(first.count, 14);
    assert_eq!(first.reason, "MCP error -32001: Request timed out");
    assert_eq!(first.first_seen, "2026-11-04T10:00:00.250000Z");
    assert_eq!(first.last_seen, "2026-11-09T21:14:03.482113Z");
    assert!(!first.resolved && !first.muted);
    assert!(issues[1].resolved && issues[1].muted);
}

#[test]
fn the_labels_come_from_the_tags() {
    let found = labels(&page::<Tag>(TAGS)).unwrap();
    assert_eq!(
        found,
        Labels {
            service: "brineworks".into(),
            tool: "triage_apply".into(),
            class: "mcp-timeout".into(),
            surfaces: vec![("telegram".into(), 11), ("main".into(), 3)],
            sessions: 2,
        }
    );
}

#[test]
fn an_issue_missing_a_tag_is_named_in_the_error() {
    let err = labels(&[tag("service", "brineworks", 1), tag("tool", "t", 1)]).unwrap_err();
    assert_eq!(err, "no class tag");
}

#[test]
fn one_session_across_two_tag_rows_counts_once() {
    let tags = [
        tag("service", "s", 2),
        tag("tool", "t", 2),
        tag("class", "other", 2),
        tag("session", "aaaaaaaaaaaa", 1),
        tag("session", "aaaaaaaaaaaa", 1),
    ];
    assert_eq!(labels(&tags).unwrap().sessions, 1);
}

#[test]
fn generation_counts_only_regressions_and_reopenings() {
    let history: Vec<TurningPoint> = page(HISTORY);
    assert_eq!(generation(&history), 2);
    assert_eq!(generation(&[turning("first_seen")]), 0);
    assert_eq!(
        generation(&[
            turning("first_seen"),
            turning("muted"),
            turning("unmuted"),
            turning("resolved"),
            turning("next_materialized"),
            turning("manual_annotation"),
        ]),
        0
    );
}

fn client(origin: &str) -> Bugsink {
    let dsn: Dsn = format!("http://0123abcd@{}/7", origin.trim_start_matches("http://"))
        .parse()
        .unwrap();
    Bugsink::new(&dsn, "api-token".into())
}

fn body(raw: &str) -> Value {
    serde_json::from_str(raw).unwrap()
}

#[tokio::test]
async fn open_issues_asks_for_the_projects_open_state_with_the_bearer_token() {
    let api = Api::serve(vec![(
        "/api/canonical/0/issues/?project=7&state=open",
        body(ISSUES),
    )])
    .await;
    let issues = client(&api.origin).open_issues().await.unwrap();
    assert_eq!(issues.len(), 2);
    assert_eq!(
        api.targets(),
        ["/api/canonical/0/issues/?project=7&state=open"]
    );
    assert_eq!(api.seen.lock().unwrap()[0].auth, "Bearer api-token");
}

#[tokio::test]
async fn a_list_follows_the_next_link_to_the_last_page() {
    let issues: Value = body(ISSUES);
    let (first, second) = (issues["results"][0].clone(), issues["results"][1].clone());
    let api = Api::serve(vec![
        (
            "/api/canonical/0/issues/?project=7&state=open",
            json!({"next": "{base}/api/canonical/0/issues/?project=7&state=open&cursor=abc", "results": [first]}),
        ),
        (
            "/api/canonical/0/issues/?project=7&state=open&cursor=abc",
            json!({"next": null, "results": [second]}),
        ),
    ])
    .await;
    let found = client(&api.origin).open_issues().await.unwrap();
    let names: Vec<&str> = found.iter().map(|i| i.friendly_id.as_str()).collect();
    assert_eq!(names, ["BRENT-12", "BRENT-13"]);
    assert_eq!(api.targets().len(), 2);
}

#[tokio::test]
async fn tags_and_history_are_read_by_the_issue_id() {
    let issue: Issue = page::<Issue>(ISSUES).remove(0);
    let api = Api::serve(vec![
        (
            "/api/canonical/0/issues/6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01/tags/",
            body(TAGS),
        ),
        (
            "/api/canonical/0/issue-history/?issue=6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01",
            body(HISTORY),
        ),
    ])
    .await;
    let bugsink = client(&api.origin);
    assert_eq!(bugsink.tags(&issue).await.unwrap().len(), 7);
    assert_eq!(generation(&bugsink.history(&issue).await.unwrap()), 2);
    assert_eq!(api.targets().len(), 2);
}

#[tokio::test]
async fn a_refusal_is_an_error_that_names_the_read_and_not_the_host() {
    let api = Api::serve(vec![]).await;
    let err = client(&api.origin).open_issues().await.unwrap_err();
    assert!(err.contains("Bugsink issue list"), "{err}");
    assert!(err.contains("404"), "{err}");
    assert!(!err.contains(&api.origin), "{err}");
}

#[test]
fn the_issue_link_is_the_uis_last_event_page() {
    let issue: Issue = page::<Issue>(ISSUES).remove(0);
    let dsn: Dsn = "https://0123abcd@bugsink.example:8443/7".parse().unwrap();
    assert_eq!(
        Bugsink::new(&dsn, "t".into()).issue_url(&issue),
        "https://bugsink.example:8443/issues/issue/6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01/event/last/"
    );
}
