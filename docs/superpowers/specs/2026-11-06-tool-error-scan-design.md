# Tool error scan: design

Status: draft for review, 2026-11-06. Supersedes the Python design of the same date, after a spike built the same workflow on ivy/almanac and matched it cluster for cluster.

brined-almanac is an almanac in the sense of [ivy/almanac](https://github.com/ivy/almanac): a Rust binary that receives events, deduplicates and correlates them, gathers facts, applies a pure policy, and produces a small closed set of effects. It watches Josh's running services the way ivy's almanac watches ivy's repositories. A model is called only where judgment is needed, and this first workflow needs none.

This document designs the first workflow. It reads OpenClaw transcripts from the Brent deployment on picklelab, groups failed tool calls into clusters, and files one GitHub issue per cluster on the private repository that owns the failing tool.

## Why this workflow first

Brent has no error tracker. The only record of a failed tool call is the session transcript, and the only alerting is Josh noticing. On 2026-11-06 a Claude session spent its whole run reading about 600 transcript messages to measure that `triage_apply` had timed out 12 times on Nov 4 and that session size had grown from 42 to 93 threads. Every one of those facts is arithmetic over JSONL. The judgment ("batch the Gmail calls, commit per item") took one paragraph at the end.

The scan does the arithmetic. It leaves the judgment to whoever picks up the issue.

## Why build on almanac

The first draft of this design was a Python package with its own dedupe markers in issue bodies. A spike on 2026-11-06 rebuilt it as an almanac workflow in 285 lines of Rust and ran `backfill --dry-run` over the same three transcripts. It found the same 137 clusters and the same 37 clusters worth an issue. Almanac already owns the parts the Python draft had to invent: durable dedupe by delivery id, one run per key, a tracker adapter that files GitHub issues, a dry-run mode that calls no provider, a budget per workflow, and a taint type that keeps transcript text out of anything a model would read. The spike's code is the shape this document describes.

The dependency is by git with a pinned revision, not a fork. The library is `publish = false` at version `0.0.0` and changes hourly, so the pin is the only stable reference. Bumping it is an edit to `Cargo.toml` and a rebuild. The spike proved this form builds at `32c2866` with both the `almanac` and `github` crates resolving as workspace members.

## What the data looks like

Measured from three pulled transcripts under `pickleclaw/.scratch/openclaw-pulls/` (one `telegram.jsonl` and two `telegram-full-*.jsonl`).

A transcript is JSONL, one event per line. A failed tool call is an event where `type` is `message`, `message.role` is `toolResult`, and `message.isError` is true. The fields the scan reads:

| Field | What it holds |
|---|---|
| `id` | Event id. Unique across the three files. |
| `timestamp` | ISO 8601, UTC. |
| `message.toolName` | For MCP tools this is the meta-tool `tool_call`. For built-ins it is the tool itself (`read`, `edit`, `exec`, `apply_patch`, `tool_search`). |
| `message.toolCallId` | Joins to the `toolCall` content block in the preceding assistant message, whose `arguments.id` names the tool the model asked for. |
| `message.details` | A Python-repr string of a dict. For an MCP tool it carries `tool.mcp.serverName` and `tool.mcp.toolName`, and the error text sits in `error` or in `result.content[0].text`. For a tool the model named wrongly it carries only `status`, `tool`, and `error`. |
| `message.__openclaw.runId` | The run the call belonged to. |

Three things make a naive reader wrong:

- The tool id the model wrote has three spellings over the two months of data (`brineworks__triage_apply`, `mcp:brineworks:brineworks__triage_apply`, bare `triage_apply`). Only `details.tool.mcp` is stable, so the scan reads identity from there and falls back to the call arguments.
- `details` is a `str(dict)`, not JSON. `ast.literal_eval` parses it. One in 90 is empty.
- One of the three files has 12 lines that are not JSON. The reader skips them and reports the count.

Error classes present in the data, by the text that identifies them:

| Class | Text | Seen | Files at |
|---|---|---|---|
| `mcp-timeout` | `MCP error -32001` | Oct 22, Nov 4 to 6 | 1 |
| `pg-too-many-clients` | `too many clients already` | Oct 22, Oct 24 | 1 |
| `approval-timeout` | `Approval timed out` | Oct 21 to Nov 4, 12 on Nov 4 alone | 3 |
| `unknown-tool-id` | `Unknown tool id` | most days, one or two | 5 |
| `arg-validation` | `validation error`, `Validation failed`, `Missing required argument` | most days | 5 |
| `other` | anything else | | 3 |

"Files at" is the count per day below which the scan stays quiet. A timeout or a database outage is worth an issue on the first hit. A model misnaming a tool is noise until it happens all day.

## Shapes

The scan is a pipeline over these records. Names are final unless implementation finds a reason to change them, and the reason gets written here.

```rust
/// One failed tool call, as the reader yields it. The only type that knows the JSONL shape.
pub struct ToolError {
	pub at: At,             // ISO 8601 UTC, checked; the day is its first ten characters
	pub service: Slug,      // "brineworks", "gog", "openclaw"; mapped into the Slug alphabet once, here
	pub tool: Slug,         // "triage_apply", "read"; never the meta-tool
	pub text: Sample,       // the full error text
}

/// Error text read from a transcript. No accessor, and `Debug` prints `Sample(..)`. It leaves
/// only through `narrow`, a `fn` to a `Copy` value (classification), or `compose`, which appends
/// it to a clean header and cuts it at 500 characters.
pub struct Sample(String);

/// What a cluster is grouped by.
pub struct ClusterKey {
	pub service: Slug,
	pub tool: Slug,
	pub class: &'static str, // a row of the class table, or "other"
	pub day: Slug,           // "2026-11-04", UTC
}

/// Everything the scan knows about one key.
pub struct Cluster {
	pub count: u64,
	pub first: At,          // the earliest error
	pub last: At,           // the latest error
	pub sample: Sample,     // the earliest error's text
}

observed! {
	/// One cluster, as the runtime sees it. `report` is composed from transcript text, so it is tainted.
	pub struct ToolFailed {
		pub service: Slug,
		pub tool: Slug,
		pub class: Slug,
		pub day: Slug,
		pub count: u64,
		pub report: Untrusted<String>,
	}
}

/// What gather hands to policy.
pub enum Facts {
	Quiet,
	Loud { target: Slug, body: Untrusted<String> },
}
```

`ToolFailed` is the workflow's event. Its `KIND` is `brent.tool.failed`, and `parse` reads each field from the payload with `Payload::field`, and `report` with `Payload::text`, so the text arrives tainted. `Slug` is almanac's validated identifier (ASCII alphanumerics and `._/-:@[]`, at most 200 characters), and the scan maps any other character to `-` before building one.

Run ids are not read in v0. The spike did without them and the issue reader has the day and the sample to find the session.

Two names changed after the type sketch on 2026-11-06. `text` holds the full error text, not a 500-character cut, because the spike classified the full text and the 137 and 37 counts were verified that way. The cut happens when the report is composed. `event_id` is not a field, because the reader deduplicates by it and nothing after the reader needs it.

Two tables hold the domain knowledge, so adding a class or a service is a row, not a branch:

- The class table maps a regular expression to a class name and a filing threshold. It is the table above, with `other` at 3.
- The ownership table maps a service to a repository. `brineworks` goes to `technicalpickles/brineworks`. Every other service (`gog`, `memory-core`, and OpenClaw's own tools) goes to `technicalpickles/pickleclaw`, because that repository owns the MCP manifest and the tool policy.

## Modules

```
Cargo.toml                  almanac and github by git, rev pinned; an empty [workspace] table so the crate stands alone
almanac.toml                runtime settings, which can only tighten what the binary declares
src/main.rs                 almanac::main("brined-almanac", workflows::all, providers::all)
src/lib.rs
src/transcript.rs           PathBuf -> Vec<ToolError>; the only code that knows the JSONL shape
src/workflows/mod.rs        registers the workflows
src/workflows/tool_failures.rs   the class and ownership tables, ToolFailed, cluster, scan, gather, policy
src/providers.rs            Transcripts from BRENT_TRANSCRIPTS; a Tracker, wrapped in PrivateOnly
tests/                      the synthetic transcript builder and the tests below
```

`transcript.rs` and `providers.rs` are the boundaries. The reader holds every rule about the file format, including the Python-repr regexes and the skipped lines. The providers hold every environment variable and every network client. The workflow trusts its inputs. Policy does no I/O, so a test runs it on a `Facts` value with no network.

## Data flow

1. The runtime calls `scan` once per `backfill`, or on its scan interval when the daemon runs.
2. `scan` takes the `Transcripts` directory from the `Scanner`, and `transcript::read` parses every `*.jsonl` under it. For each event it records `toolCall` blocks by id, so an error result joins to the tool the model asked for through `toolCallId`. It reads service and tool from `details.tool.mcp` by regex, because `details` is a Python repr. When that fails and the tool is `tool_call`, it strips any `mcp:` prefix, keeps the segment after the last `:`, and splits once on `__`. An id with no `__` is one of OpenClaw's own tools. Otherwise the service is `openclaw` and the tool is `toolName`. Empty `details` falls back to the result's `content`. A line that is not JSON is skipped and counted. A failed result whose timestamp does not start with a date is counted and dropped. A second occurrence of an event id is dropped. The reader returns the errors sorted by time, so every later `first`, `last`, and sample is independent of file order.
3. `scan` classifies each error with the class table. The first matching row wins, and no match is `other`. It clusters by `ClusterKey`, with the day taken from the first ten characters of the timestamp.
4. For each cluster, `scan` composes the report (service, tool, class, day, count, first, last, and the sample) and emits one `Scanned<ToolFailed>` with delivery id `scan:brent/{service}/{tool}/{class}/{day}`.
5. The runtime admits each scanned event through the store. A delivery id it has already seen is a duplicate and goes no further. The workflow key is `{service}:{tool}:{class}:{day}`, so one run is active per cluster.
6. Gather compares `count` to the class threshold. Below it, the facts are `Quiet`. At or above it, they are `Loud` with the owning repository and the report.
7. Policy maps `Quiet` to no effects and `Loud` to one `Effect::File` with the static title `Brent: failed tool calls`, the target, and the body.
8. In live mode the runtime queues the effect in the outbox and the tracker files it. In dry-run mode it records what would have happened and calls no provider.

The report is composed in `scan`, where the text is already tainted, because `Untrusted::map` takes a `fn` and cannot capture the clean fields (ivy/almanac#4). The title is `&'static str` (ivy/almanac#2), so the body's first lines carry what the title would. When either lands upstream, the body or title moves without changing the flow.

Thresholds live in gather, not in `scan`. Every cluster becomes an event, so the run log records the quiet ones, and a replay with a changed threshold re-decides old events without re-reading transcripts.

## Dedupe

A rerun over the same files must change nothing. Three mechanisms, and only the first is this crate's:

- Event ids deduplicate within one scan.
- The store deduplicates events by source and delivery id, durably (ivy/almanac requirement R-24). A scanned event with an id the store has seen is dropped before a run starts.
- The GitHub tracker writes a marker into each issue body carrying the outbox key, and checks for it before posting, so a retried delivery never files twice.

The delivery id carries no state. A cluster that grows during a day keeps the same id, so the store drops the second scan of it and the issue keeps the count it was filed with. v0 accepts losing intra-day growth. Carrying state in the id (a count, or a last-seen time) would file a new issue on every change, which is worse. A new day is a new id, so a recurring problem files again tomorrow.

The durable dedupe lives in PostgreSQL. Without `DATABASE_URL`, almanac uses an in-memory store that forgets everything when the process exits, and the tracker's marker is the outbox key, which is per run, so a second live `backfill` would file every cluster again. Dry runs need no database. Live runs set `DATABASE_URL` to a PostgreSQL on loopback. Where that PostgreSQL comes from is a deployment step, not this design.

## Issue content

Title: `Brent: failed tool calls`. Static until ivy/almanac#2 lands, so the body opens with the specifics.

Body, which almanac's tracker fences as a code block so no mention or markup inside it does anything:

- `service`, `tool`, `class`, `day`, `count`, `first`, `last`, one per line.
- A blank line, then `sample:` and the first error text seen, cut at 500 characters.

The tracker adds the `almanac` label and its own marker. Nothing else goes in the body. There is no count marker and no update path: the issue is a snapshot of the day the cluster crossed its threshold.

## Untrusted text

Error messages come from a bot that reads Josh's email, so a sample can quote a message body. Three rules:

- Error text is a `Sample` from the moment the reader parses the line. It has no accessor and its `Debug` hides the text, so no error message, log line, or panic in this crate can repeat it. It leaves only through `narrow` into a class name or `compose` into the report. The report is `Untrusted<String>` from the moment `scan` emits it, and stays tainted through the event, the facts, the effect, and the outbox. Almanac's taint type has no accessor either. The text never reaches a prompt, and this workflow calls no model.
- The tracker refuses a public target. `providers.rs` wraps the GitHub tracker in `PrivateOnly`, which asks `/repos/{target}` and files only when the repository answers `private: true`. A private answer is cached for the process, so a target is asked once. A public answer or a failed lookup refuses the delivery and is asked again on the retry, so an outage at GitHub never lets text through. The ownership table is a closed set of two private repositories, but a repository can go public later, and the body carries transcript text.
- Nothing in this repository holds transcript text. Fixtures are synthetic, errors name the shape and never repeat the text, and `scripts/check-forbidden` runs before every commit and push.

Pulls made with `openclaw-ro` already pass through its gitleaks gate, which masks key-shaped strings. The scan does not add a second redactor.

## Operations

The binary has almanac's verbs. `synth` prints the manifest. `check` validates providers. `backfill [--dry-run]` runs the scan once. `run` stays up and scans on an interval. Settings come from the environment:

| Variable | Holds |
|---|---|
| `BRENT_TRANSCRIPTS` | The directory of `*.jsonl` files. Defaults to `transcripts`. |
| `GITHUB_TOKEN` | Enables the GitHub tracker. Without it, a `LogTracker` prints each would-be issue to stdout. |
| `DATABASE_URL` | PostgreSQL. Required for live runs, optional for dry runs. |
| `ALMANAC_MODE` | `live` lets effects reach GitHub. Anything else is a dry run, and `--dry-run` always wins. |

Three things to know before trusting a build:

- Plain `check` validates only the providers the workflow body uses. The transcript directory and the tracker are used by the scan and the effect, so run `ALMANAC_MODE=live brined-almanac check` to have them validated too (ivy/almanac#1).
- `backfill --dry-run` spends the workflow's `runs_per_hour` budget (ivy/almanac#3). The first backfill over two months of transcripts produced 137 clusters, so the workflow declares `runs_per_hour = 1000`. Configuration can only lower it.
- v0 runs on the Mac, by hand, over transcripts pulled with `openclaw-ro`. The daemon, a timer, and reading picklelab's store directly are a deployment document.

## Errors

- A line that is not JSON is counted and reported at the end of the scan. It is never fatal.
- A failed result with no timestamp, or one that does not start with a date, is counted beside the non-JSON lines and dropped.
- An error result with no usable `details` becomes `service = openclaw`, `tool = toolName`, class `other`, with the result's `content` as its text.
- A transcript directory that cannot be read, or a file in it that cannot be read, fails the scan with the path. Almanac reports the failed scan and files nothing.
- A tracker failure fails that delivery. Almanac retries it up to its attempt cap, and the marker check makes the retry safe.
- A public target fails the delivery with the repository's name. Nothing is filed.

Errors in this crate name the path or the repository, never the transcript text.

## Testing

No transcript is ever committed. `tests/` holds a builder that writes synthetic JSONL to a temporary directory: a call block with a tool id in any of the three spellings, an error result with a Python-repr `details`, an error result with empty `details`, and lines that are not JSON. `.forbidden-patterns` matches the role and metadata keys a real transcript line carries, so the builder spells those two keys through constants. The source then passes the hook, and a pasted real line still trips it.

Tests, all offline, against almanac's in-memory store and fakes:

- `transcript::read` yields the expected `ToolError` records for each of the three tool-id spellings, reads service and tool from `details` when present, falls back to the call id when not, uses `content` when `details` is empty, ignores results that are not errors, reads an event id once, and skips the non-JSON lines.
- A 600-character error classifies from its full text, and its sample in the report is 500 characters. Printing a `ToolError` with `{:?}` never shows the text.
- `classify` maps each sample text in the class table to its class, and an unknown text to `other`.
- A directory with 12 `Approval timed out` results on `triage_apply` on one day produces one `ToolFailed` with count 12.
- Scanning the same directory twice in one runtime reports the second scan as all duplicates. This is what proves the delivery ids are stable.
- The policy files at the threshold with the owning repository as target, stays quiet one below it, and routes `brineworks` and `gog` to their repositories.
- A live-mode run over the twelve-timeout directory with almanac's fake tracker files one issue on `technicalpickles/brineworks` and nothing for a cluster below threshold.
- A second runtime with a fresh in-memory store queues every cluster again. This is the test that documents why live runs need a database.
- `PrivateOnly` refuses to file when a fake answers `private: false` or fails, files when it answers `true`, and asks once for a target that answered `true`.

The real check after implementation is `backfill --dry-run` over the three pulled transcripts on the Mac, compared by hand against the counts in the class table. The spike matched them.

## Alternatives considered

**The Python package this document replaces.** Dataclasses, a `str(dict)` parser, dedupe by markers in issue bodies, and an update path that rewrote the body when a count grew. The spike matched its counts in 285 lines of Rust, with the dedupe, the budget, and the dry run coming from almanac. Dropped 2026-11-06.

**Fork almanac.** Would let the title and body limits be fixed locally. A fork of a library that changes hourly is a merge every day. The four issues are filed upstream, and the workarounds (a static title, the body composed in `scan`) cost nothing to remove later.

**No ledger.** The Python draft argued that the transcripts do not change, so replay is rerunning the scan, and a database would be scaffolding with no reader. That was wrong once the dedupe moved into almanac: the store is where "already filed" lives, and without it a live rerun files every cluster again. Reversed.

**Filter quiet clusters in `scan`.** Would spend fewer runs on the budget, because only clusters at threshold would become events. It would also leave the quiet clusters out of the run log, and a threshold change would need a rescan instead of a replay. Thresholds stay in gather.

**Bash and jq, next to the two picklehome watchdogs.** Cheapest today and consistent with how `exec-approvals-watch` and `brineworks-server-watch` are built. The join through `toolCallId`, the Python-repr parse, and the class table are where it falls over, and there is no way to commit a fixture and prove the Nov 4 case. The watchdogs are one-signal checks. This is a parser.

**Taskwarrior instead of GitHub issues.** Taskwarrior is the backlog, and a task per cluster would land on Josh's list without opening GitHub. The facts have nowhere to live in a task, and the scan would need the task database wherever it runs. Decided 2026-11-06 for issues on the owning repository, with a taskwarrior pointer left to the person triaging.

**Brineworks container logs as a second source.** Asked for, and right. Today the server logs nothing per MCP call, so the source is empty until that lands. A second reader beside `transcript.rs` is where it plugs in.

## Out of scope

- Telegram alerts. The issue is the alert. Josh can watch the `almanac` label.
- Running on picklelab. First runs happen on the Mac over `openclaw-ro` pulls. A timer that reads the SQLite store directly, and the PostgreSQL it needs, are a deployment document.
- OSS maintenance on Josh's public repositories. That is almanac's native GitHub source and the second workflow.
- Any model call.
