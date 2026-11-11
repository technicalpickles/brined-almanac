use std::path::Path;

use almanac::prelude::*;
use brined_almanac::taskwarrior::{
    KEY_ATTR, PULL_ATTR, TARGET_ATTR, Taskwarrior, TaskwarriorConfig, sync_check,
};
use taskchampion::{ServerConfig, TaskData};
use tempfile::TempDir;

fn local(dir: &Path) -> ServerConfig {
    ServerConfig::Local {
        server_dir: dir.join("server"),
    }
}

fn open(dir: &Path, name: &str) -> Taskwarrior {
    Taskwarrior::new(TaskwarriorConfig {
        replica_dir: dir.join(name),
        server: local(dir),
    })
}

fn body(text: &str) -> Untrusted<String> {
    Untrusted::new(text.to_string())
}

fn target() -> Slug {
    "task:picklehome".parse().unwrap()
}

fn task_with<'a>(tasks: &'a [TaskData], key: &str) -> Option<&'a TaskData> {
    tasks.iter().find(|task| task.get(KEY_ATTR) == Some(key))
}

fn annotation(task: &TaskData) -> &str {
    task.iter()
        .find(|(k, _)| k.starts_with("annotation_"))
        .map(|(_, v)| v.as_str())
        .unwrap()
}

#[tokio::test]
async fn a_filing_reaches_the_other_replica_with_its_shape() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    let target = target();
    container
        .file(
            "run-1:0",
            &target,
            "Alerts open on picklehome",
            &body("line one\n\n```\nfenced\n```"),
        )
        .await
        .unwrap();
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let task = task_with(&tasks, "run-1:0").expect("the task synced");
    assert_eq!(task.get("description"), Some("Alerts open on picklehome"));
    assert_eq!(task.get("project"), Some("picklehome"));
    assert_eq!(task.get("status"), Some("pending"));
    assert!(task.has("tag_almanac"));
    assert_eq!(task.get(TARGET_ATTR), Some("task:picklehome"));
    assert_eq!(
        annotation(task),
        "almanac run-1:0\nline one\n\n```\nfenced\n```"
    );
    assert!(task.get("entry").is_some() && task.get("modified").is_some());
}

#[tokio::test]
async fn the_same_key_twice_files_once() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    let target = target();
    for _ in 0..2 {
        container
            .file("run-1:0", &target, "title", &body("synthetic body"))
            .await
            .unwrap();
    }
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let keyed = tasks
        .iter()
        .filter(|task| task.get(KEY_ATTR) == Some("run-1:0"))
        .count();
    assert_eq!(keyed, 1);
    assert_eq!(tasks.len(), 1);
    assert_eq!(container.tasks().await.unwrap().len(), 1);
}

#[tokio::test]
async fn a_different_key_adds_exactly_one() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    let target = target();
    for key in ["run-1:0", "run-1:1"] {
        container
            .file(key, &target, "title", &body("synthetic body"))
            .await
            .unwrap();
    }
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    assert_eq!(tasks.len(), 2);
    assert!(task_with(&tasks, "run-1:0").is_some());
    assert!(task_with(&tasks, "run-1:1").is_some());
}

#[tokio::test]
async fn a_task_the_mac_already_has_is_not_filed_again() {
    // The lost-volume case: the container's first sync learns of the task before it looks for it.
    let dir = TempDir::new().unwrap();
    let mac = open(dir.path(), "mac");
    let target = target();
    mac.file("run-9:0", &target, "title", &body("synthetic body"))
        .await
        .unwrap();
    let container = open(dir.path(), "container");
    container
        .file("run-9:0", &target, "title", &body("synthetic body"))
        .await
        .unwrap();
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let keyed = tasks
        .iter()
        .filter(|task| task.get(KEY_ATTR) == Some("run-9:0"))
        .count();
    assert_eq!(keyed, 1);
}

#[tokio::test]
async fn a_long_body_lands_verbatim() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    let target = target();
    let long: String = "`a``b```c\n".repeat(400);
    assert_eq!(long.len(), 4000);
    container
        .file("run-2:0", &target, "Long one", &body(&long))
        .await
        .unwrap();
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let task = task_with(&tasks, "run-2:0").expect("the task synced");
    assert_eq!(annotation(task), format!("almanac run-2:0\n{long}"));
    assert_eq!(task.get("description"), Some("Long one"));
}

#[tokio::test]
async fn an_unreachable_server_writes_nothing() {
    // server_dir is a regular file, so the local server fails to open or sync.
    let dir = TempDir::new().unwrap();
    std::fs::write(dir.path().join("server"), "not a directory").unwrap();
    let container = open(dir.path(), "container");
    let target = target();
    let err = container
        .file("run-1:0", &target, "title", &body("synthetic body"))
        .await
        .unwrap_err();
    assert!(!err.0.contains("synthetic body"));
    assert!(container.tasks().await.unwrap().is_empty());
}

#[tokio::test]
async fn an_ask_is_a_question_task_with_its_pull() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    container
        .ask("run-2:0", &target(), Some(42), "Merge this?", &body("why"))
        .await
        .unwrap();
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let task = task_with(&tasks, "run-2:0").expect("the task synced");
    assert_eq!(task.get("description"), Some("Merge this?"));
    assert!(task.has("tag_almanac") && task.has("tag_question"));
    assert_eq!(task.get(PULL_ATTR), Some("42"));
}

#[tokio::test]
async fn an_ask_without_a_pull_has_no_pull_attribute() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    container
        .ask("run-3:0", &target(), None, "Anything else?", &body("why"))
        .await
        .unwrap();
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let task = task_with(&tasks, "run-3:0").expect("the task synced");
    assert!(task.has("tag_almanac") && task.has("tag_question"));
    assert_eq!(task.get(PULL_ATTR), None);
}

fn checker(dir: &Path) -> TaskwarriorConfig {
    TaskwarriorConfig {
        replica_dir: dir.join("check"),
        server: local(dir),
    }
}

#[tokio::test]
async fn a_sync_check_pulls_the_existing_history_into_a_fresh_replica() {
    let dir = TempDir::new().unwrap();
    let mac = open(dir.path(), "mac");
    mac.file("run-1:0", &target(), "Alerts open", &body("b"))
        .await
        .unwrap();
    mac.file("run-1:1", &target(), "More alerts", &body("b"))
        .await
        .unwrap();
    assert_eq!(sync_check(checker(dir.path())).await.unwrap(), 2);
}

#[tokio::test]
async fn a_sync_check_writes_nothing_to_the_server() {
    let dir = TempDir::new().unwrap();
    let mac = open(dir.path(), "mac");
    mac.file("run-1:0", &target(), "Alerts open", &body("b"))
        .await
        .unwrap();
    sync_check(checker(dir.path())).await.unwrap();
    let other = open(dir.path(), "other");
    other.sync().await.unwrap();
    assert_eq!(other.tasks().await.unwrap().len(), 1);
}

#[tokio::test]
async fn a_sync_check_against_an_empty_history_fails_because_nothing_was_decrypted() {
    let dir = TempDir::new().unwrap();
    let err = sync_check(checker(dir.path())).await.unwrap_err();
    assert!(err.contains("empty"), "{err}");
}

#[tokio::test]
async fn a_sync_check_against_an_unreachable_server_fails() {
    let dir = TempDir::new().unwrap();
    std::fs::write(dir.path().join("server"), "not a directory").unwrap();
    assert!(sync_check(checker(dir.path())).await.is_err());
}

fn annotations(task: &TaskData) -> Vec<&str> {
    task.iter()
        .filter(|(k, _)| k.starts_with("annotation_"))
        .map(|(_, v)| v.as_str())
        .collect()
}

fn filed(keys: &[&str]) -> Resolving {
    Resolving::Filed(keys.iter().map(|k| k.to_string()).collect())
}

#[tokio::test]
async fn resolving_filed_work_completes_only_those_tasks_with_the_evidence() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    let target = target();
    for key in ["run-1:0", "run-2:0"] {
        container
            .file(key, &target, "title", &body("synthetic body"))
            .await
            .unwrap();
    }
    container
        .resolve("run-3:0", &target, &filed(&["run-1:0"]), "the check is ok")
        .await
        .unwrap();
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let closed = task_with(&tasks, "run-1:0").unwrap();
    assert_eq!(closed.get("status"), Some("completed"));
    assert!(closed.get("end").is_some());
    assert!(
        annotations(closed).contains(&"almanac resolved run-3:0\nthe check is ok"),
        "{:?}",
        annotations(closed)
    );
    let open = task_with(&tasks, "run-2:0").unwrap();
    assert_eq!(open.get("status"), Some("pending"));
}

#[tokio::test]
async fn a_repeated_resolve_changes_nothing() {
    let dir = TempDir::new().unwrap();
    let container = open(dir.path(), "container");
    let target = target();
    container
        .file("run-1:0", &target, "title", &body("synthetic body"))
        .await
        .unwrap();
    for _ in 0..2 {
        container
            .resolve("run-3:0", &target, &filed(&["run-1:0"]), "ok")
            .await
            .unwrap();
    }
    let tasks = container.tasks().await.unwrap();
    assert_eq!(annotations(task_with(&tasks, "run-1:0").unwrap()).len(), 2);
}

#[tokio::test]
async fn a_task_named_by_uuid_is_completed() {
    let dir = TempDir::new().unwrap();
    let container = open(dir.path(), "container");
    let target = target();
    container
        .file("run-1:0", &target, "title", &body("synthetic body"))
        .await
        .unwrap();
    let tasks = container.tasks().await.unwrap();
    let uuid = task_with(&tasks, "run-1:0").unwrap().get_uuid().to_string();
    let named = Resolving::Named(uuid.parse().unwrap());
    container
        .resolve("run-3:0", &target, &named, "merged in abc")
        .await
        .unwrap();
    let tasks = container.tasks().await.unwrap();
    assert_eq!(
        task_with(&tasks, "run-1:0").unwrap().get("status"),
        Some("completed")
    );
}

#[tokio::test]
async fn a_name_that_is_not_a_uuid_is_refused_before_anything_is_written() {
    let dir = TempDir::new().unwrap();
    let container = open(dir.path(), "container");
    let named = Resolving::Named("53db253c".parse().unwrap());
    let error = container
        .resolve("run-3:0", &target(), &named, "ok")
        .await
        .unwrap_err();
    assert!(error.0.contains("not a task UUID"), "{}", error.0);
    assert!(!dir.path().join("server").exists());
}
