# Tool error scan implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Rust almanac that reads Brent's OpenClaw transcripts, clusters failed tool calls by service, tool, error class, and day, and files one GitHub issue per cluster on the private repository that owns the tool.

**Architecture:** One crate on ivy/almanac by git dependency. `transcript.rs` is the only code that knows the JSONL shape and returns validated types. `workflows/tool_failures.rs` holds the class and ownership tables, clusters, emits one `ToolFailed` event per cluster with a stable delivery id, and decides with a pure gather and a pure policy. `providers.rs` owns the environment and wraps the GitHub tracker in a privacy gate. Tests build synthetic transcripts at run time and never commit one.

**Tech Stack:** Rust 1.99.0, almanac and github crates at `32c2866`, regex, serde, serde_json. Dev: tokio, tempfile.

**Spec:** `docs/superpowers/specs/2026-11-06-tool-error-scan-design.md`. The type sketch came from an arena of four candidate designs on 2026-11-06; the decisions it changed are recorded in the spec's Shapes, Data flow, and Untrusted text sections.

## Global constraints

- Toolchain: `rust = "1.99.0"` in `mise.toml`, matching ivy's `rust-toolchain.toml`. Run cargo as `mise exec -- cargo ...` from the repo root.
- Dependencies: `almanac = { git = "https://github.com/ivy/almanac", rev = "32c2866" }`, same for `github`, plus `regex = "1"`, `serde = { version = "1", features = ["derive"] }`, `serde_json = "1"`. Dev: `tokio = { version = "1", features = ["macros", "rt-multi-thread"] }`, `tempfile = "3"`. Nothing else.
- Public repo. No transcript text, no secrets, no MCP config, no Telegram ids or bot handles, in source, fixtures, tests, or commit messages. `scripts/check-forbidden` runs on every commit and push.
- `.forbidden-patterns` matches the JSON spelling of the result role key and value, and the quoted metadata key. Source that must write them spells them through constants or `concat!`, never as one literal pair.
- Errors name a path or a repository. Never transcript text.
- The delivery id is `scan:brent/{service}/{tool}/{class}/{day}` and the workflow key is `{service}:{tool}:{class}:{day}`. Neither carries a count or a time.
- The issue title is the static string `Brent: failed tool calls`.
- Thresholds are compared in gather, not in scan. Every cluster becomes an event.
- Commit after each task with a plain `git commit` (signing is set up, never bypass it). Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.

## Review focus

Inputs the spec implies but no spec test exercises. Each has its test added to the owning task below.

1. `details` arrives as a JSON object, not a Python-repr string. The regexes want single quotes, so identity must fall back to the call id, not crash or mis-route. Task 3.
2. An error result whose `toolCallId` names a call block the reader never saw. The spike produced a tool named `?`. The reader must yield `openclaw` and `unknown`. Task 3.
3. A tool name with characters outside the Slug alphabet (a space, a slash is fine, a unicode letter). The payload must still parse as `ToolFailed`. Task 4.
4. A directory with no `*.jsonl` files, or with files of other extensions. The scan must return no events and no error. Task 3.
5. A `.jsonl` entry that is a directory. `read` must fail with `ReadError::File` naming it, and the scan must file nothing. Task 3.

---

### Task 1: crate-scaffold

**Files:**
- Create: `Cargo.toml`, `almanac.toml`, `src/main.rs`, `src/lib.rs`, `src/transcript.rs` (empty module), `src/workflows/mod.rs`, `src/workflows/tool_failures.rs` (empty module), `src/providers.rs`
- Modify: `mise.toml` (add `rust = "1.99.0"` under `[tools]` and a `test` task running `cargo test`), `hk.pkl` (add a `fmt` step running `cargo fmt --check` on pre-commit, and a `clippy` step running `cargo clippy --all-targets -- -D warnings` on check and pre-push)

**Interfaces:**
- Produces: `brined_almanac::workflows::all() -> Vec<Box<dyn Registered>>` (empty for now), `brined_almanac::providers::all(&Config) -> Providers` (empty for now), binary `brined-almanac` whose `main` calls `almanac::main("brined-almanac", workflows::all, providers::all)`.

- [ ] **Step 1: Write `Cargo.toml` from `local/spike/Cargo.gitdep.toml`** with `name = "brined-almanac"`, `publish = false`, `edition = "2024"`, an empty `[workspace]` table, the dependencies in Global constraints, and `[[bin]] name = "brined-almanac", path = "src/main.rs"`.

- [ ] **Step 2: Write the module files.** `main.rs` is one function. `lib.rs` declares `pub mod providers; pub mod transcript; pub mod workflows;`. `workflows/mod.rs` declares `pub mod tool_failures;` and `all()` returning `vec![]`. `providers.rs` has `all` returning `Providers::new()`. `almanac.toml` is the spike's.

- [ ] **Step 3: Pin the toolchain and hooks.** Edit `mise.toml` and `hk.pkl` as listed. Run `mise install`.

- [ ] **Step 4: Build and smoke the binary.**

Run: `mise exec -- cargo build && mise exec -- cargo run -- synth`
Expected: a build with no warnings, and `synth` prints a JSON manifest with an empty workflow list.

- [ ] **Step 5: Run the guardrails.**

Run: `mise run check`
Expected: fmt, clippy, gitleaks, and forbidden all pass.

- [ ] **Step 6: Commit** `Cargo.toml Cargo.lock almanac.toml mise.toml hk.pkl src/` as `Scaffold the crate on ivy/almanac 32c2866`.

---

### Task 2: fixture-builder

**Files:**
- Create: `tests/support/mod.rs`

**Interfaces:**
- Consumes: nothing from the crate.
- Produces, for every later test:

```rust
pub struct Fixture;                      // owns a tempfile::TempDir and an event counter
impl Fixture {
    pub fn new() -> Self;
    pub fn path(&self) -> &Path;
    pub fn file(&self, name: &str) -> FileBuilder<'_>;
}
pub struct FileBuilder<'a>;
impl FileBuilder<'_> {
    pub fn with(self, failure: Failure) -> Self;   // an assistant line with the toolCall block, then the result line(s)
    pub fn garbage(self, n: usize) -> Self;         // n lines that are not JSON
    pub fn write(self);
}
pub enum Spelling { Prefixed, Namespaced, Bare }   // svc__tool, mcp:svc:svc__tool, tool
pub enum Details { Mcp, NamedOnly, Empty, Object } // tool.mcp keys; status/tool/error only; ""; a JSON object instead of a repr
pub struct Failure;
impl Failure {
    pub fn mcp(service: &str, tool: &str) -> Self; // toolName tool_call, Prefixed, Details::Mcp, at 2026-11-04T10:00:00Z, error "synthetic failure"
    pub fn builtin(tool: &str) -> Self;            // toolName is the tool; no call block
    pub fn spelled(self, s: Spelling) -> Self;
    pub fn details(self, d: Details) -> Self;
    pub fn at(self, iso: &str) -> Self;
    pub fn error(self, text: &str) -> Self;        // goes in details as a Python str repr and in content as a text block
    pub fn times(self, n: u64) -> Self;            // n results one second apart, fresh call and event ids
    pub fn duplicated(self) -> Self;               // the result line written twice with one event id
    pub fn succeeded(self) -> Self;                // isError false; the reader must ignore it
    pub fn orphaned(self) -> Self;                 // the result line only, its toolCallId naming no call block
    pub fn event_id(self, id: &str) -> Self;       // a fixed event id, so two files can carry the same event
}
```

The role key, the result-role value, and the metadata key are each a `const` spelled so no source line carries the pair `.forbidden-patterns` matches. Lines are built with `serde_json::Map`. `py_str` renders a Python repr with single quotes, switching to double quotes when the text has an apostrophe, as Python does. The Mcp details repr nests `'tool': {'mcp': {'serverName': '..', 'toolName': '..'}}` and `'error': '..'`.

- [ ] **Step 1: Write the builder** with every method above. Bodies are plain string and map building.

- [ ] **Step 2: Write one builder test** in `tests/transcript.rs` (a support module cannot hold its own tests): `the_builder_writes_one_call_and_one_result_per_failure` reads the written file back as lines, asserts two JSON lines for `Failure::mcp("brineworks", "triage_apply")`, and asserts the second line's `isError` is `true`.

- [ ] **Step 3: Run it.**

Run: `mise exec -- cargo test --test transcript`
Expected: PASS.

- [ ] **Step 4: Run `mise run check`.** Expected: the forbidden step passes over `tests/support/mod.rs`.

- [ ] **Step 5: Commit** `tests/` as `Add the synthetic transcript builder`.

---

### Task 3: transcript-reader

**Files:**
- Modify: `src/transcript.rs`
- Test: `tests/transcript.rs`

**Interfaces:**
- Consumes: the builder from Task 2.
- Produces:

```rust
pub struct Transcripts(pub PathBuf);                       // the provider the scan asks for
#[derive(Debug)] pub struct Read { pub errors: Vec<ToolError>, pub skipped: Skipped }
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)] pub struct Skipped { pub not_json: u64, pub undated: u64 }
#[derive(Debug)] pub struct ToolError { pub at: At, pub service: Slug, pub tool: Slug, pub text: Sample }
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)] pub struct At(String);
impl At { pub fn parse(raw: &str) -> Option<At>; pub fn day(&self) -> Slug; pub fn as_str(&self) -> &str }
pub struct Sample(String);                                  // Debug prints "Sample(..)"
impl Sample { pub const MAX: usize = 500; pub fn narrow<N: Copy>(&self, f: fn(&str) -> N) -> N; pub fn compose(&self, header: &str) -> Report }
#[derive(Serialize)] #[serde(transparent)] pub struct Report(String);
#[derive(Debug)] pub enum ReadError { Dir { path: PathBuf, source: io::Error }, File { path: PathBuf, source: io::Error } }
// Display is "cannot read {path}: {source}"; impl Error; impl From<ReadError> for StepError
pub fn read(dir: &Path) -> Result<Read, ReadError>;
```

`At::parse` accepts a string whose first eleven characters match `YYYY-MM-DDT` and keeps it whole. `read` walks `*.jsonl` entries in sorted path order, keeps one call map across files, deduplicates by event id across files, sorts `errors` by `(at, event id)`, and ignores other extensions. Identity precedence: the repr's `serverName` and `toolName` by regex; else when `toolName` is `tool_call`, the call id with `mcp:` stripped, the segment after the last `:`, split once on `__` (no `__` means `openclaw`); a `tool_call` result with no call block is `openclaw` and `unknown`; else `openclaw` and `toolName`. A non-string `details` is its JSON text, and an empty one falls back to the text blocks of `content`. `slug` maps characters outside the Slug alphabet to `-`, cuts at 200, and turns empty into `unknown`.

- [ ] **Step 1: Write the failing identity tests** in `tests/transcript.rs`:

```rust
#[test] fn each_spelling_reads_identity_from_details()        // Prefixed, Namespaced, Bare with Details::Mcp all give ("brineworks", "triage_apply")
#[test] fn without_mcp_details_the_call_id_names_the_tool()   // Details::NamedOnly: Prefixed and Namespaced give ("brineworks", "triage_apply"); Bare gives ("openclaw", "triage_apply")
#[test] fn a_builtin_tool_belongs_to_openclaw()                // Failure::builtin("read") gives ("openclaw", "read")
#[test] fn a_json_object_in_details_falls_back_to_the_call_id() // Details::Object with Namespaced gives ("brineworks", "triage_apply")
#[test] fn a_result_without_its_call_block_is_openclaw_unknown() // Failure::mcp(..).orphaned() gives ("openclaw", "unknown")
#[test] fn empty_details_falls_back_to_content()               // Details::Empty with error("Approval timed out"); text.narrow(|s| s.contains("Approval timed out"))
#[test] fn an_apostrophe_in_the_repr_still_reads()             // error("it's broken") keeps identity and narrow finds "broken"
```

- [ ] **Step 2: Run them.** Run: `mise exec -- cargo test --test transcript`. Expected: FAIL, `read` not found.

- [ ] **Step 3: Implement `At`, `Sample`, `Report`, `ReadError`, `ToolError`, `Read`, and `read`** per the interface. Two `LazyLock<Regex>` statics for the repr keys.

- [ ] **Step 4: Run them.** Expected: PASS.

- [ ] **Step 5: Write the failing robustness tests:**

```rust
#[test] fn lines_that_are_not_json_are_skipped_and_counted()   // garbage(12): skipped.not_json == 12, errors intact
#[test] fn an_event_id_seen_twice_counts_once()                // duplicated(): one ToolError
#[test] fn an_event_id_seen_in_two_files_counts_once()         // the same Failure with event_id("evt-1") written to a.jsonl and b.jsonl: one ToolError
#[test] fn results_that_are_not_errors_are_ignored()           // succeeded(): errors is empty
#[test] fn an_undated_result_is_counted_and_dropped()          // at("yesterday"): skipped.undated == 1, errors empty
#[test] fn errors_come_back_sorted_by_time()                   // two failures written late then early: errors[0].at < errors[1].at
#[test] fn other_extensions_are_ignored_and_an_empty_dir_is_empty() // a .txt file only: errors empty, skipped zero
#[test] fn a_sample_never_prints_its_text()                    // format!("{:?}", read) has no "synthetic failure"
#[test] fn a_sample_keeps_the_full_text_and_compose_cuts_it()  // 2000-char error: narrow(str::len) == 2000; serde_json::to_value(compose("h")) string ends 500 chars after "sample:\n"
#[test] fn an_unreadable_directory_fails_naming_the_path()     // read("/nonexistent/x"): Err(ReadError::Dir) whose Display contains the path
#[test] fn a_jsonl_entry_that_is_a_directory_fails_naming_it() // create dir "x.jsonl": Err(ReadError::File) naming x.jsonl
```


- [ ] **Step 6: Run, implement what fails, run again.** Expected: all PASS.

- [ ] **Step 7: Run `mise run check`, then commit** `src/transcript.rs tests/` as `Read failed tool calls from OpenClaw transcripts`.

---

### Task 4: tables-and-clusters

**Files:**
- Modify: `src/workflows/tool_failures.rs`
- Test: `tests/tool_failures.rs`

**Interfaces:**
- Consumes: `transcript::{At, Sample, Report, ToolError}`.
- Produces:

```rust
pub struct Class { pub name: &'static str, pattern: Option<&'static str>, pub threshold: u64 }
pub static CLASSES: &[Class];                 // the spec's five rows with their exact phrases, then other (None, 3)
pub fn classify(text: &str) -> &'static Class; // first matching row wins; no match is other
pub fn class_named(name: &str) -> &'static Class; // unknown name is other
pub fn owner_of(service: &Slug) -> Slug;      // brineworks -> technicalpickles/brineworks, else technicalpickles/pickleclaw
#[derive(Clone, Debug, PartialEq, Eq, Hash)] pub struct ClusterKey { pub service: Slug, pub tool: Slug, pub class: &'static str, pub day: Slug }
impl ClusterKey { pub fn delivery_id(&self) -> String }
#[derive(Debug)] pub struct Cluster { pub count: u64, pub first: At, pub last: At, pub sample: Sample }
pub fn cluster(errors: Vec<ToolError>) -> Vec<(ClusterKey, Cluster)>; // sorted by delivery id; input is time-sorted, so the first error of a key supplies first and sample
pub fn report(key: &ClusterKey, cluster: &Cluster) -> Report; // header "service: ..\ntool: ..\nclass: ..\nday: ..\ncount: ..\nfirst: ..\nlast: .." then sample.compose(header)
```

- [ ] **Step 1: Write the failing tests:**

```rust
#[test] fn classify_maps_each_table_text_to_its_class()        // table-driven over the spec's phrases, including all three arg-validation phrases
#[test] fn classify_maps_unknown_text_to_other()
#[test] fn classify_reads_past_500_chars()                     // 600 filler chars then "Approval timed out" classifies as approval-timeout
#[test] fn class_named_falls_back_to_other()                   // class_named("gone").name == "other", threshold 3
#[test] fn every_class_name_and_owner_is_a_slug()              // each CLASSES[i].name and both owner strings parse as Slug
#[test] fn twelve_approval_timeouts_on_one_day_are_one_cluster_of_twelve() // Failure::mcp(..).error("Approval timed out").times(12): one key, count 12, last - first is 11 seconds
#[test] fn a_new_day_is_a_new_cluster()                        // two failures on 2026-11-04 and 2026-11-05: two keys
#[test] fn the_delivery_id_matches_the_spec()                  // "scan:brent/brineworks/triage_apply/approval-timeout/2026-11-04"
#[test] fn report_lists_fields_then_a_blank_line_then_the_sample() // serde_json::to_value(report) starts with "service: brineworks\n" and contains "\n\nsample:\n"
```

- [ ] **Step 2: Run.** Expected: FAIL, items not found.

- [ ] **Step 3: Implement** the tables, `classify` with a `LazyLock<RegexSet>` over the rows that have a pattern, `class_named`, `owner_of`, `ClusterKey`, `Cluster`, `cluster`, and `report`.

- [ ] **Step 4: Run.** Expected: PASS.

- [ ] **Step 5: Run `mise run check`, then commit** `src/workflows/tool_failures.rs tests/tool_failures.rs` as `Classify and cluster failed tool calls`.

---

### Task 5: event-scan-and-policy

**Files:**
- Modify: `src/workflows/tool_failures.rs`, `src/workflows/mod.rs`, `src/providers.rs` (provide `Transcripts` from `BRENT_TRANSCRIPTS`, default `transcripts`), `tests/support/mod.rs` (add `Fixture::providers(&self, tracker: Arc<dyn Tracker>) -> Providers`)
- Test: `tests/tool_failures.rs`

**Interfaces:**
- Consumes: Task 4's items, `almanac::prelude`, `almanac::store::MemoryStore`, `almanac::runtime::{Runtime, Outcome}`, `almanac::testing::FakeTracker`.
- Produces:

```rust
pub const TITLE: &str = "Brent: failed tool calls";
observed! { pub struct ToolFailed { pub service: Slug, pub tool: Slug, pub class: Slug, pub day: Slug, pub count: u64, pub report: Untrusted<String> } }
impl Event for ToolFailed { const KIND: &'static str = "brent.tool.failed"; fn parse(p: &Payload<'_>) -> Result<Self, Malformed> } // field for five, text("/report")
pub async fn scan(scanner: &Scanner) -> Result<Vec<Scanned<ToolFailed>>, StepError>; // get Transcripts, read, one eprintln per nonzero Skipped counter naming the directory, cluster, one Scanned per cluster with delivery_id and a json! payload of the slugs, count, and report
#[derive(Serialize, Deserialize)] pub enum Facts { Quiet, Loud { target: Slug, body: Untrusted<String> } }
pub fn facts(event: &ToolFailed) -> Facts;     // count < class_named(class).threshold is Quiet; else Loud with owner_of(service) and event.report.map(|s| s.clone())
#[workflow(on = ToolFailed, key = "{service}:{tool}:{class}:{day}", intent = "File one issue per cluster of failed tool calls in Brent's transcripts.", may = [File], runs_per_hour = 1000, scan = scan, decide = policy)]
pub async fn tool_failures(_cx: &Cx, event: &ToolFailed) -> Result<Facts, Halt> { Ok(facts(event)) }
pub fn policy(facts: &Facts) -> Vec<Effect>;   // Quiet is empty; Loud is one Effect::File { title: TITLE, target, body }
```

`workflows::all` registers `ToolFailures`. A helper in the test file, `fn tool_failed(service, tool, class, count) -> ToolFailed`, builds an event through `ToolFailed::from_json` on a `json!` payload.

- [ ] **Step 1: Write the failing tests:**

```rust
#[test] fn files_at_the_threshold_and_stays_quiet_one_below()  // for every row of CLASSES: facts at threshold is Loud, at threshold - 1 is Quiet
#[test] fn brineworks_and_gog_route_to_their_repositories()    // Loud targets technicalpickles/brineworks and technicalpickles/pickleclaw
#[test] fn policy_files_once_with_the_static_title()           // one Effect::File with title TITLE; Quiet gives []
#[test] fn the_workflow_key_carries_no_state()                 // ToolFailures::key(&event) == "brineworks:triage_apply:approval-timeout:2026-11-04" for count 3 and count 30
#[tokio::test] async fn a_hostile_tool_name_still_makes_a_valid_payload() // Failure::mcp("brineworks", "weird tool/ü"): backfill found == 1 and rejected == 0
#[tokio::test] async fn backfill_records_one_file_for_twelve_timeouts() // dry-run runtime over the twelve-timeout fixture: settle() has one Outcome::Ran whose effects is one File targeting technicalpickles/brineworks
#[tokio::test] async fn scanning_twice_reports_the_second_scan_as_duplicates() // same runtime: second backfill has unchanged == first.found and queued == 0
#[tokio::test] async fn a_fresh_store_queues_everything_again()  // a second Runtime over the same fixture: queued == first.found. This documents why live runs need DATABASE_URL
#[tokio::test] async fn a_live_run_files_once_per_loud_cluster() // Mode::Live with a FakeTracker, twelve timeouts plus one lone "synthetic failure": after settle, filed has one entry whose target is technicalpickles/brineworks and title is TITLE
#[tokio::test] async fn a_dry_run_calls_no_tracker()             // Mode::DryRun: FakeTracker.calls is empty after settle
```

Runtime construction: `Runtime::new(workflows::all(), fx.providers(tracker)).with_store(Arc::new(MemoryStore::default())).in_mode(mode)`, then `backfill(None).await.unwrap()` and `settle().await.unwrap()`.

- [ ] **Step 2: Run.** Expected: FAIL.

- [ ] **Step 3: Implement** the event, `scan`, `Facts`, `facts`, the workflow, `policy`, the registration, the `Transcripts` provider, and `Fixture::providers`.

- [ ] **Step 4: Run.** Expected: PASS.

- [ ] **Step 5: Smoke the binary.**

Run: `mise exec -- cargo run -- synth` and `BRENT_TRANSCRIPTS=/nonexistent mise exec -- cargo run -- backfill --dry-run`
Expected: `synth` lists `tool-failures` with `scans` set and `File` in its effects. The backfill fails with a message naming `/nonexistent` and no stack trace.

- [ ] **Step 6: Run `mise run check`, then commit** as `Emit one ToolFailed event per cluster and file at threshold`.

---

### Task 6: private-only-tracker

**Files:**
- Modify: `src/providers.rs`
- Test: `tests/private_only.rs`

**Interfaces:**
- Consumes: `github::client::{Auth, GithubClient}`, `github::tracker::GithubIssues`, `almanac::testing::FakeTracker`.
- Produces:

```rust
pub trait Visibility: Send + Sync { fn is_private<'a>(&'a self, repo: &'a Slug) -> BoxFuture<'a, Result<bool, StepError>>; }
impl Visibility for GithubClient;              // GET /repos/{repo}; a missing or non-boolean private flag is Err naming the repo
pub struct PrivateOnly<T, V> { inner: T, visibility: V, private: Mutex<HashSet<Slug>> } // only true answers are remembered
impl<T: Tracker, V: Visibility> PrivateOnly<T, V> { pub fn new(inner: T, visibility: V) -> Self; async fn check(&self, target: &Slug) -> Result<(), StepError> } // Err is "{target} is not private, refusing to file"; a lookup Err passes through
impl<T: Tracker, V: Visibility> Tracker for PrivateOnly<T, V>; // file and ask both check first
struct LogTracker;                              // println key, target, title, then the body read as the only value of its serialized one-key object
pub fn all(_config: &Config) -> Providers;      // BRENT_TRANSCRIPTS; with GITHUB_TOKEN: PrivateOnly::new(GithubIssues::new(API, token), GithubClient::new(API, Auth::Token(token))), else LogTracker
```

The test file defines `FakeVisibility { answer: Result<bool, String>, asked: Mutex<Vec<String>> }` implementing `Visibility`.

- [ ] **Step 1: Write the failing tests:**

```rust
#[tokio::test] async fn refuses_a_public_target_and_files_nothing() // answer Ok(false): Err contains "technicalpickles/brineworks"; FakeTracker.filed is empty
#[tokio::test] async fn files_on_a_private_target()                 // answer Ok(true): one filed entry
#[tokio::test] async fn a_failed_lookup_files_nothing()             // answer Err: Err, nothing filed
#[tokio::test] async fn asks_once_for_a_private_target()            // two file calls, asked.len() == 1
#[tokio::test] async fn asks_again_after_a_public_answer()          // Ok(false) twice: asked.len() == 2
#[tokio::test] async fn ask_is_guarded_like_file()                  // ask on a public target is refused
```

- [ ] **Step 2: Run.** Expected: FAIL.

- [ ] **Step 3: Implement** `Visibility`, `PrivateOnly`, `LogTracker`, and the full `providers::all`.

- [ ] **Step 4: Run.** Expected: PASS. Then `mise exec -- cargo test` for the whole suite. Expected: PASS.

- [ ] **Step 5: Run `mise run check`, then commit** as `Refuse to file on a repository that is not private`.

---

### Task 7: dry-run-on-real-transcripts

Verification only. No code unless the counts disagree.

**Files:**
- Read: the three pulled transcripts under `~/github.com/technicalpickles/pickleclaw/.scratch/openclaw-pulls/`

- [ ] **Step 1: Validate providers in live mode without a token.**

Run: `ALMANAC_MODE=live BRENT_TRANSCRIPTS=<pulls dir> mise exec -- cargo run -- check`
Expected: ok. (Plain `check` skips the scan's providers, ivy/almanac#1.)

- [ ] **Step 2: Dry-run the backfill.**

Run: `BRENT_TRANSCRIPTS=<pulls dir> mise exec -- cargo run -- backfill --dry-run`
Expected: `found: 137`, `rejected: 0`, and 37 outcomes carrying a `File` effect. The spike produced those numbers at `runs_per_hour = 1000`.

- [ ] **Step 3: If the counts differ,** diff the cluster keys against `local/spike`'s output before changing anything, and record the cause in the spec's Shapes section.

- [ ] **Step 4: Note the result** in the commit message of a docs-only commit that adds a "Verified" line with the date and counts to the spec's Testing section.
