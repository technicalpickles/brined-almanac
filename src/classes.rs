use std::sync::LazyLock;

use almanac::prelude::*;
use regex::RegexSet;
use serde::Deserialize;

/// One row of `classes.toml`.
pub struct Class {
    pub name: &'static str,
    pattern: Option<&'static str>,
    /// Events an issue needs before it files. `None` never files.
    pub threshold: Option<u64>,
    pub why: &'static str,
}

impl Class {
    pub fn has_pattern(&self) -> bool {
        self.pattern.is_some()
    }
}

#[derive(Deserialize)]
struct Row {
    name: String,
    pattern: Option<String>,
    threshold: Option<u64>,
    why: String,
}

#[derive(Deserialize)]
struct Table {
    class: Vec<Row>,
}

/// First matching row wins. `other` is last and has no pattern. The strings are leaked once, so
/// the rows live as long as the process like the rest of the table.
pub static CLASSES: LazyLock<Vec<Class>> = LazyLock::new(|| {
    let table: Table =
        toml::from_str(include_str!("../classes.toml")).expect("classes.toml parses");
    let keep = |s: String| -> &'static str { Box::leak(s.into_boxed_str()) };
    table
        .class
        .into_iter()
        .map(|row| Class {
            name: keep(row.name),
            pattern: row.pattern.map(keep),
            threshold: row.threshold,
            why: keep(row.why),
        })
        .collect()
});

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
