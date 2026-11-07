# Vision

brined-almanac keeps my projects moving between bursts of attention. It is a set of almanacs built on [ivy/almanac](https://github.com/ivy/almanac). Each one watches something I care about, reports what changed, and spends model time only when I'm not using it.

## The problem

I work in bursts. Since 2026-09-01, busy days have run 20 to 50 Claude Code sessions and four to seven thousand assistant messages. Quiet stretches follow, like the first half of October, when most days had fewer than a thousand. Of 149 five-hour usage windows in that period, 31 had more than 2,000 assistant messages, and most of those ran the full five hours. On 2026-10-26, 25 parallel sessions used up a window in 1.1 hours and a session stopped with its work committed but not pushed.

Between bursts, nothing moves, and these things go stale.

- **The backlog.** Taskwarrior holds 884 pending tasks. 558 of them have not changed in 30 days or more. Some are already done, because a later burst fixed the problem without knowing a task existed. Others never come up again, because a burst starts without knowing what is open in the repo it's working in.
- **Parked work.** A session parks its state in a `.parkinglot/` handoff so a later session can resume it. As of 2026-11-06 there are 266 handoffs across my checkouts, and 172 of them are older than 30 days. Most of that work was captured somewhere else before it was parked, as a task, a branch, or a merged pull request. Some was not, and nothing tells the two apart.
- **Open source.** I maintain public repositories with open issues and pull requests from other people, and I have about 50 open issues and pull requests on other people's projects. A maintainer's question waits for an answer I don't know is due. An upstream fix lands, and the workaround I wrote for it stays in my code.
- **Lessons.** A burst in one project finds a gotcha and fixes it there. Other projects that have the same problem keep it until a burst happens to hit it again. On 2026-10-18 I reviewed a week of my own sessions for friction by hand and found items that had cost time in up to 16 sessions each. Nobody had done that review before, and nothing repeats it.
- **Running services.** In October a service stopped after a reboot and stayed down for six days with nothing alerting. A plugin was enabled in config but never ran. Pull requests merged before CI finished, and some of them merged on red.
- **Repositories.** Repos I am not working on go weeks without a dependency update, including ones that are deployed and still in use.

I don't need more output during bursts. I need steady progress between them.

## The thesis

brined-almanac is a steady lane under the bursts. It has two tiers.

**Watchers** make no model calls. They read transcripts, the forge, the backlog, and the services, and they report what they find. They cost nothing against my usage limits, so they run all the time.

**Agent work** uses a model, either to judge something code can't decide or to do a small repair. It runs only on capacity I am not using, under the budget rule below.

Everything upstream's [core principles](https://github.com/ivy/almanac/blob/main/docs/core-principles.md) say still holds. Behaviour is code, inference is the last resort, hostile text is a type, and almanac reports while the repository decides.

## The budget rule

Agents draw on the same five-hour window I do. Upstream runs them through the `claude` CLI with a `claude setup-token` login, and it caps each session by tokens, turns, and dollars, but it doesn't track the window. The budget rule adds that.

An agent run spends nothing when either of these is true:

- It is waking hours and I am in a burst. Waking hours are about 07:00 to 24:00 US Eastern, and mornings start slowly.
- Any session is working, whether or not I am at the keyboard.

The exception is capacity that is about to expire. When the window resets soon and much of it is unused, an agent may spend it, because that capacity is lost anyway. Start with simple numbers and tune them from what happens:

- **In a burst** means a session I started wrote to its transcript in the last 30 minutes.
- **Working** means any session, mine or an agent's, wrote to its transcript in the last 5 minutes.
- **About to expire** means more than half the window is unused and it resets in less than 90 minutes.

The numbers live in code next to the rule, so changing one is a one-line change.

Each input is observable today.

- **Window use and reset time.** A headless `claude -p --output-format stream-json --verbose` run emits a `rate_limit_event`. Its `unifiedWindows.five_hour` field carries `utilization`, from 0 to 1, and `resetsAt`. The statusline JSON carries the same numbers as `rate_limits.five_hour`. Before a run starts, almanac has only its last reading, or a one-line probe on a small model.
- **Whether something is working.** A session that is working writes to its transcript under `~/.claude/projects/`.
- **Waking hours.** The clock.

Watchers ignore the budget rule.

## Small steps

A workflow may take a small, reversible step itself instead of only reporting it. Examples are bringing a pull request's branch up to date with main, or rerunning a CI job that failed for a reason unrelated to the change.

A small step goes through almanac's closed set of effects, like everything else a workflow does. Updating a branch is a `Push`, which commits a checked patch to the pull request's own branch. The forge refuses the patch if the branch moved after the check. Rerunning CI has no effect yet. Adding one needs a principle that covers it, so a convenient retry doesn't open the door to arbitrary actions.

A small step that code can do, such as a clean merge of main, needs no model and ignores the budget rule. A step that needs a model, such as resolving a merge conflict, is agent work and follows the budget rule. Merging a pull request is never a small step. The repository's rules merge.

## Where output goes

A finding goes to one of two places. How the finding gets closed decides which.

**An issue, when a pull request in a private repository closes it.** The issue goes to the repository whose code changes, and merging the pull request closes the issue. pickleclaw owns OpenClaw and the apps built on it, and brineworks owns brineworks. The issue gets the `almanac` label, and the issue is the alert. tool-failures works this way today.

**Taskwarrior, for everything else.** That covers a finding about a public repository such as picklehome or dotfiles, an ops action with no pull request, and a decision only I can make, like closing a task or archiving a repository. A public repository never holds a finding, because the finding may carry transcript or task text. Findings that cut across projects, like a session-friction review or a lesson that applies to many repositories, are tasks too. Under this rule picklehome needs no owner of its own.

Both destinations sit behind almanac's `Tracker` trait, the interface that carries a workflow's `File` and `Ask` effects. A workflow says what to file and never knows where it lands. This repository will add a taskwarrior implementation next to the GitHub one, and a router that picks between them by target. Upstream's roadmap plans the same idea for its M4, so the implementation may go upstream later.

## What it is not

- **Not a merge bot.** It reports. Branch protection and the repository's own rules merge.
- **Not the backlog.** Taskwarrior is. Almanac reads the backlog and reports on it.
- **Not a closer on a guess.** It closes a task only when a merged pull request or a commit on main names the task by UUID. It annotates the closed task with that evidence, so I can review it and reopen it. Weaker evidence marks the task as probably done, and I close it.
- **Not a chat surface.** No Telegram alerts. Someone else's front end can read the issues.
- **Not a reason to hit the limit.** If directed work ever stops because almanac spent the window, the budget rule is wrong.

## What success looks like

- A task that a later burst fixed is closed, or flagged as probably done, within a day.
- A burst in a repository starts with the open tasks and parked handoffs that matter there.
- A parked handoff whose work was never captured anywhere else is reported before it is forgotten.
- A maintainer's question on one of my upstream issues gets an answer within days, and an upstream fix leads to deleting my workaround.
- A service that stops is reported before I notice it.
- Deployed repositories stay current without my attention.
- Directed work never runs into the usage limit because of something almanac did.
