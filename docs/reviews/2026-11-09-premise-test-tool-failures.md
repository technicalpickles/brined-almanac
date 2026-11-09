# Premise test: tool-failures, 2026-11-09

[vision.md](../vision.md) wants agent work to pick up what almanac reports and carry it to a fix. That only works if a report carries enough to act on. This test took the fattest cluster tool-failures had filed and tried to drive it to a fix from the issue bodies alone, writing down every fact that had to come from somewhere else. The cluster was `brineworks/triage_apply`: six issues, brineworks #137 to #142, filed by the first live run on 2026-11-08.

## What the issues were

All six were resolved before almanac filed them.

| Class | Failures | Issues | What it was | Fixed by | Fixed when |
|---|---|---|---|---|---|
| mcp-timeout | 11, 2026-11-02 to 11-06 | #138 to #141 | `triage_apply` made three Gmail `threads.modify` calls per thread inside one MCP request. A 93-thread session was about 279 Gmail requests and outran the MCP client timeout. | brineworks #122, one `messages.batchModify` per session, deployed as 1a8d8f7 | merged 2026-11-07 00:36Z; verified on prod 2026-11-08 16:24Z (taskwarrior `bbbb3091-c60a-4423-ad66-fc911b9e8287`), three and a half hours before almanac filed |
| approval-timeout | 12, 2026-11-04 | #137 | Nobody tapped the five-minute approval card, and Brent retried, posting a new card each time. Sessions 44 and 46, every failure 300.1s long. | pickleclaw #60, `timeoutReason` so an expired card comes back as a blocked result that says stop and ask for "apply" | merged and deployed 2026-11-04 |
| other, "no approval route" | 3, 2026-10-31 | #142 | OpenClaw's approval plugin failing closed on a surface that cannot carry an approval card. All three are one Open WebUI session on the day the Open WebUI pipe went live (picklehome #165, #167). | not fixed; known since the 2026-11-04 session review | task `4d3aa0e9-57cf-40db-a671-dc9a7d4ed463` filed today to decide whether that surface gets a route or stays read-only for gated tools |

Verified from the pulled transcripts, not from the pull request descriptions. Every `-32001` on this tool is from sessions 40 to 48, all before the #122 deploy. After it, nine `triage_apply` runs (sessions 48 to 56) with no timeouts, and the 93-thread session 48 finished in 81 seconds. The one failure after 11-07 is the new "Approval card expired with no answer" result from #60, followed by no retry, and the next call ten minutes later succeeded.

## What the body gave, and what it didn't

The body gave the tool, the service, the error class, the day, a count, first and last timestamps, and one raw sample. That is enough to know something failed and not enough to know what to do.

Every missing fact below is already in the transcript record that `transcript.rs` parses. None of them needs a new source.

| Needed to act | Where it already is | Why it mattered here |
|---|---|---|
| The surface the call came from | the transcript file name (`telegram-direct`, `openwebui`, `cron`) | It is the whole answer for #142. Telegram has an approval route, Open WebUI does not. |
| The tool's input | `details.input` (`{"session": 48}`) | It ties the six 11-06 failures to one session, and that session to the one #122 names. |
| How long the call ran | `details.startedAt` and `details.timestamp` | 300.1s is the approval card timing out. 70 to 118s is Gmail work. The two classes read the same without it. |
| Whether it was a retry loop | the same session number across the failures | Six failures in 18 minutes was one session retried, not six problems. |
| Whether it has recurred since | the per-day key hides it; `last` is in the body but nothing compares it to anything | The cluster's newest event was two days old when the issue was filed. |
| The error itself | the sample, which OpenClaw's untrusted-content fence wraps, so the 500-character budget is mostly fence | The useful part is 38 characters, `MCP error -32001: Request timed out`. |
| A link to the fix | nowhere | #122's body names the tool and the error. A merged pull request in the target repository since `last` that mentions the tool would have caught all four mcp-timeout issues before filing. |

## Two things the run showed about the workflow itself

**The per-day key turns one problem into five issues.** The 11-02, 11-04, 11-05 and 11-06 mcp-timeout issues are one bug. The spec's Dedupe section chose a new id per day so a recurring problem files again tomorrow. The cost is that the reader, or a worker, gets a stack of issues with no thread between them, and nothing to close when the bug is fixed except each one by hand.

**A first live run files stale clusters as if they were new.** The 2026-11-08 backfill filed every cluster in the pulled transcripts that crossed its threshold, including ones two days stale and one already fixed. The 2026-11-07 pre-prod batch (brineworks #123 to #134, pickleclaw #61 to #77) is the same thing from an earlier store: clusters from 2026-10-22 to 10-27 under the old generic title. They are not duplicates of the 11-08 issues. The 10-24 `pg-too-many-clients` ones match brineworks' 2026-10-24 Postgres connection exhaustion incident, which is written up and fixed.

## What to change

Filed as taskwarrior `b392d2f4-2245-4df9-aeac-0f3ded72466d` (brined-almanac):

- Key clusters by `service:tool:class`. The day moves into the body as a list of days with counts. Recurrence becomes a comment on the existing issue, which needs an update or comment effect that almanac does not have yet ([ivy/almanac#6](https://github.com/ivy/almanac/issues/6) is the closest proposal).
- Put the surface, the tool input, the duration range, and the number of distinct sessions in the body. Cut the fence out of the sample.
- On the first live run after a store reset, roll clusters whose last event is older than 48 hours into one backlog issue per repository instead of filing each.
- Before filing, check the target repository for a merged pull request since `last` that names the tool. That is the task-reconciler's job in M2, and this run is the argument for pulling the cheap version forward.

## What this says about the roadmap

The watcher worked. tool-failures found a real bug, clustered it correctly, and classified three different failures into three classes. Nobody read what it produced, and by the time someone did, the fix had already landed another way. Josh saw the timeouts in Telegram, filed `bbbb3091` by hand on 11-04, and fixed it on 11-06.

So the gap between "almanac filed it" and "something happened" is a missing consumer, not a missing watcher. Josh's call on 2026-11-09: the consumer is unattended work that takes a report to a state he can review, not a hook that shows him the list at session start. At most, a skill does the lookup for him in a session he already opened.

What this test did by hand is the job description for that worker. Read the report. Find the tool in the target repository. Check merged pull requests since the report's `last` timestamp for one that names the tool. Check the transcripts for recurrence after that merge. Then either close the report with the evidence, or branch, fix, and open a pull request. Four of the six issues here end in a close with evidence, which is the cheaper of the two outcomes and still work worth taking off Josh.

Two things have to be true before that worker runs unattended. The body changes above, because a worker reads the body and nothing else. And a place to run that can read the transcripts, since the recurrence check needs them and they live on the Mac. The budget rule in [vision.md](../vision.md) says when. The first version does not need almanac to start it: a scheduled headless Claude Code run that invokes one skill, with the same skill usable by hand in a session, tells us whether the output is reviewable before almanac grows a runner (continue-work, M4).
