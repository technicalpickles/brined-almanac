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

Nobody has to remember the rules, because hooks check them. A pre-commit hook runs gitleaks over the staged diff and `scripts/check-forbidden` over the staged files, and pre-push runs both over the whole repo. The forbidden check reads regex patterns from `.forbidden-patterns`, which are safe to commit, and private literal strings from `local/forbidden-strings`, which is gitignored because a denylist of private strings would publish them. On a fresh clone that file doesn't exist, so the script says the literal check is off until you create it. GitHub push protection is on server-side as a last backstop.

Run `mise install && hk install` to arm the hooks, and `mise run check` to run them by hand.

## Running it

There's one workflow so far, `tool-failures`: it reads OpenClaw transcripts, clusters the failed tool calls by service, tool, error class and day, and files one issue per cluster that crosses its threshold. Brineworks failures go to `technicalpickles/brineworks`, everything else to `technicalpickles/pickleclaw`, and nothing files on a repo that isn't private. The design doc in `docs/superpowers/specs/` has the full story.

The binary has almanac's verbs: `synth` prints the manifest, `check` validates the providers, `backfill [--dry-run]` runs the scan once, and `run` stays up. It reads its settings from the environment:

| Variable | What it does |
|---|---|
| `BRENT_TRANSCRIPTS` | Directory of `*.jsonl` transcripts. Defaults to `transcripts/`, which is gitignored. |
| `GITHUB_TOKEN` | Turns on the GitHub tracker. Without it, each would-be issue prints to stdout instead. |
| `DATABASE_URL` | The Postgres store that remembers what's been filed. Required for live runs; set by `mise run live`, never by hand (see below). |
| `ALMANAC_MODE` | `live` lets effects reach GitHub. Anything else is a dry run, and `--dry-run` always wins. |

### Dry runs

A dry run needs no database and no token, so this is the loop for poking at the scan:

```bash
BRENT_TRANSCRIPTS=path/to/pulls mise exec -- cargo run -- backfill --dry-run
```

Only the `live` task sets `DATABASE_URL`, so dry runs use the in-memory store. A dry run pointed at the real store is harmless anyway, because almanac keeps a dry run's rows apart from a live run's.

### The store

Each checkout gets its own Postgres, on a port portool (`cargo install portool`) hands out. `.portool.toml` declares the `db` slot, portool writes `DB_PORT` into the gitignored `.env.portool` (its git hooks keep it current), and `mise.toml` reads it from there.

```bash
mise run db          # start this checkout's Postgres; data lives in a named volume
mise run db:down     # worktrees only: drop the container, the volume and the ports
```

The main checkout's store is the interim store for live runs. Its port block is pinned in portool, the container restarts with OrbStack, and `db:down` refuses to touch it. There's no backup of the volume yet; losing it means the next live run files everything again.

Run `mise run db:pending` before deploying a change to an event's payload. Events the old binary admitted but hasn't run fail under the new shape, and their delivery ids stay seen, so they never run again ([ivy/almanac#10](https://github.com/ivy/almanac/issues/10)). The task fails while anything is pending.

`wt remove` runs `db:down` through the hook in `.config/wt.toml`, so removing a worktree also removes its Postgres. A worktree's store is empty, which makes a live run from a worktree refile every cluster, so live runs happen from main.

### Live runs

```bash
mise run db
BRENT_TRANSCRIPTS=path/to/pulls mise run live -- check      # validates the transcript dir and the GitHub tracker
BRENT_TRANSCRIPTS=path/to/pulls mise run live -- backfill   # files issues, then remembers them
```

`live` sets `ALMANAC_MODE=live`, builds `DATABASE_URL` from this checkout's `DB_PORT`, and takes the token from `gh auth token`, so it needs a `gh` login with the `repo` scope. Run `backfill` a second time to see the store do its job: every cluster comes back as unchanged.
