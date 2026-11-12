#!/usr/bin/env bash
set -euo pipefail

# Runs scripts/check-forbidden in a throwaway repository and in a worktree linked to it. The
# worktree is an orphan, so the fixture never makes a commit.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

main="$TMP/main"
mkdir -p "$main/local"
git -C "$main" init -q
echo 'never-match-this-pattern' >"$main/.forbidden-patterns"
echo 'fixture-literal' >"$main/local/forbidden-strings"
echo 'carries fixture-literal' >"$main/leak.txt"

# In the main checkout, the literal is found, and the literals file itself is exempt.
if out=$(cd "$main" && "$here/check-forbidden" leak.txt local/forbidden-strings 2>&1); then
  fail "the main checkout passed a file holding a literal"
fi
[[ $out == "leak.txt:1: matched local literal" ]] || fail "main checkout printed: $out"

# A linked worktree has no local/, so it reads the main checkout's list.
git -C "$main" worktree add -q --orphan -b side "$TMP/side"
cp "$main/.forbidden-patterns" "$main/leak.txt" "$TMP/side/"
if out=$(cd "$TMP/side" && "$here/check-forbidden" leak.txt 2>&1); then
  fail "the worktree skipped the main checkout's literals"
fi
[[ $out == "leak.txt:1: matched local literal" ]] || fail "worktree printed: $out"

# A worktree's own copy wins over the main checkout's.
mkdir -p "$TMP/side/local"
echo 'other-literal' >"$TMP/side/local/forbidden-strings"
out=$(cd "$TMP/side" && "$here/check-forbidden" leak.txt 2>&1) ||
  fail "the worktree's own list was ignored: $out"

# With no list anywhere, the literal half is skipped and says so.
rm "$main/local/forbidden-strings" "$TMP/side/local/forbidden-strings"
out=$(cd "$TMP/side" && "$here/check-forbidden" leak.txt 2>&1) || fail "a missing list failed: $out"
[[ $out == *"skipping the literal check"* ]] || fail "a missing list was not reported: $out"

echo "check-forbidden: ok"
