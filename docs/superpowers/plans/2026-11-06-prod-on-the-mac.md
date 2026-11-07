# Prod on the Mac Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run brined-almanac continuously on the Mac as a compose stack (Postgres, the `run` daemon, a transcript puller), deployed by `mise run deploy`, with secrets from a 1Password vault and no git checkout on the prod path.

**Architecture:** A multi-stage Containerfile builds the almanac binary with sqlx offline; `compose.prod.yaml` runs `db`, `almanac`, and `puller` under the fixed project name `brined-almanac-prod`. The puller lists OpenClaw sessions on picklelab over ssh with its own key, fenced on picklelab by a forced command (`openclaw-ro-pull`, in pickleclaw), and appends each session's transcript since its cursor into a bind-mounted `transcripts/` dir the daemon scans. `op run` resolves the two secrets from `op://` references at deploy.

**Tech Stack:** Rust 1.99.0 (existing crate, unchanged), Docker and Compose under OrbStack, postgres:17-alpine, bash + jq + openssh-client (alpine:3) for the puller, 1Password CLI 2.41.1, mise, hk 2.7.1.

**Spec:** `docs/superpowers/specs/2026-11-06-prod-on-the-mac-design.md`

## Global Constraints

- Public repo. Nothing in any committed file may be transcript text, a secret, a Telegram id, a bot handle, or a tailnet IP address. `mise run check` (fmt, clippy, gitleaks, forbidden) must pass before every commit; `scripts/check-forbidden` runs on staged files at commit.
- Compose project name is `brined-almanac-prod`, fixed in `compose.prod.yaml` with `name:`. The dev `compose.yaml` and its portool ports are untouched until Task 6.
- Prod state root is `${BRINED_ALMANAC_STATE:-$HOME/.local/share/brined-almanac}` with `transcripts/`, `prod.env`, `op-token` (mode 0600), `known_hosts`. Nothing in it is a git checkout; no private key or GitHub token is ever written there.
- Secret references are exactly `op://brined-almanac/github-token/credential` and `op://brined-almanac/puller/private key?ssh-format=openssh`. The env names compose interpolates are `BRINED_ALMANAC_GITHUB_TOKEN` and `BRINED_ALMANAC_PULLER_KEY`, both guarded with `:?`.
- Images: builder `rust:1.99-bookworm` with `SQLX_OFFLINE=true` and `cargo build --release --locked`; runtime `debian:bookworm-slim` + `ca-certificates` + `curl`, non-root user `almanac`, `ENTRYPOINT ["brined-almanac"]`, `CMD ["run"]`. Puller `alpine:3` + `bash`, `jq`, `openssh-client`. Tags `<image>:<short sha>` and `<image>:prod`.
- Daemon env in compose: `DATABASE_URL=postgres://almanac@db:5432/almanac`, `BRENT_TRANSCRIPTS=/transcripts`, `ALMANAC_LISTEN=0.0.0.0:8788`, health published at `127.0.0.1:8788`. `db` publishes no port.
- Forced command accepts exactly `sessions` and `transcript <key> --since <13 digits>` with `<key>` matching `^[A-Za-z0-9:_.-]+$`; anything else exits 2 with nothing on stdout.
- Puller cursor per session key is the session's `updatedAt` (epoch ms) from the listing that triggered the pull, plus one. `--since` is inclusive, and the reader dedups by event id, so an overlap is harmless.
- Commit messages: imperative subject, body says why, trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`. Commits are signed; never bypass signing.

## Review Focus

1. `SSH_ORIGINAL_COMMAND` carrying shell metacharacters, a second command after `;`, extra arguments, or a different case (`Sessions`) must exit 2 with empty stdout, never reach `openclaw-ro`. Test in Task 1.
2. A `transcript` pull that dies mid-stream (ssh exits non-zero after writing some lines) must leave the session's `.jsonl` and cursor exactly as they were. Test in Task 3.
3. A pass whose `sessions` listing fails must not write `.last-pull`, so the puller's healthcheck goes unhealthy instead of hiding a dead picklelab. Test in Task 3.
4. An empty or missing `PULLER_KEY` must make the puller exit non-zero before any ssh, so `compose ps` shows the restart loop instead of a silent idle. Test in Task 3.
5. `docker compose config` without `BRINED_ALMANAC_GITHUB_TOKEN` or `BRINED_ALMANAC_PULLER_KEY` must fail on the `:?` guard, so a deploy that skipped `op run` cannot start a daemon with an empty token. Test in Task 4.

---

### Task 1: The forced command on picklelab (`openclaw-ro-pull`, in pickleclaw)

This task lives in the **pickleclaw** repository (`~/github.com/technicalpickles/pickleclaw`, private). Create a worktree there with `wt switch --create almanac-pull --base origin/main --yes --no-cd --format=json` and work in its path. The script deploys to `/opt/pickleclaw/scripts/openclaw-ro/` through picklehome's deploy, same as `openclaw-ro`.

**Files:**
- Create: `scripts/openclaw-ro/openclaw-ro-pull` (bash, executable)
- Create: `scripts/openclaw-ro/openclaw-ro-pull.test.sh` (bash, executable; same style as `openclaw-ro.test.sh`: fake binary on PATH, `set -uo pipefail`, counts PASS/FAIL, exits 1 on any FAIL)

**Interfaces:**
- Consumes: `SSH_ORIGINAL_COMMAND` from sshd; `openclaw-ro` in the same directory (`OPENCLAW_RO` env overrides the path, for the test).
- Produces: `exec "$OPENCLAW_RO" sessions` or `exec "$OPENCLAW_RO" transcript <key> --since <ms>`. Any other input: one line on stderr starting with `openclaw-ro-pull: refused`, nothing on stdout, exit 2. Task 3's puller sends only these two shapes.

- [ ] **Step 1: Write the failing test**

`openclaw-ro-pull.test.sh` puts a fake `openclaw-ro` in a temp dir that prints `ARGV:` followed by its arguments one per line and exits 0, sets `OPENCLAW_RO` to it, and runs the wrapper with each `SSH_ORIGINAL_COMMAND` below, asserting stdout, stderr prefix, and exit code:

| `SSH_ORIGINAL_COMMAND` | expect |
|---|---|
| `sessions` | stdout `ARGV:\nsessions`, exit 0 |
| `transcript agent:main:direct:abc --since 1762400000000` | stdout `ARGV:\ntranscript\nagent:main:direct:abc\n--since\n1762400000000`, exit 0 |
| (unset) | exit 2, stdout empty, stderr starts `openclaw-ro-pull: refused` |
| `` (empty) | exit 2 |
| `Sessions` | exit 2 |
| `sessions extra` | exit 2 |
| `sessions; id` | exit 2 |
| `transcript agent:main:x --since 1762400000000; id` | exit 2 |
| `transcript agent:main:x --since 176240000000` (12 digits) | exit 2 |
| `transcript agent:main:x --last 5` | exit 2 |
| `transcript "agent:main:$(id)" --since 1762400000000` | exit 2 |
| `transcript agent:main:x --since 1762400000000 --last 5` | exit 2 |
| `version` | exit 2 |

Also assert the fake was never executed for a refused case (the fake appends to a log file; the log is empty after the refused cases).

- [ ] **Step 2: Run it to verify it fails**

Run: `scripts/openclaw-ro/openclaw-ro-pull.test.sh`
Expected: FAIL (wrapper missing)

- [ ] **Step 3: Implement `openclaw-ro-pull`**

`#!/usr/bin/env bash`, `set -euo pipefail`. Read `${SSH_ORIGINAL_COMMAND:-}`. Match with two anchored bash regexes, `^sessions$` and `^transcript ([A-Za-z0-9:_.-]+) --since ([0-9]{13})$` (single spaces). On match, `exec "${OPENCLAW_RO:-$HERE/openclaw-ro}"` with the literal words (never the raw string, never `eval`). Otherwise print `openclaw-ro-pull: refused: <first 40 chars, with newlines removed>` to stderr and `exit 2`. Six to ten lines of logic. Header comment: what it fences, where the `authorized_keys` line lives (the brined-almanac runbook), and that `openclaw-ro` validates again on its own.

- [ ] **Step 4: Run the test to verify it passes**

Run: `scripts/openclaw-ro/openclaw-ro-pull.test.sh`
Expected: all cases PASS, exit 0. Also `shellcheck scripts/openclaw-ro/openclaw-ro-pull` is clean.

- [ ] **Step 5: Commit in the pickleclaw worktree**

```bash
git add scripts/openclaw-ro/openclaw-ro-pull scripts/openclaw-ro/openclaw-ro-pull.test.sh
git commit   # subject: Add openclaw-ro-pull, the forced command for the almanac puller
```

Push and PR follow pickleclaw's normal flow (ask Josh). Task 5 needs this deployed on picklelab before its puller steps.

---

### Task 2: The almanac image

**Files:**
- Create: `Containerfile`
- Create: `.dockerignore` (`target/`, `local/`, `transcripts/`, `.env*`, `.claude/`, `docs/`, `.git/`)
- Create: `scripts/image-smoke` (bash; builds a tag and runs the checks below; used by the hook-free `mise run deploy` later and by hand)
- Modify: `mise.toml` (add task `image:smoke` → `scripts/image-smoke`)

**Interfaces:**
- Consumes: the crate as it is; `crates/almanac/.sqlx` arrives with the git dependency.
- Produces: image `brined-almanac:<tag>` with `ENTRYPOINT ["brined-almanac"]`, `CMD ["run"]`, user `almanac` (uid 10001), `curl` present. Task 4 references it as `brined-almanac:prod`.

- [ ] **Step 1: Write the failing smoke script**

`scripts/image-smoke [tag]` (default tag `smoke`):
1. `docker build -f Containerfile -t brined-almanac:$tag .`
2. `docker run --rm brined-almanac:$tag synth --format json | jq -e '.[0].workflow == "tool-failures" and .[0].scans == true'` (the manifest is a JSON array with one object per workflow; verified against `cargo run -- synth --format json`).
3. `docker run --rm --entrypoint id brined-almanac:$tag -u` prints `10001`.
4. `docker run --rm --entrypoint curl brined-almanac:$tag --version` exits 0.
5. `docker run --rm brined-almanac:$tag check` exits 0 and stdout contains `1 workflow(s)`.
6. `empty=$(mktemp -d); docker run --rm -v "$empty:/transcripts:ro" -e BRENT_TRANSCRIPTS=/transcripts brined-almanac:$tag backfill --dry-run` exits 0 (the reader accepts an empty directory; no token, so the LogTracker would print any File, and there is none).
7. The same `backfill --dry-run` with `-e BRENT_TRANSCRIPTS=/transcripts` but no mount exits non-zero and stderr names `/transcripts` (proves the env var is honored and a missing mount fails loudly; `check` cannot prove this, it only verifies the provider is registered).
Prints `image-smoke: ok` at the end.

- [ ] **Step 2: Run it to verify it fails**

Run: `scripts/image-smoke`
Expected: FAIL at `docker build` (no Containerfile)

- [ ] **Step 3: Write `Containerfile`**

Stage `build`: `FROM rust:1.99-bookworm`, `ENV SQLX_OFFLINE=true CARGO_NET_GIT_FETCH_WITH_CLI=true`, `WORKDIR /src`. Copy `Cargo.toml Cargo.lock`, write a stub `src/main.rs` (`fn main() {}`), `cargo build --release --locked`, then `COPY src ./src`, `touch src/main.rs`, `cargo build --release --locked` again. Stage `runtime`: `FROM debian:bookworm-slim`, `apt-get install -y --no-install-recommends ca-certificates curl` and clean, `useradd -r -u 10001 almanac`, `COPY --from=build /src/target/release/brined-almanac /usr/local/bin/`, `USER almanac`, `ENTRYPOINT ["brined-almanac"]`, `CMD ["run"]`. Comment on the stub trick (dependency layer survives a source edit) and on `SQLX_OFFLINE` (queries checked against the cache ivy ships).

- [ ] **Step 4: Run the smoke script to verify it passes**

Run: `scripts/image-smoke`
Expected: `image-smoke: ok`. Second run of `docker build` after touching `src/main.rs` must show the dependency layer cached (`CACHED` on the first `cargo build` step).

- [ ] **Step 5: Commit**

```bash
git add Containerfile .dockerignore scripts/image-smoke mise.toml
git commit   # subject: Build the almanac image with sqlx offline
```

---

### Task 3: The puller image and `pull.sh`

**Files:**
- Create: `puller/Containerfile`
- Create: `puller/pull.sh` (bash, executable)
- Create: `puller/pull.test.sh` (bash, executable)
- Modify: `mise.toml` (tools: add `shellcheck`; task `test` also runs `puller/pull.test.sh`)
- Modify: `hk.pkl` (new step `shellcheck` over `puller/pull.sh scripts/check-forbidden scripts/image-smoke scripts/deploy` in `check` and `pre-commit`; `scripts/deploy` arrives in Task 4, so list it then)

**Interfaces:**
- Consumes (env): `PULL_HOST`, `PULL_USER`, `PULL_INTERVAL_MINUTES` (default 10), `PULLER_KEY` (OpenSSH private key text), `TRANSCRIPTS` (default `/transcripts`), `KEY_FILE` (default `/run/puller/key`), `PULL_ONCE=1` runs one pass and exits (tests). `ssh` from PATH; tests put a fake first on PATH.
- Produces: `$TRANSCRIPTS/<safe-key>.jsonl` (append-only), `$TRANSCRIPTS/.cursors.json` (`{"<raw key>": <ms>}`), `$TRANSCRIPTS/.last-pull` (epoch seconds, touched after a completed pass). Task 4 mounts `TRANSCRIPTS` read-only into the daemon and checks `.last-pull` age in the healthcheck.
- ssh invocation shape, exactly: `ssh -i "$KEY_FILE" -o BatchMode=yes -o StrictHostKeyChecking=yes "$PULL_USER@$PULL_HOST" sessions` and `... transcript <key> --since <ms>`. The remote side is Task 1's forced command, so no path is sent.

- [ ] **Step 1: Write the failing test**

`puller/pull.test.sh` builds a temp dir with `bin/ssh` (fake) and `transcripts/`, exports `PATH=$TMP/bin:$PATH`, `TRANSCRIPTS=$TMP/transcripts`, `KEY_FILE=$TMP/key`, `PULLER_KEY` set to three lines shaped like an OpenSSH key (the BEGIN and END marker lines plus `fake`; build the markers by string concatenation in the test, never as one literal, or gitleaks' private-key rule blocks the commit), `PULL_HOST=h PULL_USER=u PULL_ONCE=1`. The fake `ssh` reads `$TMP/sessions.json` for `sessions`, and for `transcript K --since S` emits `$TMP/transcript.K.jsonl` filtered to lines whose `timestamp_ms` field is `>= S`, or exits 1 after printing one line when `$TMP/fail.K` exists. It logs every argv to `$TMP/ssh.log`. Synthetic lines only, e.g. `{"id":"e1","type":"message","timestamp_ms":1000,"message":{"role":"user"}}` (no `toolResult`, no `__openclaw`, so the forbidden check stays quiet).

Cases:
1. **key file**: after one pass, `$KEY_FILE` exists with mode `600` and contents equal to `PULLER_KEY`; `ssh.log` first line contains `-i $KEY_FILE`.
2. **empty key**: with `PULLER_KEY=` the script exits non-zero before any ssh (`ssh.log` absent), stderr mentions `PULLER_KEY`.
3. **first pass**: sessions `[{"key":"agent:main:a","updatedAt":5000},{"key":"agent:main:heartbeat:x","updatedAt":9000}]`, transcript for `agent:main:a` has events at 1000, 2000. After the pass: `transcripts/agent-main-a.jsonl` has 2 lines; `.cursors.json` is `{"agent:main:a":5001}`; no file for the heartbeat key and `ssh.log` has no `transcript agent:main:heartbeat` line; `.last-pull` exists.
4. **unchanged session**: same listing again → `ssh.log` gains only a `sessions` line, file still 2 lines, cursor still 5001.
5. **grown session**: listing `updatedAt` 7000, transcript now also has an event at 6000 → the `transcript` call carries `--since 5001`, file has 3 lines, cursor 7001.
6. **mid-stream failure**: `fail.agent:main:a` present, listing `updatedAt` 8000 → file still 3 lines, cursor still 7001, exit 0 (one failed session does not kill the pass), `.last-pull` updated.
7. **listing failure**: fake `ssh` exits 1 for `sessions` → `.last-pull` is NOT touched (compare mtime to before, or remove it first and assert absent), exit non-zero.
8. **safe key**: key `agent:main:direct:abc` writes `agent-main-direct-abc.jsonl`.

- [ ] **Step 2: Run it to verify it fails**

Run: `puller/pull.test.sh`
Expected: FAIL (`pull.sh` missing)

- [ ] **Step 3: Implement `puller/pull.sh`**

Structure: `install_key` (refuse empty `PULLER_KEY`, `umask 077`, write `KEY_FILE`, `unset PULLER_KEY`), `list_sessions` (ssh `sessions`, `jq -c '.sessions[] | select(.key | test("heartbeat") | not) | {key, updatedAt}'`), `pull_one key updated` (read cursor with `jq -r --arg k "$key" '.[$k] // 0'`, skip if `updated <= cursor`, ssh `transcript` into `mktemp` in `$TRANSCRIPTS`, on success `cat tmp >> file` and `set_cursor key $((updated+1))`, always `rm -f tmp`), `set_cursor` (jq into a temp file, `mv` over `.cursors.json`), `pass` (list, loop, `date +%s > .last-pull` only if the listing succeeded), `main` (install_key, then `pass` once if `PULL_ONCE`, else loop with `sleep $((PULL_INTERVAL_MINUTES*60))`). `safe_key` is `tr -c 'A-Za-z0-9_-\n' '-'`. Log one line per pulled session to stderr: `pulled <safe-key> since=<ms> bytes=<n>`; never log transcript content. A listing failure returns non-zero from `pass`; in loop mode it's logged and the loop continues.

- [ ] **Step 4: Run the test to verify it passes**

Run: `puller/pull.test.sh` and `shellcheck puller/pull.sh`
Expected: all cases PASS; shellcheck clean.

- [ ] **Step 5: Write `puller/Containerfile`**

`FROM alpine:3`, `apk add --no-cache bash jq openssh-client`, `adduser -D -u 10002 puller`, `COPY pull.sh /usr/local/bin/pull.sh`, `USER puller`, `ENTRYPOINT ["/usr/local/bin/pull.sh"]`. Build check: `docker build -t brined-almanac-puller:smoke puller/ && docker run --rm -e PULLER_KEY= brined-almanac-puller:smoke; echo $?` prints a non-zero code and a `PULLER_KEY` message (case 2 inside the image).

- [ ] **Step 6: Wire shellcheck and the test into mise and hk**

`mise.toml`: `shellcheck = "0.10.0"` under `[tools]` (or the version `mise ls-remote shellcheck` shows as latest 0.10.x); `[tasks.test]` runs `cargo test && puller/pull.test.sh`. `hk.pkl`: `local shellcheck = new Step { check = new CommandSpec { command = "shellcheck puller/pull.sh scripts/check-forbidden scripts/image-smoke"; effect = "read" } }`, added to `pre-commit` and `check`. Run `mise run check`: expected all steps pass.

- [ ] **Step 7: Commit**

```bash
git add puller/ mise.toml hk.pkl
git commit   # subject: Add the transcript puller
```

---

### Task 4: `compose.prod.yaml`, secret references, and the deploy task

**Files:**
- Create: `compose.prod.yaml`
- Create: `prod.secrets.env` (two lines, the `op://` references from Global Constraints)
- Create: `scripts/deploy` (bash, executable)
- Create: `scripts/compose-config-check` (bash; validates the compose file with stub values)
- Modify: `mise.toml` (tasks `deploy`, `prod`), `hk.pkl` (add `scripts/deploy scripts/compose-config-check` to the shellcheck step; add step `compose` running `scripts/compose-config-check` in `check`)
- Modify: `.gitignore` (no change needed; state dir is outside the repo)

**Interfaces:**
- Consumes: `brined-almanac:prod`, `brined-almanac-puller:prod` (Tasks 2, 3); env `BRINED_ALMANAC_STATE`, `BRINED_ALMANAC_GITHUB_TOKEN`, `BRINED_ALMANAC_PULLER_KEY`.
- Produces: `mise run deploy [--db-only]`, `mise run prod -- <verb> [args]`. Task 5 runs them.

- [ ] **Step 1: Write the failing config check**

`scripts/compose-config-check`: makes a temp state dir with `prod.env` (`ALMANAC_MODE=live`, `PULL_HOST=h`, `PULL_USER=u`), empty `known_hosts`, `transcripts/`; then
1. with `BRINED_ALMANAC_STATE=$tmp BRINED_ALMANAC_GITHUB_TOKEN=x BRINED_ALMANAC_PULLER_KEY=y`, `docker compose -f compose.prod.yaml config --format json` succeeds and `jq -e` asserts: `.name == "brined-almanac-prod"`; `.services.db.ports` is null; `.services.almanac.environment.DATABASE_URL == "postgres://almanac@db:5432/almanac"`; `.services.almanac.environment.BRENT_TRANSCRIPTS == "/transcripts"`; the almanac's `/transcripts` mount is read-only; `.services.almanac.ports[0].published == "8788"` and `.host_ip == "127.0.0.1"`; `.services.puller.environment.PULLER_KEY == "y"`; puller has a tmpfs at `/run/puller`.
2. without `BRINED_ALMANAC_GITHUB_TOKEN`, `config` fails and stderr contains `mise run deploy`.
3. without `BRINED_ALMANAC_PULLER_KEY`, same.
Prints `compose-config-check: ok`.

- [ ] **Step 2: Run it to verify it fails**

Run: `scripts/compose-config-check`
Expected: FAIL (no compose.prod.yaml)

- [ ] **Step 3: Write `compose.prod.yaml` and `prod.secrets.env`**

Follow the spec's Compose section verbatim for services, env, mounts, and healthchecks. Healthchecks: `db` `pg_isready -U almanac` (5s/3s/10); `almanac` `curl -fsS http://127.0.0.1:8788/healthz` (interval 30s, start_period 20s); `puller` `test $(( $(date +%s) - $(cat /transcripts/.last-pull 2>/dev/null || echo 0) )) -lt $(( ${PULL_INTERVAL_MINUTES:-10} * 180 ))` (interval 60s, start_period 120s). Both services `env_file: ${BRINED_ALMANAC_STATE:-${HOME}/.local/share/brined-almanac}/prod.env`; use the same default expression for every state-dir path. `prod.secrets.env` is the two reference lines with a two-line comment saying they're references resolved by `op run`.

- [ ] **Step 4: Run the config check to verify it passes**

Run: `scripts/compose-config-check`
Expected: `compose-config-check: ok`

- [ ] **Step 5: Write `scripts/deploy`**

Flags: `--db-only`, `--tag <sha>` (default `git rev-parse --short HEAD`). Steps, each printing one line:
1. Refuse when `git status --porcelain` is non-empty (`deploy: working tree is dirty`), warn when branch isn't `main`.
2. `STATE=${BRINED_ALMANAC_STATE:-$HOME/.local/share/brined-almanac}`; refuse unless `prod.env`, `op-token`, `known_hosts`, and `transcripts/` exist; refuse unless `op-token` mode is `600` (`stat -f %Lp`); print the runbook path on refusal.
3. `docker build -f Containerfile -t brined-almanac:$tag -t brined-almanac:prod .` and the puller the same way.
4. `--db-only`: `docker compose -f compose.prod.yaml up -d --wait db` (no secrets needed; pass dummy values for the `:?` guards via `BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused`, which never reach a started service) and exit.
5. Otherwise `OP_SERVICE_ACCOUNT_TOKEN="$(cat "$STATE/op-token")" op run --env-file prod.secrets.env -- docker compose -f compose.prod.yaml up -d --wait`.
6. `docker compose -f compose.prod.yaml ps` and `curl -fsS http://127.0.0.1:8788/healthz`.
Never `echo` any variable that came from `op run` or `op-token`.

`mise.toml`: `[tasks.deploy] run = "scripts/deploy"` (description: "Build both images and start or update the prod stack; --db-only for the first deploy"), `[tasks.prod] run = "docker compose -f compose.prod.yaml exec almanac brined-almanac"` with `usage` showing `mise run prod -- check | backfill [--dry-run]`.

- [ ] **Step 6: Verify the deploy script's preflight by hand**

With a scratch file in the tree: `scripts/deploy` prints `deploy: working tree is dirty` and exits 1. Clean tree, `BRINED_ALMANAC_STATE=$(mktemp -d) scripts/deploy` exits 1 naming the missing files and the runbook. `shellcheck scripts/deploy scripts/compose-config-check` clean. `mise run check` passes (the new `compose` step included).

- [ ] **Step 7: Commit**

```bash
git add compose.prod.yaml prod.secrets.env scripts/deploy scripts/compose-config-check mise.toml hk.pkl
git commit   # subject: Add the prod compose stack and the deploy task
```

---

### Task 5: Runbook and the first deploy on this Mac

Human steps are marked **(Josh)**. The implementer writes the runbook first, then drives the deploy with Josh, recording what each step printed into the runbook's "First deploy, 2026-11" note.

**Files:**
- Create: `docs/runbooks/prod-on-the-mac.md`
- Modify: `docs/superpowers/specs/2026-11-06-prod-on-the-mac-design.md` (Open questions: record the MagicDNS and `from=` answers once known)

**Interfaces:**
- Consumes: everything above; `openclaw-ro-pull` deployed on picklelab (Task 1 merged and deployed via picklehome).

- [ ] **Step 1: Write the runbook**

Sections: "One-time setup" (1Password vault `brined-almanac`, items `github-token` (field `credential`, a fine-grained PAT, repos brineworks + pickleclaw, Issues write, Metadata read) and `puller` (SSH key item; copy its public key), service account read-only on that vault, `op-token` file with `install -m 600`, `prod.env` contents, `ssh-keyscan picklelab > known_hosts`, the `authorized_keys` line on picklelab for user `technicalpickles` with `restrict,from="<Mac tailnet address>",command="/opt/pickleclaw/scripts/openclaw-ro/openclaw-ro-pull"`), "First deploy" (the db-only → `pg_dump` → full deploy → acceptance order from the spec, with the exact `docker exec brined-almanac-d34bd55b-db-1 pg_dump -U almanac almanac | docker compose -f compose.prod.yaml exec -T db psql -U almanac almanac` line), "Every deploy", "Rollback" (retag + `up -d`; forward-only migrations), "Rotation", "When it breaks" (logs, health, puller unhealthy means picklelab unreachable or the forced command refused), "Backups" (the `pg_dump` line; none scheduled). No tailnet IP, no Telegram id, no key material in the file.

- [ ] **Step 2 (Josh): One-time setup**

Vault, items, service account, `op-token`, `prod.env`, `known_hosts`, the `authorized_keys` line. Confirm `OP_SERVICE_ACCOUNT_TOKEN=$(cat op-token) op read "op://brined-almanac/puller/private key?ssh-format=openssh" | head -1` prints the OpenSSH private key BEGIN marker line (and nothing more is printed).

- [ ] **Step 3: Forced command check from the Mac (spec testing 4)**

With the key in a temp file from `op read` (deleted right after): `ssh -i key technicalpickles@picklelab sessions | jq '.sessions | length'` prints a number; `ssh -i key ... version` exits 2 with empty stdout; `ssh -i key ... transcript agent:main:x --last 5` exits 2; `ssh -i key technicalpickles@picklelab` (interactive) exits 2. Record whether `from=` held (if ssh is refused for `sessions` too, test once without `from=`; record the answer in the spec's open questions).

- [ ] **Step 4: Build and adopt the store (spec testing 1 to 3)**

`mise run image:smoke` → ok. `mise run deploy --db-only`. The `pg_dump | psql` line. `mise run deploy`. `mise run prod -- check` prints `ok`. `mise run prod -- backfill` prints 0 queued and 102 unchanged (record the exact counts). `gh issue list -R technicalpickles/brineworks -l almanac --json number | jq length` still 12 and pickleclaw still 17.

- [ ] **Step 5: Puller pass (spec testing 5)**

`docker compose -f compose.prod.yaml logs puller` shows `pulled ...` lines; `ls $STATE/transcripts` shows per-session files and `.cursors.json`; `docker compose ps` shows puller healthy after one interval. Record whether `PULL_HOST=picklelab` resolved from the container (MagicDNS) or needed the address; put the answer in the spec's open questions.

- [ ] **Step 6: Restart and hygiene (spec testing 6 and 7)**

`docker compose -f compose.prod.yaml restart`, then `mise run prod -- check` ok and a new `pulled`/`sessions` line in puller logs. `grep -rl 'PRIVATE KEY' $STATE; grep -rl 'github_pat_' $STATE` print nothing; `find $STATE -perm 600` prints only `op-token`.

- [ ] **Step 7: Commit the runbook and spec note**

```bash
git add docs/runbooks/prod-on-the-mac.md docs/superpowers/specs/2026-11-06-prod-on-the-mac-design.md
git commit   # subject: Add the prod runbook and record the first deploy
```

---

### Task 6: Main becomes a dev checkout

Only after Task 5 step 4 succeeded (the interim store's rows live in prod).

**Files:**
- Modify: `mise.toml` (`db:down`: drop the `[ -f .git ]` refusal and its message)
- Modify: `README.md` ("Running it" → "Dev" and "Prod" as the spec says)
- Modify: `docs/superpowers/specs/2026-11-06-tool-error-scan-design.md` (Operations: the "v0 runs on the Mac, by hand" bullet now points at the prod spec)
- Modify: `.portool.toml` comment (main no longer special)

- [ ] **Step 1: Edit `db:down`** so it works in any checkout: delete the `[ -f .git ] || { ... }` line and the "interim store" wording in the description. Verify: `grep -c 'interim store' mise.toml README.md` prints 0 for both. Do NOT run `db:down` on main in this task; the old store stays until Josh removes it (`docker compose down -v` in the main checkout, after prod has run for a while).

- [ ] **Step 2: Rewrite README "Running it"**

"Dev": dry runs, per-checkout stores, `mise run live` as a deliberate live run from a checkout (warn it shares nothing with prod and would refile). "Prod": two sentences and a link to `docs/runbooks/prod-on-the-mac.md`. Remove "the main checkout's store is the interim store" everywhere. `mise run check` passes.

- [ ] **Step 3 (Josh): Unpin main's portool block** when ready: `portool unpin` in the main checkout removes the GC protection; `.portool.toml`'s comment drops the sentence about the interim store.

- [ ] **Step 4: Commit**

```bash
git add mise.toml README.md .portool.toml docs/superpowers/specs/2026-11-06-tool-error-scan-design.md
git commit   # subject: Make the main checkout an ordinary dev checkout
```

---

### Task 7: Upstream issue on ivy/almanac

**Files:**
- Create: `docs/superpowers/plans/2026-11-06-upstream-issue-run-as-service.md` (the draft; deleted after posting, or kept as the record)

- [ ] **Step 1: Draft** per the spec's Upstream section, in Josh's voice (`writing-voice` skill): title "A supported way to run an almanac as a service in a container", body with what worked (`SQLX_OFFLINE`, `run` on Postgres alone, `ALMANAC_LISTEN`, `sd_notify`), what was missing (no Containerfile or compose example beside `contrib/systemd`), and an offer to PR a generalized Containerfile once ours has run for a while. No private repo names beyond what's already public, no transcript text.
- [ ] **Step 2 (Josh):** read the draft; post with `gh issue create -R ivy/almanac` only on Josh's go.
