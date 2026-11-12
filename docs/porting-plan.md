# Porting plan: Rust to Go

The order below comes from `use crate::` edges in `src/`, leaves first. Each step ends in a check against the Rust: the same table cases as the Rust test file, run on the Go side, with the Rust test run beside it. Status lives in the table, not in prose.

Sizes are Rust lines in `src/` and `tests/` at `ea999b3`.

| # | Unit | Rust src / tests | Depends on | Blocker | Status |
|---|---|---|---|---|---|
| 0 | `classes` | 86 / 203 | none | none | done |
| 1 | `routing` | 191 / 98 | none | `Router` waits for the Go effect package | `Project` done |
| 2 | `bugsink_api` | 134 / 250 | none | none | done |
| 3 | `transcript` | 355 / 364 | none | none | done |
| 4 | `tiers` | 111 / 157 | routing | none | done |
| 5 | `host_state` (rules) | 477 / 407 | routing | none | done |
| 6 | `bugsink` | 260 / 332 | classes, transcript | `reveal` boundary (see go-touch-points.md) | done |
| 7 | `cutover` | 630 / 314 | bugsink, bugsink_api | none | done |
| 8 | `taskwarrior` | 469 / 243 | providers, routing | No Go TaskChampion. Decided: shell out to `task` | |
| 9 | `resolve_on_close` | 70 / 192 | cutover | **Go has no `Resolve` effect** (see upstream) | |
| 10 | workflow `host_state` | 308 / 440 | host_state, routing | Go engine as a module | |
| 11 | workflow `security_alerts` | 704 / 565 | routing, tiers | Go engine, GitHub alerts client | |
| 12 | workflow `bugsink_issues` | 349 / 802 | bugsink, cutover, classes, resolve_on_close | `Resolve` | |
| 13 | `providers` and `main` | 465 + 61 / 231 + 98 | everything | all of the above | |

Out of scope: `puller/`, `checks/` and `scripts/` are shell and stay shell. `almanac.toml`, `tiers.toml`, `hosts.toml` and `classes.toml` are shared by both implementations.

## Upstream

Ivy's Go library (`ivy/almanac`, branch `go-rewrite`) is not a published module. Until it is, brined's `go.mod` uses a `replace` to a local checkout.

Gaps found so far, each to be a branch off `go-rewrite` in a local checkout of `ivy/almanac`:

- **`Resolve` effect.** `effect.Tracker` has `File` and `Ask` only. The Rust side has this in [ivy/almanac#33](https://github.com/ivy/almanac/pull/33), and the Go port of it is the first upstream branch. Steps 9 and 12 wait on it.
- **Open:** whether the engine package is meant to be imported from outside the module, and whether a `Tracker` can resolve by key.

Nothing is pushed to `ivy/almanac`. Posting a branch or PR there is the operator's call.

## Order rationale

- Leaves first (0 to 3), so every later unit has something real to import.
- Rules before workflows (4, 5, 6, 7), because the workflow tests mostly exercise these rules.
- Blocked units (8, 9) sit where their blocker is expected to clear, not at the end. Step 8 needs a decision before it starts. Nothing before it needs the answer.
- `providers` and `main` last, since they only choose between the pieces above.
