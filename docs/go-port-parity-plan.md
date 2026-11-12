# Go port, showing parity: plan

Goal: be able to say "the Go port does what Rust does" with evidence, or know exactly where it does not. Rust `main` is the spec and stays in service ([ADR 0001](adrs/0001-port-to-go-alongside-rust.md)). Nothing here is a cutover.

Start point: `go-port` at `c85ba45`. Today's evidence: the Rust unit tests are ported, and `compare` replays 22 recorded events through both sides (18 disagree: 14 on the workflow name alone, 4 real parse gaps). Bodies, scans, writes, the store and real traffic are not compared.

## What "parity" means here

Parity is a contract, not a feeling. It is done when all of these hold:

1. `compare` shows zero unexplained differences, including bodies. Every accepted divergence is written down in `docs/divergences.md`, each with a reason and an owner.
2. A shadow run on live inputs, read-only, matches for 7 days with no unexplained difference.
3. Every write path has been exercised once against a sandbox.
4. The store behaviours that decide whether work repeats or goes missing (keys, dedupe, budgets, restart) are compared, or the difference is recorded under item 1.

## Lanes

Each lane gets its own worktree and branch off `go-port` (`go-port-<lane>`), one fresh `pstack:poteto-agent` each, a draft PR against `go-port`, and an independent review before it counts. The rules and gates from [go-port-next-plan.md](go-port-next-plan.md) apply unchanged: lanes do not edit the shared docs (they write `.scratch/lane-<slug>-report.md` and the coordinator folds them in), and no agent merges, posts to another repo, or touches prod.

| Lane slug | What it proves | Owns | Starts with |
|---|---|---|---|
| `body-parity` | Filed issue bodies match | `compare/` | A spike (below) |
| `scan-parity` | The scans that produce the events match | new `compare/scan*`, fixtures | A spike (below) |
| `shadow-run` | Go and Rust agree on live inputs | new `shadow/`, a mise task | Nothing, runs after `body-parity` |
| `write-sandbox` | The write paths do what Rust's do | new `sandbox/`, a runbook | A sandbox target |
| `store-parity` | Keys, dedupe, budgets and restarts line up | tests, new `compare/store*` | The outbox-key pin |
| `divergences` | Every accepted gap is written down | new `docs/divergences.md` | The 4 parse gaps and the name |
| `upstream-prs` | The gaps that only upstream can close get closed | patches prepared in a scratch clone of steading-ai/almanac | The boundary list and workflow names |

### body-parity

`compare` cannot see bodies because Rust's `simulate` prints them as `Untrusted(..)`. But Rust's `LogTracker` prints the body it would file, and Go's does the same (`FILE key=... target=... title=...` and then the body). That path is reached by a live run with no tokens and no `DATABASE_URL`.

1. Spike, no code: confirm both binaries print identical `LogTracker` output for one recorded event, with the key's run id normalised. Report what differs.
2. If it works, extend `compare` to run both in that mode and diff the body, so bodies join title and target. Keep hostile text out of the report: print a diff of line numbers and a short hash, not the body.
3. If it does not, fall back to a Rust-side body dump as a test-only example. That adds a file to the Rust tree, so stop and ask Josh first.

Done means: `ingest-dated` and `empty-surfaces` stop being "body-only" fixtures and start testing something.

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
3. Names: store scopes and budgets key on the workflow name. If hyphens stay disallowed, write down what that does to a cutover.

### upstream-prs

Josh has write access to steading-ai/almanac, so the limits that sit in the library are patches, not requests. Candidates, in the order they unblock the most:

1. **Hyphenated workflow names.** The checker accepts `^[a-z][a-z0-9_]*$` (`check/check.go:88`). Allowing `-` clears 14 of the 18 `compare` disagreements. Check first whether anything keys on the name in a way that makes `-` unsafe (store scopes, metric or file names).
2. **An extensible `hostilevet` boundary list.** The list in `tools/hostilevet/taint/boundary.go` is hardcoded to `patch` and `github`. A flag or a module-owned list would clear three of the six findings.
3. **`github.Retrying` reaching a second call, a default config path, and a read-only default.** Smaller, and each is a field or an option.
4. **Unique outbox keys** ([#34](https://github.com/steading-ai/almanac/issues/34)). This one is the maintainer's design, so offer a patch only if she asks for one.

The lane works in a scratch clone of steading-ai/almanac, not in this repo, with one branch per patch, based on `go-rewrite` since that is the code brined imports. For each it writes the patch, the tests that upstream's own suite requires, and a pull request body in Josh's voice, then stops. Pushing a branch or opening the pull request is Josh's call, one patch at a time, and the agent never does either. When one is merged, brined moves its pin and deletes the workaround.

### divergences

Settle the 4 parse gaps and the name mismatch. For each, one of: fix on our side, send a pull request upstream (Josh has write access to steading-ai/almanac, and issue [#35](https://github.com/steading-ai/almanac/issues/35) already raises the name and boundary asks), or accept. The result is `docs/divergences.md`, which `compare`'s golden file is checked against, so an unlisted difference fails.

## Decisions for Josh

- **Is the Rust-side body dump allowed?** Only needed if the `LogTracker` route fails. It adds a file to the Rust tree.
- **Throwaway repo for `write-sandbox`.** Yours to create.
- **Which differences to accept.** The 4 parse gaps and the workflow name. `no-surfaces` would file an issue Rust refuses, so I would not accept that one.
- **Whether a shadow run should be a real deploy.** It starts as a local process beside prod. Anything that touches the prod stack is yours.

## Order

1. `body-parity` and `scan-parity` spikes, and `divergences`, in parallel. They own different files.
2. `store-parity` in parallel with those.
3. `write-sandbox` once the sandbox exists.
4. `shadow-run` after `body-parity` lands, since its diff needs bodies. It then runs for 7 days.
5. Coordinator folds the reports into `go-touch-points.md` and `porting-plan.md`, and writes the final parity statement against the contract above.
