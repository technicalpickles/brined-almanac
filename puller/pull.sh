#!/usr/bin/env bash
set -euo pipefail

# Keeps $TRANSCRIPTS fresh for the almanac daemon: lists OpenClaw sessions on
# picklelab over ssh and appends each changed session's transcript, from a
# per-session cursor, to $TRANSCRIPTS/<safe-key>.jsonl. The key on picklelab is
# fenced by a forced command that accepts only `sessions` and
# `transcript <key> --since <ms>`, so nothing else is ever sent.
#
# Env: PULL_HOST, PULL_USER, PULLER_KEY (private key text),
#      PULL_INTERVAL_MINUTES (10), TRANSCRIPTS (/transcripts),
#      KEY_FILE (/run/puller/key), PULL_ONCE=1 (one pass, then exit).

PULL_INTERVAL_MINUTES=${PULL_INTERVAL_MINUTES:-10}
TRANSCRIPTS=${TRANSCRIPTS:-/transcripts}
KEY_FILE=${KEY_FILE:-/run/puller/key}
cursors="$TRANSCRIPTS/.cursors.json"

log() { echo "puller: $*" >&2; }

safe_key() { printf '%s\n' "$1" | tr -c 'A-Za-z0-9_\n-' '-'; }

install_key() {
  if [ -z "${PULLER_KEY:-}" ]; then
    log "PULLER_KEY is empty"
    return 1
  fi
  umask 077
  mkdir -p "$(dirname "$KEY_FILE")"
  printf '%s\n' "$PULLER_KEY" >"$KEY_FILE"
  unset PULLER_KEY
}

remote() {
  ssh -i "$KEY_FILE" -o BatchMode=yes -o StrictHostKeyChecking=yes "$PULL_USER@$PULL_HOST" "$@"
}

list_sessions() {
  remote sessions |
    jq -c '.sessions[] | select(.key | test("heartbeat") | not) | {key, updatedAt}'
}

set_cursor() {
  local tmp
  tmp=$(mktemp "$TRANSCRIPTS/.cursors.XXXXXX")
  jq --arg k "$1" --argjson v "$2" '.[$k] = $v' "$cursors" >"$tmp"
  mv "$tmp" "$cursors"
}

pull_one() {
  local key=$1 updated=$2 cursor since safe tmp bytes
  cursor=$(jq -r --arg k "$key" '.[$k] // 0' "$cursors")
  [ "$updated" -gt "$cursor" ] || return 0

  safe=$(safe_key "$key")
  since=$cursor
  [ "$cursor" -ne 0 ] || since=0000000000000
  tmp=$(mktemp "$TRANSCRIPTS/.pull.XXXXXX")
  if remote transcript "$key" --since "$since" >"$tmp"; then
    bytes=$(wc -c <"$tmp" | tr -d ' ')
    cat "$tmp" >>"$TRANSCRIPTS/$safe.jsonl"
    set_cursor "$key" $((updated + 1))
    log "pulled $safe since=$cursor bytes=$bytes"
  else
    log "failed $safe since=$cursor"
  fi
  rm -f "$tmp"
}

pass() {
  local sessions key updated
  mkdir -p "$TRANSCRIPTS"
  [ -e "$cursors" ] || echo '{}' >"$cursors"
  if ! sessions=$(list_sessions); then
    log "listing sessions failed"
    return 1
  fi
  while IFS=$'\t' read -r key updated; do
    [ -n "$key" ] || continue
    pull_one "$key" "$updated"
  done < <(jq -r '[.key, .updatedAt] | @tsv' <<<"$sessions")
  date +%s >"$TRANSCRIPTS/.last-pull"
}

main() {
  install_key
  : "${PULL_HOST:?PULL_HOST is required}" "${PULL_USER:?PULL_USER is required}"
  if [ "${PULL_ONCE:-}" = 1 ]; then
    pass
    return
  fi
  while true; do
    pass || true
    sleep $((PULL_INTERVAL_MINUTES * 60))
  done
}

main
