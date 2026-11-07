use std::collections::{HashMap, HashSet};
use std::error::Error;
use std::fmt;
use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::LazyLock;

use almanac::prelude::*;
use regex::Regex;
use serde::Serialize;
use serde_json::Value;

static SERVER_NAME: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"'serverName': '([^']+)'").expect("serverName regex"));
static TOOL_NAME: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"'toolName': '([^']+)'").expect("toolName regex"));

/// The directory of OpenClaw transcripts the scan reads.
pub struct Transcripts(pub PathBuf);

#[derive(Debug)]
pub struct Read {
    pub errors: Vec<ToolError>,
    pub skipped: Skipped,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Skipped {
    pub not_json: u64,
    pub undated: u64,
}

#[derive(Debug)]
pub struct ToolError {
    pub at: At,
    pub service: Slug,
    pub tool: Slug,
    pub text: Sample,
}

/// An ISO 8601 timestamp that starts with a calendar day.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct At(String);

impl At {
    pub fn parse(raw: &str) -> Option<At> {
        let b = raw.as_bytes().get(..11)?;
        let shape = b.iter().enumerate().all(|(i, c)| match i {
            4 | 7 => *c == b'-',
            10 => *c == b'T',
            _ => c.is_ascii_digit(),
        });
        shape.then(|| At(raw.to_string()))
    }

    pub fn day(&self) -> Slug {
        self.0[..10].parse().expect("a parsed At starts with a day")
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// Error text from a transcript. It is untrusted, so `Debug` hides it and the only ways out are
/// a question about it (`narrow`) or a bounded report (`compose`).
pub struct Sample(String);

impl fmt::Debug for Sample {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("Sample(..)")
    }
}

impl Sample {
    pub const MAX: usize = 500;

    pub fn narrow<N: Copy>(&self, f: fn(&str) -> N) -> N {
        f(&self.0)
    }

    pub fn compose(&self, header: &str) -> Report {
        let cut: String = self.0.chars().take(Self::MAX).collect();
        Report(format!("{header}\n\nsample:\n{cut}"))
    }
}

#[derive(Serialize)]
#[serde(transparent)]
pub struct Report(String);

#[derive(Debug)]
pub enum ReadError {
    Dir { path: PathBuf, source: io::Error },
    File { path: PathBuf, source: io::Error },
}

impl fmt::Display for ReadError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let (Self::Dir { path, source } | Self::File { path, source }) = self;
        write!(f, "cannot read {}: {source}", path.display())
    }
}

impl Error for ReadError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        let (Self::Dir { source, .. } | Self::File { source, .. }) = self;
        Some(source)
    }
}

impl From<ReadError> for StepError {
    fn from(e: ReadError) -> Self {
        StepError(e.to_string())
    }
}

pub fn read(dir: &Path) -> Result<Read, ReadError> {
    let dir_error = |source| ReadError::Dir {
        path: dir.to_path_buf(),
        source,
    };
    let mut paths = Vec::new();
    for entry in fs::read_dir(dir).map_err(dir_error)? {
        let path = entry.map_err(dir_error)?.path();
        if path.extension().is_some_and(|x| x == "jsonl") {
            paths.push(path);
        }
    }
    paths.sort();

    let mut calls: HashMap<String, String> = HashMap::new();
    let mut seen: HashSet<String> = HashSet::new();
    let mut found: Vec<(String, ToolError)> = Vec::new();
    let mut skipped = Skipped::default();

    for path in paths {
        let bytes = fs::read(&path).map_err(|source| ReadError::File {
            path: path.clone(),
            source,
        })?;
        for line in bytes.split(|b| *b == b'\n') {
            if line.is_empty() {
                continue;
            }
            let Ok(event) = serde_json::from_slice::<Value>(line) else {
                skipped.not_json += 1;
                continue;
            };
            if event["type"] != "message" {
                continue;
            }
            let message = &event["message"];
            remember_calls(message, &mut calls);
            if message["role"] != "toolResult" || message["isError"] != true {
                continue;
            }
            let Some(id) = event["id"].as_str() else {
                continue;
            };
            if !seen.insert(id.to_string()) {
                continue;
            }
            let Some(at) = event["timestamp"].as_str().and_then(At::parse) else {
                skipped.undated += 1;
                continue;
            };
            let text = error_text(message);
            let called = message["toolCallId"]
                .as_str()
                .and_then(|c| calls.get(c))
                .map(String::as_str);
            let (service, tool) = identity(&text, called, message["toolName"].as_str());
            found.push((
                id.to_string(),
                ToolError {
                    at,
                    service: slug(&service),
                    tool: slug(&tool),
                    text: Sample(text),
                },
            ));
        }
    }

    found.sort_by(|(a_id, a), (b_id, b)| (&a.at, a_id).cmp(&(&b.at, b_id)));
    Ok(Read {
        errors: found.into_iter().map(|(_, e)| e).collect(),
        skipped,
    })
}

fn remember_calls(message: &Value, calls: &mut HashMap<String, String>) {
    let Some(blocks) = message["content"].as_array() else {
        return;
    };
    for block in blocks.iter().filter(|b| b["type"] == "toolCall") {
        if let (Some(id), Some(called)) = (block["id"].as_str(), block["arguments"]["id"].as_str())
        {
            calls.insert(id.to_string(), called.to_string());
        }
    }
}

/// `details` as text, or the text blocks of `content` when `details` is empty.
fn error_text(message: &Value) -> String {
    let details = match &message["details"] {
        Value::String(s) => s.clone(),
        Value::Null => String::new(),
        other => other.to_string(),
    };
    if details.trim().len() > 2 {
        return details;
    }
    message["content"]
        .as_array()
        .map(|blocks| {
            blocks
                .iter()
                .filter(|b| b["type"] == "text")
                .filter_map(|b| b["text"].as_str())
                .collect::<Vec<_>>()
                .join("\n")
        })
        .unwrap_or_default()
}

fn identity(text: &str, called: Option<&str>, tool_name: Option<&str>) -> (String, String) {
    if let (Some(s), Some(t)) = (SERVER_NAME.captures(text), TOOL_NAME.captures(text)) {
        return (s[1].to_string(), t[1].to_string());
    }
    if tool_name == Some("tool_call") {
        return match called {
            Some(id) => split_id(id),
            None => ("openclaw".into(), "unknown".into()),
        };
    }
    split_id(tool_name.unwrap_or("unknown"))
}

/// Splits a tool id in any of its spellings (`svc__tool`, `mcp:svc:svc__tool`, bare `tool`)
/// into service and tool. A bare name is one of OpenClaw's own tools.
fn split_id(id: &str) -> (String, String) {
    let id = id.rsplit(':').next().unwrap_or(id);
    match id.split_once("__") {
        Some((s, t)) => (s.to_string(), t.to_string()),
        None => ("openclaw".into(), id.to_string()),
    }
}

fn slug(raw: &str) -> Slug {
    let mut s: String = raw
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || "._/-:@[]".contains(c) {
                c
            } else {
                '-'
            }
        })
        .take(200)
        .collect();
    if s.is_empty() {
        s.push_str("unknown");
    }
    s.parse().expect("sanitized text is a slug")
}
