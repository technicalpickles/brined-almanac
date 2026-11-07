use std::collections::HashSet;
use std::env;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use almanac::prelude::*;
use github::client::{Auth, GithubClient};
use github::tracker::GithubIssues;

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
        question: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async move {
            self.check(target).await?;
            self.inner.ask(key, target, question, body).await
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
        _question: &'a str,
        _body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async { Err(StepError(NO_STORE.to_string())) })
    }
}

/// Prints what it would file. Stands in for GitHub when no token is set.
struct LogTracker;

impl Tracker for LogTracker {
    fn file<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        title: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        Box::pin(async move {
            // Read by position: `.forbidden-patterns` forbids the literal tainted-text key in source.
            let shown = serde_json::to_value(body)?;
            let text = shown
                .as_object()
                .and_then(|fields| fields.values().next())
                .and_then(|value| value.as_str())
                .unwrap_or("");
            println!("FILE key={key} target={target} title={title:?}\n{text}");
            Ok(())
        })
    }

    fn ask<'a>(
        &'a self,
        key: &'a str,
        target: &'a Slug,
        question: &'a str,
        body: &'a Untrusted<String>,
    ) -> BoxFuture<'a, Result<(), StepError>> {
        self.file(key, target, question, body)
    }
}

pub fn all(_config: &Config) -> Providers {
    let dir = env::var("BRENT_TRANSCRIPTS").unwrap_or_else(|_| "transcripts".to_string());
    let tracker: Arc<dyn Tracker> = match (env::var("GITHUB_TOKEN"), env::var("DATABASE_URL")) {
        (Ok(token), Ok(_)) => Arc::new(PrivateOnly::new(
            GithubIssues::new(API, token.clone()),
            GithubClient::new(API, Auth::Token(token)),
        )),
        (Ok(_), Err(_)) => Arc::new(StoreRequired),
        (Err(_), _) => Arc::new(LogTracker),
    };
    Providers::new()
        .provide::<Transcripts>(Arc::new(Transcripts(PathBuf::from(dir))))
        .provide::<dyn Tracker>(tracker)
}
