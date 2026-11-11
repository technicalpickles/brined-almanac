#!/usr/bin/env bash
set -euo pipefail

# Runs puller/pull.sh once per scenario against a fake ssh on PATH.
# The fake serves $TMP/sessions.json and $TMP/transcript.<key>.jsonl, filtered
# by --since, and fails when $TMP/fail-sessions or $TMP/fail.<key> exists.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/transcripts"

cat >"$TMP/bin/ssh" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$TMP/ssh.log"
shift 13
case "$1" in
  sessions)
    [ ! -e "$TMP/fail-sessions" ] || { echo "ssh: connection refused"; exit 1; }
    cat "$TMP/sessions.json"
    ;;
  transcript)
    key=$2
    since=$((10#$4))
    cat >/dev/null
    [ ! -e "$TMP/fail.$key" ] || { echo '{"id":"partial"}'; exit 1; }
    jq -c --argjson s "$since" 'select(.timestamp_ms >= $s)' "$TMP/transcript.$key.jsonl"
    ;;
esac
FAKE
chmod +x "$TMP/bin/ssh"

begin="-----BEGIN OPENSSH ""PRIVATE KEY-----"
end="-----END OPENSSH ""PRIVATE KEY-----"
key_text=$(printf '%s\nfake\n%s' "$begin" "$end")

export TMP
export PATH="$TMP/bin:$PATH"
export TRANSCRIPTS="$TMP/transcripts"
export KEY_FILE="$TMP/key"
export PULLER_KEY="$key_text"
export PULL_HOST=h PULL_USER=u PULL_ONCE=1

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

run_pull() {
  rc=0
  "$here/pull.sh" 2>"$TMP/stderr" || rc=$?
}

event() {
  printf '{"id":"e%s","type":"message","timestamp_ms":%s,"message":{"role":"user"}}\n' "$1" "$1"
}

lines() { wc -l <"$1" | tr -d ' '; }
cursor() { jq -c . "$TRANSCRIPTS/.cursors.json"; }
strays() { find "$TRANSCRIPTS" -maxdepth 1 -type f ! -name 'agent-main-a.jsonl' ! -name '.cursors.json' ! -name '.last-pull'; }
ssh_lines() { wc -l <"$TMP/ssh.log" | tr -d ' '; }

# 1. key file
echo '{"sessions":[]}' >"$TMP/sessions.json"
echo old >"$KEY_FILE"
chmod 644 "$KEY_FILE"
run_pull
check "key file exit" 0 "$rc"
check "key file mode" 600 "$(stat -c %a "$KEY_FILE" 2>/dev/null || stat -f %Lp "$KEY_FILE")"
check "key file contents" "$key_text" "$(cat "$KEY_FILE")"
check "ssh uses key" 1 "$(head -n1 "$TMP/ssh.log" | grep -c -e "-i $KEY_FILE")"
check "ssh timeouts" 1 "$(head -n1 "$TMP/ssh.log" | grep -c -e "-o ConnectTimeout=30 -o ServerAliveInterval=15 -o ServerAliveCountMax=4")"

# 2. empty key
rm -f "$TMP/ssh.log" "$KEY_FILE"
rc=0
env PULLER_KEY='' "$here/pull.sh" 2>"$TMP/stderr" || rc=$?
check "empty key fails" 1 "$([ "$rc" -ne 0 ] && echo 1 || echo 0)"
check "empty key stderr" 1 "$(grep -c PULLER_KEY "$TMP/stderr")"
check "empty key no ssh" absent "$([ -e "$TMP/ssh.log" ] && echo present || echo absent)"
rm -f "$TRANSCRIPTS/.last-pull" "$TRANSCRIPTS/.cursors.json" "$TMP/ssh.log"

# 3. first pass
echo '[{"key":"agent:main:a","updatedAt":5000},{"key":"agent:main:heartbeat:x","updatedAt":9000}]' |
  jq -c '{sessions: .}' >"$TMP/sessions.json"
{ event 1000; event 2000; } >"$TMP/transcript.agent:main:a.jsonl"
run_pull
check "first pass exit" 0 "$rc"
check "first pass lines" 2 "$(lines "$TRANSCRIPTS/agent-main-a.jsonl")"
check "transcript readable by daemon uid" 1 "$(( $(( 8#$(stat -c %a "$TRANSCRIPTS/agent-main-a.jsonl" 2>/dev/null || stat -f %Lp "$TRANSCRIPTS/agent-main-a.jsonl") )) & 4 ? 1 : 0 ))"
check "first pass cursor" '{"agent:main:a":5001}' "$(cursor)"
check "heartbeat file" absent "$(ls "$TRANSCRIPTS"/*heartbeat* >/dev/null 2>&1 && echo present || echo absent)"
check "heartbeat not pulled" 0 "$(grep -c 'transcript agent:main:heartbeat' "$TMP/ssh.log" || true)"
check "last-pull exists" present "$([ -e "$TRANSCRIPTS/.last-pull" ] && echo present || echo absent)"
check "no stray temp files" "" "$(strays)"

# 4. unchanged session
before=$(ssh_lines)
run_pull
check "unchanged ssh calls" 1 "$(($(ssh_lines) - before))"
check "unchanged last ssh" 1 "$(tail -n1 "$TMP/ssh.log" | grep -c ' sessions$')"
check "unchanged lines" 2 "$(lines "$TRANSCRIPTS/agent-main-a.jsonl")"
check "unchanged cursor" '{"agent:main:a":5001}' "$(cursor)"

# 5. grown session
echo '[{"key":"agent:main:a","updatedAt":7000}]' | jq -c '{sessions: .}' >"$TMP/sessions.json"
{ event 1000; event 2000; event 6000; } >"$TMP/transcript.agent:main:a.jsonl"
run_pull
check "grown since" 1 "$(tail -n1 "$TMP/ssh.log" | grep -c 'transcript agent:main:a --since 5001$')"
check "grown lines" 3 "$(lines "$TRANSCRIPTS/agent-main-a.jsonl")"
check "grown cursor" '{"agent:main:a":7001}' "$(cursor)"

# 6. mid-stream failure
echo '[{"key":"agent:main:a","updatedAt":8000}]' | jq -c '{sessions: .}' >"$TMP/sessions.json"
touch "$TMP/fail.agent:main:a"
rm -f "$TRANSCRIPTS/.last-pull"
run_pull
check "mid-stream exit" 0 "$rc"
check "mid-stream lines" 3 "$(lines "$TRANSCRIPTS/agent-main-a.jsonl")"
check "mid-stream cursor" '{"agent:main:a":7001}' "$(cursor)"
check "mid-stream last-pull" present "$([ -e "$TRANSCRIPTS/.last-pull" ] && echo present || echo absent)"
check "mid-stream no stray temp" "" "$(strays)"
rm -f "$TMP/fail.agent:main:a"

# 7. listing failure
touch "$TMP/fail-sessions"
rm -f "$TRANSCRIPTS/.last-pull"
run_pull
check "listing failure exit" 1 "$([ "$rc" -ne 0 ] && echo 1 || echo 0)"
check "listing failure last-pull" absent "$([ -e "$TRANSCRIPTS/.last-pull" ] && echo present || echo absent)"
rm -f "$TMP/fail-sessions"

# 8. safe key
echo '[{"key":"agent:main:direct:abc","updatedAt":3000}]' | jq -c '{sessions: .}' >"$TMP/sessions.json"
event 1000 >"$TMP/transcript.agent:main:direct:abc.jsonl"
run_pull
check "safe key file" present "$([ -e "$TRANSCRIPTS/agent-main-direct-abc.jsonl" ] && echo present || echo absent)"

# 9. two changed sessions in one pass
echo '[{"key":"agent:main:p","updatedAt":3000},{"key":"agent:main:q","updatedAt":4000}]' |
  jq -c '{sessions: .}' >"$TMP/sessions.json"
event 1000 >"$TMP/transcript.agent:main:p.jsonl"
event 2000 >"$TMP/transcript.agent:main:q.jsonl"
run_pull
check "two sessions p" 1 "$(lines "$TRANSCRIPTS/agent-main-p.jsonl")"
check "two sessions q" 1 "$(lines "$TRANSCRIPTS/agent-main-q.jsonl")"

# 10. report fetch skipped without REPORT_KEY, transcript pass unaffected
echo '{"sessions":[]}' >"$TMP/sessions.json"
run_pull
check "no report key exit" 0 "$rc"
check "no report key stderr" 1 "$(grep -c 'fetch-report: REPORT_KEY is empty, skipping' "$TMP/stderr")"

[ "$fails" -eq 0 ] || { echo "$fails failed"; exit 1; }
echo "all passed"
