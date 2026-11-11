#!/usr/bin/env bash
set -euo pipefail

# Fetches picklelab's host-state report into $HOST_STATE_DIR for almanac's
# host-state checks. The key on picklelab is fenced by a forced command that
# cats the report, so ssh is run with no command. Always exits 0: a missing or
# bad report must not break the transcript pass, and the old file stays put.
#
# Env: REPORT_KEY (private key text), REPORT_KEY_FILE (/run/puller/report-key),
#      PULL_HOST, PULL_USER, REPORT_HOST_NAME (picklelab),
#      HOST_STATE_DIR (/host-state).

REPORT_KEY_FILE=${REPORT_KEY_FILE:-/run/puller/report-key}
REPORT_HOST_NAME=${REPORT_HOST_NAME:-picklelab}
HOST_STATE_DIR=${HOST_STATE_DIR:-/host-state}

log() { echo "fetch-report: $*" >&2; }

if [ -z "${REPORT_KEY:-}" ]; then
  log "REPORT_KEY is empty, skipping"
  exit 0
fi

install_key() {
  local want tmp
  want=$(printf '%s\n' "$REPORT_KEY")
  if [ -f "$REPORT_KEY_FILE" ] && [ "$(cat "$REPORT_KEY_FILE")" = "$want" ]; then
    return 0
  fi
  mkdir -p "$(dirname "$REPORT_KEY_FILE")"
  tmp=$(umask 077 && mktemp "$REPORT_KEY_FILE.XXXXXX")
  if ! { printf '%s\n' "$REPORT_KEY" >"$tmp" && chmod 600 "$tmp" && mv "$tmp" "$REPORT_KEY_FILE"; }; then
    rm -f "$tmp"
    return 1
  fi
}

fetch() {
  local tmp="$HOST_STATE_DIR/$REPORT_HOST_NAME.json.tmp.$$"
  mkdir -p "$HOST_STATE_DIR"
  if ! ssh -i "$REPORT_KEY_FILE" -o BatchMode=yes -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o ConnectTimeout=30 -o ServerAliveInterval=15 -o ServerAliveCountMax=4 "$PULL_USER@$PULL_HOST" </dev/null >"$tmp"; then
    log "ssh failed"
  elif ! jq -e . "$tmp" >/dev/null 2>&1; then
    log "not json"
  elif ! jq -e --arg h "$REPORT_HOST_NAME" '.host == $h and (.checks | type) == "array"' "$tmp" >/dev/null; then
    log "not a report for $REPORT_HOST_NAME"
  elif ! { chmod 644 "$tmp" && mv "$tmp" "$HOST_STATE_DIR/$REPORT_HOST_NAME.json"; }; then
    log "could not install report"
  else
    return 0
  fi
  rm -f "$tmp"
}

{
  install_key && fetch
} || log "could not fetch report"
exit 0
