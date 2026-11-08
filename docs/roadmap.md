# Roadmap

Milestones are named by what becomes possible, not by dates. [vision.md](vision.md) says why the work exists, and [workflows.md](workflows.md) describes each workflow. Anything deferred is here with the milestone it belongs to.

## M0: Running on the Mac

**Done 2026-11-08.** Went live at 19:53Z and filed the 10 previewed issues from transcripts the puller fetched. The [runbook](runbooks/prod-on-the-mac.md) records the first deploy.

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
- A dead-man's-switch check, "age of last successful run", for the scheduled jobs on picklelab and the Mac, almanac's own puller and daemon included. Silent failures were the top pick when the pain points were ranked on 2026-11-08
- credential-expiry as a host check on the Mac: the credential broker answers and `gh auth status` succeeds. Moved here from M6 after the 2026-11-08 outage

**Done when** every open security alert on those repositories is tracked as one task or issue per repository, a stopped unit, a site that stops returning 200, a scheduled job past its maximum age, or a broker that stops answering becomes a task within an hour, and running any scan again files nothing new.

## M2: An honest backlog

The backlog and the parking lot stop going stale on their own.

- task-reconciler, closing a task when a merged pull request or commit names its UUID or when a pull request or issue it cites has closed, and flagging the ones a later burst probably finished
- A `Resolve { item, evidence }` effect and a `Tracker::resolve(key, item, evidence)` method, so closing is its own effect that guards and the run record can see. Upstream's effect module says nothing in it closes, so propose it to ivy/almanac first ([ivy/almanac#6](https://github.com/ivy/almanac/issues/6)), arguing that a task is my planning record and closing it on checked evidence is closer to `Attest` than to a merge. If upstream declines or is slow, pin a fork branch, which is a one-line change to `Cargo.toml`. Flagging and the daily list ship without it, and closing turns on when it exists
- A daily task that lists what the reconciler closed
- parked-handoffs, sorting each handoff older than a week into captured, resumable, or orphaned, reading the vault's parked-sessions folder, the legacy `.parkinglot/` files, and project-type memory files
- left-behind, reporting unpushed commits, orphan branches, dirty trees and idle worktrees per repository, loud at first and tuned from the counts it keeps
- own-upstream, reporting the other direction for maintained-tier repositories: a checkout behind its origin, origin branches with work this machine has never seen, contributions by others, and installed plugin and binary copies behind their source. cq's checkout was ten weeks behind its origin on 2026-11-08
- Shared evidence code for all three: merged pull requests, cited files that changed, commits that mention a task or a branch, branches that still exist
- session-friction's weekly counts, which are SQL over cq and need no model, once cq can serve a reader while another process indexes
- A read-only mount of `~/github.com`, the vault, and `~/.claude/projects/*/memory/` in the prod stack

**Done when** a task with conclusive evidence is closed within a day with that evidence attached, a task with weaker evidence is flagged instead, every handoff older than a week is sorted, a repository with unpushed work for a week has a task saying so, and a maintained-tier checkout that falls a week behind its origin, or an installed copy that falls behind its source, has a task saying so too.

## M3: Bursts start informed

A session starts with what is open in its repository.

- task-resurfacer, ranking open tasks and resumable handoffs per repository
- Its weekly burst-orphans list: tasks the last burst created that nothing has touched since. Third of the three top picks on 2026-11-08, after silent failures and dormant projects
- A SessionStart hook that reads the list almanac wrote and shows it, including own-upstream's line for maintained-tier repositories, so a burst starts by pulling

**Done when** a session in an active repository opens with its open tasks and resumable handoffs, the list reflects M2's reconciling, and the week after a burst has a burst-orphans task per project that created any.

## M4: Work continues between bursts

Work a burst left unfinished keeps going on capacity I'm not using.

- The budget rule in code, with its starting numbers next to it, and the likely-free and likely-mine hours from the 2026-11-08 histogram as a prior
- A probe that reads `unifiedWindows.five_hour` from a headless `claude -p` run
- Burst and working detection from transcript writes under `~/.claude/projects/`, with the repository taken from each message's `cwd`, then tool-call paths, rather than the transcript's project field, and eval-harness sessions excluded by their `/private/tmp` project path
- A GitHub App, installed on fair-game repositories with contents and pull request write access, and later on maintained-tier repositories for M5, following upstream's [deploying guide](https://github.com/ivy/almanac/blob/main/docs/deploying.md)
- A worker runner: a headless Claude Code session in a container under upstream's rootless Podman sandbox, with no credentials, started by almanac and stopped when the budget rule says so. It returns a patch that almanac checks and pushes
- An effect that opens a pull request. Upstream's `Push` only commits to an existing pull request's branch, so propose it upstream ([ivy/almanac#9](https://github.com/ivy/almanac/issues/9))
- `park` writing portable handoffs: branch pushed, work in progress committed, repository-relative paths
- continue-work, picking up resumable handoffs, tasks in fair-game repositories (brineworks first), and follow-ups on its own pull requests, and parking a handoff whenever it stops
- task-reconciler's evidence hunt, where an agent returns a proof of read-only checks and code runs them before closing
- session-friction's summary, with a model ranking the week's items
- lesson-spread, finding repositories where a recorded lesson applies and has not been applied

**Done when** a handoff I park at the end of a burst has a pull request opened from it before my next burst, that burst starts from the handoff the worker left, an agent proof closes a task code alone couldn't, a session-friction summary runs in a window that would otherwise expire, and four weeks pass with no directed session stopping on a usage limit because of almanac.

## M5: Open source doesn't wait

Threads on public repositories get answered between bursts. The GitHub App from M4 already exists.

- oss-maintained, for outside issues and pull requests on maintained-tier repositories
- oss-contributions, polling the issues and pull requests I opened on other people's projects
- A convention that a workaround names its upstream issue in a comment

**Done when** a maintainer's reply on one of my upstream issues becomes a task within a day, and an upstream fix names the workaround it makes deletable.

## M6: Services watched

Almanac reports problems in running services and deployed repositories.

- More checks: container images, CA packages, releases past end of life, and site checksums
- picklehome's watchdogs rewritten as checks one at a time, each retiring its timer and Telegram alert
- merged-on-red, with CI queue time and runner count, deploy-drift, skill-drift, and credential-expiry's transcript scan (its host check is in M1)
- deployed-upkeep: Renovate on deployed-tier repositories, then upstream's `almanacs/renovate` repairing what breaks, under the budget rule
- container-logs, once brineworks logs each call
- eval-queue, running queued plugin evals in likely-free hours under the budget rule

**Done when** the eMudhra survey I did by hand would have come from host-state reports, and a picklehome watchdog has retired in favour of its check.

## Not yet placed

- **Moving the host to picklelab.** Not scheduled. Almanac lives on the Mac because its most valuable inputs are Mac-local: parked handoffs in my checkouts, my Claude Code transcripts, which the budget rule and session-friction read, and my memory files. If the Mac turns out to be the wrong host, the move is the Mac pushing reports of those inputs, the way a push host sends its host-state report, and picklelab running almanac.
- **The budget rule upstream.** Proposed upstream ([ivy/almanac#8](https://github.com/ivy/almanac/issues/8)), since anyone running agents on a personal subscription shares the window with their own sessions.
- **A rerun-CI effect.** Proposed upstream ([ivy/almanac#7](https://github.com/ivy/almanac/issues/7)) so other builders can weigh in. Building it waits until deployed-upkeep in M6 runs into flaky CI.
- **Usage signals for tiers.** Dormant was the second of the three top picks on 2026-11-08, but computing it from commits and tasks would misread a deployed thing in daily use. It waits on knowing what says "in use" per repository: Brent receiving messages, cq being invoked, a site serving requests. See [open questions](open-questions.md).

## Deliberately unscheduled

- **Merging pull requests.** The repository's rules merge. See [vision.md](vision.md) "What it is not".
- **Closing a task on a model's word.** A model finds evidence, and code checks it. See [workflows.md](workflows.md) "Decided against".
- **Telegram alerts, filing to public repositories, dev VM transcripts, and work repositories.** See [workflows.md](workflows.md) "Decided against".
