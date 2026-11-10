use std::sync::LazyLock;

use almanac::prelude::*;
use regex::RegexSet;

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
