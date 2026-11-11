#!/usr/bin/env bash
set -euo pipefail

# Runs scripts/fence-line against a fake av and op on PATH. The fake av execs everything after
# `--`. The fake op prints $TMP/op.out. ssh-keygen is the real one, so the validation is real.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

cat >"$TMP/bin/av" <<'FAKE'
#!/usr/bin/env bash
while [ "$1" != "--" ]; do shift; done
shift
exec "$@"
FAKE
cat >"$TMP/bin/op" <<'FAKE'
#!/usr/bin/env bash
cat "$TMP/op.out"
FAKE
chmod +x "$TMP/bin/av" "$TMP/bin/op"

ssh-keygen -q -t ed25519 -N '' -C '' -f "$TMP/key"
pub=$(cut -d' ' -f1,2 "$TMP/key.pub")
printf '%s\n' "$pub" >"$TMP/op.out"
blob=${pub#* }
other_blob=$(ssh-keygen -q -t ed25519 -N '' -C '' -f "$TMP/other" >/dev/null && cut -d' ' -f2 "$TMP/other.pub")

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

from=100.64.0.7
cmd='cat /var/lib/host-state/picklelab.json'
comment=brined-almanac-host-state
want="restrict,from=\"$from\",command=\"$cmd\" ssh-ed25519 $blob $comment"
flags=(--item host-state-report --from "$from" --command "$cmd" --comment "$comment")

run() {
  rc=0
  out=$("$here/fence-line" "$@" 2>"$TMP/stderr") || rc=$?
  err=$(cat "$TMP/stderr")
}
run_check() {
  rc=0
  out=$("$here/fence-line" --check "${flags[@]}" <"$TMP/authorized_keys" 2>"$TMP/stderr") || rc=$?
  err=$(cat "$TMP/stderr")
}
has() { if grep -qF -e "$2" <<<"$1"; then echo yes; else echo no; fi; }

run "${flags[@]}"
check "line exit" 0 "$rc"
check "line exact" "$want" "$out"

printf '%s the-mac\n' "$pub" >"$TMP/op.out"
run "${flags[@]}"
check "trailing comment exit" 0 "$rc"
check "trailing comment dropped" "$want" "$out"
printf '%s\n' "$pub" >"$TMP/op.out"

printf 'ssh-ed25519 %s\n' "$pub" >"$TMP/op.out"
run "${flags[@]}"
check "doubled type refused" 1 "$rc"
printf '%s\n' "$pub" >"$TMP/op.out"

run "${flags[@]}" --command 'echo "hi"'
check "quote in command refused" 1 "$rc"
check "quote in command says why" yes "$(has "$err" 'double quote')"

run --item x --from "" --command c --comment k
check "empty from exit" 2 "$rc"

run --item x --from f --command c
check "missing comment exit" 2 "$rc"

run --bogus
check "unknown arg exit" 2 "$rc"

printf '%s\n' "ssh-rsa" >"$TMP/op.out"
run "${flags[@]}"
check "non-key public value refused" 1 "$rc"
printf '%s\n' "$pub" >"$TMP/op.out"

{
  echo '# a comment'
  echo "ssh-ed25519 $other_blob me@laptop"
  echo "$want"
  echo
} >"$TMP/authorized_keys"
run_check
check "check passes exit" 0 "$rc"
check "check passes output" "ok $comment" "$out"

echo "restrict,from=\"$from\",command=\"$cmd\" ssh-ed25519 ssh-ed25519 $blob $comment" >"$TMP/authorized_keys"
run_check
check "doubled type exit" 1 "$rc"
check "doubled type names comment" yes "$(has "$err" "$comment")"
check "doubled type says differs" yes "$(has "$err" differs)"

echo "from=\"$from\",command=\"$cmd\" ssh-ed25519 $blob $comment" >"$TMP/authorized_keys"
run_check
check "missing restrict exit" 1 "$rc"
check "missing restrict shows both lines" yes "$(has "$err" 'expected: restrict,')"

echo "restrict,from=\"100.64.0.99\",command=\"$cmd\" ssh-ed25519 $blob $comment" >"$TMP/authorized_keys"
run_check
check "wrong from exit" 1 "$rc"
check "wrong from shows found line" yes "$(has "$err" 'from="100.64.0.99"')"

printf '%s\n%s\n' "$want" "$want" >"$TMP/authorized_keys"
run_check
check "two lines exit" 1 "$rc"
check "two lines says 2" yes "$(has "$err" '2 lines')"

echo "ssh-ed25519 $other_blob me@laptop" >"$TMP/authorized_keys"
run_check
check "none exit" 1 "$rc"
check "none says no line" yes "$(has "$err" 'no line')"

: >"$TMP/authorized_keys"
run_check
check "empty file exit" 1 "$rc"

echo "restrict,from=\"$from\",command=\"$cmd\" ssh-ed25519 garbage $comment" >"$TMP/authorized_keys"
run_check
check "garbage blob exit" 1 "$rc"

if ((fails)); then
  echo "$fails failed"
  exit 1
fi
echo "all passed"
