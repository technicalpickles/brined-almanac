# Repository tiers

The declared tier of each repository, per [workflows.md](workflows.md) "Repository tiers". [`tiers.toml`](../tiers.toml) is the declaration and [`src/tiers.rs`](../src/tiers.rs) defines the tiers. A repository can hold more than one tier. Each entry's `signal` is what would say the thing is in use, not only developed; it feeds the open question on computed dormant in [open-questions.md](open-questions.md).

`tiers.toml` is compiled into the binary, so changing it needs a rebuild and deploy, while `local/tiers.toml` is read at startup.

Only repositories that are public, or that a public repository already names, appear in `tiers.toml`. Each repository's `name` is `owner/repo`, and the repo part must be a valid taskwarrior project name (lowercase `[a-z0-9_-]`, dotted segments), because security-alerts files to `task:<repo name>` and fails the scan loudly on a name that is not.

Ten more private repositories are declared in the gitignored `local/tiers.toml`, whose durable copy is a document in the `brined-almanac` 1Password vault, next to the prod secrets.

## Snapshot, 2026-11-08

Human commits since 2026-10-01: dotfiles 203, picklehome 171, brineworks 103, pickleclaw 91, pickled-claude-plugins 82, brined-almanac 52, feeder-journal 50, openclaw-workspace 36. Since 2026-09-01: nikke-roster-scanner 35 (none since 11-01), second-brain-agent 2.

The picklelab services with no repository of their own (open-webui, woodpecker, taskchampion-sync, obsidian-sync, backup, disk_monitor, disk-hygiene, climate-auto-switch, github-actions-runner) are owned by picklehome and have no entry.

## Handed off and retired

homesick is handed off and jeweler is retired, per [workflows.md](workflows.md). Neither is cloned on the Mac.

## Unclear

- feeder-journal: deployed somewhere, or a CLI run by hand?
- sb: also installed on the work machine?
- pickled-coi: active, or dormant until the next VM change?
- welcome2u: still runs on shell open anywhere? Then it is deployed.
- Which picklelab services map to repositories not listed here?
