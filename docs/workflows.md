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

A repository's tier decides which workflows watch it. Tiers are declared in code, and moving a repository between tiers is a one-line change.

| Tier | What it is | What watches it |
|---|---|---|
| dormant | Not being worked on and not deployed, like an iOS app on hold. | Nothing. Its tasks are marked as parked, so they don't count as stale. |
| deployed | Running somewhere and in use, but not being developed, like a hobby web app I still use or the WordPress sites I host. | Service health, dependency and security updates, and drift between main and what is deployed. |
| active | Being worked on now. Today these are pickleclaw, brineworks, and picklehome. | Every workflow. |
| maintained | Public and used by other people, like bktide and pickled-claude-plugins. | Outside issues and pull requests, plus whatever its deployed or active tier adds. |
| retired | Public, unmaintained, and still collecting issues, like jeweler and homesick. | Nothing. The open question is whether to archive them on GitHub so they stop collecting issues. |

A repository can be in more than one tier. bktide is maintained and active while I work on it.

## Rules every workflow follows

- A workflow files an issue only when a pull request in a private repository closes the finding. Everything else, including findings about public repositories like picklehome or dotfiles, ops actions, and decisions, goes to taskwarrior. Issue bodies can carry transcript text or task text, so nothing files to a public repository. "Where output goes" in [vision.md](vision.md) has the full rule.
- Work repositories are out of scope. I can't reach them from here.
- A workflow that calls a model follows the budget rule. A watcher calls no model.
- Every workflow has a dry run that needs no database and no token.

## Catalog

### tool-failures

- **Status:** shipped.
- **Watches:** OpenClaw transcripts pulled from picklelab.
- **Reports:** one issue per cluster of failed tool calls, grouped by service, tool, error class, and day, once a cluster crosses its threshold. Brineworks failures go to brineworks, and everything else goes to pickleclaw.
- **Model:** none.
- **Design:** [the tool error scan design](superpowers/specs/2026-11-06-tool-error-scan-design.md).

### task-reconciler

- **Status:** next.
- **Watches:** pending taskwarrior tasks for active and deployed repositories.
- **Closes:** a task when code confirms one of these:
  - a merged pull request or a commit on main names the task by UUID
  - the task cites a pull request that has merged, or an issue that has closed
  - an agent's proof checks out (see below)

  Each closed task gets the evidence as an annotation and the `+almanac` tag. One taskwarrior task per day lists what closed, so I see every close and can reopen a mistake.
- **Reports:** tasks that are probably done on weaker evidence, such as a merged pull request that touched the files the task cites, or a cited line that no longer looks the way the task describes. The report is one task per project per day.
- **Evidence hunt:** for a task code can't decide, an agent reads the task and the repository and returns a proof. A proof is a list of checks code can run: a commit SHA on main, a file that must contain or lack a pattern, a test that must exist and pass, an issue or pull request that must be closed. Code runs every check. The task closes only if all of them pass. If the agent finds the work isn't done, it annotates the task with what's left, which is useful on its own. The proof is agent output, so it is untrusted until code has checked it.
- **Model:** none for the UUID and cited-link checks. The evidence hunt is agent work under the budget rule, and suits the end of a window that would otherwise expire.
- **Why first:** it shrinks the backlog that the task-resurfacer has to choose from, and its first version needs no model. parked-handoffs checks the same kinds of evidence, so the two share that code.

### security-alerts

- **Status:** next.
- **Watches:** Dependabot, code scanning, and secret scanning alerts on deployed, active, and maintained repositories, through the GitHub API.
- **Reports:** one item per repository per day that lists its open alerts by severity, and says whether an open Renovate or Dependabot pull request already fixes each one. A fix that is a pull request in a private repository files there as an issue. Everything else is a task, including alerts on public repositories and a leaked secret to rotate.
- **Model:** none.
- **Later:** upstream's [roadmap](https://github.com/ivy/almanac/blob/main/docs/roadmap.md) M2, "Exposure", joins a CVE feed against an inventory of what each repository, image, and host contains. When it lands, use it for the images and hosts on picklelab and the WordPress droplet instead of writing that here. A Dependabot alert covers a lockfile. It says nothing about a host's base images, its CA package, or a release past end of life, which is what the eMudhra advisory turned on.
- **Retired repositories:** their alerts are not reported. The count is evidence for the open question about archiving them.

### task-resurfacer

- **Status:** next, after task-reconciler.
- **Watches:** pending tasks and resumable parked handoffs, ranked per repository by age, priority, and recent activity.
- **Reports:** a short list of open tasks and handoffs for each repository, which a Claude Code SessionStart hook shows when a session starts there. Almanac computes the list. The hook only reads it, because almanac is not a chat surface.
- **Model:** none.

### parked-handoffs

- **Status:** next.
- **Watches:** `.parkinglot/*.md` handoffs in every checkout under `~/github.com`, and the vault's.
- **Reports:** each handoff older than a week, sorted into one of three groups by evidence in code:
  - **captured.** Its branch merged, its pull request closed, or a task names it. It can go.
  - **resumable.** Its branch or worktree still exists and nothing else tracks the work.
  - **orphaned.** Nothing else tracks the work and its branch is gone. Only the handoff remembers it.

  Orphaned handoffs matter most. Deciding what to do with one is my call, so the workflow files one task per repository per day.
- **Model:** none.
- **Needs:** read access to the checkouts. The compose stack in [the prod design](superpowers/specs/2026-11-06-prod-on-the-mac-design.md) mounts `~/github.com` and the vault read-only. Add the mount to that design when this workflow is built.

### oss-maintained

- **Status:** next.
- **Watches:** issues and pull requests from other people on maintained-tier repositories, through almanac's native GitHub source.
- **Reports:** a pull request that needs my review, an issue that waits on my answer, or a thread that has gone quiet.
- **Model:** none to start.
- **Needs:** a GitHub App, following upstream's [deploying guide](https://github.com/ivy/almanac/blob/main/docs/deploying.md).

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
- **Watches:** my Claude Code transcripts under `~/.claude/projects/`, read through [cq](https://github.com/technicalpickles/pickled-claude-plugins/tree/main/plugins/cq). tool-failures does this for Brent's transcripts. This workflow does it for mine.
- **Reports:** a weekly friction review, like the one I did by hand for picklelab on 2026-10-18. It counts sandbox denials, auto-mode denials, failing tool calls, hand-backs to me, usage-limit stops, and repeated attempts at the same command. Each item gets its count, the sessions it came from, and whether a task already tracks it.
- **Model:** none for the counts, which are SQL over cq. A model may rank the items and write the summary, under the budget rule. This work suits the end of a window that would otherwise expire.
- **Feeds:** lesson-spread. A friction item with a fix becomes a lesson.

### lesson-spread

- **Status:** later, after session-friction.
- **Watches:** lessons I already record. Those are devlog entries in the vault, Claude Code memory files, rules in dotfiles, and commits that fix a gotcha.
- **Reports:** other repositories where a lesson applies and has not been applied. A repository qualifies when it uses the same tool and lacks the fix. For example, a lesson about hk's `builtins.gitleaks` scanning gitignored files applies to every repository with an `hk.pkl` that uses it.
- **How:** each lesson names a check that code can run against a repository, such as "has `hk.pkl`" plus "uses `builtins.gitleaks`". Where a lesson can't name such a check, a model may judge whether it applies, under the budget rule.
- **Prefer structure:** when a lesson applies to every repository, the better fix is a rule in dotfiles or a lint, not an issue per repository. The report says so.
- **Model:** none for lessons with a check.

### service-down

- **Status:** later.
- **Watches:** the health of services on picklelab and of deployed-tier apps, including the WordPress sites. One of them returned HTTP 500 for weeks while it was compromised, and nothing reported it.
- **Reports:** a service that is down or restarting in a loop.
- **Model:** none.
- **Open question:** picklehome already has one-signal watchdogs written in bash. Decide whether this workflow replaces them or only covers what they don't.

### merged-on-red

- **Status:** later.
- **Watches:** merges to main on active repositories and their CI status at merge time.
- **Reports:** a pull request that merged before CI finished or with a failing check, and whether main went red after it.
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

- **Status:** later.
- **Watches:** authentication errors in transcripts and service logs.
- **Reports:** a credential that has started failing, such as an expired OAuth refresh token.
- **Model:** none.

### deployed-upkeep

- **Status:** later.
- **Watches:** dependency and security updates on deployed-tier repositories.
- **Reports and repairs:** a pull request that is broken or stale. Repair is agent work under the budget rule.
- **Model:** yes, for repair.
- **Open question:** whether to run upstream's `almanacs/renovate` on these repositories instead of writing this workflow.

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
