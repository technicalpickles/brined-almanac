#!/usr/bin/env bash
set -euo pipefail

# Exercises scripts/almanac-harvest --dry-run against the synthetic comments and summaries in
# tests/fixtures/almanac-harvest. A fake gh on PATH fails the test if the script reaches for it.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fx=$here/../tests/fixtures/almanac-harvest
harvest=$here/almanac-harvest
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"
printf '#!/usr/bin/env bash\necho "gh called: $*" >&2\nexit 99\n' >"$TMP/bin/gh"
chmod +x "$TMP/bin/gh"
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

out=$TMP/out
stdout=$("$harvest" --dry-run --out "$out" \
  --case "$fx/brineworks-comment.json" "$fx/summary-subagent.json" \
  --case "$fx/pickleclaw-comment.json" "$fx/summary-claude-p.json" \
  --case "$fx/brineworks-second-comment.json" "$fx/summary-claude-p.json")

for f in brineworks/2026-11-12-widget-list brineworks/2026-11-12-widget-list-wrong-param-names pickleclaw/2026-11-12-web-search; do
  repo=${f%%/*}
  name=${f#*/}
  got=0
  diff -u "$fx/expected/$f.md" "$out/$repo/docs/almanac/cases/$name.md" || got=1
  check "record $f matches" 0 "$got"
done
check "no other files written" 3 "$(find "$out" -type f | wc -l | tr -d ' ')"
check "one PR per repo" 2 "$(grep -c '^would open a PR' <<<"$stdout")"
check "brineworks PR lists both records" 1 "$(grep -c 'Add 2 almanac case records' <<<"$stdout")"
check "says nothing was pushed" 1 "$(grep -c 'nothing pushed, no PR opened' <<<"$stdout")"
check "worker filled from the summary" 1 "$(grep -c '^  tokens: 140283$' "$out/brineworks/docs/almanac/cases/2026-11-12-widget-list.md")"
check "worker block replaces the flow form" 0 "$(grep -c 'worker: {' "$out/brineworks/docs/almanac/cases/2026-11-12-widget-list.md")"

reject() {
  local name=$1 comment=$2 want=$3 rc=0 err
  rm -rf "$TMP/bad"
  err=$("$harvest" --dry-run --out "$TMP/bad" --case "$fx/$comment" "$fx/summary-subagent.json" 2>&1 >/dev/null) || rc=$?
  check "$name exits 1" 1 "$rc"
  check "$name says why" 1 "$(grep -c -- "$want" <<<"$err")"
  check "$name writes nothing" 0 "$(find "$TMP/bad" -type f 2>/dev/null | wc -l | tr -d ' ')"
}
reject "comment without a block" no-block-comment.json "no fenced yaml block"
reject "repo outside the allowlist" bad-repo-comment.json "is not one of brineworks, pickleclaw"
reject "block missing a key" missing-key-comment.json "missing keys: cause"

rc=0
"$harvest" --dry-run --out "$TMP/bad2" \
  --case "$fx/brineworks-comment.json" "$fx/summary-subagent.json" \
  --case "$fx/no-block-comment.json" "$fx/summary-subagent.json" >/dev/null 2>&1 || rc=$?
check "one bad case fails the batch" 1 "$rc"
check "and nothing is written for the good one" 0 "$(find "$TMP/bad2" -type f 2>/dev/null | wc -l | tr -d ' ')"

if [ "$fails" -gt 0 ]; then
  echo "$fails failure(s)"
  exit 1
fi
