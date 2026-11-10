# Host state, starting with the Mac's credential check

**Status:** design, 2026-11-10. The next slice of roadmap M1. It covers the
credential-expiry bullet and builds the check, report, and workflow shape that
service-down and the dead-man's switch reuse.

## Goal

When the credential broker stops answering or `gh auth status` fails on the
Mac, a taskwarrior task exists within an hour, and a rescan files nothing new.

On 2026-11-08 the broker was unreachable for over an hour. Every `gh` call and
every signed commit hung in every agent session on the Mac, and nothing
reported it.

No host-state code exists yet. This slice builds the first check, the first
report, and the first workflow that reads one, so the shapes it picks are the
ones every later check inherits.

## Decisions already made

| Decision | Choice | Decided |
|---|---|---|
| Who owns the dead-man's switch list | Both: hosts check their own jobs, almanac lists only its puller and daemon | 2026-11-10, recorded in workflows.md "Host state" |
| The Mac's transport | Push: a LaunchAgent writes the report to a directory the container mounts | 2026-11-10 |
| Where the Mac's checks live | This repository, in `checks/` | 2026-11-10 |
| Where the Mac's tasks go | The `brined-almanac` taskwarrior project | 2026-11-10 |

Pull over SSH was the other transport. It needs Remote Login on the Mac and a
key in the container, to read a file that is one bind mount away.

dotfiles was the other home for the checks. It splits every check change
across two repositories, and it is public while the report describes a private
stack.

## What was measured

Run on 2026-11-10 on the Mac, from an agent session.

- `gh auth status` from the repository took 3.9 seconds and exited 0. It
  resolves the token through the broker on every call, so one check covers
  "the broker answers for `gh`".
- The same command from `/private/tmp` failed at once with `project directory
  must be canonical`. A check that runs from the wrong directory reports an
  outage that isn't one.
- `puller/pull.sh` already writes `.last-pull` after each completed pass, and
  the compose healthcheck reads it. The dead-man's switch for the puller needs
  no new marker.

Not measured, and the plan starts by measuring them:

- Whether `gh` gets its token when launchd runs it, with no terminal and no
  agent session.
- Which `av` command probes the broker without raising an approval prompt.
- Whether a scan right after the Mac wakes sees a stale report before launchd
  reruns the collector.

## Shape

Three units. Each is tested alone.

```
checks/gh-auth ──┐
checks/...     ──┼─> checks/collect ──> $STATE/host-state/mac.json ──> host-state workflow ──> task
                 │   (LaunchAgent, 5 min)   (read-only mount)          (in the almanac container)
```

### Checks

A check is an executable in `checks/` that follows the Nagios plugin
convention, as workflows.md "Host state" describes. It exits 0 for OK, 1 for
warning, 2 for critical, and 3 for unknown, and prints one status line.

A check knows nothing about the report, the collector, or almanac. It runs by
hand and under any Nagios-shaped runner.

This slice ships one check, `checks/gh-auth`:

- It changes to a fixed, real directory first, so the broker's canonical-path
  rule can't fail it.
- It runs `gh auth status` and exits 0 when that succeeds, 2 when it fails.
- It prints `GH OK - logged in to github.com` or `GH CRITICAL - <first line of
  gh's error>`. It never prints a token. `gh auth status` masks the token, and
  the check keeps only the lines it names.

A direct broker check, `checks/broker`, joins it if the spike finds an `av`
command that probes without prompting. It covers the commit-signing path,
which `gh auth status` does not exercise. If no such command exists, the slice
ships with `gh-auth` alone and the gap is filed as a task.

### The collector

`checks/collect` runs every check and writes one report.

- It takes the host name, the output path, and the checks to run from a small
  config file next to it, `checks/hosts/mac.conf`. Adding a check to a host is
  one line there.
- It runs each check under a 20 second timeout. A check that times out is
  recorded as critical with the output `<NAME> CRITICAL - timed out after
  20s`. The 11-08 failure was a hang, so a hang has to be a result and can't
  stall the report.
- A check that exits outside 0 to 3, or can't be executed, is recorded as
  unknown.
- It reads the previous report to carry `since` forward (below).
- It writes the report under a temporary name in the same directory and
  renames it, so a reader never sees half a file.
- It exits 0 whenever it wrote a report, whatever the checks said.

It is a shell script. Its dependencies are `jq` and a timeout, both already on
the Mac.

A LaunchAgent, `com.technicalpickles.brined-almanac.host-state`, runs it every
300 seconds and at load. `scripts/deploy` installs the plist and loads it, the
same run that starts the stack. The plist template lives in `checks/launchd/`.

### The report

```json
{
  "host": "mac",
  "generated_at": "2026-11-10T15:00:00Z",
  "interval_seconds": 300,
  "checks": [
    {
      "name": "gh-auth",
      "status": 2,
      "since": "2026-11-10T14:50:00Z",
      "output": "GH CRITICAL - gh auth status timed out after 20s"
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `host` | The host's name. It must match the file name and an entry in `hosts.toml`. |
| `generated_at` | When the collector finished, UTC. |
| `interval_seconds` | How often the collector runs. |
| `checks[].name` | The check's file name. `[a-z0-9-]`. |
| `checks[].status` | The exit code, 0 to 3. |
| `checks[].since` | When the check entered this status, UTC. |
| `checks[].output` | The status line, cut to 500 bytes. |

`since` is what lets almanac stay stateless. The collector sets it to
`generated_at` when a check is new or its status differs from the previous
report, and copies it otherwise. If the previous report is missing or
unreadable, every `since` restarts at `generated_at`.

Almanac parses the report at the boundary into a typed model and rejects
anything else. `output` is host text and is carried as untrusted.

### hosts.toml

A committed file at the repository root, next to `tiers.toml`.

```toml
[[host]]
name = "mac"
project = "brined-almanac"
```

`name` finds the report at `<reports dir>/<name>.json`. `project` is the
taskwarrior project its findings file to. A report whose host has no entry is
an error in the scan's log and files nothing, so a stray file can't file
tasks.

A host that is declared and has no report file is a stale-report finding. The
transport is not in this file yet, because only push exists. Pull adds a field
when picklelab arrives.

### The host-state workflow

`src/workflows/host_state.rs`, registered in `workflows::all()`.

The scan reads `HOST_STATE_REPORTS`, a directory. When the variable is unset
the scan logs that and returns nothing, as security-alerts does without its
token. The dry run reads the same directory and needs no database and no
token.

For each declared host, the scan produces findings by this rule:

| Condition | Finding |
|---|---|
| Status critical or unknown, and `now - since` is at least 10 minutes | `check` |
| Status warning, and `now - since` is at least 24 hours | `check` |
| Report missing, or `now - generated_at` is over 30 minutes | `stale` |
| Report unreadable or invalid | `stale`, with the parse error as the body |

Ten minutes is two collector runs, so one bad run files nothing and a real
outage still files well inside the hour. A stale report files no `check`
findings, because its results describe the past.

Each finding is one event and one task.

| Finding | Delivery key | Title |
|---|---|---|
| `check` | `<host>/<check>/<UTC date of since>` | `host-state: gh-auth critical on mac since 2026-11-10 14:50Z` |
| `stale` | `<host>/stale/<UTC date of the last report>` | `host-state: no report from mac since 2026-11-10 14:50Z` |

The body is the check's output line, the status, and `since`.

A stale finding dates its key from the last report's `generated_at`. An
unreadable report has no such time, so its key uses the file's modification
date. A declared host with no file at all gets the key `<host>/stale/missing`,
which files once.

The date in the key is the design's central choice. Filing is create-once per
key. An outage that lasts days keeps one `since`, so it files once. After the
task is closed, a new outage on another day has a new `since` and files again.
A key of `<host>/<check>` alone would file once and then stay silent forever,
which is the trap that moved tool-failures to Bugsink. The cost is known: a
second outage on the same UTC day as one already closed files nothing.

The workflow may `File` and nothing else. It calls no model.

Nothing closes a task when a check recovers. That waits for the `Resolve`
effect in M2.

### service-down and credential-expiry

workflows.md lists these as two workflows. In code they would read the same
reports with the same rule, so there is one workflow, `host-state`.
credential-expiry and service-down become the names of check sets it reports:
`gh-auth` and `broker` now, units and site status next. The transcript scan
that credential-expiry plans for M6 stays its own workflow, because it reads
transcripts.

## Prod

- `compose.prod.yaml` mounts `$STATE/host-state` at `/host-state`, read-only,
  and sets `HOST_STATE_REPORTS=/host-state`.
- `scripts/deploy` creates `$STATE/host-state`, installs the LaunchAgent, and
  runs the collector once so the first scan has a report.
- The runbook gains a section: where the report is, how to run a check by
  hand, how to read the LaunchAgent's log, and how to unload it.

## Doc changes

- workflows.md: a `host-state` catalog entry with status next, and the
  service-down and credential-expiry entries point at it as their workflow.
  "Host state" gains the report's fields and the `since` rule.
- roadmap.md: the M1 credential-expiry bullet links to this design.
- README: `checks/` and `hosts.toml` in the layout, if it lists one.

## Testing

Each unit is tested through what its user sees.

- **`checks/gh-auth`**: a shell test with a fake `gh` first on `PATH`. It
  asserts the exit code and status line for success, failure, and a `gh` that
  is missing.
- **`checks/collect`**: a shell test with fake checks. It asserts the report
  for each exit code, a timeout recorded as critical, `since` copied when the
  status holds and reset when it changes, a missing previous report, and that
  a failed run leaves the old report in place. It follows
  `puller/pull.test.sh`, and `mise run test` runs it.
- **The report parser**: Rust tests over fixture files, valid and invalid.
- **The filing rule**: Rust tests over a table of reports and clock times,
  asserting the findings and their keys. The clock is a parameter.
- **Dry run**: a fixture directory with one critical report prints the task it
  would file.
- **The real thing**: on the Mac, after deploy, stop the broker or point the
  check at a `gh` that fails, and confirm a task appears within an hour and a
  second scan files nothing. Then confirm recovery writes a new `since`.

## Delivery

The plan opens with the three measurements under "What was measured". Their
answers can change the check list and the stale threshold, so they run before
any code that depends on them.

Then, in order, each a small pull request that ends in a check:

- `checks-and-collector`: `checks/gh-auth` and `checks/collect`, with their tests.
- `report-and-rule`: the report parser, `hosts.toml`, and the filing rule, with
  no workflow wired.
- `host-state-workflow`: the workflow and its dry run.
- `prod-wiring`: the compose mount, LaunchAgent, deploy, and runbook. Deploy
  waits for Josh's go-ahead.

## Not in this slice

- picklelab, the WordPress droplet, and the pull transport.
- Checks for units, site status, and job age.
- Closing a task when a check recovers.
- Almanac's own list for its puller and daemon. It is the next slice, and it
  reads `.last-pull`.

## Open risks

- **A false stale finding after wake.** Almanac may scan before launchd reruns
  the collector. This is a guess. If the measurement shows it, the fix is in
  the rule, for example requiring the report to be stale by more than the time
  the container itself has been running since wake. The plan decides after it
  has the number.
- **launchd may not get a token.** If the broker refuses `gh` outside an
  approved session, `gh-auth` under launchd would always be critical. The
  spike answers this first. A fallback is a check that asks the broker a
  question it answers without approval.
- **The thresholds are defaults.** Ten minutes, 24 hours, and 30 minutes are
  chosen, not measured. They are constants in one place.
