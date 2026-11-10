#!/usr/bin/env bash
set -euo pipefail

# Runs checks/puller-age once per scenario against a marker file in a temp directory.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'chmod -R u+rw "$TMP"; rm -rf "$TMP"' EXIT

export CHECK_NOW=1794326400
export PULLER_MARKER="$TMP/.last-pull"

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

run_check() {
  rc=0
  out=$("$here/puller-age" 2>&1) || rc=$?
}

fresh='PULLER OK - last pull 414s ago | age=414s;;1800'

printf '1794325986' >"$PULLER_MARKER"
run_check
check "fresh" "0 $fresh" "$rc $out"

printf '1794324601' >"$PULLER_MARKER"
run_check
check "one second under the limit" "0 PULLER OK - last pull 1799s ago" "$rc ${out%% |*}"

printf '1794324600' >"$PULLER_MARKER"
run_check
check "at the limit" "2 PULLER CRITICAL - last pull 1800s ago, limit 1800s | age=1800s;;1800" "$rc $out"

printf '1794240000' >"$PULLER_MARKER"
run_check
check "long dead" "2 PULLER CRITICAL - last pull 86400s ago" "$rc ${out%%, limit*}"

rm -f "$PULLER_MARKER"
run_check
check "no marker" "2 PULLER CRITICAL - no completed pull recorded" "$rc $out"

printf 'soon' >"$PULLER_MARKER"
run_check
check "not a number" "3 PULLER UNKNOWN - marker is not a timestamp" "$rc $out"

: >"$PULLER_MARKER"
run_check
check "empty" "3 PULLER UNKNOWN - marker is not a timestamp" "$rc $out"

printf '1794326500' >"$PULLER_MARKER"
run_check
check "later than now" "0 PULLER OK - last pull 0s ago" "$rc ${out%% |*}"

printf '1794325986\n' >"$PULLER_MARKER"
run_check
check "trailing newline" "0 $fresh" "$rc $out"

printf '99999999999999999999' >"$PULLER_MARKER"
run_check
check "20-digit marker" "3 PULLER UNKNOWN - marker is not a timestamp" "$rc $out"

printf '1794325986\n1794325986\n' >"$PULLER_MARKER"
run_check
check "two-line marker" "3 PULLER UNKNOWN - marker is not a timestamp" "$rc $out"

printf '1794325986' >"$PULLER_MARKER"
chmod 000 "$PULLER_MARKER"
run_check
chmod 644 "$PULLER_MARKER"
check "unreadable marker" "3 PULLER UNKNOWN - marker unreadable" "$rc $out"

if [ "$fails" -ne 0 ]; then
  echo "$fails case(s) failed" >&2
  exit 1
fi
