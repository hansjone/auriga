#!/bin/sh
# Real UID/mode/race checks; Compose and announcement authorization are stubs.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
if [ "$(uname -s)" != Linux ] || [ "$(id -u)" != 0 ]; then
  printf 'SKIP announcement_hook_security (requires Linux root/private mount namespace and different UID)\n'
  exit 0
fi
for command in unshare setpriv mount python3; do
  command -v "$command" >/dev/null || { printf 'SKIP announcement_hook_security (missing %s)\n' "$command"; exit 0; }
done
if ! unshare --mount --fork true 2>/dev/null; then
  printf 'SKIP announcement_hook_security (private mount namespace unavailable)\n'
  exit 0
fi
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
export HOOK_TEST_MOUNT="$temp"
unshare --mount --fork sh -eu -c '
  mount --make-rprivate /
  mount -t tmpfs -o size=16m,mode=0755 tmpfs "$HOOK_TEST_MOUNT"
  cp "$1/test/announcement-hook-security.py" "$HOOK_TEST_MOUNT/test.py"
  cp "$1/test/message-edge-recovery.sh" "$HOOK_TEST_MOUNT/message-edge-recovery.sh"
  cp "${HOOK_SECURITY_START_SCRIPT:-$1/scripts/start-collab-network.sh}" "$HOOK_TEST_MOUNT/start.sh"
  mount --bind "$HOOK_TEST_MOUNT" /tmp
  export TMPDIR=/tmp
  if [ "${HOOK_SECURITY_CASE:-}" = message-orchestration ]; then
    export HOOK_TEST_PRIVATE_TMP=1 R20_START_SCRIPT=/tmp/start.sh
    exec sh /tmp/message-edge-recovery.sh
  fi
  exec python3 /tmp/test.py
' sh "$root"
