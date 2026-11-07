#![allow(dead_code)]

use std::cell::Cell;
use std::fs;
use std::path::Path;
use std::sync::Arc;

use almanac::prelude::*;
use brined_almanac::transcript::Transcripts;
use serde_json::{Map, Value};
use tempfile::TempDir;

const ROLE_KEY: &str = "role";
const RESULT_ROLE: &str = concat!("tool", "Result");
const DEFAULT_TIME: &str = "2026-11-04T10:00:00Z";
const DEFAULT_ERROR: &str = "synthetic failure";

pub struct Fixture {
    dir: TempDir,
    counter: Cell<u64>,
}

impl Fixture {
    pub fn new() -> Self {
        Fixture {
            dir: TempDir::new().expect("create temp dir"),
            counter: Cell::new(0),
        }
    }

    pub fn path(&self) -> &Path {
        self.dir.path()
    }

    pub fn providers(&self, tracker: Arc<dyn Tracker>) -> Providers {
        Providers::new()
            .provide::<Transcripts>(Arc::new(Transcripts(self.path().to_path_buf())))
            .provide::<dyn Tracker>(tracker)
    }

    pub fn file(&self, name: &str) -> FileBuilder<'_> {
        FileBuilder {
            fixture: self,
            name: name.to_string(),
            lines: Vec::new(),
        }
    }

    fn next(&self) -> u64 {
        let n = self.counter.get() + 1;
        self.counter.set(n);
        n
    }
}

pub struct FileBuilder<'a> {
    fixture: &'a Fixture,
    name: String,
    lines: Vec<String>,
}

impl FileBuilder<'_> {
    pub fn with(mut self, failure: Failure) -> Self {
        for i in 0..failure.times {
            let call_id = format!("call-{:04}", self.fixture.next());
            let at = add_seconds(&failure.at, i);
            if failure.has_call_block() {
                let event_id = format!("evt-{:04}", self.fixture.next());
                self.lines
                    .push(failure.assistant_line(&event_id, &at, &call_id));
            }
            let event_id = failure
                .event_id
                .clone()
                .unwrap_or_else(|| format!("evt-{:04}", self.fixture.next()));
            let line = failure.result_line(&event_id, &at, &call_id);
            self.lines.push(line.clone());
            if failure.duplicated {
                self.lines.push(line);
            }
        }
        self
    }

    pub fn garbage(mut self, n: usize) -> Self {
        for i in 0..n {
            self.lines.push(format!("this line {i} is not json"));
        }
        self
    }

    pub fn write(self) {
        let mut text = self.lines.join("\n");
        text.push('\n');
        fs::write(self.fixture.path().join(&self.name), text).expect("write fixture file");
    }
}

#[derive(Clone, Copy)]
pub enum Spelling {
    Prefixed,
    Namespaced,
    Bare,
}

#[derive(Clone, Copy)]
pub enum Details {
    Mcp,
    NamedOnly,
    Empty,
    Object,
}

pub struct Failure {
    service: String,
    tool: String,
    builtin: bool,
    spelling: Spelling,
    details: Details,
    at: String,
    error: String,
    times: u64,
    duplicated: bool,
    succeeded: bool,
    orphaned: bool,
    event_id: Option<String>,
}

impl Failure {
    pub fn mcp(service: &str, tool: &str) -> Self {
        Failure {
            service: service.to_string(),
            tool: tool.to_string(),
            builtin: false,
            spelling: Spelling::Prefixed,
            details: Details::Mcp,
            at: DEFAULT_TIME.to_string(),
            error: DEFAULT_ERROR.to_string(),
            times: 1,
            duplicated: false,
            succeeded: false,
            orphaned: false,
            event_id: None,
        }
    }

    pub fn builtin(tool: &str) -> Self {
        Failure {
            builtin: true,
            details: Details::NamedOnly,
            ..Failure::mcp("", tool)
        }
    }

    pub fn spelled(mut self, s: Spelling) -> Self {
        self.spelling = s;
        self
    }

    pub fn details(mut self, d: Details) -> Self {
        self.details = d;
        self
    }

    pub fn at(mut self, iso: &str) -> Self {
        self.at = iso.to_string();
        self
    }

    pub fn error(mut self, text: &str) -> Self {
        self.error = text.to_string();
        self
    }

    pub fn times(mut self, n: u64) -> Self {
        self.times = n;
        self
    }

    pub fn duplicated(mut self) -> Self {
        self.duplicated = true;
        self
    }

    pub fn succeeded(mut self) -> Self {
        self.succeeded = true;
        self
    }

    pub fn orphaned(mut self) -> Self {
        self.orphaned = true;
        self
    }

    pub fn event_id(mut self, id: &str) -> Self {
        self.event_id = Some(id.to_string());
        self
    }

    fn has_call_block(&self) -> bool {
        !self.builtin && !self.orphaned
    }

    fn spelled_id(&self) -> String {
        match self.spelling {
            Spelling::Prefixed => format!("{}__{}", self.service, self.tool),
            Spelling::Namespaced => {
                format!("mcp:{}:{}__{}", self.service, self.service, self.tool)
            }
            Spelling::Bare => self.tool.clone(),
        }
    }

    fn tool_name(&self) -> String {
        if self.builtin {
            self.tool.clone()
        } else {
            "tool_call".to_string()
        }
    }

    fn assistant_line(&self, event_id: &str, at: &str, call_id: &str) -> String {
        let arguments = obj([("id", Value::from(self.spelled_id()))]);
        let block = obj([
            ("type", Value::from("toolCall")),
            ("id", Value::from(call_id)),
            ("name", Value::from("tool_call")),
            ("arguments", arguments),
        ]);
        let message = obj([
            (ROLE_KEY, Value::from("assistant")),
            ("content", Value::Array(vec![block])),
        ]);
        event(event_id, at, message)
    }

    fn result_line(&self, event_id: &str, at: &str, call_id: &str) -> String {
        let text_block = obj([
            ("type", Value::from("text")),
            ("text", Value::from(self.error.as_str())),
        ]);
        let message = obj([
            (ROLE_KEY, Value::from(RESULT_ROLE)),
            ("toolName", Value::from(self.tool_name())),
            ("toolCallId", Value::from(call_id)),
            ("isError", Value::from(!self.succeeded)),
            ("details", self.details_value()),
            ("content", Value::Array(vec![text_block])),
        ]);
        event(event_id, at, message)
    }

    fn details_value(&self) -> Value {
        match self.details {
            Details::Empty => Value::from(""),
            Details::Object => obj([
                ("status", Value::from("error")),
                ("tool", self.tool_value()),
                ("error", Value::from(self.error.as_str())),
            ]),
            Details::Mcp | Details::NamedOnly => Value::from(self.details_repr()),
        }
    }

    fn tool_value(&self) -> Value {
        match self.details {
            Details::NamedOnly => Value::from(self.spelled_id()),
            _ => {
                let mcp = obj([
                    ("serverName", Value::from(self.service.as_str())),
                    ("toolName", Value::from(self.tool.as_str())),
                ]);
                obj([("mcp", mcp)])
            }
        }
    }

    fn details_repr(&self) -> String {
        let tool = match self.details {
            Details::NamedOnly => py_str(&self.spelled_id()),
            _ => format!(
                "{{'mcp': {{'serverName': {}, 'toolName': {}}}}}",
                py_str(&self.service),
                py_str(&self.tool)
            ),
        };
        format!(
            "{{'status': 'error', 'tool': {tool}, 'error': {}}}",
            py_str(&self.error)
        )
    }
}

fn obj<const N: usize>(entries: [(&str, Value); N]) -> Value {
    let mut map = Map::new();
    for (key, value) in entries {
        map.insert(key.to_string(), value);
    }
    Value::Object(map)
}

fn event(id: &str, at: &str, message: Value) -> String {
    let line = obj([
        ("type", Value::from("message")),
        ("id", Value::from(id)),
        ("timestamp", Value::from(at)),
        ("message", message),
    ]);
    line.to_string()
}

fn py_str(text: &str) -> String {
    let quote = if text.contains('\'') && !text.contains('"') {
        '"'
    } else {
        '\''
    };
    let mut out = String::new();
    out.push(quote);
    for c in text.chars() {
        match c {
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            c if c == quote => {
                out.push('\\');
                out.push(c);
            }
            c => out.push(c),
        }
    }
    out.push(quote);
    out
}

fn add_seconds(iso: &str, seconds: u64) -> String {
    if iso.len() < 19 || !iso.is_ascii() {
        return iso.to_string();
    }
    let n = |range: std::ops::Range<usize>| -> i64 { iso[range].parse().expect("ISO timestamp") };
    let (year, month, day) = (n(0..4), n(5..7), n(8..10));
    let secs_of_day = n(11..13) * 3600 + n(14..16) * 60 + n(17..19);
    let total = days_from_civil(year, month, day) * 86_400 + secs_of_day + seconds as i64;
    let (y, m, d) = civil_from_days(total.div_euclid(86_400));
    let s = total.rem_euclid(86_400);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z",
        s / 3600,
        s % 3600 / 60,
        s % 60
    )
}

fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let doy = (153 * (if m > 2 { m - 3 } else { m + 9 }) + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

fn civil_from_days(z: i64) -> (i64, i64, i64) {
    let z = z + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + if m <= 2 { 1 } else { 0 };
    (y, m, d)
}
