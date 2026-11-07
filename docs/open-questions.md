# Open questions

Questions about [vision.md](vision.md) and [workflows.md](workflows.md) that need my answer. Remove a question when it is answered, and record the answer in the doc it changes.

## Scope

- Archive the retired repositories on GitHub so they stop collecting issues? That is a one-time cleanup, not a workflow.
- Tier assignments will live in code, and this repository is public. Is it fine for code to name private repositories, or do tier assignments live in a gitignored file?

## Where output goes

- How does the prod container write to taskwarrior? It can mount `~/.task` read-write and run `task` itself, or sync through a taskchampion sync server. A read-write mount means the container and my own `task` commands write the same files at the same time.
- How does the taskwarrior tracker avoid filing the same finding twice? GitHub issues get this from almanac's store. A taskwarrior task could also carry the delivery key in a user-defined attribute, so a lost store doesn't refile everything.

## Agent work

- Closing a taskwarrior task needs a new effect. Is it a method on the taskwarrior tracker, or an effect of its own? Does it also suit upstream, whose M4 plans a tracker provider?
- Is a UUID in a merged pull request or a commit on main the right bar for closing a task? A commit can mention a task it only touches, as in "part of <uuid>".
- Rerunning CI needs a new effect. Add it in this repository, or propose it upstream? Which principle covers it?

## Workflow design

- service-down. Does it replace picklehome's bash watchdogs, or cover only what they don't?
- deployed-upkeep. Run upstream's `almanacs/renovate` on deployed repositories, or write a workflow?
- session-friction. Weekly, or on another schedule? Which friction counts belong in the first version?
- lesson-spread. Which recorded lessons does it read first: devlog entries, memory files, or dotfiles rules?

## Roadmap

- Is [roadmap.md](roadmap.md)'s order right? It puts the taskwarrior tracker first, because every "next" workflow files to taskwarrior. It moves task-resurfacer up to follow the backlog work it depends on, and session-friction down to ship with the budget rule.
- Where does moving the host to picklelab fall relative to these workflows?
