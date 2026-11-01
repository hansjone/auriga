#!/bin/sh
# File-backed secrets retain the preparing runner's UID; grant only group reads.
set -eu
[ "$#" -eq 1 ] || exit 2
BIFROST="$1/bifrost"
[ -d "$BIFROST" ] && [ ! -L "$BIFROST" ] || exit 1
# Validate every leaf before changing any metadata.
for name in admin_password encryption_key pg_password pg_migration_password; do
  path="$BIFROST/$name"
  [ -f "$path" ] && [ ! -L "$path" ] && [ "$(stat -c '%h' "$path")" = 1 ] \
    || { echo 'invalid Bifrost secret metadata' >&2; exit 1; }
done
for name in admin_password encryption_key pg_password pg_migration_password; do
  case "$name" in encryption_key) gid=10003 ;; admin_password) gid=10005 ;; pg_password|pg_migration_password) gid=999 ;; esac
  if [ "$(id -u)" -eq 0 ]; then
    chgrp "$gid" "$BIFROST/$name"
  else
    sudo -n chgrp "$gid" "$BIFROST/$name"
  fi
  chmod 640 "$BIFROST/$name"
done
