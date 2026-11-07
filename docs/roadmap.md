# Roadmap

Milestones are named by what becomes possible, not by dates. [vision.md](vision.md) says why the work exists, and [workflows.md](workflows.md) describes each workflow. Anything deferred is here with the milestone it belongs to.

## M0: Running on the Mac

tool-failures runs all the time instead of when I remember to run it. [The prod design](superpowers/specs/2026-11-06-prod-on-the-mac-design.md) has the details.

- A Docker Compose stack with the almanac, its Postgres store, and a puller that fetches fresh OpenClaw transcripts from picklelab
- Prod secrets from a 1Password vault
- A runbook for deploying and checking the stack

**Done when** tool-failures files from transcripts the puller fetched, with no hand pulls, and the main checkout is a dev checkout again.

## M1: Filing to taskwarrior

A workflow can file a finding that no pull request closes. Every "next" workflow needs this, so it comes first.

- A taskwarrior `Tracker` in `src/providers.rs`, next to `LogTracker`, `StoreRequired` and `PrivateOnly`
- A router that sends a finding to a GitHub issue or to taskwarrior, by the rule in [vision.md](vision.md) "Where output goes"
- A way for the prod container to write to taskwarrior, either a read-write mount of `~/.task` or a taskchampion sync server
- A delivery key on each task, so a rerun or a lost store doesn't file the same finding twice

**Done when** a dry run's finding becomes one taskwarrior task in prod, and running the scan again files nothing new.

## M2: An honest backlog

The backlog and the parking lot stop going stale on their own.

- task-reconciler, flagging tasks that a later burst probably finished, with the evidence
- parked-handoffs, sorting each handoff older than a week into captured, resumable, or orphaned
- Shared evidence code for both: merged pull requests, cited files that changed, commits that mention a task or a branch
- A read-only mount of `~/github.com` and the vault in the prod stack

**Done when** a task that a merged pull request fixed is flagged within a day, and every handoff older than a week is sorted.

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

## M5: Spending idle capacity

Agent work runs on capacity I'm not using.

- The budget rule in code, with its starting numbers next to it
- A probe that reads `unifiedWindows.five_hour` from a headless `claude -p` run
- Burst and working detection from transcript writes under `~/.claude/projects/`
- session-friction, counting friction in my transcripts through cq each week, with a model ranking the items and writing the summary
- lesson-spread, finding repositories where a recorded lesson applies and has not been applied

**Done when** a session-friction summary runs in a window that would otherwise expire, and four weeks pass with no directed session stopping on a usage limit because of almanac.

## M6: Services watched

Almanac reports problems in running services and deployed repositories.

- service-down, after deciding whether it replaces picklehome's bash watchdogs
- merged-on-red, deploy-drift, skill-drift, and credential-expiry
- deployed-upkeep, or upstream's `almanacs/renovate` on deployed-tier repositories
- container-logs, once brineworks logs each call

**Done when** a service that stops is reported before I notice it.

## Not yet placed

- **Moving the host to picklelab.** It could come before M2 or after M6. [open-questions.md](open-questions.md) tracks it.
- **A rerun-CI effect.** Small steps need it, and it needs a principle first. It may belong upstream.

## Deliberately unscheduled

- **Merging pull requests.** The repository's rules merge. See [vision.md](vision.md) "What it is not".
- **Closing tasks automatically.** The reconciler marks a task as probably done, and I close it.
- **Telegram alerts, filing to public repositories, dev VM transcripts, and work repositories.** See [workflows.md](workflows.md) "Decided against".
