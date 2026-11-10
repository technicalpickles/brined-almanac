# transcript-shipper: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** brined-almanac sends each failed tool call in Brent's transcripts to Bugsink's `brent-tool-failures` project exactly once, so Bugsink can take over grouping and issue state from the tool-failures filer.

**Program plan:** `docs/superpowers/plans/2026-11-09-error-tracker.md` (PR #15), step `transcript-shipper`. Its spike results and local Bugsink recipe are in its Appendix A.

**Architecture:** `src/transcript.rs` reads each failed call into a `ToolError` that now carries the transcript event id, the surface and a hashed session key. `src/bugsink.rs` turns one `ToolError` into one Sentry event (a pure function), keeps a cursor file, and posts to the project's store endpoint with an `X-Sentry-Auth` header. The tool-failures scan calls it once per scan with the errors it already read, before clustering. A Bugsink failure prints and never fails that scan.

**Tech stack:** Rust 2024, almanac at rev `b8bdefa`, `reqwest` 0.13 (already in the tree through the `github` crate), `sha2`, `time`.

## Data shape

One event per failed call:

| Field | Value |
|---|---|
| `event_id` | 32 hex characters of SHA-256 over the transcript event id. A resend repeats it, and Bugsink digests a repeated id once. |
| `timestamp` | The transcript event's own time. |
| `fingerprint` | `[service, tool, class]`, with `class` from the filer's `classify` table. |
| `message` | The reason line: the `error` of a JSON object, the line after `Command failed:`, or the first line with text, at most 200 characters. |
| `logger` | `service.tool`. |
| `tags` | `service`, `tool`, `class`, `surface` (the word after `agent-<id>-` in the file name, else `unknown`), `session` (12 hex characters of SHA-256 over the file name, because some names hold a chat id). |

## Decisions

- **Where it runs.** Inside the tool-failures scan, which already reads the transcripts. Almanac has no scan-only workflow, and a second workflow would need an event and an effect it never uses. `tracker-consumer` deletes the tool-failures registration and moves this call into its own scan, which polls Bugsink anyway.
- **Dry runs.** The shipper sends outside almanac's effects, so it asks almanac's question itself: live only with `ALMANAC_MODE=live` and no `--dry-run`. A dry run counts what is due and sends nothing.
- **Cursor.** A file holding the time of the newest accepted event, default `/var/lib/brined-almanac/bugsink/cursor` on its own volume. A scan resends from one hour before it, so a file the puller copied late still ships. A failed send stops there and leaves the cursor at the last success. A lost or garbled cursor resends history, which costs traffic only.
- **Failure.** Printed as one `transcript-shipper:` line, never returned, so a Bugsink outage never stops the filer or the security-alerts scan after it.

## Steps

### `old-shape-error-text`

Land taskwarrior `3544e1fd` from the parked worktree of the same name.

- [x] Apply its diff: `error_text` uses `details` only when it is a non-empty string or an object with `error` or `result`, and reads `content` otherwise. Tests: `details_that_only_name_the_tool_fall_back_to_content`, `a_message_that_is_only_in_content_still_picks_the_class`.
- [x] Diff the filer's cluster keys on the real transcripts before and after. Result: exactly 5 keys change, all `brineworks/*/other/2026-10-22` becoming `pg-too-many-clients`, and all 5 are loud after (2 were quiet before). See "Deploy" below.

### `tool-error-fields`

- [x] Add `id`, `surface` and `session: SessionKey` to `ToolError`. `SessionKey` is built only by hashing, so the raw file name never leaves `read`.
- [x] Add `Sample::reason`, which returns an `Excerpt`, so the text stays opaque except through serialization.
- [x] Test: `the_session_tag_is_a_hash_of_the_file_name`, `the_surface_is_the_word_after_the_agent_or_unknown`.

### `event-builder`

- [x] `bugsink::event(&ToolError) -> Value` and `bugsink::event_id`.
- [x] Tests on fixtures built by `tests/support`, which follow `.forbidden-patterns`: fingerprint, timestamp, tags, the reason rules, a stable event id per transcript id.

### `cursor`

- [x] `Cursor::load` and `Cursor::save` (write beside, rename over), and `due(errors, cursor)`.
- [x] Tests: round trip, missing and garbled are `None`, only the last hour onward is due.

### `shipper-client`

- [x] `Dsn` parses `scheme://key@host[:port]/project` into the store URL and key. Neither its error nor its `Debug` carries the key.
- [x] `Bugsink::ship` sends what is due oldest first and saves the cursor.
- [x] Tests against a loopback store endpoint: each event posted with the key, a second ship resends only the overlap with the same event ids, a dry run sends nothing and writes no cursor, a 503 stops the send with the cursor at the last success.

### `provider-wiring`

- [x] `bugsink_client_from(lookup, live)` in `src/providers.rs`, registered in `all()` and `tests/support/mod.rs`. `BugsinkClient(None)` prints a skip message.
- [x] `ship_from` is called from the tool-failures scan before clustering.

### `prod-wiring`

- [x] `BUGSINK_DSN` from `${BRINED_ALMANAC_BUGSINK_DSN:?}` in `compose.prod.yaml`, the `bugsink` volume for the cursor, and its directory in the `Containerfile`.
- [x] `prod.secrets.env` points at `op://brined-almanac/bugsink/dsn`.
- [x] The `unused` lists in `mise.toml`, `scripts/deploy`, `scripts/compose-config-check` and the runbook. `compose-config-check` asserts the variable, the guard and the volume.
- [x] The runbook's 1Password steps, rotation, and a "Bugsink gets no events" section.

### `live-check`

- [x] Start a local Bugsink 2.6.3 with the Appendix A recipe on a loopback port, create a project in `bugsink-manage shell`, and use its DSN.
- [x] `ALMANAC_MODE=live BUGSINK_DSN=<dsn> BUGSINK_CURSOR=<scratch>/cursor BRENT_TRANSCRIPTS=<pulled transcripts> cargo run -- backfill`, with no GitHub token, store or sync URL, so the filer only prints.
- [x] Count events and issues in Bugsink after each run. Remove the container.

Results on 2026-11-09, against the transcripts pulled to the Mac at the time:

| Run | Shipper said | Events in Bugsink | Issues |
|---|---|---|---|
| First | sent 360 of 360 | 360 | 68 |
| Second, with the cursor | sent 3 of 3 (the last hour) | 360 | 68 |
| Third, cursor removed | sent 360 of 360 | 360 | 68 |
| `--dry-run` | 3 due, sent none | 360 | 68 |

The spike saw 358 events and 68 issues. The transcripts grew between the two: one failure is dated after the spike ran, and the other most likely arrived in a pull after it (two failures sit in the 10-minute pull window before the spike). The fingerprint count matches. Stored events keep their transcript time, and every `session` tag is 12 hex characters.

## Verify

`mise run test` and `mise run check` pass.

## Deploy

Josh only:

1. Confirm `op://brined-almanac/bugsink/dsn` resolves (the item exists).
2. `mise run deploy`.
3. Read the first scan's `transcript-shipper:` line in the daemon log and the `brent-tool-failures` project in Bugsink. Whether the almanac container reaches `svc:bugsink` over the tailnet is unproven until that first event lands.

The `old-shape-error-text` fix re-keys 5 filer clusters, so the first scan after deploy files 5 new brineworks issues for 2026-10-22 `pg-too-many-clients`, the outage brineworks #82 fixed.
