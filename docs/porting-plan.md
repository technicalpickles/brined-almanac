# Porting plan: Rust to Go

The port follows Rust `main` and nothing else. Behaviour that is not on `main` yet, such as the tracker `Resolve` effect in [ivy/almanac#33](https://github.com/ivy/almanac/pull/33) and brined's PR [#38](https://github.com/technicalpickles/brined-almanac/pull/38), comes over only after it lands in Rust.

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
| 9 | `resolve_on_close` | 70 / 192 | cutover | none. It resolves or mutes the Bugsink issue through the Bugsink API, not an almanac effect | done |
| 10 | workflow `host_state` | 308 / 440 | host_state, routing | Go engine as a module | |
| 11 | workflow `security_alerts` | 704 / 565 | routing, tiers | Go engine, GitHub alerts client | |
| 12 | workflow `bugsink_issues` | 349 / 802 | bugsink, cutover, classes, resolve_on_close | Go engine as a module | |
| 13 | `providers` and `main` | 465 + 61 / 231 + 98 | everything | all of the above | |

Out of scope: `puller/`, `checks/` and `scripts/` are shell and stay shell. `almanac.toml`, `tiers.toml`, `hosts.toml` and `classes.toml` are shared by both implementations.

## Upstream

Ivy's Go library (`ivy/almanac`, branch `go-rewrite`) has no tagged release. brined's `go.mod` pins Ivy's own `go-rewrite` commit `86122f2` as a pseudo-version, with no `replace` and no local changes on top.

- **`Resolve` effect.** `effect.Tracker` has `File` and `Ask` only. Rust `main` does not use a tracker `Resolve` either (it pins `b8bdefa`), so nothing here waits on it. A Go version exists on a local, unpushed `brined-go` branch of `ivy/almanac`, shelved until `Resolve` lands in Rust.
- **Open:** whether the engine package is meant to be imported from outside the module.

Nothing is pushed to `ivy/almanac`. Posting a branch or PR there is the operator's call.

## Order rationale

- Leaves first (0 to 3), so every later unit has something real to import.
- Rules before workflows (4, 5, 6, 7), because the workflow tests mostly exercise these rules.
- Step 8 sits where its decision (shell out to `task`) was made, not at the end.
- `providers` and `main` last, since they only choose between the pieces above.
