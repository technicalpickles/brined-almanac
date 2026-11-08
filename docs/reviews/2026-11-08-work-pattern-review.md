# Work pattern review, 2026-11-08

A fresh look at how work happens on this Mac and what it leaves behind, two days after [vision.md](../vision.md) was written. The point is to check the vision's numbers and find pain points the [workflow catalog](../workflows.md) does not cover yet. Each pain point below says whether a catalogued workflow already covers it.

Sources, all read-only: assistant-message timestamps from Claude Code transcripts (2026-09-01 to today, 113k messages), git history and working state of 67 checkouts and 145 worktrees under `~/github.com`, the taskwarrior export (1844 tasks), 243 Claude Code memory files, and 111 devlog notes in the vault. Open pull request state is missing: `gh` could not reach the credential broker during the review (see "Pain points", item 9). Raw reports from the five subagents that gathered this live in the session scratchpad, not in this repo, because they carry private names and task text.

## How bursty the work is

The vision says I work in bursts. The shape is sharper than that: bursts are usually one or two days long, and the quiet between them is usually about three days.

| Measure | Value |
|---|---|
| Days in range | 69 |
| Median assistant messages per day | 992 |
| Days with 2,000 or more | 23 |
| Days with fewer than 500 | 17, six of them zero |
| Top 17 days' share of all messages | 56% |
| Burst runs (consecutive days at 2,000 or more) | 10 |
| Median run length | 1.5 days |
| Median quiet gap between runs | 3 days |
| Longest quiet gap | 18 days (2026-09-28 to 10-15) |
| Weekend mean vs weekday mean | 1,926 vs 1,486 per day |

Eval-harness sessions, the `claude plugin eval` runs under `/private/tmp/claude-501/`, were 0.6% of all assistant messages and are excluded. The first version of this review excluded 19% as evals by mistake: it took the hash-suffixed project directories for eval scratch dirs, and they are Claude Code worktree sessions, real work that names its repository. The numbers above are the corrected ones.

**The day has two humps.** Messages peak at 09:00 to 11:00 and again at 20:00 to 21:00, with a trough from 13:00 to 17:00 and nothing from 04:00 to 06:00. Git commits show the same humps (peaks at 11:00 and 21:00).

**Bursts are wide, not deep.** A burst day touches a median of 6.5 projects (max 16, on 2026-10-26); a non-burst active day touches 3. The most parallel moments were 16 sessions active in one ten-minute slice (10-26) and 15 (09-03). The median active day peaks at 4 concurrent sessions.

**Sessions run long.** 161 continuous stretches with no hour-long gap: median 1.5 h, p90 5.2 h, max 13.7 h (Sunday 2026-10-18). 20 stretches ran 5 hours or more, 3 ran 10 or more. A burst day usually has two stretches, morning and evening.

**Git says presence is steady, volume is not.** 2,172 human commits since 09-01 across all refs; 64 of 69 days had at least one commit and the longest run of zero-commit days is one. The top decile of days (70 or more commits) holds 26% of commits. Weekly totals swing from 71 to 344.

**Which repositories.** Since 09-01: picklehome 365 commits, dotfiles 275, pickled-claude-plugins 162, pickleclaw 154, brineworks 146, brined-almanac 52 (all in November). In October nine repositories got 50 or more commits. In November, 14 repositories got at least one. A hobby scanner app that had 470 commits all time got 35 since 09-01 and none this month, which is what the dormant tier is for.

**Tasks follow the same curve.** Since 2026-08-01: 1,249 tasks created, 601 completed. The median week creates 70. Spike weeks created 194 (W36), 134 (W42), 148 (W43) and 166 (W44, with 74 completed, net +88). Creation spikes land on two-day weekend clusters (10-17/18, 10-31/11-01). Half of completions happen within a day of creation (317 of 601, 87 within an hour). The rest is a long tail: p90 of completion lag is 53 days.

## What a burst leaves behind

These are the counts of things that only a later burst, or almanac, will ever touch.

| Left behind | Count | Note |
|---|---|---|
| Pending tasks | 928 | 603 untouched for 30 days or more. 29 projects have pending tasks and no task activity in 60 days. |
| Parked handoffs | 242 | 167 older than 30 days (69%), 70 at 7 to 30 days, 5 under a week. Only 5 files touched in the last week. |
| Worktrees | 145 | 69 under `.claude/worktrees`. 22 last committed in July to September. 3 dirty. |
| Local branches unmerged into default | 107 | 59 stale (over 30 days), 51 with no remote. Squash-merged branches inflate this. |
| Repositories with unpushed commits | 5 | 41 in one hobby app, 10 in a static site, 5, 4 and 2 elsewhere. One repo shows 432 ahead but that is a remote ref last fetched 09-10. |
| Dirty working trees | 26 repositories | Largest 11 files. |
| Own repositories with no commit in 90 days | 12 | Plus 8 reference clones of other people's projects. |
| Memory files | 243 | 141 `project` type, many of them parked-work snapshots that belong in handoffs. 69 `feedback`. |

The vision's numbers of 884 tasks and 266 handoffs are now 928 and 242. Handoffs went down because the `park` simplification on 2026-11-08 cleaned some up, not because anything consumed them.

## Pain points against the catalog

Ordered by how much they cost, judging by how many sources name them and how long each hit takes. "Covered" means a catalogued workflow would report it as designed. "Partial" means the workflow needs an addition. "Gap" means nothing in the catalog watches it.

1. **Silent failures that look like success.** Gap, partly. Five independent cases: a backup cron that failed 101 consecutive runs over 13 days and recorded it only in a field nobody reads; a launchd sweep that silently drops runs; a poller whose failed sweeps write no row, so "ran and found nothing" equals "never ran"; the two systemd units in the vision; and today, the almanac daemon itself, which logs nothing on a scan that finds nothing (task bbbb9666). service-down covers units that should be running. It does not cover a scheduled job that stops producing. **Add:** a dead-man's-switch check in the Nagios convention, "age of last successful run of X", for every cron, launchd and systemd timer that matters, including almanac's own puller and daemon. picklehome's watchdogs become checks anyway; this is the check shape they need.

2. **Sandbox and classifier hand-backs.** Covered by session-friction's counts. The record is consistent: sandbox denials in about one session in three, the sandbox advisor wrong about a third of the time, classifier denials as hand-backs, and 25 or so pending tasks about it concentrated in pickled-claude-plugins, dotfiles and pickleclaw. Nothing to add to the design except urgency: this is the largest single friction cluster in both the vault and the backlog.

3. **Work left unpushed or uncommitted.** Gap. The vision mentions one session that stopped with work committed but not pushed. The checkouts hold five repositories with real unpushed commits (41 in one), 26 dirty trees, and 51 unmerged branches with no remote. Nothing in the catalog looks at the working state of a checkout. **Add a `left-behind` watcher:** per repository, commits ahead of the remote, dirty trees older than a day, local branches with no remote, and worktrees whose last commit is over 30 days old. One task per repository per week. It needs the same read-only mount of `~/github.com` that parked-handoffs needs, and it is the evidence source continue-work needs to decide whether a handoff is portable.

4. **Handoffs are written and not read.** Covered by parked-handoffs and task-resurfacer. The number that makes the case: 167 handoffs older than 30 days against 5 touched this week. Two additions to the design. First, 141 `project`-type memory files include parked-work snapshots, so parked-handoffs should also read `memory/*.md` with `type: project` whose body describes unfinished work, and report them as handoffs living in the wrong place. Second, "captured" needs one more evidence kind: a task that cites the handoff by path.

5. **Dormant projects still count as backlog.** Partial. 29 taskwarrior projects have pending tasks and no task activity in 60 days, and 12 own repositories have no commit in 90 days. The catalog marks a dormant repository's tasks as parked, but tiers are declared by hand. **Change:** compute dormant the way inactive is computed, from no human commit and no task activity in N days, and let a declaration override it. Otherwise the dormant tier stays empty and 29 projects' tasks inflate every stale count.

6. **Burst-created tasks with no follow-through.** Partial. Half the throughput is capture-then-do within a day. The other half is the real backlog, and a burst week like W44 adds 88 net. task-reconciler closes what got done; task-resurfacer ranks what is open. Neither says "this burst created 40 tasks and touched none of them again". **Add to task-resurfacer's weekly report:** tasks created in the last burst with no activity since, per project. That is the list to review before parking a project.

7. **Agents cannot see prod, and merged is not live.** Covered by deploy-drift. The friction review found each blocked read costs 10 to 30 minutes and the stale checkout on picklelab went unnoticed from 10-03. Nothing to add.

8. **Slow or unreadable CI, merged on red.** Covered by merged-on-red (later). The data moves it up: median 26 minutes per pull request on a single runner, max 68; agents get a 401 reading Woodpecker logs so red runs go undiagnosed; 8 of 20 pull requests in one week merged before CI finished. **Add to merged-on-red:** report CI queue time and runner count, since the queue is why people merge early.

9. **The credential broker is a single point of failure.** Partial. During this review the broker was unreachable, so every `gh` call and every signed commit hung in every agent session on the Mac for over an hour, and nothing reported it. The vault also records a dead Gmail refresh token found only when a check needed it, and sudo allowlist gaps on picklelab. credential-expiry is "later" and watches auth errors in transcripts. **Change:** move it to M1 next to service-down, and make the first version a host check, "the broker answers and `gh auth status` succeeds", not a transcript scan. A broker that is down is a service that is down.

10. **Parallel sessions collide.** Partial. Worktree sessions share a dev database, deploys refuse a tree another session dirtied (15 minutes per hit), subagents commit to main instead of their worktree, and one worktree session cannot write a sibling's files. session-friction will count the symptoms. The structural fix is in dotfiles and the project skills, not almanac, so the right output is a lesson-spread lesson once a fix exists. Nothing to add here beyond counting it.

11. **Measurement gaps found while measuring.** Gap, and they bite the M4 design directly.
    - Since about 2026-10-16 most sessions start from the `~/github.com` working directory, so the transcript `project` field is "github.com" for 28k of 113k messages, 67% of them since that date, and cannot attribute work to a repository on its own. Every message does carry a `cwd`, and 68% of messages have one inside a repository or its worktree. For the rest, tool-call file paths named a repository in 34 of the last 40 sessions. Attribution is cwd first, tool paths second.
    - Eval-harness sessions run from `/private/tmp/claude-501/` and were 0.6% of assistant messages. Their project path tells them apart, and the budget rule counts them.
    - Two cq processes running at once fight over the DuckDB lock, and a retry loop on that lock hung the Mac badly enough to stop this review. session-friction must be the only cq reader when it runs, use `--no-reindex`, or read the JSONL directly the way tool-failures does.

12. **Agents claim done without a durable artifact.** Covered by nothing, and probably rightly: this is a process rule, recorded in four memories and a devlog (delegated agent reports done, nothing committed). It is a lesson-spread candidate once the rule is written down once in dotfiles.

## What the day shape says about the budget rule

The budget rule in [vision.md](../vision.md) uses window usage and recent transcript writes. The day shape adds a cheap prior: weekday 13:00 to 17:00 and every day 23:00 to 07:00 are nearly always mine to lose, and weekend mornings and every evening from 18:00 to 22:00 are nearly always in use. Task 090b42c6 is measuring the starting numbers; it should check them against this histogram.

Bursts being one day long with a three-day gap also sets the clock for continue-work. A worker has about three days to move a handoff before the next burst, and the next burst lands in a different project more often than not (a burst day touches six or seven projects). The resurfacer, which tells the next burst what is open where it lands, pays off sooner than worker speed does.

## Proposed catalog changes

- New: `left-behind` (pain point 3). Watcher, no model, M2 with parked-handoffs.
- New check shape: dead-man's switch for scheduled jobs (pain point 1). M1, with service-down.
- Move: credential-expiry to M1 as a host check (pain point 9).
- Change: compute the dormant tier (pain point 5). M2.
- Add to parked-handoffs: read `project` memories as handoffs, and count a citing task as captured (pain point 4). M2.
- Add to task-resurfacer: tasks created in the last burst with no activity since (pain point 6). M3.
- Add to merged-on-red: CI queue time and runner count (pain point 8). M6.
- Constraints for M4: repository attribution from paths not `project`; exclude eval sessions from burst detection; single cq reader (pain point 11).

## Decisions, 2026-11-08

Answered the same day on a review page, and recorded in [workflows.md](../workflows.md), [roadmap.md](../roadmap.md) and [vision.md](../vision.md).

- **Top three by pain:** silent failures, dormant projects in the backlog, burst-created tasks with no follow-through. This sets the order inside M1 to M3.
- **Dormant tier: declared, not computed.** Development activity says a thing is being developed, not that it is used. Computing it needs usage signals, which is now an open question.
- **left-behind: report everything first,** quiet it down quickly, and keep the counts under each threshold so the thresholds can be retuned from data.
- **Dead-man's switch: a host check,** with the owner-of-the-list question left open until the options are laid out next to picklehome's watchdogs.
- **credential-expiry: M1, as a host check** that the broker answers and `gh auth status` succeeds.
- **parked-handoffs also reads project-type memory files,** and now watches the vault's parked-sessions folder first, since `park` writes there as of 2026-11-08.
- **task-resurfacer gets a weekly burst-orphans list.**
- **merged-on-red reports CI queue time and runner count.**
- **The budget rule takes the hour-of-day histogram as a prior.**
- **cq is the tool for session-friction.** Its gaps are sorted into integration points, feature gaps and ops in [open-questions.md](../open-questions.md).
- **Eval sessions and repository attribution** were answered the same evening by looking harder at the data. Evals are 0.6% of messages and their `/private/tmp` path identifies them. Attribution comes from each message's `cwd`, then from tool-call paths. The eval note also produced the eval-queue idea: run queued evals in likely-free hours.
- Nothing in the findings was flagged as wrong, and nothing was named as missing. The transcript hand-back counts are still missing from this pass.

### Added after the review: tools I use from several machines

Raised the same evening. cq, the Claude plugins and dotfiles run at home, at work and on the dev VM, and other people contribute to some of them. The catalog watched other people's issues on those repositories and nothing else, so nothing said when this machine fell behind its own origin. Measured on the spot:

| Repository | Finding |
|---|---|
| cq | Checkout's main 69 commits and ten weeks behind origin/main, which moved from another machine and an outside contributor. The installed binary (v0.7.0, from a 2026-09-11 commit) was newer than the checkout but 29 commits behind origin's v0.11.0, which includes the 2026-10-16 fix capping DuckDB threads and memory per process. The hang that stopped this review's cq agents is what that fix prevents. Root cause, found by a parallel session the same afternoon: a September `cargo install` left 0.7.0 in `~/.cargo/bin` ahead of the mise shims on `PATH`, shadowing mise's 0.11.0. Fixed by hand that day; the PATH order and the leftover `cargo:` mise entry in dotfiles were still open. |
| dotfiles | main in sync. Seven origin branches carry unmerged work, four with no local branch, one 61 commits deep. |
| pickled-claude-plugins | Main in sync. Twelve origin branches carry work with no local branch, two at 23 commits each, the newest from the week before. |
| plugin cache | Installed versions ahead of the marketplace manifest for one plugin, behind for two. |
| bktide | Main unchanged since July; one outside bot contribution. |

Encoded as the `own-upstream` workflow in M2, next to left-behind: a checkout behind its origin, origin branches this machine has never seen, contributions by others, installed copies behind their source, and file-path overlap between a local branch and an origin branch as the duplicate-work signal. The maintained tier now also means "used by me from more than one machine". One structural note came out of it: Claude Code memory files don't sync between machines, so a lesson learned at work is not on this Mac, which is one more reason a lesson that applies everywhere should become a dotfiles rule.
