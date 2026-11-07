use std::collections::HashMap;
use std::sync::LazyLock;

use almanac::prelude::*;
use regex::RegexSet;

use crate::transcript::{At, Report, Sample, ToolError};

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

pub fn report(key: &ClusterKey, cluster: &Cluster) -> Report {
    let header = format!(
        "service: {}\ntool: {}\nclass: {}\nday: {}\ncount: {}\nfirst: {}\nlast: {}",
        key.service,
        key.tool,
        key.class,
        key.day,
        cluster.count,
        cluster.first.as_str(),
        cluster.last.as_str(),
    );
    cluster.sample.compose(&header)
}
