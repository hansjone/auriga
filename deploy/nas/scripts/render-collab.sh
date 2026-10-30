#!/bin/sh
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
case "${1:-}" in ''|--initialize|--stopped) ;; *) die 'invalid collab render operation' ;; esac
_source="$AURIGA_DATA_ROOT/config/collab-source"
_boot=$(bootstrap_binary)
_input=${AURIGA_COLLAB_INPUT_DIR:-}
if [ -n "$_input" ]; then
  case "$_input" in /*) ;; *) die 'AURIGA_COLLAB_INPUT_DIR must be canonical absolute' ;; esac
  [ -d "$_input" ] && [ ! -L "$_input" ] && [ "$(readlink -f -- "$_input")" = "$_input" ] || die 'invalid collab approval directory'
fi
if [ -e "$_source" ] || [ -L "$_source" ] || [ -e "$_source.pending" ]; then
  "$_boot" collab -base "$AURIGA_DATA_ROOT/config/materials" -out "$_source" -public-origin "${AURIGA_COLLAB_PUBLIC_ORIGIN:-}" -check
  if [ -n "$_input" ]; then "$_boot" collab -base "$AURIGA_DATA_ROOT/config/materials" -out "$_source" -public-origin "${AURIGA_COLLAB_PUBLIC_ORIGIN:-}" -input "$_input/input.json"; fi
elif [ -n "$_input" ]; then
  case "${1:-}" in
    --initialize) [ ! -e "$(marker_path)" ] || die 'existing installation requires activate-collab.sh' ;;
    --stopped) ;;
    *) die 'run activate-collab.sh before starting an existing NAS installation' ;;
  esac
  "$_boot" collab -base "$AURIGA_DATA_ROOT/config/materials" -out "$_source" -public-origin "${AURIGA_COLLAB_PUBLIC_ORIGIN:-}" -input "$_input/input.json" -stopped-confirmed
else
  exit 0
fi
sh "$NAS_ROOT/scripts/collab-materials.sh" "$_source" "$AURIGA_DATA_ROOT/config/collab-runtime"
if [ -e "$AURIGA_DATA_ROOT/collab-tokens" ] || [ -L "$AURIGA_DATA_ROOT/collab-tokens" ]; then
  [ -d "$AURIGA_DATA_ROOT/collab-tokens" ] && [ ! -L "$AURIGA_DATA_ROOT/collab-tokens" ] \
    && [ "$(stat -c '%u:%g:%a' "$AURIGA_DATA_ROOT/collab-tokens")" = 10001:65534:750 ] || die 'invalid collab token directory'
else
  mkdir "$AURIGA_DATA_ROOT/collab-tokens"; chown 10001:65534 "$AURIGA_DATA_ROOT/collab-tokens"; chmod 0750 "$AURIGA_DATA_ROOT/collab-tokens"
fi
