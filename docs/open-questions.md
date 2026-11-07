# Open questions

Questions about [vision.md](vision.md) and [workflows.md](workflows.md) that need my answer. Remove a question when it is answered, and record the answer in the doc it changes.

## Workflow design

- service-down. Does it replace picklehome's bash watchdogs, or cover only what they don't?
- deployed-upkeep. Run upstream's `almanacs/renovate` on deployed repositories, or write a workflow?
- session-friction. Weekly, or on another schedule? Which friction counts belong in the first version?
- lesson-spread. Which recorded lessons does it read first: devlog entries, memory files, or dotfiles rules?

## Roadmap

- Is [roadmap.md](roadmap.md)'s order right? It puts the taskwarrior tracker first, because every "next" workflow files to taskwarrior. It moves task-resurfacer up to follow the backlog work it depends on, and session-friction down to ship with the budget rule.
- Where does moving the host to picklelab fall relative to these workflows?
