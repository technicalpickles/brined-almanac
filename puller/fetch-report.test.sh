#!/usr/bin/env bash
set -euo pipefail

# Runs puller/fetch-report.sh once per scenario against a fake ssh on PATH.
# The fake logs its argv, prints $TMP/ssh.out to stdout and $TMP/ssh.err to
# stderr, and exits with the code in $TMP/ssh.rc (default 0).

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/transcripts" "$TMP/host-state"

cat >"$TMP/bin/ssh" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TMP/ssh.log"
[ ! -e "$TMP/ssh.err" ] || cat "$TMP/ssh.err" >&2
[ ! -e "$TMP/ssh.out" ] || cat "$TMP/ssh.out"
exit "$(cat "$TMP/ssh.rc" 2>/dev/null || echo 0)"
FAKE
chmod +x "$TMP/bin/ssh"

begin="-----BEGIN OPENSSH ""PRIVATE KEY-----"
end="-----END OPENSSH ""PRIVATE KEY-----"
key_text=$(printf '%s\nfake\n%s' "$begin" "$end")

export TMP
export PATH="$TMP/bin:$PATH"
export TRANSCRIPTS="$TMP/transcripts"
export HOST_STATE_DIR="$TMP/host-state"
export REPORT_KEY_FILE="$TMP/report-key"
export REPORT_KEY="$key_text"
export PULL_HOST=h PULL_USER=u

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

run_fetch() {
  rc=0
  "$here/fetch-report.sh" 2>"$TMP/stderr" || rc=$?
}

report() { printf '{"host":"%s","checks":[]}\n' "$1"; }
state_files() { find "$HOST_STATE_DIR" -mindepth 1 -exec basename {} \; | sort | tr '\n' ' '; }
mode() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }

# 1. good
report picklelab >"$TMP/ssh.out"
run_fetch
check "good exit" 0 "$rc"
check "good report" "$(cat "$TMP/ssh.out")" "$(cat "$HOST_STATE_DIR/picklelab.json")"
check "good only report file" "picklelab.json " "$(state_files)"
check "good key file mode" 600 "$(mode "$REPORT_KEY_FILE")"
check "good key file contents" "$key_text" "$(cat "$REPORT_KEY_FILE")"
check "good ssh uses key" 1 "$(grep -c -e "-i $REPORT_KEY_FILE" "$TMP/ssh.log")"
check "good ssh options" 1 "$(grep -c -e "-o BatchMode=yes -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o ConnectTimeout=30 -o ServerAliveInterval=15 -o ServerAliveCountMax=4 u@h$" "$TMP/ssh.log")"
good=$(cat "$HOST_STATE_DIR/picklelab.json")

# 2. not-json
echo garbage >"$TMP/ssh.out"
run_fetch
check "not-json exit" 0 "$rc"
check "not-json unchanged" "$good" "$(cat "$HOST_STATE_DIR/picklelab.json")"
check "not-json stderr" 1 "$(grep -c 'not json' "$TMP/stderr")"
check "not-json no temp files" "picklelab.json " "$(state_files)"

# 3. wrong-host
report mac >"$TMP/ssh.out"
run_fetch
check "wrong-host exit" 0 "$rc"
check "wrong-host unchanged" "$good" "$(cat "$HOST_STATE_DIR/picklelab.json")"
check "wrong-host stderr" 1 "$(grep -c 'not a report for picklelab' "$TMP/stderr")"
check "wrong-host no temp files" "picklelab.json " "$(state_files)"

# 4. ssh-fails: report valid-looking on stdout must not be accepted either
report picklelab >"$TMP/ssh.out"
echo 'cat: /var/lib/host-state/picklelab.json: No such file or directory' >"$TMP/ssh.err"
echo 1 >"$TMP/ssh.rc"
run_fetch
check "ssh-fails exit" 0 "$rc"
check "ssh-fails unchanged" "$good" "$(cat "$HOST_STATE_DIR/picklelab.json")"
check "ssh-fails stderr" 1 "$(grep -c 'ssh failed' "$TMP/stderr")"
check "ssh-fails one log line" 1 "$(grep -c '^fetch-report:' "$TMP/stderr")"
check "ssh-fails no temp files" "picklelab.json " "$(state_files)"
rm -f "$TMP/ssh.err" "$TMP/ssh.rc"

# 4b. checks not an array: same rejection, reason names the report
printf '{"host":"picklelab","checks":{}}\n' >"$TMP/ssh.out"
run_fetch
check "bad-checks unchanged" "$good" "$(cat "$HOST_STATE_DIR/picklelab.json")"
check "bad-checks stderr" 1 "$(grep -c 'not a report for picklelab' "$TMP/stderr")"

# 5. last-pull-untouched (after good and ssh-fails)
check "last-pull untouched" absent "$([ -e "$TRANSCRIPTS/.last-pull" ] && echo present || echo absent)"
check "transcripts untouched" "" "$(ls -A "$TRANSCRIPTS")"

# 6. no-key
rm -f "$TMP/ssh.log" "$REPORT_KEY_FILE"
rc=0
env REPORT_KEY='' "$here/fetch-report.sh" 2>"$TMP/stderr" || rc=$?
check "no-key exit" 0 "$rc"
check "no-key stderr" 1 "$(grep -c 'REPORT_KEY is empty, skipping' "$TMP/stderr")"
check "no-key no ssh" absent "$([ -e "$TMP/ssh.log" ] && echo present || echo absent)"
check "no-key no key file" absent "$([ -e "$REPORT_KEY_FILE" ] && echo present || echo absent)"

# 7. changed key is rewritten
report picklelab >"$TMP/ssh.out"
echo old >"$REPORT_KEY_FILE"
chmod 644 "$REPORT_KEY_FILE"
run_fetch
check "rewritten key contents" "$key_text" "$(cat "$REPORT_KEY_FILE")"
check "rewritten key mode" 600 "$(mode "$REPORT_KEY_FILE")"

# 8. custom host name
report other >"$TMP/ssh.out"
env REPORT_HOST_NAME=other "$here/fetch-report.sh" 2>"$TMP/stderr" || true
check "custom host file" present "$([ -e "$HOST_STATE_DIR/other.json" ] && echo present || echo absent)"

# 9. install fails (mv broken): logged, temp removed, old report kept
report picklelab >"$TMP/ssh.out"
mkdir -p "$TMP/badbin"
printf '#!/bin/sh\nexit 1\n' >"$TMP/badbin/mv"
chmod +x "$TMP/badbin/mv"
before=$(state_files)
PATH="$TMP/badbin:$PATH" run_fetch
check "mv-fails exit" 0 "$rc"
check "mv-fails stderr" 1 "$(grep -c 'could not install report' "$TMP/stderr")"
check "mv-fails no temp files" "$before" "$(state_files)"

[ "$fails" -eq 0 ] || { echo "$fails failed"; exit 1; }
echo "all passed"
