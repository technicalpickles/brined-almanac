# Filing to taskwarrior: the tracker and the router

**Status:** design, 2026-11-08. First piece of roadmap M1. Builds on prod on
the Mac (`2026-11-06-prod-on-the-mac-design.md`), which left every finding
going to a GitHub issue. Taskwarrior task `4cc5a639-e11a-4cf0-98ad-71a85cfcfa6e`
is the M1 item.

## Goal

A workflow can file a finding that no pull request closes, and it lands as a
task on my taskwarrior list. The rule in [vision.md](../../vision.md) "Where
output goes" decides between an issue and a task; this spec gives that rule a
second destination and the piece that picks between them. security-alerts and
service-down are the first workflows that need it, and so does every workflow
after M1.

Non-goals: tier declarations (`local/tiers.toml` and code-declared tiers),
which the tracker does not need and the security-alerts spec designs next;
closing or annotating existing tasks (M2's reconciler); a periodic sync that
is not tied to a filing; any workflow.

## What already exists

- Upstream's `Tracker` trait is two methods, `file(key, target, title, body)`
  and `ask(key, target, pull, question, body)`. The runtime's outbox calls them
  with `key` set to the outbox entry's key, `run-id:index`, which is stable
  across retries of one entry. `GithubIssues` dedupes a retry by putting that
  key in an HTML marker and looking for the marker before opening an issue.
- Run ids are `run-<n>` from a Postgres sequence. A restored store keeps the
  sequence (pg_dump includes it); a fresh store restarts it, so `run-1:0` names
  a different finding in a fresh store than it did in the old one. This is
  upstream's shape and the GitHub tracker has the same exposure today.
- A dry run never reaches a tracker. The runtime records `WouldApply` entries
  and `check` prints them. `LogTracker` in `src/providers.rs` covers the other
  gap: a live run with no `GITHUB_TOKEN` prints what it would file.
- `providers::all` layers `PrivateOnly<GithubIssues>` over the GitHub client,
  or `StoreRequired` when `DATABASE_URL` is unset, or `LogTracker` when the
  token is unset.
- picklehome runs a taskchampion sync server on picklelab, reachable over the
  tailnet at `https://taskchampion.<tailnet>.ts.net`. My Mac syncs to it on a
  launchd timer (dotfiles ADR 0062). The sync settings (`sync.server.url`,
  `sync.server.client_id`, `sync.encryption_secret`) live in the `picklehome`
  vault as the `TaskChampion Sync` item.
- The `taskchampion` crate (3.1.0) hosts a replica in-process. Verified against
  its source on 2026-11-08: `SqliteStorage::new(path, access_mode,
  create_if_missing)`; `Replica::create_task(uuid, &mut ops)` returns the
  existing task if the uuid exists; `Task::set_user_defined_attribute(key,
  value, &mut ops)` takes any key not in the data model; `add_annotation`,
  `add_tag`, `set_value("project", ..)`; `Replica::sync(&mut server,
  avoid_snapshots)`; `ServerConfig::Remote { url, client_id,
  encryption_secret }` and `ServerConfig::Local { server_dir }`, and a `Git`
  variant behind the `server-git` feature. Its `SyncServer::new` doc says that
  replicas synchronizing the same task history use the same `client_id`.
- My taskwarrior projects mirror repository names, with dotted subprojects:
  `brined-almanac`, `homelab.backup`, `pickled-claude-plugins.hk`.
- Taskwarrior itself accepts any string as a project name (probed 2026-11-08
  with a throwaway data directory: spaces, slashes, colons and a trailing dot
  all save). It keeps attributes its `.taskrc` does not define, shows them in
  `info` and tags the task `ORPHAN`, and can filter on them once they are
  defined.

## Targets

A workflow names a taskwarrior destination with a slug of the form
`task:<project>`. Anything else stays a GitHub target, `owner/repo`, as today.

`Project` is a newtype in `src/providers.rs`: one or more segments of
`[a-z0-9_-]+` joined by `.`, at most 200 characters in all so the slug fits.
`Project::target()` returns the `task:<project>` slug. A workflow builds its
target through `Project`, so an invalid project cannot become an
`Effect::File`; the router's parse of a `task:` slug is the same function, so
it cannot fail at apply time, where a failure would retry forever. Taskwarrior
would accept worse, but these are the names I already use, and anything
outside them breaks `task project:<name>` filters.

`task:almanac` is the project for findings about almanac itself and for
findings that cut across repositories, like a session-friction review. No
bare `task:`.

## The router

`Router<I, T>` implements `Tracker`. `file` and `ask` parse the target: a
`task:<project>` slug goes to `T` with the project; anything else goes to `I`
unchanged. The router holds no state and does no I/O.

`providers::all` builds the tracker in three steps, each side falling back the
way the GitHub side does today:

| Setting | Issues side | Tasks side |
|---|---|---|
| `DATABASE_URL` unset | `StoreRequired` | `StoreRequired` |
| `GITHUB_TOKEN` unset | `LogTracker` | |
| `TASKCHAMPION_SYNC_URL` unset | | `LogTracker` |
| all set | `PrivateOnly<GithubIssues>` | `Taskwarrior` |

`StoreRequired` stays outermost: without a durable store a live run would
refile everything, whichever side it lands on. `LogTracker` prints the target
as given, so a dev run shows `target=task:picklehome`.

## The taskwarrior tracker

`Taskwarrior` owns a `Replica<SqliteStorage>` behind an async mutex and a
`Box<dyn Server>`. Both come from a `TaskwarriorConfig { replica_dir,
server: ServerConfig }`, built from the environment in `providers::all`. One
filing is one critical section:

1. **Sync** with the server. This brings in anything the Mac or an earlier
   container filed or closed, so step 2 reads current state. A sync error
   fails the effect; the outbox retries it later. Nothing is written before a
   successful sync.
2. **Look for the key.** Iterate the replica's tasks (`all_task_data`,
   pending and completed alike) for one whose `almanac_key` equals `key`. If
   found, return `Ok(())`: a retry of an entry that already landed. This is
   the taskwarrior form of the GitHub marker.
3. **Create the task** with a fresh v4 uuid and commit one `Operations`:
   - `description`: the title (or the question, for `ask`).
   - `project`: from the target.
   - `status`: pending; `entry` and `modified`: now.
   - tags: `almanac`; and `question` for `ask`.
   - one annotation holding the body text, verbatim. The body is `Untrusted`
     and stays so until here; the annotation is where it becomes text. A task
     has no other place for prose, and the annotation is what `task info`
     shows under the description.
   - attributes, all prefixed `almanac_`: `almanac_key` (the outbox key),
     `almanac_target` (the slug as given), and `almanac_pull` when `ask` has a
     pull number. One prefix means one block in `.taskrc` and one filter
     shape, `task almanac_target:task:picklehome`.
4. **Sync again**, so the task reaches the server before the effect is marked
   applied. If this sync fails, the task is in the replica and the effect
   fails; the retry's step 1 pushes it and step 2 finds it, so the retry files
   nothing twice.

The uuid is random, not derived from the key. Deriving it from `run-id:index`
looked like free idempotence, but a fresh store restarts the run sequence, and
then a new finding's key would map to an old finding's task and
`create_task` would hand it back silently. With a random uuid and the UDA
check, that case files a duplicate at worst, which I can see and delete.

`ask` differs from `file` only in the `question` tag and the `almanac_pull`
attribute. There is no one to mention and nothing to assign; the task on my
list is the ask.

The body is not fenced or quoted the way `GithubIssues::quoted` does, because
an annotation is not rendered as markup. It is still text I did not write, so
the annotation's first line is `almanac <key>` and the body starts on the
second, so that an agent reading my tasks sees where the text came from before
it reads it.

### What the key guarantees

`almanac_key` dedupes a retry of one outbox entry, which is what upstream's
`key` promises. Dedupe across reruns of a workflow is the store's job, through
delivery ids, as it is for issues. Three cases:

- **Lost replica volume.** Step 1 rebuilds it from the server. Nothing in the
  volume is the only copy of anything.
- **Lost store, restored from the daily dump.** Delivery ids and the run
  sequence come back with it, so nothing refiles. The runbook already says to
  restore before starting the daemon.
- **Lost store, started fresh.** Every finding is new to the store, so every
  open finding refiles, as it would for issues. The sequence restart means an
  old task's `almanac_key` can equal a new entry's key; step 2 would then skip
  a different finding. The runbook gains one line: never start the daemon on
  an empty store while tasks or issues filed by the old one exist; restore
  first. The structural fix is upstream's (Open questions).

### Configuration

Four variables, read in `providers::all`:

| Variable | Meaning |
|---|---|
| `TASKCHAMPION_SYNC_URL` | `https://taskchampion.<tailnet>.ts.net`. Unset means the tasks side is `LogTracker`. |
| `TASKCHAMPION_CLIENT_ID` | The same uuid the Mac uses. A `client_id` names a task history, not a device. |
| `TASKCHAMPION_ENCRYPTION_SECRET` | The same secret the Mac uses, base64 as stored in 1Password; decoded to bytes. |
| `TASKCHAMPION_REPLICA_DIR` | Where the replica's SQLite file lives. Default `/var/lib/brined-almanac/taskchampion`. |

Missing `CLIENT_ID` or `SECRET` when `URL` is set is a startup error, not a
fallback: half a configuration should not silently print.

`TASKCHAMPION_SYNC_URL` is the one place that names the sync server kind.
Changing the backend later (Open questions) changes how `providers::all`
builds the `ServerConfig` and which secrets compose passes; the tracker takes
a `ServerConfig` and does not care.

## Compose and secrets

- A named volume `taskchampion` mounted at `/var/lib/brined-almanac/taskchampion`
  in the `almanac` service, owned by the `almanac` user (the Containerfile
  creates the directory). Not under `$STATE`: nothing in it is the only copy
  of anything, and the backup service does not need to see it.
- One new item in the `brined-almanac` vault, `taskchampion`, with fields
  `client_id` and `encryption_secret` copied from picklehome's
  `TaskChampion Sync` item. `prod.secrets.env` gains
  `BRINED_ALMANAC_TASKCHAMPION_CLIENT_ID` and
  `BRINED_ALMANAC_TASKCHAMPION_ENCRYPTION_SECRET` references, and
  `compose.prod.yaml` passes them to the almanac service with the same `:?`
  guard the GitHub token has. `TASKCHAMPION_SYNC_URL` goes in `prod.env`,
  since it is not a secret.
- The copy is deliberate. The prod service account reads one vault, and
  widening it to `picklehome` would hand almanac every homelab secret. The
  encryption secret effectively never rotates: rotating it means
  re-initialising every replica. The runbook records that the two vaults' items hold
  the same values and that a rotation touches both.
- `scripts/deploy` and `scripts/compose-config-check` keep working with the
  `unused` stand-ins; they gain the two new names.

## Dotfiles

`home/.taskrc` in dotfiles defines the attributes so they stop being orphans
and become filterable:

```
uda.almanac_key.type=string
uda.almanac_key.label=Almanac key
uda.almanac_target.type=string
uda.almanac_target.label=Almanac target
uda.almanac_pull.type=numeric
uda.almanac_pull.label=Almanac pull
```

Order does not matter: a task filed before the definitions land keeps its
attributes and shows them in `info`. This is a separate dotfiles change,
recorded as a taskwarrior task when this spec is approved.

## Testing

Proof, in order. No test touches `~/.task`, picklelab, or GitHub.

1. **Project parsing.** Unit tests on `Project`: the three real names above
   round-trip through `target()`; `Bad Name`, `has/slash`, `UPPER`, `trail.`
   and the empty string are rejected; a `task:` slug with an invalid project
   is rejected the same way.
2. **Routing.** With two `FakeTracker`s, a `task:picklehome` target reaches
   the tasks side with the full key, title and body; `technicalpickles/
   brineworks` reaches the issues side; neither sees the other's calls.
3. **Filing.** An integration test in `tests/taskwarrior.rs` runs
   `ServerConfig::Local` in a temp directory with two replicas: `container`
   and `mac`. Filing through `container` and syncing `mac` shows one task with
   the description, project `picklehome`, tag `almanac`, the annotation, and
   the three attributes. Filing the same key again adds nothing to either
   replica. Filing a different key adds exactly one more.
4. **Retry after a landed write.** A task with `almanac_key=k` created through
   `mac` and synced; filing `k` through `container` adds nothing. This is the
   lost-volume case: a fresh `container` replica learns of the task in step 1.
5. **Ask.** Filing through `ask` with `pull: Some(42)` produces the `question`
   tag and `almanac_pull=42`.
6. **Fallbacks.** `providers::all` with `DATABASE_URL` and `GITHUB_TOKEN` set
   and no `TASKCHAMPION_SYNC_URL` routes a `task:` target to something that
   prints `target=task:...` and files nothing; `URL` set without `CLIENT_ID`
   fails construction with a message naming the missing variable.
7. **Prod.** After deploy, `mise run prod -- check` passes with the new
   variables, and a dry-run filing from a dev checkout shows the task as
   `WouldApply`. The first live filing is whichever M1 workflow lands first;
   the task appears on the Mac after its next `task sync`, with the attributes
   visible in `task info` and no `ORPHAN` tag once dotfiles has the
   definitions.

The `taskchampion` dependency is built with `default-features = false` and
`features = ["storage-sqlite", "server-sync", "server-local", "bundled",
"tls-webpki-roots"]`: sqlite for the replica, sync for prod, local for tests,
and no cloud or git backends until one is chosen. `mise run check` gains
nothing slow.

## Work in other repos

- dotfiles: the six `uda.*` lines in `home/.taskrc`.
- picklehome: nothing in code. The taskchampion-sync README and its plan
  said a second device needs a new allowlisted uuid; corrected 2026-11-08 to
  say a replica of the same history uses the same `client_id`.
- 1Password: the `taskchampion` item in the `brined-almanac` vault, by Josh.
- Roadmap: the M1 bullet corrected the same day; tier declarations noted as
  the security-alerts spec's job.

## Upstream

Two things for ivy/almanac, after this has run:

- The `Tracker` key is `run-id:index`, and run ids restart with the store, so
  a tracker that dedupes on the key can skip a new finding after a store
  reset. A finding-stable key, or the run's workflow name and key alongside
  the outbox key, would let a tracker dedupe across reruns and label what it
  files. File as an issue with this spec's "What the key guarantees" as the
  case.
- Upstream's roadmap plans a taskwarrior tracker for its M4. Once this one has
  filed real tasks for a while, offer it, with `Project` and the router.

## Open questions

- **Credential story.** Option 1 above copies two values into a second vault.
  Josh wants a different story eventually, possibly taskchampion's git
  backend (`ServerConfig::Git`: a local clone, a branch, a remote, the same
  encryption secret). That would trade the shared `client_id` for a git
  credential, which could reuse the puller's fenced-SSH-key pattern, and would
  move the Mac off the sync server too. Not decided; the tracker's
  `ServerConfig` boundary keeps it a configuration change.
- **Workflow name in the task.** The tracker does not receive the workflow
  name, so `almanac_workflow` is not in this spec. If upstream adds it to the
  key or the call, add the attribute and its `.taskrc` line.
- **Periodic sync.** M2's reconciler wants to see my closes promptly, which
  means syncing on a timer, not only on a filing. Not needed for M1.
- **Body size.** Annotations are unbounded strings, but `task info` renders
  them in full. If a workflow's body runs long, truncate at the tracker with
  a note, or point to a fuller record. Decide when a body is actually long.
