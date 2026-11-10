# resolve-on-close

Goal: a closed almanac issue must never silence its failure. Almanac dedupes on `state=all` and only a resolved Bugsink issue can regress, so a close that leaves the Bugsink issue unresolved hides every later event. The scan resolves the Bugsink issue when its almanac issue is closed as completed, and mutes it when closed as not planned. The decision is D4 in `2026-11-09-error-tracker.md`; the worker never touches Bugsink.

## Data shape

```
Closed  (one per almanac issue that carries a Bugsink link and is closed)
  repo, number
  bugsink issue UUID      parsed from the `bugsink:` line, `.../issues/issue/<uuid>/event/last/`
  generation              the `regressions: N` line, or 0 when absent
  reason                  Completed | NotPlanned   from GitHub's `state_reason`
  closed_at

Verdict  (per open Bugsink issue)
  Resolve { proof: url }
  Mute    { proof: url }
  Leave
```

## Rule

A Bugsink issue is acted on only when all of these hold.

- It is in the open list (unresolved, unmuted).
- Among the almanac issues that carry its UUID, the newest one by creation time is closed.
- That newest issue's generation equals the Bugsink issue's current generation (`generation(history)`). Without this check, a regression would be resolved again before its new issue files, because the old closed issue still carries the same link.
- `state_reason` is `completed` for Resolve and `not_planned` for Mute. Any other close (reason missing) leaves the issue alone.

Then the scan acts before it emits events, and an acted issue is skipped by the emit loop in the same scan. A dry run prints what it would do and calls nothing.

Resolve: comment first, then `POST /issues/{id}/resolve/`, as `cutover::Bugsink::resolve` does, with a comment that links the close. Mute: comment, then `POST /issues/{id}/mute-until/` with `period_name`, `nr_of_periods` 1, `gte_threshold` `MUTE_WAKE_EVENTS`. Check the accepted `period_name` values in `bugsink/period_utils.py` at tag 2.6.3. `MUTE_WAKE_EVENTS` is 50, a default for Josh to tune, and the doc comment says a wrong not-planned close would otherwise be silent forever.

## Steps

- `read-closed`: a read of almanac issues across `cutover::REPOS` with `state_reason`, closed state, creation time, number and body, reusing `cutover::Github` (extend `Filed` or add a sibling, whichever is smaller). Parse functions (`bugsink_link_in_body`, `generation_in_body`) are pure and tested on fixtures. Needs `GITHUB_TOKEN`, already in the prod container; extend `providers::bugsink_reader_from` or add a provider. Without it the step skips with a message, as the reader does today.
- `decide`: a pure function from (open Bugsink issues with their current generation, closed almanac issues) to verdicts, in `src/workflows/bugsink_issues.rs` or a new module. Tests cover newest-issue-wins, generation mismatch leaves it, not planned mutes, missing reason leaves it, an issue with no almanac issue leaves it, and an already-resolved issue is never in the input.
- `act`: `Bugsink::mute_until` beside `resolve` in `src/cutover.rs`, and the scan wiring with the dry-run guard. A failure to resolve one issue prints and moves on, since Bugsink or GitHub being down must not stop filing; a read failure of GitHub skips the whole step with a message.
- `docs`: README and runbook mention the behaviour and `GITHUB_TOKEN`; the error-tracker plan's `worker-resolves` checkboxes tick.

## Verify

- `mise run test`, `mise run check`.
- Loopback mock like `tests/support/api.rs`: close then scan resolves; resolve then new event then scan files a regression and does not resolve it again; not planned mutes.
- Dry run against prod through `scripts/bugsink-dry-run`: it should print zero actions today, since no almanac issue is closed. Then close one test case by hand after deploy and read the next scan.
