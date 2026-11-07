# Prod on the Mac: brined-almanac as a compose stack

**Status:** design, 2026-11-06. Builds on the tool error scan
(`2026-11-06-tool-error-scan-design.md`), which shipped the `tool-failures`
workflow and left "the daemon, a timer, and reading picklelab's store
directly" to a deployment document. This is that document, for the Mac.

## Goal

Run brined-almanac continuously on the Mac as a Docker Compose stack, fed by
fresh OpenClaw transcripts, so that the main checkout goes back to being a dev
checkout. A deploy is a build and a `compose up`. Nothing in prod is a git
checkout, and nothing in a checkout can reach prod's store.

Non-goals: picklelab as the host (later), reading OpenClaw's SQLite store
directly (the pull stays the source), brineworks container logs (waits on
per-call logging), the OSS-PR workflow (its own spec).

## What already exists

- `brined-almanac run` is a daemon: it connects to Postgres (running its own
  migrations, no broker needed), scans every 15 minutes (`SCAN_INTERVAL` in
  almanac's runtime, `ALMANAC_SCAN_INTERVAL_MINUTES` to override), serves
  `GET /healthz` on `ALMANAC_LISTEN` (default `127.0.0.1:8788`), and stops on
  SIGTERM. `sd_notify` is a no-op outside systemd.
- ivy/almanac ships `crates/almanac/.sqlx`, so `SQLX_OFFLINE=true cargo build`
  needs no database. Upstream has no Containerfile or compose example for
  running an almanac; `contrib/` holds systemd units only.
- `openclaw-ro` on picklelab lists sessions (`sessions`) and streams one
  session's transcript (`transcript <key> --since <epoch-ms|ISO-8601>`), already
  redacted and gated. Today a person runs it through
  `scripts/openclaw-ro/remote` in pickleclaw, which uses `ssh picklelab` with
  the Mac's own identity, signed by Automic Vault. In prod the puller makes
  those same two calls itself, over its own fenced key (see The puller);
  `remote` stays a hand tool and is not on the prod path.
- The reader scans every `*.jsonl` directly in `BRENT_TRANSCRIPTS` (not
  recursive), and deduplicates events by their `id`, so a file that repeats an
  event is read once.
- OrbStack containers reach the tailnet: a throwaway alpine container connected
  to picklelab's port 22 (verified 2026-11-06).
- The main checkout's Postgres (`brined-almanac-d34bd55b-db-1`) holds the 102
  clusters the first live run admitted. Prod must adopt those rows or it files
  the 29 open issues again.

## Layout

Prod state lives outside every checkout at `~/.local/share/brined-almanac/`
(`BRINED_ALMANAC_STATE` overrides it):

| Path | Holds | Mounted |
|---|---|---|
| `transcripts/` | `<safe-key>.jsonl` per session, plus `.cursors.json` and `.last-pull` | `almanac` read-only, `puller` read-write |
| `prod.env` | Non-secret settings: `ALMANAC_MODE=live`, `ALMANAC_SCAN_INTERVAL_MINUTES`, `PULL_INTERVAL_MINUTES`, `PULL_HOST`, `PULL_USER` | `env_file` for both services |
| `op-token` | The 1Password service account token, mode 0600. The only secret on disk. | Read by the deploy task, never mounted |
| `known_hosts` | picklelab's host key, from `ssh-keyscan` at first deploy | `puller` read-only |

The repo gains:

| Path | Purpose |
|---|---|
| `Containerfile` | The almanac image |
| `puller/Containerfile`, `puller/pull.sh` | The puller image and its loop |
| `compose.prod.yaml` | The stack; project name fixed to `brined-almanac-prod` |
| `prod.secrets.env` | `op://` references for the two secrets, resolved by `op run` at deploy. References, not values, so safe in a public repo |
| `mise.toml` tasks `deploy`, `prod` | Build and start; exec a verb in prod |
| `docs/runbooks/prod-on-the-mac.md` | First deploy, rollback, rotation, what to do when it breaks |

`compose.yaml` keeps doing per-checkout dev stores on portool ports. It
doesn't change. The project name `brined-almanac-prod` never collides with a
checkout's `brined-almanac-<hash>`.

## The almanac image

Multi-stage. Builder: `rust:1.99-bookworm`, `SQLX_OFFLINE=true`,
`cargo build --release --locked`, with a dependency layer built from
`Cargo.toml` and `Cargo.lock` against a stub `main.rs` first, so a source edit
doesn't rebuild ivy/almanac. The git dependency resolves inside the build, same
as locally. Runtime: `debian:bookworm-slim` plus `ca-certificates` and `curl`
(for the healthcheck), a non-root `almanac` user, `ENTRYPOINT ["brined-almanac"]`,
`CMD ["run"]`. Tags: `brined-almanac:<short sha>` and `brined-almanac:prod`.

The puller image is `alpine:3` with `openssh-client`, `jq`, `bash`, and
`pull.sh`, same two tags with `-puller`.

## Compose

```yaml
# compose.prod.yaml (shape, not final text)
name: brined-almanac-prod
services:
  db:
    image: postgres:17-alpine
    restart: unless-stopped
    environment: { POSTGRES_USER: almanac, POSTGRES_DB: almanac, POSTGRES_HOST_AUTH_METHOD: trust }
    healthcheck: pg_isready -U almanac
    volumes: [pgdata:/var/lib/postgresql/data]
  almanac:
    image: brined-almanac:prod
    restart: unless-stopped
    depends_on: { db: { condition: service_healthy } }
    env_file: ${BRINED_ALMANAC_STATE}/prod.env
    environment:
      DATABASE_URL: postgres://almanac@db:5432/almanac
      BRENT_TRANSCRIPTS: /transcripts
      ALMANAC_LISTEN: 0.0.0.0:8788
      GITHUB_TOKEN: ${BRINED_ALMANAC_GITHUB_TOKEN:?run `mise run deploy`, which resolves it through op run}
    ports: ["127.0.0.1:8788:8788"]
    volumes: ["${BRINED_ALMANAC_STATE}/transcripts:/transcripts:ro"]
    healthcheck: curl -fsS http://127.0.0.1:8788/healthz
  puller:
    image: brined-almanac-puller:prod
    restart: unless-stopped
    env_file: ${BRINED_ALMANAC_STATE}/prod.env
    environment:
      PULLER_KEY: ${BRINED_ALMANAC_PULLER_KEY:?run `mise run deploy`, which resolves it through op run}
    tmpfs: [/run/puller]
    volumes:
      - "${BRINED_ALMANAC_STATE}/transcripts:/transcripts"
      - "${BRINED_ALMANAC_STATE}/known_hosts:/etc/ssh/ssh_known_hosts:ro"
    healthcheck: .last-pull is younger than 3 × PULL_INTERVAL_MINUTES
volumes:
  pgdata:
```

`db` publishes no port; only the compose network reaches it. Trust auth stays,
as in the dev store: nothing outside the network can connect, and
`DATABASE_URL` carries no credential. The health port is published on loopback
so a person can `curl` it.

## The puller

`pull.sh` starts by writing `$PULLER_KEY` to `/run/puller/key` with mode 0600
and unsetting the variable, then loops every `PULL_INTERVAL_MINUTES` (default
10). One pass:

1. `ssh -i /run/puller/key $PULL_USER@$PULL_HOST sessions`, parse
   `.sessions[] | {key, updatedAt}`. Skip keys containing `heartbeat`.
2. For each session whose `updatedAt` is after its cursor (0 when unknown):
   `ssh ... transcript <key> --since <cursor>`, appended to
   `/transcripts/<safe-key>.jsonl` through a temp file, so a pull that dies
   mid-stream leaves the file as it was. `<safe-key>` is the key with every
   character outside `[A-Za-z0-9_-]` replaced by `-`, the same mapping
   `remote` uses for export directories.
3. On success, set the cursor to the last event's timestamp plus one
   millisecond. On failure, leave it; the next pass retries. Write
   `.last-pull` after every pass that completed, even one with nothing new.

Cursors are `.cursors.json`, a map from key to epoch ms, written atomically.
`--since` is inclusive, so a cursor exactly at the last event would repeat it;
the plus-one avoids that, and the reader's dedup by event id covers any
repeat anyway. Transcript files only grow; rotation is a later concern (two
months of two sessions was 66 MB).

Telegram, direct, and openwebui sessions all come through the same list. No
session key is configured anywhere. File names in `transcripts/` contain
session keys, which can contain Telegram ids; that directory is prod state,
never in a repo.

### The key and the forced command

The puller has its own ed25519 key, a 1Password SSH key item named `puller`
in the `brined-almanac` vault (see Secrets). 1Password generates it and shows
the public half, so no private key is ever on a disk outside a container's
tmpfs. What fences it is picklelab's `authorized_keys` line for the ssh user
`remote` already uses:

```
restrict,from="<the Mac's tailnet address>",command="/opt/pickleclaw/scripts/openclaw-ro/openclaw-ro-pull" ssh-ed25519 <public key> brined-almanac-puller
```

`openclaw-ro-pull` is a new script in pickleclaw next to `openclaw-ro`, so
picklehome's deploy puts it at `/opt/pickleclaw` with the rest. It reads
`SSH_ORIGINAL_COMMAND`, accepts exactly two shapes, and execs `openclaw-ro`
with the parsed words:

- `sessions`
- `transcript <key> --since <13 digits>` where `<key>` matches
  `^[A-Za-z0-9:_.-]+$`

Anything else exits 2 with a one-line reason on stderr and nothing on stdout.
`openclaw-ro` validates again on its own; the wrapper only narrows which
subcommands exist for this key. `restrict` turns off forwarding, pty, and
X11; `from=` limits the key to the Mac's tailnet address. A leaked key then
gets the same redacted transcripts `remote` already pulls, from one address,
until the line is removed.

picklehome doesn't manage `authorized_keys` on picklelab, so the line is a
hand edit recorded in the runbook. The public key is not a secret and may be
quoted there.

## Secrets

1Password supplies both secrets; nothing in prod reads `gh auth token`, and
no token or private key sits in a file. The deploy task resolves them at
deploy time and they exist only in the environment of that one `op run`
process and the containers it creates.

- **Vault** `brined-almanac`, used for nothing else. Two items: `github-token`,
  a fine-grained PAT scoped to `technicalpickles/brineworks` and
  `technicalpickles/pickleclaw` with Issues write and Metadata read; and
  `puller`, an SSH key item whose public key goes in picklelab's
  `authorized_keys`.
- **Service account** with read access to that vault only. Its token is the
  one bootstrap secret, stored at `${BRINED_ALMANAC_STATE}/op-token` with mode
  0600. What that file unlocks is read access to a two-item vault; the PAT is
  scoped and the key is fenced by the forced command. The same token serves a
  later picklelab deployment under systemd (`LoadCredential=`), which is the
  reason for a service account over Automic Vault: av is Mac-only.
- **References in the repo.** `prod.secrets.env` holds
  `BRINED_ALMANAC_GITHUB_TOKEN=op://brined-almanac/github-token/credential` and
  `BRINED_ALMANAC_PULLER_KEY="op://brined-almanac/puller/private key?ssh-format=openssh"`.
  Verified against `op` 2.41.1: `?ssh-format=openssh` returns the key in the
  format `ssh -i` reads.
- **Deploy** runs
  `OP_SERVICE_ACCOUNT_TOKEN=$(cat op-token) op run --env-file prod.secrets.env -- docker compose -f compose.prod.yaml up -d --wait`,
  so compose interpolates both values into the services' environments. A
  `restart: unless-stopped` restart, including OrbStack coming back at login,
  reuses the containers as created, with their environments. `docker inspect`
  on this Mac shows both values, the trade accepted here over files on disk.
- **Rotation** is editing the item in 1Password (or regenerating the SSH key
  and replacing the `authorized_keys` line), then `mise run deploy`.
- If the confirmation step is ever wanted back, the task changes by one line:
  `av inject +OP_SERVICE_ACCOUNT_TOKEN -- op run ...` and the `op-token` file
  goes away.

`prod.env` is therefore non-secret.

## Deploy

`mise run deploy`:

1. Refuse a dirty tree. Warn (don't refuse) when the branch isn't `main`.
2. `sha=$(git rev-parse --short HEAD)`; build both images with the sha tag and
   the `prod` tag.
3. Check `${BRINED_ALMANAC_STATE}` has `prod.env`, `op-token`, and
   `known_hosts`; point at the runbook if not.
4. `op run --env-file prod.secrets.env -- docker compose -f compose.prod.yaml up -d --wait`,
   with the service account token read from `op-token` into the task's
   environment and nowhere else.
5. `docker compose -f compose.prod.yaml ps` and `curl` the health endpoint.

`mise run prod -- <verb>` runs `docker compose -f compose.prod.yaml exec
almanac brined-almanac <verb>`, for an ad hoc `check` or `backfill` against
prod's store with prod's token. A `backfill --dry-run` there is fine: prod's
`seen` already holds everything prod has filed.

Rollback: `docker tag brined-almanac:<older sha> brined-almanac:prod` and
`up -d`. The migrations are forward-only, so rolling back past one that added
a migration is a restore, which the runbook covers by `pg_dump`.

### First deploy adopts the interim store

Order matters, because `run` applies migrations on start and a restore into a
migrated database fails on existing tables:

1. `mise run deploy` builds, then `up -d db` only (the task takes
   `--db-only`, used here and never again).
2. `docker exec brined-almanac-d34bd55b-db-1 pg_dump -U almanac almanac |
   docker compose -f compose.prod.yaml exec -T db psql -U almanac almanac`.
   The dump carries `_sqlx_migrations`, so the almanac's start sees them applied.
3. `mise run deploy` again, now the whole stack.
4. `mise run prod -- backfill` must report 0 queued and every cluster
   unchanged. That line is the acceptance test for the migration.

After that the main checkout's store is an ordinary dev store. The same change
removes the `db:down` refusal on main, unpins its portool block, and rewrites
README "Running it" into "Dev" (dry runs, per-checkout stores, `mise run live`
for a deliberate live run from a checkout) and "Prod" (pointer to the runbook).

## Operations

- Logs: `docker compose -f compose.prod.yaml logs -f almanac` (JSON lines,
  since stderr isn't a terminal) and `... logs -f puller`.
- Health: `curl -f http://127.0.0.1:8788/healthz`; `docker compose ps` shows
  the puller's healthcheck, which fails when `.last-pull` goes stale.
- Scan cadence: `ALMANAC_SCAN_INTERVAL_MINUTES` in `prod.env`, then `up -d`.
- Backups: none yet, same as the interim store. The runbook has the
  `pg_dump` line. Losing the volume refiles everything; that's the known risk
  carried over from the tool error scan spec.
- Key rotation: regenerate the `puller` item in 1Password, replace the line
  on picklelab, `mise run deploy`.

## Testing

Proof, in order. Each step gates the next.

1. The almanac image builds with no network to a database and no `.env`;
   `docker run --rm brined-almanac:prod synth` prints the manifest.
2. `mise run prod -- check` with `ALMANAC_MODE=live` in `prod.env` validates
   the transcripts mount and the GitHub tracker (ivy/almanac#1 means plain
   `check` wouldn't).
3. After the restore: `mise run prod -- backfill` reports 0 queued, 102
   unchanged. Nothing new appears in either repo's `almanac` label.
4. The forced command: with the puller key, `ssh ... sessions` lists sessions;
   `ssh ... transcript <key> --since <ms>` streams events; `ssh ... version`,
   `ssh ... transcript <key> --last 5`, and an interactive `ssh` all exit 2
   with nothing on stdout.
5. One puller pass against picklelab appends to at least one session file,
   advances its cursor, and writes `.last-pull`. A second pass with nothing
   new appends nothing and still writes `.last-pull`.
6. The stack survives `docker compose restart` and an OrbStack restart with
   both secrets still in the containers' environments (`check` passes again,
   the next puller pass succeeds).
7. The state dir contains no private key and no GitHub token: `grep -rl` for
   `PRIVATE KEY` and `github_pat_` under it finds nothing. `op-token` is the
   only file with mode 0600.

`mise run check` gains nothing slow: `docker compose -f compose.prod.yaml
config` with a stub state dir validates the compose file, and shellcheck
covers `pull.sh` and `openclaw-ro-pull`. The wrapper gets a bats-style test in
pickleclaw next to `openclaw-ro.test.sh`, feeding `SSH_ORIGINAL_COMMAND`
values and asserting the exec'd argv or the exit 2. The image build runs in
the deploy task, not the hook.

## Work in other repos

- pickleclaw: `scripts/openclaw-ro/openclaw-ro-pull` and its test. Private
  repo; merges and deploys through picklehome as usual.
- picklelab: one `authorized_keys` line, by hand, recorded in this repo's
  runbook.
- 1Password: the `brined-almanac` vault, its two items, and the service
  account, created by Josh once. The runbook lists the exact item and field
  names the references expect.
- dotfiles: nothing. OrbStack supervises the stack.

## Upstream

File one issue on ivy/almanac: a supported way to run an almanac as a
service, in a container. What we learned that it should mention:
`SQLX_OFFLINE=true` builds work out of the box; `run` needs only Postgres;
`ALMANAC_LISTEN` must be set to a non-loopback address for a container
healthcheck from the outside; `sd_notify` is harmless without systemd; the
scaffold could ship a Containerfile and a compose example next to the systemd
units. Text drafted in the plan, shown to Josh before posting. A PR with this
repo's Containerfile generalized can follow once it has run for a while.

## Open questions

- Does the container resolve `picklelab` by MagicDNS, or does `PULL_HOST` need
  the tailnet address? The first deploy finds out; `PULL_HOST` exists so
  either answer works without a code change.
- Transcript file growth: append-only files are read whole every 15 minutes.
  Fine at tens of MB; revisit when a scan takes longer than a minute.
- Whether to also pull from the dev VM. Not now: dev transcripts are test
  traffic and would file noise.
- `from=` assumes picklelab sees a container's ssh arriving from the Mac's
  tailnet address, which is how OrbStack NATs container traffic. Testing step
  4 proves or disproves it; if it fails, drop `from=` and keep `restrict` and
  the forced command.
- A separate picklelab user for the pulls, owning only the puller's key and
  the docker group membership `openclaw-ro` needs, would keep the forced
  command off the interactive user's `authorized_keys`. Worth doing once this
  runs; not needed to start.
