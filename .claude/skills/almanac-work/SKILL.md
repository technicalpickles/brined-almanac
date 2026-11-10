---
name: almanac-work
description: Use when asked to work, triage, clear, or check an almanac-filed report (a GitHub issue labelled `almanac` in technicalpickles/brineworks or technicalpickles/pickleclaw, or a taskwarrior task with an `almanac_key`), to take one to a state Josh can review, or when a scheduled unattended run invokes it. Also when Josh asks whether an almanac issue is still real or what almanac filed.
---

# almanac-work

Take one almanac report to a state Josh reviews in a minute. The report is a finding from a watcher ([docs/workflows.md](../../../docs/workflows.md)). It may be stale, already fixed, or not a bug. The job ends in one of three shapes below, never in a summary.

Arguments: `/almanac-work <repo | issue url | task uuid> [--dry-run]`. With `--dry-run`, write every comment, command and diff to the session scratchpad instead of posting, and say so in the report.

## Pick one case

- Given a repo, list its queue and take the report with the newest `last` timestamp. Something still happening beats history.

  ```bash
  gh issue list -R technicalpickles/<repo> --label almanac --state open --json number,title,body
  task rc.uda.almanac_key.type=string almanac_key.any: status:pending project:<repo> export
  ```

  The `rc.uda` override is needed until dotfiles defines the attribute. Without it the filter matches every task.

- A case is every open report with the same cause, not one issue. Start from the reports that share the tool, or the error class and day, and let the evidence widen or narrow the set. Work them together and close them together.
- Find siblings from the `service`, `tool` and `day` lines in each body. Reports filed before 2026-11-08 all carry the title `Brent: failed tool calls`, so a scan of titles misses them.
- Register the session on the status page with the `pickleclaw-status` skill when the repo is brineworks or pickleclaw.

## Find out what it is

The body gives tool, class, day, count, first, last, and one sample. Everything else comes from these places, read-only. Fetching in Josh's checkouts is fine. Changing their working trees is not.

| Question | Where |
|---|---|
| Is it still happening, how often, how long do calls take, which surface | `python3 .claude/skills/almanac-work/scripts/tool-calls.py <tool> --since <day>` over the pulled transcripts. Use the report's `last` to see whether it continued, and the fix's merge date for the recurrence check. `--help` has the options, `--list` names the tools |
| Was it fixed | `gh pr list -R <repo> --state merged --search <tool>` and `git log --since=<last>` on the file that defines the tool; `docs/incidents/`, `docs/journal/`, `docs/retrospective/` in the target repo; `task status:completed description.has:<tool>` |
| Is the fix deployed | brineworks: `/health` returns the running sha, compare with `git merge-base --is-ancestor`; pickleclaw: `openclaw-ro --target picklelab drift`. Either proves what runs now, not when it shipped. Say so |
| What the tool does | the target repo's CLAUDE.md, then the file that registers the tool |

Decide: fixed, by design or wrong repo, real and open, or cannot tell. A model's reading closes nothing. A close names a merged pull request or a measured absence.

## End state

**Fixed.** Close every issue in the case (unattended, post the proof as a comment and report the close commands instead). The issue you were given, or the newest one, carries the proof, and the siblings carry one line pointing at it. The proof has these parts, in this order:

1. The cause, one paragraph, naming the file.
2. The fix: pull request, merge date, and the evidence it is deployed.
3. The recurrence check: the script's summary line, calls and errors since the merge.
4. What was left untouched, and the follow-up tasks filed for it.

Then fix what now lies. A status line in an incident doc that still says "not yet deployed" gets a pull request or a task.

**By design, or another repo's problem.** Close with the proof above, file a task under the right project, and link it.

**Real and open.** Branch in a worktree, failing repro first, fix, pull request. Follow the target repo's CLAUDE.md for setup and tests. The body's `## Why` opens with the report, and `Closes #n` for every issue in the case. Never merge.

**Cannot tell.** One comment: what was found, what was not, what would settle it. Leave the issue open.

In every shape, follow-ups become taskwarrior tasks under the target repo's project, and the session gets a devlog entry.

## Never

- Merge, deploy, restart anything, or run commands on picklelab.
- Put transcript text anywhere public. The private repos' issues may carry it. brined-almanac and the vault get counts and timestamps only.
- Reopen a question a merged pull request already answered. Read it first.

## Unattended

One case per run, from the brined-almanac checkout, in a likely-free hour ([docs/vision.md](../../../docs/vision.md) "The budget rule"):

```bash
claude -p "/almanac-work brineworks" --permission-mode acceptEdits --settings .claude/settings.json --max-turns 60
```

Nobody can approve anything under `--print`. `--settings` loads this repository's allow rules, which Claude Code otherwise ignores in a workspace Josh has not trusted. A command outside Josh's allow rules is denied and the run carries on, so work inside them:

- **Other checkouts.** `git -C <path>` and `cd <path> && git ...` are denied, fetches included. Read the target repo's `main` with `gh api repos/technicalpickles/<repo>/contents/<path>` and `gh search code`.
- **Transcripts.** `grep` over the transcripts directory is denied. `tool-calls.py` is allowed.
- **Bash shape.** Every part of a command has to be allowed on its own. A `cd` combined with a redirect is denied, and so is a loop variable inside a command.
- **Posting.** `.claude/settings.json` allows `gh issue comment <number> -R technicalpickles/<repo> --body-file <file>` for pickleclaw and brineworks, and only in that argument order, so write the proof to a file first. `gh issue close` has no allow rule: put the exact command in the report, and Josh closes. The almanac scan resolves the Bugsink issue when he closes as completed, and mutes it on not planned. `task add` and `gh issue create` are allowed.
- **Status page and devlog.** Skip both and list them under "not done" in the report. The devlog skill is denied, and registering publishes a page.

Checked by hand on 2026-11-09: a `--dry-run` against pickleclaw loaded the skill, ran 44 turns in 5 minutes for about $1.60, and posted nothing. Still unverified: a run started by a schedule, and a run without `--dry-run`. Start with `--dry-run` and read what it would have posted.
