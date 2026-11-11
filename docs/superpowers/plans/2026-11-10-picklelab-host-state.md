# Picklelab host state Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A timer, unit, or site that breaks on picklelab files a task in the taskwarrior project `homelab` within about 30 minutes, and a rescan files nothing new.

**Architecture:** picklehome gains `homelab/services/host-state/`: three Nagios-style checks, a `discover` step that writes one wrapper per discovered timer, unit, and site, a byte-for-byte copy of brined-almanac's `checks/collect`, and a systemd timer that writes `/var/lib/host-state/picklelab.json` every 5 minutes. A fenced SSH key whose forced command is `cat` of that file lets brined-almanac's puller container fetch it into `$STATE/host-state/`, where the existing host-state workflow reads it unchanged.

**Tech Stack:** bash, jq, curl, systemd (picklelab, Ubuntu 24.04, bash 5.2, systemd 255); pytest for picklehome tests; bash tests and docker compose in brined-almanac.

**Spec:** `docs/superpowers/specs/2026-11-10-picklelab-host-state-design.md` (this repository). Its decisions table is settled.

## Global Constraints

- Steps are named by slug. Delivery slugs: `host-state-checks`, `host-state-timer`, `report-key`, `report-fetch`, `real-thing`. One PR per code slug.
- picklehome work lives in `homelab/services/host-state/` and follows `homelab/services/CLAUDE.md`, `docs/CONVENTIONS.md`, and the `disk_monitor` service's layout. Tests are pytest under `tests/homelab/`, driving scripts through `subprocess` with stubs first on `PATH`, as `tests/homelab/test_openclaw_quiet.py` does.
- `collect` in picklehome is byte-for-byte brined-almanac `checks/collect` at origin/main, plus a header comment block inserted after the shebang line naming the source commit and saying it is not edited here. Nothing else differs.
- Check names match `[a-z0-9-]+`. Timer checks are `timer-<stem>`, unit checks `unit-<stem>`, site checks `site-<name>`.
- Report path on picklelab: `/var/lib/host-state/picklelab.json`, mode 0644. Host name `picklelab`. Collector interval 300 seconds.
- `site-check` timeout: 10 seconds. `timer-check` stuck-activating limit: 3600 seconds.
- The fence line is exactly: `restrict,from="<the Mac's tailnet address>",command="cat /var/lib/host-state/picklelab.json" ssh-ed25519 <public key> brined-almanac-host-state`
- 1Password item: `host-state-report`, an SSH key item in the `brined-almanac` vault. Secret reference `op://brined-almanac/host-state-report/private key?ssh-format=openssh`, env name `BRINED_ALMANAC_REPORT_KEY`.
- `hosts.toml` entry: `name = "picklelab"`, `project = "homelab"`.
- No env vars inline in commands. Never bypass commit signing. Deploys, merges, the 1Password item, the `authorized_keys` edit, and stopping `docker-prune.timer` wait for Josh.

## Review Focus

- **A timer driven by `OnUnitActiveSec` or `OnBootSec`.** Its realtime next elapse is empty and its monotonic one is set (`openclaw-approvals-watch` today). Expected: OK, not "unscheduled". Test in `host-state-checks` (timer-check case `monotonic-only`).
- **A unit name with characters outside `[a-z0-9-]`** (`dbus-org.freedesktop.resolve1.service` style, or uppercase). Expected: skipped with one stderr line, and the run still writes a report. Test in `host-state-checks` (discover case `bad-stem`).
- **A site that redirects.** Expected: the final status after following redirects decides (`curl -L`), so a `/` that 302s to a login page that returns 200 is OK. Test in `host-state-checks` (site-check case `redirect-200`).
- **picklelab answers SSH but the report file is missing** (first boot before the timer ran). `cat` prints an error and exits 1. Expected: `fetch-report.sh` keeps the old file and logs one line. Test in `report-fetch` (case `ssh-fails`).
- **`REPORT_KEY` unset in the puller.** Expected: `fetch-report.sh` logs `REPORT_KEY is empty, skipping` and returns 0, the transcript pass is unaffected, and almanac files `picklelab/stale/missing`. Test in `report-fetch` (case `no-key`).

---

## Slug `host-state-checks` (picklehome, one PR)

### Task: the three checks

**Files:**
- Create: `homelab/services/host-state/timer-check`, `unit-check`, `site-check` (executable, `#!/usr/bin/env bash`, `set -euo pipefail`)
- Test: `tests/homelab/test_host_state_checks.py`

**Interfaces:**
- Produces: `timer-check <name>.timer`, `unit-check <name>.service`, `site-check <url>`. Each prints one line and exits 0 (OK), 2 (critical), or 3 (unknown). Status-line prefix is the upper-cased check kind plus the argument, e.g. `TIMER backup.timer OK - next 2026-11-11 03:03 UTC`.

Behavior, all read through `systemctl show -p <props> --timestamp=unix <unit>` (one call per unit) or `curl`:

| Check | Critical when | Unknown when |
|---|---|---|
| `timer-check` | timer `ActiveState` is not `active`; both `NextElapseUSecRealtime` and `NextElapseUSecMonotonic` are empty; the timer's `Unit=` service has `Result` not `success`; that service is `activating` and `StateChangeTimestamp` is 3600s or more ago | `systemctl` fails or the timer's `LoadState` is `not-found` |
| `unit-check` | `ActiveState` is not `active` (print the state and `SubState`) | `systemctl` fails or `LoadState` is `not-found` |
| `site-check` | `curl -sS -L -o /dev/null -w '%{http_code}' --max-time 10` gives a code outside 200-299, or curl fails (print curl's exit reason, no body) | never |

`HOST_STATE_NOW` (epoch seconds), when set, replaces `date +%s` in `timer-check`, so tests fix the clock.

- [ ] **Write the failing tests.** Stub `systemctl` and `curl` scripts in `tmp_path/bin`, put first on `PATH`. The stub `systemctl` answers `show` from a JSON or key=value fixture keyed by unit name; the stub `curl` prints a code from an env var or exits 28. Cases, each asserting exit code and that stdout's first line starts with the expected prefix and contains the expected word:
  - timer: `ok` (active, realtime next set, service success) → 0; `monotonic-only` (realtime empty, monotonic set) → 0; `inactive` → 2; `no-next` (both empty) → 2; `last-failed` (service Result `exit-code`) → 2 and output contains `exit-code`; `stuck` (service activating, StateChangeTimestamp `@<now-3600>`) → 2; `activating-briefly` (`@<now-60>`) → 0; `not-found` → 3; `systemctl-fails` → 3.
  - unit: `active` → 0; `failed` → 2 and output contains `failed`; `inactive` → 2; `not-found` → 3.
  - site: `200` → 0; `redirect-200` (stub asserts `-L` is in its argv, prints 200) → 0; `500` → 2 and output contains `500`; `timeout` (curl exits 28) → 2 and output contains `timed out`.
- [ ] **Run them, see them fail.** `uv run pytest tests/homelab/test_host_state_checks.py -q` → errors, scripts missing.
- [ ] **Write the three scripts.**
- [ ] **Run them, see them pass.** Same command → all pass. `shellcheck homelab/services/host-state/*-check` clean.
- [ ] **Commit** via git:commit: `homelab/services/host-state/{timer,unit,site}-check`, the test.

### Task: discover, sites, host config, collect

**Files:**
- Create: `homelab/services/host-state/discover` (executable), `sites.conf`, `picklelab.conf`, `collect` (copy)
- Test: `tests/homelab/test_host_state_discover.py`

**Interfaces:**
- Consumes: the three checks from the previous task.
- Produces: `discover <wrappers-dir>`. Env `HOST_STATE_UNIT_DIR` (default `/etc/systemd/system`), `HOST_STATE_LINK_PREFIX` (default `/opt/homelab/`), `HOST_STATE_SITES` (default `sites.conf` beside the script). Writes the wrapper set into `<wrappers-dir>` atomically: build in `<wrappers-dir>.new.$$`, then replace the directory. Prints nothing on success.
- Produces: `picklelab.conf`, sourced by `collect`: `HOST=picklelab`, `OUT=/var/lib/host-state/picklelab.json`, `CHECK_DIR=/var/lib/host-state/checks`, `CHECKS` = the sorted file names in `CHECK_DIR`.

Discovery rules:
- `timer-<stem>` for each `*.timer` in the unit dir that is a symlink whose target starts with the link prefix. Wrapper body: `exec <service-dir>/timer-check <stem>.timer`.
- `unit-<stem>` for each such `*.service` whose stem does not contain `@` and is not the `Unit=` of a discovered timer (read `Unit=` from the timer file; when absent it is `<stem>.service`).
- `site-<name>` for each non-blank, non-`#` line `<name> <url>` in the sites file.
- A stem or name outside `[a-z0-9-]+` is skipped with `discover: skipping <name>: bad name` on stderr.
- `<service-dir>` is the directory `discover` lives in, resolved at run time.

`sites.conf`, with the tailnet suffix from picklehome's CLAUDE.md:

```
brineworks https://brineworks.<suffix>/health
taskchampion https://taskchampion.<suffix>/
bugsink https://bugsink.<suffix>/
openwebui https://openwebui.<suffix>/
woodpecker https://woodpecker.<suffix>/healthz
```

Confirm the woodpecker host name against `homelab/services/woodpecker/README.md` and its Tailscale serve config before writing it.

- [ ] **Write the failing tests.** A fake unit dir in `tmp_path` with: `backup.timer` → link into a fake `/opt/homelab/...` prefix, `backup.service` → link, `openclaw.service` → link, `notify-failure@.service` → link, `hand.timer` and `hand.service` as plain files, `smartd.service` → link outside the prefix, `Bad_Name.service` → link inside. Cases:
  - `discovers`: wrappers are exactly `timer-backup`, `unit-openclaw`, plus one `site-*` per sites line; each wrapper is executable and its body names the right check and argument.
  - `bad-stem`: stderr contains `skipping Bad_Name`; exit 0.
  - `converges`: running twice gives identical directory listings and contents.
  - `removed-unit`: delete the `openclaw.service` link, rerun, `unit-openclaw` is gone.
  - `timer-unit-key`: a timer with `Unit=other.service` excludes `other.service`, not `<stem>.service`, from unit checks.
  - `collect-end-to-end`: point the checks at stubs (stub `systemctl` and `curl` on `PATH` as in the previous task), run `discover` then `collect` on a copy of `picklelab.conf` whose `OUT` and `CHECK_DIR` are under `tmp_path`; the report parses as JSON with `.host == "picklelab"`, `.interval_seconds == 300`, and one `.checks[]` entry per wrapper.
  - `collect-is-a-copy`: `collect` with its header comment block removed equals a fixture `tests/homelab/fixtures/host_state/collect.upstream` byte-for-byte, and the header names the same commit as a `SOURCE_COMMIT` line in that fixture's sibling `collect.upstream.commit`.
- [ ] **Run them, see them fail.**
- [ ] **Copy `collect`.** `git -C ~/github.com/technicalpickles/brined-almanac show origin/main:checks/collect` into both `homelab/services/host-state/collect` and the fixture; record the commit (`git -C ... rev-parse --short origin/main`) in `collect.upstream.commit`. Insert the header block after the shebang in the service copy only.
- [ ] **Write `discover`, `sites.conf`, `picklelab.conf`.**
- [ ] **Run them, see them pass.** `uv run pytest tests/homelab/test_host_state_*.py -q`, then `shellcheck` on `discover`.
- [ ] **Commit**, then open the `host-state-checks` PR via git:pull-request.

## Slug `host-state-timer` (picklehome, one PR, stacked on `host-state-checks`)

### Task: units, deploy, docs

**Files:**
- Create: `homelab/services/host-state/host-state.service`, `host-state.timer`, `deploy.sh`, `README.md`, `CLAUDE.md` (`@README.md` only, if the other services have one)
- Modify: `Justfile` (add `deploy-host-state` and `host-state-run-now`, beside `deploy-disk-monitor`), `README.md` service list if it has one

**Interfaces:**
- Consumes: `discover`, `collect`, `picklelab.conf` from `host-state-checks`.

`host-state.service`: `Type=oneshot`, `User=root`, `StateDirectory=host-state`, `StateDirectoryMode=0755`, `UMask=0022`, `After=network-online.target`, `Wants=network-online.target`, `ExecStart=/bin/bash -c '/opt/homelab/homelab/services/host-state/discover /var/lib/host-state/checks && /opt/homelab/homelab/services/host-state/collect /opt/homelab/homelab/services/host-state/picklelab.conf'`.

`host-state.timer`: `OnBootSec=60`, `OnUnitActiveSec=300`, `Persistent=true`, `WantedBy=timers.target`.

`deploy.sh` mirrors `disk_monitor/deploy.sh` (deploy lock, link both units, daemon-reload, enable and restart the timer, then `systemctl start host-state.service` once so a report exists, then print `systemctl list-timers host-state.timer`). Every `sudo` in it must be on `homelab/config/sudoers-deploy-ops`; `ln -sf`, `daemon-reload`, `enable`, `restart`, and `start` are.

README: what it watches and how (discovery rules, `sites.conf`), where the report is, how to run a check by hand, how to read the journal, the fence line and that its `authorized_keys` entry is a hand edit documented in brined-almanac's runbook, and that `collect` is copied from brined-almanac and never edited here.

- [ ] **Write the units, `deploy.sh`, Justfile recipes, README.**
- [ ] **Verify locally.** `systemd-analyze verify` is not on the Mac, so: `shellcheck deploy.sh`, `just --list` shows both recipes, and `uv run pytest tests/homelab -q` still passes.
- [ ] **Commit**, open the `host-state-timer` PR.
- [ ] **Stop for Josh: merge both PRs and approve the deploy.** Then `just deploy-host-state`.
- [ ] **Check on the box.** `ssh picklelab cat /var/lib/host-state/picklelab.json | jq` shows `host == "picklelab"`, a `generated_at` within 5 minutes, and one check per discovered item. `ls -l` shows mode 0644. Read each non-OK check and record it in the decision trail; expected non-OK today: `unit-climate-auto-switch`.
- [ ] **Dry run from the Mac.** Copy the report into a temp directory as `picklelab.json` and run brined-almanac's `scripts/host-state-dry-run <dir>` from a checkout whose `hosts.toml` has the picklelab entry (the `report-fetch` branch). It parses, and shows what would file.

## Slug `report-key` (Josh, by hand)

- [ ] **Hand Josh the exact line**, with the Mac's tailnet address filled in from `tailscale ip -4` and the public key read after he creates the item (`op item get host-state-report --vault brined-almanac --fields 'public key'`, run unsandboxed only when Josh is at the laptop; otherwise ask him to paste the public key).
- [ ] **Josh:** creates the `host-state-report` SSH key item in the `brined-almanac` vault, and appends the line to `~technicalpickles/.ssh/authorized_keys` on picklelab.
- [ ] **Fence test.** Write the private key to a 0600 temp file through `op read` under `op run` or by Josh, then: `ssh -i <tmpkey> -o IdentitiesOnly=yes picklelab` prints the report JSON; `ssh -i <tmpkey> -o IdentitiesOnly=yes picklelab id` prints the report JSON and not `uid=`; `ssh -i <tmpkey> -o IdentitiesOnly=yes -L 9999:localhost:22 picklelab` gets no forwarding. Delete the temp key.

## Slug `report-fetch` (brined-almanac, one PR)

### Task: fetch-report.sh

**Files:**
- Create: `puller/fetch-report.sh` (executable), `puller/fetch-report.test.sh`
- Modify: `puller/pull.sh` (`main`), `puller/Containerfile` (copy the new script), `mise.toml` test task if it lists tests explicitly, the shellcheck file list in the hooks config

**Interfaces:**
- Produces: `fetch-report.sh`, env `REPORT_KEY` (private key text), `REPORT_KEY_FILE` (default `/run/puller/report-key`), `PULL_HOST`, `PULL_USER`, `REPORT_HOST_NAME` (default `picklelab`), `HOST_STATE_DIR` (default `/host-state`). Always exits 0.
- `pull.sh` `main` calls `"$(dirname "$0")/fetch-report.sh" || true` after each `pass`, in both loop and `PULL_ONCE` modes.

Behavior: if `REPORT_KEY` is empty, log `fetch-report: REPORT_KEY is empty, skipping` and exit 0. Write the key to `REPORT_KEY_FILE` (umask 077) if absent or different. `ssh -i "$REPORT_KEY_FILE" -o BatchMode=yes -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o ConnectTimeout=30 "$PULL_USER@$PULL_HOST"` with no command, stdout to `$HOST_STATE_DIR/$REPORT_HOST_NAME.json.tmp.$$`. Accept only when ssh exits 0 and `jq -e --arg h "$REPORT_HOST_NAME" '.host == $h and (.checks | type) == "array"'` passes; then `mv` into `$HOST_STATE_DIR/$REPORT_HOST_NAME.json`. Otherwise log one line naming the reason (`ssh failed`, `not json`, `wrong host`) and remove the temp file. Never write under `TRANSCRIPTS`.

- [ ] **Write the failing test** in the style of `puller/pull.test.sh` (fake `ssh` first on `PATH`, logs argv; key text built by string concatenation so gitleaks stays quiet). Cases:
  - `good`: fake prints a valid report → `$HOST_STATE_DIR/picklelab.json` equals it; key file mode 600; ssh argv has `-i $REPORT_KEY_FILE` and no remote command.
  - `not-json`: fake prints `garbage` → previous file unchanged, stderr has `not json`, no temp files left.
  - `wrong-host`: fake prints a report with `host: "mac"` → unchanged, stderr has `wrong host`.
  - `ssh-fails`: fake prints `cat: /var/lib/host-state/picklelab.json: No such file or directory` to stderr and exits 1 → unchanged, stderr has `ssh failed`.
  - `no-key`: `REPORT_KEY=` → exit 0, stderr has `skipping`, fake ssh never called.
  - `last-pull-untouched`: after `good` and `ssh-fails`, `$TRANSCRIPTS/.last-pull` does not exist (the test sets `TRANSCRIPTS` to a temp dir).
  - In `pull.test.sh`, one added case: a `PULL_ONCE` run with `REPORT_KEY` empty still exits as before and stderr contains `skipping`.
- [ ] **Run it, see it fail.** `bash puller/fetch-report.test.sh`.
- [ ] **Write `fetch-report.sh`, hook it into `pull.sh`, add it to the Containerfile.**
- [ ] **Run it, see it pass**, plus `bash puller/pull.test.sh` and `docker build -t brined-almanac-puller:smoke puller/`.
- [ ] **Commit.**

### Task: compose, secrets, hosts.toml, docs

**Files:**
- Modify: `compose.prod.yaml` (puller: `REPORT_KEY: "${BRINED_ALMANAC_REPORT_KEY:-}"`, volume `$STATE/host-state:/host-state` read-write), `prod.secrets.env` (the `BRINED_ALMANAC_REPORT_KEY` reference), `hosts.toml`, `scripts/compose-config-check`, `docs/runbooks/prod-on-the-mac.md` (Host state section: picklelab, the fence line, the key item, how to test the fence, what a stale picklelab means), `docs/workflows.md` (Host state: picklelab is the first pull host, and the transport sentence), `docs/roadmap.md` (M1 bullet links the new spec)

`REPORT_KEY` is optional in compose (`:-`, not `:?`), so the `unused` placeholder lists in `mise.toml`, the runbook, and `scripts/deploy` do not grow. A missing key shows up as `picklelab/stale/missing`.

- [ ] **Update `compose-config-check` first.** Replace the assertion that exactly one service mounts `/host-state` with: almanac mounts it read-only, puller mounts it read-write, and no other service mounts it; and `.services.puller.environment.REPORT_KEY` equals a stub value passed to `render`. Run it → fails.
- [ ] **Edit `compose.prod.yaml`, `prod.secrets.env`, `hosts.toml`.** Run `scripts/compose-config-check` → passes. `cargo test` passes (the hosts parser includes `hosts.toml` at build time).
- [ ] **Dry run.** A temp dir with the real picklelab report fetched in `host-state-timer`'s check, through `scripts/host-state-dry-run`.
- [ ] **Docs.** Runbook, workflows.md, roadmap.md as listed.
- [ ] **Commit**, run `scripts/image-smoke`, open the `report-fetch` PR.
- [ ] **Stop for Josh: merge, and approve the deploy.** Requires `report-key` done first, because `op run` fails on a reference to a missing item. Then `mise run deploy` from the main checkout.
- [ ] **Check.** Within 10 minutes `$STATE/host-state/picklelab.json` exists and matches the box's report; `docker logs brined-almanac-prod-puller-1` shows no `fetch-report:` errors; the next scan's log lists picklelab.

## Slug `real-thing`

- [ ] **Tell Josh before stopping the timer**, and get his OK.
- [ ] **Stop it.** `ssh picklelab sudo systemctl stop docker-prune.timer`. Record the time.
- [ ] **Watch.** Within about 30 minutes a `homelab` task titled `host-state: timer-docker-prune critical on picklelab since ...` exists. A rescan (`docker compose exec almanac brined-almanac scan` per the runbook) reports it unchanged and files nothing new.
- [ ] **Start it again, whatever happened.** `ssh picklelab sudo systemctl start docker-prune.timer`. Confirm the next report shows `timer-docker-prune` OK.
- [ ] **Record** times and task UUID in the decision trail and the spec's "What was measured".

## Close-out

- [ ] Close task 1b7eff2e with an annotation linking the PRs.
- [ ] File follow-ups (cite UUIDs): bring `openclaw-approvals-watch` under picklehome; retire `brineworks-server-watch` once `site-brineworks` has filed; the `climate-auto-switch` timer that isn't installed.
- [ ] Devlog entry.
