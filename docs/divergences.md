# Divergences between the Go port and Rust

Parity contract item 1 ([go-port-parity-plan.md](go-port-parity-plan.md)) says `compare` shows zero unexplained differences. This file is where each accepted difference is explained. `compare/divergences_test.go` reads the table below and checks it against `compare/testdata/compare.golden`:

- A difference in the golden file that no row covers fails the test.
- A row that matches no difference in the golden file fails the test, so a fixed divergence has to leave this file.

Every resolution below is **proposed**. Accepting a divergence, or choosing between our side and upstream, is Josh's call. Nothing here is accepted yet. Until Josh decides, the rows stand as "known and explained", which is what keeps `compare` honest.

Fixture text is not quoted here. The gaps are described by structure, and the fixtures are named so you can open them under `compare/fixtures/`.

## Machine-readable table

One row per difference class and case set. Columns: `id | signature | cases`. The signature is the difference line from the golden file, with the detail of a `runs:` line reduced to the Go workflow and its result. Cases are comma separated.

```divergences
DV-WORKFLOW-NAME | run 1 workflow: rust "bugsink-issues", go "bugsink_issues" | bugsink.issue.unaddressed/body-only-empty-surfaces, bugsink.issue.unaddressed/body-only-ingest-dated, bugsink.issue.unaddressed/first, bugsink.issue.unaddressed/other-service, bugsink.issue.unaddressed/regressed
DV-WORKFLOW-NAME | run 1 workflow: rust "host-state", go "host_state" | host.state.finding/check-critical, host.state.finding/check-dotted-project, host.state.finding/check-warning, host.state.finding/stale-never, host.state.finding/stale-since
DV-WORKFLOW-NAME | run 1 workflow: rust "security-alerts", go "security_alerts" | security.alert.finding/coverage, security.alert.finding/overdue-every-severity, security.alert.finding/overdue-secrets-only, security.alert.finding/overdue
DV-NO-SURFACES | ingest: rust "rejected", go "queued" | bugsink.issue.unaddressed/no-surfaces
DV-NO-SURFACES | runs: rust (no run), go "bugsink_issues" ran | bugsink.issue.unaddressed/no-surfaces
DV-HOST-PROJECT | ingest: rust "rejected", go "queued" | host.state.finding/project-not-taskwarrior
DV-HOST-PROJECT | runs: rust (no run), go "host_state" failed | host.state.finding/project-not-taskwarrior
DV-HOST-SINCE | ingest: rust "rejected", go "queued" | host.state.finding/since-without-date
DV-HOST-SINCE | runs: rust (no run), go "host_state" failed | host.state.finding/since-without-date
DV-SECURITY-PROJECT | ingest: rust "rejected", go "queued" | security.alert.finding/project-not-taskwarrior
DV-SECURITY-PROJECT | runs: rust (no run), go "security_alerts" failed | security.alert.finding/project-not-taskwarrior
```

Today that is 18 of 22 cases: 14 on the name (rows 1 to 3) and 4 on a parse gap, each of which shows up as two difference lines (the ingest status and the run).

Each id needs a section below with a `Status:` line and an `Owner:` line. The test checks that too.

## DV-WORKFLOW-NAME: hyphen vs underscore in the workflow name

- **Status:** open, proposed resolution pending.
- **Owner:** Josh (decision). Proposed worker: the `upstream-prs` lane.
- **Fixtures:** 14 cases, listed in the table above: every case that starts a run. A name difference does not hide others, because `compare` checks each field of a run on its own.
- **Rust side:** the workflow is the function `host_state` in `src/workflows/host_state.rs:264`, and likewise `security_alerts` (`src/workflows/security_alerts.rs:663`) and `bugsink_issues` (`src/workflows/bugsink_issues.rs:319`). The recordings print the names hyphenated. That the `#[workflow]` macro turns underscores into hyphens is inferred from the recordings, not read from the macro source.
- **Go side:** `Define("host_state", ...)` at `brine/hoststate/hoststate.go:16`, `Define("security_alerts", ...)` at `brine/securityalerts/securityalerts.go:20`, `Define("bugsink_issues", ...)` at `brine/bugsinkissues/bugsinkissues.go:15`. The checker in upstream almanac accepts only `^[a-z][a-z0-9_]*$` (`check/check.go:88` in the module `github.com/ivy/almanac`), so a hyphenated name does not check.
- **Why it matters beyond cosmetics:** store scopes and budgets key on the workflow name (parity plan, `store-parity` lane, item 3). A cutover that reuses the Rust store would see different scope names.
- **Proposed resolution:** upstream PR allowing `-` in workflow names (issue [steading-ai/almanac#35](https://github.com/steading-ai/almanac/issues/35) raises it; the `upstream-prs` lane is measuring a fix). If upstream declines, accept and record the store consequence for cutover.
- **Decision needed from Josh:** upstream PR or accept. If accept, say how a cutover handles names already in the Rust store.

## DV-NO-SURFACES: Go queues an event Rust rejects

- **Status:** open, proposed resolution pending.
- **Owner:** Josh (decision). Proposed worker: a `go-port` lane for the fix on our side.
- **Fixture:** `bugsink.issue.unaddressed/no-surfaces`. The event has every field except `surfaces`.
- **Rust side:** `Unaddressed::parse` reads `/surfaces` with `p.field(...)?` (`src/workflows/bugsink_issues.rs:68`), so a missing field is malformed and the event is rejected at ingest.
- **Go side:** `Unaddressed.Parse` reads the list with `wire.List` (`brine/bugsinkissues/bugsinkissues.go:97`). Upstream documents that "a missing list is empty" (`wire/payload.go:78-81`), and `readList` discards whether the pointer was found (`wire/payload.go:132-143`). The event is queued, and the run files an issue with an empty surfaces line.
- **Effect:** Go would file an issue that Rust refuses. This is the only parse gap where the outputs differ in what gets filed.
- **Proposed resolution:** fix, do not accept. The parity plan's view is the same. Two routes: reject in our `Parse` when `/surfaces` is absent (needs a way to see the raw pointer, which `wire.Payload` may not expose), or an upstream `wire` helper for a required list. Either route also needs a unit test with an event lacking the field.
- **Decision needed from Josh:** our side or upstream helper. This one should not be accepted.

## DV-HOST-PROJECT: project name checked at run time, not at ingest (host state)

- **Status:** open, proposed resolution pending.
- **Owner:** Josh (decision).
- **Fixture:** `host.state.finding/project-not-taskwarrior`. The `project` field is a valid slug but not a valid taskwarrior project name.
- **Rust side:** `HostFinding::parse` parses `/project` as a `Project` and returns malformed on failure (`src/workflows/host_state.rs:136-139`). The rule is `Project::from_str` (`src/routing.rs:13-38`). The event is rejected at ingest.
- **Go side:** deliberately deferred. `HostFinding.Parse` (`brine/hoststate.go:92-108`) only reads a slug, and its comment (`brine/hoststate.go:89-91`) says a parser can refuse only a field whose type fails to decode. The check runs later in `FilingOf` via `routing.ParseProject` (`brine/hoststate.go:140-141`, `routing/project.go:21-40`). The event is queued and the run fails at the `filing` step.
- **Effect:** nothing is filed on either side. Go leaves a failed run where Rust leaves a rejected event. The rule itself is identical (`routing/project.go` ports `src/routing.rs`).
- **Proposed resolution:** accept, provided a failed run is harmless in the store (needs a check in the `store-parity` lane). The alternative is a parse-time validation hook upstream, which is a bigger change for no change in what gets filed.
- **Decision needed from Josh:** accept, or ask for parse-time validation upstream.

## DV-HOST-SINCE: since checked at run time, not at ingest (host state)

- **Status:** open, proposed resolution pending.
- **Owner:** Josh (decision).
- **Fixture:** `host.state.finding/since-without-date`. The `since` value splits at `T` but its date part is empty, so it is not a slug.
- **Rust side:** `HostFinding::parse` runs `split(since)` and returns malformed on `None` (`src/workflows/host_state.rs:154-156`; `split` is `src/workflows/host_state.rs:217-224`). Rejected at ingest.
- **Go side:** `Parse` only requires `since` on a check finding (`brine/hoststate.go:95-99`). `splitSince` (`brine/hoststate.go:122-136`) runs from `FilingOf` (`brine/hoststate.go:150-155`). The event is queued and the run fails at the `filing` step.
- **Effect:** same shape as DV-HOST-PROJECT. Nothing is filed on either side.
- **Proposed resolution:** accept for the same reason, or fix in one move with DV-HOST-PROJECT if upstream gets a parse-time hook.
- **Decision needed from Josh:** same as DV-HOST-PROJECT. Decide the pair together.

## DV-SECURITY-PROJECT: project name checked at run time, not at ingest (security alerts)

- **Status:** open, proposed resolution pending.
- **Owner:** Josh (decision).
- **Fixture:** `security.alert.finding/project-not-taskwarrior`.
- **Rust side:** `SecurityFinding::parse` parses `/project` as a `Project` (`src/workflows/security_alerts.rs:527-532`). Rejected at ingest.
- **Go side:** `SecurityFinding.Parse` (`brine/securityalerts.go:87-101`, comment at `brine/securityalerts.go:84-86`) reads a slug. `fileSecurity` runs `routing.ParseProject` (`brine/securityalerts.go:109`), so the run fails at the `filing` step.
- **Effect:** same as DV-HOST-PROJECT.
- **Proposed resolution:** accept, decided together with DV-HOST-PROJECT.
- **Decision needed from Josh:** same as DV-HOST-PROJECT.

## Open decisions in one place

1. Workflow names: upstream PR (preferred, clears 14 cases) or accept.
2. `no-surfaces`: fix. Not for acceptance.
3. The three run-time versus ingest-time project and since checks: accept as one decision, or ask for a parse-time hook upstream.
