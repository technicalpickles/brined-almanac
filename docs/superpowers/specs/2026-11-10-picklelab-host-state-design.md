# Host state for picklelab, over pull

**Status:** design, 2026-11-10. The next slice of roadmap M1 after
[host state on the Mac](2026-11-10-host-state-design.md), whose report shape,
filing rule, and wake grace this slice reuses unchanged.

## Goal

When a scheduled job on picklelab stops being scheduled or fails, when a
long-running unit there is down, or when one of its sites stops answering, a
task exists in the `homelab` taskwarrior project within about 30 minutes, and
a rescan files nothing new.

The Mac slice built the check, report, and workflow shape. This slice adds the
second host, the first pull transport, and the first checks of the
dead-man's-switch and service-down check sets that are not almanac's own.

picklehome shipped Telegram alerting on 2026-11-09: `notify-telegram`, an
`OnFailure=` hook on `backup.service`, and a disk-monitor timer. That covers a
failed run of a unit that has the hook. It does not cover a timer that is no
longer scheduled, a long-running unit that is down, or a site returning 500.
workflows.md "service-down" says picklehome's watchdogs retire once their
checks file through almanac, so this slice is where that alerting is heading,
not a second copy of it.

## Decisions

| Decision | Choice |
|---|---|
| Where picklelab's checks live | picklehome, `homelab/services/host-state/` |
| How the report is made | A systemd timer runs the collector every 5 minutes and writes a file |
| What the SSH key may do | Print that file. The forced command is a literal `cat` |
| What fetches it on the Mac | The existing puller container, with its own key |
| Granularity | One check per timer, per unit, and per site |
| Which timers and units | Discovered from picklehome's symlinks, no list |
| Which sites | An explicit list of five in picklehome |
| Where findings file | taskwarrior project `homelab` |

Rejected, with the reason:

- **Checks in brined-almanac.** One copy of the collector, but every picklelab
  unit change becomes a cross-repository change, and brined-almanac would need
  its own way to deploy onto picklelab. workflows.md already says a job is
  registered on the host that runs it.
- **The pull runs the checks.** The report is always fresh, but checks then
  run as the SSH user inside the SSH session, a slow check stalls the pull, and
  the fence has to run code instead of reading a file. `since` still needs a
  state file on picklelab, so nothing is saved.
- **The almanac daemon pulls.** It puts SSH, a key, and a network step into
  the Rust scan, and `host-state` is registered first precisely because network
  scans fail. The puller already has the SSH client, `known_hosts`, and the
  1Password key wiring.
- **The Mac's LaunchAgent pulls.** SSH from launchd goes through the broker,
  which `launchd-spike` showed waits on a person.
- **A `host-state` verb on the puller's existing key.** No new secret, but the
  existing fence is pickleclaw's `openclaw-ro-pull`, which fronts redacted
  OpenClaw queries. A second key keeps each fence to one purpose in the
  repository that owns it, and each key rotates alone.
- **One aggregate check per kind.** Fewer checks, but while it is critical for
  one unit a second failure files nothing. That is the open risk `stack-health`
  already carries.
- **A hand-kept list of timers and units.** Reviewable, but adding a service is
  one more line to forget, which is the silent gap this work closes.

## What was read

On picklelab on 2026-11-10, read-only over SSH:

- picklehome installs each unit it manages as a symlink in
  `/etc/systemd/system/` pointing into `/opt/homelab/homelab/services/`. There
  were 6 such timers (backup, brineworks-server-watch, bugsink-vacuum,
  disk-monitor, docker-prune, nikke-sync), 9 services no linked timer
  activates (brineworks-server, bugsink, climate-auto-switch, nikke,
  obsidian-sync, open-webui, openclaw, taskchampion-sync, woodpecker), and the
  template `notify-failure@.service`.
- `openclaw-approvals-watch.service` and `.timer` are plain files, not
  symlinks. Its timer is monotonic (`OnUnitActiveSec` style): its realtime
  next elapse is empty and its monotonic one is set, which is how
  `list-timers` came to show it with no next time. `timer-check` must read
  both.
- `climate-auto-switch.timer` is unlinked on purpose: picklehome ADR 0001
  paused it on 2026-10-18. Its service is linked, and is `Type=oneshot` with
  no `RemainAfterExit`, a job and not a long-running unit. Every real
  long-running picklehome service is `Type=oneshot` with
  `RemainAfterExit=yes` (compose wrappers). So discovery skips a plain
  oneshot when it builds unit checks; once the timer is re-linked, the job is
  watched as `timer-climate-auto-switch`.
- `systemctl list-timers --output=json` gives `next` and `last` per timer.

The tailnet sites named in picklehome's service READMEs: brineworks
(`/health`), taskchampion, bugsink, openwebui, and woodpecker (`/healthz`).

## Shape

```
picklelab                                              Mac
host-state.timer (5 min)                               puller container (10 min)
  discover -> wrappers dir                               fetch-report.sh
  collect  -> /var/lib/host-state/picklelab.json  <──ssh── report key (forced cat)
                                                         -> $STATE/host-state/picklelab.json
                                                       almanac host-state workflow (unchanged)
```

### picklehome: `homelab/services/host-state/`

**`collect`** is a byte-for-byte copy of brined-almanac's `checks/collect`. A
header comment names the source commit. It is not edited in picklehome, so the
report format cannot drift between hosts. A change to it lands in
brined-almanac first and is copied.

**Checks.** Each takes one argument and follows the Nagios convention (exit 0
OK, 2 critical, 3 unknown, one status line).

- `timer-check <name>.timer` is critical when the timer is not active, when it
  has neither a realtime nor a monotonic next elapse time, when its service's last run's `Result` is not
  `success`, or when its service has been `activating` for more than an hour.
  It reads `systemctl show`. It does not judge an overdue run: picklehome's
  timers are `Persistent=true`, so systemd catches up missed runs itself.
- `unit-check <name>.service` is critical when `ActiveState` is not `active`.
- `site-check <url>` is critical on any status outside 2xx, or no answer within
  10 seconds. It runs from picklelab against the `ts.net` URL, so it tests the
  Tailscale serve path on the box, not reachability from elsewhere.

**`discover`** bridges a mismatch: `collect` runs checks by file name with no
arguments, and these checks take one. Each run, it empties a scratch directory
and writes one wrapper per discovered item, such as `timer-backup` containing
`exec <dir>/timer-check backup.timer`:

- every `.timer` in `/etc/systemd/system/` that is a symlink into
  `/opt/homelab/`, as `timer-<stem>`
- every such `.service` that is not a template and that no discovered timer
  activates, as `unit-<stem>`
- every line of `sites.conf` (`<name> <url>`), as `site-<name>`

The host config is sourced by `collect`, so it sets `CHECK_DIR` to the scratch
directory and computes `CHECKS` from its contents. `collect` is unchanged.
Wrapper names must match `[a-z0-9-]+`; an item whose stem does not is skipped
with a line on stderr.

**`host-state.service` and `host-state.timer`.** A oneshot that runs
`discover` then `collect`, every 5 minutes, `Persistent=true`, `User=root` like
the other picklehome units. `StateDirectory=host-state` puts the report at
`/var/lib/host-state/picklelab.json`, mode 0644: it names units and sites and
holds no secrets. `deploy.sh` links and enables them the usual way.

`host-state.timer` discovers itself, so if it stops being scheduled the next
run that does happen says so. If it stops entirely, the report goes stale and
almanac files that from the other side.

**The fence.** One hand-added line in `~technicalpickles/.ssh/authorized_keys`
on picklelab:

```
restrict,from="<the Mac's tailnet address>",command="cat /var/lib/host-state/picklelab.json" ssh-ed25519 <public key> brined-almanac-host-state
```

Whatever the client asks to run, it gets the file.

### brined-almanac

**`puller/fetch-report.sh`** runs once per puller loop iteration, whether or
not that iteration's transcript pass succeeded.

- `ssh -i <report key file> picklelab` with no command, output to
  `/host-state/picklelab.json.tmp`.
- The output must parse as JSON with `.host == "picklelab"`, then it is moved
  into place. Almanac's parser is still the boundary. This only keeps a
  truncated or wrong fetch from replacing a good report.
- On any failure it logs one line and leaves the old file. The stale rule
  decides what that means. It never touches `.last-pull`, so picklelab being
  down does not mark the puller unhealthy.

**Compose and deploy.** The puller gets `REPORT_KEY` from a new SSH key item,
`host-state-report`, in the `brined-almanac` vault, resolved by `mise run
deploy` as `PULLER_KEY` is, and written to the `/run/puller` tmpfs the same
way. `$STATE/host-state` is mounted read-write at `/host-state` in the puller.
The puller writes only `picklelab.json`; the Mac's collector keeps writing
`mac.json` beside it.

**`hosts.toml`** gains:

```toml
[[host]]
name = "picklelab"
project = "homelab"
```

The workflow, parser, filing rule, and delivery keys do not change. To almanac
picklelab is one more file in the directory.

### Timing

The report is regenerated every 5 minutes and fetched every 10, so it is at
most about 15 minutes old when read, under the 30 minute stale limit. A check
critical for 10 minutes files, so a dead timer, a down unit, or a failing site
files within about 25 minutes of starting.

## Testing

- **Each check**: a shell test with fake `systemctl` or `curl` first on `PATH`,
  asserting exit code and status line for each case above.
- **`discover`**: a fake `/etc/systemd/system` tree with linked, hand-installed,
  template, and timer-activated units, and a `sites.conf`. Assert the wrapper
  names, that a rerun converges to the same directory, and that a removed unit
  drops its wrapper.
- **`collect` on picklelab's config**: one run over the generated directory
  produces a report almanac's parser accepts (`scripts/host-state-dry-run`).
- **`fetch-report.sh`**: a test in the style of `puller/pull.test.sh`, with a
  fake `ssh` serving a good report, garbage, the wrong host, and a failure.
  Assert what ends in `/host-state` and that `.last-pull` is untouched.
- **The fence**: from the Mac, `ssh -i <key> picklelab` prints the report, and
  `ssh -i <key> picklelab id` prints the report too.
- **The real thing**: stop `docker-prune.timer` (weekly, harmless). A `homelab`
  task appears within about 30 minutes, and a rescan files nothing. Start it
  again.

## Delivery

Each a small pull request that ends in a check. Deploys wait for Josh.

- `host-state-checks` (picklehome): the three checks, `discover`, `sites.conf`,
  the copied `collect`, the host config, and their tests. No unit wired.
- `host-state-timer` (picklehome): service, timer, `deploy.sh`, README. After
  deploy, the report on picklelab is read and checked by hand.
- `report-key` (Josh, by hand): the `host-state-report` item in 1Password and
  the `authorized_keys` line. Then the fence test.
- `report-fetch` (brined-almanac): `fetch-report.sh`, compose, the deploy key
  wiring, `hosts.toml`, the runbook's Host state section, and workflows.md.
  `scripts/image-smoke`, then deploy.
- `real-thing`: the `docker-prune.timer` test above.

## What was measured

After the deploy on 2026-11-11 (picklehome `eeb9a4a`, brined-almanac
`b0f36d2`):

- The first report on picklelab had 20 checks: the 19 discovery found on
  2026-11-10, plus `timer-host-state` for the collector's own timer. All five
  sites answered 2xx.
- `timer-nikke-sync` was critical in that first report. That was a real
  failure: the 12:02Z sync died on a blablalink API error. The first scan after
  the deploy filed it as task `53db253c-f69a-4b17-a302-af513f116a8b`.
- The fence test with the 1Password key printed the report both with no
  command and with `id`. The key line carries `restrict`, so it allows no
  forwarding.
- `real-thing`: `docker-prune.timer` stopped at 14:49:20Z. The check went
  critical at 14:50:53Z, and the 15:03:55Z run filed task
  `dabac98e-6752-408d-ba1a-14406fc2b5ab`. That was 14m35s after the stop. The
  15:04:00Z scan counted the still-critical nikke-sync check as unchanged, so
  a rescan files nothing new. The timer started again at 15:09:40Z, and the
  15:11:05Z report showed the check OK.
- `backfill --dry-run` lists already-filed host-state findings as queued, the
  same way it lists already-filed Bugsink findings. It does not show what
  the store would count as unchanged, so read a live scan's counts instead.

## Not in this slice

- The WordPress droplet. It joins the tailnet first.
- Picklelab watching the Mac's almanac (15ea5b41). This slice makes it
  possible: picklelab now has a collector a check can join.
- Retiring picklehome's watchdogs. Each retires after its check has filed
  through almanac, one at a time, starting with `brineworks-server-watch`,
  which `site-brineworks` duplicates.
- Closing a task on recovery. Still waits on `Resolve` in M2.

## Open risks

- **The first scan may file a burst.** Every picklehome unit already failed
  files at once. That is the point, and it should be expected.
- **Duplicate alerts until a watchdog retires.** `brineworks-server-watch`
  alerts on Telegram, and `site-brineworks` files a task, for one outage.
- **Hand-installed units are not watched.** `openclaw-approvals-watch` is one
  today. Bringing it under picklehome is its own task.
- **Site checks run on the box.** They do not prove the site is reachable from
  elsewhere.
- **A copied collector can drift.** The header names the source commit, and
  the rule is that picklehome never edits it. Nothing enforces that yet.
- **Inherited:** a second outage on the same UTC day does not file (48058389),
  and the wake race on the Mac is unmeasured (7df153f6). Pulled reports are
  read on the same scan schedule, so a Mac wake can also see an old
  `picklelab.json`, under the same grace rule.
