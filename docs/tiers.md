# Repository tiers

The declared tier of each repository, per [workflows.md](workflows.md) "Repository tiers". This file is the declaration until M1 moves it into code. A repository can hold more than one tier. "Usage signal" is what would say the thing is in use, not only developed; it feeds the open question on computed dormant in [open-questions.md](open-questions.md).

Only repositories that are public, or that a public repository already names, appear here. Ten more private repositories are declared in the gitignored `local/tiers.toml`, whose durable copy is a document in the `brined-almanac` 1Password vault, next to the prod secrets. Commit counts are human commits since 2026-10-01 from the 2026-11-08 survey.

## Active

| Repository | Commits | Also | Usage signal |
|---|---|---|---|
| picklehome | 171 | deploys picklelab | its timers and units run |
| brineworks | 103 | deployed, fair game for workers | Brent's transcripts show triage and email calls |
| pickleclaw | 91 | deployed | Brent's transcripts show messages received |
| pickled-claude-plugins | 82 | maintained | skill and hook invocations in my transcripts |
| brined-almanac | 52 | deployed on the Mac | daemon `/healthz`, issues filed |
| dotfiles | 203 | maintained | every shell on every machine |
| openclaw-workspace | 36 | | in use whenever Brent runs |
| feeder-journal | 50 | | unclear, see below |

## Deployed

| Repository or service | Where | Usage signal | Note |
|---|---|---|---|
| nikke-roster-scanner | picklelab `nikke` service | the sync job's last run | 35 commits since 09-01, none since 11-01 |
| second-brain-agent | picklelab service | its unit runs | 2 commits since 09-01 |
| sb | my shell and Claude sessions | `sb` invocations in transcripts | no commits since 08-16, used daily by devlog |
| picklelab services with no repository of their own | open-webui, woodpecker, taskchampion-sync, obsidian-sync, backup, disk_monitor, disk-hygiene, climate-auto-switch, github-actions-runner | each unit or timer runs | owned by picklehome |

## Maintained

| Repository | Why | Lag on 2026-11-08 |
|---|---|---|
| pickled-claude-plugins | home, work, VM; installed as plugins | 12 origin branches with no local branch |
| cq | home, work; an outside contributor | checkout 69 commits behind origin; binary shadowed |
| dotfiles | home, work, VM | 7 origin branches with unmerged work |
| bktide | public, outside bot pull request | main unchanged since July |
| sb | home, possibly work | unclear, see below |
| bb-plugin-taskwarrior | plugin for get-bb/bb | no commits since 09-19 |

## Dormant

| Repository | Last human commit | Note |
|---|---|---|
| chirpfinder | 2026-10-28 | iOS app on hold, 56 pending tasks |
| pirpg | 2026-11-01 | 29 pending tasks, CI on a homelab runner |
| a2a-experiments, a2a-cli | 2026-08 | 26 pending tasks |
| llamafit | 2026-08-12 | 10 pending tasks |
| bowerbird | 2026-08-03 | 22 pending tasks |
| cenv | 2026-08-08 | Mac-only by design |
| herdr-keymap | 2026-08-02 | |
| petri-dish | 2026-05-29 | |
| fitout | 2026-03-10 | |
| town-charter, xmcp, homelab-dev, brineworks-workspace, rust-embedded-scripting-bench, dotfiles-devcontainer | autumn 2026 | a few commits each |
| pickled-coi | 2026-08-28 | unclear, see below |
| welcome2u | 2026-02-13 | unclear, see below |

## Handed off and retired

homesick is handed off and jeweler is retired, per [workflows.md](workflows.md). Neither is cloned on the Mac.

## Unclear

- feeder-journal: deployed somewhere, or a CLI run by hand?
- sb: also installed on the work machine?
- pickled-coi: active, or dormant until the next VM change?
- welcome2u: still runs on shell open anywhere? Then it is deployed.
- Which picklelab services map to repositories not listed here?
