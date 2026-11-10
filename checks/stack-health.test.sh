#!/usr/bin/env bash
set -euo pipefail

# Runs checks/stack-health once per scenario against a fake docker on PATH.
# The fake prints $TMP/ps.txt, records its arguments in $TMP/docker.args, and
# fails with a daemon error when $TMP/fail exists.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/nodocker"

cat >"$TMP/bin/docker" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$*" >"$TMP/docker.args"
if [ -e "$TMP/fail" ]; then
  echo "Cannot connect to the Docker daemon"
  exit 1
fi
cat "$TMP/ps.txt"
FAKE
chmod +x "$TMP/bin/docker"

bash_bin=$(command -v bash)
export TMP
export PATH="$TMP/bin:$PATH"

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
  out=$("$here/stack-health" 2>&1) || rc=$?
}

# svc <service> <state> <status>
svc() { printf '%s\t%s\t%s\n' "$1" "$2" "$3"; }
healthy=$(svc db running "Up 3 hours (healthy)"; svc almanac running "Up 3 hours (healthy)"; svc puller running "Up 3 hours (healthy)"; svc backup running "Up 3 hours (healthy)")
without() { printf '%s\n' "$healthy" | grep -v "^$1"$'\t'; }

printf '%s\n' "$healthy" >"$TMP/ps.txt"
run_check
check "all healthy" "0 STACK OK - 4 services healthy" "$rc $out"

{ without puller; svc puller running "Up 3 hours (unhealthy)"; } >"$TMP/ps.txt"
run_check
check "one unhealthy" "2 STACK CRITICAL - puller unhealthy" "$rc $out"

{ without backup; svc backup exited "Exited (1) 2 hours ago"; } >"$TMP/ps.txt"
run_check
check "one exited" "2 STACK CRITICAL - backup exited" "$rc $out"

without db >"$TMP/ps.txt"
run_check
check "one missing" "2 STACK CRITICAL - db missing" "$rc $out"

{ without puller | grep -v "^backup"$'\t'; svc backup exited "Exited (1) 2 hours ago"; svc puller running "Up 3 hours (unhealthy)"; } >"$TMP/ps.txt"
run_check
check "two bad" "2 STACK CRITICAL - puller unhealthy, backup exited" "$rc $out"

{ without almanac; svc almanac running "Up 5 seconds (health: starting)"; } >"$TMP/ps.txt"
run_check
check "still starting" "0 STACK OK - 4 services healthy" "$rc $out"

{ without db; svc db running "Up 3 hours"; } >"$TMP/ps.txt"
run_check
check "no health reported" "0 STACK OK - 4 services healthy" "$rc $out"

check "the right project" "ps -a --filter label=com.docker.compose.project=brined-almanac-prod --format {{.Label \"com.docker.compose.service\"}}"$'\t'"{{.State}}"$'\t'"{{.Status}}" "$(cat "$TMP/docker.args")"

touch "$TMP/fail"
run_check
check "docker fails" "3 STACK UNKNOWN - docker not reachable" "$rc $out"
rm -f "$TMP/fail"

rc=0
out=$(
  export PATH="$TMP/nodocker"
  "$bash_bin" "$here/stack-health" 2>&1
) || rc=$?
check "docker absent" "3 STACK UNKNOWN - docker not found" "$rc $out"

if [ "$fails" -ne 0 ]; then
  echo "$fails case(s) failed" >&2
  exit 1
fi
