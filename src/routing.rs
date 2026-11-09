//! Routing between GitHub issue targets and taskwarrior projects.

use std::str::FromStr;

use almanac::prelude::*;

pub const TASK_PREFIX: &str = "task:";

/// The name of a taskwarrior project: dotted segments of `[a-z0-9_-]`.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct Project(String);

impl FromStr for Project {
    type Err = String;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        if s.is_empty() {
            return Err("project name is empty".to_string());
        }
        let max = 200 - TASK_PREFIX.len();
        if s.len() > max {
            return Err(format!("project name is longer than {max} bytes"));
        }
        for segment in s.split('.') {
            if segment.is_empty() {
                return Err(format!("project {s:?} has an empty dotted segment"));
            }
            if !segment
                .bytes()
                .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'_' || b == b'-')
            {
                return Err(format!(
                    "project {s:?} has a segment outside [a-z0-9_-]: {segment:?}"
                ));
            }
        }
        Ok(Project(s.to_string()))
    }
}

impl Project {
    pub fn as_str(&self) -> &str {
        &self.0
    }

    /// The `task:<project>` target slug for this project.
    pub fn target(&self) -> Slug {
        format!("{TASK_PREFIX}{}", self.0)
            .parse()
            .expect("a validated project name always forms a valid slug")
    }

    /// `None` when the slug has no `task:` prefix; `Some(Err)` when it has
    /// the prefix but the project part is invalid.
    pub fn from_target(slug: &Slug) -> Option<Result<Project, String>> {
        slug.to_string().strip_prefix(TASK_PREFIX).map(str::parse)
    }
}

/// Sends `task:<project>` targets to the tasks tracker and everything else to the issues tracker.
pub struct Router<I, T> {
    issues: I,
    tasks: T,
}

impl<I: Tracker, T: Tracker> Router<I, T> {
    pub fn new(issues: I, tasks: T) -> Self {
        Self { issues, tasks }
    }
}

impl<I: Tracker, T: Tracker> Tracker for Router<I, T> {
    fn file<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        title: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        match Project::from_target(target) {
            None => self.issues.file(key, target, title, body),
            Some(Ok(_)) => self.tasks.file(key, target, title, body),
            Some(Err(why)) => Box::pin(async move { Err(not_a_task_target(target, &why)) }),
        }
    }

    fn ask<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        pull: Option<u64>,
        question: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        match Project::from_target(target) {
            None => self.issues.ask(key, target, pull, question, body),
            Some(Ok(_)) => self.tasks.ask(key, target, pull, question, body),
            Some(Err(why)) => Box::pin(async move { Err(not_a_task_target(target, &why)) }),
        }
    }
}

/// A boxed tracker, so the router's two sides can be chosen at runtime. The orphan rule rules
/// out implementing almanac's `Tracker` for `Box` or `Arc` directly.
pub struct AnyTracker(pub Box<dyn Tracker>);

impl Tracker for AnyTracker {
    fn file<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        title: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        self.0.file(key, target, title, body)
    }

    fn ask<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        pull: Option<u64>,
        question: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        self.0.ask(key, target, pull, question, body)
    }
}

fn not_a_task_target(target: &Slug, why: &str) -> StepError {
    StepError(format!("{target} is not a task target: {why}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn real_project_names_round_trip() {
        for name in [
            "picklehome",
            "homelab.backup",
            "pickled-claude-plugins.hk",
            "almanac",
        ] {
            let project: Project = name.parse().unwrap();
            assert_eq!(project.target().to_string(), format!("task:{name}"));
            assert_eq!(
                Project::from_target(&project.target())
                    .unwrap()
                    .unwrap()
                    .as_str(),
                name
            );
        }
    }

    #[test]
    fn rejects_names_taskwarrior_would_take_but_filters_would_not() {
        for bad in [
            "Bad Name",
            "has/slash",
            "has:colon",
            "UPPER",
            "trail.",
            ".lead",
            "a..b",
            "",
        ] {
            assert!(bad.parse::<Project>().is_err(), "{bad:?} parsed");
        }
    }

    #[test]
    fn a_github_target_is_not_a_task_target() {
        let slug: Slug = "technicalpickles/brineworks".parse().unwrap();
        assert!(Project::from_target(&slug).is_none());
    }

    #[test]
    fn a_task_prefix_with_a_bad_project_is_an_error_not_a_miss() {
        let slug: Slug = "task:Bad".parse().unwrap();
        assert!(matches!(Project::from_target(&slug), Some(Err(_))));
    }

    #[test]
    fn a_bare_task_prefix_is_an_error() {
        let slug: Slug = "task:".parse().unwrap();
        assert!(matches!(Project::from_target(&slug), Some(Err(_))));
    }
}
