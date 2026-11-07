# Prod on the Mac

How to set up, deploy, and operate the brined-almanac prod stack on the Mac.
The design and its reasons are in
`docs/superpowers/specs/2026-11-06-prod-on-the-mac-design.md`.

The stack is the compose project `brined-almanac-prod`, defined in
`compose.prod.yaml`, with three services:

- `db` is Postgres.
- `almanac` is the daemon.
- `puller` copies transcripts from picklelab.

State lives in `~/.local/share/brined-almanac/`. Set `BRINED_ALMANAC_STATE` to
move it. The commands below call that directory `$STATE`. They assume bash or zsh, run from the repo root:

```sh
STATE=${BRINED_ALMANAC_STATE:-$HOME/.local/share/brined-almanac}
```

## Gotchas for hand-typed compose commands

`mise run deploy` and `mise run prod` handle both traps. Read this section
before you type a `docker compose` command yourself.

1. In a mise-activated shell, `COMPOSE_PROJECT_NAME` is set to the dev project
   name. A bare `docker compose -f compose.prod.yaml ...` then targets the wrong
   project. Put `env -u COMPOSE_PROJECT_NAME` in front.
2. `compose.prod.yaml` refuses to load unless `BRINED_ALMANAC_GITHUB_TOKEN` and
   `BRINED_ALMANAC_PULLER_KEY` are set. For any subcommand other than `up`, put
   `BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused` in
   front. Never do this with `up`: it creates containers that hold the unused
   values. `up` goes through `mise run deploy` only.

Every hand-typed `docker compose` command in this runbook carries that prefix.

## One-time setup

Do this once, before the first deploy. The four steps in 1Password come first.

### 1Password

1. Create a vault named `brined-almanac`. Put nothing else in it.
2. Create an item `github-token` with a field `credential`. The value is a
   fine-grained personal access token with these settings:
   - Repositories: `technicalpickles/brineworks` and
     `technicalpickles/pickleclaw`.
   - Issues: write.
   - Metadata: read.
3. Create an SSH key item named `puller`, with a new ed25519 key. 1Password
   generates the key. Copy the public key for the `authorized_keys` step below.
4. Create a service account with read access to the `brined-almanac` vault and
   nothing else. Copy its token.

`prod.secrets.env` in the repo holds the `op://` references to these items.
The item and field names above must match it.

### State directory

Create the directory. Copy the token from step 4 in 1Password, then write it
from the clipboard so it never appears on a command line:

```sh
mkdir -p "$STATE/transcripts"
(umask 077; pbpaste > "$STATE/op-token")
chmod 600 "$STATE/op-token"
```

To paste into the terminal instead, use `read -rs token` and then
`(umask 077; printf '%s' "$token" > "$STATE/op-token"); unset token`. Never put
the token itself in a command.

Write `$STATE/prod.env`:

```sh
ALMANAC_MODE=live
PULL_HOST=picklelab
PULL_USER=technicalpickles
```

- `ALMANAC_MODE=live` is required. Without it, nothing files.
- `PULL_HOST` is the name the puller container uses for picklelab. If the
  container cannot resolve `picklelab`, set it to picklelab's tailnet address
  (`tailscale ip -4 picklelab` prints it). On the Mac the CLI may not be on
  `PATH`. It lives at `/Applications/Tailscale.app/Contents/MacOS/Tailscale`.
  The puller checks host keys strictly and `known_hosts` is keyed by hostname,
  so also run `ssh-keyscan <address> >> "$STATE/known_hosts"` after the
  "Record picklelab's host key" step below. Keep the
  address out of the repo.
- `ALMANAC_SCAN_INTERVAL_MINUTES` is optional. The default is 15.
- `PULL_INTERVAL_MINUTES` is optional. The default is 10.

Record picklelab's host key:

```sh
ssh-keyscan picklelab > "$STATE/known_hosts"
```

Check that the service account can read the private key:

```sh
OP_SERVICE_ACCOUNT_TOKEN=$(cat "$STATE/op-token") op read "op://brined-almanac/puller/private key?ssh-format=openssh" | head -1
```

The output is one line, the OpenSSH private key header. If you see more, or an
error, stop and fix the item or the service account first.

### The authorized_keys line on picklelab

picklehome does not manage `authorized_keys` on picklelab, so this is a hand
edit. Add one line to `~technicalpickles/.ssh/authorized_keys` on picklelab:

```text
restrict,from="<the Mac's tailnet address>",command="/opt/pickleclaw/scripts/openclaw-ro/openclaw-ro-pull" ssh-ed25519 <public key from the puller item> brined-almanac-puller
```

- `command=` runs `openclaw-ro-pull`, which ships with pickleclaw through
  picklehome's deploy. It accepts only `sessions` and
  `transcript <key> --since <epoch-ms>`. Anything else exits 2.
- `restrict` turns off forwarding, pty, and X11.
- `from=` limits the key to the Mac's tailnet address. Get it with
  `tailscale ip -4` on the Mac (same `PATH` caveat as above). If a container's ssh
  does not arrive from that address, the puller is refused. Drop `from=` and
  keep the rest.

Check the fence from the Mac. Write the key to a temporary file, test, and
delete it:

```sh
key=$(mktemp)
OP_SERVICE_ACCOUNT_TOKEN=$(cat "$STATE/op-token") op read "op://brined-almanac/puller/private key?ssh-format=openssh" > "$key"
ssh -i "$key" -o IdentitiesOnly=yes -o BatchMode=yes technicalpickles@picklelab sessions | jq '.sessions | length'
ssh -i "$key" -o IdentitiesOnly=yes -o BatchMode=yes technicalpickles@picklelab version; echo "exit $?"
rm -f "$key"
```

`sessions` prints a number. `version` prints nothing and exits 2. If `sessions`
is refused with a permission error, either `from=` does not match the Mac's
address or the `authorized_keys` line holds the wrong public key. Test once
without `from=` to tell which.

## First deploy

The first deploy adopts the interim store from the main checkout's container,
`brined-almanac-d34bd55b-db-1`. The order matters. `run` applies migrations on
start, and a restore into a migrated database fails on existing tables.

1. Build the images and start only the database:

   ```sh
   mise run deploy --db-only
   ```

2. Make sure the interim database is running, then copy its store into prod's
   database. Both commands stop on the first error:

   ```sh
   docker start brined-almanac-d34bd55b-db-1
   set -o pipefail
   docker exec brined-almanac-d34bd55b-db-1 pg_dump -U almanac almanac | env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml exec -T db psql -v ON_ERROR_STOP=1 -U almanac almanac
   ```

   `docker start` does nothing if the container is already up. The dump carries
   `_sqlx_migrations`, so the almanac sees its migrations as applied on start.

   A failed dump or restore must not pass quietly. If either command exits
   non-zero, stop here and fix it.

3. Compare the two databases. Both pairs of counts must be equal:

   ```sh
   for q in 'select count(*) from seen' 'select count(*) from _sqlx_migrations'; do
     docker exec brined-almanac-d34bd55b-db-1 psql -U almanac almanac -Atc "$q"
     env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml exec -T db psql -U almanac almanac -Atc "$q"
   done
   ```

   The first two lines are `seen` (interim, then prod). The last two are
   `_sqlx_migrations`. If either pair differs, the deploy stops here. Do not run
   step 4. The restore is wrong, and the daemon would refile every cluster.

4. Deploy the whole stack:

   ```sh
   mise run deploy
   ```

   The daemon's first scan is immediate, and the puller pulls every session
   from the start.

5. Confirm with a live backfill:

   ```sh
   mise run prod -- backfill
   ```

   It must report 0 queued and every cluster unchanged. This is the final
   confirmation after the count comparison, not a probe. If the restore had
   silently failed, this run would file every cluster again. A different result
   means something is still wrong. Do not leave the stack running in live mode
   until you know why. To stop the daemon:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml stop almanac
   ```

6. Confirm the connection:

   ```sh
   mise run prod -- check
   ```

   It prints `ok`.

After this, the main checkout's database is an ordinary dev store.

## Every deploy

1. Commit your change. `mise run deploy` refuses a dirty tree and warns when
   the branch is not `main`.
2. Run:

   ```sh
   mise run deploy
   ```

The task checks that `$STATE` has `prod.env`, `op-token` (mode 600),
`known_hosts`, and `transcripts/`. It builds both images with the short commit
SHA and the `prod` tag. It runs `docker compose up -d --wait` under one
`op run`, then prints `ps` and the `/healthz` response.

To run a verb against prod's store with prod's token:

```sh
mise run prod -- check
mise run prod -- backfill
```

Do not run `backfill --dry-run` against prod. A dry run still admits every
delivery id into `seen`, so the filings a real run would make are suppressed
for good.

The `--` hands the verb and its arguments to `brined-almanac` in the running
`almanac` container. Argument pass-through is not yet confirmed. If a flag does
not arrive, run the `exec` by hand with `env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml exec almanac brined-almanac <verb>`.

To change `prod.env`, edit it and run `mise run deploy`.

## Rollback

Every deploy tags the images with the commit SHA, so an older build is already
on the Mac. Find the SHA with `docker images brined-almanac`.

1. Point `prod` at the older images:

   ```sh
   docker tag brined-almanac:<older sha> brined-almanac:prod
   docker tag brined-almanac-puller:<older sha> brined-almanac-puller:prod
   ```

2. Recreate the containers. This is the one hand-typed `up`, and it needs the
   real secrets, so it goes through `op run`:

   ```sh
   OP_SERVICE_ACCOUNT_TOKEN=$(cat "$STATE/op-token") op run --env-file prod.secrets.env -- env -u COMPOSE_PROJECT_NAME docker compose -f compose.prod.yaml up -d --wait
   ```

Migrations are forward-only. If the newer build ran a migration that the older
build does not know, the rollback fails or misbehaves. In that case, restore
a dump taken before the migration (see Backups). A restore into a migrated
database fails on existing tables, so recreate the database first. This uses
`$STATE/backups/almanac-<date>.sql`; pick the dump you want.

1. Stop the almanac container:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml stop almanac
   ```

2. Drop and recreate the database. This keeps the volume:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml exec db dropdb -U almanac --force almanac
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml exec db createdb -U almanac almanac
   ```

3. Restore the dump:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml exec -T db psql -v ON_ERROR_STOP=1 -U almanac almanac < "$STATE/backups/almanac-<date>.sql"
   ```

4. Retag the older images and recreate the containers through `op run`, as in
   the two steps at the top of this section.

Do not run `mise run deploy` to roll back. It rebuilds from the current commit
and moves `prod` back.

## Rotation

- **GitHub token.** Edit the `credential` field of `github-token` in
  1Password. Run `mise run deploy`.
- **Puller key.** Regenerate the SSH key in the `puller` item. Replace the
  `authorized_keys` line on picklelab with the new public key. Run
  `mise run deploy`.
- **Service account token.** Create a new token in 1Password, then overwrite
  the file:

  ```sh
  (umask 077; pbpaste > "$STATE/op-token")
  chmod 600 "$STATE/op-token"
  ```

  Copy the new token from 1Password first. The `read -rs` flow from "State
  directory" works too.

  The next `mise run deploy` uses it.

The containers hold the secrets in their environment as created. A rotated
value reaches them only when `mise run deploy` recreates them.

## When it breaks

Start with the stack's state and the health endpoint:

```sh
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml ps
curl -f http://127.0.0.1:8788/healthz
```

Read the logs. The almanac logs JSON lines.

```sh
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml logs -f almanac
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml logs -f puller
```

### The puller is unhealthy

The healthcheck fails when `$STATE/transcripts/.last-pull` is older than three
times `PULL_INTERVAL_MINUTES`. That means one of three things:

- picklelab is unreachable. If the log shows a name-resolution error, set
  `PULL_HOST` in `prod.env` to picklelab's tailnet address (`tailscale ip -4
  picklelab`), run `ssh-keyscan <address> >> "$STATE/known_hosts"`, and run
  `mise run deploy`. Without the `known_hosts` entry, the strict host-key check
  fails.
- The forced command refused the request. The log shows the one-line reason
  from `openclaw-ro-pull`. Check that picklelab has the current pickleclaw
  deploy.
- picklelab rejected the key or the source address. Check the
  `authorized_keys` line against the `puller` item, and drop `from=` to test.

`docker compose logs puller` (with the prefix) shows which.

### The almanac is unhealthy or files nothing

- Run `mise run prod -- check`. A failure here points at the database or the
  GitHub token.
- If nothing files, check that `prod.env` has `ALMANAC_MODE=live`.
- If `mise run deploy` fails on a `:?` message, you ran `up` by hand. Use
  `mise run deploy`.

### Files in the state directory

The puller writes these under `$STATE/transcripts/`:

- `<safe-key>.jsonl`, one per session.
- `.cursors.json`, mode 600, owned by the puller's uid 10002. The daemon
  ignores dotfiles.
- `.last-pull`, the time the last completed pass ended.

The state directory must hold no private key and no GitHub token. To check:

```sh
grep -rl 'PRIVATE KEY' "$STATE"
grep -rl 'github_pat_' "$STATE"
```

Both print nothing.

## Backups

None are scheduled. Losing the `pgdata` volume refiles every issue. To take a
dump by hand:

```sh
mkdir -p "$STATE/backups"
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused docker compose -f compose.prod.yaml exec -T db pg_dump -U almanac almanac > "$STATE/backups/almanac-$(date +%F).sql"
```

Take one before any deploy that adds a migration. The dump lives in the state
directory, outside the repo.

## First deploy, 2026-11

The controller records here what each step printed.
