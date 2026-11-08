# Taskwarrior Tracker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A workflow can file a finding to taskwarrior by naming a `task:<project>` target, and the running almanac delivers it as a task on Josh's list through a replica it syncs with picklelab's taskchampion server.

**Architecture:** A `Project` newtype makes `task:<project>` slugs well-formed by construction. `Router<I, T>` implements almanac's `Tracker` trait and dispatches on the target's prefix. `Taskwarrior` implements `Tracker` over an in-process taskchampion `Replica`, syncing before it checks for the key and after it writes. `providers::all` wires the three with the same fallbacks the GitHub side has today.

**Tech Stack:** Rust 2024, the `almanac` crate at ivy/almanac b8bdefa, `taskchampion` 3.1.0 (sqlite storage, sync and local servers), `tokio::sync::Mutex`, Docker Compose, 1Password `op run`.

**Spec:** `docs/superpowers/specs/2026-11-08-taskwarrior-tracker-design.md`

## Global Constraints

- Project names: segments of `[a-z0-9_-]+` joined by `.`, whole slug at most 200 characters (almanac's `Slug` limit).
- Target slugs: `task:<project>` for taskwarrior; anything else is a GitHub `owner/repo` and is passed through untouched.
- `task:almanac` is the project for findings about almanac itself and cross-repository findings. No bare `task:`.
- Attributes on a filed task: `almanac_key`, `almanac_target`, and `almanac_pull` (only on `ask` with a pull number). Tag `almanac` on every task; `question` added on `ask`.
- Annotation text: first line `almanac <key>`, body from the second line, verbatim.
- Task UUIDs are random v4, never derived from the key.
- Environment: `TASKCHAMPION_SYNC_URL`, `TASKCHAMPION_CLIENT_ID`, `TASKCHAMPION_ENCRYPTION_SECRET`, `TASKCHAMPION_REPLICA_DIR` (default `/var/lib/brined-almanac/taskchampion`). URL set with either secret missing is a startup error. URL unset means `LogTracker` on the tasks side.
- The encryption secret's bytes are the string's bytes as stored, no base64 decoding (taskwarrior 3.4.2 does `as_bytes()`).
- `taskchampion` is pulled with `default-features = false, features = ["storage-sqlite", "server-sync", "server-local", "bundled", "tls-webpki-roots"]`.
- No source file may contain the literal tainted-text JSON key (see `.forbidden-patterns`); read an `Untrusted<String>` by position the way `LogTracker` does.
- No error message carries body text or the encryption secret.
- Commits are signed as configured; do not pass signing overrides.

## Review Focus

1. A `task:` slug with an invalid or empty project reaching the router (hand-built, or from a future workflow that skips `Project`): expect an error naming the target, not a GitHub call. Test in Task 2.
2. The sync server unreachable before anything is written: expect the effect to fail, the replica unchanged, and the error to name the server, not the secret. Test in Task 4.
3. A body with backticks, blank lines and a few thousand characters: expect the annotation to carry it unchanged and the description to be the title alone. Test in Task 4.
4. A failure message after a filing attempt: expect it never to contain the body text. Test in Task 4.
5. `TASKCHAMPION_SYNC_URL` set with `TASKCHAMPION_CLIENT_ID` missing or not a UUID: expect construction to fail naming that variable. Test in Task 6.

---

### Task 1: `Project` and the `task:` target scheme

**Files:**
- Create: `src/routing.rs`
- Modify: `src/lib.rs` (add `pub mod routing;`)

**Interfaces:**
- Produces: `pub struct Project(String)` with `impl FromStr for Project` (error type `String`), `Project::target(&self) -> Slug`, `Project::from_target(slug: &Slug) -> Option<Result<Project, String>>` (`None` when the slug has no `task:` prefix, `Some(Err)` when it has the prefix and an invalid project), `Project::as_str(&self) -> &str`, and `pub const TASK_PREFIX: &str = "task:"`.

- [ ] **Step 1: Write the failing tests** in a `#[cfg(test)] mod tests` at the bottom of `src/routing.rs`:

```rust
#[test]
fn real_project_names_round_trip() {
    for name in ["picklehome", "homelab.backup", "pickled-claude-plugins.hk", "almanac"] {
        let project: Project = name.parse().unwrap();
        assert_eq!(project.target().to_string(), format!("task:{name}"));
        assert_eq!(Project::from_target(&project.target()).unwrap().unwrap().as_str(), name);
    }
}

#[test]
fn rejects_names_taskwarrior_would_take_but_filters_would_not() {
    for bad in ["Bad Name", "has/slash", "has:colon", "UPPER", "trail.", ".lead", "a..b", ""] {
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cargo test --lib routing`
Expected: compile error, `Project` not found.

- [ ] **Step 3: Implement `Project` in `src/routing.rs`**

`FromStr` checks: non-empty, at most `200 - TASK_PREFIX.len()` bytes, split on `.` gives segments that are each non-empty and all `[a-z0-9_-]`. `target()` builds `format!("task:{}", self.0).parse()` and `expect`s, which the parse rules guarantee. `from_target` is `slug.to_string().strip_prefix(TASK_PREFIX).map(str::parse)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cargo test --lib routing`
Expected: 5 passed.

- [ ] **Step 5: Commit**

```bash
git add src/routing.rs src/lib.rs
git commit -m "Add Project, the task:<project> target scheme"
```

### Task 2: `Router` over two trackers

**Files:**
- Modify: `src/routing.rs`
- Create: `tests/router.rs`

**Interfaces:**
- Consumes: `Project::from_target` from Task 1; `almanac::testing::FakeTracker` (has `filed: Arc<Mutex<Vec<(String, String, String)>>>` of key, target, title).
- Produces: `pub struct Router<I, T> { issues: I, tasks: T }` with `Router::new(issues: I, tasks: T) -> Self` and `impl<I: Tracker, T: Tracker> Tracker for Router<I, T>`. The tasks side receives the target slug unchanged (it parses the project itself).

- [ ] **Step 1: Write the failing tests** in `tests/router.rs`:

```rust
#[tokio::test]
async fn a_task_target_goes_to_the_tasks_side_with_everything_intact() {
    let (issues, tasks) = (FakeTracker::default(), FakeTracker::default());
    let router = Router::new(issues.clone(), tasks.clone());
    let target: Slug = "task:picklehome".parse().unwrap();
    router.file("run-1:0", &target, "title", &body()).await.unwrap();
    assert_eq!(tasks.filed.lock().unwrap()[0], ("run-1:0".into(), "task:picklehome".into(), "title".into()));
    assert!(issues.filed.lock().unwrap().is_empty());
}

#[tokio::test]
async fn a_repository_target_goes_to_the_issues_side() { /* mirror of the above with technicalpickles/brineworks */ }

#[tokio::test]
async fn ask_routes_the_same_way_and_keeps_the_pull() { /* ask with pull Some(7) on a task: target reaches tasks.calls once; issues untouched */ }

#[tokio::test]
async fn a_malformed_task_target_is_refused_before_either_side() {
    let (issues, tasks) = (FakeTracker::default(), FakeTracker::default());
    let router = Router::new(issues.clone(), tasks.clone());
    let target: Slug = "task:Bad".parse().unwrap();
    let err = router.file("k", &target, "title", &body()).await.unwrap_err();
    assert!(err.0.contains("task:Bad"), "{}", err.0);
    assert!(!err.0.contains("synthetic body"));
    assert!(issues.filed.lock().unwrap().is_empty() && tasks.filed.lock().unwrap().is_empty());
}
```

`body()` returns `Untrusted::new("synthetic body".to_string())`, as `tests/private_only.rs` does.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cargo test --test router`
Expected: compile error, `Router` not found.

- [ ] **Step 3: Implement `Router` in `src/routing.rs`**

Both methods call `Project::from_target(target)`: `None` forwards to `issues`, `Some(Ok(_))` forwards to `tasks`, `Some(Err(why))` returns `StepError(format!("{target} is not a task target: {why}"))`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cargo test --test router`
Expected: 4 passed.

- [ ] **Step 5: Commit**

```bash
git add src/routing.rs tests/router.rs
git commit -m "Route File and Ask by target: task:<project> or owner/repo"
```

### Task 3: Shared body reader and the taskchampion dependency

**Files:**
- Modify: `Cargo.toml`, `Cargo.lock`
- Modify: `src/providers.rs` (`LogTracker` becomes `pub struct LogTracker;`, its by-position body read moves to a shared function)

**Interfaces:**
- Produces: `pub fn body_text(body: &Untrusted<String>) -> Result<String, StepError>` in `src/providers.rs`, returning the tainted text by position (first value of the serialized object), empty string when absent. `pub struct LogTracker` usable from other modules and tests.

- [ ] **Step 1: Add the dependencies**

In `[dependencies]`: `taskchampion = { version = "3.1.0", default-features = false, features = ["storage-sqlite", "server-sync", "server-local", "bundled", "tls-webpki-roots"] }` and `tokio = { version = "1", features = ["sync"] }`. Keep the existing `tokio` dev-dependency.

Run: `cargo build --locked 2>&1 | tail -3 || cargo build`
Expected: the first build fails on the lockfile; the plain build updates `Cargo.lock` and succeeds. Confirm `grep -c '^name = "taskchampion"' Cargo.lock` prints 1.

- [ ] **Step 2: Write the failing test** at the bottom of `src/providers.rs` in a `#[cfg(test)] mod tests`:

```rust
#[test]
fn body_text_reads_the_tainted_value_without_naming_its_key() {
    let body = Untrusted::new("a\n`b`\nc".to_string());
    assert_eq!(body_text(&body).unwrap(), "a\n`b`\nc");
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cargo test --lib providers`
Expected: compile error, `body_text` not found.

- [ ] **Step 4: Extract `body_text` from `LogTracker::file` and make `LogTracker` public**

`LogTracker::file` calls `body_text` and prints as before. The comment about reading by position moves with the code.

- [ ] **Step 5: Run the tests and the forbidden check**

Run: `cargo test && scripts/check-forbidden src/providers.rs`
Expected: all tests pass; the check prints nothing and exits 0.

- [ ] **Step 6: Commit**

```bash
git add Cargo.toml Cargo.lock src/providers.rs
git commit -m "Add taskchampion and share the by-position body reader"
```

### Task 4: The `Taskwarrior` tracker files a task

**Files:**
- Create: `src/taskwarrior.rs`
- Modify: `src/lib.rs` (add `pub mod taskwarrior;`)
- Create: `tests/taskwarrior.rs`

**Interfaces:**
- Consumes: `Project::from_target` (Task 1), `body_text` (Task 3).
- Produces in `src/taskwarrior.rs`:
  - `pub struct TaskwarriorConfig { pub replica_dir: PathBuf, pub server: taskchampion::ServerConfig }`
  - `pub struct Taskwarrior` with `pub fn new(config: TaskwarriorConfig) -> Taskwarrior` (sync, stores the config) and `impl Tracker for Taskwarrior`. The replica and server open lazily on the first call, through a `tokio::sync::OnceCell<Opened>` where `Opened { replica: Mutex<Replica<SqliteStorage>>, server: Mutex<Box<dyn Server>> }`: create `replica_dir`, `SqliteStorage::new(replica_dir.join("taskchampion.sqlite3"), AccessMode::ReadWrite, true)`, `config.server.into_server().await`. `providers::all` is called outside the tokio runtime (`almanac::app::main` calls `setup(&config)` before any `block_on`), so nothing async may run at construction.
  - `pub const KEY_ATTR: &str = "almanac_key"`, `pub const TARGET_ATTR: &str = "almanac_target"`, `pub const PULL_ATTR: &str = "almanac_pull"`, `pub const TAG: &str = "almanac"`, `pub const QUESTION_TAG: &str = "question"`.
  - Test-facing: `pub async fn sync(&self) -> Result<(), StepError>` and `pub async fn tasks(&self) -> Result<Vec<taskchampion::TaskData>, StepError>` (all tasks, so a test can read what landed).

- [ ] **Step 1: Write the test harness and the first failing tests** in `tests/taskwarrior.rs`

Harness: `fn local(dir: &Path) -> ServerConfig { ServerConfig::Local { server_dir: dir.join("server") } }` and `fn open(dir: &Path, name: &str) -> Taskwarrior` as `Taskwarrior::new(TaskwarriorConfig { replica_dir: dir.join(name), server: local(dir) })`. Two replicas, `container` and `mac`, share one `server` directory under a `tempfile::TempDir`. `fn task_with(tasks: &[TaskData], key: &str) -> Option<&TaskData>` finds by `KEY_ATTR`.

```rust
#[tokio::test]
async fn a_filing_reaches_the_other_replica_with_its_shape() {
    let dir = TempDir::new().unwrap();
    let (container, mac) = (open(dir.path(), "container"), open(dir.path(), "mac"));
    let target: Slug = "task:picklehome".parse().unwrap();
    container.file("run-1:0", &target, "Alerts open on picklehome", &body("line one\n\n```\nfenced\n```")).await.unwrap();
    mac.sync().await.unwrap();
    let tasks = mac.tasks().await.unwrap();
    let task = task_with(&tasks, "run-1:0").expect("the task synced");
    assert_eq!(task.get("description"), Some("Alerts open on picklehome"));
    assert_eq!(task.get("project"), Some("picklehome"));
    assert_eq!(task.get("status"), Some("pending"));
    assert!(task.has("tag_almanac"));
    assert_eq!(task.get(TARGET_ATTR), Some("task:picklehome"));
    let annotation = task.iter().find(|(k, _)| k.starts_with("annotation_")).map(|(_, v)| v.as_str()).unwrap();
    assert_eq!(annotation, "almanac run-1:0\nline one\n\n```\nfenced\n```");
    assert!(task.get("entry").is_some() && task.get("modified").is_some());
}

#[tokio::test]
async fn the_same_key_twice_files_once() { /* file run-1:0 twice through container; mac.sync(); exactly one task with that key and one task in all */ }

#[tokio::test]
async fn a_different_key_adds_exactly_one() { /* run-1:0 then run-1:1; mac sees two */ }

#[tokio::test]
async fn a_task_the_mac_already_has_is_not_filed_again() {
    // mac.file("run-9:0", ..) lands it on the server; a fresh container files "run-9:0";
    // mac.sync(); mac.tasks() holds exactly one task with that key. This is the lost-volume
    // case: the container's step 1 learns of the task before step 2 looks for it.
}

#[tokio::test]
async fn a_long_body_lands_verbatim() { /* 4000-character body with backticks; annotation equals "almanac <key>\n" + body; description is the title only */ }

#[tokio::test]
async fn an_unreachable_server_writes_nothing_and_names_no_secret() {
    // server_dir is a regular file, so the local server fails to open or sync.
    let err = container.file("run-1:0", &target, "title", &body("synthetic body")).await.unwrap_err();
    assert!(!err.0.contains("synthetic body"));
    assert!(container.tasks().await.unwrap().is_empty());
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cargo test --test taskwarrior`
Expected: compile error, `taskwarrior` module not found.

- [ ] **Step 3: Implement `Taskwarrior::new`, the lazy open, and `Tracker::file` in `src/taskwarrior.rs`**

`file` gets or initialises the `OnceCell` (an open failure is a `StepError` naming the replica directory, and the cell stays empty so the retry tries again), holds both locks for the whole call, then:

1. `replica.sync(&mut server, true)`; any error becomes `StepError(format!("taskwarrior sync failed: {e}"))`.
2. `replica.all_task_data()`; if any `TaskData::get(KEY_ATTR) == Some(key)`, return `Ok(())`.
3. `let mut ops = Operations::new(); let mut task = replica.create_task(Uuid::new_v4(), &mut ops)`; set description (title), status `Status::Pending`, entry and modified to `Utc::now()`, `set_value("project", Some(project))`, `add_tag(&TAG.parse()?)`, `add_annotation(Annotation { entry: now, description: format!("almanac {key}\n{}", body_text(body)?) })`, `set_user_defined_attribute(KEY_ATTR, key)`, `set_user_defined_attribute(TARGET_ATTR, target.to_string())`; `replica.commit_operations(ops)`.
4. `replica.sync(&mut server, true)` again, same error mapping.

`ask` is a stub returning the same as `file` for now; Task 5 finishes it. Map every `taskchampion::Error` and `anyhow::Error` through one `fn step(e: impl Display, what: &str) -> StepError` so no message interpolates a body or a config value.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cargo test --test taskwarrior`
Expected: 6 passed.

- [ ] **Step 5: Run the whole suite and the forbidden check**

Run: `cargo test && scripts/check-forbidden src/taskwarrior.rs tests/taskwarrior.rs`
Expected: all pass; check exits 0.

- [ ] **Step 6: Commit**

```bash
git add src/taskwarrior.rs src/lib.rs tests/taskwarrior.rs
git commit -m "File a finding as a taskwarrior task through a synced replica"
```

### Task 5: `ask` becomes a question task

**Files:**
- Modify: `src/taskwarrior.rs`
- Modify: `tests/taskwarrior.rs`

**Interfaces:**
- Consumes: Task 4's `Taskwarrior`.
- Produces: `Tracker::ask` on `Taskwarrior` behaving as `file` plus the `question` tag and `PULL_ATTR` when `pull` is `Some`.

- [ ] **Step 1: Write the failing tests**

```rust
#[tokio::test]
async fn an_ask_is_a_question_task_with_its_pull() {
    container.ask("run-2:0", &target, Some(42), "Merge this?", &body("why")).await.unwrap();
    mac.sync().await.unwrap();
    let task = task_with(&mac.tasks().await.unwrap(), "run-2:0").unwrap();
    assert_eq!(task.get("description"), Some("Merge this?"));
    assert!(task.has("tag_almanac") && task.has("tag_question"));
    assert_eq!(task.get(PULL_ATTR), Some("42"));
}

#[tokio::test]
async fn an_ask_without_a_pull_has_no_pull_attribute() { /* pull None; PULL_ATTR absent; question tag present */ }
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cargo test --test taskwarrior ask`
Expected: 2 failed (no `question` tag).

- [ ] **Step 3: Implement**

Refactor `file`'s steps 1 to 4 into `async fn deliver(&self, key, target, description, body, question: bool, pull: Option<u64>)`; `file` calls it with `(false, None)`, `ask` with `(true, pull)`. `question` adds `QUESTION_TAG`; `pull` sets `PULL_ATTR` to `pull.to_string()`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cargo test --test taskwarrior`
Expected: 8 passed.

- [ ] **Step 5: Commit**

```bash
git add src/taskwarrior.rs tests/taskwarrior.rs
git commit -m "Ask through taskwarrior as a question task"
```

### Task 6: Wiring in `providers::all`

**Files:**
- Modify: `src/providers.rs`
- Create: `tests/providers.rs`

**Interfaces:**
- Consumes: `Router::new` (Task 2), `LogTracker`, `body_text` (Task 3), `TaskwarriorConfig`, `Taskwarrior::open` (Task 4).
- Produces in `src/providers.rs`:
  - `pub enum IssuesSide { Github { token: String }, Log }`, `pub enum TasksSide { Taskwarrior(TaskwarriorConfig), Log }`, `pub enum Choice { StoreRequired, Route { issues: IssuesSide, tasks: TasksSide } }`.
  - `pub fn choose(lookup: impl Fn(&str) -> Option<String>) -> Result<Choice, String>`: pure, reads `DATABASE_URL`, `GITHUB_TOKEN`, `TASKCHAMPION_SYNC_URL`, `TASKCHAMPION_CLIENT_ID`, `TASKCHAMPION_ENCRYPTION_SECRET`, `TASKCHAMPION_REPLICA_DIR`. `Err` names the missing or malformed variable and nothing else.
  - `pub fn tracker_from(choice: Choice) -> Arc<dyn Tracker>` builds the layered tracker; it is sync because `Taskwarrior::new` is.
  - `providers::all` becomes `choose(|k| env::var(k).ok())`, then `tracker_from`, panicking with the message on `Err` (a startup error, as the spec requires; `all` runs before the daemon starts).

- [ ] **Step 1: Write the failing tests** in `tests/providers.rs`, each building a `HashMap<&str, &str>` and passing `|k| map.get(k).map(|v| v.to_string())`:

```rust
#[test]
fn no_database_means_store_required_for_both_sides() { /* only GITHUB_TOKEN and TASKCHAMPION_* set → Choice::StoreRequired */ }

#[test]
fn no_sync_url_means_log_on_the_tasks_side() { /* DATABASE_URL, GITHUB_TOKEN → Route { issues: Github, tasks: Log } */ }

#[test]
fn no_token_means_log_on_the_issues_side() { /* DATABASE_URL, TASKCHAMPION_* → Route { issues: Log, tasks: Taskwarrior(..) } */ }

#[test]
fn a_full_configuration_builds_both() {
    // all six set; tasks is Taskwarrior(config) with replica_dir == "/some/dir" and
    // server matching ServerConfig::Remote { url, client_id, encryption_secret } where
    // encryption_secret == b"s3cret-as-stored".to_vec()
}

#[test]
fn a_url_without_a_client_id_is_an_error_naming_it() {
    let err = choose(..).unwrap_err();
    assert!(err.contains("TASKCHAMPION_CLIENT_ID"));
    assert!(!err.contains("s3cret"));
}

#[test]
fn a_url_without_a_secret_is_an_error_naming_it() { /* contains "TASKCHAMPION_ENCRYPTION_SECRET" */ }

#[test]
fn a_client_id_that_is_not_a_uuid_is_an_error_naming_it() { /* "not-a-uuid" → contains "TASKCHAMPION_CLIENT_ID" */ }

#[test]
fn the_replica_dir_defaults() { /* TASKCHAMPION_REPLICA_DIR unset → "/var/lib/brined-almanac/taskchampion" */ }
```

`ServerConfig` is `#[non_exhaustive]` and not `PartialEq`; assert with `matches!(server, ServerConfig::Remote { url, client_id, encryption_secret } if ...)`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cargo test --test providers`
Expected: compile error, `choose` not found.

- [ ] **Step 3: Implement `choose`, `tracker_from`, and rewire `all`**

`tracker_from`: `Choice::StoreRequired` → `Arc::new(StoreRequired)`. `Route`: issues side is `PrivateOnly::new(GithubIssues::new(API, token.clone()), GithubClient::new(API, Auth::Token(token)))` or `LogTracker`; tasks side is `Taskwarrior::new(config)` or `LogTracker`; wrap in `Router::new`. Since `I` and `T` differ per branch, each side is an `Arc<dyn Tracker>` and the router is `Router<Arc<dyn Tracker>, Arc<dyn Tracker>>`. Upstream has no `Tracker` impl for `Arc`, so add `impl<T: Tracker + ?Sized> Tracker for Arc<T>` forwarding both methods in `src/routing.rs`, next to the router (the same shape `Visibility for Arc<V>` has in `src/providers.rs`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cargo test`
Expected: all pass, including the earlier suites.

- [ ] **Step 5: Dry run end to end**

Run: `env -u TASKCHAMPION_SYNC_URL cargo run -- check`
Expected: exits 0 and reports a tracker provided (the tasks side is `LogTracker`), with or without this checkout's `DATABASE_URL`.

- [ ] **Step 6: Commit**

```bash
git add src/providers.rs src/routing.rs tests/providers.rs
git commit -m "Wire the router: GitHub issues or taskwarrior by target, with fallbacks"
```

### Task 7: Compose, image, secrets, deploy, runbook

**Files:**
- Modify: `Containerfile` (create `/var/lib/brined-almanac/taskchampion` owned by `almanac`)
- Modify: `compose.prod.yaml` (named volume `taskchampion`; `TASKCHAMPION_CLIENT_ID` and `TASKCHAMPION_ENCRYPTION_SECRET` from `BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID` and `BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET` with the same `:?run \`mise run deploy\`...` guard the token uses)
- Modify: `prod.secrets.env` (two `op://brined-almanac/taskchampion/<field>` references)
- Modify: `scripts/deploy`, `scripts/compose-config-check`, `mise.toml` `prod` task (add the two new names to every `unused` stand-in list)
- Modify: `docs/runbooks/prod-on-the-mac.md`

**Interfaces:**
- Consumes: the variable names from Global Constraints.
- Produces: a stack that passes `TASKCHAMPION_*` to the almanac service and refuses to `up` without the secrets.

- [ ] **Step 1: Extend `scripts/compose-config-check` first** so it fails: assert `.services.almanac.environment.TASKCHAMPION_CLIENT_ID == "c"` and `.TASKCHAMPION_ENCRYPTION_SECRET == "s"` when rendered with `BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=c BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=s`; assert one almanac volume with `.target == "/var/lib/brined-almanac/taskchampion"`; add `expect_guard_failure` cases for each new name; add `TASKCHAMPION_SYNC_URL=https://example.invalid` to the stub `prod.env`. Every `render` that passes stub secrets passes all four.

Run: `scripts/compose-config-check`
Expected: fails on the first new assertion.

- [ ] **Step 2: Edit `compose.prod.yaml`, `Containerfile`, `prod.secrets.env`, `scripts/deploy`, `mise.toml`**

In the Containerfile's runtime stage: `mkdir -p /var/lib/brined-almanac/taskchampion && chown almanac /var/lib/brined-almanac/taskchampion` after `useradd`. In compose: `volumes: - taskchampion:/var/lib/brined-almanac/taskchampion` on the almanac service and `taskchampion:` under top-level `volumes`. `TASKCHAMPION_SYNC_URL` is not in compose; it comes from `prod.env` through `env_file`.

Run: `scripts/compose-config-check && mise run check`
Expected: `compose-config-check: ok`; hooks pass (shellcheck covers the edited scripts).

- [ ] **Step 3: Build the image and confirm the directory**

Run: `docker build -f Containerfile -t brined-almanac:plan-check . && docker run --rm --entrypoint sh brined-almanac:plan-check -c 'stat -c %U /var/lib/brined-almanac/taskchampion'`
Expected: `almanac`.

- [ ] **Step 4: Update the runbook**

Sections to touch, in the runbook's existing voice:
- "1Password": item 5, `taskchampion`, fields `client_id` and `encryption_secret`, copied from `op://picklehome/TaskChampion Sync`; one sentence that the two items hold the same values and a rotation touches both and re-initialises every replica.
- "State directory": `prod.env` gains `TASKCHAMPION_SYNC_URL=https://taskchampion.<tailnet>.ts.net` with the real host.
- "Rollback" or a new "Restore" note near the existing restore steps: never start the daemon on an empty store while tasks or issues filed by the old one exist; restore the dump first, because run ids restart and a tracker's retry check could skip a new finding.
- "When it breaks": "the almanac is unhealthy or files nothing" gains: if `check` reports a missing `TASKCHAMPION_*` variable, `prod.env` or the vault item is incomplete; the tasks side never falls back to printing when the URL is set.
- "Gotchas": the `unused` prefix now lists four names; update the example.

Run: `scripts/check-forbidden docs/runbooks/prod-on-the-mac.md`
Expected: exits 0.

- [ ] **Step 5: Commit**

```bash
git add Containerfile compose.prod.yaml prod.secrets.env scripts/deploy scripts/compose-config-check mise.toml docs/runbooks/prod-on-the-mac.md
git commit -m "Carry the taskchampion replica and its sync secrets in the prod stack"
```

### Task 8: Prove it in prod

These steps need Josh: the deploy runs from his shell, and the 1Password item is his.

**Files:** none; verification only, recorded in the runbook's deploy log section if one exists.

- [ ] **Step 1: Josh creates the `taskchampion` item** in the `brined-almanac` vault with the two fields copied from picklehome's item, and adds `TASKCHAMPION_SYNC_URL` to `$STATE/prod.env`.

- [ ] **Step 2: Deploy**

Run (Josh): `mise run deploy`
Expected: `deploy: stack is up`, four healthy containers, `/healthz` answers.

- [ ] **Step 3: Check**

Run: `mise run prod -- check`
Expected: exits 0; no message about a missing `TASKCHAMPION_*` variable.

- [ ] **Step 4: First sync against the real server**

Run: `mise run prod -- check` already constructed the tracker, but the first sync happens on the first filing. Until an M1 workflow files, prove the sync by hand: `env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused docker compose -f compose.prod.yaml exec almanac ls -la /var/lib/brined-almanac/taskchampion`
Expected: the directory exists and is owned by `almanac`; it is empty until the first filing. The sync itself, and the encryption-secret-bytes question, are proven by the first M1 workflow's first task appearing on the Mac after `task sync`, with `almanac_key` visible in `task info` (as an orphan until dotfiles ships the UDA lines, task `f95397dc-603b-4722-9667-574635a32d8a`).

- [ ] **Step 5: Record**

Append the date and outcome to the runbook's deploy history, and annotate taskwarrior task `4cc5a639-e11a-4cf0-98ad-71a85cfcfa6e` with the commit range and what was proven.
