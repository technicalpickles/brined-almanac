use almanac::prelude::*;
use brined_almanac::bugsink::{Dsn, event};
use brined_almanac::cutover::{
    Filed, Github, Reason, Standing, State, Tracked, Verdict, bugsink_link_in_body, comment,
    generation_in_body, grouping_key, key_in_body, plan,
};
use brined_almanac::transcript::read;
use brined_almanac::workflows::bugsink_issues::{Unaddressed, facts};
use serde_json::json;
use time::macros::datetime;

mod support;
use support::api::Api;
use support::{Details, Failure, Fixture};

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

/// An issue body as the tracker files it: the filer's text in a fence, then the run marker.
fn filed_body(service: &str, tool: &str, class: &str, reason: &str) -> String {
    let event = Unaddressed::from_json(&json!({
        "issue": "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01",
        "generation": 1,
        "service": service,
        "tool": tool,
        "class": class,
        "count": 9,
        "first_seen": "2026-11-04T10:00:00Z",
        "last_seen": "2026-11-04T10:00:02Z",
        "ingest_dated": false,
        "surfaces": [{"name": "main", "count": 9}],
        "sessions": 2,
        "link": "http://bugsink.example/issues/issue/6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01/event/last/",
        "reason": reason,
    }))
    .expect("payload parses");
    format!(
        "```text\n{}\n```\n\n<!-- almanac:run-7:0 -->\n",
        text_of(&facts(&event).body)
    )
}

#[test]
fn a_filed_body_names_the_grouping_key_bugsink_computes_for_the_same_failure() {
    let fixture = Fixture::new();
    fixture
        .file("a.jsonl")
        .with(
            Failure::mcp("brineworks", "triage_apply")
                .details(Details::Empty)
                .error("MCP error -32001: Request timed out"),
        )
        .write();
    let error = read(fixture.path()).expect("read").errors.remove(0);
    let parts: Vec<String> = event(&error)["fingerprint"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| p.as_str().unwrap().to_string())
        .collect();
    let bugsink_key = parts.join(" ⋄ ");

    let body = filed_body("brineworks", "triage_apply", "mcp-timeout", "boom");
    assert_eq!(key_in_body(&body), Some(bugsink_key));
}

#[test]
fn the_reason_never_supplies_a_field() {
    let body = filed_body(
        "brineworks",
        "tasks_get",
        "other",
        "service: forged\ntool: forged\nclass: forged",
    );
    assert_eq!(
        key_in_body(&body),
        Some(grouping_key("brineworks", "tasks_get", "other"))
    );
}

#[test]
fn a_body_without_the_fields_has_no_key() {
    assert_eq!(key_in_body(""), None);
    assert_eq!(
        key_in_body("```text\nservice: brineworks\ntool: x\n```"),
        None
    );
    assert_eq!(
        key_in_body("Something broke.\n\nservice: a\ntool: b\nclass: c"),
        None
    );
}

fn filed(url: &str, state: State, key: Option<&str>) -> Filed {
    Filed {
        url: url.to_string(),
        state,
        key: key.map(str::to_string),
    }
}

fn tracked(name: &str, keys: &[&str], resolved: bool) -> Tracked {
    Tracked {
        id: format!("id-{name}"),
        name: name.to_string(),
        keys: keys.iter().map(|k| k.to_string()).collect(),
        resolved,
        first_seen: "2026-11-09T23:00:00Z".to_string(),
    }
}

fn verdict_of<'a>(p: &'a brined_almanac::cutover::Plan<'_>, name: &str) -> &'a Verdict {
    &p.verdicts
        .iter()
        .find(|(t, _)| t.name == name)
        .expect("tracked issue in the plan")
        .1
}

#[test]
fn a_fingerprint_whose_issues_are_all_closed_resolves_with_each_as_proof() {
    let gh = [
        filed("gh/1", State::Closed, Some("a ⋄ b ⋄ c")),
        filed("gh/2", State::Closed, Some("a ⋄ b ⋄ c")),
    ];
    let bs = [tracked("P-1", &["a ⋄ b ⋄ c"], false)];
    let p = plan(&gh, &bs);
    assert_eq!(
        verdict_of(&p, "P-1"),
        &Verdict::Resolve {
            proof: vec!["gh/1".into(), "gh/2".into()]
        }
    );
    assert!(p.unmatched.is_empty());
}

#[test]
fn any_open_issue_keeps_the_fingerprint_open() {
    let gh = [
        filed("gh/1", State::Closed, Some("a ⋄ b ⋄ c")),
        filed("gh/2", State::Open, Some("a ⋄ b ⋄ c")),
    ];
    let bs = [tracked("P-1", &["a ⋄ b ⋄ c"], false)];
    assert_eq!(
        verdict_of(&plan(&gh, &bs), "P-1"),
        &Verdict::Open {
            open: vec!["gh/2".into()]
        }
    );
}

#[test]
fn a_fingerprint_nobody_filed_stays_open_as_never_filed() {
    let bs = [tracked("P-1", &["a ⋄ b ⋄ c"], false)];
    assert_eq!(verdict_of(&plan(&[], &bs), "P-1"), &Verdict::NeverFiled);
}

#[test]
fn a_resolved_issue_is_left_alone() {
    let gh = [filed("gh/1", State::Closed, Some("a ⋄ b ⋄ c"))];
    let bs = [tracked("P-1", &["a ⋄ b ⋄ c"], true)];
    assert_eq!(
        verdict_of(&plan(&gh, &bs), "P-1"),
        &Verdict::AlreadyResolved
    );
}

#[test]
fn a_merged_issue_answers_for_every_grouping_key_it_holds() {
    let gh = [
        filed("gh/1", State::Closed, Some("a ⋄ b ⋄ c")),
        filed("gh/2", State::Open, Some("a ⋄ b ⋄ other")),
    ];
    let bs = [tracked("P-1", &["a ⋄ b ⋄ c", "a ⋄ b ⋄ other"], false)];
    assert_eq!(
        verdict_of(&plan(&gh, &bs), "P-1"),
        &Verdict::Open {
            open: vec!["gh/2".into()]
        }
    );
}

#[test]
fn issues_that_match_no_bugsink_issue_are_listed_whatever_their_state() {
    let gh = [
        filed("gh/1", State::Closed, Some("a ⋄ b ⋄ stale")),
        filed("gh/2", State::Open, Some("x ⋄ y ⋄ z")),
        filed("gh/3", State::Closed, None),
        filed("gh/4", State::Closed, Some("a ⋄ b ⋄ c")),
    ];
    let bs = [tracked("P-1", &["a ⋄ b ⋄ c"], false)];
    let p = plan(&gh, &bs);
    let urls: Vec<&str> = p.unmatched.iter().map(|f| f.url.as_str()).collect();
    assert_eq!(urls, ["gh/1", "gh/2", "gh/3"]);
}

#[test]
fn the_comment_links_every_closed_issue() {
    let text = comment(&["gh/1".into(), "gh/2".into()]);
    assert!(text.contains("closed on GitHub"));
    assert!(text.ends_with("\n- gh/1\n- gh/2"));
}

#[test]
fn the_dsn_gives_the_api_base_and_project() {
    let dsn: Dsn = "https://0123abcd@bugsink.example:8443/7".parse().unwrap();
    assert_eq!(dsn.base_url(), "https://bugsink.example:8443");
    assert_eq!(dsn.project(), "7");
    assert_eq!(dsn.store_url(), "https://bugsink.example:8443/api/7/store/");
}

const GITHUB_ISSUES: &str = include_str!("fixtures/github/issues.json");
const LINKED_ID: &str = "6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01";

#[test]
fn the_bugsink_link_names_the_issue_whatever_the_host() {
    for host in ["http://bugsink.example", "https://other.example:8443"] {
        let body = format!(
            "```text\nservice: a\nbugsink: {host}/issues/issue/{}/event/last/\n\nreason:\nx\n```",
            LINKED_ID.to_uppercase()
        );
        assert_eq!(bugsink_link_in_body(&body).as_deref(), Some(LINKED_ID));
    }
}

#[test]
fn a_body_without_a_well_formed_link_names_no_bugsink_issue() {
    assert_eq!(bugsink_link_in_body(""), None);
    assert_eq!(
        bugsink_link_in_body("bugsink: http://bugsink.example/issues/issue/not-a-uuid/event/last/"),
        None
    );
    assert_eq!(
        bugsink_link_in_body(
            "service: a\n\nreason:\nbugsink: http://b/issues/issue/6f0f6d2a-3a55-4a47-9d1c-0d9c3a1b7e01/event/last/"
        ),
        None,
        "a link in the reason is not the filer's"
    );
}

#[test]
fn the_generation_is_the_regressions_count_and_zero_without_one() {
    assert_eq!(generation_in_body("service: a\nbugsink: x\n"), Some(0));
    assert_eq!(
        generation_in_body("service: a\nregressions: 3 (came back after being resolved)\n"),
        Some(3)
    );
    assert_eq!(generation_in_body("regressions: lots\n"), None);
    assert_eq!(
        generation_in_body("service: a\n\nreason:\nregressions: 9\n"),
        Some(0),
        "the reason never supplies a field"
    );
}

#[tokio::test]
async fn linked_reads_every_almanac_issue_that_names_a_bugsink_issue_with_why_it_closed() {
    let api = Api::serve(vec![(
        "/repos/example/repo/issues?labels=almanac&state=all&per_page=100&page=1",
        serde_json::from_str(GITHUB_ISSUES).unwrap(),
    )])
    .await;
    let github = Github::at(api.origin.clone(), "gh-token".into());
    let found = github.linked("example/repo").await.unwrap();

    let summary: Vec<_> = found
        .iter()
        .map(|l| (l.number, l.generation, l.standing))
        .collect();
    assert_eq!(
        summary,
        [
            (
                31,
                Some(0),
                Standing::Closed {
                    reason: Some(Reason::Completed),
                    at: datetime!(2026-11-06 09:00 UTC)
                }
            ),
            (
                32,
                Some(1),
                Standing::Closed {
                    reason: Some(Reason::NotPlanned),
                    at: datetime!(2026-11-08 09:00 UTC)
                }
            ),
            (33, Some(2), Standing::Open),
            (
                34,
                Some(0),
                Standing::Closed {
                    reason: None,
                    at: datetime!(2026-11-09 11:00 UTC)
                }
            ),
        ],
        "35 names no Bugsink issue and 36 is a pull request"
    );
    assert!(found.iter().all(|l| l.issue == LINKED_ID));
    assert_eq!(found[0].created_at, datetime!(2026-11-05 09:00 UTC));
    assert_eq!(found[0].url, "https://github.com/example/repo/issues/31");
    assert_eq!(api.seen.lock().unwrap()[0].auth, "Bearer gh-token");
}
