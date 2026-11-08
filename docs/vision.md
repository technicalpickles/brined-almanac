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
- **Ops and security work.** On 2026-11-07, 13 of my repositories had 286 open Dependabot alerts. 103 were on jeweler, which is retired. dotfiles, cq and pickled-claude-plugins, which I use every day, had 34 between them. Nothing turns an alert into work I've planned, so it waits until a burst happens to land in that repository.

Hosts are worse, because no alert exists. The clearest case is a few WordPress sites I host on a droplet. On 2026-10-20, eight of eMudhra's intermediate certificate authorities turned out to have recoverable private keys. I checked every container on the droplet by hand. Nearly all of them still trusted the compromised chains, and WordPress ships its own CA bundle, so patching the OS wasn't enough. While I was orienting for that fix, I found that one of the sites had been compromised. Webshells first ran on 2026-08-31, and on 2026-09-14 its web root was replaced with an SEO-spam dropper. The site had returned HTTP 500 for weeks, and nothing reported it. The fix took two days and left six follow-up plans, among them hardening, a database upgrade, and base images on a Debian release past end of life with no CA fix. None of that work was on the backlog until the advisory made me look.

I don't need more output during bursts. I need the work to keep going between them, instead of waiting for the next burst.

## The thesis

brined-almanac is a steady lane under the bursts. It has two tiers.

**Watchers** make no model calls. They read transcripts, the forge, the backlog, and the services, and they report what they find. They cost nothing against my usage limits, so they run all the time.

**Agent work** uses a model to keep my projects moving. Mostly that means continuing what a burst left unfinished, from a parked handoff or an open task through to a pull request. It also judges what code can't decide and makes small repairs. It runs only on capacity I am not using, under the budget rule below.

Upstream's [vision](https://github.com/ivy/almanac/blob/main/docs/vision.md) describes almanac as how things outside the plan get into it, and it names vulnerability response as the same shape of problem as broken dependency updates. Here the plan is taskwarrior. Ops and security work that nobody scheduled, like an open advisory on a deployed app, is exactly what should get in.

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

## Work continues between bursts

When a burst ends, its work usually isn't finished. It's parked in a handoff, left as open tasks, or sitting in a pull request with review comments. Agent work picks it up and keeps going on capacity I'm not using, until the work is done or it needs me.

- **What it picks up.** Resumable parked handoffs, which already say what comes next. Open tasks in repositories I've declared fair game, unless a task is tagged `+mine`. Follow-ups on its own pull requests, such as review comments and failing checks.
- **How far it goes.** As far as the work goes. It writes code, runs the tests, and opens or updates a pull request. The limits are the budget rule, the sandbox, and the repository's rules, which still decide what merges.
- **When it stops.** When the window runs out, a burst starts, or it reaches a decision only I can make. It parks its state in a handoff, the same way my own sessions do, and the next window picks up from there. A burst that starts in the same repository finds that handoff first.
- **Less stays mine.** Every stop that needs me records why: a deploy, a host, a sign-in, a decision, or a stale handoff. Those reasons get counted each week, and the common ones become tasks to give workers a safe way through that step. The set of things only I can do should shrink over time.
- **Who does the work.** Almanac decides what continues and when. A worker does the work: a headless Claude Code session in a container, using upstream's rootless Podman sandbox. It gets a fresh clone and the handoff or task text, and no credentials. It returns a patch, code checks it, and almanac pushes it through a GitHub App, the same path upstream's own agents take. The App is installed only on fair-game repositories, and its name on a pull request tells me a worker wrote it. Upstream's [vision](https://github.com/ivy/almanac/blob/main/docs/vision.md) describes the same split, with almanac upstream of a tracker and workers.

## Small steps

Some steps are small enough that a workflow takes them itself, without a worker. Examples are bringing a pull request's branch up to date with main, or rerunning a CI job that failed for a reason unrelated to the change.

A small step goes through almanac's closed set of effects, like everything else a workflow does. Updating a branch is a `Push`, which commits a checked patch to the pull request's own branch. The forge refuses the patch if the branch moved after the check. Rerunning CI has no effect yet. The narrow version is one rerun per commit, only for a failure code classifies as infrastructure, with an `Attest` that says it reran. It goes to upstream as a proposal, so other people building on almanac can say whether they need it too.

A small step that code can do, such as a clean merge of main, needs no model and ignores the budget rule. A step that needs a model, such as resolving a merge conflict, is agent work and follows the budget rule. Merging a pull request is never a small step. The repository's rules merge.

## Where output goes

A finding goes to one of two places. How the finding gets closed decides which.

**An issue, when a pull request in a private repository closes it.** The issue goes to the repository whose code changes, and merging the pull request closes the issue. pickleclaw owns OpenClaw and the apps built on it, and brineworks owns brineworks. The issue gets the `almanac` label, and the issue is the alert. tool-failures works this way today.

**Taskwarrior, for everything else.** That covers a finding about a public repository such as picklehome or dotfiles, an ops action with no pull request, and a decision only I can make, like closing a task or archiving a repository. A public repository never holds a finding, because the finding may carry transcript or task text. Findings that cut across projects, like a session-friction review or a lesson that applies to many repositories, are tasks too. Under this rule picklehome needs no owner of its own.

Both destinations sit behind almanac's `Tracker` trait, the interface that carries a workflow's `File` and `Ask` effects. A workflow says what to file and never knows where it lands. This repository will add a taskwarrior implementation next to the GitHub one, and a router that picks between them by target. The taskwarrior tracker writes to its own replica and syncs through the taskchampion sync server picklehome already runs, so it never shares my task database file. Almanac's record of effects is the contract between a workflow and its tracker. Dry runs print it, and a different tracker could apply it later. Upstream's roadmap plans the same idea for its M4, so the implementation may go upstream later.

## What it is not

- **Not a merge bot.** It reports. Branch protection and the repository's own rules merge.
- **Not the backlog.** Taskwarrior is. Almanac reads the backlog and reports on it.
- **Not a closer on a model's word.** It closes a task when code confirms the evidence, and an agent may go looking for that evidence. A model's opinion that a task is done never closes it, which is upstream's principle 4, ["a model can veto, never approve"](https://github.com/ivy/almanac/blob/main/docs/core-principles.md). Every close carries its evidence, so I can review it and reopen a mistake.
- **Not a chat surface.** No Telegram alerts. Someone else's front end can read the issues.
- **Not a reason to hit the limit.** If directed work ever stops because almanac spent the window, the budget rule is wrong.

## What success looks like

- A burst ends with work parked, and the next burst finds it moved forward: a pull request opened or updated, or a handoff that names the decision it's waiting on.
- A task that a later burst fixed is closed, or flagged as probably done, within a day.
- A burst in a repository starts with the open tasks and parked handoffs that matter there.
- A parked handoff whose work was never captured anywhere else is reported before it is forgotten.
- A maintainer's question on one of my upstream issues gets an answer within days, and an upstream fix leads to deleting my workaround.
- A service that stops is reported before I notice it.
- Deployed repositories stay current without my attention.
- An open security alert on a deployed, active, or maintained repository becomes tracked work within a day.
- Directed work never runs into the usage limit because of something almanac did.
