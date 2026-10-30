#!/bin/sh
# File-backed secrets retain the preparing runner's UID; grant only group reads.
set -eu
[ "$#" -eq 1 ] || exit 2
LITELLM="$1/litellm"
[ -d "$LITELLM" ] && [ ! -L "$LITELLM" ] || exit 1
# Validate every leaf before changing any metadata.
for name in master salt team pg_password; do
  path="$LITELLM/$name"
  [ -f "$path" ] && [ ! -L "$path" ] && [ "$(stat -c '%h' "$path")" = 1 ] \
    || { echo 'invalid LiteLLM secret metadata' >&2; exit 1; }
done
for name in master salt team pg_password; do
  case "$name" in master|salt) gid=10003 ;; team) gid=10001 ;; pg_password) gid=999 ;; esac
  if [ "$(id -u)" -eq 0 ]; then
    chgrp "$gid" "$LITELLM/$name"
  else
    sudo -n chgrp "$gid" "$LITELLM/$name"
  fi
  chmod 640 "$LITELLM/$name"
done
