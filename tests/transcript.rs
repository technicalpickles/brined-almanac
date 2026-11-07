mod support;

use std::fs;

use support::{Failure, Fixture};

#[test]
fn the_builder_writes_one_call_and_one_result_per_failure() {
    let fixture = Fixture::new();
    fixture
        .file("a.jsonl")
        .with(Failure::mcp("brineworks", "triage_apply"))
        .write();

    let text = fs::read_to_string(fixture.path().join("a.jsonl")).unwrap();
    let lines: Vec<serde_json::Value> = text
        .lines()
        .map(|l| serde_json::from_str(l).expect("each line is JSON"))
        .collect();

    assert_eq!(lines.len(), 2);
    assert_eq!(lines[1]["message"]["isError"], true);
}
