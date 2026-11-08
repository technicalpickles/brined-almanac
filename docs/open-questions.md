# Open questions

Questions about [vision.md](vision.md) and [workflows.md](workflows.md) that need my answer. Remove a question when it is answered, and record the answer in the doc it changes.

Open as of 2026-11-08, from the [work pattern review](reviews/2026-11-08-work-pattern-review.md).

## Who owns the dead-man's switch list?

A check that reports the age of a scheduled job's last successful run needs to know which jobs exist and how old is too old. The leaning is a host check, because every other check lives on the host with the thing it checks. The options:

1. **Each host ships its own check.** A `check-job-age` executable on picklelab and on the Mac reads a list on that host (job name, max age, where it records success) and reports in the Nagios shape. Almanac only reads the host-state report. One more line in the host's list adds a job. Almanac's own puller and daemon are jobs on the Mac.
2. **Almanac keeps the list.** One file in this repository names every job on every host and its max age. The host check is generic and reports every job's last-success age; almanac compares against the list. One place to read, but a host can't add a job without a change here.
3. **Both.** Hosts check their own jobs. Almanac's list covers only almanac itself, since it already knows when its puller and daemon last ran.

Deciding needs a look at how picklehome's existing watchdogs record success today, because the check has to read what they already write.

## What signals say a repository is in use, not just in development?

Dormant stays declared (see [workflows.md](workflows.md) "Repository tiers"). Computing it later needs usage signals per repository. Candidates: Brent's transcripts showing messages received (pickleclaw, brineworks); cq's index showing invocations (cq); HTTP access logs or site checks (the WordPress sites, hobby web apps); a Mac launchd job's last run (picklehome's watchdogs); an installed plugin's hook firing (pickled-claude-plugins). Which of these exist, and which repositories have none, decides whether computed dormant is possible.

## How does burst detection treat eval-harness sessions?

Plugin evals run from hash-suffixed scratch directories were 19% of assistant messages from 2026-09-01 to 2026-11-08. They spend the five-hour window like any session. They are usually in service of a specific job in a real session, so they happen during bursts. Choices: exclude them from burst detection but count them against the budget; exclude them from both; or treat them as bursts. The eval-queue idea in [workflows.md](workflows.md) would move them to likely-free hours, which makes the first choice the natural one, but it isn't decided.

## Where does repository attribution come from?

Since about 2026-10-16 nearly every session starts from `~/github.com` and works in worktrees, so a transcript's project field says `github.com` and can't name the repository. Three workflows need the repository: session-friction for per-repository counts, continue-work to stop when a burst starts in the same repository, and the budget rule to tell a burst from a worker. Options: map the file paths in tool calls to checkouts and worktrees; use the `cwd` recorded on each transcript message plus a worktree lookup; or ignore transcripts and read git, since commits and worktree writes say which repository is hot. Paths from tool calls are the most direct, and cq could expose them as a column, which ties this to the cq question below.

## What does session-friction need from cq?

cq is the tool, decided 2026-11-08. The gaps, sorted as the decision asked:

- **Integration points:** a repository column derived from tool-call paths (above); a flag or filter for eval-harness sessions; a stable way for a daemon to query without owning the index.
- **Feature gaps:** reading while another process indexes, or one long-lived reader other callers query; `--no-reindex` as the default for a secondary reader; lock waits instead of retry loops on a conflicting lock.
- **Ops:** the index sync on a schedule so the daemon never triggers one; a memory cap that fits beside the compose stack.

Which of these cq already has, and which to file as cq issues, is the open part.
