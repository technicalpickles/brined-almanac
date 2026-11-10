FROM rust:1.99-bookworm AS build

# ivy ships its sqlx query cache (crates/almanac/.sqlx) with the git dependency, so
# queries are checked against it and the build needs no database.
ENV SQLX_OFFLINE=true CARGO_NET_GIT_FETCH_WITH_CLI=true
WORKDIR /src

# Build dependencies against a stub main first: this layer is reused until
# Cargo.toml or Cargo.lock change, so a source edit recompiles only this crate.
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo 'fn main() {}' > src/main.rs && cargo build --release --locked

COPY tiers.toml hosts.toml ./
COPY src ./src
RUN touch src/main.rs && cargo build --release --locked

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -u 10001 almanac \
    && mkdir -p /var/lib/brined-almanac/taskchampion /var/lib/brined-almanac/bugsink \
    && chown almanac /var/lib/brined-almanac/taskchampion /var/lib/brined-almanac/bugsink

COPY --from=build /src/target/release/brined-almanac /usr/local/bin/

USER almanac
ENTRYPOINT ["brined-almanac"]
CMD ["run"]
