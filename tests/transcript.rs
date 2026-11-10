mod support;

use std::fs;
use std::io::Write;

use brined_almanac::transcript::{At, Read, ReadError, read};
use support::{Details, Failure, Fixture, Spelling};

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

fn read_one(failure: Failure) -> Read {
    let fixture = Fixture::new();
    fixture.file("a.jsonl").with(failure).write();
    read(fixture.path()).expect("read")
}

fn identity(failure: Failure) -> (String, String) {
    let result = read_one(failure);
    assert_eq!(result.errors.len(), 1);
    let e = &result.errors[0];
    (e.service.to_string(), e.tool.to_string())
}

fn pair(service: &str, tool: &str) -> (String, String) {
    (service.to_string(), tool.to_string())
}

#[test]
fn each_spelling_reads_identity_from_details() {
    for spelling in [Spelling::Prefixed, Spelling::Namespaced, Spelling::Bare] {
        let failure = Failure::mcp("brineworks", "triage_apply").spelled(spelling);
        assert_eq!(identity(failure), pair("brineworks", "triage_apply"));
    }
}

#[test]
fn without_mcp_details_the_call_id_names_the_tool() {
    let named = |s| {
        Failure::mcp("brineworks", "triage_apply")
            .details(Details::NamedOnly)
            .spelled(s)
    };
    assert_eq!(
        identity(named(Spelling::Prefixed)),
        pair("brineworks", "triage_apply")
    );
    assert_eq!(
        identity(named(Spelling::Namespaced)),
        pair("brineworks", "triage_apply")
    );
    assert_eq!(
        identity(named(Spelling::Bare)),
        pair("openclaw", "triage_apply")
    );
}

#[test]
fn a_builtin_tool_belongs_to_openclaw() {
    assert_eq!(identity(Failure::builtin("read")), pair("openclaw", "read"));
}

#[test]
fn a_tool_called_directly_by_its_prefixed_name_belongs_to_its_service() {
    assert_eq!(
        identity(Failure::builtin("brineworks__email_rules_add")),
        pair("brineworks", "email_rules_add")
    );
    assert_eq!(
        identity(Failure::builtin("gog__calendar")),
        pair("gog", "calendar")
    );
}

#[test]
fn a_json_object_in_details_falls_back_to_the_call_id() {
    let failure = Failure::mcp("brineworks", "triage_apply")
        .details(Details::Object)
        .spelled(Spelling::Namespaced);
    assert_eq!(identity(failure), pair("brineworks", "triage_apply"));
}

#[test]
fn a_result_without_its_call_block_is_openclaw_unknown() {
    let failure = Failure::mcp("brineworks", "triage_apply")
        .details(Details::NamedOnly)
        .orphaned();
    assert_eq!(identity(failure), pair("openclaw", "unknown"));
}

#[test]
fn empty_details_falls_back_to_content() {
    let failure = Failure::mcp("brineworks", "triage_apply")
        .details(Details::Empty)
        .error("Approval timed out");
    let result = read_one(failure);
    assert_eq!(result.errors.len(), 1);
    assert!(
        result.errors[0]
            .text
            .narrow(|s| s.contains("Approval timed out"))
    );
}

#[test]
fn details_that_only_name_the_tool_fall_back_to_content() {
    let failure = Failure::builtin("brineworks__tasks_update")
        .details(Details::StatusOnly)
        .error("sorry, too many clients already");
    let result = read_one(failure);
    assert_eq!(result.errors.len(), 1);
    let e = &result.errors[0];
    assert_eq!(
        (e.service.to_string(), e.tool.to_string()),
        pair("brineworks", "tasks_update")
    );
    assert!(e.text.narrow(|s| s.contains("too many clients already")));
    let excerpt = serde_json::to_value(e.text.excerpt()).unwrap();
    assert_eq!(excerpt, "sorry, too many clients already");
}

#[test]
fn an_apostrophe_in_the_repr_still_reads() {
    let failure = Failure::mcp("brineworks", "triage_apply").error("it's broken");
    let result = read_one(failure);
    let e = &result.errors[0];
    assert_eq!(
        (e.service.to_string(), e.tool.to_string()),
        pair("brineworks", "triage_apply")
    );
    assert!(e.text.narrow(|s| s.contains("broken")));
}

#[test]
fn lines_that_are_not_json_are_skipped_and_counted() {
    let fixture = Fixture::new();
    fixture
        .file("a.jsonl")
        .garbage(12)
        .with(Failure::mcp("brineworks", "triage_apply"))
        .write();
    let result = read(fixture.path()).unwrap();
    assert_eq!(result.skipped.not_json, 12);
    assert_eq!(result.errors.len(), 1);
}

#[test]
fn a_line_with_invalid_utf8_is_skipped_and_counted() {
    let fixture = Fixture::new();
    fixture
        .file("a.jsonl")
        .with(Failure::mcp("brineworks", "triage_apply"))
        .write();
    let path = fixture.path().join("a.jsonl");
    let mut file = fs::OpenOptions::new().append(true).open(&path).unwrap();
    file.write_all(b"\xff\xfe not json\n").unwrap();
    drop(file);
    fixture
        .file("b.jsonl")
        .with(Failure::mcp("brineworks", "triage_apply"))
        .write();
    let mut bytes = fs::read(&path).unwrap();
    bytes.extend(fs::read(fixture.path().join("b.jsonl")).unwrap());
    fs::write(&path, bytes).unwrap();
    fs::remove_file(fixture.path().join("b.jsonl")).unwrap();

    let result = read(fixture.path()).unwrap();
    assert_eq!(result.errors.len(), 2);
    assert_eq!(result.skipped.not_json, 1);
}

#[test]
fn an_event_id_seen_twice_counts_once() {
    let result = read_one(Failure::mcp("brineworks", "triage_apply").duplicated());
    assert_eq!(result.errors.len(), 1);
}

#[test]
fn an_event_id_seen_in_two_files_counts_once() {
    let fixture = Fixture::new();
    for name in ["a.jsonl", "b.jsonl"] {
        fixture
            .file(name)
            .with(Failure::mcp("brineworks", "triage_apply").event_id("evt-1"))
            .write();
    }
    assert_eq!(read(fixture.path()).unwrap().errors.len(), 1);
}

#[test]
fn results_that_are_not_errors_are_ignored() {
    let result = read_one(Failure::mcp("brineworks", "triage_apply").succeeded());
    assert!(result.errors.is_empty());
}

#[test]
fn an_undated_result_is_counted_and_dropped() {
    let result = read_one(Failure::mcp("brineworks", "triage_apply").at("yesterday"));
    assert_eq!(result.skipped.undated, 1);
    assert!(result.errors.is_empty());
}

#[test]
fn errors_come_back_sorted_by_time() {
    let fixture = Fixture::new();
    fixture
        .file("a.jsonl")
        .with(Failure::mcp("brineworks", "triage_apply").at("2026-11-04T12:00:00Z"))
        .with(Failure::mcp("brineworks", "triage_apply").at("2026-11-04T08:00:00Z"))
        .write();
    let errors = read(fixture.path()).unwrap().errors;
    assert_eq!(errors.len(), 2);
    assert!(errors[0].at < errors[1].at);
}

#[test]
fn other_extensions_are_ignored_and_an_empty_dir_is_empty() {
    let fixture = Fixture::new();
    assert!(read(fixture.path()).unwrap().errors.is_empty());
    fs::write(fixture.path().join("notes.txt"), "not a transcript\n").unwrap();
    let result = read(fixture.path()).unwrap();
    assert!(result.errors.is_empty());
    assert_eq!((result.skipped.not_json, result.skipped.undated), (0, 0));
}

#[test]
fn a_sample_never_prints_its_text() {
    let result = read_one(Failure::mcp("brineworks", "triage_apply"));
    assert_eq!(result.errors.len(), 1);
    assert!(!format!("{result:?}").contains("synthetic failure"));
}

#[test]
fn a_sample_keeps_the_full_text_and_its_excerpt_cuts_it() {
    let long = "x".repeat(2000);
    let failure = Failure::mcp("brineworks", "triage_apply")
        .details(Details::Empty)
        .error(&long);
    let result = read_one(failure);
    let sample = &result.errors[0].text;
    assert_eq!(sample.narrow(str::len), 2000);
    let excerpt = serde_json::to_value(sample.excerpt()).unwrap();
    assert_eq!(excerpt.as_str().unwrap().chars().count(), 500);
}

#[test]
fn an_unreadable_directory_fails_naming_the_path() {
    let err = read(std::path::Path::new("/nonexistent/x")).unwrap_err();
    assert!(matches!(err, ReadError::Dir { .. }));
    assert!(err.to_string().contains("/nonexistent/x"));
}

#[test]
fn a_jsonl_entry_that_is_a_directory_fails_naming_it() {
    let fixture = Fixture::new();
    fs::create_dir(fixture.path().join("x.jsonl")).unwrap();
    let err = read(fixture.path()).unwrap_err();
    assert!(matches!(err, ReadError::File { .. }));
    assert!(err.to_string().contains("x.jsonl"));
}

#[test]
fn at_parse_needs_a_date_and_a_t() {
    assert!(At::parse("2026-11-04T10:00:00Z").is_some());
    assert!(At::parse("2026-11-04").is_none());
    assert_eq!(
        At::parse("2026-11-04T10:00:00Z").unwrap().day().to_string(),
        "2026-11-04"
    );
}
