# brined-almanac

Almanac workflows, built on [ivy/almanac](https://github.com/ivy/almanac), that watch my services and keep them maintained. They're written in Rust.

## What never goes in this repo

This repo is public from its first push, so a few things stay out of it for good:

- Transcript or log text from the bot, including pasted samples and serialized tainted values.
- Secrets, or anything that resolves one, like a token, a key, or a path to a credential.
- The private MCP server config.
- Telegram ids and bot handles.

Hostnames and repo names are fine, since [picklehome](https://github.com/technicalpickles/picklehome) already names them.

## How the guardrails work

Nobody has to remember the rules, because hooks check them. A pre-commit hook runs gitleaks over the staged diff and `scripts/check-forbidden` over the staged files, and pre-push runs both over the whole repo. The forbidden check reads regex patterns from `.forbidden-patterns`, which are safe to commit, and private literal strings from `local/forbidden-strings`, which is gitignored because a denylist of private strings would publish them. On a fresh clone that file doesn't exist, so the script says the literal check is off until you create it. A worktree that `wt switch --create` makes gets a symlink to the main checkout's copy, and to its `local/tiers.toml`, from the `pre-start` hook in `.config/wt.toml` (`scripts/link-local`). GitHub push protection is on server-side as a last backstop.

Run `mise install && hk install` to arm the hooks, and `mise run check` to run them by hand.

## Running it

There are two workflows. `bugsink-issues` reads OpenClaw transcripts and ships each failed tool call to [Bugsink](https://www.bugsink.com/) as one event (`docs/superpowers/plans/2026-11-09-transcript-shipper.md`). Bugsink groups the events into issues and keeps their state. The same scan then reads Bugsink's open issues and files one GitHub issue for each that is unresolved, not muted, and has as many events as its error class needs. An issue that regresses files again (`docs/superpowers/plans/2026-11-10-tracker-consumer.md`). Before it files, the scan reads the almanac issues back: a Bugsink issue whose newest almanac issue was closed as completed is resolved, and one closed as not planned is muted until 50 events arrive in a day, each with a comment that links the close. A close with no reason, or one from before the failure last regressed, changes nothing, so a close never hides the next event (`docs/superpowers/plans/2026-11-10-resolve-on-close.md`). Brineworks failures go to `technicalpickles/brineworks`, everything else to `technicalpickles/pickleclaw`, and nothing files on a repo that isn't private. `security-alerts` reads the open Dependabot and secret scanning alerts on deployed, active, and maintained repositories, and files a taskwarrior task for each repository with an alert still open past its severity window, plus one task listing repositories where those alerts are off. The design docs in `docs/superpowers/specs/` have the full story.

The binary has almanac's verbs: `synth` prints the manifest, `check` validates the providers, `backfill [--dry-run]` runs the scan once, and `run` stays up. `cutover [--apply]` resolves in Bugsink the fingerprints whose almanac issues are all closed, once, at the move to the tracker (`mise run cutover`, `docs/superpowers/plans/2026-11-09-cutover.md`). It reads its settings from the environment:

| Variable | What it does |
|---|---|
| `BRENT_TRANSCRIPTS` | Directory of `*.jsonl` transcripts. Defaults to `transcripts/`, which is gitignored. |
| `GITHUB_TOKEN` | Turns on the GitHub tracker. Without it, each would-be issue prints to stdout instead. The issue scan also reads the almanac issues with it to find closed ones; without it, the scan skips that step and still files. |
| `GITHUB_ALERTS_TOKEN` | A read-only token for the security-alerts scan. Unset, that scan prints a skip message and finds nothing. |
| `BUGSINK_DSN` | The Bugsink project that receives one event per failed tool call. Unset, the shipper prints a skip message and sends nothing. It sends only on a live run. |
| `BUGSINK_API_TOKEN` | A Bugsink API token that reads the project's issues, tags and history, and can comment on, resolve and mute issues. Unset, or without the DSN, the issue scan prints a skip message and finds nothing. |
| `BUGSINK_CURSOR` | Where the shipper keeps the time of the newest event Bugsink accepted. Defaults to `/var/lib/brined-almanac/bugsink/cursor`. |
| `HOST_STATE_REPORTS` | Directory of host-state reports, one `<host>.json` per host in `hosts.toml`. Unset or empty, the host-state scan prints a skip message and finds nothing. `scripts/host-state-dry-run <dir>` previews what a directory would file. |
| `DATABASE_URL` | The Postgres store that remembers what's been filed. Required for live runs; set by `mise run live`, never by hand (see below). |
| `ALMANAC_MODE` | `live` lets effects reach GitHub. Anything else is a dry run, and `--dry-run` always wins. |

### Prod

Prod runs on the Mac as a compose stack: Postgres, the daemon, and a puller that fetches Brent's transcripts from picklelab every 10 minutes. Deploys, rollback, rotation and what to do when it breaks are in [the prod runbook](docs/runbooks/prod-on-the-mac.md).

### Dev

Everything below runs from a checkout and shares nothing with prod.

#### Dry runs

A dry run needs no database and no token, so this is the loop for poking at the scan:

```bash
BRENT_TRANSCRIPTS=path/to/pulls mise exec -- cargo run -- backfill --dry-run
```

Only the `live` task sets `DATABASE_URL`, so dry runs use the in-memory store. A dry run pointed at this checkout's store is harmless anyway, because almanac keeps a dry run's rows apart from a live run's.

#### The store

Each checkout gets its own Postgres, on a port portool (`cargo install portool`) hands out. `.portool.toml` declares the `db` slot, portool writes `DB_PORT` into the gitignored `.env.portool` (its git hooks keep it current), and `mise.toml` reads it from there.

```bash
mise run db          # start this checkout's Postgres; data lives in a named volume
mise run db:down     # drop this checkout's container, volume and ports
```

Events the old binary admitted but hasn't run fail under a new payload shape, and their delivery ids stay seen, so they never run again ([ivy/almanac#10](https://github.com/ivy/almanac/issues/10)). `mise run deploy` refuses while prod has pending events, and `mise run db:pending` runs the same check against this checkout's store.

`wt remove` runs `db:down` through the hook in `.config/wt.toml`, so removing a worktree also removes its Postgres.

#### Live runs

A live run from a checkout is deliberate and rare. Its store shares nothing with prod's, so it refiles every issue prod has already filed. Point it at a transcript directory prod never sees, or don't run it.

```bash
mise run db
BRENT_TRANSCRIPTS=path/to/pulls mise run live -- check      # validates the transcript dir and the GitHub tracker
BRENT_TRANSCRIPTS=path/to/pulls mise run live -- backfill   # files issues, then remembers them
```

`live` sets `ALMANAC_MODE=live`, builds `DATABASE_URL` from this checkout's `DB_PORT`, and takes the token from `gh auth token`, so it needs a `gh` login with the `repo` scope. Run `backfill` a second time to see the store do its job: every issue comes back as unchanged.
