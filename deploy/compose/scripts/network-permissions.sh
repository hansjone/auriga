#!/bin/sh
# Keep installer ownership for receipt reuse; grant only the runtime group reads.
set -eu
[ "$#" -eq 1 ] || exit 2
VAR=$1
NETWORK="$VAR/network"
[ -d "$NETWORK" ] && [ ! -L "$NETWORK" ] || exit 1
runtime_group() {
  if [ "$(id -u)" -eq 0 ]; then chgrp 10001 "$@"
  else sudo -n chgrp 10001 "$@"; fi
}
for path in "$NETWORK"/* "$VAR/pki/gateway.key"; do
  [ -f "$path" ] && [ ! -L "$path" ] && [ "$(stat -c '%h' "$path")" = 1 ] \
    || { echo 'invalid network material metadata' >&2; exit 1; }
done
for path in "$NETWORK"/* "$VAR/pki/gateway.key"; do
  case "$path" in
    "$NETWORK/gateway.secret"|"$NETWORK/platform.properties") chmod 600 "$path" ;;
    *) runtime_group "$path"; chmod 640 "$path" ;;
  esac
done
runtime_group "$NETWORK"
chmod 750 "$NETWORK"
