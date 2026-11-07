# Open questions

Questions about [vision.md](vision.md) and [workflows.md](workflows.md) that need my answer. Remove a question when it is answered, and record the answer in the doc it changes.

## Continuing work

- How do I mark a task as fair game for a worker? A tag such as `+steady`, a project-level opt-in, or every task in an active repository unless tagged otherwise?
- Is every resumable handoff fair game, or only ones that say so? A handoff already holds a resume prompt, so it's the easiest thing to hand a worker.
- Where does a worker run? A worktree on the Mac reads my checkouts and transcripts directly. A container is easier to sandbox. A Claude Code cloud session needs no local capacity but can't see Mac-local files.
- Whose name is on a worker's pull requests? Mine, through my token, or a GitHub App's, so I can tell its work from mine at a glance?
- How many workers run at once, and in how many repositories?
- continue-work is the main reason for agent work, and it sits in M5 behind the OSS workflows in M4. Should M5 move ahead of M4?
