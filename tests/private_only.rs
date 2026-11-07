use std::sync::{Arc, Mutex};

use almanac::prelude::*;
use almanac::testing::FakeTracker;
use brined_almanac::providers::{PrivateOnly, StoreRequired, Visibility};

struct FakeVisibility {
    answer: Result<bool, String>,
    asked: Mutex<Vec<String>>,
}

impl FakeVisibility {
    fn new(answer: Result<bool, String>) -> Self {
        Self {
            answer,
            asked: Mutex::new(Vec::new()),
        }
    }
}

impl Visibility for FakeVisibility {
    fn is_private<'a>(&'a self, repo: &'a Slug) -> BoxFuture<'a, Result<bool, StepError>> {
        Box::pin(async move {
            self.asked.lock().unwrap().push(repo.to_string());
            self.answer.clone().map_err(StepError)
        })
    }
}

fn target() -> Slug {
    "technicalpickles/brineworks".parse().unwrap()
}

fn body() -> Untrusted<String> {
    Untrusted::new("synthetic body".to_string())
}

#[tokio::test]
async fn refuses_a_public_target_and_files_nothing() {
    let inner = FakeTracker::default();
    let tracker = PrivateOnly::new(inner.clone(), FakeVisibility::new(Ok(false)));
    let err = tracker
        .file("k1", &target(), "title", &body())
        .await
        .unwrap_err();
    assert!(err.0.contains("technicalpickles/brineworks"), "{}", err.0);
    assert!(!err.0.contains("synthetic body"));
    assert!(inner.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn files_on_a_private_target() {
    let inner = FakeTracker::default();
    let tracker = PrivateOnly::new(inner.clone(), FakeVisibility::new(Ok(true)));
    tracker
        .file("k1", &target(), "title", &body())
        .await
        .unwrap();
    assert_eq!(inner.filed.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn a_failed_lookup_files_nothing() {
    let inner = FakeTracker::default();
    let tracker = PrivateOnly::new(inner.clone(), FakeVisibility::new(Err("boom".to_string())));
    let err = tracker
        .file("k1", &target(), "title", &body())
        .await
        .unwrap_err();
    assert!(err.0.contains("boom"));
    assert!(inner.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn asks_once_for_a_private_target() {
    let visibility = Arc::new(FakeVisibility::new(Ok(true)));
    let tracker = PrivateOnly::new(FakeTracker::default(), visibility.clone());
    tracker.file("k1", &target(), "a", &body()).await.unwrap();
    tracker.file("k2", &target(), "b", &body()).await.unwrap();
    assert_eq!(visibility.asked.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn asks_again_after_a_public_answer() {
    let visibility = Arc::new(FakeVisibility::new(Ok(false)));
    let tracker = PrivateOnly::new(FakeTracker::default(), visibility.clone());
    assert!(tracker.file("k1", &target(), "a", &body()).await.is_err());
    assert!(tracker.file("k2", &target(), "b", &body()).await.is_err());
    assert_eq!(visibility.asked.lock().unwrap().len(), 2);
}

#[tokio::test]
async fn ask_is_guarded_like_file() {
    let inner = FakeTracker::default();
    let tracker = PrivateOnly::new(inner.clone(), FakeVisibility::new(Ok(false)));
    let err = tracker
        .ask("k1", &target(), "question?", &body())
        .await
        .unwrap_err();
    assert!(err.0.contains("technicalpickles/brineworks"));
    assert!(inner.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn a_live_run_without_a_store_refuses_to_file_or_ask() {
    let filed = StoreRequired
        .file("k1", &target(), "title", &body())
        .await
        .unwrap_err();
    let asked = StoreRequired
        .ask("k1", &target(), "question?", &body())
        .await
        .unwrap_err();
    for err in [filed, asked] {
        assert!(err.0.contains("DATABASE_URL"), "{}", err.0);
        assert!(!err.0.contains("synthetic body"));
        assert!(!err.0.contains("technicalpickles/brineworks"));
    }
}

#[tokio::test]
async fn a_failed_lookup_is_asked_again() {
    let visibility = Arc::new(FakeVisibility::new(Err("boom".to_string())));
    let inner = FakeTracker::default();
    let tracker = PrivateOnly::new(inner.clone(), visibility.clone());
    assert!(tracker.file("k1", &target(), "a", &body()).await.is_err());
    assert!(tracker.file("k2", &target(), "b", &body()).await.is_err());
    assert_eq!(visibility.asked.lock().unwrap().len(), 2);
    assert!(inner.filed.lock().unwrap().is_empty());
}
