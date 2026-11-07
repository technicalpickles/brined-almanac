# A supported way to run an almanac as a service in a container

I wanted to run an almanac continuously on my Mac as a compose stack, and it works. But `contrib/` only has systemd units, and `docs/deploying.md` describes the broker plus the renovate almanac on one host with systemd. There's no container story.

What I found along the way:

- `SQLX_OFFLINE=true cargo build --release --locked` works out of the box because `crates/almanac/.sqlx` ships in the repo. A multi-stage image on `rust:1.99-bookworm` into `debian:bookworm-slim` (plus `ca-certificates` and `curl`) is about 180 MB.
- `run` needs only Postgres. It runs the `deliveries` migrations itself and starts its consumer cursor at the end of the log, so an almanac with no broker works fine for scan-driven workflows (it ticks `scan_interval` every 15 minutes).
- `ALMANAC_LISTEN` has to be a non-loopback address (I use `0.0.0.0:8788`) for a healthcheck from outside the process, since the default is `127.0.0.1:8788`.
- `sd_notify` is harmless without systemd, and `restart: unless-stopped` supervises it fine.

What was missing is a Containerfile and a compose example next to the systemd units, and a line in `deploying.md` saying an almanac can run without the broker when it only scans.

If you want them, I'll PR a generalized Containerfile and compose example from https://github.com/technicalpickles/brined-almanac once it has run for a while.
