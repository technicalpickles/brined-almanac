# Implicit state machines in brined-almanac, 2026-11-09

The app half of a two-part audit. [The framework half](2026-11-09-state-machines-almanac.md) explains the method, the verified / traced / suspected labels, and the patterns that recur. [The recommendations](2026-11-09-state-machines-recommendations.md) are separate. This covers `main` at `c9e5a63`, which pins almanac `b8bdefa`.

The app is small, about 3,900 lines including tests and scripts, and much of it is careful: `providers::choose` is an explicit `Choice` enum with 16 cases, the puller fetches into a temp file and swaps its cursor atomically, deploy refuses a dirty tree and held events, and the taskwarrior worker answers a panic with its cause. The trouble is where the app meets the framework's dedupe, and where "healthy" is measured by "the process is up" rather than "the work is happening".

## 1. A cluster's life

- **Where:** no stored type. A cluster is a `ClusterKey` (`src/workflows/tool_failures.rs:88-101`) turned into the delivery id `scan:brent/{service}/{tool}/{class}/{day}`. Its state lives in almanac's `seen`, outbox and ledger tables, and then in the tracker: an `<!-- almanac:{key} -->` marker in a GitHub issue body, or the `almanac_key` UDA on a taskwarrior task.
- **States:**
  - unseen;
  - seen and below threshold (`Facts::Quiet`);
  - seen and over threshold, with the effect pending in the outbox;
  - filed;
  - abandoned after five failed deliveries.
  "Recurring" isn't a state, because a new day is a new id. "Resolved" doesn't exist either: `already_filed` searches `state=all`, so a closed issue is never reopened or refiled. That one is deliberate, and the design doc calls an issue "a snapshot of the day the cluster crossed its threshold".
- **The bug (verified): a cluster that crosses its threshold late in the day never files.**
  1. The delivery id carries no count (`tool_failures.rs:95-100`).
  2. Postgres `admit_tx` inserts into `seen` with `ON CONFLICT DO NOTHING` and returns `Ingest::Duplicate` before it touches `pending` (almanac `crates/almanac/src/store/postgres.rs:55-73`). The memory store does the same (`memory.rs:103`).
  3. The threshold is applied in `facts()` (`tool_failures.rs:221-223`), inside the run.
  So the first scan to see a cluster decides it for the whole day. If that first sighting is below threshold, the run is `Quiet`, and every later scan of the same cluster is dropped before a run starts, however much it grows. This affects every class with a threshold above 1: approval-timeout and `other` at 3, unknown-tool-id and arg-validation at 5. Only mcp-timeout and pg-too-many-clients (threshold 1) are immune. A daemon scanning every 15 minutes almost always sees a growing cluster first while it is small, so this is the normal path, not an edge case.
- **Why it isn't a design choice:** the spec's [Dedupe section](../superpowers/specs/2026-11-06-tool-error-scan-design.md) accepts that a cluster which *already filed* keeps its filing-time count. It doesn't say anything about clusters that never filed. Almanac has a tool for this: `Scanned::at_state` appends `~{state}` to the id, and its doc comment says a run that decided to wait "would never be reconsidered" without it.
- **Other gaps:**
  - An effect abandoned after five attempts (an outage, or a `PrivateOnly` refusal) leaves the id seen forever, and nothing tells Josh.
  - `already_filed` lists every `almanac`-labelled issue on each filing.
  - The spec says a threshold change can replay old events, but no replay path is wired up. Quiet events also keep only their first-sighting count, so the "counts under threshold" the run log holds are lower bounds.
- **Tests (`tests/tool_failures.rs`):** threshold boundary, a double scan being all duplicates, a fresh store requeuing, live filing once, a dry run calling no tracker. Missing: growth between two scans, retry exhaustion, the day rolling over.

## 2. Transcripts and the puller

- **Where:** `puller/pull.sh` keeps a per-session cursor (`updatedAt + 1`) in `$TRANSCRIPTS/.cursors.json` and appends each fetch to `<safe-key>.jsonl`. `src/transcript.rs` holds no state: every scan re-reads every file and dedupes error events by event id.
- **Per-session states:**
  - never pulled (cursor 0);
  - pulled up to a cursor;
  - changed on the remote;
  - fetch failed, which discards the temp file and leaves the cursor alone.
- **Done well:** fetch to a temp file first, an atomic `set_cursor` (`jq` to a temp file, then `mv`), a key file written with mode 600, heartbeat sessions filtered out, skipped lines counted and logged, and a fake-ssh test harness with `PULL_ONCE`.
- **Health says yes while the work says no (verified):**
  - A failed session logs `failed <key>`, and the pass carries on.
  - An unreadable `.cursors.json` logs `could not read cursors` and returns 0 for every session (`pull.sh:60-62`).
  - Either way, the pass ends with `date +%s > .last-pull` (`pull.sh:93`), and the compose health check only asks whether `.last-pull` is recent (`compose.prod.yaml:64-70`). A test ("mid-stream last-pull present") pins this behaviour.
  So the puller stays green with every session failing.
- **Suspected:**
  - An event stamped before the cursor, but written after the pull, is never fetched, because there's no overlap window.
  - A key reused with a lower `updatedAt` is never pulled again.
  - `safe_key` can map two keys to one file.
  - `cat tmp >> file` isn't atomic. A partial append followed by a retry fuses two lines and loses an event, and a daemon scan that reads mid-append drops the tail line for that scan.
- **Cost:** re-reading everything is O(total bytes) per scan, 51 MB at first deploy. That was a deliberate trade to avoid offset state, and it still holds up at this size.

## 3. Mode and tracker selection

- **Where:** almanac decides the mode: live only for exactly `ALMANAC_MODE=live` with no `--dry-run` (almanac `app.rs:94-99`, verified). `src/providers.rs` picks each tracker side from the environment:

  | Side | Real tracker if | Otherwise |
  |---|---|---|
  | issues | `GITHUB_TOKEN` is non-empty | `LogTracker`, which prints |
  | tasks | `TASKCHAMPION_SYNC_URL` is set | `LogTracker` |

  A real tracker with no `DATABASE_URL` becomes `StoreRequired`, which refuses every filing. A half-set `TASKCHAMPION_*` panics at startup.
- **Silent dry run in prod (verified gap):** a typo or a case change in `ALMANAC_MODE` runs dry, and nothing catches it:
  - `scripts/deploy` validates `TASKCHAMPION_SYNC_URL` but not the mode;
  - `scripts/compose-config-check` puts `ALMANAC_MODE=live` into its own stub env;
  - no startup line names the effective mode or tracker sides;
  - `/healthz` stays green.
- **Representable but wrong:** live mode, both sides on `LogTracker`, and a store marks findings applied without filing them. The preview-on-a-copy runbook relies on exactly this, which is safe only because the copy is thrown away.
- **Done well:** `choose` takes a lookup closure, errors name the variable and never the value, Debug output redacts tokens, `PrivateOnly` caches only `true`, and `StoreRequired` turns a dangerous config into a loud failure. Each of these is the app working around a framework gap: almanac has one `dyn Tracker` and doesn't check that live mode has a real tracker behind it.

## 4. The taskwarrior worker

- **Where:** a dedicated thread owns the non-`Send` replica and server (`src/taskwarrior.rs`).
- **States:**
  - replica unopened or opened;
  - server unopened or connected;
  - dead, with a cause.
  Each delivery syncs, scans for the `almanac_key`, creates the task, commits, and syncs again.
- **Dead is forever (traced, low risk to verify):** after a panic, the worker records the cause and answers every later request with it (tested). It never restarts, `/healthz` doesn't see it, and each filing burns its five outbox attempts and goes to `failed` with no log line (see the framework doc's outbox section).
- **Suspected:**
  - A task deleted on another client disappears from `all_task_data`, so a crash-retry can file it again.
  - A failed post-commit sync leaves the task local until the next sync.
- Almanac never completes or annotates a task once it has filed it.

## 5. Deploy

- **Steps (`scripts/deploy`):**
  1. Refuse a dirty tree.
  2. Check the state directory (`op-token` at mode 600, a sync URL present).
  3. Refuse when events are pending, if the database is up.
  4. Build both images.
  5. `op run ... up -d --wait`.
  6. Run `sync-check`, `compose ps` and `healthz`.
- **Done well:** every secret guarded by `:?` and tested by `compose-config-check`, the database unpublished, a 127.0.0.1 bind, a 20-minute puller start period, an atomic backup with a 25-hour freshness rule, and a thorough `image-smoke`.
- **Gaps (verified by reading):**
  - `:prod` is tagged at build time (`scripts/deploy:99-100`), before `up` proves anything. There's no rollback verb, only the sha tags and the runbook.
  - A failed `up --wait` leaves a mixed stack.
  - There's no migration gate: the pre-migration dump is a manual runbook step.
  - `ALMANAC_MODE`, `PULL_HOST` and `PULL_USER` aren't validated.
  - The script has no test.

## 6. Startup and the scan tick (minor)

- The daemon scans every 15 minutes, using almanac's `SCAN_INTERVAL`, which `almanac.toml` can only tighten.
- The scan try-lock is per process. That's fine, because `seen` makes a race between two scans harmless.
- The first-deploy log in the runbook shows liveness being used as a manual state check ("stopped before its second scan"). A status view would replace that.

## What belongs to whom

| Machine | Owner | Workaround for a framework gap? |
|---|---|---|
| Cluster id and threshold | app | Yes. The id has no state because there is no update effect. `at_state` exists and isn't used. |
| Dedupe, outbox, retry, give-up | almanac | |
| Puller cursor and health | app | |
| Mode | almanac | |
| Tracker sides, `StoreRequired`, `PrivateOnly` | app | Yes: one `dyn Tracker`, and no live-needs-real-tracker check |
| Taskwarrior worker | app | Partly: non-`Send` server |
| Deploy | app | Yes: the pending-events gate exists because of ivy/almanac#10 |
