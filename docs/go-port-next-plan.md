# Go port, after row 13: parallel plan

Goal: take everything left on the Go port as far as it can go without a human, in parallel, and hand back reviewable branches. Rust `main` stays the spec and stays in service ([ADR 0001](adrs/0001-port-to-go-alongside-rust.md)).

Start point: `go-port` at `605f886` (PR #49). Every lane branches from it.

## Shape

Six lanes, each in its own worktree on its own branch (`go-port-<lane>`), each owned by one fresh `pstack:poteto-agent`. Lanes own disjoint files, so nothing serializes shared state. This session is the coordinator. It briefs, reviews every diff, reruns the checks, and is the only writer of the two shared docs.

| Lane slug | What | Owns (files) | Model |
|---|---|---|---|
| `compare-verb` | `compare` verb that puts the Go report beside the Rust report for the same inputs | new `compare/`, the verb registration in `cmd/brined-almanac/main.go`, fixtures | opus |
| `listeners-and-db` | Find what Rust listens on and stores, then decide own ports and database name per the ADR | new ADR 0002, compose/config files, `wiring/` only if a listener needs a provider | opus |
| `securityalerts-hardening` | Retry and rate-limit wait on the Dependabot switch call, and the `plain.go` `reveal` finding | `securityalerts/` | sonnet |
| `hostilevet-findings` | Fix the findings that can be fixed downstream, classify the rest as upstream asks | `taskwarrior/`, `bugsink/`, `transcript/transcript_test.go`, `wiring/trackers.go` | sonnet |
| `run-cap` | Make Go declare the 1000 runs per hour per workflow that Rust declares, and test it | the three models in `brine/*` | sonnet |
| `outbox-key` | Check whether the Go port has the outbox-key bug filed as steading-ai/almanac#34 and brined-almanac#43, and pin it with a test if so | tests in the affected package, no fix unless the bug is ours | sonnet |

Then one tail step after the lanes land: `ivy-feedback-draft`, which turns the Import friction section plus the `hostilevet-findings` classification into a draft message for Ivy.

Not lanes:

- **Resolve.** It lives only in open upstream PRs. The port carries it when Rust does. Nothing to do.
- **Cutover decision.** Full cutover or exploration is Josh's call. It is the one question that gates whether `listeners-and-db` implementation is worth finishing, so that lane stops at the design and reports.

## Rules for every lane

1. Worktree: `wt switch --create go-port-<lane> --base go-port --yes --no-cd --format=json`, then work in `.path`.
2. Never edit `docs/porting-plan.md` or `docs/go-touch-points.md`. Write findings into `.scratch/lane-<slug>-report.md` and the coordinator folds them into the docs in one commit. These two files are where parallel lanes would otherwise conflict.
3. Rust is the spec. Port only behaviour on Rust `main`. Do not change Rust code.
4. Test behaviour, not implementation. Port or add a test before the change, and mutation-check any rule you add.
5. Error text never carries a secret value.
6. Commit with plain `git commit`, never a signing override. Push with plain `git push`, no `-u`.
7. Stop and report after the same approach fails twice. Do not try more variants.
8. A call only Josh can make: apply the default named in the lane brief, say so in the report, and say what Josh could choose instead.
9. Lane report must carry real output of `go vet ./...`, `go test -count=1 ./...`, `cargo test`, `hostilevet`, and `scripts/check-forbidden`, and name any new `hostilevet` finding.

## Gates the agents may not cross

- No post, comment, issue, or PR on `ivy/almanac` or `steading-ai/almanac`.
- No merge, no push to `go-port` or `main`, no force-push.
- A lane may push its own `go-port-<lane>` branch and open a draft PR against `go-port`.
- No change to the deployed service or to prod config.

## Lane briefs

### compare-verb

The ADR says agreement between the Go report and the Rust report for the same inputs is the evidence a workflow ported. Today the port is checked only against the Rust unit tests.

1. Read-only first: find what each side can emit for the same input (`check`, `backfill`, the scan outputs), and pick the smallest comparable surface. Record the choice and why in the report.
2. Build `compare` to run both on recorded fixtures and diff states and effects. Ivy's Go renovate almanac has the same verb against its Rust one, so read it first for shape.
3. Cover the three workflows. A disagreement is a finding, not something to paper over: list each with the input that shows it.
4. Default if the Rust side cannot be driven non-interactively: compare against Rust test fixtures instead, and say so.

Done means: `brined-almanac compare` runs on committed fixtures, exits non-zero on a disagreement, and the report lists every disagreement it found.

### listeners-and-db

The ADR defers ports and the database name until the first listener lands. `run` fails today without `DATABASE_URL`.

1. Investigation only at first: what does Rust listen on, what does it store, and what does Go's `app.Almanac` need (`run`, `DATABASE_URL`, migrations via goose)?
2. If Rust has no listener, say so, write ADR 0002 recording that, and stop.
3. If it does, write ADR 0002 with proposed ports and a separate database name that cannot collide with prod, plus the compose/config change. Stop before wiring anything that can touch prod.
4. Default: ports and database name chosen so both binaries run at once, nothing shared.

Done means: ADR 0002 as a draft PR, with the evidence for each claim about Rust.

### securityalerts-hardening

1. Give `Client.Status` for the Dependabot switch call a retry with backoff on 5xx and a wait on rate limit, matching what Rust's `get_text` got through `send`. Test with the fake API in `internal/fakeapi`.
2. The `reveal` import in `securityalerts/plain.go`: try to narrow with `hostile.Narrow` or `hostile.Classify`. If it needs `reveal`, leave it and say why.

### hostilevet-findings

Known findings: `taskwarrior/taskwarrior.go:27`, `bugsink/bugsink.go:28`, `securityalerts/plain.go:7` (owned by the other lane, do not touch), `wiring/trackers.go:13`, and two `fmt.Sprintf` in `transcript/transcript_test.go:301,304`.

1. For each, decide: fixable downstream by narrowing, or a real boundary that upstream's list does not know about.
2. Fix the first kind. For the Sprintf pair, the no-leak test must still assert what it asserts today.
3. For the second kind, write the upstream ask (what boundary, which package, why it must reveal) into the report, once, with the call site as evidence.

Done means: every remaining finding has a written reason. Do not suppress the vet pass.

### run-cap

Go reports `runs_per_hour_cap=none` where Rust declares 1000 per workflow.

1. Find where the Rust value is declared and where Go's `DefaultLimits` or a model's own budget would take it.
2. Declare it on each model and prove `check` reports it. Test that exceeding it is refused.
3. If Ivy's library offers no per-model field, say so as import friction and stop. Do not patch around it.

### outbox-key

Read steading-ai/almanac#34 and brined-almanac#43. Decide whether the Go port keys its outbox the same broken way.

1. If the Go side is not affected, say why with the code that shows it, and stop.
2. If it is, add a failing test that pins the bug. Do not fix it, since the upstream fix decides the shape. Report.

## Verification and handback

1. Each lane's report is a claim. The coordinator reads every whole diff and reruns `go vet`, `go test`, `cargo test`, `hostilevet`, and `scripts/check-forbidden` in the lane's worktree before accepting.
2. An independent reviewer, one that did not write the change, gets each diff before a lane counts as merge-ready. Passing tests and CI do not count as the verdict.
3. The coordinator commits the combined doc changes (`porting-plan.md`, `go-touch-points.md`) from all lane reports in one commit on `go-port`, after the lanes are accepted.
4. Josh lands the lane PRs into `go-port`. Order does not matter by construction. If two lanes collide anyway, that is a bug in this plan's ownership table, so fix the table.

## Order of work

1. Start all six lanes at once.
2. As each finishes: review, rerun checks, independent review.
3. When `hostilevet-findings` and `securityalerts-hardening` are accepted, run `ivy-feedback-draft`.
4. Coordinator folds the reports into the docs.
5. Final report to Josh: what landed in each lane, every disagreement `compare` found, every new import friction, and the two calls left for him (post to Ivy, cutover or explore).
