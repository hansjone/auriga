#!/bin/sh
# Root projects an independently sealed activation into least-privilege readers.
set -eu
[ "$#" = 2 ] && [ "$(id -u)" = 0 ] || exit 2
SOURCE=$1
RUNTIME=$2
directory() {
  if [ -e "$1" ] || [ -L "$1" ]; then
    [ -d "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a' "$1")" = "$2:$3:$4" ] || exit 1
  else
    mkdir "$1"; chown "$2:$3" "$1"; chmod "$4" "$1"
  fi
}
project() {
  [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a:%h' "$1")" = 0:0:600:1 ] || exit 1
  if [ -e "$2" ] || [ -L "$2" ]; then
    [ -f "$2" ] && [ ! -L "$2" ] && [ "$(stat -c '%u:%g:%a:%h' "$2")" = "$3:$4:$5:1" ] || exit 1
    cmp -s "$1" "$2" || { echo 'NAS collab runtime differs; refusing replacement' >&2; exit 1; }
  else
    _tmp=$(umask 077; mktemp "$(dirname -- "$2")/.install.XXXXXX")
    trap 'rm -f -- "$_tmp"' EXIT
    cat "$1" >"$_tmp"; chown "$3:$4" "$_tmp"; chmod "$5" "$_tmp"; mv -T "$_tmp" "$2"
    trap - EXIT
  fi
}
[ -d "$SOURCE" ] && [ ! -L "$SOURCE" ] || exit 1
directory "$RUNTIME" 0 0 700
directory "$RUNTIME/collab" 10002 10002 700
directory "$RUNTIME/state" 10002 10002 700
directory "$RUNTIME/edge" 0 65532 750
directory "$RUNTIME/network" 10001 10001 700
directory "$RUNTIME/platform" 10001 10001 700
directory "$RUNTIME/db" 0 0 700
for name in root.der signed.json announcement.json business.der protocol.der management.der collab.properties browser.crt ca.crt server.crt server.key client.crt client.key; do
  project "$SOURCE/collab-installation/$name" "$RUNTIME/collab/$name" 10002 10002 600
done
project "$SOURCE/collab-installation/state.json" "$RUNTIME/state/state.json" 10002 10002 600
if [ -e "$RUNTIME/state/state.json.lock" ] || [ -L "$RUNTIME/state/state.json.lock" ]; then
  [ -f "$RUNTIME/state/state.json.lock" ] && [ ! -L "$RUNTIME/state/state.json.lock" ] \
    && [ "$(stat -c '%u:%g:%a:%h' "$RUNTIME/state/state.json.lock")" = 10002:10002:600:1 ] || exit 1
fi
for name in ca.crt edge-collab.crt edge-collab.key; do project "$SOURCE/pki/$name" "$RUNTIME/edge/$name" 0 65532 640; done
project "$SOURCE/edge.config.json" "$RUNTIME/edge/edge.config.json" 0 65532 640
for name in runtime.json protocol.key; do project "$SOURCE/network/$name" "$RUNTIME/network/$name" 10001 10001 600; done
for name in platform.properties gateway.secret collab.secret managed-network.json; do project "$SOURCE/network-platform/$name" "$RUNTIME/platform/$name" 10001 10001 600; done
project "$SOURCE/roles.sql" "$RUNTIME/db/roles.sql" 0 0 600
