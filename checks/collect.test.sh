#!/usr/bin/env bash
set -euo pipefail

# Runs checks/collect once per scenario against fake checks in a temp CHECK_DIR.
# Every run writes to $TMP/out/mac.json through a conf file; nothing touches the real state directory.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'chmod -R u+rwx "$TMP"; rm -rf "$TMP"' EXIT
mkdir -p "$TMP/checks"

OUT=$TMP/out/mac.json
export CHECK_TIMEOUT=5
export HOST_STATE_NOW=2026-11-10T15:00:00Z

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

# mkcheck <name> <exit code> <shell body>
mkcheck() {
  printf '#!/usr/bin/env bash\n%s\nexit %s\n' "$3" "$2" >"$TMP/checks/$1"
  chmod +x "$TMP/checks/$1"
}

# conf <check names>
conf() {
  cat >"$TMP/mac.conf" <<CONF
HOST=mac
OUT=$OUT
CHECK_DIR=$TMP/checks
CHECKS="$1"
CONF
}

reset() {
  chmod -R u+rwx "$TMP/out" 2>/dev/null || true
  rm -rf "$TMP/out" "$TMP/checks"
  mkdir -p "$TMP/out" "$TMP/checks"
}

run_collect() {
  rc=0
  "$here/collect" "$TMP/mac.conf" >"$TMP/stdout" 2>"$TMP/stderr" || rc=$?
}

field() { jq -r --arg n "$1" --arg f "$2" '.checks[] | select(.name == $n) | .[$f] | tostring' "$OUT" 2>/dev/null || true; }

reset
mkcheck a 0 'echo "A OK - fine"'
mkcheck b 1 'echo "B WARNING - meh"'
mkcheck c 2 'echo "C CRITICAL - bad"'
mkcheck d 3 'echo "D UNKNOWN - huh"'
conf "a b c d"
run_collect
check "each exit code: run" "0" "$rc"
check "each exit code: statuses" "0 1 2 3" "$(field a status) $(field b status) $(field c status) $(field d status)"
check "each exit code: outputs" "A OK - fine|B WARNING - meh|C CRITICAL - bad|D UNKNOWN - huh" "$(field a output)|$(field b output)|$(field c output)|$(field d output)"

reset
mkcheck a 0 'echo "A OK - fine"'
conf "a"
run_collect
check "report shape: fields" "host=mac generated_at=2026-11-10T15:00:00Z interval_seconds=300" \
  "host=$(jq -r .host "$OUT") generated_at=$(jq -r .generated_at "$OUT") interval_seconds=$(jq -r .interval_seconds "$OUT")"
check "report shape: keys" '["checks","generated_at","host","interval_seconds"] ["name","output","since","status"]' \
  "$(jq -c 'keys' "$OUT") $(jq -c '.checks[0] | keys' "$OUT")"

reset
mkcheck slow 0 'sleep 5; echo "SLOW OK - late"'
conf "slow"
export CHECK_TIMEOUT=1
run_collect
export CHECK_TIMEOUT=5
check "timeout: status" "2" "$(field slow status)"
check "timeout: output" "SLOW CRITICAL - timed out after 1s" "$(field slow output)"

reset
mkcheck odd 7 'echo "ODD OK - wrong code"'
conf "odd"
run_collect
check "exit code out of range" "3" "$(field odd status)"

reset
printf '#!/usr/bin/env bash\necho "NOEXEC OK - never runs"\n' >"$TMP/checks/noexec"
conf "noexec"
run_collect
check "check not executable: run" "0" "$rc"
check "check not executable: status" "3" "$(field noexec status)"
output=$(field noexec output)
check "check not executable: output" "NOEXEC UNKNOWN -" "${output:0:16}"

reset
mkcheck c 2 'echo "C CRITICAL - bad"'
conf "c"
export HOST_STATE_NOW=2026-11-10T15:00:00Z
run_collect
export HOST_STATE_NOW=2026-11-10T15:05:00Z
run_collect
check "since held" "2026-11-10T15:00:00Z" "$(field c since)"

reset
mkcheck c 0 'echo "C OK - fine"'
conf "c"
export HOST_STATE_NOW=2026-11-10T15:00:00Z
run_collect
mkcheck c 2 'echo "C CRITICAL - bad"'
export HOST_STATE_NOW=2026-11-10T15:05:00Z
run_collect
check "since reset" "2026-11-10T15:05:00Z" "$(field c since)"

export HOST_STATE_NOW=2026-11-10T15:00:00Z
reset
mkcheck c 2 'echo "C CRITICAL - bad"'
conf "c"
printf '{"host":' >"$OUT"
run_collect
check "previous report corrupt: run" "0" "$rc"
check "previous report corrupt: since" "2026-11-10T15:00:00Z" "$(field c since)"

reset
mkcheck c 2 'echo "C CRITICAL - bad"'
conf "c"
: >"$OUT"
run_collect
check "previous report empty: run" "0" "$rc"
check "previous report empty: since" "2026-11-10T15:00:00Z" "$(field c since)"

reset
mkcheck c 2 'echo "C CRITICAL - bad"'
conf "c"
printf '{"checks":[{"name":"c","status":2,"since":"yesterday-ish","output":"x"}]}' >"$OUT"
run_collect
check "previous since not a time" "2026-11-10T15:00:00Z" "$(field c since)"

reset
mkcheck hostile 2 'head -c 5000 /dev/zero | tr "\0" x; printf "\n\x60\t second line\n"'
conf "hostile"
run_collect
want=$(head -c 500 /dev/zero | tr '\0' x)
check "hostile output: valid JSON" "0" "$(jq . "$OUT" >/dev/null 2>&1 && echo 0 || echo 1)"
check "hostile output: first line, 500 bytes" "$want" "$(field hostile output)"
check "hostile output: byte length" "500" "$(jq -j '.checks[0].output' "$OUT" | wc -c | tr -d ' ')"

reset
# shellcheck disable=SC2016
mkcheck wide 2 'for _ in $(seq 400); do printf "\xe2\x82\xac"; done; printf "\n"'
conf "wide"
run_collect
euros=$(jq -j '.checks[0].output' "$OUT" | wc -c | tr -d ' ')
check "multi-byte cut: not split" "498" "$euros"
check "multi-byte cut: whole characters" "166" "$(jq -r '.checks[0].output | length' "$OUT")"

reset
mkcheck ctl 0 'printf "A\033[31mB\001C\n"'
conf "ctl"
run_collect
check "control bytes removed" "A[31mBC" "$(field ctl output)"

reset
mkcheck a 0 'echo "A OK - fine"'
conf "a"
run_collect
cp "$OUT" "$TMP/before.json"
chmod 555 "$TMP/out"
export HOST_STATE_NOW=2026-11-10T16:00:00Z
run_collect
chmod 755 "$TMP/out"
export HOST_STATE_NOW=2026-11-10T15:00:00Z
check "atomic write: exit nonzero" "yes" "$([ "$rc" -ne 0 ] && echo yes || echo no)"
check "atomic write: old report unchanged" "same" "$(cmp -s "$OUT" "$TMP/before.json" && echo same || echo different)"
check "atomic write: no tmp file" "mac.json" "$(ls -A "$TMP/out")"

reset
mkcheck a 0 'echo "A OK - fine"'
mkcheck b 2 'echo "B CRITICAL - bad"'
conf "a b"
run_collect
run_collect
check "no stray files" "mac.json" "$(ls -A "$TMP/out")"

reset
conf "a ../etc"
mkcheck a 0 'echo "A OK - fine"'
run_collect
check "bad check name: refused" "yes" "$([ "$rc" -ne 0 ] && [ ! -e "$OUT" ] && echo yes || echo no)"

if [ "$fails" -ne 0 ]; then
  echo "$fails case(s) failed" >&2
  exit 1
fi
