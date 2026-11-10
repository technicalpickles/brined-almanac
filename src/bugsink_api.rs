//! What the tracker consumer reads from Bugsink's API: issues, their tags, and their history.
//! The client that fetches them is `cutover::Bugsink`, which owns the paging.

use std::collections::{BTreeMap, BTreeSet};

use serde::Deserialize;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

/// One issue as `GET /issues/` lists it.
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
pub struct Issue {
    pub id: String,
    pub friendly_id: String,
    pub first_seen: String,
    pub last_seen: String,
    #[serde(rename = "digested_event_count")]
    pub count: u64,
    /// The first event's message, which is transcript text.
    #[serde(rename = "calculated_value")]
    pub reason: String,
    #[serde(rename = "is_resolved")]
    pub resolved: bool,
    #[serde(rename = "is_muted")]
    pub muted: bool,
}

/// One tag value on an issue, with how many events carried it.
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
pub struct Tag {
    pub key: String,
    pub value: String,
    pub count: u64,
}

/// One entry of an issue's history. `kind` is the lower-case name of Bugsink's turning point
/// kind, such as `first_seen`, `resolved`, `regressed` or `reopened`.
#[derive(Clone, Debug, Deserialize, PartialEq, Eq)]
pub struct TurningPoint {
    pub kind: String,
    pub timestamp: String,
}

/// When the issue was last unmuted, by a person or by its mute's volume condition.
pub fn last_unmuted(history: &[TurningPoint]) -> Result<Option<OffsetDateTime>, String> {
    let mut latest = None;
    for t in history.iter().filter(|t| t.kind == "unmuted") {
        let at = OffsetDateTime::parse(&t.timestamp, &Rfc3339)
            .map_err(|e| format!("history has the time {:?}: {e}", t.timestamp))?;
        latest = latest.max(Some(at));
    }
    Ok(latest)
}

/// How many times an issue has come back after being resolved: Bugsink marks a resolved issue
/// that fires again `regressed`, and a person reopening it `reopened`.
pub fn generation(history: &[TurningPoint]) -> u64 {
    history
        .iter()
        .filter(|t| matches!(t.kind.as_str(), "regressed" | "reopened"))
        .count() as u64
}

/// What the shipper's tags say about an issue.
#[derive(Debug, PartialEq, Eq)]
pub struct Labels {
    pub service: String,
    pub tool: String,
    pub class: String,
    /// Surface and its event count, most events first.
    pub surfaces: Vec<(String, u64)>,
    pub sessions: u64,
}

/// The commonest value of `key`. An issue is grouped by `[service, tool, class]`, so each has
/// one value, and the commonest only breaks a tie that should not happen.
fn dominant<'a>(tags: &'a [Tag], key: &str) -> Result<&'a str, String> {
    tags.iter()
        .filter(|t| t.key == key)
        .max_by(|a, b| a.count.cmp(&b.count).then_with(|| b.value.cmp(&a.value)))
        .map(|t| t.value.as_str())
        .ok_or_else(|| format!("no {key} tag"))
}

pub fn labels(tags: &[Tag]) -> Result<Labels, String> {
    let mut surfaces: BTreeMap<&str, u64> = BTreeMap::new();
    let mut sessions: BTreeSet<&str> = BTreeSet::new();
    for tag in tags {
        match tag.key.as_str() {
            "surface" => *surfaces.entry(&tag.value).or_default() += tag.count,
            "session" => {
                sessions.insert(&tag.value);
            }
            _ => {}
        }
    }
    let mut surfaces: Vec<(String, u64)> = surfaces
        .into_iter()
        .map(|(name, count)| (name.to_string(), count))
        .collect();
    surfaces.sort_by(|a, b| b.1.cmp(&a.1).then_with(|| a.0.cmp(&b.0)));
    Ok(Labels {
        service: dominant(tags, "service")?.to_string(),
        tool: dominant(tags, "tool")?.to_string(),
        class: dominant(tags, "class")?.to_string(),
        surfaces,
        sessions: sessions.len() as u64,
    })
}
