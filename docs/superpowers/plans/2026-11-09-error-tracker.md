# Error tracker: program plan

**Goal:** Failures in the Brent stack become reviewable work with nobody at the keyboard. A self-hosted error tracker owns grouping and issue state. brineworks-server reports its own exceptions to it, brined-almanac ships Brent's failed tool calls to it, and almanac files a GitHub issue when the tracker says a problem is new or has come back.

**Why not build it in the filer:** tool-failures was growing fingerprints, first and last seen, resolve and regress, with GitHub issue markers as the database. That is an error tracker. Almanac's `File` is create-once and its existence check lists issues with `state=all`, so a key without the day could never file again after a close. See [Appendix B](#appendix-b-alternatives-rejected).

**Tracker:** [Bugsink](https://www.bugsink.com/), pinned to 2.6.3. One Django container, speaks the Sentry SDK protocol.

**This is a program plan.** Each step below is one pull request in one repository. A step gets its own task-by-task implementation plan when it starts. Steps are named by slug, and commits and cross-references cite the slug.

## Steps in order

| Slug | Repository | Depends on | Stops for Josh |
|---|---|---|---|
| `bugsink-service` | picklehome | nothing | secrets, Tailscale approval, deploy |
| `server-reporting` | brineworks | `bugsink-service` live | deploy |
| `server-dsn-wiring` | picklehome | `server-reporting` merged | deploy |
| `transcript-shipper` | brined-almanac | `bugsink-service` live | prod secret, `mise run deploy` |
| `cutover` | brined-almanac | `transcript-shipper` live | go-ahead to resolve history |
| `tracker-consumer` | brined-almanac | `cutover` done | `mise run deploy` |
| `worker-resolves` | brined-almanac | `tracker-consumer` live | the open posting decision |

`server-reporting` and `transcript-shipper` do not depend on each other and can run at the same time.

No agent merges, deploys, or runs a command on picklelab. Every step ends at a pull request and a written deploy note.

## bugsink-service

Run Bugsink on picklelab, reachable only over the tailnet.

**Build.**

- [ ] Add `homelab/services/bugsink/` following the service layout in `homelab/services/README.md`. Start from `taskchampion-sync/` for a self-contained base compose, and add a `db` service. No existing service ships a self-contained web plus Postgres compose, so the `db` block is new.
- [ ] Use Postgres. Bugsink's docs advise against SQLite on a Docker volume.
- [ ] Pin the image by version and digest. Set `PHONEHOME=False`. Leave the webhook outbound policy at its default, because nothing here uses webhooks.
- [ ] Bind the web port to loopback and expose it with `tailscale serve --service=svc:bugsink`, as `brineworks-server/deploy.sh` does.
- [ ] Name the compose project after the service directory and the database service `db`, then add a `dump_postgres` line and a raw-directory exclude in `backup/backup.sh`.
- [ ] Add the secret names to `.env.vars` and their references to `.env.template`. Create the 1Password item first, because `op inject` fails the whole template on a bad reference.
- [ ] Add `just deploy-bugsink` and a read-only `just bugsink-verify` that checks health and the running version.
- [ ] Cap retention. `/srv` is 30G shared. See [decision D1](#decisions-for-josh).

**You see.** `just bugsink-verify` prints PASS, and the login page loads over the tailnet from the Mac.

**Verify.** `uv run pytest` in picklehome. Then on the real host after Josh deploys: `just bugsink-verify`, and one event sent from the Mac with the spike's `replay.py` shows up as an issue.

**Only Josh.** Create the 1Password item (secret key, database password, superuser password). Approve `svc:bugsink` in the Tailscale admin console. Run or approve the deploy. Create two projects, `brineworks-server` and `brent-tool-failures`, and one API token.

## server-reporting

brineworks-server reports its own exceptions at the source.

**Build.**

- [ ] Add `sentry-sdk[fastapi]` to `server/pyproject.toml` and regenerate `uv.lock`. The dependency belongs to the package that imports it.
- [ ] Add `sentry_dsn: str | None = None` to `server/brineworks_server/config.py`. It reads `BRINEWORKS_SENTRY_DSN`.
- [ ] Call `sentry_sdk.init` at the top of `create_app()` in `server/brineworks_server/app.py`, only when the DSN is set. Pass the git sha as the release. Import `sentry_sdk` at module top, so `scripts/check-server-image` catches a missing dependency in the built image.
- [ ] Add a FastMCP middleware next to `TextOnlyResults` in `server/brineworks_server/mcp/server.py`. Its `on_call_tool` captures the exception and re-raises. FastMCP turns a tool exception into an error result before FastAPI sees it, so the automatic integration misses every tool failure without this.
- [ ] In `_fill_replied_cache` in `mcp/rules_tools.py`, capture the exception next to the existing warning. The default logging integration sends ERROR and above, and this handled failure logs at WARNING.

**You see.** A tool that raises in the dev server produces one issue in a local Bugsink, with a stack trace that names the tool's file.

**Verify.** `cd server && pytest`, with new cases for DSN unset (no init) and DSN set (a raising tool is captured, and the caller still gets the error result). `mise run ci` for the image check. Live: start a local Bugsink with the recipe in [Appendix A](#appendix-a-what-the-spike-proved), point the dev server at it, call a tool that raises, and read the issue.

**Closes the loop on** taskwarrior `a7624a7f`, which is waiting on someone to read a log line by hand.

## server-dsn-wiring

- [ ] Add `BRINEWORKS_SENTRY_DSN` to `homelab/services/brineworks-server/compose.picklelab.yaml`, `.env.vars`, and `.env.template`.

**Verify.** After Josh deploys through the quiet gate: `just brineworks-server-verify` passes and the Bugsink project shows the release sha on its next event.

**Unproven.** Whether the brineworks-server container can reach a `svc:` name on the tailnet. openclaw already calls brineworks-server that way, which suggests yes.

## transcript-shipper

brined-almanac sends each failed tool call in Brent's transcripts to Bugsink exactly once.

**Data shape.** One event per failed tool call. The fingerprint is `[service, tool, class]`. The event carries the transcript's own timestamp, the surface, a session key, and the line of the error that says why.

**Build.**

- [ ] Extend `ToolError` in `src/transcript.rs` with the transcript event id and the surface. Derive the surface from the file name. Hash the session name before it becomes a tag, because the file name holds a chat id.
- [ ] Land the parked `3544e1fd` change with its two tests: `error_text` uses `details` only when it is a string or has an `error` or `result`, and reads `content` otherwise. Branch `old-shape-error-text` has it.
- [ ] Add a shipper module that posts to the project's store endpoint with a `X-Sentry-Auth` header. No SDK. The event id is a hash of the transcript event id, which makes a resend harmless: Bugsink answers 200 and digests the event once.
- [ ] Keep a cursor so a scan does not resend all history every 15 minutes. The cursor only limits traffic. Exactly-once comes from the event id.
- [ ] Add `BugsinkClient(Option<..>)` to `src/providers.rs`, built from env like `alerts_client_from`, and register it in `all()` and `tests/support/mod.rs`. `None` means skip with a message.
- [ ] Wire the token through `compose.prod.yaml` as a `${VAR:?}`, `prod.secrets.env`, the `mise` tasks' unused list, and `docs/runbooks/prod-on-the-mac.md`. Keep tailnet addresses out of the repository.

**You see.** After a scan, Bugsink's `brent-tool-failures` project holds one issue per fingerprint, and a second scan adds no events.

**Verify.** `mise run test` and `mise run check`. Pure functions build the event from a `ToolError`, tested against fixtures that follow `.forbidden-patterns`. Live: run against a local Bugsink and the real transcripts, and compare with the spike: 358 events, 68 issues, and zero new events on the second run.

**The old filer keeps running** through this step. Nothing is deleted yet.

## cutover

Make history quiet before anything files from the tracker.

- [ ] After the first full ship, resolve in Bugsink every fingerprint whose almanac issues are closed on GitHub. A script reads `service`, `tool` and `class` from the closed issue bodies and resolves the matching Bugsink issue with a comment linking the proof. Without this, a fix that fails later cannot register as a regression.
- [ ] Record the cutover time. The consumer never files an issue first seen before it.
- [ ] Leave the 21 open almanac issues as they are. The worker handles them.

**Verify.** The script prints each fingerprint it resolved and each closed issue it could not match. Josh reads the list before it runs for real.

## tracker-consumer

Almanac files a GitHub issue when the tracker reports a new problem or a regression.

**Build.**

- [ ] Add a workflow whose scan polls Bugsink's unresolved issues, then reads each one's grouping key, tags and history. Almanac has no generic webhook route, and `security_alerts` already polls an API in its scan.
- [ ] Emit `Scanned::new("bugsink:<issue id>").at_state(<state>)`, where the state changes when the issue regresses or first crosses its class threshold. A regression then files a new GitHub issue.
- [ ] Build the body from the tracker: counts, the days it happened, surfaces, distinct sessions, the reason line, and the Bugsink link. Keep `owner_of` for routing and the `classify` table for thresholds.
- [ ] In the same pull request, remove the `tool_failures` workflow registration, `ClusterKey`, `Cluster`, `cluster`, `ToolFailed`, `facts`, `policy`, and the cluster tests.

**You see.** A new fingerprint files one issue. More events on it file nothing. Resolving it and sending one more event files one issue marked as a regression.

**Verify.** `mise run test` and `mise run check`, with the parse layer tested on JSON fixtures as `security_alerts` does. Live against a local Bugsink in dry-run mode: replay, resolve, replay, and read what almanac would file. Then a before and after count of filed issues over the first 24 hours in prod.

## worker-resolves

The almanac-work skill closes the loop in the tracker.

- [ ] The issue body carries the Bugsink link, so the skill resolves the tracker issue when it closes the GitHub issue with proof, and adds a comment that links the proof.
- [ ] When the resolve is denied in an unattended run, the skill puts the exact command in its report.

**Blocked on** decision D4.

## Decisions for Josh

Josh accepted the plan with these recommendations on 2026-11-09. D4 is still open.

| | Decision | Outcome |
|---|---|---|
| D1 | Retention on a 30G `/srv` | 10,000 events per project, which is Bugsink's default, and a 90-day age cap. The spike stored 358 events in 2.8 MB. |
| D2 | Postgres or SQLite for Bugsink | Postgres, per Bugsink's docs. It costs one more container and the backup wiring. |
| D3 | Phone-home | Off. It sends install metadata and counts. |
| D4 | May the unattended worker comment, close and resolve on its own | Open. Carried over from the almanac-work skill. |

## Appendix A. What the spike proved

Run on the Mac on 2026-11-09 against the pulled transcripts, with Bugsink 2.6.3 in one container on SQLite.

- 358 failed tool calls became 68 issues. The Rust filer counts 68 fingerprints on the same data.
- A backdated event is accepted and keeps its own timestamp. An issue's first and last seen are ingest time.
- Resolving an issue through the API and sending three more events reopened it, wrote `regressed` to its history, and fired one alert. 15 open issues took new events with no alert.
- A repeated event id returns 200 and is digested once.
- 353 MiB of memory and a 401 MB image.

Local recipe for the live checks above:

```bash
docker run -d --name bugsink-local -e SECRET_KEY="$(openssl rand -base64 60 | tr -d '\n')" -e CREATE_SUPERUSER="admin@example.org:<password>" -e PORT=8000 -e BASE_URL=http://localhost:8377 -e PHONEHOME=False -p 127.0.0.1:8377:8000 bugsink/bugsink:2.6.3
```

The login name is the email. `bugsink-manage create_auth_token` inside the container prints an API token.

Not proven: almanac's prod container reaching Bugsink on the tailnet, brineworks-server reaching it from picklelab, and anything about Bugsink on Postgres.

## Appendix B. Alternatives rejected

| Alternative | Why it lost |
|---|---|
| Key by `service:tool:class` in the filer | `File` is create-once and a closed issue holds its key, so a regression is silent forever. |
| Quiet-gap flare-ups in the filer | A bug that returns within the gap after a close stays silent, and every key is new at migration. |
| A regression-aware filer that reads issue state | It works, and it is an error tracker stored in GitHub issue markers. |
| GlitchTip | Its docs do not cover regressions, fingerprints, or its webhook payload, and it needs Postgres from the start. |
| Sentry self-hosted | Too heavy for a 30G host. From memory, not measured. |
| A webhook from Bugsink into almanac | Almanac's daemon has no route for it, and the webhook body has no fingerprint. |

## Appendix C. Risks

| Risk | Where | What to watch |
|---|---|---|
| The Sentry Python SDK drops events past 100 queued | `server-reporting` | Not a concern at request rate. Any batch sender must flush. |
| Replayed history looks first seen today | `cutover` | The consumer ignores issues first seen before the cutover time. |
| State lives in two places | `worker-resolves` | A GitHub close without a tracker resolve hides the next regression. |
| Transcript error text is stored on picklelab | `transcript-shipper` | The transcripts already live there. Bugsink stays tailnet-only. |
| Tailnet reach from containers | `server-dsn-wiring`, `transcript-shipper` | Prove it with one event before building further. |
| FastMCP's error masking was read from a comment, not traced | `server-reporting` | The first test in that step proves or disproves it. |
