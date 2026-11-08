# Open questions

Questions about [vision.md](vision.md) and [workflows.md](workflows.md) that need my answer. Remove a question when it is answered, and record the answer in the doc it changes.

Open as of 2026-11-08, from the [work pattern review](reviews/2026-11-08-work-pattern-review.md). Two questions from that review, how to treat eval sessions and where repository attribution comes from, were answered the same evening from the transcripts and are recorded in [workflows.md](workflows.md) under session-friction and eval-queue.

## Who owns the dead-man's switch list?

A check that reports the age of a scheduled job's last successful run needs to know which jobs exist and how old is too old.

What exists today, read from the repositories on 2026-11-08:

- picklehome's watchdogs on picklelab are systemd oneshot services on timers (`OnUnitActiveSec=5min`, `OnBootSec`). They record nothing themselves beyond an alerted-marker file. systemd already records each unit's last start time and result, readable with `systemctl show`, so a host check can report every timer's last-success age with no change to the jobs.
- The almanac stack on the Mac has compose healthchecks on Postgres, the daemon's `/healthz`, and the backup container (a dump file younger than 25 hours). The puller logs each pull to stderr and writes no last-success marker, so its health is only "the container is up".

The options:

1. **Each host ships its own check.** A `check-job-age` executable reads systemd's own records on picklelab and a small list on the Mac (compose health plus a puller marker file), and reports in the Nagios shape. Almanac only reads the host-state report.
2. **Almanac keeps the list.** One file here names every job on every host and its max age. The host check is generic and almanac compares.
3. **Both.** Hosts check their own jobs from systemd records. Almanac's list covers only its own puller and daemon, which it already knows about.

Leaning: option 3, because systemd already holds the picklelab answer and the Mac's jobs are almanac's own. The one change the jobs need is a puller marker file. The decision is mine because it fixes where new jobs get registered.

## What signals say a repository is in use, not just in development?

Dormant stays declared (see [workflows.md](workflows.md) "Repository tiers"). Computing it later needs usage signals per repository. What is observable from the Mac today, checked 2026-11-08:

| Repository or thing | Usage signal | Observable from the Mac |
|---|---|---|
| pickleclaw, brineworks | Brent's transcripts show messages received | Yes, the puller already fetches them |
| cq | The index file's modification time | Yes, `~/Library/Caches/cq/index.duckdb` |
| pickled-claude-plugins | Skill and hook invocations in my transcripts | Yes, through cq |
| picklehome watchdogs | systemd timer last-run | Only through a host-state report |
| WordPress sites, hobby web apps | HTTP access logs or site checks | Only through a host-state report |

What I have to supply: there is no `local/tiers.toml` yet, so no repository is declared in any tier. The list of deployed-tier repositories, with what each one does when it is in use, is the context this question waits on. Without it the table above cannot be completed, and computed dormant has nothing to compare against.

## What does session-friction need from cq?

cq is the tool, decided 2026-11-08. Checked against cq v0.11.0 on origin/main:

- **Already there:** `--no-reindex`; smart sync that try-locks the index and skips the sync when the lock is busy; recreation serialized through `index.lock`; a thread and memory cap per process; staleness reported on stderr. The 2026-11-08 hang came from a v0.7.0 binary that predates the cap, not from a gap in v0.11.0.
- **Integration points still to build:** a repository column derived from each message's `cwd` and tool-call paths, since `project` alone is "github.com" for most recent sessions; a daemon-friendly way to query without owning the index, or a scheduled export.
- **Ops:** the index sync on a schedule so the daemon never triggers one; the memory cap sized beside the compose stack.

Open part: whether the repository column and the export mode are cq features or almanac-side SQL over cq's existing views. Both work; the first helps every cq user.
