#!/bin/sh
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
if [ "${AURIGA_INSTALL_MODE:-host}" = container ] && [ "${NAS_INSTALLER_ACTIVE:-}" != 1 ]; then
  exec sh "$NAS_ROOT/scripts/installer.sh" render-collab
fi
case "${1:-}" in ''|--initialize|--stopped) ;; *) die 'invalid collab render operation' ;; esac
_source="$AURIGA_DATA_ROOT/config/collab-source"
_boot=$(bootstrap_binary)
_input=${AURIGA_COLLAB_INPUT_DIR:-}
_rotation=
if [ -n "$_input" ]; then
  case "$_input" in /*) ;; *) die 'AURIGA_COLLAB_INPUT_DIR must be canonical absolute' ;; esac
  [ -d "$_input" ] && [ ! -L "$_input" ] && [ "$(readlink -f -- "$_input")" = "$_input" ] || die 'invalid collab approval directory'
fi
if [ -e "$_source" ] || [ -L "$_source" ] || [ -e "$_source.pending" ]; then
  "$_boot" collab -base "$AURIGA_DATA_ROOT/config/materials" -out "$_source" -public-origin "${AURIGA_COLLAB_PUBLIC_ORIGIN:-}" -check
  if ! _rotation=$("$_boot" collab-rotation path -data "$AURIGA_DATA_ROOT" -recipe "$NAS_ROOT"); then
    printf '%s\n' 'message_network=degraded rotation_snapshot_unavailable; continuing static projections for core startup' >&2
    _rotation_unavailable=1
  fi
  if [ -z "$_rotation" ] && [ -n "$_input" ] && [ ! -d "$AURIGA_DATA_ROOT/config/collab-operations" ]; then "$_boot" collab -base "$AURIGA_DATA_ROOT/config/materials" -out "$_source" -public-origin "${AURIGA_COLLAB_PUBLIC_ORIGIN:-}" -input "$_input/input.json"; fi
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
if [ "${_rotation_unavailable:-0}" = 1 ]; then
  sh "$NAS_ROOT/scripts/collab-materials.sh" "$_source" "$AURIGA_DATA_ROOT/config/collab-runtime" --rotated
  # Existing checked policy is immutable during an unavailable snapshot.
  # Never feed an output back as network-budget's input (it rejects aliasing).
  if [ -f "$AURIGA_DATA_ROOT/config/network-policy/runtime.json" ]; then
    _skip_budget=1
  fi
  _gateway="$_source/network/runtime.json"
elif [ -n "$_rotation" ]; then
  sh "$NAS_ROOT/scripts/collab-materials.sh" "$_source" "$AURIGA_DATA_ROOT/config/collab-runtime" --rotated
  _gateway="$_rotation/network/runtime.json"
else
  sh "$NAS_ROOT/scripts/collab-materials.sh" "$_source" "$AURIGA_DATA_ROOT/config/collab-runtime"
  _gateway="$AURIGA_DATA_ROOT/config/collab-runtime/network/runtime.json"
fi
# Public policy projections are separate from all sealed/byte-exact materials.
_policy="$AURIGA_DATA_ROOT/config/network-policy"
if [ -e "$_policy" ] || [ -L "$_policy" ]; then
  [ -d "$_policy" ] && [ ! -L "$_policy" ] && [ "$(stat -c '%u:%g:%a' "$_policy")" = 0:0:755 ] || die 'invalid network policy directory'
else
  mkdir "$_policy"; chown 0:0 "$_policy"; chmod 0755 "$_policy"
fi
if [ "${_skip_budget:-0}" != 1 ]; then
  "$_boot" network-budget -runtime "$_gateway" -broker "$AURIGA_DATA_ROOT/config/materials/network/broker.conf" -out-runtime "$_policy/runtime.json" -out-broker "$_policy/broker.conf" -operation-ms "${AURIGA_NETWORK_OPERATION_TIMEOUT_MS:-30000}"
fi

if [ -e "$AURIGA_DATA_ROOT/collab-tokens" ] || [ -L "$AURIGA_DATA_ROOT/collab-tokens" ]; then
  [ -d "$AURIGA_DATA_ROOT/collab-tokens" ] && [ ! -L "$AURIGA_DATA_ROOT/collab-tokens" ] \
    && [ "$(stat -c '%u:%g:%a' "$AURIGA_DATA_ROOT/collab-tokens")" = 10001:65534:750 ] || die 'invalid collab token directory'
else
  mkdir "$AURIGA_DATA_ROOT/collab-tokens"; chown 10001:65534 "$AURIGA_DATA_ROOT/collab-tokens"; chmod 0750 "$AURIGA_DATA_ROOT/collab-tokens"
fi

# A staged Bifrost override must follow the newly checked message edge as well.
# Core-only render has already selected the sealed base; failed message render
# leaves that core projection in place for the independent degraded startup.
if [ -e "$AURIGA_DATA_ROOT/config/materials-upgrades" ] || [ -L "$AURIGA_DATA_ROOT/config/materials-upgrades" ]; then
  install_bifrost_materials
  materials_upgrade project
fi
