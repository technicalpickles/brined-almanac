# Host state, starting with almanac's own jobs on the Mac

**Status:** design, 2026-11-10, revised the same day after `launchd-spike`.
The next slice of roadmap M1. It covers the dead-man's switch for almanac's
own puller and stack, and builds the check, report, and workflow shape that
service-down and picklelab's checks reuse.

## Goal

When almanac's puller stops completing pulls, or a container in the prod stack
stops or turns unhealthy, a taskwarrior task exists within an hour, and a
rescan files nothing new.

Silent job failures were the top pick when the pain points were ranked on
2026-11-08. The puller's compose healthcheck already knows when pulls stop,
and nothing reads it but the runbook.

No host-state code existed before this slice. It builds the first checks, the
first report, and the first workflow that reads one, so the shapes it picks
are the ones every later check inherits.

### What this slice was first, and why it changed

The first version aimed at credential-expiry: a `gh-auth` check that ran `gh
auth status` on the Mac, after the broker outage of 2026-11-08. The spike
below showed that check can't work, and the broker's history showed it would
have tested the wrong thing even if it could. Automic Vault decides by
launcher, the app that started the command. Requests launched by Claude are
approved by an automatic rule, 1692 of them in the 24 hours before the spike.
The same `gh auth status` under launchd has `gh` itself as its launcher, has
no rule, and waits for a person. The 11-08 outage broke the Claude-launched
path. A launchd check travels a different one, so it could stay green while
every agent session hangs.

credential-expiry goes back to what it was before the 11-08 review moved it:
a scan of agent transcripts for authentication errors, which reads the
failure where it happens. That is roadmap M6 and not this slice.

## Decisions already made

| Decision | Choice | Decided |
|---|---|---|
| Who owns the dead-man's switch list | Both: hosts check their own jobs, almanac lists only its puller and daemon | 2026-11-10, recorded in workflows.md "Host state" |
| The Mac's transport | Push: a LaunchAgent writes the report to a directory the container mounts | 2026-11-10 |
| Where the Mac's checks live | This repository, in `checks/` | 2026-11-10 |
| Where the Mac's tasks go | The `brined-almanac` taskwarrior project | 2026-11-10 |
| The first checks | `puller-age` and `stack-health`, in place of `gh-auth` | 2026-11-10, after the spike |

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

Measured by `launchd-spike` on 2026-11-10 at 16:07Z. A throwaway LaunchAgent
ran each command once from the main checkout under a 20 second timeout, with
`PATH` set to `/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin`.

| Command under launchd | Exit | Time | Result |
|---|---|---|---|
| `gh auth status` | 124 | 20s | timed out, printing `automic vault: human approval required` |
| `av list --json` | 124 | 20s | timed out with no output |
| `av history --json --since 1m --no-pager` | 124 | 20s | timed out with no output |
| `av doctor gh --json` | 1 | 1s | answered at once, and reported an unrelated Git transport issue |

So `gh` does not get its token under launchd without a person approving it.
`av history` for the same minute records all three requests as `Manual`,
`Canceled`, "Gate client exited", with the command itself as the launcher.

Also read on 2026-11-10, with no credential involved: all four prod
containers reported healthy through `docker ps`, and `.last-pull` was 414
seconds old.

Still not measured:

- Whether a scan right after the Mac wakes sees a stale report before launchd
  reruns the collector.
- Whether `docker ps` answers when launchd runs it. Docker asks the broker for
  nothing, so this is expected to work, and `prod-wiring` confirms it on the
  real agent.

## Shape

Three units. Each is tested alone.

```
checks/puller-age   ──┐
checks/stack-health ──┼─> checks/collect ──> $STATE/host-state/mac.json ──> host-state workflow ──> task
                 │   (LaunchAgent, 5 min)   (read-only mount)          (in the almanac container)
```

### Checks

A check is an executable in `checks/` that follows the Nagios plugin
convention, as workflows.md "Host state" describes. It exits 0 for OK, 1 for
warning, 2 for critical, and 3 for unknown, and prints one status line.

A check knows nothing about the report, the collector, or almanac. It runs by
hand and under any Nagios-shaped runner.

This slice ships two checks. Neither needs a credential.

`checks/puller-age` is the dead-man's switch for the puller:

- It reads the epoch seconds in `$STATE/transcripts/.last-pull`, which
  `puller/pull.sh` writes after each completed pass.
- It is critical when the last pull is 1800 seconds old or more. That is three
  missed passes at the 10 minute interval, the same limit the puller's compose
  healthcheck uses.
- A missing marker is critical, because no pass has completed. A marker that
  is not a number is unknown. A marker later than now counts as age zero.
- It prints `PULLER OK - last pull 414s ago | age=414s;;1800`, `PULLER
  CRITICAL - last pull 2400s ago, limit 1800s | age=2400s;;1800`, `PULLER
  CRITICAL - no completed pull recorded`, or `PULLER UNKNOWN - marker is not
  a timestamp`.

`checks/stack-health` covers the daemon and the rest of the stack:

- It lists the containers of the compose project `brined-almanac-prod` and
  expects the services `db`, `almanac`, `puller`, and `backup`.
- It is critical when an expected service is missing, is not running, or
  reports unhealthy. A service whose health is still starting is fine.
- It is unknown when Docker doesn't answer.
- It prints `STACK OK - 4 services healthy`, `STACK CRITICAL - puller
  unhealthy, backup exited`, or `STACK UNKNOWN - docker not reachable`.

One limit is built in. Almanac files the tasks, so a stopped almanac container
files nothing about itself. `stack-health` reports it, and the report sits
unread until the daemon is back. Watching the watcher from another host is
picklelab's job, later.

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
      "name": "puller-age",
      "status": 2,
      "since": "2026-11-10T14:50:00Z",
      "output": "PULLER CRITICAL - last pull 2400s ago, limit 1800s"
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
| `check` | `<host>/<check>/<UTC date of since>` | `host-state: puller-age critical on mac since 2026-11-10 14:50Z` |
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

workflows.md lists these as two workflows. service-down would read the same
reports with the same rule, so it is not a second workflow. There is one,
`host-state`, and service-down becomes the name of a check set it reports:
`stack-health` now, units and site status next. The dead-man's switch is
another check set: `puller-age` now, picklelab's timers next.

credential-expiry is not a check set. It stays its own workflow, a transcript
scan in M6, for the reason under "What this slice was first".

## Prod

- `compose.prod.yaml` mounts `$STATE/host-state` at `/host-state`, read-only,
  and sets `HOST_STATE_REPORTS=/host-state`.
- `scripts/deploy` creates `$STATE/host-state`, installs the LaunchAgent, and
  runs the collector once so the first scan has a report.
- The runbook gains a section: where the report is, how to run a check by
  hand, how to read the LaunchAgent's log, and how to unload it.

## Doc changes

- workflows.md: a `host-state` catalog entry with status next, and the
  service-down entry points at it as its workflow. credential-expiry goes
  back to a transcript scan with status later.
  "Host state" gains the report's fields and the `since` rule.
- roadmap.md: the M1 dead-man's switch bullet links to this design, and the
  credential-expiry bullet moves back to M6 with the reason.
- README: `checks/` and `hosts.toml` in the layout, if it lists one.

## Testing

Each unit is tested through what its user sees.

- **`checks/puller-age`**: a shell test over a marker file in a temp
  directory and a fixed clock. It asserts the exit code and status line for a
  fresh pull, a pull at the limit, a missing marker, a marker that is not a
  number, and a marker later than now.
- **`checks/stack-health`**: a shell test with a fake `docker` first on
  `PATH`. It asserts the exit code and status line for four healthy services,
  one unhealthy, one exited, one missing, one still starting, and a `docker`
  that fails.
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
- **The real thing**: on the Mac, after deploy, stop the puller container and
  confirm a task appears within an hour and a second scan files nothing. Then
  start it and confirm recovery writes a new `since`.

## Delivery

The plan opened with `launchd-spike`, whose result is under "What was
measured" and changed the checks this slice ships.

Then, in order, each a small pull request that ends in a check:

- `checks-and-collector`: `checks/puller-age`, `checks/stack-health`, and
  `checks/collect`, with their tests.
- `report-and-rule`: the report parser, `hosts.toml`, and the filing rule, with
  no workflow wired.
- `host-state-workflow`: the workflow and its dry run.
- `prod-wiring`: the compose mount, LaunchAgent, deploy, and runbook. Deploy
  waits for Josh's go-ahead.

## Not in this slice

- picklelab, the WordPress droplet, and the pull transport.
- Checks for units and site status, and job-age checks for picklelab's timers.
- Any credential check. See "What this slice was first".
- Closing a task when a check recovers.

## Open risks

- **A false stale finding after wake.** Almanac may scan before launchd reruns
  the collector. This is a guess. If the measurement shows it, the fix is in
  the rule, for example requiring the report to be stale by more than the time
  the container itself has been running since wake. The plan decides after it
  has the number.
- **Docker under launchd is unmeasured.** If `docker ps` fails there,
  `stack-health` reports unknown on every run and files a task about itself
  after ten minutes. `prod-wiring` checks this before the agent is left
  loaded.
- **The thresholds are defaults.** Ten minutes, 24 hours, and 30 minutes are
  chosen, not measured. They are constants in one place.
