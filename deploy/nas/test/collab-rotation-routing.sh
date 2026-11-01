#!/bin/sh
# Public rotation/start routing, without a daemon or deployment credentials.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf -- "$TMP"' EXIT HUP INT TERM
mkdir -p "$TMP/nas/scripts" "$TMP/data/config/materials" "$TMP/bin"
cp "$ROOT/scripts/rotate-collab-keys.sh" "$TMP/nas/scripts/"
cat >"$TMP/nas/scripts/lib.sh" <<'EOF'
NAS_ROOT=$TEST_ROTATION_NAS
AURIGA_DATA_ROOT=$TEST_ROTATION_DATA
AURIGA_IMAGE_PLATFORM=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
AURIGA_INSTALL_MODE=container
load_stack_env() { :; }
die() { echo "$*" >&2; exit 1; }
EOF
cat >"$TMP/nas/scripts/installer.sh" <<'EOF'
printf '%s\n' "$@" >"$TEST_ROTATION_DATA/argv"
EOF
export TEST_ROTATION_NAS="$TMP/nas" TEST_ROTATION_DATA="$TMP/data"
for action in status check prepare prepare-recovery renew stop; do
 sh "$TMP/nas/scripts/rotate-collab-keys.sh" "$action"
 [ "$(cat "$TMP/data/argv")" = "collab-$action" ]
done
for action in publish install recover-expired; do
 sh "$TMP/nas/scripts/rotate-collab-keys.sh" "$action" /private/approved
 [ "$(head -n 1 "$TMP/data/argv")" = "collab-$action" ]
 [ "$(tail -n 1 "$TMP/data/argv")" = /private/approved ]
 if sh "$TMP/nas/scripts/rotate-collab-keys.sh" "$action"; then exit 1; fi
done
if sh "$TMP/nas/scripts/rotate-collab-keys.sh" reset; then exit 1; fi
if sh "$TMP/nas/scripts/rotate-collab-keys.sh" publish; then exit 1; fi
if sh "$TMP/nas/scripts/rotate-collab-keys.sh" status extra; then exit 1; fi
printf 'PASS collab_rotation_fixed_public_actions\n'
