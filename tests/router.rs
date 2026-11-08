use almanac::prelude::*;
use almanac::testing::FakeTracker;
use brined_almanac::routing::Router;

fn body() -> Untrusted<String> {
    Untrusted::new("synthetic body".to_string())
}

fn sides() -> (FakeTracker, FakeTracker, Router<FakeTracker, FakeTracker>) {
    let (issues, tasks) = (FakeTracker::default(), FakeTracker::default());
    let router = Router::new(issues.clone(), tasks.clone());
    (issues, tasks, router)
}

#[tokio::test]
async fn a_task_target_goes_to_the_tasks_side_with_everything_intact() {
    let (issues, tasks, router) = sides();
    let target: Slug = "task:picklehome".parse().unwrap();
    router
        .file("run-1:0", &target, "title", &body())
        .await
        .unwrap();
    assert_eq!(
        tasks.filed.lock().unwrap()[0],
        (
            "run-1:0".to_string(),
            "task:picklehome".to_string(),
            "title".to_string()
        )
    );
    assert!(issues.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn a_repository_target_goes_to_the_issues_side() {
    let (issues, tasks, router) = sides();
    let target: Slug = "technicalpickles/brineworks".parse().unwrap();
    router
        .file("run-1:0", &target, "title", &body())
        .await
        .unwrap();
    assert_eq!(
        issues.filed.lock().unwrap()[0],
        (
            "run-1:0".to_string(),
            "technicalpickles/brineworks".to_string(),
            "title".to_string()
        )
    );
    assert!(tasks.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn ask_routes_the_same_way_and_keeps_the_pull() {
    let (issues, tasks, router) = sides();
    let target: Slug = "task:picklehome".parse().unwrap();
    router
        .ask("k", &target, Some(7), "question?", &body())
        .await
        .unwrap();
    assert_eq!(tasks.calls.lock().unwrap().len(), 1);
    assert_eq!(
        tasks.filed.lock().unwrap()[0],
        (
            "k".to_string(),
            "task:picklehome".to_string(),
            "question?".to_string()
        )
    );
    assert!(issues.calls.lock().unwrap().is_empty());

    let repo: Slug = "technicalpickles/brineworks".parse().unwrap();
    router
        .ask("k2", &repo, Some(7), "question?", &body())
        .await
        .unwrap();
    assert_eq!(issues.calls.lock().unwrap().len(), 1);
    assert_eq!(tasks.calls.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn a_malformed_task_target_is_refused_before_either_side() {
    let (issues, tasks, router) = sides();
    let target: Slug = "task:Bad".parse().unwrap();
    let err = router
        .file("k", &target, "title", &body())
        .await
        .unwrap_err();
    assert!(err.0.contains("task:Bad"), "{}", err.0);
    assert!(!err.0.contains("synthetic body"));
    assert!(issues.filed.lock().unwrap().is_empty() && tasks.filed.lock().unwrap().is_empty());
    let err = router
        .ask("k", &target, None, "q?", &body())
        .await
        .unwrap_err();
    assert!(err.0.contains("task:Bad"), "{}", err.0);
    assert!(issues.calls.lock().unwrap().is_empty() && tasks.calls.lock().unwrap().is_empty());
}
