#!/usr/bin/env bash
set -euo pipefail

# Runs scripts/fence-test against a fake av, op and ssh on PATH. The fake ssh logs its argv. With -L
# it listens on the forwarded port the way ssh does and, per $TMP/mode, either prints the server's
# refusal when something connects (refuse), stays quiet (allow), or exits before listening (die).
# Otherwise it prints $TMP/ssh.out.

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

command -v python3 >/dev/null || {
  echo "skip fence-test.test.sh: python3 is needed for the fake forward listener"
  exit 0
}

cat >"$TMP/bin/av" <<'FAKE'
#!/usr/bin/env bash
while [ "$1" != "--" ]; do shift; done
shift
exec "$@"
FAKE
cat >"$TMP/bin/op" <<'FAKE'
#!/usr/bin/env bash
echo fake-private-key
FAKE
cat >"$TMP/bin/ssh" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$TMP/ssh.log"
port=''
while (($#)); do
  if [ "$1" = -L ]; then
    port=${2%%:*}
    break
  fi
  shift
done
if [ -z "$port" ]; then
  cat "$TMP/ssh.out"
  exit 0
fi
mode=$(cat "$TMP/mode")
if [ "$mode" = die ]; then
  echo "Permission denied (publickey)." >&2
  exit 255
fi
exec python3 -c '
import socket, sys
mode, port = sys.argv[1], int(sys.argv[2])
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", port))
s.listen(5)
while True:
    c, _ = s.accept()
    if mode == "refuse":
        sys.stderr.write("channel 2: open failed: administratively prohibited: open failed\n")
        sys.stderr.flush()
    c.close()
' "$mode" "$port"
FAKE
chmod +x "$TMP/bin/av" "$TMP/bin/op" "$TMP/bin/ssh"

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
has() { if grep -qF -e "$2" <<<"$1"; then echo yes; else echo no; fi; }

run() {
  rc=0
  out=$("$here/fence-test" "$@" 2>&1) || rc=$?
}

echo report >"$TMP/ssh.out"
echo refuse >"$TMP/mode"
run --item host-state-report u@h
check "refused forward exit" 0 "$rc"
check "refused forward passes" yes "$(has "$out" 'PASS the server refused')"
check "id ignored" yes "$(has "$out" 'PASS id was ignored')"
check "ssh options" 1 "$(grep -c -e '^-n -F /dev/null -o IdentityAgent=none -i .* -o IdentitiesOnly=yes -o BatchMode=yes u@h$' "$TMP/ssh.log")"

echo allow >"$TMP/mode"
run --item host-state-report u@h
check "allowed forward exit" 1 "$rc"
check "allowed forward fails" yes "$(has "$out" 'FAIL forward: no refusal')"

echo die >"$TMP/mode"
run --item host-state-report u@h
check "dead ssh exit" 1 "$rc"
check "dead ssh reports stderr" yes "$(has "$out" 'Permission denied')"

echo refuse >"$TMP/mode"
echo 'uid=501(josh) gid=20' >"$TMP/ssh.out"
run --item host-state-report u@h
check "uid output exit" 1 "$rc"
check "uid output fails" yes "$(has "$out" 'FAIL id ran')"

run --item host-state-report
check "missing host exit" 2 "$rc"
run u@h
check "missing item exit" 2 "$rc"

if ((fails)); then
  echo "$fails failed"
  exit 1
fi
echo "all passed"
