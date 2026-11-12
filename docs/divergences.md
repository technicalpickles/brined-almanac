# Divergences between the Go port and Rust

Parity contract item 1 ([go-port-parity-plan.md](go-port-parity-plan.md)) says `compare` shows zero unexplained differences. This file is where each accepted difference is explained. `compare/divergences_test.go` reads the table below and checks it against `compare/testdata/compare.golden`:

- A difference in the golden file that no row covers fails the test.
- A row that matches no difference in the golden file fails the test, so a fixed divergence has to leave this file.

Every resolution below is **proposed**. Accepting a divergence, or choosing between our side and upstream, is Josh's call. Nothing here is accepted yet. Until Josh decides, the rows stand as "known and explained", which is what keeps `compare` honest.

Fixture text is not quoted here. The gaps are described by structure, and the fixtures are named so you can open them under `compare/fixtures/`.

## Machine-readable table

One row per difference class and case set. Columns: `id | signature | cases`. Cases are comma separated. The signature is the difference line from the golden file. A `runs:` line is reduced so it carries no fixture text, and nothing else is dropped:

- Every run on both sides keeps its workflow and result, so an extra or missing run changes the signature.
- A failed run keeps `reason#` and the first 8 hex digits of the SHA-256 of the quoted reason as the golden file prints it. The reason can quote fixture values, so the doc holds only the hash, and a different reason changes the signature. When a row goes stale after a reason change, read the new reason in `compare.golden` and decide whether it is still the same divergence.
- Every effect keeps its kind and target and drops its title, so an extra, missing or retargeted effect changes the signature.

```divergences
```

No rows are open. Each id needs a section below with a `Status:` line and an `Owner:` line, if a row ever returns. The test checks that.

## Accepted and normalised in compare: workflow names

Rust names the workflows `host-state`, `bugsink-issues` and `security-alerts`. Go names them `host_state`, `bugsink_issues` and `security_alerts`. Josh decided to keep the Go underscore names. A cutover uses a fresh Go store seeded from Rust, and a tracker finds its item by the outbox key alone (the GitHub `<!-- almanac:{key} -->` marker, the taskwarrior `almanac_key` UDA), so the workflow name never crosses over.

`compare` therefore treats those three pairs as the same workflow. The pairs live in the table `acceptedWorkflowRenames` in `compare/compare.go`. It is an explicit table, not a `-` to `_` rewrite, so a fourth workflow or any other rename still shows as a difference. Because it is normalised away, it has no row in the machine-readable block.
