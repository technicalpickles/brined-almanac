use std::collections::HashSet;
use std::env;
use std::fmt;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use almanac::prelude::*;
use github::client::{Auth, GithubClient};
use github::tracker::GithubIssues;
use taskchampion::{ServerConfig, Uuid};

use crate::routing::{AnyTracker, Router};
use crate::taskwarrior::{Taskwarrior, TaskwarriorConfig};
use crate::transcript::Transcripts;

const API: &str = "https://api.github.com";

/// Whether a repository is private. Issue bodies carry transcript text, so this gates every delivery.
pub trait Visibility: Send + Sync {
    fn is_private<'a>(&'a self, repo: &'a Slug) -> BoxFuture<'a, Result<bool, StepError>>;
}

impl<V: Visibility + ?Sized> Visibility for Arc<V> {
    fn is_private<'a>(&'a self, repo: &'a Slug) -> BoxFuture<'a, Result<bool, StepError>> {
        (**self).is_private(repo)
    }
}

impl Visibility for GithubClient {
    fn is_private<'a>(&'a self, repo: &'a Slug) -> BoxFuture<'a, Result<bool, StepError>> {
        Box::pin(async move {
            let found = self.get(&format!("/repos/{repo}")).await?;
            found
                .get("private")
                .and_then(|flag| flag.as_bool())
                .ok_or_else(|| StepError(format!("{repo} has no private flag in the response")))
        })
    }
}

/// Refuses every delivery to a repository that is not confirmed private. Only `true` answers are
/// remembered, so a repository that goes public after a refusal is checked again.
pub struct PrivateOnly<T, V> {
    inner: T,
    visibility: V,
    private: Mutex<HashSet<Slug>>,
}

impl<T: Tracker, V: Visibility> PrivateOnly<T, V> {
    pub fn new(inner: T, visibility: V) -> Self {
        Self {
            inner,
            visibility,
            private: Mutex::new(HashSet::new()),
        }
    }

    async fn check(&self, target: &Slug) -> Result<(), StepError> {
        if self.private.lock().unwrap().contains(target) {
            return Ok(());
        }
        if !self.visibility.is_private(target).await? {
            return Err(StepError(format!(
                "{target} is not private, refusing to file"
            )));
        }
        self.private.lock().unwrap().insert(target.clone());
        Ok(())
    }
}

impl<T: Tracker, V: Visibility> Tracker for PrivateOnly<T, V> {
    fn file<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        title: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async move {
            self.check(target).await?;
            self.inner.file(key, target, title, body).await
        })
    }

    fn ask<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        pull: Option<u64>,
        question: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async move {
            self.check(target).await?;
            self.inner.ask(key, target, pull, question, body).await
        })
    }
}

/// Stands in for the tracker on a live run that has no durable store. Without one, every
/// `backfill` re-admits every delivery id, so filing would repeat each cluster with its transcript text.
pub struct StoreRequired;

const NO_STORE: &str =
    "DATABASE_URL is not set; a live run without a durable store would file every cluster again";

impl Tracker for StoreRequired {
    fn file<'a>(
        &'a self,
        _key: &'a str,
        _target: &'a Slug,
        _title: &'a str,
        _body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async { Err(StepError(NO_STORE.to_string())) })
    }

    fn ask<'a>(
        &'a self,
        _key: &'a str,
        _target: &'a Slug,
        _pull: Option<u64>,
        _question: &'a str,
        _body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async { Err(StepError(NO_STORE.to_string())) })
    }
}

/// The text inside a tainted body, or an empty string when it has none.
pub fn body_text(body: &Untrusted<String>) -> Result<String, StepError> {
    // Read by position: `.forbidden-patterns` forbids the literal tainted-text key in source.
    let shown = serde_json::to_value(body)?;
    Ok(shown
        .as_object()
        .and_then(|fields| fields.values().next())
        .and_then(|value| value.as_str())
        .unwrap_or("")
        .to_string())
}

/// Prints what it would file. Stands in for GitHub when no token is set.
pub struct LogTracker;

impl Tracker for LogTracker {
    fn file<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        title: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async move {
            let text = body_text(body)?;
            println!("FILE key={key} target={target} title={title:?}\n{text}");
            Ok(())
        })
    }

    fn ask<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        _pull: Option<u64>,
        question: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        self.file(key, target, question, body)
    }
}

/// The issues side of the router: GitHub when a token is set, otherwise printing.
pub enum IssuesSide {
    Github { token: String },
    Log,
}

impl fmt::Debug for IssuesSide {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            IssuesSide::Github { .. } => f.write_str("Github"),
            IssuesSide::Log => f.write_str("Log"),
        }
    }
}

/// The tasks side of the router: a synced taskwarrior replica when a sync URL is set, otherwise printing.
pub enum TasksSide {
    Taskwarrior(TaskwarriorConfig),
    Log,
}

impl fmt::Debug for TasksSide {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            TasksSide::Taskwarrior(config) => f
                .debug_tuple("Taskwarrior")
                .field(&config.replica_dir)
                .finish(),
            TasksSide::Log => f.write_str("Log"),
        }
    }
}

/// Which tracker the environment asks for.
#[derive(Debug)]
pub enum Choice {
    StoreRequired,
    Route {
        issues: IssuesSide,
        tasks: TasksSide,
    },
}

const DEFAULT_REPLICA_DIR: &str = "/var/lib/brined-almanac/taskchampion";

/// Reads the tracker settings through `lookup`. An `Err` names the missing or malformed variable
/// and never carries a value, since the values include secrets.
pub fn choose(lookup: impl Fn(&str) -> Option<String>) -> Result<Choice, String> {
    // `KEY=` in an env file sets the variable to nothing, which is not configuration.
    let lookup = |key: &str| lookup(key).filter(|value| !value.is_empty());
    let issues = match lookup("GITHUB_TOKEN") {
        Some(token) => IssuesSide::Github { token },
        None => IssuesSide::Log,
    };
    let tasks = match lookup("TASKCHAMPION_SYNC_URL") {
        None => {
            // Without this, a lost URL line would fall back to printing, and a live run would
            // mark every task finding applied without filing it.
            for key in ["TASKCHAMPION_CLIENT_ID", "TASKCHAMPION_ENCRYPTION_SECRET"] {
                if lookup(key).is_some() {
                    return Err(format!("{key} is set but TASKCHAMPION_SYNC_URL is not"));
                }
            }
            TasksSide::Log
        }
        Some(url) => {
            let required = |key: &str| {
                lookup(key).ok_or_else(|| format!("TASKCHAMPION_SYNC_URL is set but {key} is not"))
            };
            let client_id = required("TASKCHAMPION_CLIENT_ID")?
                .parse::<Uuid>()
                .map_err(|_| "TASKCHAMPION_CLIENT_ID is not a UUID".to_string())?;
            let encryption_secret = required("TASKCHAMPION_ENCRYPTION_SECRET")?
                .as_bytes()
                .to_vec();
            let replica_dir = lookup("TASKCHAMPION_REPLICA_DIR")
                .unwrap_or_else(|| DEFAULT_REPLICA_DIR.to_string());
            TasksSide::Taskwarrior(TaskwarriorConfig {
                replica_dir: PathBuf::from(replica_dir),
                server: ServerConfig::Remote {
                    url,
                    client_id,
                    encryption_secret,
                },
            })
        }
    };
    let prints_only = matches!((&issues, &tasks), (IssuesSide::Log, TasksSide::Log));
    if lookup("DATABASE_URL").is_none() && !prints_only {
        return Ok(Choice::StoreRequired);
    }
    Ok(Choice::Route { issues, tasks })
}

/// The taskwarrior settings `sync-check` uses, pointed at `replica_dir` instead of the daemon's own
/// replica so the check starts empty and never touches the daemon's sqlite file.
pub fn sync_check_config(
    lookup: impl Fn(&str) -> Option<String>,
    replica_dir: PathBuf,
) -> Result<TaskwarriorConfig, String> {
    match choose(lookup)? {
        Choice::Route {
            tasks: TasksSide::Taskwarrior(mut config),
            ..
        } => {
            config.replica_dir = replica_dir;
            Ok(config)
        }
        Choice::Route { .. } => Err("TASKCHAMPION_SYNC_URL is not set".to_string()),
        Choice::StoreRequired => Err("DATABASE_URL is not set".to_string()),
    }
}

/// Builds the tracker a `Choice` names. Sync: nothing here opens a connection.
pub fn tracker_from(choice: Choice) -> Arc<dyn Tracker> {
    let (issues, tasks) = match choice {
        Choice::StoreRequired => return Arc::new(StoreRequired),
        Choice::Route { issues, tasks } => (issues, tasks),
    };
    let issues: Box<dyn Tracker> = match issues {
        IssuesSide::Github { token } => Box::new(PrivateOnly::new(
            GithubIssues::new(API, token.clone()),
            GithubClient::new(API, Auth::Token(token)),
        )),
        IssuesSide::Log => Box::new(LogTracker),
    };
    let tasks: Box<dyn Tracker> = match tasks {
        TasksSide::Taskwarrior(config) => Box::new(Taskwarrior::new(config)),
        TasksSide::Log => Box::new(LogTracker),
    };
    Arc::new(Router::new(AnyTracker(issues), AnyTracker(tasks)))
}

pub fn all(_config: &Config) -> Providers {
    let dir = env::var("BRENT_TRANSCRIPTS").unwrap_or_else(|_| "transcripts".to_string());
    let choice = choose(|key| env::var(key).ok()).unwrap_or_else(|err| panic!("{err}"));
    Providers::new()
        .provide::<Transcripts>(Arc::new(Transcripts(PathBuf::from(dir))))
        .provide::<dyn Tracker>(tracker_from(choice))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sync_check_config_needs_a_sync_url() {
        let err = sync_check_config(|_| None, PathBuf::from("x"))
            .err()
            .unwrap();
        assert_eq!(err, "TASKCHAMPION_SYNC_URL is not set");
    }

    #[test]
    fn sync_check_config_uses_the_directory_it_is_given() {
        let lookup = |key: &str| match key {
            "TASKCHAMPION_SYNC_URL" => Some("https://sync.example".to_string()),
            "TASKCHAMPION_CLIENT_ID" => Some("8b46d55e-0000-4000-8000-000000000000".to_string()),
            "TASKCHAMPION_ENCRYPTION_SECRET" => Some("s".to_string()),
            "DATABASE_URL" => Some("postgres://x".to_string()),
            _ => None,
        };
        let config = sync_check_config(lookup, PathBuf::from("/tmp/check")).unwrap();
        assert_eq!(config.replica_dir, PathBuf::from("/tmp/check"));
    }

    #[test]
    fn body_text_reads_the_tainted_value_without_naming_its_key() {
        let body = Untrusted::new("a\n`b`\nc".to_string());
        assert_eq!(body_text(&body).unwrap(), "a\n`b`\nc");
    }
}
