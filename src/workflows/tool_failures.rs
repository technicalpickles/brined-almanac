use std::collections::HashMap;
use std::sync::LazyLock;

use almanac::prelude::*;
use regex::RegexSet;
use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::bugsink::ship_from;
use crate::transcript::{At, Sample, ToolError, Transcripts, read};

pub struct Class {
    pub name: &'static str,
    pattern: Option<&'static str>,
    pub threshold: u64,
}

/// First matching row wins. `other` is last and has no pattern.
pub static CLASSES: &[Class] = &[
    Class {
        name: "mcp-timeout",
        pattern: Some("MCP error -32001"),
        threshold: 1,
    },
    Class {
        name: "pg-too-many-clients",
        pattern: Some("too many clients already"),
        threshold: 1,
    },
    Class {
        name: "approval-timeout",
        pattern: Some("Approval timed out"),
        threshold: 3,
    },
    Class {
        name: "unknown-tool-id",
        pattern: Some("Unknown tool id"),
        threshold: 5,
    },
    Class {
        name: "arg-validation",
        pattern: Some("validation error|Validation failed|Missing required argument"),
        threshold: 5,
    },
    Class {
        name: "other",
        pattern: None,
        threshold: 3,
    },
];

/// Maps a `RegexSet` index back to its row, since `other` has no pattern in the set.
static MATCHER: LazyLock<(RegexSet, Vec<&'static Class>)> = LazyLock::new(|| {
    let rows: Vec<&'static Class> = CLASSES.iter().filter(|c| c.pattern.is_some()).collect();
    let set = RegexSet::new(rows.iter().filter_map(|c| c.pattern)).expect("class patterns");
    (set, rows)
});

fn other() -> &'static Class {
    CLASSES.last().expect("other is the last row")
}

pub fn classify(text: &str) -> &'static Class {
    let (set, rows) = &*MATCHER;
    set.matches(text)
        .iter()
        .next()
        .map_or_else(other, |i| rows[i])
}

pub fn class_named(name: &str) -> &'static Class {
    CLASSES
        .iter()
        .find(|c| c.name == name)
        .unwrap_or_else(other)
}

pub fn owner_of(service: &Slug) -> Slug {
    let repo = if service.to_string() == "brineworks" {
        "technicalpickles/brineworks"
    } else {
        "technicalpickles/pickleclaw"
    };
    repo.parse().expect("owner is a slug")
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct ClusterKey {
    pub service: Slug,
    pub tool: Slug,
    pub class: &'static str,
    pub day: Slug,
}

impl ClusterKey {
    pub fn delivery_id(&self) -> String {
        format!(
            "scan:brent/{}/{}/{}/{}",
            self.service, self.tool, self.class, self.day
        )
    }
}

#[derive(Debug)]
pub struct Cluster {
    pub count: u64,
    pub first: At,
    pub last: At,
    pub sample: Sample,
}

/// `errors` arrive sorted by time, so the first error of a key supplies `first` and `sample`.
pub fn cluster(errors: Vec<ToolError>) -> Vec<(ClusterKey, Cluster)> {
    let mut found: HashMap<ClusterKey, Cluster> = HashMap::new();
    for e in errors {
        let key = ClusterKey {
            service: e.service,
            tool: e.tool,
            class: e.text.narrow(|t| classify(t).name),
            day: e.at.day(),
        };
        match found.get_mut(&key) {
            Some(c) => {
                c.count += 1;
                c.last = e.at;
            }
            None => {
                let c = Cluster {
                    count: 1,
                    first: e.at.clone(),
                    last: e.at,
                    sample: e.text,
                };
                found.insert(key, c);
            }
        }
    }
    let mut out: Vec<_> = found.into_iter().collect();
    out.sort_by_cached_key(|(k, _)| k.delivery_id());
    out
}

observed! {
    /// One cluster of failed tool calls. `sample` is transcript text, so it is tainted.
    pub struct ToolFailed {
        pub service: Slug,
        pub tool: Slug,
        pub class: Slug,
        pub day: Slug,
        pub count: u64,
        pub first: Slug,
        pub last: Slug,
        pub sample: Untrusted<String>,
    }
}

impl Event for ToolFailed {
    const KIND: &'static str = "brent.tool.failed";
    fn parse(p: &Payload<'_>) -> Result<Self, Malformed> {
        Ok(Self {
            service: p.field("/service")?,
            tool: p.field("/tool")?,
            class: p.field("/class")?,
            day: p.field("/day")?,
            count: p.field("/count")?,
            first: p.field("/first")?,
            last: p.field("/last")?,
            sample: p.text("/sample")?,
        })
    }
}

#[scan]
pub async fn scan(scanner: &Scanner) -> Result<Vec<Scanned<ToolFailed>>, StepError> {
    let dir = scanner.get::<Transcripts>()?;
    let read = read(&dir.0)?;
    let shown = dir.0.display();
    if read.skipped.not_json > 0 {
        eprintln!(
            "{shown}: skipped {} lines that were not JSON",
            read.skipped.not_json
        );
    }
    if read.skipped.undated > 0 {
        eprintln!(
            "{shown}: skipped {} lines with no usable timestamp",
            read.skipped.undated
        );
    }
    ship_from(scanner, &read.errors).await?;
    Ok(cluster(read.errors)
        .into_iter()
        .map(|(key, c)| {
            let payload = json!({
                "service": key.service,
                "tool": key.tool,
                "class": key.class.parse::<Slug>().expect("class names are slugs"),
                "day": key.day,
                "count": c.count,
                "first": c.first.as_str(),
                "last": c.last.as_str(),
                "sample": c.sample.excerpt(),
            });
            Scanned::new(key.delivery_id(), payload)
        })
        .collect())
}

#[derive(Serialize, Deserialize)]
pub enum Facts {
    Quiet,
    Loud {
        target: Slug,
        service: Slug,
        tool: Slug,
        class: Slug,
        day: Slug,
        count: u64,
        body: Untrusted<String>,
    },
}

pub fn facts(event: &ToolFailed) -> Facts {
    if event.count < class_named(&event.class.to_string()).threshold {
        return Facts::Quiet;
    }
    let header = format!(
        "service: {}\ntool: {}\nclass: {}\nday: {}\ncount: {}\nfirst: {}\nlast: {}\n\nsample:\n",
        event.service, event.tool, event.class, event.day, event.count, event.first, event.last,
    );
    Facts::Loud {
        target: owner_of(&event.service),
        service: event.service.clone(),
        tool: event.tool.clone(),
        class: event.class.clone(),
        day: event.day.clone(),
        count: event.count,
        body: Untrusted::join(&header, &event.sample),
    }
}

#[workflow(
    on = ToolFailed,
    key = "{service}:{tool}:{class}:{day}",
    intent = "File one issue per cluster of failed tool calls in Brent's transcripts.",
    may = [File],
    runs_per_hour = 1000,
    scan = scan,
    decide = policy,
)]
pub async fn tool_failures(_cx: &Cx, event: &ToolFailed) -> Result<Facts, Halt> {
    Ok(facts(event))
}

pub fn policy(facts: &Facts) -> Vec<Effect> {
    match facts {
        Facts::Quiet => vec![],
        Facts::Loud {
            target,
            service,
            tool,
            class,
            day,
            count,
            body,
        } => vec![Effect::File {
            title: title!(
                "Brent: {}/{} {} on {} (count {})",
                service,
                tool,
                class,
                day,
                count
            ),
            target: target.clone(),
            body: body.map(|s| s.clone()),
        }],
    }
}
