# Tiers in code, and security-alerts: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Declare repository tiers in code, then add a security-alerts workflow that files one taskwarrior task per repository whose Dependabot or secret scanning alerts have outlived their severity's window, plus one task listing coverage gaps.

**Architecture:** `src/tiers.rs` parses a compiled-in `tiers.toml` and an optional private `local/tiers.toml` into a typed registry. `src/workflows/security_alerts.rs` has a scan that reads GitHub with a read-only client and does every time-dependent decision, emitting one `SecurityFinding` event per finding with a digest-based delivery ID. The workflow only formats and files, so `simulate` dry-runs it with no token.

**Tech Stack:** Rust 2024, almanac and github crates at rev `b8bdefa`, `toml`, `sha2`, `time`.

**Spec:** `docs/superpowers/specs/2026-11-08-tiers-and-security-alerts-design.md`. Read it before any task.

## Global Constraints

- brined-almanac is a public repository. No committed file names a repository from `local/tiers.toml`. In a worktree, copy `local/` from the main checkout (`cp -R ../../../local .` from the worktree root) so `scripts/check-forbidden` runs its literal check instead of skipping it.
- Every finding files to a `task:<project>` target. Nothing files an issue.
- Windows, verbatim: secret 0 days, critical 2, high 14, medium 30, unknown 30, low 90.
- Delivery IDs: `scan:security-alerts/<owner>/<repo>@<digest>` and `scan:security-alerts/coverage@<digest>`. Digest is the first 12 hex characters of SHA-256 over the sorted keys, each `dependabot:<number>` or `secret:<number>`, joined with `\n`. Coverage keys are `dependabot-off:<owner>/<repo>` and `secret-scanning-off:<owner>/<repo>`.
- The secret scanning `secret` field is never read into any struct.
- Commit with the `git:commit` skill, signed, never bypassing signing. Push with plain `git push`. Open PRs with the `git:pull-request` skill and `gh pr create --head <branch>`. PR bodies have no em dashes.
- `mise run check` and `mise run test` pass at the end of every task.

## Review Focus

1. An alert with no `first_patched_version` still counts and its line says `no fix yet` (test in `alerts-model`).
2. A severity string GitHub adds later (anything outside low, medium, high, critical) maps to `Unknown` instead of failing the scan (test in `alerts-model`).
3. A repository in `tiers.toml` that was renamed or transferred: the API redirects, and the scan would report under the old name. `scripts/check-tiers` flags any entry whose `full_name` differs (step in `tiers-public-file`).
4. A repository whose overdue alerts all get fixed emits no event, so nothing files and no empty task appears (test in `alerts-model`).
5. An alert created late in the day near a window boundary: age is computed on UTC dates from `created_at`, so the boundary does not move with the Mac's time zone (test in `alerts-model`).

---

## PR 1: tiers

Branch: `tiers-in-code`, from `origin/main`, via `wt switch --create tiers-in-code --base origin/main --yes --no-cd --format=json`.

### Task `tiers-model`

**Files:**
- Create: `src/tiers.rs`
- Modify: `src/lib.rs` (add `pub mod tiers;`), `Cargo.toml` (add `toml = "0.8"`)
- Test: `tests/tiers.rs`

**Interfaces:**
- Produces:
  - `pub enum Tier { Dormant, Deployed, Active, Maintained }`, serde `rename_all = "lowercase"`, `Copy + Ord + Hash + Debug`.
  - `pub struct Tiers` with `pub fn parse(public: &str, local: Option<&str>) -> Result<Tiers, String>`, `pub fn load(local: Option<&Path>) -> Result<Tiers, String>` (public file from `include_str!("../tiers.toml")`; `None` prints `tiers: LOCAL_TIERS is not set, loading public tiers only` to stderr), and `pub fn watched(&self, any_of: &[Tier]) -> Vec<&Slug>`, sorted.
  - `pub const WATCHED_BY_SECURITY_ALERTS: &[Tier] = &[Tier::Deployed, Tier::Active, Tier::Maintained];`

- [ ] **Step 1: Write the failing tests** in `tests/tiers.rs`. Inline TOML strings, not the real file:
  - `parses_declarations_from_both_files`: public declares `technicalpickles/a` active, local declares `technicalpickles/b` deployed; `watched(&[Active, Deployed])` is `[a, b]`.
  - `a_repository_in_both_files_is_an_error`: error message contains `technicalpickles/a` and `both`.
  - `an_unknown_tier_is_an_error`: `tiers = ["sleepy"]` errors, message contains `sleepy`.
  - `an_unknown_key_is_an_error`: `teirs = ["active"]` errors, message contains `teirs`.
  - `an_empty_tier_list_is_an_error`: `tiers = []` errors, message names the repository.
  - `a_name_without_an_owner_is_an_error`: `name = "a"` errors.
  - `watched_keeps_a_repository_that_is_dormant_and_deployed`: with `[dormant, deployed]`, `watched(WATCHED_BY_SECURITY_ALERTS)` includes it, and a dormant-only one is left out.
  - `load_with_a_missing_path_is_an_error`: `Tiers::load(Some(Path::new("/nonexistent/tiers.toml")))` errors with the path in the message.
- [ ] **Step 2: Run `cargo test --test tiers`.** Expected: compile failure, `tiers` not found.
- [ ] **Step 3: Implement `src/tiers.rs`.** A private `#[serde(deny_unknown_fields)] struct Declaration { name: String, tiers: BTreeSet<Tier>, signal: Option<String>, note: Option<String> }` inside `struct File { repo: Vec<Declaration> }`. `name` must contain exactly one `/` and parse as `Slug`. Store `BTreeMap<Slug, BTreeSet<Tier>>`.
- [ ] **Step 4: Run `cargo test --test tiers`.** Expected: all pass.
- [ ] **Step 5: Commit** `Add Tier and a tiers registry parsed from two TOML files`.

### Task `tiers-public-file`

**Files:**
- Create: `tiers.toml`, `scripts/check-tiers`
- Modify: `docs/tiers.md`, `tests/tiers.rs`

**Interfaces:**
- Consumes: `Tiers::parse`, `Tiers::load` from `tiers-model`.

- [ ] **Step 1: Write the failing test** `the_committed_file_parses_and_watches_the_documented_repositories`: `Tiers::load(None)` succeeds, and `watched(WATCHED_BY_SECURITY_ALERTS)` contains `technicalpickles/dotfiles`, `technicalpickles/sb`, `technicalpickles/cq`, `technicalpickles/pickled-claude-plugins`, and leaves out `technicalpickles/chirpfinder`.
- [ ] **Step 2: Run it.** Expected: FAIL, `tiers.toml` missing.
- [ ] **Step 3: Write `tiers.toml`.** One `[[repo]]` per row in `docs/tiers.md`'s Active, Deployed, Maintained, and Dormant tables, owner `technicalpickles`, tiers merged across tables (so `sb` is `["deployed", "maintained"]`, `brineworks` is `["active", "deployed"]`). Carry each table's usage-signal and note text into `signal` and `note`. Rows that list several repositories (`a2a-experiments, a2a-cli`, the `town-charter, ...` row) become one entry each. The "picklelab services with no repository" row is not a repository and stays in the doc. Header comment: the schema, that `local/tiers.toml` uses the same one, and that code defines the tiers.
- [ ] **Step 4: Write `scripts/check-tiers`** (bash, shellcheck-clean). For each `name` in `tiers.toml`, and in `local/tiers.toml` when it exists, run `gh api repos/<name> --jq .full_name`. Print `missing <name>` on a 404 and `renamed <name> -> <full_name>` on a mismatch. Exit 1 if anything printed.
- [ ] **Step 5: Run `scripts/check-tiers`.** Expected: exit 0. Fix any entry it flags, such as an owner other than `technicalpickles`, by reading `gh api repos/<name>`.
- [ ] **Step 6: Cut `docs/tiers.md`.** Remove the four tables. Keep the intro (rewritten to say `tiers.toml` is the declaration and `src/tiers.rs` defines the tiers), the 2026-11-08 commit counts as a short dated snapshot paragraph, "Handed off and retired", and "Unclear". Update `docs/workflows.md` "Repository tiers", which says the declarations live in `docs/tiers.md` until M1, to point at `tiers.toml`.
- [ ] **Step 7: Run `cargo test --test tiers` and `scripts/check-forbidden tiers.toml docs/tiers.md`.** Expected: pass, and no private names.
- [ ] **Step 8: Commit** `Move the tier declarations into tiers.toml`.

### Task `tiers-prod-wiring`

**Files:**
- Modify: `src/providers.rs`, `compose.prod.yaml`, `scripts/deploy`, `docs/runbooks/prod-on-the-mac.md`
- Test: `tests/providers.rs`

**Interfaces:**
- Consumes: `Tiers::load`.
- Produces: `Tiers` provided as `Arc<Tiers>`, retrievable with `scanner.get::<Tiers>()`. `pub fn tiers_from(lookup: impl Fn(&str) -> Option<String>) -> Result<Tiers, String>` reads `LOCAL_TIERS`, empty value treated as unset (same rule as `choose`).

- [ ] **Step 1: Write the failing tests** in `tests/providers.rs`: `tiers_from_unset_loads_public_only` (succeeds and watches `technicalpickles/dotfiles`), `tiers_from_a_missing_path_is_an_error` (message contains `LOCAL_TIERS` and the path), `tiers_from_an_empty_value_loads_public_only`.
- [ ] **Step 2: Run `cargo test --test providers`.** Expected: compile failure.
- [ ] **Step 3: Implement `tiers_from`**, and in `providers::all` call it and `panic!` on error, as `choose` does, then `.provide::<Tiers>(Arc::new(tiers))`.
- [ ] **Step 4: Run `cargo test`.** Expected: pass.
- [ ] **Step 5: Wire prod.** In `scripts/deploy`, after the state-dir checks and before `docker build`, fetch with `OP_SERVICE_ACCOUNT_TOKEN="$(cat "$STATE/op-token")" op document get tiers.toml --vault brined-almanac --out-file "$STATE/tiers.toml" --force`, `chmod 600` it, and exit 1 with `deploy: could not fetch tiers.toml from the brined-almanac vault; see $runbook` on failure. In `compose.prod.yaml`'s `almanac` service add the volume `"${BRINED_ALMANAC_STATE:-${HOME}/.local/share/brined-almanac}/tiers.toml:/etc/brined-almanac/tiers.toml:ro"` and the environment `LOCAL_TIERS: /etc/brined-almanac/tiers.toml`. Add a runbook paragraph on what deploy fetches and how to update the vault document (the commands from the `local/tiers.toml` header).
- [ ] **Step 6: Verify.** Run `scripts/compose-config-check` and `shellcheck scripts/deploy`. Then run `LOCAL_TIERS=$PWD/local/tiers.toml cargo run -- check` and `cargo run -- check`. Expected: both succeed, the second prints the public-only line.
- [ ] **Step 7: Commit** `Load local tiers in prod from the vault document`, then open PR 1.

---

## PR 2: security-alerts

Branch: `security-alerts`, created after PR 1 merges, from `origin/main`.

### Task `alerts-model`

**Files:**
- Create: `src/workflows/security_alerts.rs` (model section only in this task)
- Modify: `src/workflows/mod.rs` (`pub mod security_alerts;`), `Cargo.toml` (`sha2 = "0.10"`, `time = { version = "0.3", features = ["parsing", "macros"] }`)
- Test: `tests/security_alerts.rs`

**Interfaces:**
- Produces:
  - `pub enum Severity { Low, Medium, High, Critical, Unknown }` with `pub fn from_github(s: &str) -> Severity`.
  - `pub enum Alert { Dependency { number: u64, severity: Severity, package: String, manifest: String, ghsa: String, patched: Option<String>, created: Date, url: String }, Secret { number: u64, kind: String, created: Date, url: String } }` with `pub fn key(&self) -> String` (`dependabot:<n>` or `secret:<n>`), `pub fn window_days(&self) -> i64`, `pub fn line(&self, today: Date) -> String`.
  - `pub fn overdue(alerts: &[Alert], today: Date) -> Vec<&Alert>`.
  - `pub fn digest(keys: &[String]) -> String`.
  - `pub fn parse_dependabot(v: &Value) -> Result<Alert, String>`, `pub fn parse_secret(v: &Value) -> Result<Alert, String>`. Dependabot fields: `number`, `security_advisory.severity`, `security_advisory.ghsa_id`, `dependency.package.name`, `dependency.manifest_path`, `security_vulnerability.first_patched_version.identifier` (optional), `created_at`, `html_url`. Secret fields: `number`, `secret_type_display_name` (fall back to `secret_type`), `created_at`, `html_url`. `created` is the UTC date of `created_at`.

- [ ] **Step 1: Write the failing tests:**
  - `windows_match_the_spec`: a secret is 0, critical 2, high 14, medium 30, unknown 30, low 90.
  - `overdue_starts_on_the_last_day_of_the_window`: a high created 2026-10-25 is not overdue on 2026-11-07 and is overdue on 2026-11-08. A secret created today is overdue today.
  - `age_uses_the_utc_date`: `created_at` `2026-10-25T23:30:00-05:00` parses to created 2026-10-26.
  - `digest_ignores_order_and_changes_with_the_set`: same keys in two orders give the same 12-character hex digest, and adding one key changes it.
  - `an_unknown_severity_is_unknown`: `from_github("moderate")` is `Unknown`, `from_github("high")` is `High`.
  - `a_missing_patched_version_says_no_fix_yet`: the line contains `no fix yet`.
  - `a_secret_value_never_reaches_the_line`: parse a recorded secret alert whose `secret` is `ghp_DO_NOT_LEAK`, and the line does not contain it.
  - `nothing_overdue_is_empty`: three alerts inside their windows give an empty `overdue`.
  - Fixtures: `tests/fixtures/security_alerts/dependabot_alert.json` and `secret_alert.json`, each a real response item trimmed to the fields above plus `secret`.
- [ ] **Step 2: Run `cargo test --test security_alerts`.** Expected: compile failure.
- [ ] **Step 3: Implement the model section.** Windows in one `const` table keyed by a `Kind` enum, per the spec's "The overdue rule". `line` format: `#<n> <severity> <package> (<manifest>) <ghsa>, fixed in <patched>, <age> days, <url>`, and for a secret `#<n> secret <kind>, <age> days, <url>`.
- [ ] **Step 4: Run the tests.** Expected: pass.
- [ ] **Step 5: Commit** `Add the security alert model, windows, and digest`.

### Task `alerts-github`

**Files:**
- Modify: `src/workflows/security_alerts.rs` (reading section)
- Test: `tests/security_alerts.rs`

**Interfaces:**
- Consumes: `parse_dependabot`, `parse_secret`.
- Produces:
  - `pub enum Coverage<A> { On(Vec<A>), Off }`.
  - `pub struct RepoRead { pub private: bool, pub dependabot: Coverage<Alert>, pub secrets: Coverage<Alert> }`.
  - `pub fn alerts_switch(result: Result<String, almanac::diag::Error>, repo: &Slug) -> Result<bool, StepError>`: `Ok(_)` is on, `ForgeRefused { status: 404 }` is off, anything else is an error naming the repository.
  - `pub fn secret_alerts(result: Result<Vec<Value>, almanac::diag::Error>, repo: &Slug) -> Result<Coverage<Alert>, StepError>`: 404 is `Off`.
  - `pub fn repo_missing(err: almanac::diag::Error, repo: &Slug) -> StepError`: on 404 the message contains the repository and `check tiers.toml and local/tiers.toml`.
  - `pub async fn read_repo(client: &GithubClient, repo: &Slug) -> Result<RepoRead, StepError>`, following the spec's four steps in order.
  - `pub struct AlertsClient(pub GithubClient)`.

- [ ] **Step 1: Write the failing tests** for the three pure mappers, constructing `Error::ForgeRefused { action, status }` directly: 404 on vulnerability-alerts is off, `Ok("")` is on, 403 is an error containing the repository and `403`; secret scanning 404 is `Off` and 401 is an error; a repository 404 message names both tiers files.
- [ ] **Step 2: Run them.** Expected: compile failure.
- [ ] **Step 3: Implement the mappers and `read_repo`.** `vulnerability-alerts` goes through `get_text`, and both alert lists go through `get_all`. Dependabot alerts are only fetched when the switch is on.
- [ ] **Step 4: Run them.** Expected: pass.
- [ ] **Step 5: Commit** `Read alerts and their on or off state from GitHub`.

### Task `alerts-workflow`

**Files:**
- Modify: `src/workflows/security_alerts.rs` (event, scan, workflow), `src/workflows/mod.rs` (register in `all()`), `src/providers.rs`
- Create: `tests/fixtures/security_alerts/overdue_event.json`, `coverage_event.json`
- Test: `tests/security_alerts.rs`, `tests/providers.rs`

**Interfaces:**
- Consumes: `Tiers`, `WATCHED_BY_SECURITY_ALERTS`, `read_repo`, `overdue`, `digest`, `Project`.
- Produces:
  - Event kind `security.alert.finding`: `observed! pub struct SecurityFinding { kind: Slug, project: Slug, subject: Slug, critical: u64, high: u64, medium: u64, low: u64, unknown: u64, secrets: u64, dependabot_off: u64, secret_scanning_off: u64, lines: Untrusted<String> }`. `kind` is `overdue` or `coverage`. `subject` is `owner/repo` for overdue and `coverage` for coverage.
  - `pub fn findings(reads: &[(Slug, RepoRead)], today: Date) -> Vec<(String, Value)>`: pure, returns delivery ID and payload pairs. One `overdue` entry per repository with a non-empty overdue set, and one `coverage` entry when any repository has Dependabot off, or is public with secret scanning off.
  - `pub fn facts(e: &SecurityFinding) -> Facts` and `pub fn policy(f: &Facts) -> Vec<Effect>`: one `Effect::File` with target `task:<project>`.
  - The workflow: `key = "{subject}"`, `may = [File]`, `runs_per_hour = 1000`, `scan = scan`, `decide = policy`.
  - In `providers::all`: `.provide::<AlertsClient>(...)` built from `GITHUB_ALERTS_TOKEN` with `GithubClient::new(API, Auth::Token(t)).read_only()` when set. When it is unset, the scan returns `StepError("GITHUB_ALERTS_TOKEN is not set; security-alerts needs a read-only alerts token")`.

- [ ] **Step 1: Write the failing tests:**
  - `findings_files_one_task_per_repository_with_overdue_alerts`: two repositories, one with an overdue high and one with nothing overdue. One `overdue` finding, delivery ID `scan:security-alerts/technicalpickles/dotfiles@<digest>`, project `dotfiles`.
  - `findings_lists_coverage_gaps_once`: a private repository with Dependabot off and secret scanning off, and a public one with secret scanning off. One `coverage` finding with `dependabot_off` 1, `secret_scanning_off` 1 (the private repository's secret scanning is not a gap), project `brined-almanac`.
  - `no_gaps_and_nothing_overdue_is_no_findings`.
  - `the_title_uses_counts_only`: an overdue event with 12 high, 5 medium, 1 low gives the title `security-alerts: 18 overdue on technicalpickles/dotfiles (12 high, 5 medium, 1 low)`. A coverage event gives `security-alerts: alerts are off on 2 repositories`.
  - `policy_files_to_the_task_target`: target is `task:dotfiles`, through `Project::target`.
  - `the_runtime_files_once_per_delivery`: following `tests/tool_failures.rs`, run the overdue fixture through `Runtime` with `FakeTracker` twice under the same delivery ID, and expect one filing.
  - `alerts_client_absent_without_a_token` in `tests/providers.rs`.
- [ ] **Step 2: Run `cargo test`.** Expected: compile failure.
- [ ] **Step 3: Implement.** The scan gets `Tiers` and `AlertsClient`, reads every watched repository with `read_repo` one at a time, calls `findings` with `OffsetDateTime::now_utc().date()`, and wraps each pair in `Scanned::new`. Lines are joined with `\n` into the `lines` text, grouped by severity, critical first, then secrets.
- [ ] **Step 4: Run `cargo test`.** Expected: pass.
- [ ] **Step 5: Dry run.** `cargo run -- simulate security.alert.finding tests/fixtures/security_alerts/overdue_event.json --dry-run`. Expected: a `FILE ... target=task:dotfiles` line and the alert lines, with no token and no database.
- [ ] **Step 6: Commit** `Add security-alerts: file overdue alerts and coverage gaps as tasks`.

### Task `alerts-prod-and-docs`

**Files:**
- Modify: `compose.prod.yaml`, `prod.secrets.env`, `scripts/deploy`, `docs/runbooks/prod-on-the-mac.md`, `docs/workflows.md`, `docs/vision.md`, `docs/roadmap.md`

- [ ] **Step 1: Wire the token.** `prod.secrets.env`: `BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=op://brined-almanac/github-alerts-token/token`. `compose.prod.yaml`, `almanac` service: `GITHUB_ALERTS_TOKEN: "${BRINED_ALMANAC_GITHUB_ALERTS_TOKEN:?run `mise run deploy`, which resolves it through op run}"`. Add `BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused` to both `unused` blocks in `scripts/deploy`.
- [ ] **Step 2: Runbook.** Add the `github-alerts-token` item next to `github-token`, with the spec's four permissions, all repositories, and the check: `GH_TOKEN=<new token> gh api -i repos/technicalpickles/dotfiles/vulnerability-alerts` answers 204.
- [ ] **Step 3: Docs**, per the spec's "Doc changes": workflows.md security-alerts entry, vision.md success line, roadmap.md M1 bullets and done-when.
- [ ] **Step 4: Verify.** `scripts/compose-config-check`, `shellcheck scripts/deploy`, `scripts/check-forbidden` on every changed file. Expected: pass.
- [ ] **Step 5: Commit** `Wire the alerts token and update the docs for security-alerts`.

### Task `alerts-live-check`

No new files. This proves the workflow against real GitHub before PR 2 opens.

- [ ] **Step 1: Dry-run backfill against GitHub.** `GITHUB_ALERTS_TOKEN=$(gh auth token) LOCAL_TIERS=$PWD/local/tiers.toml cargo run -- backfill --dry-run`. Expected: one overdue finding for each of sb, dotfiles, cq, and pickled-claude-plugins whose alerts are past their windows, and one coverage finding.
- [ ] **Step 2: Cross-check the counts.** For each overdue finding, count with `gh api --paginate "repos/<repo>/dependabot/alerts?state=open"` and the windows. Expected: the counts match. If any differ, stop and report rather than adjust the windows.
- [ ] **Step 3: Rerun against a store.** Start the dev database with `mise run db`. Run `GITHUB_ALERTS_TOKEN=$(gh auth token) LOCAL_TIERS=$PWD/local/tiers.toml mise run live -- backfill` twice with no `TASKCHAMPION_SYNC_URL`, so the tasks side is `LogTracker` and prints instead of syncing. Expected: the first run prints a `FILE` line for each finding from step 1, and the second prints none, because the store has seen every delivery ID.
- [ ] **Step 4: Open PR 2.** The body includes the counts from step 2.

Steps only I can do, after PR 2 merges: create the `github-alerts-token` item, confirm the vault's `tiers.toml` document matches `local/tiers.toml`, and deploy.
