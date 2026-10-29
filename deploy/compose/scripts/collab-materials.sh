#!/bin/sh
# Run as root: source stays installer-owned/receipt-managed; runtime is uid10002.
set -eu
[ "$#" -eq 1 ] && [ "$(id -u)" -eq 0 ] || exit 2
VAR=$1
[ -d "$VAR" ] && [ ! -L "$VAR" ] || exit 1
SOURCE="$VAR/collab-installation"
if [ ! -e "$SOURCE" ] && [ ! -L "$SOURCE" ]; then exit 0; fi
[ -d "$SOURCE" ] && [ ! -L "$SOURCE" ] || exit 1
checked_file() {
  [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%h' "$1")" = 1 ] \
    || { echo 'collab material metadata refused' >&2; exit 1; }
}
directory() {
  if [ -e "$1" ] || [ -L "$1" ]; then
    [ -d "$1" ] && [ ! -L "$1" ] || exit 1
    [ "$(stat -c '%u:%g:%a' "$1")" = "$2:$3:$4" ] || exit 1
  else mkdir "$1"; chown "$2:$3" "$1"; chmod "$4" "$1"; fi
}
project() {
  checked_file "$1"
  if [ -e "$2" ] || [ -L "$2" ]; then
    checked_file "$2"
    cmp -s "$1" "$2" || { echo 'collab runtime differs; refusing replacement' >&2; exit 1; }
    [ "$(stat -c '%u:%g:%a' "$2")" = "$3:$4:$5" ] || exit 1
  else
    cp -- "$1" "$2"; chown "$3:$4" "$2"; chmod "$5" "$2"
  fi
}
RUNTIME="$VAR/collab-runtime"
directory "$RUNTIME" 10002 10002 700
directory "$RUNTIME/config" 10002 10002 700
directory "$RUNTIME/state" 10002 10002 700
for name in root.der signed.json announcement.json business.der protocol.der management.der collab.properties browser.crt ca.crt server.crt server.key client.crt client.key; do
  project "$SOURCE/$name" "$RUNTIME/config/$name" 10002 10002 600
done
project "$SOURCE/state.json" "$RUNTIME/state/state.json" 10002 10002 600
# Java creates its persistent lock beside the state; it is outside source receipt.
EDGE="$VAR/collab-edge"
directory "$EDGE" 0 65532 750
for name in edge-collab.crt edge-collab.key ca.crt; do
  project "$VAR/pki/$name" "$EDGE/$name" 0 65532 640
done
