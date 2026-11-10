//! Ships each failed tool call in Brent's transcripts to Bugsink as one Sentry event, through
//! the store endpoint. Bugsink owns grouping and issue state from there.

use std::fmt;
use std::fs;
use std::io;
use std::path::PathBuf;
use std::str::FromStr;
use std::time::Duration;

use almanac::prelude::*;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use time::macros::datetime;

use crate::transcript::{At, ToolError};
use crate::workflows::tool_failures::classify;

/// A scan resends this much before the cursor, so an event in a file the puller copied late
/// still ships. Resending is harmless: the event id makes Bugsink digest it once.
pub const OVERLAP: time::Duration = time::Duration::HOUR;

/// When history went quiet in Bugsink. Every issue first seen before it was replayed from old
/// transcripts by the first full ship, and `cutover` resolved those GitHub had already closed. The
/// tracker consumer never files an issue first seen before it. Issue first-seen is ingest time,
/// so this is just after the last new issue of that ship, read from the prod dry run.
pub const CUTOVER: OffsetDateTime = datetime!(2026-11-10 03:13:00 UTC);

pub const TIMEOUT: Duration = Duration::from_secs(10);

/// A project's DSN, `scheme://key@host[:port]/project`, split into what a send needs.
pub struct Dsn {
    base: String,
    project: String,
    store: String,
    key: String,
}

impl fmt::Debug for Dsn {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Dsn").field("store", &self.store).finish()
    }
}

impl Dsn {
    pub fn store_url(&self) -> &str {
        &self.store
    }

    /// `scheme://host[:port]`, where Bugsink's API lives. It names the tailnet host: never print it.
    pub fn base_url(&self) -> &str {
        &self.base
    }

    pub fn project(&self) -> &str {
        &self.project
    }
}

impl FromStr for Dsn {
    type Err = String;

    /// The error never carries the value, which holds the key.
    fn from_str(raw: &str) -> Result<Self, Self::Err> {
        let malformed = || "BUGSINK_DSN is not of the form scheme://key@host/project".to_string();
        let (scheme, rest) = raw.split_once("://").ok_or_else(malformed)?;
        let (key, place) = rest.split_once('@').ok_or_else(malformed)?;
        let (host, project) = place.rsplit_once('/').ok_or_else(malformed)?;
        let fine = matches!(scheme, "http" | "https")
            && !key.is_empty()
            && !host.is_empty()
            && !project.is_empty()
            && project.bytes().all(|b| b.is_ascii_digit());
        if !fine {
            return Err(malformed());
        }
        let base = format!("{scheme}://{host}");
        Ok(Dsn {
            store: format!("{base}/api/{project}/store/"),
            base,
            project: project.to_string(),
            key: key.to_string(),
        })
    }
}

/// 32 hex characters of SHA-256 over the transcript event id, so a resend repeats the id.
pub fn event_id(transcript_id: &str) -> String {
    Sha256::digest(transcript_id.as_bytes())
        .iter()
        .take(16)
        .map(|b| format!("{b:02x}"))
        .collect()
}

/// The Sentry event for one failed call. Bugsink groups by `fingerprint`, and keeps
/// `timestamp` as the event's own time.
pub fn event(e: &ToolError) -> Value {
    let class = e.text.narrow(|t| classify(t).name);
    json!({
        "event_id": event_id(&e.id),
        "timestamp": e.at.as_str(),
        "platform": "other",
        "level": "error",
        "logger": format!("{}.{}", e.service, e.tool),
        "message": e.text.reason(),
        "fingerprint": [e.service, e.tool, class],
        "tags": {
            "service": e.service,
            "tool": e.tool,
            "class": class,
            "surface": e.surface,
            "session": e.session.as_str(),
        },
    })
}

fn instant(at: &At) -> Option<OffsetDateTime> {
    OffsetDateTime::parse(at.as_str(), &Rfc3339).ok()
}

/// The errors a scan sends: all of them without a cursor, otherwise those no older than
/// `OVERLAP` before it. An error whose time does not parse is sent, since a resend costs nothing.
pub fn due<'a>(errors: &'a [ToolError], cursor: Option<&At>) -> Vec<&'a ToolError> {
    let Some(floor) = cursor.and_then(instant).map(|at| at - OVERLAP) else {
        return errors.iter().collect();
    };
    errors
        .iter()
        .filter(|e| instant(&e.at).is_none_or(|at| at >= floor))
        .collect()
}

/// The time of the newest event Bugsink has accepted, in a file. It only limits traffic: a lost
/// or garbled cursor resends history, which Bugsink digests once.
pub struct Cursor(pub PathBuf);

impl Cursor {
    pub fn load(&self) -> Result<Option<At>, StepError> {
        match fs::read_to_string(&self.0) {
            Ok(text) => Ok(At::parse(text.trim())),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(StepError(format!("cannot read {}: {e}", self.0.display()))),
        }
    }

    /// Writes beside the file and renames over it, so a crash leaves the old cursor or the new.
    pub fn save(&self, at: &At) -> Result<(), StepError> {
        let failed = |e: io::Error| StepError(format!("cannot write {}: {e}", self.0.display()));
        let next = self.0.with_extension("next");
        fs::write(&next, at.as_str()).map_err(failed)?;
        fs::rename(&next, &self.0).map_err(failed)
    }
}

#[derive(Debug, PartialEq, Eq)]
pub struct Shipped {
    pub due: usize,
    pub sent: usize,
}

pub struct Bugsink {
    http: reqwest::Client,
    dsn: Dsn,
    cursor: Cursor,
    /// A dry run counts what is due and sends nothing.
    live: bool,
}

/// Always provided, because almanac checks a scan's providers at startup. `None` means no DSN.
pub struct BugsinkClient(pub Option<Bugsink>);

impl Bugsink {
    pub fn new(dsn: Dsn, cursor: Cursor, live: bool) -> Bugsink {
        let http = reqwest::Client::builder()
            .timeout(TIMEOUT)
            .build()
            .expect("an HTTP client with a timeout builds");
        Bugsink {
            http,
            dsn,
            cursor,
            live,
        }
    }

    async fn send(&self, event: &Value) -> Result<(), StepError> {
        let auth = format!(
            "Sentry sentry_version=7, sentry_key={}, sentry_client=brined-almanac/{}",
            self.dsn.key,
            env!("CARGO_PKG_VERSION")
        );
        let response = self
            .http
            .post(&self.dsn.store)
            .header("X-Sentry-Auth", auth)
            .json(event)
            .send()
            .await
            .map_err(|e| StepError(format!("Bugsink unreachable: {e}")))?;
        let status = response.status();
        if !status.is_success() {
            return Err(StepError(format!("Bugsink answered {status}")));
        }
        Ok(())
    }

    /// Sends what is due, oldest first, and moves the cursor to the newest event accepted. A
    /// failure stops the send and keeps the cursor at the last success, so the next scan resumes.
    pub async fn ship(&self, errors: &[ToolError]) -> Result<Shipped, StepError> {
        let cursor = self.cursor.load()?;
        let due = due(errors, cursor.as_ref());
        let mut shipped = Shipped {
            due: due.len(),
            sent: 0,
        };
        if !self.live {
            return Ok(shipped);
        }
        let mut newest = cursor;
        let mut failure = None;
        for e in due {
            if let Err(err) = self.send(&event(e)).await {
                failure = Some(err);
                break;
            }
            shipped.sent += 1;
            if newest.as_ref().is_none_or(|at| e.at > *at) {
                newest = Some(e.at.clone());
            }
        }
        if shipped.sent > 0
            && let Some(at) = &newest
        {
            self.cursor.save(at)?;
        }
        match failure {
            Some(StepError(reason)) => Err(StepError(format!(
                "sent {} of {} event(s), then: {reason}",
                shipped.sent, shipped.due
            ))),
            None => Ok(shipped),
        }
    }
}

/// Ships `errors` if a DSN is set. A failure is printed, not returned, so Bugsink being down
/// never stops the scan that called this.
pub async fn ship_from(scanner: &Scanner, errors: &[ToolError]) -> Result<(), StepError> {
    let client = scanner.get::<BugsinkClient>()?;
    let Some(bugsink) = &client.0 else {
        eprintln!("transcript-shipper: BUGSINK_DSN is not set, skipping");
        return Ok(());
    };
    match bugsink.ship(errors).await {
        Ok(Shipped { due, .. }) if !bugsink.live => {
            eprintln!("transcript-shipper: dry run, {due} event(s) due, sent none");
        }
        Ok(Shipped { due, sent }) => {
            eprintln!("transcript-shipper: sent {sent} of {due} event(s)");
        }
        Err(StepError(reason)) => eprintln!("transcript-shipper: {reason}"),
    }
    Ok(())
}
