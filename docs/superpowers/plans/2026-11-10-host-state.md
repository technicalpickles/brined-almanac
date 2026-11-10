# Host State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When almanac's puller stops completing pulls, or a container in the prod stack stops or turns unhealthy, a task appears in the `brined-almanac` taskwarrior project within an hour, and a rescan files nothing new.

**Revised 2026-11-10** after `launchd-spike`: the first checks are `puller-age` and `stack-health`, in place of `gh-auth`. The spec's "What this slice was first" says why. `report-and-rule` and `host-state-workflow` were built before the change and use `gh-auth` only as a check name in test data, which still stands.

**Architecture:** Two checks in the Nagios plugin convention run on the Mac. A shell collector, run by a LaunchAgent every 300 seconds, writes every check's result to one JSON report in the prod state directory. The almanac container mounts that directory read-only, and a `host-state` workflow turns reports into `File` effects on `task:brined-almanac`.

**Tech Stack:** bash, `jq`, `gtimeout` (coreutils), launchd, Rust 1.99 with the `almanac` crate at `b8bdefa`, `serde`, `time`, `toml`.

**Spec:** [docs/superpowers/specs/2026-11-10-host-state-design.md](../specs/2026-11-10-host-state-design.md)

Tasks are named by slug. Commits and cross-references cite the slug. Each slug except `launchd-spike` is one pull request.

| Slug | Depends on | Stops for Josh |
|---|---|---|
| `launchd-spike` | nothing | done 2026-11-10 |
| `checks-and-collector` | `launchd-spike` | nothing |
| `report-and-rule` | nothing | nothing |
| `host-state-workflow` | `report-and-rule` | nothing |
| `prod-wiring` | all of the above merged | `mise run deploy`, and one sleep and wake of the Mac |

`checks-and-collector` and `report-and-rule` touch disjoint files and can run at the same time. They meet only at the report's JSON, which the spec fixes.

## Global Constraints

- Check exit codes: 0 OK, 1 warning, 2 critical, 3 unknown. One status line on stdout.
- Check timeout: 20 seconds. A timeout is status 2 with the output `<NAME> CRITICAL - timed out after 20s`, where `<NAME>` is the check's name in upper case.
- Collector interval: 300 seconds. LaunchAgent label: `com.technicalpickles.brined-almanac.host-state`.
- Report path: `$STATE/host-state/<host>.json`, where `STATE` is `${BRINED_ALMANAC_STATE:-$HOME/.local/share/brined-almanac}`. In the container: `/host-state/<host>.json`, read through `HOST_STATE_REPORTS=/host-state`.
- Report fields, exactly: `host`, `generated_at`, `interval_seconds`, `checks[]` with `name`, `status`, `since`, `output`. Times are RFC 3339 in UTC. `name` matches `[a-z0-9-]+`. `output` is at most 500 bytes and one line.
- Filing thresholds, as named constants in one place: critical or unknown after 10 minutes, warning after 24 hours, report stale after 30 minutes.
- Delivery keys: `<host>/<check>/<UTC date of since>` and `<host>/stale/<UTC date>`.
- Tasks file to `task:brined-almanac`. The workflow may `File` and nothing else, and calls no model.
- No token and no secret value reaches a status line, a report, a log, or a test fixture.
- No environment variable inline on a command line. Tests export what they need inside the test script.
- Before each push, run `scripts/check-forbidden` (copy `local/forbidden-strings` from the main checkout into the worktree first) and `mise run check`.
- Work in a `wt` worktree off `origin/main`. Commit with the git:commit skill, open pull requests with git:pull-request.

## Review Focus

Inputs the spec implies and no happy-path test reaches, most likely first. Each has a test in the task named.

1. **A `since` or `generated_at` later than now** (clock skew, or the container's clock behind the Mac's after a wake). Expected: the age counts as zero, nothing files, nothing panics. Test in `report-and-rule`.
2. **A status line with newlines, control bytes, backticks, or 5 KB of text.** Expected: the collector keeps the first line, cut to 500 bytes on a character boundary, and the report is still valid JSON. Test in `checks-and-collector`.
3. **A report file whose `host` field differs from its file name, or a file for a host `hosts.toml` doesn't declare.** Expected: an error in the scan's log, no task. Test in `report-and-rule` and `host-state-workflow`.
4. **A previous report that is empty, truncated, or not JSON.** Expected: the collector writes a fresh report with every `since` reset, and exits 0. Test in `checks-and-collector`.
5. **Two entries with the same check name, or no checks at all.** Expected: a duplicate name is an invalid report, and an empty list is valid and files nothing. Test in `report-and-rule`.

---

### launchd-spike

**Done 2026-11-10.** A throwaway LaunchAgent ran `gh auth status`, `av list --json`, `av history --json`, and `av doctor gh --json` once each. The first three timed out waiting on a human approval from the broker, and `av doctor` is not a broker probe. The agent was unloaded and deleted. The table and what it means are in the spec under "What was measured" and "What this slice was first". The result replaced `gh-auth` with the two checks below.

---

### checks-and-collector

**Files:**
- Create: `checks/puller-age`, `checks/stack-health`, `checks/collect`, `checks/hosts/mac.conf`
- Create: `checks/puller-age.test.sh`, `checks/stack-health.test.sh`, `checks/collect.test.sh`
- Modify: `mise.toml` (`[tasks.test]` runs the new test scripts), `hk.pkl` (shellcheck covers `checks/`)

**Interfaces:**
- Produces: the report file described in Global Constraints. `report-and-rule` parses it.
- Produces: `checks/collect <conf>`, where the conf file is shell assignments: `HOST=mac`, `OUT=<path>`, `CHECKS="puller-age stack-health"`, `CHECK_DIR=<dir>`. `prod-wiring` runs it from the LaunchAgent. A relative `OUT` or `CHECK_DIR` resolves against the conf file's directory.
- Environment the tests may set: `HOST_STATE_NOW` (an RFC 3339 time the collector uses in place of the clock) and `CHECK_TIMEOUT` (seconds, default 20). For `puller-age`: `PULLER_MARKER` (the marker file, default `${BRINED_ALMANAC_STATE:-$HOME/.local/share/brined-almanac}/transcripts/.last-pull`), `PULLER_MAX_AGE` (seconds, default 1800), and `CHECK_NOW` (epoch seconds in place of the clock). For `stack-health`: `STACK_PROJECT` (default `brined-almanac-prod`).

Both test scripts follow `puller/pull.test.sh`: a temp directory, fakes first on `PATH`, a `check name want got` helper, and a nonzero exit when any case fails.

- [ ] **Step 1: Write `checks/puller-age.test.sh`.** Each case writes a marker in the temp directory and sets `CHECK_NOW` to 1794326400.

| Case | Marker holds | Want exit | Want stdout |
|---|---|---|---|
| fresh | 1794325986 | 0 | `PULLER OK - last pull 414s ago \| age=414s;;1800` |
| one second under the limit | 1794324601 | 0 | starts `PULLER OK - last pull 1799s ago` |
| at the limit | 1794324600 | 2 | `PULLER CRITICAL - last pull 1800s ago, limit 1800s \| age=1800s;;1800` |
| long dead | 1794240000 | 2 | starts `PULLER CRITICAL - last pull 86400s ago` |
| no marker | file absent | 2 | `PULLER CRITICAL - no completed pull recorded` |
| not a number | `soon` | 3 | `PULLER UNKNOWN - marker is not a timestamp` |
| empty | zero bytes | 3 | `PULLER UNKNOWN - marker is not a timestamp` |
| later than now | 1794326500 | 0 | starts `PULLER OK - last pull 0s ago` |
| trailing newline | `1794325986` and a newline | 0 | the fresh line |

- [ ] **Step 2: Run it and see it fail,** then write `checks/puller-age` to the table. The 1800 second default is three missed passes at the 10 minute pull interval, the limit the puller's compose healthcheck uses, and that reason is the one comment the script needs.

Run: `checks/puller-age.test.sh`
Expected first: FAIL, `checks/puller-age: No such file or directory`. Then: every case `ok`.

- [ ] **Step 3: Write `checks/stack-health.test.sh`** with a fake `docker` first on `PATH` that prints the lines a case names, one container per line as `<service><TAB><state><TAB><status>`, and records its arguments.

| Case | Fake prints | Want exit | Want stdout |
|---|---|---|---|
| all healthy | `db`, `almanac`, `puller`, `backup`, each `running` and `Up 3 hours (healthy)` | 0 | `STACK OK - 4 services healthy` |
| one unhealthy | `puller` is `running`, `Up 3 hours (unhealthy)` | 2 | `STACK CRITICAL - puller unhealthy` |
| one exited | `backup` is `exited`, `Exited (1) 2 hours ago` | 2 | `STACK CRITICAL - backup exited` |
| one missing | no `db` line | 2 | `STACK CRITICAL - db missing` |
| two bad | `puller` unhealthy and `backup` exited | 2 | `STACK CRITICAL - puller unhealthy, backup exited` |
| still starting | `almanac` is `running`, `Up 5 seconds (health: starting)` | 0 | `STACK OK - 4 services healthy` |
| no health reported | `db` is `running`, `Up 3 hours` | 0 | `STACK OK - 4 services healthy` |
| docker fails | exits 1, prints `Cannot connect to the Docker daemon` | 3 | `STACK UNKNOWN - docker not reachable` |
| docker absent | not on `PATH` | 3 | `STACK UNKNOWN - docker not found` |
| the right project | any | any | the fake saw the filter `label=com.docker.compose.project=brined-almanac-prod` |

- [ ] **Step 4: Run it and see it fail,** then write `checks/stack-health`. It runs `docker ps -a` with that label filter and a `--format` that prints the compose service label, the state, and the status, and it reports the bad services in the fixed order `db`, `almanac`, `puller`, `backup`. Then run both checks for real once.

Run: `checks/puller-age` and `checks/stack-health`
Expected: `PULLER OK - last pull ...` and `STACK OK - 4 services healthy`, each exit 0, with the prod stack up.

- [ ] **Step 5: Write `checks/collect.test.sh`** with fake checks in a temp `CHECK_DIR` and `HOST_STATE_NOW` set per run.

| Case | Setup | Want in the report |
|---|---|---|
| each exit code | four fake checks exiting 0, 1, 2, 3 | `status` 0, 1, 2, 3 and each check's line as `output` |
| report shape | one OK check, now `2026-11-10T15:00:00Z` | `host` `mac`, `generated_at` `2026-11-10T15:00:00Z`, `interval_seconds` 300 |
| timeout | a check that sleeps 5, `CHECK_TIMEOUT` 1 | `status` 2, `output` `SLOW CRITICAL - timed out after 1s` |
| exit code out of range | a check exiting 7 | `status` 3 |
| check not executable | a file with no execute bit | `status` 3, `output` starting `NAME UNKNOWN -` |
| `since` held | run at 15:00 critical, again at 15:05 critical | `since` `2026-11-10T15:00:00Z` after both |
| `since` reset | run at 15:00 OK, again at 15:05 critical | `since` `2026-11-10T15:05:00Z` |
| previous report corrupt | `OUT` holds `{"host":` | a valid report, every `since` equal to now, exit 0 |
| previous report empty | `OUT` is a zero-byte file | the same |
| hostile output | a check printing 5000 bytes, then a newline, a backtick, and a tab | `output` is the first line, at most 500 bytes, and `jq .` reads the file |
| atomic write | `OUT`'s directory is read-only | exit nonzero, the old report unchanged, no `.tmp` file left |
| no stray files | any passing run | the directory holds only `mac.json` |

- [ ] **Step 6: Run it and see it fail.**

Run: `checks/collect.test.sh`
Expected: FAIL, `checks/collect: No such file or directory`

- [ ] **Step 7: Write `checks/collect`.** It sources the conf, runs each check under `gtimeout "$CHECK_TIMEOUT"` (falling back to `timeout` when `gtimeout` is absent), and builds the report with `jq -n`, passing every value through `--arg` or `--argjson` so no check output is ever interpolated into a filter. `gtimeout` exits 124 on a timeout, and that is the only path to the timed-out line. It reads the previous `since` values with `jq` and treats any `jq` failure as "no previous report". It writes to `$OUT.tmp.$$` and renames. It exits 0 when the rename succeeds.

- [ ] **Step 8: Write `checks/hosts/mac.conf`** with `HOST=mac`, `OUT="${BRINED_ALMANAC_STATE:-$HOME/.local/share/brined-almanac}/host-state/mac.json"`, `CHECK_DIR=..`, and `CHECKS="puller-age stack-health"`.

- [ ] **Step 9: Run both test scripts and see them pass.** Add them to `[tasks.test]` in `mise.toml` and `checks/*` to the shellcheck step in `hk.pkl`.

Run: `mise run test` and `mise run check`
Expected: both pass.

- [ ] **Step 10: Commit and open the pull request.**

---

### report-and-rule

Pure code: no I/O beyond a function that reads one directory, no workflow, no clock of its own.

**Files:**
- Create: `src/host_state.rs`, `hosts.toml`
- Modify: `src/lib.rs` (add `pub mod host_state;`)
- Test: `tests/host_state.rs`, fixtures under `tests/fixtures/host_state/`

**Interfaces:**
- Produces, all in `brined_almanac::host_state`:

```rust
pub const CRITICAL_AFTER: Duration = Duration::minutes(10);
pub const WARNING_AFTER: Duration = Duration::hours(24);
pub const STALE_AFTER: Duration = Duration::minutes(30);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Status { Ok, Warning, Critical, Unknown }

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CheckResult {
    pub name: String,
    pub status: Status,
    pub since: OffsetDateTime,
    pub output: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Report {
    pub host: String,
    pub generated_at: OffsetDateTime,
    pub interval_seconds: u64,
    pub checks: Vec<CheckResult>,
}
impl Report {
    pub fn parse(text: &str) -> Result<Report, String>;
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Host { pub name: String, pub project: Project }

#[derive(Debug)]
pub struct Hosts(Vec<Host>);
impl Hosts {
    pub fn parse(text: &str) -> Result<Hosts, String>;
    pub fn load() -> Result<Hosts, String>; // include_str!("../hosts.toml")
    pub fn iter(&self) -> impl Iterator<Item = &Host>;
}

/// What the scan found at `<dir>/<host>.json`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Read {
    Report(Report),
    Missing,
    Invalid { modified: OffsetDateTime, error: String },
}
pub fn read(dir: &Path, host: &Host) -> Read;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Finding {
    Check { host: String, check: String, status: Status, since: OffsetDateTime, output: String },
    Stale { host: String, since: Option<OffsetDateTime>, reason: String },
}
impl Finding {
    pub fn key(&self) -> String;
    pub fn title(&self) -> String;
    pub fn body(&self) -> String;
}
pub fn findings(host: &Host, read: &Read, now: OffsetDateTime) -> Vec<Finding>;
```

`Status` is an enum because the rule branches on it in one place. `Read` and `Finding` are enums so that "missing", "invalid", and "stale" cannot be confused with a parsed report downstream.

- [ ] **Step 1: Write the parser tests in `tests/host_state.rs`.**

| Test | Input | Asserts |
|---|---|---|
| `parses_the_spec_example` | the JSON block from the spec | `host == "mac"`, one check, `status == Status::Critical`, `since == 2026-11-10 14:50 UTC` |
| `rejects_a_status_outside_0_to_3` | `status: 4` | `Err` naming `status` |
| `rejects_a_check_name_outside_the_alphabet` | `name: "GH Auth"` | `Err` naming the check |
| `rejects_a_duplicate_check_name` | two `gh-auth` entries | `Err` containing `twice` |
| `an_empty_check_list_is_valid` | `checks: []` | `Ok`, zero checks |
| `rejects_unknown_fields` | an extra top-level key | `Err` |
| `rejects_a_time_without_an_offset` | `generated_at: "2026-11-10 15:00"` | `Err` naming `generated_at` |
| `cuts_output_to_500_bytes_on_a_char_boundary` | 600 bytes ending in a multi-byte character | `output.len() <= 500` and it is valid UTF-8 |

- [ ] **Step 2: Run them and see them fail.**

Run: `cargo test --test host_state`
Expected: FAIL to compile, `could not find host_state in brined_almanac`

- [ ] **Step 3: Implement `Status`, `CheckResult`, `Report`, and `Report::parse`** with private `serde` structs marked `deny_unknown_fields`, converting into the public model. Follow `src/tiers.rs` for the error strings.

- [ ] **Step 4: Write the `Hosts` tests.**

| Test | Input | Asserts |
|---|---|---|
| `the_committed_hosts_file_declares_the_mac` | `Hosts::load()` | one host, `name == "mac"`, `project.as_str() == "brined-almanac"` |
| `rejects_a_host_declared_twice` | two `mac` entries | `Err` containing `twice` |
| `rejects_a_project_name_taskwarrior_would_refuse` | `project = "Bad Name"` | `Err` naming the host |
| `rejects_a_host_name_that_is_a_path` | `name = "../x"` | `Err` |

- [ ] **Step 5: Implement `Host`, `Hosts`, and create `hosts.toml`** with the one `[[host]]` entry from the spec. A host name matches `[a-z0-9-]+`, which is what keeps `read` from leaving its directory.

- [ ] **Step 6: Write the rule tests.** Each builds a `Read` and a `now` and asserts on `findings`. `now` is `2026-11-10 15:00 UTC` unless the row says otherwise.

| Test | Read | Asserts |
|---|---|---|
| `a_critical_check_files_after_ten_minutes` | critical since 14:50, generated 14:59 | one `Check`, key `mac/gh-auth/2026-11-10` |
| `a_critical_check_under_ten_minutes_files_nothing` | critical since 14:51 | empty |
| `unknown_files_like_critical` | unknown since 14:50 | one `Check` |
| `a_warning_files_after_a_day` | warning since 2026-11-09 15:00 | one `Check`, key `mac/gh-auth/2026-11-09` |
| `a_warning_under_a_day_files_nothing` | warning since 2026-11-09 15:01 | empty |
| `an_ok_check_files_nothing` | OK | empty |
| `a_long_outage_keeps_one_key` | critical since 2026-11-08 23:00, now 2026-11-10 | key `mac/gh-auth/2026-11-08` |
| `a_stale_report_files_stale_and_no_checks` | generated 14:29 with a critical check | exactly one `Stale`, key `mac/stale/2026-11-10`, `since == Some(14:29)` |
| `a_report_at_thirty_minutes_is_not_stale` | generated 14:30 | no `Stale` |
| `a_missing_report_is_stale` | `Read::Missing` | one `Stale`, key `mac/stale/missing`, `since == None` |
| `an_invalid_report_is_stale_with_the_error` | `Read::Invalid`, modified 14:00, error `x` | one `Stale`, key `mac/stale/2026-11-10`, `body()` contains `x` |
| `a_since_in_the_future_files_nothing` | critical since 15:30 | empty, no panic |
| `a_generated_at_in_the_future_is_fresh` | generated 15:30 | no `Stale` |
| `the_title_names_check_status_host_and_time` | critical since 14:50 | `title() == "host-state: gh-auth critical on mac since 2026-11-10 14:50Z"` |
| `the_stale_title_names_the_host_and_time` | generated 14:29 | `title() == "host-state: no report from mac since 2026-11-10 14:29Z"` |

- [ ] **Step 7: Implement `Finding`, `findings`, `key`, `title`, and `body`.** A negative age is treated as zero. `body()` for a check is three lines: the output, `status: <name>`, and `since: <RFC 3339>`.

- [ ] **Step 8: Write the `read` tests** against a `tempfile` directory.

| Test | Directory | Asserts |
|---|---|---|
| `reads_a_valid_report` | `mac.json` with the spec example | `Read::Report` |
| `a_missing_file_is_missing` | empty | `Read::Missing` |
| `a_truncated_file_is_invalid` | `mac.json` holding `{"host":` | `Read::Invalid` |
| `a_report_naming_another_host_is_invalid` | `mac.json` with `"host": "lab"` | `Read::Invalid`, error names both hosts |

- [ ] **Step 9: Implement `read`,** then run the whole suite.

Run: `cargo test --test host_state` and `mise run check`
Expected: every test passes, clippy and fmt are clean.

- [ ] **Step 10: Commit and open the pull request.**

---

### host-state-workflow

**Files:**
- Create: `src/workflows/host_state.rs`
- Modify: `src/workflows/mod.rs` (register `host_state::HostState`), `src/providers.rs` (provide `Hosts` and `HostReports`), `README.md` (the settings table gains `HOST_STATE_REPORTS`)
- Create: `scripts/host-state-dry-run`
- Modify: `docs/workflows.md` (a `host-state` catalog entry; service-down and credential-expiry point at it; "Host state" gains the report fields and the `since` rule)
- Test: `tests/host_state_workflow.rs`, fixtures under `tests/fixtures/host_state/`

**Interfaces:**
- Consumes: `Hosts`, `Host`, `read`, `findings`, `Finding` from `report-and-rule`, exactly as declared there.
- Produces:

```rust
/// The directory `HOST_STATE_REPORTS` names, or none when it is unset or empty.
pub struct HostReports(pub Option<PathBuf>);

/// Event kind `host.state.finding`. `kind` is `check` or `stale`.
pub struct HostFinding {
    pub kind: Slug,
    pub project: Slug,
    pub subject: Slug,          // the delivery key with `/` replaced by `.`
    pub title: String,
    pub body: Untrusted<String>,
}

pub fn scan_findings(hosts: &Hosts, dir: &Path, now: OffsetDateTime) -> Vec<(String, Value)>;
pub fn facts(event: &HostFinding) -> Facts;
pub fn policy(facts: &Facts) -> Vec<Effect>;
```

If `title!` accepts only clean typed fields, drop `title` from the event, carry `host`, `check`, `status`, and `since` as validated fields instead, and build the same title string in `policy`. The tests assert the title text either way.

Follow `src/workflows/security_alerts.rs` for the `observed!` event, `Event::parse`, the `#[scan]` function, and the `#[workflow(...)]` attribute. The attribute is `on = HostFinding`, `key = "{subject}"`, `may = [File]`, `runs_per_hour = 1000`, with the intent "File one task for each host check that has been failing long enough, and one when a host's report stops arriving."

- [ ] **Step 1: Write the tests in `tests/host_state_workflow.rs`.**

| Test | Setup | Asserts |
|---|---|---|
| `a_critical_report_scans_to_one_event` | fixture dir with `mac.json` critical since 14:50, now 15:00 | one pair, delivery id `mac/gh-auth/2026-11-10`, payload `kind == "check"`, `project == "brined-almanac"` |
| `a_healthy_report_scans_to_nothing` | every check OK, fresh | empty |
| `a_declared_host_with_no_file_scans_to_stale` | empty dir | one pair, delivery id `mac/stale/missing` |
| `an_undeclared_report_files_nothing` | dir also holds `lab.json` critical | still only the mac's events |
| `the_event_parses_back` | the payload from the first test | `HostFinding::parse` succeeds and `title` matches |
| `a_payload_with_another_kind_is_malformed` | `kind: "other"` | `Malformed` with pointer `/kind` |
| `a_payload_with_a_bad_project_is_malformed` | `project: "Bad Name"` | `Malformed` with pointer `/project` |
| `policy_files_one_task_to_the_project` | facts from the first event | exactly one `Effect::File`, target `task:brined-almanac`, the title from `Finding::title` |
| `the_body_stays_untrusted` | an output holding a backtick and `@someone` | the `File` body is the `Untrusted` value, unchanged |
| `the_reports_dir_is_none_when_unset_or_empty` | lookup returning `None`, then `Some("")` | `HostReports(None)` both times |

- [ ] **Step 2: Run them and see them fail.**

Run: `cargo test --test host_state_workflow`
Expected: FAIL to compile, `could not find host_state in workflows`

- [ ] **Step 3: Implement `HostReports` and `host_reports_from(lookup) -> HostReports` in `src/providers.rs`,** and provide `Hosts` and `HostReports` in `providers::all`. A `hosts.toml` that fails to parse panics at startup, as tiers do.

- [ ] **Step 4: Implement `scan_findings`, the event, `facts`, `policy`, the `#[scan]` function, and the workflow.** The scan function logs `host-state: HOST_STATE_REPORTS is not set, skipping` and returns nothing when the directory is `None`. It logs one line per file in the directory that no declared host owns. It takes `now` from `OffsetDateTime::now_utc()` and passes it down.

- [ ] **Step 5: Register the workflow** in `workflows::all()` and run the tests.

Run: `cargo test` and `mise run check`
Expected: every test passes.

- [ ] **Step 6: Write `scripts/host-state-dry-run`,** following `scripts/bugsink-dry-run`: it takes a reports directory, builds the binary, and runs `backfill --dry-run` with `HOST_STATE_REPORTS` exported inside the script.

- [ ] **Step 7: Prove the dry run** against a fixture directory holding one critical report whose `since` is over ten minutes old.

Run: `scripts/host-state-dry-run tests/fixtures/host_state/critical`
Expected: the output names one task that would be filed to `task:brined-almanac`, titled `host-state: gh-auth critical on mac since ...`, and the run needed no database and no token. Run it a second time against a directory with a healthy report and confirm it would file nothing.

- [ ] **Step 8: Update `docs/workflows.md` and `README.md`** as the Files list says.

- [ ] **Step 9: Commit and open the pull request.**

---

### prod-wiring

**Files:**
- Create: `checks/launchd/com.technicalpickles.brined-almanac.host-state.plist.template`
- Create: `scripts/install-host-state`, `scripts/install-host-state.test.sh`
- Modify: `scripts/deploy` (create `$STATE/host-state`, call `scripts/install-host-state`), `compose.prod.yaml` (mount and variable), `scripts/compose-config-check` if it lists expected mounts
- Modify: `docs/runbooks/prod-on-the-mac.md`, `docs/roadmap.md`, `docs/superpowers/specs/2026-11-10-host-state-design.md` (the wake measurement)

**Interfaces:**
- Consumes: `checks/collect <conf>` and `checks/hosts/mac.conf` from `checks-and-collector`, and `HOST_STATE_REPORTS` from `host-state-workflow`.
- Produces: `scripts/install-host-state [--print]`. With `--print` it writes the rendered plist to stdout and changes nothing.

The plist runs `<checkout>/checks/collect <checkout>/checks/hosts/mac.conf` with `StartInterval` 300, `RunAtLoad` true, `PATH` set to `/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin`, and stdout and stderr appended to `$STATE/host-state/collect.log`. `<checkout>` is the main checkout's real path, resolved when the script runs, so the agent follows `main` as deploys do.

- [ ] **Step 1: Write `scripts/install-host-state.test.sh`.** It runs `scripts/install-host-state --print` and asserts with `plutil -lint` that the output is a valid plist, that `Label` is `com.technicalpickles.brined-almanac.host-state`, that `StartInterval` is 300, and that both program arguments are absolute paths that exist.

- [ ] **Step 2: Run it and see it fail,** then write the template and the script. Without `--print` the script writes the plist to `~/Library/LaunchAgents/`, runs `launchctl bootout` for the label if it is loaded, then `launchctl bootstrap gui/$(id -u)`, then runs the collector once. Running it twice leaves one loaded agent.

- [ ] **Step 3: Wire compose.** In `compose.prod.yaml`, the `almanac` service gains the volume `"${BRINED_ALMANAC_STATE:-${HOME}/.local/share/brined-almanac}/host-state:/host-state:ro"` and the environment line `HOST_STATE_REPORTS: /host-state`.

Run: `scripts/compose-config-check`
Expected: pass.

- [ ] **Step 4: Wire deploy.** `scripts/deploy` creates `$STATE/host-state` before `compose up` and calls `scripts/install-host-state` after the stack is healthy. `--db-only` skips both.

- [ ] **Step 5: Write the runbook section** "Host state": where the report and the log are, how to run a check and the collector by hand, how to read `launchctl print` for the agent, and how to unload it.

- [ ] **Step 6: Commit, open the pull request, and stop for Josh's go-ahead to deploy.**

- [ ] **Step 7: After `mise run deploy`, verify on the real stack.**

| Check | Command | Expected |
|---|---|---|
| The agent is loaded | `launchctl print gui/$(id -u)/com.technicalpickles.brined-almanac.host-state` | state and last exit code 0 |
| Docker answers under launchd | `jq '.checks[] \| select(.name=="stack-health")' "$STATE/host-state/mac.json"` | status 0, `STACK OK - 4 services healthy`. A status 3 means `docker` fails under launchd: unload the agent and stop |
| The report is fresh | `jq . "$STATE/host-state/mac.json"` | `puller-age` status 0, `generated_at` within 300 seconds |
| The container sees it | `mise run prod -- backfill --dry-run` | a `host-state` scan that files nothing |
| A failure files once | With Josh's go-ahead, stop the puller container, wait 45 minutes, then scan twice | exactly two tasks in `brined-almanac`: `host-state: puller-age critical on mac since ...` and `host-state: stack-health critical on mac since ...`. The second scan files nothing |
| Recovery resets `since` | start the puller, wait for one pull and one collector run | both checks status 0 with a new `since` |

Close the two test tasks by hand afterwards and say so in the pull request.

- [ ] **Step 8: Measure the wake.** With Josh, sleep the Mac for at least 40 minutes and wake it. Record, from `collect.log` and the almanac container's log, the time of the first collector run and the first `host-state` scan after wake.

| Result | What changes |
|---|---|
| The collector runs before the first scan | Nothing. Record the two times in the spec. |
| A scan sees a stale report first | A follow-up pull request changes the stale rule so a report is stale only when it is also older than the container's time since wake, with a test for it. Close any false task and cite it. |

- [ ] **Step 9: Record the result** in the spec's "What was measured", mark the M1 credential-expiry bullet in `docs/roadmap.md` as shipped with the date, and commit.
