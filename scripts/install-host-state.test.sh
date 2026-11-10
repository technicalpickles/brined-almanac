#!/usr/bin/env bash
set -euo pipefail

# Renders the plist with --print, which changes nothing, and checks it. Never loads an agent.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

export HOME=$TMP/home
mkdir -p "$HOME"
export HOST_STATE_CHECKOUT=$repo
export BRINED_ALMANAC_STATE=$TMP/state

fails=0
check() {
  local name=$1 want=$2 got=$3
  if [ "$want" = "$got" ]; then
    echo "ok   $name"
  else
    echo "FAIL $name: want [$want] got [$got]"
    fails=$((fails + 1))
  fi
}

plist=$TMP/out.plist
"$here/install-host-state" --print >"$plist"

lint_rc=0
plutil -lint "$plist" >/dev/null || lint_rc=$?
check "plutil lint" 0 "$lint_rc"

pl() { plutil -extract "$1" raw -o - "$plist"; }

check label com.technicalpickles.brined-almanac.host-state "$(pl Label)"
check interval 300 "$(pl StartInterval)"
check run-at-load true "$(pl RunAtLoad)"
check path /opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin "$(pl EnvironmentVariables.PATH)"
check home "$HOME" "$(pl EnvironmentVariables.HOME)"
check state "$TMP/state" "$(pl EnvironmentVariables.BRINED_ALMANAC_STATE)"
check argc 2 "$(pl ProgramArguments)"
check collector "$repo/checks/collect" "$(pl ProgramArguments.0)"
check conf "$repo/checks/hosts/mac.conf" "$(pl ProgramArguments.1)"
check collector-exists yes "$([ -x "$(pl ProgramArguments.0)" ] && echo yes || echo no)"
check conf-exists yes "$([ -f "$(pl ProgramArguments.1)" ] && echo yes || echo no)"
check stdout-log "$TMP/state/host-state.log" "$(pl StandardOutPath)"
check stderr-log "$TMP/state/host-state.log" "$(pl StandardErrorPath)"
check print-writes-nothing no "$([ -e "$TMP/state" ] || [ -e "$HOME/Library" ] && echo yes || echo no)"

if "$here/install-host-state" --bogus >/dev/null 2>&1; then
  check unknown-flag-rejected rejected accepted
else
  check unknown-flag-rejected rejected rejected
fi

[ "$fails" -eq 0 ] || { echo "$fails failed"; exit 1; }
echo "all passed"
