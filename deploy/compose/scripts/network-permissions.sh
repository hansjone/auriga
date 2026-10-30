#!/bin/sh
# Keep installer ownership for receipt reuse; grant only the runtime group reads.
set -eu
[ "$#" -eq 1 ] || exit 2
VAR=$1
NETWORK="$VAR/network"
[ -d "$NETWORK" ] && [ ! -L "$NETWORK" ] || exit 1
PLATFORM="$VAR/network-platform"
[ -d "$PLATFORM" ] && [ ! -L "$PLATFORM" ] || exit 1
FILES='gateway.secret platform.properties'
COUNT=2
if [ -d "$VAR/collab-installation" ] && [ ! -L "$VAR/collab-installation" ]; then
  FILES="$FILES collab.secret"; COUNT=3
fi
[ "$(find "$PLATFORM" -mindepth 1 -maxdepth 1 | wc -l)" -eq "$COUNT" ] || exit 1
for name in $FILES; do
  path="$PLATFORM/$name"
  [ -f "$path" ] && [ ! -L "$path" ] && [ "$(stat -c '%h' "$path")" = 1 ] \
    || { echo 'invalid platform credential metadata' >&2; exit 1; }
  chmod 600 "$path"
done
# Preserve the generator UID, which is recorded as installation-owner.
# This directory itself is bind-mounted so Java sees owner-only permissions.
chmod 700 "$PLATFORM"
runtime_group() {
  if [ "$(id -u)" -eq 0 ]; then chgrp 10001 "$@"
  else sudo -n chgrp 10001 "$@"; fi
}
for path in "$NETWORK"/* "$VAR/pki/gateway.key"; do
  [ -f "$path" ] && [ ! -L "$path" ] && [ "$(stat -c '%h' "$path")" = 1 ] \
    || { echo 'invalid network material metadata' >&2; exit 1; }
done
for path in "$NETWORK"/* "$VAR/pki/gateway.key"; do
  runtime_group "$path"; chmod 640 "$path"
done
runtime_group "$NETWORK"
chmod 750 "$NETWORK"
