# Roadmap

Milestones are named by what becomes possible, not by dates. [vision.md](vision.md) says why the work exists, and [workflows.md](workflows.md) describes each workflow. Anything deferred is here with the milestone it belongs to.

## M0: Running on the Mac

tool-failures runs all the time instead of when I remember to run it. [The prod design](superpowers/specs/2026-11-06-prod-on-the-mac-design.md) has the details.

- A Docker Compose stack with the almanac, its Postgres store, and a puller that fetches fresh OpenClaw transcripts from picklelab
- Prod secrets from a 1Password vault
- A runbook for deploying and checking the stack

**Done when** tool-failures files from transcripts the puller fetched, with no hand pulls, and the main checkout is a dev checkout again.

## M1: Filing to taskwarrior

A workflow can file a finding that no pull request closes. Every "next" workflow needs this, so it comes first. security-alerts is the first workflow to use it, because it needs no model and its findings are real work.

- A taskwarrior `Tracker` in `src/providers.rs`, next to `LogTracker`, `StoreRequired` and `PrivateOnly`
- A router that sends a finding to a GitHub issue or to taskwarrior, by the rule in [vision.md](vision.md) "Where output goes"
- A way for the prod container to write to taskwarrior, either a read-write mount of `~/.task` or a taskchampion sync server
- A delivery key on each task, so a rerun or a lost store doesn't file the same finding twice
- security-alerts, for deployed, active, and maintained repositories

**Done when** every open security alert on those repositories is tracked as one task or issue per repository, and running the scan again files nothing new.

## M2: An honest backlog

The backlog and the parking lot stop going stale on their own.

- task-reconciler, closing a task when a merged pull request or commit names its UUID or when a pull request or issue it cites has closed, and flagging the ones a later burst probably finished
- A close effect for taskwarrior tasks. Almanac's effect set has no way to close anything yet
- A daily task that lists what the reconciler closed
- parked-handoffs, sorting each handoff older than a week into captured, resumable, or orphaned
- Shared evidence code for both: merged pull requests, cited files that changed, commits that mention a task or a branch
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

## M5: Spending idle capacity

Agent work runs on capacity I'm not using.

- The budget rule in code, with its starting numbers next to it
- A probe that reads `unifiedWindows.five_hour` from a headless `claude -p` run
- Burst and working detection from transcript writes under `~/.claude/projects/`
- task-reconciler's evidence hunt, where an agent returns a proof and code checks it before closing
- session-friction, counting friction in my transcripts through cq each week, with a model ranking the items and writing the summary
- lesson-spread, finding repositories where a recorded lesson applies and has not been applied

**Done when** an agent proof closes a task code alone couldn't, a session-friction summary runs in a window that would otherwise expire, and four weeks pass with no directed session stopping on a usage limit because of almanac.

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
- **Closing a task on a model's word.** A model finds evidence, and code checks it. See [workflows.md](workflows.md) "Decided against".
- **Telegram alerts, filing to public repositories, dev VM transcripts, and work repositories.** See [workflows.md](workflows.md) "Decided against".
