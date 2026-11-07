# Roadmap

Milestones are named by what becomes possible, not by dates. [vision.md](vision.md) says why the work exists, and [workflows.md](workflows.md) describes each workflow. Anything deferred is here with the milestone it belongs to.

## M0: Running on the Mac

tool-failures runs all the time instead of when I remember to run it. [The prod design](superpowers/specs/2026-11-06-prod-on-the-mac-design.md) has the details.

- A Docker Compose stack with the almanac, its Postgres store, and a puller that fetches fresh OpenClaw transcripts from picklelab
- Prod secrets from a 1Password vault
- A runbook for deploying and checking the stack

**Done when** tool-failures files from transcripts the puller fetched, with no hand pulls, and the main checkout is a dev checkout again.

## M1: Filing to taskwarrior, starting with what costs most

A workflow can file a finding that no pull request closes. Every "next" workflow needs this, so it comes first. The first two workflows on it are the cheapest ones that catch the costliest problems: open security alerts, and services that stop. A service down for six days and a site returning 500 for seven weeks cost more than anything else in [vision.md](vision.md), and neither needs a model.

- A taskwarrior `Tracker` in `src/providers.rs`, next to `LogTracker`, `StoreRequired` and `PrivateOnly`
- A router that sends a finding to a GitHub issue or to taskwarrior, by the rule in [vision.md](vision.md) "Where output goes"
- The container as a second client of the taskchampion sync server picklehome already runs on picklelab, which my Mac already syncs to. It keeps its own taskwarrior replica, so nothing shares the SQLite file. Adding it means allowlisting a client UUID and giving the container the encryption secret from 1Password
- An `almanac_key` user-defined attribute on each task, holding its delivery key. The tracker checks its replica for the key before filing, so a rerun or a lost store doesn't file the same finding twice. The close effect and the daily list of closes find tasks by the same key. My `.taskrc` defines the attribute through dotfiles
- security-alerts, for deployed, active, and maintained repositories
- Checks in the Nagios plugin convention for units that should be running and for site status, collected into each host's host-state report over pull
- The WordPress droplet on the tailnet, with a fenced key for its report
- service-down, reading those reports

**Done when** every open security alert on those repositories is tracked as one task or issue per repository, a stopped unit or a site that stops returning 200 becomes a task within an hour, and running either scan again files nothing new.

## M2: An honest backlog

The backlog and the parking lot stop going stale on their own.

- task-reconciler, closing a task when a merged pull request or commit names its UUID or when a pull request or issue it cites has closed, and flagging the ones a later burst probably finished
- A `Resolve { key, evidence }` effect and a `Tracker::resolve` method, so closing is its own effect that guards and the run record can see. Upstream's effect module says nothing in it closes, so propose it to ivy/almanac first ([draft](superpowers/plans/2026-11-07-upstream-issue-resolve-effect.md)), arguing that a task is my planning record and closing it on checked evidence is closer to `Attest` than to a merge. If upstream declines or is slow, pin a fork branch, which is a one-line change to `Cargo.toml`. Flagging and the daily list ship without it, and closing turns on when it exists
- A daily task that lists what the reconciler closed
- parked-handoffs, sorting each handoff older than a week into captured, resumable, or orphaned
- Shared evidence code for both: merged pull requests, cited files that changed, commits that mention a task or a branch
- session-friction's weekly counts, which are SQL over cq and need no model
- A read-only mount of `~/github.com` and the vault in the prod stack

**Done when** a task with conclusive evidence is closed within a day with that evidence attached, a task with weaker evidence is flagged instead, and every handoff older than a week is sorted.

## M3: Bursts start informed

A session starts with what is open in its repository.

- task-resurfacer, ranking open tasks and resumable handoffs per repository
- A SessionStart hook that reads the list almanac wrote and shows it

**Done when** a session in an active repository opens with its open tasks and resumable handoffs, and the list reflects M2's reconciling.

## M4: Open source doesn't wait

Threads on public repositories get answered between bursts.

- A GitHub App, following upstream's [deploying guide](https://github.com/ivy/almanac/blob/main/docs/deploying.md)
- oss-maintained, for outside issues and pull requests on maintained-tier repositories
- oss-contributions, polling the issues and pull requests I opened on other people's projects
- A convention that a workaround names its upstream issue in a comment

**Done when** a maintainer's reply on one of my upstream issues becomes a task within a day, and an upstream fix names the workaround it makes deletable.

## M5: Work continues between bursts

Work a burst left unfinished keeps going on capacity I'm not using.

- The budget rule in code, with its starting numbers next to it
- A probe that reads `unifiedWindows.five_hour` from a headless `claude -p` run
- Burst and working detection from transcript writes under `~/.claude/projects/`
- A worker runner: a headless Claude Code session in its own worktree and sandbox, started by almanac and stopped when the budget rule says so
- continue-work, picking up resumable handoffs, tasks marked as fair game, and follow-ups on its own pull requests, and parking a handoff whenever it stops
- task-reconciler's evidence hunt, where an agent returns a proof of read-only checks and code runs them before closing
- session-friction's summary, with a model ranking the week's items
- lesson-spread, finding repositories where a recorded lesson applies and has not been applied

**Done when** a handoff I park at the end of a burst has a pull request opened from it before my next burst, that burst starts from the handoff the worker left, an agent proof closes a task code alone couldn't, a session-friction summary runs in a window that would otherwise expire, and four weeks pass with no directed session stopping on a usage limit because of almanac.

## M6: Services watched

Almanac reports problems in running services and deployed repositories.

- More checks: container images, CA packages, releases past end of life, and site checksums
- picklehome's watchdogs rewritten as checks one at a time, each retiring its timer and Telegram alert
- merged-on-red, deploy-drift, skill-drift, and credential-expiry
- deployed-upkeep: Renovate on deployed-tier repositories, then upstream's `almanacs/renovate` repairing what breaks, under the budget rule
- container-logs, once brineworks logs each call

**Done when** the eMudhra survey I did by hand would have come from host-state reports, and a picklehome watchdog has retired in favour of its check.

## Not yet placed

- **Moving the host to picklelab.** Not scheduled. Almanac lives on the Mac because its most valuable inputs are Mac-local: parked handoffs in my checkouts, my Claude Code transcripts, which the budget rule and session-friction read, and my memory files. If the Mac turns out to be the wrong host, the move is the Mac pushing reports of those inputs, the way a push host sends its host-state report, and picklelab running almanac.
- **The budget rule upstream.** Proposed upstream ([draft](superpowers/plans/2026-11-07-upstream-issue-budget-rule.md)), since anyone running agents on a personal subscription shares the window with their own sessions.
- **A rerun-CI effect.** Proposed upstream ([draft](superpowers/plans/2026-11-07-upstream-issue-rerun-ci.md)) so other builders can weigh in. Building it waits until deployed-upkeep in M6 runs into flaky CI.

## Deliberately unscheduled

- **Merging pull requests.** The repository's rules merge. See [vision.md](vision.md) "What it is not".
- **Closing a task on a model's word.** A model finds evidence, and code checks it. See [workflows.md](workflows.md) "Decided against".
- **Telegram alerts, filing to public repositories, dev VM transcripts, and work repositories.** See [workflows.md](workflows.md) "Decided against".
