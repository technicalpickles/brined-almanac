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
- **The module is importable as a pseudo-version.** `go get github.com/ivy/almanac@<commit>` works against `go-rewrite` with no `replace`. Nothing else has blocked so far.

## Not in scope until it is in Rust

- **Tracker `Resolve`.** `effect.Tracker` has `File` and `Ask`, and so does the Rust `main` pin (`b8bdefa`). Closing tracker work on checked evidence lives only in [ivy/almanac#33](https://github.com/ivy/almanac/pull/33) and brined PR [#38](https://github.com/technicalpickles/brined-almanac/pull/38), both open. `resolve_on_close` on `main` resolves the **Bugsink** issue through the Bugsink API, which ports without it. A Go `Resolve` sits on a local, unpushed `brined-go` branch of ivy/almanac, shelved.

## Gaps this port introduces on its own

- **Lone-surrogate JSON escapes.** serde_json rejects `\ud800`, Go's decoder accepts it (`transcript`).
- **Required JSON fields.** serde rejects a response missing a field, Go leaves it zero (`bugsinkapi`).
- **Error text.** Messages that came from the OS or the decoder differ in wording, with the same prefixes.
- **The `reveal` import in `bugsink`**, which `hostilevet` flags until the boundary question above is settled.

## Touch points

| brined piece | Plugs into | Notes |
|---|---|---|
| Three workflows | `app.Almanac.Models`, built with `dsl` over a brined `vocab.Vocabulary` | Not `Almanac.Workflows`, which is for code workflows |
| Scans (host reports, Bugsink issues, security alerts) | `vocab.Scan(...)` registered in the vocabulary | Each reads its source through providers, as `renovate.failing_pulls` does |
| Events `HostFinding`, `Unaddressed`, `SecurityFinding` | Vocabulary event parsers | Fields that were `Slug` become `hostile.Slug`, whose alphabet is `._/-:@[]` plus letters and digits |
| `Tracker` (issues side) | `github.NewIssues(client, person)` | Unchanged from upstream |
| `Tracker` (tasks side) | brined's own `effect.Tracker` that runs `task` | Must refuse writes when `Setting.Switches.ReadOnly` is set, because it writes through a concrete type |
| `Router` | brined's `effect.Tracker` wrapping the two above | Uses `routing.Project` and `routing.FromTarget`, already ported |
| Tiers, host reports, Bugsink reader, alerts client | `providers.Provide[...]` in a `Setup` func shaped like `almanacs/renovate/wiring` | The alerts client needs its own token, so its own `github.New` |
| `sync-check`, `cutover` | `app.Almanac.Verbs` (flags via `Verb.Flags`) | `cutover --apply` also honors ReadOnly |
| Views, `compare` | `app.Almanac.Commands` | Free once the workflows are models |
| Ports and database | `almanac.toml` plus `ALMANAC_*` | The Go binary takes its own port and database, per the ADR |

## Order this implies

1. Port `resolve_on_close` from Rust `main`. It is pure rules over `cutover` types.
2. Port `host_state` as a model first. It has the simplest scan and the smallest body, so it proves the `MapPart` body approach before the larger workflows commit to it.
3. Write the Router and the `task` tracker against `effect.Tracker` as it is (`File`, `Ask`).
4. Raise the boundary question with Ivy before the next package needs `reveal`.
