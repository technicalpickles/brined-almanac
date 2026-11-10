# Prod on the Mac

How to set up, deploy, and operate the brined-almanac prod stack on the Mac.
The design and its reasons are in
`docs/superpowers/specs/2026-11-06-prod-on-the-mac-design.md`.

The stack is the compose project `brined-almanac-prod`, defined in
`compose.prod.yaml`, with four services:

- `db` is Postgres.
- `almanac` is the daemon.
- `puller` copies transcripts from picklelab.
- `backup` dumps the store once a day.

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
2. `compose.prod.yaml` refuses to load unless eight values are set:
   `BRINED_ALMANAC_GITHUB_TOKEN`, `BRINED_ALMANAC_GITHUB_ALERTS_TOKEN`,
   `BRINED_ALMANAC_PULLER_KEY`, `BRINED_ALMANAC_TIERS_SHA256`,
   `BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID`,
   `BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET` and
   `BRINED_ALMANAC_BUGSINK_DSN` and `BRINED_ALMANAC_BUGSINK_API_TOKEN`. For any
   subcommand other than `up`, put all eight in front with the value `unused`:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml ps
   ```

   Never do this with `up`: it creates containers that hold the unused
   values. `up` goes through `mise run deploy` only.

Every hand-typed `docker compose` command in this runbook carries that prefix.

## One-time setup

Do this once, before the first deploy. The seven steps in 1Password come first.

### 1Password

1. Create a vault named `brined-almanac`. Put nothing else in it.
2. Create an item `github-token` with a field `token`. The value is a
   fine-grained personal access token with these settings:
   - Repositories: `technicalpickles/brineworks` and
     `technicalpickles/pickleclaw`.
   - Issues: write.
   - Metadata: read.
3. Create an item `github-alerts-token` with a field `token`. The value is a
   fine-grained personal access token for the security-alerts scan, which only
   reads. Settings:
   - Repositories: all repositories, so adding a repository to a tiers file
     never needs a token edit.
   - Dependabot alerts: read.
   - Secret scanning alerts: read.
   - Administration: read.
   - Metadata: read.

   Check the permissions before saving it: `GH_TOKEN=<new token> gh api -i
   repos/technicalpickles/dotfiles/vulnerability-alerts` answers 204. If it
   answers 403, the Administration permission is missing. Then check
   `repos/technicalpickles/bb-plugin-taskwarrior/secret-scanning/alerts`
   answers 200. Use a repository with secret scanning on: one with it off
   answers 404 before GitHub checks the token, so it passes without the
   permission. A 403 there means Secret scanning alerts is missing, which is
   easy to do by picking Code scanning alerts, the entry next to it. Without this token in
   `GITHUB_ALERTS_TOKEN` the scan prints a skip message and finds nothing, but
   compose refuses to start prod without it.
4. Create an SSH key item named `puller`, with a new ed25519 key. 1Password
   generates the key. Copy the public key for the `authorized_keys` step below.
5. Create a service account with read access to the `brined-almanac` vault and
   nothing else. Copy its token.
6. Create an item `taskchampion` with fields `client_id` and
   `encryption_secret`. Copy both values from picklehome's item
   `op://picklehome/TaskChampion Sync`. A `client_id` names a task history, not
   a device, so the container shares the Mac's history. The two items hold the
   same values. A rotation touches both, and rotating the encryption secret
   re-initialises every replica.
7. Create an item `bugsink` with a field `dsn`: the DSN of the
   `brent-tool-failures` project in Bugsink, from the project's settings page.
   The DSN's host is a tailnet name, so it stays in the vault and out of the
   repo. Every scan sends the transcripts' new failed tool calls there as
   events (see "Bugsink gets no events" below). Give the item a second field,
   `api_token`: an API token that can read issues and their events in that
   project, and comment on, resolve and mute them. The same scan reads the
   project's open issues back with it and files a GitHub issue for each one
   that nobody has resolved or muted (see "Bugsink issues do not file" below).
   It also resolves or mutes an issue whose almanac issue was closed (see
   "Closed issues do not resolve" below).

`prod.secrets.env` in the repo holds the `op://` references to these items.
The item and field names above must match it.

The vault also holds one document that is not a secret: `tiers.toml`, the
tier declarations for private repositories that nothing public names
(created 2026-11-08). Its working copy is the gitignored `local/tiers.toml`.
Every `mise run deploy` fetches it with `op document get` into
`$STATE/tiers.toml` (mode 600), and compose mounts that file read-only into the
almanac container, where `LOCAL_TIERS` points at it. The daemon refuses to start
if the file is missing or does not parse. Deploy also passes the file's SHA-256
to compose as an environment value, which is what makes a redeploy pick up a
vault edit. To change it, edit `local/tiers.toml`, then from the repository
root save it and redeploy:

```sh
op document edit tiers.toml local/tiers.toml --vault brined-almanac
mise run deploy
```

On a fresh checkout, restore the working copy first:

```sh
op document get tiers.toml --vault brined-almanac --output local/tiers.toml
```

### State directory

Create the directory. Copy the token from step 5 in 1Password, then write it
from the clipboard so it never appears on a command line:

```sh
mkdir -p "$STATE/transcripts" "$STATE/backups"
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
TASKCHAMPION_SYNC_URL=https://taskchampion.<tailnet>.ts.net
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
- `TASKCHAMPION_SYNC_URL` is the taskchampion sync server. Put the real host
  in place of `<tailnet>`. It is not a secret, so it lives here and not in
  1Password. With it set, the almanac files tasks through its replica in the
  `taskchampion` volume.
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
ssh -n -F /dev/null -o IdentityAgent=none -i "$key" -o IdentitiesOnly=yes -o BatchMode=yes technicalpickles@picklelab sessions | jq '.sessions | length'
ssh -n -F /dev/null -o IdentityAgent=none -i "$key" -o IdentitiesOnly=yes -o BatchMode=yes technicalpickles@picklelab version; echo "exit $?"
rm -f "$key"
```

`-F /dev/null` and `IdentityAgent=none` matter. Without them, an `IdentityFile`
or agent from your ssh config is offered after the puller key, so a refused
puller key falls back to your own key and the check tests the wrong thing
(the symptom is `bash: sessions: command not found`).

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
   docker exec brined-almanac-d34bd55b-db-1 pg_dump -U almanac almanac | env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml exec -T db psql -v ON_ERROR_STOP=1 -U almanac almanac
   ```

   `docker start` does nothing if the container is already up. The dump carries
   `_sqlx_migrations`, so the almanac sees its migrations as applied on start.

   A failed dump or restore must not pass quietly. If either command exits
   non-zero, stop here and fix it.

3. Compare the two databases. Both pairs of counts must be equal:

   ```sh
   for q in 'select count(*) from seen' 'select count(*) from _sqlx_migrations'; do
     docker exec brined-almanac-d34bd55b-db-1 psql -U almanac almanac -Atc "$q"
     env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml exec -T db psql -U almanac almanac -Atc "$q"
   done
   ```

   The first two lines are `seen` (interim, then prod). The last two are
   `_sqlx_migrations`. If either pair differs, the deploy stops here. Do not run
   step 4. The restore is wrong, and the daemon would refile every issue.

4. Deploy the whole stack:

   ```sh
   mise run deploy
   ```

   The puller pulls every session from the start, so the deploy waits several
   minutes for its first pass. The daemon is live from the moment it starts,
   and its first scan is immediate. On a first deploy the transcripts are
   still empty then, but its next scan comes 15 minutes later. As soon as the
   deploy returns, stop the daemon:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml stop almanac
   ```

   Then check the issue counts in both repos' `almanac` label. A scan during
   the deploy may already have filed.

5. Preview on a copy (below). The live `unchanged` will equal the restored
   live ids, and the new issues in the preview are what files. If the
   restore had silently failed, every issue would show as new. Do not go on
   until the numbers make sense.

6. Start the daemon. Its first scan files what the preview showed:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml start almanac
   ```

   Then confirm with a live backfill:

   ```sh
   mise run prod -- backfill
   ```

   It reports 0 queued, and `unchanged` is every issue the scan found.
   Anything queued means the scan and the preview disagree. Stop the daemon
   again and find out why.

7. Confirm the connection:

   ```sh
   mise run prod -- check
   ```

   It prints `ok`.

After this, the main checkout's database is an ordinary dev store.

### Preview a backfill on a copy

Copy prod's database into a throwaway Postgres and run the backfill there with
no `GITHUB_TOKEN` and no TaskChampion settings. Without them the almanac uses a
tracker that only prints, so nothing can file, whatever the mode. The run does
get the read-only `GITHUB_ALERTS_TOKEN` and the private tiers file, so
security-alerts scans the same repositories prod does. A dry run against prod's own database
would be harmless to `seen`, since 416e8e6 keeps dry-run rows under their own
almanac name, but it spends prod's run budget, so use the copy.

```sh
docker network create ba-preview
docker run -d --name ba-preview-db --network ba-preview -e POSTGRES_USER=almanac -e POSTGRES_DB=almanac -e POSTGRES_HOST_AUTH_METHOD=trust postgres:17-alpine
until docker exec ba-preview-db pg_isready -U almanac -q; do sleep 1; done
(set -o pipefail; docker exec brined-almanac-prod-db-1 pg_dump -U almanac almanac | docker exec -i ba-preview-db psql -q -v ON_ERROR_STOP=1 -U almanac almanac)
OP_SERVICE_ACCOUNT_TOKEN=$(cat "$STATE/op-token") op run --env-file prod.secrets.env -- sh -c 'GITHUB_ALERTS_TOKEN=$BRINED_ALMANAC_GITHUB_ALERTS_TOKEN exec "$@"' sh docker run --rm --network ba-preview -e DATABASE_URL=postgres://almanac@ba-preview-db:5432/almanac -e BRENT_TRANSCRIPTS=/transcripts -v "$STATE/transcripts:/transcripts:ro" -e GITHUB_ALERTS_TOKEN -e LOCAL_TIERS=/etc/brined-almanac/tiers.toml -v "$STATE/tiers.toml:/etc/brined-almanac/tiers.toml:ro" brined-almanac:prod backfill > "$STATE/preview.out"
```

`op run` resolves every secret in `prod.secrets.env`, but the container gets
only the alerts token. `-e GITHUB_ALERTS_TOKEN` with no value passes it from the
environment, so it never appears on a command line. The
first deploy with security-alerts files its whole backlog: one task per
repository with overdue alerts, plus one coverage task. This preview is where
to look at that backlog before it files.

A dry run records ids under the almanac name `brined-almanac.dry-run`, apart
from the live rows, so its own summary reports every issue as queued. The
live answer is the difference:

```sh
docker exec ba-preview-db psql -U almanac almanac -Atc "select delivery_id from seen where almanac = 'brined-almanac.dry-run' and delivery_id not in (select delivery_id from seen where almanac = 'brined-almanac') order by 1"
```

Not every new issue files. The `WouldApply` lines in `$STATE/preview.out` are what
files; the ones whose title names a new issue file on the live run. Then
clean up:

```sh
docker rm -f ba-preview-db && docker network rm ba-preview && rm "$STATE/preview.out"
```

## Every deploy

1. Commit your change. `mise run deploy` refuses a dirty tree and warns when
   the branch is not `main`.
2. Run:

   ```sh
   mise run deploy
   ```

The task checks that `$STATE` has `prod.env`, `op-token` (mode 600),
`known_hosts`, and `transcripts/`, then fetches `tiers.toml` from the vault.
It refuses while prod's `pending` table holds events: a binary with a different payload shape fails them, and their
delivery ids stay seen, so they never run
([ivy/almanac#10](https://github.com/ivy/almanac/issues/10)). Let them run on
the current binary first, or pass `--allow-pending` when you know the payload
shape did not change. It then builds both images with the short commit SHA and
the `prod` tag, runs `docker compose up -d --wait` under one `op run`, and
then runs `brined-almanac sync-check` in the almanac container. That pulls
the taskchampion server's whole history into a throwaway replica, so a wrong
sync URL, client id or encryption secret fails the deploy instead of the
first filing. It writes nothing to the server. An empty history also fails,
because nothing was decrypted. The stack stays up when it fails. Last, the
task prints `ps` and the `/healthz` response.

To run a verb against prod's store with prod's token:

```sh
mise run prod -- check
mise run prod -- backfill
```

The `--` hands the verb and its arguments to `brined-almanac` in the running
`almanac` container. Argument pass-through is not yet confirmed. If a flag does
not arrive, run the `exec` by hand with `env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml exec almanac brined-almanac <verb>`.

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
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml stop almanac
   ```

2. Drop and recreate the database. This keeps the volume:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml exec db dropdb -U almanac --force almanac
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml exec db createdb -U almanac almanac
   ```

3. Restore the dump:

   ```sh
   env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml exec -T db psql -v ON_ERROR_STOP=1 -U almanac almanac < "$STATE/backups/almanac-<date>.sql"
   ```

4. Retag the older images and recreate the containers through `op run`, as in
   the two steps at the top of this section.

Never start the daemon on an empty store while issues or tasks filed by the
old one exist. Restore the dump first. Run ids come from a Postgres sequence,
so a fresh store starts again at `run-1`, and a tracker's retry check can then
skip a new finding as already filed.

Do not run `mise run deploy` to roll back. It rebuilds from the current commit
and moves `prod` back.

## Rotation

- **GitHub token.** Edit the `token` field of `github-token` in
  1Password. Run `mise run deploy`.
- **GitHub alerts token.** Edit the `token` field of `github-alerts-token` in
  1Password, then check it as in the setup step. Run `mise run deploy`.
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
- **Bugsink DSN.** Create a new DSN on the `brent-tool-failures` project in
  Bugsink, put it in the `bugsink` item's `dsn` field, and run
  `mise run deploy`. Delete the old DSN in Bugsink after the next scan sends.
- **Bugsink API token.** Create a new token in Bugsink, put it in the
  `bugsink` item's `api_token` field, and run `mise run deploy`. Delete the old
  token in Bugsink after the next scan reads.
- **Taskchampion `client_id` or `encryption_secret`.** The same two values live
  in the `taskchampion` item here and in picklehome's `TaskChampion Sync`
  item, and the Mac's `taskrc` reads the picklehome one. Change both items to
  the same new value, then run `mise run deploy`. Its `sync-check` step proves
  the container can read the history. Rotating the `encryption_secret`
  re-initialises every replica, including the Mac's, because the history on
  the server is encrypted with the old secret.

The containers hold the secrets in their environment as created. A rotated
value reaches them only when `mise run deploy` recreates them.

## When it breaks

Start with the stack's state and the health endpoint:

```sh
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml ps
curl -f http://127.0.0.1:8788/healthz
```

Read the logs. The almanac logs JSON lines.

```sh
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml logs -f almanac
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml logs -f puller
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

- Look for `scheduled scan finished` in the almanac log. The daemon writes one
  every 15 minutes, with `found`, `queued` and `unchanged` counts, so a quiet
  log means the daemon is not scanning, not that nothing is new.
- Run `mise run prod -- check`. A failure here points at the database or the
  GitHub token.
- If nothing files, check that `prod.env` has `ALMANAC_MODE=live`.
- Run `mise run prod -- sync-check` to test the taskchampion side alone. It
  prints `sync ok, pulled N task(s)` or `Failed to synchronize with server`.
  The failure does not say why. A wrong `client_id`, a wrong
  `encryption_secret` and an unreachable host all read the same, so compare
  the container's values against the Mac's (hash them, never print them) and
  `curl` the `TASKCHAMPION_SYNC_URL` host from inside the container.
- To prove the other direction, that the Mac can read what the almanac
  writes, run `scripts/live-interop` from a checkout. It files one probe task
  into the live history. Run `task sync` on the Mac, find it with
  `task +almanac "description.has:interop" list`, then delete it. Used once on
  2026-11-08: the Mac read the task with `almanac_key` and its annotation
  intact.
- If `up --wait` fails, read the almanac container's logs
  (`docker compose -f compose.prod.yaml logs almanac` with the usual prefix).
  A half `TASKCHAMPION_*` configuration panics at startup for every verb, so
  the daemon crash-loops and `mise run prod -- check` cannot run. A missing
  variable named there means `prod.env` or the `taskchampion` item in
  1Password is incomplete. A missing `TASKCHAMPION_SYNC_URL` with the secrets
  present fails the same way, and `scripts/deploy` refuses it before building.
- If `mise run deploy` fails on a `:?` message, you ran `up` by hand. Use
  `mise run deploy`.

### Bugsink gets no events

Each scan prints one `transcript-shipper:` line to the daemon's log:

```sh
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml logs almanac | grep transcript-shipper
```

- `sent N of M event(s)` is a healthy scan. The first scan after a deploy
  sends all history; later scans send the last hour again plus anything new,
  and Bugsink stores a repeated event once.
- `Bugsink unreachable` means the container cannot reach the DSN's host.
  The host is a tailnet name, so check the Mac's Tailscale first.
- `Bugsink answered 403` or `401` means the DSN is wrong or was deleted in
  Bugsink. Rotate it as in "Rotation".
- A failure stops that scan's send and keeps the cursor at the last event
  Bugsink accepted, so the next scan picks up where it stopped. It never
  fails the scan.

The cursor is a file in the `bugsink` volume. Removing the volume resends all
history on the next scan, which costs traffic and nothing else.

### Bugsink issues do not file

Each scan prints a `bugsink-issues:` line when it skips:

- `BUGSINK_API_TOKEN or BUGSINK_DSN is not set, skipping` means the container
  has no token or no DSN. Check the `bugsink` item's `dsn` and `api_token`
  fields, then run `mise run deploy`.
- `skipping <issue>: no <name> tag` means a Bugsink issue the shipper did not
  create. Nothing files for it.

A read that Bugsink refuses fails the scan, and the scan's error names the
read and the status. A `401` or `403` means the token is wrong or was deleted.
An issue files once its event count reaches its class's threshold, so a quiet
issue stays unfiled until it grows. Muting an issue in Bugsink stops its
filing, and resolving it does too until it regresses.

### Closed issues do not resolve

Each scan reads the almanac issues in both repositories before it files, using
`GITHUB_TOKEN`. A Bugsink issue is acted on when its newest almanac issue is
closed and was filed for the issue's current regression count:

- Closed as completed: the scan comments on the Bugsink issue with a link to the
  close, then resolves it.
- Closed as not planned: the scan comments, then mutes it until 50 events arrive
  within a day. `MUTE_WAKE_EVENTS` in `src/resolve_on_close.rs` sets the 50.
  The scan does not mute it again after it wakes.
- Closed with no reason, or reopened: nothing changes.

The scan prints a `bugsink-issues:` line when it cannot:

- `GITHUB_TOKEN is not set, not resolving closed issues` means the container has
  no token. Check `BRINED_ALMANAC_GITHUB_TOKEN`, then run `mise run deploy`.
- `not resolving closed issues: GitHub ... answered <status>` means the read
  failed. Filing goes on, and the next scan tries again.
- `resolve <issue>: answered <status>`, or the same for `mute-until`, means
  Bugsink refused. A `401` or `403` means the token lacks the right to comment,
  resolve or mute. The next scan tries again.

A dry run prints `dry run, would resolve <issue> for <url>` and changes nothing.
`scripts/bugsink-dry-run` shows what the next live scan would do.

### Files in the state directory

The puller writes these under `$STATE/transcripts/`:

- `<safe-key>.jsonl`, one per session.
- `.cursors.json`, mode 600. Inside the container the puller's uid 10002 owns
  it; on the Mac, OrbStack shows it as your user. The daemon ignores dotfiles.
- `.last-pull`, the time the last completed pass ended.

The state directory must hold no private key and no GitHub token. To check:

```sh
grep -rl 'PRIVATE KEY' "$STATE"
grep -rl 'github_pat_' "$STATE"
```

Both print nothing.

## Backups

Losing the `pgdata` volume refiles every issue. The `backup` service writes
`$STATE/backups/almanac-<date>.sql` when it starts and every 24 hours after,
and deletes dumps older than 14 days. Its healthcheck fails when no dump is
younger than 25 hours. The dumps hold event payloads, which carry transcript
text, so they stay in the state directory, outside the repo, like the
transcripts themselves.

To take a dump by hand, before a deploy that adds a migration:

```sh
env -u COMPOSE_PROJECT_NAME BRINED_ALMANAC_GITHUB_TOKEN=unused BRINED_ALMANAC_GITHUB_ALERTS_TOKEN=unused BRINED_ALMANAC_PULLER_KEY=unused BRINED_ALMANAC_TIERS_SHA256=unused BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID=unused BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET=unused BRINED_ALMANAC_BUGSINK_DSN=unused BRINED_ALMANAC_BUGSINK_API_TOKEN=unused docker compose -f compose.prod.yaml exec -T db pg_dump -U almanac almanac > "$STATE/backups/almanac-$(date +%F).sql"
```

The backups live on the same disk as the volume. They cover a lost volume or a
bad migration, not a lost Mac.

## First deploy, 2026-11

Deployed 2026-11-08 from branch `prod-on-the-mac` at `6125d77`, after main
(almanac bumped to `416e8e6`) was merged in.

- Fence check from the Mac, puller key alone: `sessions` returned 12;
  `version`, `transcript ... --last 5`, a bare login, and piped shell input
  exited 2; pty and port forwarding were refused.
- `mise run image:smoke`: ok. The interim store held 0 pending events.
- Step 1, `mise run deploy --db-only`: db healthy on a new `pgdata` volume.
- Step 2, dump and restore: exit 0.
- Step 3, counts (interim, prod): `seen` 102, 102. `_sqlx_migrations` 5, 5.
  The almanac at `416e8e6` applied a sixth migration on its first start.
- Step 4, `mise run deploy`: db and almanac healthy; the deploy exited 1
  because the puller was still in its first pass. That pass pulled all 12
  sessions (51 MB, 49 MB of it one telegram session) in about 5 minutes, then
  went healthy. The puller's `start_period` is now 20 minutes so `up --wait`
  waits the pass out. `PULL_HOST=picklelab` resolved by MagicDNS, and `from=`
  held for the container.
- The almanac daemon was stopped before its second scan, with nothing filed,
  to preview step 5 on a copy. The preview: 162 clusters, 102 unchanged
  against the restored ids, 0 restored ids missing, 60 new. 10 of the new
  ones file (8 brineworks, 2 pickleclaw, dated 2026-10-30 to 2026-11-07);
  the rest file nothing.
- Hygiene: no private key and no GitHub token under `$STATE`.
- Going live, 19:53Z: the daemon's first scan ran 60 new clusters, 10 with a
  file effect, no warnings. It filed exactly the previewed 10: brineworks
  #135 to #142 and pickleclaw #79 and #80, for 20 and 19 open.
- `mise run prod -- backfill`: found 162, queued 0, unchanged 162.
  `mise run prod -- check`: ok.
- Restart: db and almanac came back healthy, but the puller crash-looped on
  `mktemp: : Permission denied`. A default tmpfs is 1777 on create and root
  755 after a restart, so uid 10002 lost its key directory. Fixed in
  `5bbecd8` with an explicit `mode=0700,uid=10002,gid=10002`; redeployed, and
  a second restart came back with all three healthy, the puller at 0
  restarts and a fresh `.last-pull`, `check` ok, and nothing new filed.
