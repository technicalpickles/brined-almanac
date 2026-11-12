# 1. Port to Go, alongside the Rust

## Status

Proposed for the `go-port` branch. The Rust implementation stays in the tree and in service until the operator decides on a cutover.

## Context

brined-almanac is built on [ivy/almanac](https://github.com/ivy/almanac), pinned in `Cargo.toml` at `b8bdefa`. Upstream's author is rewriting almanac in Go on the `go-rewrite` branch of that repository, recorded in her [ADR 0002](https://github.com/ivy/almanac/blob/go-rewrite/docs/adrs/0002-go-and-a-workflow-model.md). In that design a workflow is no longer Rust code. It is a checked data model built by a Go DSL, and an engine interprets it. The branch had no pull request and was 115 commits ahead of upstream's `main` when this ADR was written. Like the Rust pin, it has no tracker `Resolve` effect. That effect exists only in open PRs ([ivy/almanac#33](https://github.com/ivy/almanac/pull/33), brined [#38](https://github.com/technicalpickles/brined-almanac/pull/38)).

Staying on the Rust pin works today. It leaves brined on a library whose author has moved on. A port waits for upstream's Go surface to settle, but the work that does not depend on it can start now.

## Decision

Work on a `go-port` branch, in the same spirit as upstream's.

- **Rust stays in service, and is the spec.** Nothing on this branch changes the Rust code or the deployed service. The port carries only behaviour that is on Rust `main`; anything new lands in Rust first.
- **Own ports and own database.** The Go binary shares nothing with prod, so both can run at once. Ports and the database name are chosen when the first listener lands.
- **Read-only first.** Until cutover the Go binary runs from scans and acts on nothing. `ALMANAC_READ_ONLY=1` style refusal of every write and model call is the default.
- **Prove it by comparison.** A `compare` verb puts the Go report beside the Rust report for the same inputs. Agreement between them is the evidence a workflow has ported, not a reading of the code.
- **Port what does not need upstream first.** Pure logic with its own tests, such as `classes`, `routing`, `providers` and the host-state rules, moves before anything that needs almanac's engine.
- **Do not fork the library.** Depend on upstream's own `go-rewrite` commits, pinned as a pseudo-version, with no local changes on top. Move to a tag once one exists.

## Consequences

- Two implementations exist until cutover, so every behaviour change on `main` needs a matching change here or a recorded gap.
- The port can stall on upstream. Open questions to put to upstream: where `Resolve` lands (Rust only, Go only, or both), which decides when the port picks it up, and whether the Go module is meant to be imported by other almanacs.
- Module path is `github.com/technicalpickles/brined-almanac`, at the repository root, beside `Cargo.toml`.

## Options rejected

- **Stay on the Rust pin indefinitely.** Cheap now, but the pin stops getting upstream fixes, such as the hardening in ivy/almanac#26 to #32.
- **Port everything in one cutover.** No way to check the port while it is being written.
