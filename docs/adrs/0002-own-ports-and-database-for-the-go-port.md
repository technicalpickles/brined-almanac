# 2. Own port and database for the Go port

## Status

Proposed for the `go-port` branch. Covers local runs only. Nothing here changes `compose.prod.yaml`, `scripts/deploy` or the deployed service.

## Context

[ADR 0001](0001-port-to-go-alongside-rust.md) says the Go binary shares nothing with prod, so both can run at once, and leaves the ports and the database name until the first listener lands. This ADR records what each side listens on and stores, and picks Go's.

Rust is read at `origin/main` `4d3b90a`, which pins ivy/almanac `b8bdefa`. Go is read at `go-port` `605f886`, which pins ivy/almanac `go-rewrite` `86122f2`.

### What Rust listens on

One listener: `GET /healthz`, answered by the `run` daemon.

- The Rust library serves it in `serve_health` (`crates/almanac/src/daemon.rs`), at `config.listen` or `HEALTH_LISTEN = "127.0.0.1:8788"` (`crates/almanac/src/app.rs:22`, `:162`).
- The repository's `almanac.toml` says `listen = "127.0.0.1:8788"`.
- Prod sets `ALMANAC_LISTEN: 0.0.0.0:8788` inside the container and publishes `127.0.0.1:8788:8788` (`compose.prod.yaml`). The container healthcheck and `scripts/deploy` both curl `/healthz` on 8788.
- With prod up, `lsof -iTCP:8788 -sTCP:LISTEN` shows the container runtime holding `127.0.0.1:8788` for `brined-almanac-prod-almanac-1`.
- No webhook receiver. That is the broker (`crates/broker`, default `127.0.0.1:8787`), and `compose.prod.yaml` runs no broker. Events come from scans only.

### What Rust stores

- PostgreSQL, through sqlx migrations embedded in the library (`crates/almanac/migrations/0001` to `0005`, plus `crates/deliveries/migrations`). It creates `seen`, `pending`, `runs`, `run_subjects`, `run_log`, `tripped`, `outbox`, `ledger`, `cursors` and `deliveries`.
- Rows are scoped by almanac name: `brined-almanac` live, `brined-almanac.dry-run` otherwise (`Mode::scope`, `crates/almanac/src/effect.rs:191`).
- Prod: database `almanac`, user `almanac`, trust auth, in compose project `brined-almanac-prod`, service `db`, not published to the host. `DATABASE_URL=postgres://almanac@db:5432/almanac`.
- Dev: `compose.yaml` runs one Postgres per checkout, database `almanac`, user `almanac`, trust auth, published on `127.0.0.1:$DB_PORT` from portool. `mise run live` points `DATABASE_URL` at it.

### What Go's `app.Almanac` needs

- `run` refuses to start without `DATABASE_URL` ("DATABASE_URL is not set", measured with an empty environment).
- It answers `GET /healthz` at `listen` or `ALMANAC_LISTEN`, default `127.0.0.1:8790` (`app.DefaultListen`, `app/run.go:26`).
- It refuses any address that is not loopback (`healthAddress`, `app/run.go`). Measured with `listen = "0.0.0.0:8790"`: "ALMANAC_LISTEN=0.0.0.0:8790 is not a loopback address and port", exit 1.
- It reads `ALMANAC_CONFIG`, or `almanac.toml` in the working directory (`config.Load`). Run from the repository root, it picks up Rust's `listen = "127.0.0.1:8788"`. Measured with prod up: "cannot listen on 127.0.0.1:8788 ... address already in use", exit 1. Run from any other directory it listens on `127.0.0.1:8790`.
- It migrates with goose, versioned in its own tables `store_schema_version` and `deliveries_schema_version` (`store/postgres/postgres.go`, `deliveries/deliveries.go`). Its tables have the same names as Rust's (`seen`, `pending`, `runs`, `run_subjects`, `run_log`, `tripped`, `outbox`, `ledger`, `deliveries`, `cursors`), created with plain `CREATE TABLE`.
- It scopes rows by the same name, `brined-almanac`, through the same rule (`effect.Mode.Scope`).

So a shared database is not an option even in principle. Go's first migration meets Rust's tables under the same names, and if the schemas did line up, the two binaries would read and claim each other's rows under one scope. (Inferred from the DDL and the scope rule, not run.)

### Ports already taken

| Port | Holder |
|---|---|
| 8787 | Rust broker default, not deployed |
| 8788 | Rust almanac health, prod, published on the host |
| 8789 | Go broker default (`cmd/almanac-broker`) |
| 8790 | Go almanac health default |

## Decision

- **Health on `127.0.0.1:8790`.** That is Ivy's own default, so a Go almanac anywhere else uses the same port, and it is free of both Rust ports and the Go broker's. It is set in a Go-only config file, `almanac.go.toml`, which the Go tasks pass as `ALMANAC_CONFIG`, so the shared `almanac.toml` never decides Go's port. ADR 0001's porting plan says `almanac.toml` is shared by both implementations. For `listen` that stops being true.
- **Database `brined_almanac_go`, user `brined_almanac_go`, on its own server.** `compose.go.yaml` runs a Postgres of its own, on its own portool port (`GO_DB_PORT`, from `go_db` in `.portool.toml`), in its own compose project (`$COMPOSE_PROJECT_NAME-go`) with its own volume. The name differs from Rust's `almanac` on purpose. A `DATABASE_URL` aimed at the wrong server fails and creates nothing, instead of migrating into Rust's store. Measured against a checkout's Rust dev store: `role "brined_almanac_go" does not exist`, retried with backoff, and the Rust store still had no tables afterwards.
- **Tasks.** `mise run go:db` starts it, `mise run go:db:down` removes it and its volume, and `mise run go:run -- <verb>` runs a Go verb against it with `almanac.go.toml`. `go:db:down` leaves the portool block alone, because `DB_PORT` in the same block belongs to the Rust store.

Not decided here: the prod shape for Go. When it comes, the loopback rule means Go cannot copy Rust's `ALMANAC_LISTEN=0.0.0.0` and port publish. Its healthcheck runs inside the container, and the deploy script checks health with `docker compose exec`, not a curl from the host. Its compose project and database follow the names above (`brined-almanac-go-prod`, `brined_almanac_go`).

## Consequences

- Rust prod, Rust dev and Go dev run at the same time, on 8788 and 8790, with three separate Postgres servers. Measured with prod up: `mise run go:run -- run` logged `"scope":"brined-almanac.dry-run","read_only":true,"listen":"127.0.0.1:8790"`, both `/healthz` answered 200, and `brined_almanac_go` held Go's ten tables plus its two goose version tables.
- `mise run db:down` also removes the Go store, because `portool release` frees `GO_DB_PORT` with the rest of the block.
- `go run ./cmd/brined-almanac run` from the repository root without `ALMANAC_CONFIG` still reads `almanac.toml` and fails on 8788 while prod is up. It fails at once and touches nothing, but the safe way in is `mise run go:run`.
- Running `portool sync` in a checkout allocates one more port in its block.
- **8790 is not reserved with portool.** The health port is a fixed number, not an offset in a checkout's block, so portool does not know about it. Blocks are five ports wide and handed out from a range wide enough to include 8790 (`portool ls` shows the blocks in use), so a future block could cover 8790 and a Postgres could be published on it. `portool reserve 8790 --label go-almanac-health` closes that. It writes machine-wide portool state that every project on the machine shares, so this change does not run it. Until someone does, Go's health port depends on no block ever landing on 8790. If `go:run` fails with "address already in use" on 8790, check `portool ls` first.

## Options rejected

- **Change `almanac.toml`'s `listen`.** Rust's dev runs read it, so that moves Rust, which is the spec.
- **Have `main` default `ALMANAC_CONFIG` or `ALMANAC_LISTEN`, as it does `ALMANAC_READ_ONLY`.** Closes the root-directory trap above. It is a code change in `cmd/brined-almanac`, which this decision does not need, and Ivy's library may grow a way to declare it (see the read-only default ask in `docs/go-touch-points.md`).
- **One Postgres server, two databases.** Saves a container, but `compose.yaml` creates `almanac` and nothing else, and a second database there is one `DATABASE_URL` typo from the Rust store.
- **Same database, a separate schema through `search_path`.** Go's store has no schema setting, so this needs a URL option the library never promises to honour.
