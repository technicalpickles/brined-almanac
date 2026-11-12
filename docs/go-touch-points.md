# Where brined meets ivy/almanac's Go library

Read against `ivy/almanac` branch `go-rewrite` at `86122f2`, and brined's Rust at `ea999b3`. Two questions: what does the Go library do that the Rust path never did, and where should brined plug into it.

## What Go does that the Rust brined path did not

These are gains. They cost nothing to take once the workflows are models.

- **Read-only switch.** `ALMANAC_READ_ONLY=1` refuses every write and every model call. The Rust side has its own `live` flag, a `LogTracker` and `--apply`, which are three mechanisms for one idea.
- **Dry runs stored as `would-apply`.** Effects are recorded but can never be delivered, and `--dry-run` always wins over configured mode.
- **A checker at startup.** A model that files a title built from anything but a literal or clean value, reads a fact before observing it, or uses an effect not in `may`, does not start. Rust only enforced taint through types, and nothing checked workflow shape.
- **`compare` between implementations.** The Go renovate almanac compares its states and effects with the Rust one for the same inputs. brined can have the same verb for the same reason.
- **Views.** Every way a run can end, and a diff between two workflow versions, fall out of the model being data.
- **Taint enforced by a vet pass** (`tools/hostilevet`), not only by types.

## What the Go library lacks that brined's Rust relies on

- **A way for a downstream package to be a taint boundary.** `tools/hostilevet/taint/boundary.go` lists the only packages allowed to import `hostile/reveal`, and the list names upstream's `patch` and `github`. brined needs the same standing in three places that must hand text to the outside: the Bugsink shipper, the taskwarrior tracker, and the Router wrapping both. Putting brined in upstream's list is the wrong fix. A flag or config on the analyzer is the right one, and is worth proposing.
- **A non-GitHub `Tracker`.** `github.NewIssues` exists. Anything for `task:` targets does not, and the Router that sends `task:` targets to taskwarrior and everything else to GitHub is brined's to write against `effect.Tracker`.
- **Production workflows as code.** `workflow.Coded` is documented as "for tests and samples". An almanac's own workflows are models. brined's three workflows (`host_state`, `bugsink_issues`, `security_alerts`) are scan, then `File`, with no repair step, so they fit a model with a scan, a small `decide`, and `File` effects. The open question is the body: the DSL gives `Saying(literal)` and `Showing(hostileFact, mapPart)`. A body assembled from clean facts plus quoted hostile text has to be a `MapPart` written in Go. That works, and it is the thing to prove first with `host_state`.
- **Limits that match.** Rust declares `runs_per_hour = 1000` per workflow. Go's `DefaultLimits` set no run cap, so check that a workflow's own budget is taken as declared before relying on it.

## Import friction, as feedback for upstream

Found by importing `go-rewrite` at `86122f2` from outside its module. Running `hostilevet` here takes `go build -o hostilevet github.com/ivy/almanac/tools/hostilevet` then `go vet -vettool=./hostilevet ./...`; `go run` refuses with "use go hostilevet".

- **`reveal` is upstream-only.** `boundaries` in `tools/hostilevet/taint/boundary.go` is a hardcoded list of `ivy/almanac/patch` and `ivy/almanac/github`. A downstream tracker has to hand hostile text to something outside almanac exactly as `github` does, so `bugsink` and `taskwarrior` both get "only the boundary packages tools/hostilevet lists may import hostile/reveal". The ask: let an almanac declare its own boundary packages (an analyzer flag, or a list the almanac's module owns), reviewed the same way.
- **A no-leak test is flagged.** The boundary check skips `_test.go` files ("Tests may do both, to assert what a boundary sends"), but the formatting check does not. `transcript_test.go` formats a sample with `%v`, `%+v`, `%#v` and `%s` to prove its text never prints, and gets "fmt.Sprintf formats a hostile value". Upstream's own `hostile` tests are exempt only because `implementsTaint` covers package `hostile`. The ask: the same test-file exemption for formatting, or a sanctioned way to write that assertion.
- **An event parser cannot refuse a value that decodes.** `wire.Field` takes only `hostile.Clean` types and `wire.Member` only closed sets, and `wire.Payload` has no exported way to mark a field malformed. Rust's `host_state` refused, at parse, a `/project` that is a slug but not a taskwarrior project (`Bad`) and a `/since` that does not split at `T` into two slugs (`T14:50Z`). In Go those parse, and the `brine.file_finding` observer refuses them, so the delivery becomes a failed run instead of a malformed one. The ask: a way for `Parse` to report a field malformed after reading it.
- **A pure derivation has to be an Observer.** Turning a project into `task:<project>` and splitting `since` reads nothing from outside, but the DSL has no compute step, so it is declared as an observer.
- **A body of a clean header and hostile text works, through an observer.** `bugsink_issues` builds its body as `hostile.Join(header, reason)` inside an observer, whose output struct carries `Body hostile.Of[string]`. `dsl.HostileField[string](filing, "body")` feeds `ShowingText`, and the body reaches the tracker as the exact string Rust builds. No `MapPart` is needed. The one cost is that the observer is where the header is written, so the model shows "write the body" as an observe step and not as part of the file rule.
- **A key is only event fields.** Rust's key is `bugsink:{issue}:{generation}`, and `dsl.Key` takes fields joined with `:`, with no literal. The `bugsink:` is a `Source` field on the event that `Parse` sets to a constant without reading the payload. The ask: a literal in `Key`, or a note in the docs that a constant field is the way.
- **A list of structs has no reader.** `wire.List` reads one field of each item, so `surfaces` (`[{name, count}]`) is read as two lists and zipped. The ask: a way to read a list of clean structs, as `Member` reads a closed set.
- **A missing list is empty, not malformed.** Rust refuses a payload with no `surfaces`. `wire.List` and `TextList` return an empty list, and there is no way to ask for a required one. This is the same gap as the parse-time refusals above, since `Parse` cannot mark a field malformed itself.
- **A scan cannot see the mode, and its writes are outside the effects.** `settle_closed` resolves or mutes a Bugsink issue and the shipper posts events, from inside a scan, through providers that are not a `Tracker`, `Forge` or `Sweep`. `ALMANAC_READ_ONLY=1` swaps those three for refusing ones (`refusingWrites`) and leaves every other provider alone, and `workflow.Scanner` carries no mode. brined builds `BugsinkReader.Live` and `BugsinkClient.Live` from `Setting.Switches.Mode == effect.Live` in `wiring.Providers` itself, which is what Rust's `live()` does. `Setup` is handed the switches the runtime will use, `--dry-run` included, so Rust's rule needs no workaround. The one difference is that `ALMANAC_READ_ONLY` also ends live, which Rust has no notion of. The ask: put the mode on the `Scanner`, or say in the docs that a scan with its own writes must be given it.
- **No test double for `workflow.Cx`, and `FakeTracker` drops bodies.** Walking a model outside a runtime needs a `Cx`, which `almanactest` does not offer, so `internal/walktest` holds one, shared by both models. `almanactest.FakeTracker` keeps title and target but not the body, so asserting a delivered body needs a tracker of our own that reads it.
- **`Client.Get` cannot read a 204.** `GET /repos/{repo}/vulnerability-alerts` answers 204 with no body when Dependabot alerts are on, and 404 when they are off. `Get` decodes JSON, so the 204 comes back as `GitHub could not be reached for ...: the answer is not the JSON expected: EOF`, a `*diag.Error` that is not a `Refusal`, which reads as a failure. 404 and 403 do come back as `*Refusal` with the right `Status`, and `GetAll` handles a path that already has `?state=open` (it sends `&per_page=100`). Rust's client had `get_text` for this; Go's has no equivalent outside `JobLogs`. brined makes one call of its own for that endpoint (`securityalerts.Client.Status`, status only), built from the same token and base URL as the `github.Client`, and keeps everything else on Ivy's. `TestAnEmpty204IsOnWhereGithubGetFailsDecodingIt` fails the day `Get` reads a 204, as the cue to drop it. The ask: `GetText`, or a status-only GET, or `Get` treating an empty 2xx as null. Two smaller costs of the workaround. `Client` exposes neither its token nor its base URL, so brined holds a second copy of both. A `Refusal` can only be built through `diag.Refused` with `Cause` set by hand, because Ivy's `refused` is unexported, so brined's call repeats that to stay indistinguishable from Ivy's refusals.
- **Hostile text cannot go from a scan into an event.** `github.Client` returns every answer hostile, and a scan's payload is plain JSON that `wire.Payload.Text` reads back as a plain string. Rust puts package names and manifest paths from the alerts in the event's `lines`, so Go has to open them to write the payload, and the only way is `hostile/reveal`, which `hostilevet` allows in upstream's `patch` and `github` only. `securityalerts/plain.go` does it in one function and is a fifth known finding. The text is hostile again at `p.Text("/lines")`. Ivy's own scans never meet this: a Renovate event carries clean fields and an observer re-reads the pull request in the run. Doing that here would read GitHub twice and could file a body that differs from the digest in the delivery ID. The ask: a way to carry a `hostile.Of` through a payload (`Payload.Text` accepting the stored form `Of` marshals to), or a sanctioned scan-to-event conversion.
- **A title cannot list a variable set of counts.** Rust's overdue title ends in the counts above zero, such as `(12 high, 5 medium, 1 low)`. A title is a literal format and numbers, bools, slugs and shas, so the text between the parentheses cannot be a fact, and a literal format has to be a constant, so it cannot be built in a loop. The model has one rule and one literal format for each of the 63 combinations (`brine/securityalerts/titles.go`), plus the zero case, and a test checks all 64 against Rust's display. The ask: a title value that is a list of clean values, or a `hostile.Title` that may be a fact's field.
- **An almanac cannot declare read-only as its default.** The ADR has the Go binary act on nothing until cutover. `runtime.SwitchesFrom` reads `ALMANAC_READ_ONLY` from the process environment, and `app.Almanac` has no field for a default, so `main` sets `ALMANAC_READ_ONLY=1` with `os.Setenv` when the environment leaves it unset. `ALMANAC_READ_ONLY=0` turns it off. The ask: a read-only default on `app.Almanac`, or a config key that may only tighten it.
- **A printing tracker needs `reveal` too.** Rust's `LogTracker` prints the body it would file, so a live run with no credentials shows what it would have sent. In Go that is `reveal.Value(issue.Body)` in `wiring/trackers.go`, a sixth `hostilevet` finding of the same boundary kind as `bugsink` and `taskwarrior`. A dry run never reaches it: the runtime records the effect and delivers nothing.
- **The module is importable as a pseudo-version.** `go get github.com/ivy/almanac@<commit>` works against `go-rewrite` with no `replace`. A model written downstream passes `check.Check` and runs under `engine.New`.

## Not in scope until it is in Rust

- **Tracker `Resolve`.** `effect.Tracker` has `File` and `Ask`, and so does the Rust `main` pin (`b8bdefa`). Closing tracker work on checked evidence lives only in [ivy/almanac#33](https://github.com/ivy/almanac/pull/33) and brined PR [#38](https://github.com/technicalpickles/brined-almanac/pull/38), both open. `resolve_on_close` on `main` resolves the **Bugsink** issue through the Bugsink API, which ports without it. A Go `Resolve` sits on a local, unpushed `brined-go` branch of ivy/almanac, shelved.

## Gaps this port introduces on its own

- **Lone-surrogate JSON escapes.** serde_json rejects `\ud800`, Go's decoder accepts it (`transcript`).
- **Required JSON fields.** serde rejects a response missing a field, Go leaves it zero (`bugsinkapi`).
- **Error text.** Messages that came from the OS or the decoder differ in wording, with the same prefixes.
- **The `reveal` imports in `bugsink` and `wiring`** (the printing tracker), which `hostilevet` flags until the boundary question above is settled. `securityalerts/plain.go` is the same kind of finding, for a different reason (see "Hostile text cannot go from a scan into an event").
- **No retry or rate-limit wait on the Dependabot switch call.** Rust's `get_text` went through the same `send` as every other read. `Client.Status` makes one attempt, so a 5xx or a rate limit fails that repository's read for the scan, which skips it and holds the coverage task until the next one.

## Touch points

| brined piece | Plugs into | Notes |
|---|---|---|
| Three workflows | `app.Almanac.Models`, built with `dsl` over a brined `vocab.Vocabulary` | Not `Almanac.Workflows`, which is for code workflows |
| Scans (host reports, Bugsink issues, security alerts) | `vocab.Scan(...)` registered in the vocabulary | Each reads its source through providers, as `renovate.failing_pulls` does |
| Events `HostFinding`, `Unaddressed`, `SecurityFinding` | Vocabulary event parsers | Fields that were `Slug` become `hostile.Slug`, whose alphabet is `._/-:@[]` plus letters and digits |
| `Tracker` (issues side) | `github.NewIssues(client, person)` | Unchanged from upstream |
| `Tracker` (tasks side) | brined's own `effect.Tracker` that runs `task` | Must refuse writes when `Setting.Switches.ReadOnly` is set, because it writes through a concrete type |
| `Router` | brined's `effect.Tracker` wrapping the two above | Uses `routing.Project` and `routing.FromTarget`, already ported |
| Tiers, host reports, Bugsink reader, alerts client | `providers.Provide[...]` in a `Setup` func shaped like `almanacs/renovate/wiring` | The alerts client needs its own token: `securityalerts.NewClient(base, token)` builds Ivy's `github.Client` and brined's status call from one pair |
| `sync-check`, `cutover` | `app.Almanac.Verbs` (flags via `Verb.Flags`) | `cutover --apply` also honors ReadOnly |
| Views, `compare` | `app.Almanac.Commands` | Free once the workflows are models |
| Ports and database | `almanac.toml` plus `ALMANAC_*` | The Go binary takes its own port and database, per the ADR |

## Order this implies

1. Port `resolve_on_close` from Rust `main`. It is pure rules over `cutover` types.
2. Port `host_state` as a model first. It has the simplest scan and the smallest body, so it proves the `MapPart` body approach before the larger workflows commit to it.
3. Write the Router and the `task` tracker against `effect.Tracker` as it is (`File`, `Ask`).
4. Raise the boundary question with Ivy before the next package needs `reveal`.
