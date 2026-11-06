# Tool error scan: design

Status: draft for review, 2026-11-06.

brined-almanac watches Josh's running services the way [ivy/almanac](https://github.com/ivy/almanac) watches ivy's repositories. Events come in, code deduplicates and correlates them, a pure policy decides, and a small closed set of effects goes out. A model is called only where judgment is needed, and this first workflow needs none.

This document designs the first workflow. It reads OpenClaw transcripts from the Brent deployment on picklelab, groups failed tool calls into clusters, and files one GitHub issue per cluster on the repository that owns the failing tool.

## Why this workflow first

Brent has no error tracker. The only record of a failed tool call is the session transcript, and the only alerting is Josh noticing. On 2026-11-06 a Claude session spent its whole run reading about 600 transcript messages to measure that `triage_apply` had timed out 12 times on Nov 4 and that session size had grown from 42 to 93 threads. Every one of those facts is arithmetic over JSONL. The judgment ("batch the Gmail calls, commit per item") took one paragraph at the end.

The scan does the arithmetic. It leaves the judgment to whoever picks up the issue.

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

The scan is a pipeline of pure functions over these records. Names are final unless implementation finds a reason to change them, and the reason gets written here.

```python
@dataclass(frozen=True)
class ToolError:
	event_id: str
	at: datetime            # UTC
	run_id: str | None
	source_file: str        # basename, for the issue body
	service: str            # "brineworks", "gog", "openclaw"
	tool: str               # "triage_apply", "read"; never the meta-tool
	raw_tool_id: str        # what the model wrote, for the sample
	message: str            # error text, untrusted, truncated to 500 chars

ErrorClass = str            # one of the table's class names

@dataclass(frozen=True)
class ClusterKey:
	service: str
	tool: str
	error_class: ErrorClass
	day: date               # UTC

@dataclass(frozen=True)
class Cluster:
	key: ClusterKey
	count: int
	first_at: datetime
	last_at: datetime
	run_ids: frozenset[str]
	samples: tuple[str, ...]   # up to 3 messages

@dataclass(frozen=True)
class FileIssue:
	repo: str               # "technicalpickles/brineworks"
	key: str                # "almanac:tool-errors:brineworks/triage_apply/approval-timeout/2026-11-04"
	title: str
	body: str

@dataclass(frozen=True)
class UpdateIssue:
	repo: str
	number: int
	body: str               # rewritten facts, same key marker, new count marker
	comment: str            # "count 10 -> 12, last seen 21:14Z"

@dataclass(frozen=True)
class Skip:
	key: str
	reason: str

Effect = FileIssue | UpdateIssue | Skip

@dataclass(frozen=True)
class ExistingIssue:
	repo: str
	number: int
	count: int              # read from the body's count marker
	closed: bool
```

Two tables hold the domain knowledge, so adding a class or a service is a row, not a branch:

- The class table maps a regular expression to a class name and a filing threshold.
- The ownership table maps a service to a repository. `brineworks` goes to `technicalpickles/brineworks`. `gog`, `memory-core`, and OpenClaw's own tools go to `technicalpickles/pickleclaw`, because that repository owns the MCP manifest and the tool policy.

## Modules

```
brined_almanac/
	sources/openclaw_transcript.py   # Path -> Iterable[ToolError]; the only code that knows the JSONL shape
	workflows/tool_errors.py         # classify, cluster, decide; pure, no I/O
	effects/github_issues.py         # find existing issues by key, apply FileIssue and UpdateIssue through gh
	cli.py                           # brined-almanac errors scan DIR [--since DAY] [--min-count N] [--apply]
```

`sources` and `effects` are the boundaries and hold all the validation and all the subprocess calls. `workflows` trusts its inputs and does no I/O, so a test runs it on fixtures with no network.

## Data flow

1. `cli` lists `*.jsonl` under `DIR`.
2. `sources.openclaw_transcript.read` parses each file, skips lines that are not JSON, joins each error result to its call by `toolCallId`, and yields one `ToolError` per event id. A second occurrence of an event id across files is dropped.
3. `workflows.tool_errors.classify` assigns each error a class from the class table. The first matching row wins. No row matches gives `other`.
4. `workflows.tool_errors.cluster` groups by `ClusterKey` and builds one `Cluster` per key.
5. `workflows.tool_errors.decide` takes the clusters and a map from key to `ExistingIssue`, and returns effects. A cluster below its class threshold is a `Skip`. A cluster with no existing issue is a `FileIssue`. A cluster whose issue is open and whose count grew is an `UpdateIssue` with the rewritten body and one comment naming the old and new counts. A closed issue, or an unchanged count, is a `Skip`.
6. `cli` prints every effect. With `--apply` it also runs them through `effects.github_issues`. Without `--apply` nothing is written, which is the same safe default as the scripts in `impressed-hosting`.

`--min-count N` raises every class threshold to at least `N`. It cannot lower one. Configuration only tightens.

## Idempotency

A rerun over the same files must change nothing. Three mechanisms:

- Event ids deduplicate within a run.
- The issue body carries `<!-- almanac-key: ... -->` and `<!-- almanac-count: N -->`. Before deciding, `effects.github_issues.existing` lists issues with the `almanac` label on each owned repository, open and closed, and reads both markers out of each body. That map is the "existing" input to `decide`. An update rewrites the body, so the count marker always holds the last count the scan saw.
- A closed issue counts as existing and is never updated or reopened. A new day is a new key, so a recurring problem files again tomorrow.

The scan has no state of its own. The issues are the ledger. If a later workflow needs replay or budgets, a ledger goes behind `effects/` and the pure middle does not change.

## Issue content

Title: `<tool> <class> on <day>`, for example `triage_apply approval-timeout on 2026-11-04`. The count is not in the title because it changes.

Body:

- The key marker.
- A facts table: service, tool, class, count, first and last time, run ids, source files.
- Up to three sample error messages in a fenced block, each cut at 500 characters.
- The count marker.
- One line saying brined-almanac filed it from a transcript scan and that a rerun rewrites the facts and adds a comment when the count grows.

Label: `almanac`. The applier creates the label on a repository that lacks it.

## Untrusted text

Error messages come from a bot that reads Josh's email, so a sample can quote a message body. Two rules:

- The scan never passes a message to a model. It is data in a table.
- `effects.github_issues` refuses to file on a repository that is not private. It checks `gh repo view --json isPrivate` once per repository per run.

Pulls made with `openclaw-ro` already pass through its gitleaks gate, which masks key-shaped strings. The scan does not add a second redactor.

## Errors

- A line that is not JSON is counted and reported at the end. It is never fatal.
- An error result with no usable `details` becomes `service="openclaw"`, `tool=<toolName>`, class `other`, with the raw `content` as its message.
- `gh` failing on one effect stops the run with a nonzero exit. Effects already applied are listed so the operator knows what landed. The next run is safe because of the idempotency rules above.

## Testing

Fixtures under `tests/fixtures/` are trimmed and redacted slices of the pulled transcripts:

- Nov 4, the 12 approval timeouts on `triage_apply`.
- Oct 22, the `too many clients` burst across `tasks_*`.
- A file with the three tool-id spellings and two lines that are not JSON.

Tests, all offline:

- `read` yields the expected `ToolError` records from each fixture, including the join through `toolCallId` and the fallback when `details` is empty.
- `classify` maps each sample text in the class table to its class.
- `cluster` turns the Nov 4 fixture into one cluster with count 12.
- `decide` files on an empty existing map, updates when the count grew, skips when it did not, skips a closed issue, and skips below threshold. Running `decide` twice on the same clusters with the first run's effects folded into the existing map yields only `Skip`. `--min-count` only raises.
- The dry run prints a stable listing. A snapshot test guards it.

The real check after implementation is a dry run over the three pulled transcripts on the Mac, compared by hand against the counts in the table above.

## Alternatives considered

**A ledger from day one.** ivy/almanac records every observation in PostgreSQL so a run can be replayed without re-reading the source. Here the source is a directory of files that do not change, so replay is rerunning the scan. The ledger would be scaffolding with no reader. It goes in when a workflow needs budgets or cross-run correlation.

**Bash and jq, next to the two picklehome watchdogs.** Cheapest today and consistent with how `exec-approvals-watch` and `brineworks-server-watch` are built. The join through `toolCallId`, the `str(dict)` parse, and the class table are where it falls over, and there is no way to commit a fixture and prove the Nov 4 case. The watchdogs are one-signal checks. This is a parser.

**Taskwarrior instead of GitHub issues.** Taskwarrior is the backlog, and a task per cluster would land on Josh's list without opening GitHub. The facts table has nowhere to live in a task, and the scan would need the task database wherever it runs. Decided 2026-11-06 for issues on the owning repository, with a taskwarrior pointer left to the person triaging.

**Brineworks container logs as a second source.** Asked for, and right. Today the server logs nothing per MCP call (taskwarrior `34325580-f7f0-4141-8c15-7d2c0f13780a`), so the source is empty until that lands. The `sources/` boundary is where it plugs in.

## Out of scope

- Telegram alerts. The issue is the alert. Josh can watch the `almanac` label.
- Running on picklelab. First runs happen on the Mac over `openclaw-ro` pulls. A timer that reads the SQLite store directly is a deployment step with its own design.
- impressed-hosting. Same shape, different source. Next workflow.
- Any model call.
