#!/usr/bin/env bash
set -euo pipefail

# Runs scripts/link-local against a fake primary checkout and worktree.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

primary="$TMP/primary"
worktree="$TMP/worktree"
mkdir -p "$primary/local/scratch" "$worktree"
echo strings >"$primary/local/forbidden-strings"
echo tiers >"$primary/local/tiers.toml"

"$here/link-local" "$primary" "$worktree" >/dev/null
[ "$(readlink "$worktree/local/forbidden-strings")" = "$primary/local/forbidden-strings" ] ||
  fail "forbidden-strings is not linked to the primary's"
[ "$(readlink "$worktree/local/tiers.toml")" = "$primary/local/tiers.toml" ] ||
  fail "tiers.toml is not linked to the primary's"
[ ! -e "$worktree/local/scratch" ] || fail "scratch in local/ was shared"

# A rerun changes nothing and says nothing.
out=$("$here/link-local" "$primary" "$worktree")
[ -z "$out" ] || fail "a rerun printed: $out"

# A file the worktree already has is left alone.
other="$TMP/other"
mkdir -p "$other/local"
echo mine >"$other/local/tiers.toml"
"$here/link-local" "$primary" "$other" >/dev/null
[ ! -L "$other/local/tiers.toml" ] || fail "an existing tiers.toml was replaced"
[ "$(cat "$other/local/tiers.toml")" = mine ] || fail "an existing tiers.toml changed"

# A file the primary lacks is skipped with a message, not an error.
bare="$TMP/bare"
fresh="$TMP/fresh"
mkdir -p "$bare" "$fresh"
out=$("$here/link-local" "$bare" "$fresh")
case "$out" in
  *"forbidden-strings"*) ;;
  *) fail "a missing forbidden-strings was not reported: $out" ;;
esac
[ ! -e "$fresh/local/forbidden-strings" ] || fail "a link to a missing file was made"

# Run in the primary itself, it does nothing.
"$here/link-local" "$primary" "$primary" >/dev/null
[ ! -L "$primary/local/forbidden-strings" ] || fail "the primary linked to itself"

echo "link-local: ok"
