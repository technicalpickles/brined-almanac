# Divergences between the Go port and Rust

Parity contract item 1 ([go-port-parity-plan.md](go-port-parity-plan.md)) says `compare` shows zero unexplained differences. This file is where each accepted difference is explained. `compare/divergences_test.go` reads the table below and checks it against `compare/testdata/compare.golden`:

- A difference in the golden file that no row covers fails the test.
- A row that matches no difference in the golden file fails the test, so a fixed divergence has to leave this file.

Every resolution below is **proposed**. Accepting a divergence, or choosing between our side and upstream, is Josh's call. Nothing here is accepted yet. Until Josh decides, the rows stand as "known and explained", which is what keeps `compare` honest.

Fixture text is not quoted here. The gaps are described by structure, and the fixtures are named so you can open them under `compare/fixtures/`.

## Machine-readable table

One row per difference class and case set. Columns: `id | signature | cases`. Cases are comma separated. The signature is the difference line from the golden file. A `runs:` line is reduced so it carries no fixture text, and nothing else is dropped:

- Every run on both sides keeps its workflow and result, so an extra or missing run changes the signature.
- A failed run keeps `reason#` and the first 8 hex digits of the SHA-256 of the quoted reason as the golden file prints it. The reason can quote fixture values, so the doc holds only the hash, and a different reason changes the signature. When a row goes stale after a reason change, read the new reason in `compare.golden` and decide whether it is still the same divergence.
- Every effect keeps its kind and target and drops its title, so an extra, missing or retargeted effect changes the signature.

```divergences
DV-WORKFLOW-NAME | run 1 workflow: rust "bugsink-issues", go "bugsink_issues" | bugsink.issue.unaddressed/body-only-empty-surfaces, bugsink.issue.unaddressed/body-only-ingest-dated, bugsink.issue.unaddressed/first, bugsink.issue.unaddressed/other-service, bugsink.issue.unaddressed/regressed
DV-WORKFLOW-NAME | run 1 workflow: rust "host-state", go "host_state" | host.state.finding/check-critical, host.state.finding/check-dotted-project, host.state.finding/check-warning, host.state.finding/stale-never, host.state.finding/stale-since
DV-WORKFLOW-NAME | run 1 workflow: rust "security-alerts", go "security_alerts" | security.alert.finding/coverage, security.alert.finding/overdue-every-severity, security.alert.finding/overdue-secrets-only, security.alert.finding/overdue
```

Today that is 14 of 22 cases, all on the name (rows 1 to 3). The four parse gaps, where Rust refused an event at ingest and Go queued it, are fixed: brined's parsers now refuse a missing or non-list `surfaces`, a project that is not a taskwarrior project, and a `since` that does not split at `T`, as Rust's do.

Each id needs a section below with a `Status:` line and an `Owner:` line. The test checks that too.

## DV-WORKFLOW-NAME: hyphen vs underscore in the workflow name

- **Status:** open, proposed resolution pending.
- **Owner:** Josh (decision). Proposed worker: the `upstream-prs` lane.
- **Fixtures:** 14 cases, listed in the table above: every case where both sides start a run. A name difference does not hide others, because `compare` checks each field of a run on its own.
- **Rust side:** the workflow is the function `host_state` in `src/workflows/host_state.rs:264`, and likewise `security_alerts` (`src/workflows/security_alerts.rs:663`) and `bugsink_issues` (`src/workflows/bugsink_issues.rs:319`). The recordings print the names hyphenated. That the `#[workflow]` macro turns underscores into hyphens is inferred from the recordings, not read from the macro source.
- **Go side:** `Define("host_state", ...)` at `brine/hoststate/hoststate.go:16`, `Define("security_alerts", ...)` at `brine/securityalerts/securityalerts.go:20`, `Define("bugsink_issues", ...)` at `brine/bugsinkissues/bugsinkissues.go:15`. The checker in upstream almanac accepts only `^[a-z][a-z0-9_]*$` (`check/check.go:88` in the module `github.com/ivy/almanac`), so a hyphenated name does not check.
- **Why it matters beyond cosmetics:** store scopes and budgets key on the workflow name (parity plan, `store-parity` lane, item 3). A cutover that reuses the Rust store would see different scope names.
- **Proposed resolution:** upstream PR allowing `-` in workflow names (issue [steading-ai/almanac#35](https://github.com/steading-ai/almanac/issues/35) raises it; the `upstream-prs` lane is measuring a fix). If upstream declines, accept and record the store consequence for cutover.
- **Decision needed from Josh:** upstream PR or accept. If accept, say how a cutover handles names already in the Rust store.

## Open decisions in one place

1. Workflow names: upstream PR (preferred, clears 14 cases) or accept.
