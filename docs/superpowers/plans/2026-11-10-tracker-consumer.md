# tracker-consumer

Goal: every failed tool call in prod lands in Bugsink, and every Bugsink issue nobody has addressed has a GitHub issue. Bugsink owns grouping and state; almanac files from it. This replaces the per-day `tool_failures` filer.

Step of: `docs/superpowers/plans/2026-11-09-error-tracker.md`, section `tracker-consumer`. Taskwarrior 33316a7c, with 7c21d5e7 folded into the issue body.

## Changes from the parent plan

- **No cutover gate.** The parent plan skipped every issue first seen before `bugsink::CUTOVER`. That hides the 48 issues the old per-day threshold never filed, though nothing addressed them. The consumer applies the class threshold to Bugsink's total event count instead, so backlog and new issues follow one rule. `CUTOVER` goes away.
- **Muted means addressed.** A muted Bugsink issue files nothing. Muting is how to say "known, leave it".
- **The 10 open legacy issues.** Their almanac markers carry the old per-day key, so the consumer files a new issue for each. After deploy, close each old issue as superseded with a link to its new one. The consumer does not read GitHub.

## Data shape

```
Unaddressed  (one per Bugsink issue that should have a GitHub issue)
  issue: Bugsink issue UUID
  generation: count of `regressed` + `reopened` turning points in its history
  service, tool, class: from the issue's tags
  count: digested_event_count
  first_seen, last_seen: earliest and latest event timestamp, from `/events/?issue={id}` (the issue's own are ingest time)
  surfaces: tag values with counts
  sessions: number of distinct `session` tag values
  reason: calculated_value (transcript text, Untrusted)
  link: Bugsink UI URL of the issue
```

An issue is unaddressed when it is unresolved, not muted, and `count >= threshold(class)`.

Identity, two layers, both idempotent:

- Scan delivery id `bugsink:<issue>`, `at_state(<generation>)`. Only unaddressed issues are emitted, so the first scan after an issue crosses its threshold is the first time almanac sees it.
- Workflow key `bugsink:{issue}:{generation}`. The GitHub tracker's marker lookup is `state=all`, so a closed issue still blocks a refile until a regression bumps the generation.

## Steps

- `move-classes`: move `Class`, `CLASSES`, `classify`, `class_named` to `src/classes.rs`, and `owner_of` with them. `bugsink.rs` imports from there. No behavior change. Tests pass.
- `api-client`: a read client for Bugsink's API (`/api/canonical/0`, Bearer `BUGSINK_API_TOKEN`), reusing `cutover::Bugsink`'s paging. Reads: unresolved issues of the project (`state=open` or equivalent, check what 2.6.3 accepts), `/issues/{id}/tags/`, `/issue-history/?issue={id}`. Parse layer tested on JSON fixtures in `tests/fixtures/`.
- `consumer`: workflow `bugsink_issues` in `src/workflows/bugsink_issues.rs`. Its scan ships transcripts first (`ship_from`, moved from `tool_failures`), then reads Bugsink and emits unaddressed issues. Skips with a message when `BUGSINK_API_TOKEN` or the DSN is unset. A Bugsink read failure fails the scan. `facts` and `policy` build one `Effect::File` to `owner_of(service)`.
- `body`: header lines `service:`, `tool:`, `class:` first (so `cutover::key_in_body` still parses it), then events, first and last seen, surfaces, distinct sessions, regression note when generation > 0, Bugsink link, then the reason as untrusted text. Title `Brent: {service}/{tool} {class} ({count} events)`, with `regressed` when generation > 0.
- `delete-filer`: remove `tool_failures` (registration, `ClusterKey`, `Cluster`, `cluster`, `ToolFailed`, `facts`, `policy`) and its tests. Drop `CUTOVER`.
- `prod-wiring`: `BUGSINK_API_TOKEN` through `prod.secrets.env`, `compose.prod.yaml`, `scripts/deploy`, `scripts/compose-config-check`, as `BUGSINK_DSN` is. The op reference is the one `cutover.secrets.env` uses. Update the runbook and README where they name the filer.

## Verify

- `mise run test` and `mise run check`.
- A dry run of the scan against prod Bugsink lists what would file. Expect the 10 legacy fingerprints plus however many of the 48 cross their thresholds.
- After deploy: the issues file once, a second scan files nothing, and the 10 legacy issues get closed as superseded.
