---
name: almanac-work
description: Use when asked to work, triage, clear, or check an almanac-filed report (a GitHub issue labelled `almanac` in technicalpickles/brineworks or technicalpickles/pickleclaw, or a taskwarrior task with an `almanac_key`), to take one to a state Josh can review, or when a scheduled unattended run invokes it. Also when Josh asks whether an almanac issue is still real or what almanac filed.
---

# almanac-work

Take one almanac report to a state Josh reviews in a minute. The report is a finding from a watcher ([docs/workflows.md](../../../docs/workflows.md)). It may be stale, already fixed, or not a bug. The job ends in one of three shapes below, never in a summary.

Arguments: `/almanac-work <repo | issue url | task uuid> [--dry-run]`. With `--dry-run`, write every comment, command and diff to the session scratchpad instead of posting, and say so in the report.

## Pick one case

- Given a repo, list its queue and take the report whose tool failed most recently. Something still happening beats history.

  ```bash
  gh issue list -R technicalpickles/<repo> --label almanac --state open --json number,title,body
  task rc.uda.almanac_key.type=string almanac_key.any: status:pending project:<repo> export
  python3 .claude/skills/almanac-work/scripts/tool-calls.py --list
  ```

  The `rc.uda` override is needed until dotfiles defines the attribute. Without it the filter matches every task.

  Do not rank by the body's `last seen`. For a report filed from Bugsink it is the ingest time, and a replay stamps every report within seconds of each other. `--list` prints each tool's calls, errors and last real error from the transcripts, newest error first. Match a report's `service/tool` (title or body) to a row: `gog/calendar` is `gog__calendar`, `brineworks/tasks_list` is `brineworks__tasks_list`, and OpenClaw built-ins (`read`, `edit`, `exec`, `apply_patch`, `ls`) appear under their bare name. Take the report whose row has the newest last error. A tool with no row is unranked: take it only when nothing else is left, and say the transcripts could not confirm it.

- A case is every open report with the same cause, not one issue. Start from the reports that share the tool, or the error class and day, and let the evidence widen or narrow the set. Work them together and close them together.
- Find siblings from the `service`, `tool` and `day` lines in each body. Reports filed before 2026-11-08 all carry the title `Brent: failed tool calls`, so a scan of titles misses them.
- Register the session on the status page with the `pickleclaw-status` skill when the repo is brineworks or pickleclaw.

## Find out what it is

The body gives tool, class, day, count, first, last, and one sample. Everything else comes from these places, read-only. Fetching in Josh's checkouts is fine. Changing their working trees is not.

| Question | Where |
|---|---|
| Is it still happening, how often, how long do calls take, which surface | `python3 .claude/skills/almanac-work/scripts/tool-calls.py <tool> --since <day>` over the pulled transcripts. Use the script's last error to see whether it continued, not the report's `last`, and the fix's merge date for the recurrence check. `--help` has the options, `--list` names every tool, built-ins included, with its last error |
| Was it fixed | `gh pr list -R <repo> --state merged --search <tool>` and `git log --since=<last>` on the file that defines the tool; `docs/incidents/`, `docs/journal/`, `docs/retrospective/` in the target repo; `task status:completed description.has:<tool>` |
| Is the fix deployed | brineworks: `/health` returns the running sha, compare with `git merge-base --is-ancestor`; pickleclaw: `openclaw-ro --target picklelab drift`. Either proves what runs now, not when it shipped. Say so |
| What the tool does | the target repo's CLAUDE.md, then the file that registers the tool |
| Which model made the calls | the transcript's model per turn. A session pinned to a different model than the configured primary can be the whole cause |

Decide: fixed, by design or wrong repo, real and open, or cannot tell. A model's reading closes nothing. A close names a merged pull request or a measured absence.

## End state

**Fixed.** Close every issue in the case (unattended, post the proof as a comment and report the close commands instead). The issue you were given, or the newest one, carries the proof, and the siblings carry one line pointing at it. The proof has these parts, in this order:

1. The cause, one paragraph, naming the file.
2. The fix: pull request, merge date, and the evidence it is deployed.
3. The recurrence check: the script's summary line, calls and errors since the merge.
4. What was left untouched, and the follow-up tasks filed for it.

Then fix what now lies. A status line in an incident doc that still says "not yet deployed" gets a pull request or a task.

**By design, or another repo's problem.** First answer one question in the proof: would a change to the tool's description, its interface, the config, the agent's instructions, or the environment have prevented these failures? "The tool did what it was built to do" is not an answer, and neither is "the model made a mistake": a model mistake has a cause, usually a description, example or shape that led it there. If yes, the case is real and open at that layer. File the task there and say which layer, even when the failures stopped. Only when no change would have helped (an external site, a person interrupting) is it by design. A yes whose fix is not worth making can still be by design, with the reason in `fix_note`.

Then close with the proof above, file a task under the right project, and link it.

The close reason acts on the whole Bugsink fingerprint, `[service, tool, class]`, not just this issue. `completed` resolves it, so the next event reopens it as a regression. `not planned` mutes it, so a real failure that lands in the same fingerprint stays silent. When the fingerprint's class is `other`, or otherwise mixes this cause with ones that could be real, recommend `hold until <task>` for the classifier fix that splits them out, and close after it lands.

**Real and open.** Branch in a worktree, failing repro first, fix, pull request. Follow the target repo's CLAUDE.md for setup and tests. The body's `## Why` opens with the report, and `Closes #n` for every issue in the case. Never merge.

**Cannot tell.** One comment: what was found, what was not, what would settle it. Leave the issue open.

In every shape, follow-ups become taskwarrior tasks under the target repo's project, the case gets a record (below), and the session gets a devlog entry.

## Case record

Every worked case leaves one record in the repo that owns its issues, at `docs/almanac/cases/<YYYY-MM-DD>-<tool-slug>.md`. The records are labelled data: what almanac filed next to what it turned out to be, so the classifier and filer can be checked and tuned against them later. Both target repos are private, so a record may quote error text. It never goes in brined-almanac.

The frontmatter is the record. Fill every key, using `null` or `[]` when there is nothing.

```yaml
---
date: 2026-11-11
repo: pickleclaw
issues: [82, 83, 87]            # every issue in the case, given one first
fingerprint:                    # what almanac filed, from the given issue
  service: openclaw
  tool: edit
  class: arg-validation
true_class: model-error         # what the class should have been; null if the filed class was right; the dominant cause when events mix causes
verdict: by-design              # fixed | by-design | wrong-repo | real-open | cannot-tell
cause: model-wrong-arg-names    # short kebab-case slug, reused across cases with the same cause
fix_layer: config                # description | interface | config | instructions | reliability | ux | environment | external | none
fix_note: "one session pinned to kimi for 7 weeks; nothing flagged it"   # one sentence: what change at that layer prevents this, or why none would
model: kimi-k2.7-code           # the model that made the failing calls, when it mattered; else null
events: 79                      # failed calls in the case, from the transcripts
real_window: {first: 2026-09-03, last: 2026-11-09}   # from the transcripts, never Bugsink's seen times
recurrence: "34 edit calls since 2026-10-31, 0 validation errors"
close:                          # per issue: completed | not-planned | hold until <task or PR>
  82: completed
  83: hold until a47480a4
prs: []
tasks: [a47480a4-94de-4705-a389-e7e03328241f]
comments: [https://github.com/technicalpickles/pickleclaw/issues/82#issuecomment-6808045019]
evidence: [transcripts, merged-prs, journal, deploy-config]   # where the verdict came from
skill_gaps: ["no step for checking which model made the calls"]
worker: {how: subagent, model: opus, tokens: 140283, minutes: 5}   # how: subagent | claude-p | interactive
---
```

Below it, a few short paragraphs: the cause with the file, the evidence, the recurrence check, and what was left untouched. When one fingerprint holds several causes with different fixes or close decisions, write one record per cause; when they share one disposition, write one record and break the causes down in the body.

Put the same frontmatter at the end of the proof or "cannot tell" comment, as a fenced `yaml` block whose first line is `# almanac-case`. The block can't hold its own comment's URL, so leave that out of `comments` there and add it in the file. That block is the record of last resort. An unattended run cannot commit to another checkout, so it posts the block and reports that the file is not written yet.

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
- **Case record.** End the comment with the `almanac-case` block and list the record file under "not done". Whoever reviews the run writes the file from the block.
- **Status page and devlog.** Skip both and list them under "not done" in the report. The devlog skill is denied, and registering publishes a page.

Checked by hand on 2026-11-09: a `--dry-run` against pickleclaw loaded the skill, ran 44 turns in 5 minutes for about $1.60, and posted nothing. Still unverified: a run started by a schedule, and a run without `--dry-run`. Start with `--dry-run` and read what it would have posted.
