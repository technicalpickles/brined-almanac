# Go port, showing parity: plan

Goal: be able to say "the Go port does what Rust does" with evidence, or know exactly where it does not. Rust `main` is the spec and stays in service ([ADR 0001](adrs/0001-port-to-go-alongside-rust.md)). Nothing here is a cutover.

Start point: `go-port` at `c85ba45`, where `compare` showed 18 of 22 cases disagreeing (14 on the workflow name alone, 4 parse gaps) and compared no bodies.

Status at `go-port` `5c30769`:

- `compare` compares filed bodies and tracker keys (`body-parity`, [#57](https://github.com/technicalpickles/brined-almanac/pull/57)). On every case where both sides file, key and body agree.
- Every disagreement is listed in `docs/divergences.md`, and a test fails on an unlisted one (`divergences`, [#56](https://github.com/technicalpickles/brined-almanac/pull/56), hardened in [#58](https://github.com/technicalpickles/brined-almanac/pull/58)).
- The 4 parse gaps are closed. brined's parsers refuse at parse what Rust's refuse ([#59](https://github.com/technicalpickles/brined-almanac/pull/59)).
- `compare` is 0 of 22. The 14 workflow-name disagreements are accepted and normalised: `acceptedWorkflowRenames` maps exactly the three Rust names to Go's underscore names ([#60](https://github.com/technicalpickles/brined-almanac/pull/60)). `docs/divergences.md` has no open entry.
- Scans, write paths, the store and live traffic are not compared yet.

## What "parity" means here

Parity is a contract, not a feeling. It is done when all of these hold:

1. `compare` shows zero unexplained differences, including bodies. Every accepted divergence is written down in `docs/divergences.md`, each with a reason and an owner.
2. A shadow run on live inputs, read-only, matches for 7 days with no unexplained difference.
3. Every write path has been exercised once against a sandbox.
4. The store behaviours that decide whether work repeats or goes missing (keys, dedupe, budgets, restart) are compared, or the difference is recorded under item 1.

## Lanes

Each lane gets its own worktree and branch off `go-port` (`go-port-<lane>`), one fresh `pstack:poteto-agent` each, a draft PR against `go-port`, and an independent review before it counts. The rules and gates from [go-port-next-plan.md](go-port-next-plan.md) apply unchanged: lanes do not edit the shared docs (they write `.scratch/lane-<slug>-report.md` and the coordinator folds them in), and no agent merges, posts to another repo, or touches prod.

| Lane slug | What it proves | Owns | Starts with | State |
|---|---|---|---|---|
| `body-parity` | Filed issue bodies match | `compare/` | A spike (below) | Done, [#57](https://github.com/technicalpickles/brined-almanac/pull/57) |
| `scan-parity` | The scans that produce the events match | new `compare/scan*`, fixtures | A spike (below) | Not started |
| `shadow-run` | Go and Rust agree on live inputs | new `shadow/`, a mise task | Nothing, runs after `body-parity` | Not started. Unblocked, since `body-parity` landed |
| `write-sandbox` | The write paths do what Rust's do | new `sandbox/`, a runbook | A sandbox target | Not started. Waits on Josh's throwaway repo |
| `store-parity` | Keys, dedupe, budgets and restarts line up | tests, new `compare/store*` | The outbox-key pin | Partly answered by the cutover investigation (below). Budgets and restart not compared |
| `divergences` | Every accepted gap is written down | new `docs/divergences.md` | The 4 parse gaps and the name | Done, [#56](https://github.com/technicalpickles/brined-almanac/pull/56), tests hardened in [#58](https://github.com/technicalpickles/brined-almanac/pull/58). The parse gaps were then fixed in [#59](https://github.com/technicalpickles/brined-almanac/pull/59) |
| `upstream-prs` | The gaps that only upstream can close get closed | patches prepared in a scratch clone of steading-ai/almanac | The boundary list and workflow names | Workflow names done as [steading-ai/almanac#36](https://github.com/steading-ai/almanac/pull/36), open and not carried. Boundary list not started |

### body-parity

`compare` cannot see bodies because Rust's `simulate` prints them as `Untrusted(..)`. But Rust's `LogTracker` prints the body it would file, and Go's does the same (`FILE key=... target=... title=...` and then the body). That path is reached by a live run with no tokens and no `DATABASE_URL`.

1. Spike, no code: confirm both binaries print identical `LogTracker` output for one recorded event, with the key's run id normalised. Report what differs.
2. If it works, extend `compare` to run both in that mode and diff the body, so bodies join title and target. Keep hostile text out of the report: print a diff of line numbers and a short hash, not the body.
3. If it does not, fall back to a Rust-side body dump as a test-only example. That adds a file to the Rust tree, so stop and ask Josh first.

Done means: `ingest-dated` and `empty-surfaces` stop being "body-only" fixtures and start testing something.

Result ([#57](https://github.com/technicalpickles/brined-almanac/pull/57)). The `LogTracker` route worked, so no Rust-side body dump was needed. `compare` runs both binaries live with no credentials and compares filing count, then key and body per filing. A body difference is reported as line numbers and a short hash. Over the 14 cases where both sides file, key and body agree, and the golden file did not change. A mutation in the ingest-time suffix or the empty-surfaces line makes `ingest-dated` or `empty-surfaces` disagree, so both fixtures now test something. Two side effects. `compare` no longer exercises Go's `ALMANAC_READ_ONLY` refusal, which has no Rust counterpart and stays covered by `wiring` tests. The FILE line's title is not compared, because Rust quotes it with `{:?}` and Go with `%q`, and the parsed title is already compared through the effects.

### scan-parity

Events are only half of it. The scans (`bugsink_issues`, `security_alerts`, `host_state`) read Bugsink, GitHub and host reports, and are not compared at all.

1. Spike: which inputs can both sides point at a fake? Bugsink is reachable by DSN. `securityalerts` reads GitHub through a constant `https://api.github.com` in Rust (`providers.rs`), so a fake GitHub needs a Rust change or a proxy. Report which scans are fakeable and how.
2. For the fakeable ones, run both sides' `backfill` against one shared fake and diff what each would file.
3. For the rest, say so in the report and leave them to `shadow-run`.

### shadow-run

The strongest evidence: Go runs beside prod, read-only, on the same live inputs, and the would-file lists are diffed.

1. Build a `shadow` command that runs both binaries in a no-token, read-only mode on the same inputs and diffs title, target and body (hashed). ADR 0002 already gives Go its own port (8790) and database, and `ALMANAC_READ_ONLY=1` is the default.
2. Run it on a schedule for 7 days. Output goes to a local file, never to a tracker.
3. Each day, an agent classifies new differences as noise, a known divergence, or new, and files the new ones as issues in this repo.

Never points at the prod database, prod compose project, or port 8788. Go uses `brined_almanac_go`.

### write-sandbox

The shipper, `cutover`, the taskwarrior tracker and the GitHub tracker are only tested against fakes.

1. Make a throwaway private GitHub repo and a throwaway taskchampion server (a local-dir server for the tests already exists). Write down how to recreate them.
2. For each write path, run Rust and Go once with the same input and compare what landed. Record the result per path.
3. Tear the sandbox down.

Needs Josh to create the throwaway repo, since creating one is not something an agent does on its own.

### store-parity

1. Keys: [#34](https://github.com/steading-ai/almanac/issues/34) repeats outbox keys after a reset or restore, and the Go side shares it (`BRINED_PIN_OUTBOX_KEY=1`). Track the upstream fix, and flip the gated test to run by default when it lands.
2. Budgets: the Go models declare `PerHour(1000)` like Rust. Confirm the budget trips at the same count and recovers the same way, restart included.
3. Names: answered. The workflow name is part of the key in `runs` (`one_active_run_per_key` is on `(almanac, workflow, key)`), `pending` (primary key `(almanac, workflow, key)`) and `tripped` (primary key `(almanac, workflow)`), and `ledger` has a `workflow` column. `seen` has no workflow column. No tracker dedupes on the name. A cutover onto a fresh Go store copies only `seen` and moves `run_seq` with `setval`, so no name crosses and underscores cost nothing at cutover (read from code). `compare` treats the three names as equal ([#60](https://github.com/technicalpickles/brined-almanac/pull/60)). See "Cutover onto a fresh Go store" below.

Still open in this lane: item 1 beyond cutover (a restore from a dump still reissues keys) and item 2.

### upstream-prs

Josh has write access to steading-ai/almanac, so the limits that sit in the library are patches, not requests. Candidates, in the order they unblock the most:

1. **Hyphenated workflow names.** The checker accepts `^[a-z][a-z0-9_]*$` (`check/check.go:88`). Allowing `-` would clear the 14 workflow-name disagreements (of 18 at the time). Check first whether anything keys on the name in a way that makes `-` unsafe (store scopes, metric or file names). Done. The hazard audit found nothing unsafe, provided fact names keep the old rule, and with the patch `compare` went from 18 to 4 of 22 (measured on a throwaway branch). Filed as [steading-ai/almanac#36](https://github.com/steading-ai/almanac/pull/36). Josh chose not to carry it. Go keeps underscores, and [#60](https://github.com/technicalpickles/brined-almanac/pull/60) normalises the names in `compare`, so `compare` is 0 of 22.
2. **An extensible `hostilevet` boundary list.** The list in `tools/hostilevet/taint/boundary.go` is hardcoded to `patch` and `github`. A flag or a module-owned list would clear all 4 findings measured on 2026-11-12 (`taskwarrior/taskwarrior.go`, `bugsink/bugsink.go`, `securityalerts/plain.go`, `wiring/trackers.go`), each "only the boundary packages tools/hostilevet lists may import hostile/reveal".
3. **`github.Retrying` reaching a second call, a default config path, and a read-only default.** Smaller, and each is a field or an option.
4. **Unique outbox keys** ([#34](https://github.com/steading-ai/almanac/issues/34)). This one is the maintainer's design, so offer a patch only if she asks for one.

The lane works in a clone of steading-ai/almanac (or a fork), one branch per patch, based on `go-rewrite` since that is the code brined imports. Each patch is proved by running it: brined points at the clone through a `replace` on a throwaway branch, and the patch has to clear `compare`, `hostilevet` and the tests, with the before and after output kept. That evidence goes in the pull request body. The lane also writes the tests upstream's own suite requires and the body in Josh's voice, then stops. Carrying a proven patch in brined while its pull request is open is fine, and is recorded in `docs/go-touch-points.md`. Pushing a branch to steading-ai/almanac or opening the pull request is Josh's call, one patch at a time, and the agent does neither. When one is merged, brined moves its pin and drops the `replace`.

### divergences

Settle the 4 parse gaps and the name mismatch. For each, one of: fix on our side, send a pull request upstream (Josh has write access to steading-ai/almanac, and issue [#35](https://github.com/steading-ai/almanac/issues/35) already raises the name and boundary asks), or accept. The result is `docs/divergences.md`, which `compare`'s golden file is checked against, so an unlisted difference fails.

Result. `docs/divergences.md` and its test landed in [#56](https://github.com/technicalpickles/brined-almanac/pull/56), and [#58](https://github.com/technicalpickles/brined-almanac/pull/58) hardened the test. The 4 parse gaps were fixed on our side rather than accepted ([#59](https://github.com/technicalpickles/brined-almanac/pull/59)), so their rows left the table. The workflow name was the only remaining entry, and [#60](https://github.com/technicalpickles/brined-almanac/pull/60) normalised it inside `compare`, so no entry is open.

## Cutover onto a fresh Go store

Nothing here schedules a cutover. This section records what a cutover onto a fresh Go store needs, so that it files no duplicates and drops nothing. Josh chose a fresh store, on the condition that duplicates are avoided. It comes from a read-only investigation at `go-port` `094ffc1`. Each claim carries its label: read from code, inferred, or needs prod.

### Why an empty store is not enough

- **Trackers dedupe on the outbox key only** (read from code). Both runtimes hand the tracker the outbox key `{run_id}:{i}`, and `run_id` is `run-` plus `nextval('run_seq')` on both sides. The GitHub tracker searches for that key in issue bodies, and both taskwarrior trackers look for a task whose `almanac_key` equals it. The workflow name is in no tracker identity.
- **An empty `seen` re-files current findings** (read from code). Scans re-yield every current finding each tick with a stable delivery id, and `seen` turns repeats into duplicates. With `seen` empty, every current finding runs again and files under a new key no tracker knows. That covers failing host checks, the overdue security-alert set per repository, and open Bugsink issues whose almanac issue is still open.
- **A restarted `run_seq` drops new findings silently** (inferred). Go's `run_seq` starts at 1, so Go reissues `run-1:0` and later keys. When Rust's run of the same number filed at the same index into the same tracker, the tracker reports success and Go marks the effect delivered. This is [steading-ai/almanac#34](https://github.com/steading-ai/almanac/issues/34), triggered by cutover. The prod runbook already warns about it for Rust's own rollback ([runbooks/prod-on-the-mac.md](runbooks/prod-on-the-mac.md), "Rollback").

### Procedure

`.scratch/cutover-proof.sh` (not committed, since `.scratch` is ignored) runs every SQL statement and pipe below against two throwaway Postgres 17 containers. Rust's schema comes from the pinned crate's migrations, and Go's from the Go binary's own `check`. Its checks are named after the step slugs below. It never touches prod, and it runs neither side's backfill, which needs prod's tokens, so it stands in for the two dry runs with hand-inserted rows.

Rust stays live through steps 1 and 2, so a failed gate leaves prod running. The shell helpers name the containers. Rust's prod database is not published to the host ([ADR 0002](adrs/0002-own-ports-and-database-for-the-go-port.md)), so every command goes through `docker exec`. `brined-almanac-prod-db-1` is the container the runbook uses. `brined-almanac-go-prod-db-1` is inferred from ADR 0002's project name `brined-almanac-go-prod`, and no such stack exists yet, so use whatever container Go's prod database ends up in.

```sh
rustdb() { docker exec -i brined-almanac-prod-db-1 psql -X -q -v ON_ERROR_STOP=1 -U almanac almanac "$@"; }
godb() { docker exec -i brined-almanac-go-prod-db-1 psql -X -q -v ON_ERROR_STOP=1 -U brined_almanac_go brined_almanac_go "$@"; }
# copy_seen FROM TO: Rust's live seen rows into a Go store. The same command is the first run and every rerun.
copy_seen() {
  (set -o pipefail
   "$1" -c "\copy (SELECT almanac, source, delivery_id, seen_at FROM seen WHERE almanac = 'brined-almanac') TO STDOUT CSV" |
     "$2" -c "CREATE TEMP TABLE seen_in (LIKE seen)" \
          -c "\copy seen_in FROM STDIN CSV" \
          -c "INSERT INTO seen SELECT * FROM seen_in ON CONFLICT DO NOTHING")
}
```

1. **Create Go's schema** (`go-schema`). Go has no migrate verb. A verb that opens the store with `DATABASE_URL` set runs goose first (`open` in `app/verbs.go:142` of the pinned `ivy/almanac`), and `check` is the one that does nothing else. Run `brined-almanac check` with `DATABASE_URL` pointed at the Go store and `ALMANAC_CONFIG=almanac.go.toml`. It prints `brined-almanac: ok, 3 workflow(s), read-only dry-run, ...`, creates the tables, and leaves `run_seq` at `last_value = 1`, `is_called = false` (measured). Step 4's `SELECT last_value FROM run_seq` needs this table. Until Go has an image, a Linux build mounted into any container on the database's network works, which is what the proof does:

   ```sh
   GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o .scratch/brined-almanac ./cmd/brined-almanac
   docker run --rm --network <go db network> --entrypoint /w/.scratch/brined-almanac -v "$PWD:/w:ro" \
     -e DATABASE_URL=postgres://brined_almanac_go@<go db host>:5432/brined_almanac_go \
     -e ALMANAC_CONFIG=/w/almanac.go.toml postgres:17-alpine check
   ```

2. **Gate on a dry-run backfill of both sides, on copies** (`dry-run-gate`). Make the Rust copy as in the runbook's "Preview a backfill on a copy" (`ba-preview-db`, a `pg_dump` of prod). Make the Go copy on the same network, give it Go's schema as in step 1, and seed its live `seen` from the Rust copy, not from prod, so both copies start from the same rows:

   ```sh
   docker run -d --name ba-preview-go-db --network ba-preview -e POSTGRES_USER=brined_almanac_go -e POSTGRES_DB=brined_almanac_go -e POSTGRES_HOST_AUTH_METHOD=trust postgres:17-alpine
   until docker exec ba-preview-go-db pg_isready -U brined_almanac_go -q; do sleep 1; done
   # step 1's check, with DATABASE_URL=postgres://brined_almanac_go@ba-preview-go-db:5432/brined_almanac_go
   rustcopy() { docker exec -i ba-preview-db psql -X -q -v ON_ERROR_STOP=1 -U almanac almanac "$@"; }
   gocopy() { docker exec -i ba-preview-go-db psql -X -q -v ON_ERROR_STOP=1 -U brined_almanac_go brined_almanac_go "$@"; }
   copy_seen rustcopy gocopy
   ```

   Then give both copies the same dry-run scope. A dry run reads and writes `seen` under `brined-almanac.dry-run`, and the Rust copy already holds dry-run rows from earlier previews that the Go copy lacks. This replaces that scope with the live rows on each copy, and running it twice changes nothing:

   ```sh
   for db in rustcopy gocopy; do
     "$db" <<'SQL'
   BEGIN;
   DELETE FROM seen WHERE almanac = 'brined-almanac.dry-run';
   INSERT INTO seen (almanac, source, delivery_id, seen_at)
   SELECT 'brined-almanac.dry-run', source, delivery_id, seen_at FROM seen WHERE almanac = 'brined-almanac';
   COMMIT;
   SQL
   done
   ```

   Run `backfill --dry-run` against each copy with the runbook preview's tokens and mounts, Rust's image against `ba-preview-db` and the Go binary against `ba-preview-go-db`. Each prints `found`, `queued` and `unchanged`. `queued` counts every finding whose delivery id was not in `seen` (`runtime.rs:740-756` of the pinned crate), so the two `queued` must be equal. Then list what each side queued and compare. The diff must be empty:

   ```sh
   q="SELECT source, delivery_id FROM seen WHERE almanac = 'brined-almanac.dry-run' EXCEPT SELECT source, delivery_id FROM seen WHERE almanac = 'brined-almanac' ORDER BY 1, 2"
   diff <(rustcopy -At -c "$q") <(gocopy -At -c "$q") && echo "same ids queued"
   ```

   A line in the diff is a delivery id one side derives and the other does not. Do not go on until it is empty. The two lists need not be empty themselves. Prod's daemon keeps an outage's first `since` in memory (`Episodes`, `src/host_state.rs:454-476`), and a fresh backfill process has no episodes, so a host that is stale right now gets a new delivery id on both sides (read from code). Remove the copies with the runbook's cleanup and `docker rm -f ba-preview-go-db`.

3. **Stop Rust and drain it** (`drain`). Stop it with the `stop almanac` command from the runbook's "First deploy" step 4. On Rust's database, all three counts must be 0:

   ```sh
   rustdb -At -c "SELECT count(*) FROM pending WHERE almanac = 'brined-almanac'"
   rustdb -At -c "SELECT count(*) FROM outbox WHERE almanac = 'brined-almanac' AND status = 'pending'"
   rustdb -At -c "SELECT count(*) FROM runs WHERE almanac = 'brined-almanac' AND status = 'running'"
   ```

   Each one is work Rust has admitted and not finished. A clean stop finishes the run in flight and starts no new one (`runtime.rs:250-251`, `stop_claiming` at `runtime.rs:781-784`), so `pending` and pending `outbox` rows can be left after a clean stop. A `running` run means Rust died mid-run, for example when `docker compose stop` gave up after its 10-second default (`compose.prod.yaml` sets no `stop_grace_period`). That run's `seen` row is written and its `pending` row is gone, so the first two counts read 0. Rust's startup requeues it (`recover` at `app.rs:196`, `recover_tx` at `store/postgres.rs:321-350`). After cutover Rust never starts again, and the copied `seen` row makes Go treat the redelivery as a duplicate, so the finding is dropped silently. The proof's `drain` checks show the first two counts at 0 on such a run and the third at 1 (measured).

   If any count is not 0, start Rust with step 6's `start almanac` command from the runbook's "First deploy". Its startup requeues any running run, and its workers claim the pending rows and deliver the outbox. Watch the three counts while it runs. When all three read 0, stop it again and recheck. Repeat until they read 0 after a stop.

   Never run Rust and Go live at once. Each would file what the other filed. A `failed` outbox row usually filed nothing, and it stays as it was under Rust. A taskwarrior delivery is the exception. Rust commits the task to its local replica before the final sync (`Worker::deliver`, `src/taskwarrior.rs:264-284`), so a failed sync can leave the task in the replica in Rust's `taskchampion` volume, and it reaches the server only if that replica syncs again (read from code).

4. **Move Go's `run_seq` past Rust's** (`run-seq`), before Go's first live run:

   ```sh
   L=$(rustdb -At -c "SELECT last_value FROM run_seq")
   godb -At -c "SELECT setval('run_seq', GREATEST($L + 1000, (SELECT last_value FROM run_seq)))"
   ```

   `GREATEST` makes a rerun safe and never moves Go's sequence back. The proof sets Rust to 41 and gets 1041 on Go twice, then 1042 from Go's next `nextval`, and leaves a Go sequence already at 5000 at 5000. With `GREATEST` removed, that last check fails (measured). This avoids the #34 drop at cutover. It does not cover a later restore from a dump, which still needs #34.

5. **Copy Rust's `seen` rows into the Go store** (`copy-seen`). The two `seen` tables match column for column (`almanac`, `source`, `delivery_id`, `seen_at`), with no workflow column (read from both migrations). One command does the first copy and every rerun, through a pipe, so no file is left on either container:

   ```sh
   copy_seen rustdb godb
   q="SELECT source, delivery_id, seen_at FROM seen WHERE almanac = 'brined-almanac' ORDER BY 1, 2"
   diff <(rustdb -At -c "$q") <(godb -At -c "$q") && echo "seen copied"
   ```

   Run the check before Go starts, since Go adds rows from then on. In the proof, an id with commas, double quotes and a backslash, and one with a newline, arrive byte-identical. A rerun adds nothing, and a row added to Rust between runs arrives on the next one. Without `ON CONFLICT DO NOTHING` the rerun fails on `seen_pkey` (all measured). No `runs`, `outbox`, `ledger`, `pending` or `tripped` row crosses over, so this is still a fresh store, and no workflow name crosses.

6. **Keep the Bugsink cursor.** `BUGSINK_CURSOR` is a file, not the store. Mount the same volume or copy the file. A missing cursor resends history. Bugsink drops a repeated event id, and both sides derive it the same way, so this wastes work but should not duplicate, as long as Bugsink still keeps the old events (inferred).

7. **Start Go live.** Rust stays stopped.

### Read-only queries Josh runs for the sizes

These need prod data. They size the risk and do not change anything. The gate in step 2 gives the exact number. The SQL is meant for a copy of prod's database.

```sh
# Open almanac tasks per project: what an empty store would duplicate for host_state and security_alerts
task +almanac status:pending export | jq -r '.[].project' | sort | uniq -c
# Open almanac issues per repo: what an empty store would duplicate for bugsink_issues
gh issue list -R technicalpickles/brineworks -l almanac --state open --limit 500 --json number | jq length
gh issue list -R technicalpickles/pickleclaw -l almanac --state open --limit 500 --json number | jq length
```

```sql
-- How far Go's sequence has to move, and how many keys Rust handed out
SELECT last_value FROM run_seq;
SELECT r.workflow, count(*) AS applied, max(r.seq) AS last_seq
FROM runs r JOIN outbox o ON o.run_id = r.id
WHERE r.almanac = 'brined-almanac' AND o.status = 'applied'
GROUP BY r.workflow;
```

### Follow-ups, not on the cutover path

- `bugsink_issues` could skip a Bugsink issue whose newest linked almanac issue is open for the current generation. That makes it dedupe on the finding in the issue body, independent of any store, and covers a lost or restored database. About 10 lines on each side (inferred size).
- The taskwarrior tracker could also skip a pending task with the same target and description. That changes the tracker's semantics, so it is Josh's call.

## Decisions for Josh

- **Is the Rust-side body dump allowed?** No longer needed. The `LogTracker` route worked ([#57](https://github.com/technicalpickles/brined-almanac/pull/57)).
- **Throwaway repo for `write-sandbox`.** Yours to create.
- **Which differences to accept.** Settled. The 4 parse gaps were fixed, not accepted ([#59](https://github.com/technicalpickles/brined-almanac/pull/59)). The workflow name stays underscored, and `compare` treats the three Rust names as the Go ones ([#60](https://github.com/technicalpickles/brined-almanac/pull/60)).
- **Whether a shadow run should be a real deploy.** It starts as a local process beside prod. Anything that touches the prod stack is yours.
- **Cutover store.** Decided: a fresh Go store, seeded as in "Cutover onto a fresh Go store". Still yours: running the read-only queries above, and when to cut over.

## Order

1. `body-parity` and `scan-parity` spikes, and `divergences`, in parallel. They own different files. `body-parity` and `divergences` are done. `scan-parity` is not started.
2. `store-parity` in parallel with those. The name and cutover-key questions are answered above. Budgets and restart remain.
3. `write-sandbox` once the sandbox exists.
4. `shadow-run` after `body-parity` lands, since its diff needs bodies. It then runs for 7 days. Unblocked now.
5. Coordinator folds the reports into `go-touch-points.md` and `porting-plan.md`, and writes the final parity statement against the contract above. The reports for `body-parity`, `divergences`, the name patch and the parse fixes are folded in.
