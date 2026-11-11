# Workflows

The workflows brined-almanac runs or plans to run. [vision.md](vision.md) explains why they exist and the budget rule that agent work follows. [open-questions.md](open-questions.md) lists the decisions still open.

Each workflow has a status:

| Status | Meaning |
|---|---|
| shipped | Runs today. |
| next | The next thing to build. |
| later | Wanted, not scheduled. |
| blocked | Waits on work somewhere else. |
| no | Decided against. The entry says why. |

## Repository tiers

A repository's tier decides which workflows watch it. Tiers are declared, and moving a repository between tiers is a one-line change. Public repositories, and private ones that something public already names, are declared in [`tiers.toml`](../tiers.toml), which [tiers.md](tiers.md) explains. Private repositories that nothing public names are declared in a gitignored `local/tiers.toml`, whose durable copy is a document in the `brined-almanac` 1Password vault, so prod gets it the same way it gets its secrets and a fresh checkout restores it with one `op document get`. That file can only add a repository to a tier that code defines. It can't define a tier or a behaviour. Inactive repositories aren't declared at all, because the rule below computes them.

Dormant stays declared, decided 2026-11-08. Commits and task activity say a thing is being developed, not that it is used: a deployed app can serve every day with no commit for a year, and 29 taskwarrior projects with no activity in 60 days include things in daily use. Computing dormant from development activity would park their tasks wrongly. What could compute it is usage signals, such as Brent receiving messages, cq being invoked, or a site serving requests. Which signals exist per repository is an [open question](open-questions.md).

| Tier | What it is | What watches it |
|---|---|---|
| dormant | Not being worked on and not deployed, like an iOS app on hold. | Nothing. Its tasks are marked as parked, so they don't count as stale. |
| deployed | Running somewhere and in use, but not being developed, like a hobby web app I still use or the WordPress sites I host. | Service health, dependency and security updates, and drift between main and what is deployed. |
| active | Being worked on now. Today these are pickleclaw, brineworks, and picklehome. | Every workflow. |
| maintained | Public and used by other people, or by me from more than one machine, like bktide, cq and pickled-claude-plugins. | Outside issues and pull requests, my own lag behind the repository (own-upstream), plus whatever its deployed or active tier adds. |
| handed off | Someone else maintains it, like homesick, where another maintainer merged updates in July 2026. | Only the archive question below, if that person stops too. Its README names who maintains it now. |
| retired | Public and inactive, like jeweler, whose last human commit was in 2019. | Only the archive question below. |

A repository can be in more than one tier. bktide is maintained and active while I work on it.

## Host state

A workflow that checks a host reads a host-state report. The report is the results of the host's checks, collected as JSON.

A check is a small executable that tests one thing and follows the Nagios plugin convention. It exits 0 for OK, 1 for warning, 2 for critical, and 3 for unknown, and it prints one line of status with optional metrics after a `|`, as in `DISK OK - / at 61% | used=61%;80;90`. Nagios, Icinga, Sensu, Checkmk, and Zabbix run checks in this shape unchanged, and so can cron or a systemd timer, so a check written here works with other monitoring tools. A check can be written in any language and tested on its own.

Checks cover container images, CA package versions, operating system releases past end of life, site checksums, systemd units that should be running, and the HTTP status of the sites a host serves.

One more check shape matters as much as any of those: a **dead-man's switch**, which reports the age of the last successful run of a scheduled job and goes critical past a maximum age. Five failures in 2026 were invisible because nothing had it: a backup cron that failed 101 consecutive runs over 13 days and recorded that only in a field nobody reads, a launchd sweep that silently dropped runs, a poller whose failed sweeps wrote no row so "ran and found nothing" looked like "never ran", two systemd units down for days, and almanac's own daemon, which logs nothing on a scan that finds nothing. The check lives on the host with the job, like every other check, and the Mac counts as a host for almanac's own puller and daemon.

Who owns the list of jobs was decided on 2026-11-10: both. Each host checks its own jobs from the records it already has, so picklelab's check reads systemd's last start time and result for each timer and no job changes. Almanac's own list names only its puller and daemon, which it already knows about. A new job is registered on the host that runs it, and never in almanac unless almanac runs it. The puller already writes its last completed pass to `.last-pull` in the transcripts directory, which its compose healthcheck reads, so its check needs no new marker.

A workflow never knows how the report arrived. Two transports deliver it:

- **Pull.** Almanac connects with a fenced SSH key whose forced command prints the report and does nothing else. This is the pattern the prod design uses for picklelab. It suits hosts on the tailnet.
- **Push.** The host writes the report on a schedule to a place almanac reads. It suits hosts almanac can't or shouldn't reach, because the host needs no inbound key. A host whose report stops arriving is itself a finding.

A report is one JSON file per host, named `<host>.json` in the directory `HOST_STATE_REPORTS` names. It has these fields, and no others:

| Field | What it holds |
|---|---|
| `host` | The host's name. It must match the file name and a host in `hosts.toml`. |
| `generated_at` | When the collector wrote the report, RFC 3339 in UTC. |
| `interval_seconds` | How often the collector runs. The Mac's is 300. |
| `checks[]` | One entry per check: `name` (`[a-z0-9-]+`, unique in the report), `status` (0 OK, 1 warning, 2 critical, 3 unknown), `since`, and `output`. `output` is one line of at most 500 bytes. |

`since` is when the check entered its current status, so it does not move while the status holds. The workflow files a critical or unknown check once its `since` is 10 minutes old, and a warning once it is 24 hours old. A report whose `generated_at` is older than 30 minutes is stale, and files one task for the host in place of its checks. A time ahead of the scan's clock counts as age zero.

Each host declares its transport. picklelab is the first pull host: the puller fetches its report over a fenced SSH key, in the same pass as the transcripts, and writes it to the directory beside `mac.json`. The WordPress droplet uses pull too, once it joins the tailnet, which also lets it close SSH to the internet.

## Rules every workflow follows

- A workflow files an issue only when a pull request in a private repository closes the finding. Everything else, including findings about public repositories like picklehome or dotfiles, ops actions, and decisions, goes to taskwarrior. Issue bodies can carry transcript text or task text, so nothing files to a public repository. "Where output goes" in [vision.md](vision.md) has the full rule.
- Maintenance on an inactive repository asks first whether to archive it. A repository is inactive when no person has committed to it in 12 months. Commits from bots such as Dependabot don't count. When a workflow finds maintenance there, like an alert or an outside pull request, it files one task that asks whether to archive the repository, and it holds the maintenance until I answer. The task includes a README notice to add before archiving, which points people to a recommended path: a successor project, a maintained fork, or the tool to use instead. The workflow checks that an existing pointer still leads somewhere alive. jeweler's README points to Juwelier, which hasn't been pushed to since 2021. If I archive it, the maintenance goes away with it. If I keep it, I give it a tier, and the held findings file. A repository someone else still commits to is active by this rule, even when I don't commit to it.
- Work repositories are out of scope. I can't reach them from here. My own public repositories that I use at work are in scope: the work machine's pushes land on the same origin this Mac fetches, so origin is where the two machines meet.
- A workflow that calls a model follows the budget rule. A watcher calls no model.
- Every workflow has a dry run that needs no database and no token.

## Catalog

### bugsink-issues

- **Status:** shipped.
- **Watches:** OpenClaw transcripts pulled from picklelab, which it ships to Bugsink as one event per failed tool call, and then Bugsink's open issues for that project.
- **Reports:** one issue per Bugsink issue that is unresolved, not muted, and has as many events as its error class needs. Bugsink groups by service, tool, and error class. An issue that regresses after being resolved files again. Brineworks failures go to brineworks, and everything else goes to pickleclaw.
- **Model:** none.
- **Design:** [the tracker consumer plan](superpowers/plans/2026-11-10-tracker-consumer.md). It replaced the per-day `tool-failures` filer of [the tool error scan design](superpowers/specs/2026-11-06-tool-error-scan-design.md).

### task-reconciler

- **Status:** next.
- **Watches:** pending taskwarrior tasks for active and deployed repositories.
- **Closes:** a task when code confirms one of these:
  - a merged pull request or a commit on main names the task by UUID, after "closes", "fixes", or "resolves", or with no not-done phrase in the same sentence. The starting not-done phrases are "still open", "tracked in", "phase N", "follow-up", "part of", and "refs". A UUID next to one of them only marks the task as probably done. On commits from 2026-06 to 2026-11, this rule closes 32 of the 35 tasks commits cited and marks the other 3, which are all still open.
  - the task cites a pull request that has merged, or an issue that has closed
  - an agent's proof checks out (see below)

  Each closed task gets the evidence as an annotation and the `+almanac` tag. One taskwarrior task per day lists what closed, so I see every close and can reopen a mistake.
- **Reports:** tasks that are probably done on weaker evidence, such as a merged pull request that touched the files the task cites, or a cited line that no longer looks the way the task describes. The report is one task per project per day.
- **Model veto:** later, a small model reads the sentence around a cited UUID and can stop a close it reads as not done. It can't close anything itself.
- **Evidence hunt:** for a task code can't decide, an agent reads the task and the repository and returns a proof. A proof is a list of read-only checks code can run: a commit that must be on main, a file on main that must contain or lack a pattern, an issue or pull request that must be closed, and CI that must have passed on main at or after a given commit. A test that exists and passed in CI stands in for running it, so a proof never runs repository code. Running a test in a sandbox gets added only if proofs keep falling short without it. Code runs every check. The task closes only if all of them pass. If the agent finds the work isn't done, it annotates the task with what's left, which is useful on its own. The proof is agent output, so it is untrusted until code has checked it.
- **Model:** none for the UUID and cited-link checks. The evidence hunt is agent work under the budget rule, and suits the end of a window that would otherwise expire.
- **Why first:** it shrinks the backlog that the task-resurfacer has to choose from, and its first version needs no model. parked-handoffs checks the same kinds of evidence, so the two share that code.

### security-alerts

- **Status:** shipped 2026-11-09.
- **Watches:** Dependabot and secret scanning alerts on deployed, active, and maintained repositories, through the GitHub API, with a read-only token. Code scanning is out of scope: nothing runs it on a watched repository.
- **Reports:** a task on `task:<repository name>` for each repository with an overdue alert, listing every overdue alert by severity. An alert is overdue when it is still open after its window: secret 0 days, critical 2, high 14, medium 30, unknown 30, low 90. Renovate and Dependabot get that long to fix it, and an alert fixed inside its window never files. The delivery ID carries a digest of the overdue set, so a rescan files nothing new, and a change to the set files a fresh task. A second task on `task:brined-almanac` lists the coverage gaps: scanned repositories with Dependabot alerts off, and public ones with secret scanning off. Nothing here files an issue. See the [design](superpowers/specs/2026-11-08-tiers-and-security-alerts-design.md).
- **Model:** none.
- **Inactive repositories:** the archive question is not built yet. No watched repository is inactive today.
- **Later:** upstream's [roadmap](https://github.com/ivy/almanac/blob/main/docs/roadmap.md) M2, "Exposure", joins a CVE feed against an inventory of what each repository, image, and host contains. When it lands, use it for the images and hosts on picklelab and the WordPress droplet instead of writing that here. A Dependabot alert covers a lockfile. It says nothing about a host's base images, its CA package, or a release past end of life, which is what the eMudhra advisory turned on.

### continue-work

- **Status:** next, in M4 with the budget rule.
- **Watches:** resumable parked handoffs, open tasks in fair-game repositories, and almanac's own open pull requests.
- **Fair game:** a repository is fair game or it isn't, declared next to its tier. Every open task in a fair-game repository is eligible unless it's tagged `+mine`, and workers take them in task-resurfacer's ranked order. A task that turns out to need a decision gets parked with the question written down. brineworks is the first fair-game repository. Others join once workers have earned it there.
- **Handoffs:** a handoff is eligible when its repository is fair game, parked-handoffs sorts it as resumable, its next steps are code a worker can do in its worktree, and it has no `**Worker:** no` line. Newest go first. The worker runs the same validation as the `unpark` skill before it changes anything. If the handoff is stale, or a next step needs a deploy, a host, a sign-in, or a decision, the worker records what blocked it in the handoff and stops there. Those handoffs stay mine.
- **Does:** starts a worker to continue one piece of work toward a pull request. The worker is a headless Claude Code session in a container under upstream's rootless Podman sandbox, with a fresh clone, the handoff or task text that almanac passes in, and no credentials. It returns a patch and a description. Code checks the patch, and almanac pushes it through a GitHub App to the pull request's branch, or opens the pull request on the first round. The App is installed only on fair-game repositories, with write access to contents and pull requests. Later rounds answer review comments and fix failing checks the same way. Nothing merges.
- **How many:** at most one worker per repository, so two workers never edit the same files. One worker in total to start. When the budget rule's about-to-expire exception applies, up to three, each in a different repository. Both numbers live in code next to the budget rule.
- **Local CI first:** when a repository has a local CI command, such as brineworks' `mise run ci`, the worker runs it in its sandbox before returning a patch, so a pull request reaches the forge's CI only after local CI has passed.
- **Portable handoffs:** `.parkinglot/` is gitignored and a container sees only the forge, so a handoff is eligible only when its branch is pushed and nothing it needs is uncommitted or at an absolute Mac path. The `park` skill should push the branch, commit work in progress to it, and write repository-relative paths.
- **Stops:** when the budget rule says stop, when a burst starts in the same repository, or when a step needs me. It parks its state in a handoff before it stops, and the next window resumes from that handoff. A stop that needs me records its reason from a fixed set: deploy, host, sign-in, decision, or stale. session-friction counts those reasons each week, and a common one becomes a task to give workers a safe way through that step.
- **Model:** yes. This is the main use of agent work.
- **Feeds:** task-resurfacer. A burst in a repository sees the handoffs and pull requests that workers left there.

### task-resurfacer

- **Status:** next, after task-reconciler.
- **Watches:** pending tasks and resumable parked handoffs, ranked per repository by age, priority, and recent activity.
- **Reports:** a short list of open tasks and handoffs for each repository, which a Claude Code SessionStart hook shows when a session starts there. Almanac computes the list. The hook only reads it, because almanac is not a chat surface.
- **Weekly, burst orphans:** tasks created during the last burst that nothing has touched since, per project. Half of all completions happen within a day of creation, and the rest is a long tail: in the week of 2026-10-26 a burst created 166 tasks and completed 74. Neither the reconciler nor the ranked list says which burst a task came from, and this list is what to review before parking a project.
- **Model:** none.

### parked-handoffs

- **Status:** next.
- **Watches:** three places a handoff can live. Since 2026-11-08 `park` writes to the vault's parked-sessions folder when second-brain is configured, so that folder comes first. The legacy `.parkinglot/*.md` files in every checkout under `~/github.com` come second: 242 of them on 2026-11-08, 167 older than 30 days and 5 touched in the last week. Third, Claude Code memory files of `type: project` whose body describes unfinished work. 141 of 243 memory files were project type on 2026-11-08 and many are parked-work snapshots; a handoff there is a handoff in the wrong place, and the report says so.
- **Reports:** each handoff older than a week, sorted into one of three groups by evidence in code:
  - **captured.** Its branch merged, its pull request closed, or a task names it by path or by topic. It can go.
  - **resumable.** Its branch or worktree still exists and nothing else tracks the work.
  - **orphaned.** Nothing else tracks the work and its branch is gone. Only the handoff remembers it.

  Orphaned handoffs matter most. Deciding what to do with one is my call, so the workflow files one task per repository per day.
- **Model:** none.
- **Needs:** read access to the checkouts, the vault, and `~/.claude/projects/*/memory/`. The compose stack in [the prod design](superpowers/specs/2026-11-06-prod-on-the-mac-design.md) mounts `~/github.com` and the vault read-only. Add the mounts to that design when this workflow is built.

### left-behind

- **Status:** next, in M2 with parked-handoffs.
- **Watches:** the working state of every checkout under `~/github.com`: commits ahead of the remote, local branches with no remote, dirty working trees, stashes, and worktrees by last commit date.
- **Why:** nothing watches checkout state, and a burst leaves a lot of it. On 2026-11-08 five repositories had real unpushed commits (41 in one), 26 had dirty trees, 107 local branches were unmerged into their default with 51 of them on no remote, and 145 worktrees existed, 22 of them last committed in July to September. The vision's one session that stopped with work committed but not pushed is the small version of this.
- **Reports:** everything at first, one task per repository per week, with the thresholds in code next to the workflow. Decided 2026-11-08: start loud, quiet it down quickly, and keep recording the counts under each threshold so the thresholds can be retuned from data instead of guessed. A dirty tree in a repository with a session active today is noise, so the first threshold to add is "idle for N days".
- **Model:** none.
- **Feeds:** continue-work, which needs to know whether a handoff's branch is pushed before a worker can take it, and parked-handoffs, whose "resumable" group depends on whether a branch or worktree still exists.
- **Needs:** the same read-only mount of `~/github.com` as parked-handoffs.

### own-upstream

- **Status:** next, in M2 with left-behind, which it shares code with. left-behind looks at what this machine has that origin lacks. own-upstream looks the other way: what origin has that this machine, and the copies installed on it, lack.
- **Why:** the tools I build, I also use, at home, at work and on the dev VM, and other people contribute to some of them. A fix made on one machine is invisible on the others until something pulls, so the same problem gets fixed twice, and an installed copy keeps a bug its source fixed months ago. On 2026-11-08 the cq checkout's main was 69 commits and ten weeks behind origin/main, which had moved from another machine and from an outside contributor, while the cq binary on the same Mac was built from a commit newer than the checkout. pickled-claude-plugins had twelve origin branches carrying work no local branch had, two of them 23 commits each. dotfiles' main was in sync, but seven origin branches carried unmerged work and four of them had no local branch, one 61 commits deep. Installed plugin versions were ahead of the marketplace manifest for one plugin and behind it for two.

  The cq case had a price the same day. The installed binary was v0.7.0, built from a commit of 2026-09-11; origin's main was at v0.11.0 and included a 2026-10-16 fix that caps DuckDB threads and memory per cq process. Two cq processes without that cap, retrying on a conflicting lock, hung the Mac during the review that found this. The fix had been on the public repository for three weeks.

  The root cause was two installs of the same tool. mise had cq 0.11.0, but a `cargo install` from September had left 0.7.0 in `~/.cargo/bin`, which sat ahead of the mise shims on `PATH`, so every shell ran the old one. A parallel session that afternoon found it, moved cq to mise's copy, and reindexed: the index shrank from 7.8 GB to 1.25 GB and `cq projects` went from over seven minutes to two seconds. The PATH order and the leftover `cargo:` entry in mise are dotfiles problems and were still open that evening. Version lag was the symptom; the shadowed install was the cause, and the watcher has to see both.
- **Replaces a manual check.** Taskwarrior task `87e5a33b-c04e-4cab-a7a0-b4a1c94e04a3` (dotfiles) is the hand version of this workflow: a baseline of installed plugin versions from 2026-10-18 and a note to verify them again. It has been re-annotated once and never closed. own-upstream makes it a report, and the task closes when the report runs.
- **Watches:** for maintained-tier repositories, after a read-only fetch:
  - the checkout's default branch against origin's: commits behind, and the age of the last fetch
  - origin branches with commits not on the default branch and no local branch of the same name, with their age. This is work in progress on another machine, or a contributor's branch
  - commits and merged pull requests by other people since the checkout last moved
  - installed copies against their source: plugin versions in `~/.claude/plugins/cache` against the marketplace manifest on origin's main, and `cargo install --list` commits against origin's main, for cq and anything else installed from a git source
  - the same tool installed more than once: for each tool a maintained-tier repository ships, every copy on `PATH` (`which -a`), mise's copy, and `~/.cargo/bin`, with the version each reports. A report when `PATH` resolves to a copy that isn't the newest, which is what shadowed cq 0.11.0 with 0.7.0
- **Reports:** one task per repository per week with the lag in each direction, and a line for task-resurfacer's SessionStart list: this checkout is N commits behind origin, these origin branches have work, your installed copy is at version X and main is at Y. A burst in a repository then starts by pulling, not by rediscovering.
- **Duplication:** a local branch or dirty tree that touches the same files as an origin branch from elsewhere is the clearest sign of the same fix being made twice. The first version reports the overlap by file path and leaves the judgement to me.
- **Memory is per machine.** Claude Code memory files under `~/.claude/projects/` don't sync between machines, so a lesson recorded at work is not on this Mac. lesson-spread reads this Mac's memory only. Rules in dotfiles do sync, which is one more reason a lesson that applies everywhere should become a rule.
- **Model:** none.
- **Needs:** fetch access to origin for maintained-tier repositories, read-only. The container can fetch public repositories without credentials; private ones in that tier wait for the GitHub App. Plus the `~/github.com` mount and read access to `~/.claude/plugins/cache` and `~/.cargo/.crates.toml`.

### oss-maintained

- **Status:** next.
- **Watches:** issues and pull requests from other people on maintained-tier repositories, through almanac's native GitHub source.
- **Reports:** a pull request that needs my review, an issue that waits on my answer, or a thread that has gone quiet.
- **Model:** none to start.
- **Needs:** the GitHub App that continue-work sets up in M4, installed on maintained-tier repositories too.

### oss-contributions

- **Status:** next, after oss-maintained.
- **Watches:** issues and pull requests I opened on other people's projects, such as ivy/almanac, openclaw, and get-bb/bb.
- **Reports:**
  - a maintainer who replied or asked me a question
  - a pull request with failing checks or merge conflicts
  - an issue that was closed or fixed upstream, together with the workaround in my code that it makes deletable

  To find workarounds, a workaround names its upstream issue in a comment, and the workflow searches for that link.
- **Model:** none.
- **Needs:** only a token. The repositories are not mine, so webhooks are not available. This workflow polls on a schedule.

### session-friction

- **Status:** next.
- **Watches:** my Claude Code transcripts under `~/.claude/projects/`, read through [cq](https://github.com/technicalpickles/pickled-claude-plugins/tree/main/plugins/cq). bugsink-issues does this for Brent's transcripts. This workflow does it for mine.
- **cq is the tool**, decided 2026-11-08, and it has gaps to close before a daemon can lean on it. On 2026-11-08 two processes ran `cq sql` at once, the second got a conflicting lock on the DuckDB index, both retried in loops, and the retries plus an index sync hung the Mac. What this workflow needs from cq, sorted as integration point, feature gap, or ops: a way to read while another process indexes, or a single long-lived reader other callers query (feature); `--no-reindex` as the default for a secondary reader, with lock waits instead of retry loops (feature); a schedule for the index sync so the daemon never triggers one (ops); and a way to tell eval-harness sessions from mine. They run from `/private/tmp/claude-501/`, so the project path already tells them apart, and they were 0.6% of assistant messages in 2026-09 to 2026-11 (integration point).
- **Attribution:** since about 2026-10-16 nearly every session starts from `~/github.com` and works in worktrees, so the transcript's project field says `github.com` for 28k of 113k messages since 2026-09-01, and for 67% of them since 2026-10-16. Decided 2026-11-08: attribute by `cwd` first, tool-call paths second, git only as a fallback. Every transcript message carries a `cwd`, and 68% of messages have one inside a repository or its worktree, so they attribute directly. For the rest, the file paths in tool calls named a repository in 34 of the last 40 sessions. The same answer serves the budget rule and continue-work.
- **Schedule:** weekly, at the end of the week, the same window as the review I did by hand for picklelab on 2026-10-18. Running it when a burst ends waits until the weekly version shows the counts are useful.
- **Counts:** only what transcripts record exactly. That review found its regex counts overcounted when agents quoted error text, and it couldn't measure hand-backs at all. The first version counts:
  - auto-mode classifier denials and permission rule denials, grouped by reason
  - usage-limit stops
  - failed tool calls, grouped by command. Sandbox denials count only when the call itself failed, not when a result quotes the error.
  - the same command attempted three or more times in one session
  - worker stops by reason, from continue-work

  Hand-backs and the time friction costs stay out until something measures them.
- **Reports:** by whether a task tracks the item. An untracked item over its threshold becomes a task with its count and the sessions it came from. A tracked item gets the week's count annotated on its existing task, so the evidence stays current without a new task.
- **Model:** none for the counts, which are SQL over cq. A model may rank the items and write the summary, under the budget rule. This work suits the end of a window that would otherwise expire.
- **Feeds:** lesson-spread. A friction item with a fix becomes a lesson.

### lesson-spread

- **Status:** later, after session-friction.
- **Watches:** lessons I already record, in this order:
  1. Claude Code `feedback` memories. On 2026-11-07 there were 69 of 243 memory files. Each holds one lesson with how to apply it, scoped to one project, so the open question for each is whether my other projects need it too.
  2. Devlog entries in the vault, 106 session notes in the inbox alone. They're prose, so pulling a check out of one is model work under the budget rule.

  Rules in dotfiles are where a lesson ends up, not a source. They already apply everywhere.
- **Reports:** other repositories where a lesson applies and has not been applied. A repository qualifies when it uses the same tool and lacks the fix. For example, a lesson about hk's `builtins.gitleaks` scanning gitignored files applies to every repository with an `hk.pkl` that uses it.
- **How:** each lesson names a check that code can run against a repository, such as "has `hk.pkl`" plus "uses `builtins.gitleaks`". Where a lesson can't name such a check, a model may judge whether it applies, under the budget rule.
- **Prefer structure:** a lesson that applies to every repository files one task to promote it to a dotfiles rule or a lint. A lesson that applies to a few files one task per repository that lacks it.
- **Model:** none for lessons with a check.

### host-state-reports

- **Status:** built. The workflow, the checks, the collector and the prod wiring are built, and it goes live at the next deploy.
- **Watches:** the JSON report each host's collector writes into `HOST_STATE_REPORTS`, one file per host declared in `hosts.toml`. Unset, the scan prints a skip message and finds nothing. The Mac is the first host. Its collector runs every 5 minutes with two checks, the age of the puller's last completed pull and the health of the prod stack's containers.
- **Reports:** a task on the host's project for each check that has been failing long enough, and one when a host's report stops arriving or can't be read. The delivery ID is `<host>/<check>/<UTC date of since>` or `<host>/stale/<UTC date>`, so a rescan files nothing new, and a check that recovers and fails again on another day files a fresh task. A file in the directory that no declared host owns is logged and ignored. The thresholds are in the Host state section above.
- **Model:** none.
- **Preview:** `scripts/host-state-dry-run <reports-dir>` prints what a directory of reports would file, with no database and no token. It is a first scan, so a report that is merely old shows no stale finding; see the runbook's Host state section.

### service-down

- **Status:** next, in M1 with security-alerts. Its checks report through [host-state](#host-state-reports).
- **Watches:** host-state reports from picklelab and deployed-tier hosts, including the WordPress droplet. One of its sites returned HTTP 500 for weeks while it was compromised, and nothing reported it.
- **Reports:** a check that turned critical or stayed at warning, such as a unit that should be running and isn't, or a site that doesn't return 200.
- **Model:** none.
- **Order:** checks for the gaps come first: units that should be running, and site status. picklehome's watchdogs (disk_monitor, exec-approvals-watch, brineworks-server-watch) then become checks one at a time, starting with disk_monitor, which already has thresholds and stays silent when all is well. Each watchdog's timer and Telegram alert retire once its check files through almanac.

### merged-on-red

- **Status:** later.
- **Watches:** merges to main on active repositories and their CI status at merge time, plus the CI queue itself.
- **Reports:** a pull request that merged before CI finished or with a failing check, and whether main went red after it. Also CI queue time per pull request and the runner count, decided 2026-11-08, because the queue is why people merge early: in 2026-10 the median pull request waited 26 minutes on a single runner, the longest 68, and 8 of 20 merged before CI finished.
- **Model:** none.

### deploy-drift

- **Status:** later.
- **Watches:** the difference between what is merged and what is running, starting with `openclaw-ro drift` on picklelab.
- **Reports:** a plugin enabled in config but not loaded, a stale checkout, or a container built from an old image.
- **Model:** none.

### skill-drift

- **Status:** later.
- **Watches:** skill copies that Brent edits in its workspace against their source.
- **Reports:** an edit that the next sync would overwrite.
- **Model:** none.

### credential-expiry

- **Status:** later, in M6. It was moved to M1 as a host check on 2026-11-08 and moved back on 2026-11-10.
- **Watches:** authentication errors in agent transcripts and service logs.
- **Why not a host check:** on 2026-11-08 the credential broker was unreachable for over an hour. Every `gh` call and every signed commit hung in every agent session on the Mac, and nothing reported it. A check on the Mac that runs `gh auth status` looked like the cheap answer. It isn't one. The broker approves by launcher, the app that started the command. A request an agent session starts is approved by an automatic rule. The same command under launchd has no rule and waits for a person, so the check timed out on every run when it was tried. Even with an exemption it would test a path no agent session uses, and could stay green while every session hangs. The failure shows up in the transcripts, so that is where this reads.
- **Reports:** a credential that has started failing, such as an expired OAuth refresh token, or sessions whose credential calls hang or are refused.
- **Model:** none.

### deployed-upkeep

- **Status:** later. This is Renovate plus upstream's `almanacs/renovate`, not a workflow of ours.
- **Why:** on 2026-11-07 only dotfiles and dotfiles-devcontainer had Renovate, and most deployed-tier repositories had no update bot at all. The gap is that update pull requests never open.
- **Steps:**
  1. Turn on Renovate for deployed-tier repositories, including Docker base images and digests. On the WordPress droplet's repository, that would have flagged the end-of-life base images before the eMudhra advisory did.
  2. Run upstream's `almanacs/renovate` unchanged to repair the Renovate pull requests that break. It shares the GitHub App that continue-work sets up.
  3. Its repairs are agent work, so they run only under the budget rule. Upstream caps each session by tokens, turns, and dollars, but not by the five-hour window.
- **Model:** upstream's, for repair.

### eval-queue

- **Status:** later, after M4's budget rule. An idea from 2026-11-08, not yet a design.
- **Does:** runs queued plugin evals as agent work in hours the budget rule says are free. Evals spend the same five-hour window as everything else. In 2026-09 to 2026-11 they were 0.6% of assistant messages, on six days, and most of them already ran around 02:00, so the saving is small today. The idea stands for when evals grow.
- **Model:** yes, under the budget rule.
- **Needs:** a queue the `claude plugin eval` runner can read. Burst detection excludes eval sessions by their `/private/tmp` project path and the budget rule counts them, decided 2026-11-08.

### container-logs

- **Status:** blocked on per-call logging in brineworks.
- **Watches:** brineworks container logs.
- **Reports:** failures that don't reach an OpenClaw transcript.
- **Model:** none.

## Decided against

- **Telegram alerts.** The issue is the alert.
- **Filing to a public repository.** Issue bodies carry private text.
- **Transcripts from the dev VM.** They are test traffic and would file noise.
- **Work repositories.** I can't reach them from here.
- **Closing a task on a model's word.** An agent can find evidence. Only code that checks the evidence closes a task.
