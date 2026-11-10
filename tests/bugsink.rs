mod support;

use std::sync::{Arc, Mutex};

use brined_almanac::bugsink::{Bugsink, Cursor, Dsn, Shipped, due, event};
use brined_almanac::providers::bugsink_client_from;
use brined_almanac::transcript::{At, ToolError, read};
use serde_json::{Value, json};
use support::{Details, Failure, Fixture};
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
use tokio::net::TcpListener;

const DSN_KEY: &str = "0123abcd";

fn errors_in(fixture: &Fixture) -> Vec<ToolError> {
    read(fixture.path()).expect("read").errors
}

fn one(name: &str, failure: Failure) -> ToolError {
    let fixture = Fixture::new();
    fixture.file(name).with(failure).write();
    let mut errors = errors_in(&fixture);
    assert_eq!(errors.len(), 1);
    errors.remove(0)
}

fn timeout(error: &str) -> Failure {
    Failure::mcp("brineworks", "triage_apply")
        .details(Details::Empty)
        .error(error)
}

fn message_of(error: &str) -> Value {
    event(&one("a.jsonl", timeout(error)))["message"].clone()
}

#[test]
fn an_event_carries_the_fingerprint_its_own_time_and_where_it_happened() {
    let e = one(
        "agent-main-telegram-direct-42.jsonl",
        timeout("MCP error -32001: Request timed out").at("2026-11-04T10:00:00Z"),
    );
    let ev = event(&e);
    assert_eq!(
        ev["fingerprint"],
        json!(["brineworks", "triage_apply", "mcp-timeout"])
    );
    assert_eq!(ev["timestamp"], "2026-11-04T10:00:00Z");
    assert_eq!(ev["level"], "error");
    assert_eq!(ev["logger"], "brineworks.triage_apply");
    assert_eq!(ev["message"], "MCP error -32001: Request timed out");
    assert_eq!(ev["tags"]["surface"], "telegram");
    assert_eq!(ev["tags"]["class"], "mcp-timeout");
    assert_eq!(ev["tags"]["service"], "brineworks");
    assert_eq!(ev["tags"]["tool"], "triage_apply");
}

#[test]
fn the_session_tag_is_a_hash_of_the_file_name() {
    let session = |name: &str| {
        let e = one(name, timeout("boom"));
        event(&e)["tags"]["session"].as_str().unwrap().to_string()
    };
    let a = session("agent-main-telegram-direct-42.jsonl");
    assert_eq!(a.len(), 12);
    assert!(a.bytes().all(|b| b.is_ascii_hexdigit()));
    assert_eq!(a, session("agent-main-telegram-direct-42.jsonl"));
    assert_ne!(a, session("agent-main-telegram-direct-43.jsonl"));
}

#[test]
fn the_surface_is_the_word_after_the_agent_or_unknown() {
    let surface = |name: &str| one(name, timeout("boom")).surface.to_string();
    assert_eq!(surface("agent-main-main.jsonl"), "main");
    assert_eq!(surface("agent-main-openwebui-1-2.jsonl"), "openwebui");
    assert_eq!(surface("agent-main-cron-1.jsonl"), "cron");
    assert_eq!(surface("notes.jsonl"), "unknown");
}

#[test]
fn a_resend_repeats_the_event_id_and_each_call_gets_its_own() {
    let fixture = Fixture::new();
    fixture
        .file("a.jsonl")
        .with(timeout("boom").times(2))
        .write();
    let ids = |errors: Vec<ToolError>| -> Vec<String> {
        errors
            .iter()
            .map(|e| event(e)["event_id"].as_str().unwrap().to_string())
            .collect()
    };
    let first = ids(errors_in(&fixture));
    assert_eq!(first, ids(errors_in(&fixture)));
    assert_ne!(first[0], first[1]);
    assert!(
        first
            .iter()
            .all(|id| id.len() == 32 && id.bytes().all(|b| b.is_ascii_hexdigit()))
    );
}

#[test]
fn the_message_is_the_line_that_says_why() {
    assert_eq!(
        message_of("{\"error\": \"pool exhausted\\nat line 3\"}"),
        "pool exhausted"
    );
    assert_eq!(
        message_of("Command failed: git push\nremote rejected"),
        "remote rejected"
    );
    assert_eq!(
        message_of("\n\n  first real line\nsecond"),
        "first real line"
    );
    assert_eq!(message_of(""), "(no text)");
    let long = "x".repeat(300);
    assert_eq!(message_of(&long).as_str().unwrap().len(), 200);
}

fn at(raw: &str) -> At {
    At::parse(raw).expect("timestamp")
}

#[test]
fn without_a_cursor_everything_is_due_and_with_one_the_last_hour_onward() {
    let fixture = Fixture::new();
    for time in [
        "2026-11-04T10:59:59Z",
        "2026-11-04T11:00:00Z",
        "2026-11-04T13:00:00Z",
    ] {
        fixture
            .file(&format!("{time}.jsonl"))
            .with(timeout("boom").at(time))
            .write();
    }
    let errors = errors_in(&fixture);
    assert_eq!(due(&errors, None).len(), 3);
    let times: Vec<&str> = due(&errors, Some(&at("2026-11-04T12:00:00Z")))
        .iter()
        .map(|e| e.at.as_str())
        .collect();
    assert_eq!(times, ["2026-11-04T11:00:00Z", "2026-11-04T13:00:00Z"]);
}

#[test]
fn a_cursor_survives_a_round_trip_and_a_missing_or_garbled_one_is_none() {
    let dir = tempfile::tempdir().unwrap();
    let cursor = Cursor(dir.path().join("cursor"));
    assert_eq!(cursor.load().unwrap(), None);
    cursor.save(&at("2026-11-04T10:00:00.123Z")).unwrap();
    assert_eq!(cursor.load().unwrap(), Some(at("2026-11-04T10:00:00.123Z")));
    std::fs::write(&cursor.0, "not a time").unwrap();
    assert_eq!(cursor.load().unwrap(), None);
}

#[test]
fn a_dsn_names_the_store_endpoint_and_a_bad_one_never_echoes_its_key() {
    let dsn: Dsn = format!("https://{DSN_KEY}@bugsink.example:8000/3")
        .parse()
        .unwrap();
    assert_eq!(dsn.store_url(), "https://bugsink.example:8000/api/3/store/");
    assert!(!format!("{dsn:?}").contains(DSN_KEY));
    for bad in [
        format!("{DSN_KEY}@bugsink.example/3"),
        format!("ftp://{DSN_KEY}@bugsink.example/3"),
        "https://bugsink.example/3".to_string(),
        format!("https://{DSN_KEY}@bugsink.example/project"),
    ] {
        let err = bad.parse::<Dsn>().unwrap_err();
        assert!(!err.contains(DSN_KEY), "{err}");
    }
}

#[test]
fn the_client_is_off_without_a_dsn_and_a_bad_dsn_fails_without_its_value() {
    assert!(bugsink_client_from(|_| None, true).unwrap().0.is_none());
    let empty = |key: &str| (key == "BUGSINK_DSN").then(String::new);
    assert!(bugsink_client_from(empty, true).unwrap().0.is_none());
    let bad = |key: &str| (key == "BUGSINK_DSN").then(|| format!("{DSN_KEY}@nowhere"));
    let err = bugsink_client_from(bad, true).err().unwrap();
    assert!(!err.contains(DSN_KEY), "{err}");
    let good =
        |key: &str| (key == "BUGSINK_DSN").then(|| format!("http://{DSN_KEY}@127.0.0.1:9/1"));
    assert!(bugsink_client_from(good, true).unwrap().0.is_some());
}

struct Received {
    path: String,
    auth: String,
    body: Value,
}

/// A store endpoint on loopback that answers 200 to the first `accept` events and 503 after.
async fn store(accept: usize) -> (String, Arc<Mutex<Vec<Received>>>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let seen = Arc::new(Mutex::new(Vec::new()));
    let log = seen.clone();
    tokio::spawn(async move {
        loop {
            let (socket, _) = listener.accept().await.unwrap();
            let log = log.clone();
            tokio::spawn(async move {
                let mut socket = BufReader::new(socket);
                loop {
                    let mut request_line = String::new();
                    if socket.read_line(&mut request_line).await.unwrap_or(0) == 0 {
                        return;
                    }
                    let path = request_line.split(' ').nth(1).unwrap_or("").to_string();
                    let (mut length, mut auth) = (0, String::new());
                    loop {
                        let mut header = String::new();
                        socket.read_line(&mut header).await.unwrap();
                        let header = header.trim_end();
                        if header.is_empty() {
                            break;
                        }
                        let (name, value) = header.split_once(':').unwrap();
                        match name.to_ascii_lowercase().as_str() {
                            "content-length" => length = value.trim().parse().unwrap(),
                            "x-sentry-auth" => auth = value.trim().to_string(),
                            _ => {}
                        }
                    }
                    let mut body = vec![0; length];
                    socket.read_exact(&mut body).await.unwrap();
                    let count = {
                        let mut log = log.lock().unwrap();
                        log.push(Received {
                            path,
                            auth,
                            body: serde_json::from_slice(&body).unwrap(),
                        });
                        log.len()
                    };
                    let status = if count <= accept {
                        "200 OK"
                    } else {
                        "503 Service Unavailable"
                    };
                    let reply = format!("HTTP/1.1 {status}\r\ncontent-length: 0\r\n\r\n");
                    socket.get_mut().write_all(reply.as_bytes()).await.unwrap();
                }
            });
        }
    });
    (format!("http://{DSN_KEY}@{address}/7"), seen)
}

fn three_hours() -> Fixture {
    let fixture = Fixture::new();
    for time in [
        "2026-11-04T10:00:00Z",
        "2026-11-04T11:30:00Z",
        "2026-11-04T12:00:00Z",
    ] {
        fixture
            .file(&format!("{time}.jsonl"))
            .with(timeout("boom").at(time))
            .write();
    }
    fixture
}

#[tokio::test]
async fn a_live_ship_posts_each_event_with_the_key_and_a_second_resends_only_the_overlap() {
    let (dsn, seen) = store(usize::MAX).await;
    let dir = tempfile::tempdir().unwrap();
    let cursor = || Cursor(dir.path().join("cursor"));
    let bugsink = Bugsink::new(dsn.parse().unwrap(), cursor(), true);
    let errors = errors_in(&three_hours());

    let first = bugsink.ship(&errors).await.unwrap();
    assert_eq!(first, Shipped { due: 3, sent: 3 });
    assert_eq!(cursor().load().unwrap(), Some(at("2026-11-04T12:00:00Z")));
    {
        let seen = seen.lock().unwrap();
        assert!(seen.iter().all(|r| r.path == "/api/7/store/"));
        assert!(
            seen.iter()
                .all(|r| r.auth.contains(&format!("sentry_key={DSN_KEY}")))
        );
        assert_eq!(seen[0].body["timestamp"], "2026-11-04T10:00:00Z");
    }

    let second = bugsink.ship(&errors).await.unwrap();
    assert_eq!(second, Shipped { due: 2, sent: 2 });
    let seen = seen.lock().unwrap();
    assert_eq!(seen[3].body["event_id"], seen[1].body["event_id"]);
}

#[tokio::test]
async fn a_dry_run_sends_nothing_and_leaves_the_cursor() {
    let (dsn, seen) = store(usize::MAX).await;
    let dir = tempfile::tempdir().unwrap();
    let bugsink = Bugsink::new(
        dsn.parse().unwrap(),
        Cursor(dir.path().join("cursor")),
        false,
    );
    let shipped = bugsink.ship(&errors_in(&three_hours())).await.unwrap();
    assert_eq!(shipped, Shipped { due: 3, sent: 0 });
    assert!(seen.lock().unwrap().is_empty());
    assert!(!dir.path().join("cursor").exists());
}

#[tokio::test]
async fn a_refused_event_stops_the_send_and_the_cursor_stays_at_the_last_accepted() {
    let (dsn, _) = store(1).await;
    let dir = tempfile::tempdir().unwrap();
    let cursor = || Cursor(dir.path().join("cursor"));
    let bugsink = Bugsink::new(dsn.parse().unwrap(), cursor(), true);
    let err = bugsink.ship(&errors_in(&three_hours())).await.unwrap_err();
    assert!(err.0.contains("sent 1 of 3"), "{}", err.0);
    assert!(err.0.contains("503"), "{}", err.0);
    assert_eq!(cursor().load().unwrap(), Some(at("2026-11-04T10:00:00Z")));
}
