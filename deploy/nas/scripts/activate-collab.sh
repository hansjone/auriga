#!/bin/sh
# Upgrade only this fixed NAS project, retaining all volumes and instances.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'set AURIGA_COLLAB_INPUT_DIR in stack.env; no positional arguments'
load_stack_env
require_cmd docker
[ -f "$(marker_path)" ] || die 'initialize NAS first'
[ -n "${AURIGA_COLLAB_INPUT_DIR:-}" ] || die 'explicit collab approval directory required'
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop edge network collab platform 2>/dev/null \
  || run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop edge network platform
_running=$(run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps --status running -q platform edge network)
[ -z "$_running" ] || die 'NAS control processes still running'
if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  sh "$NAS_ROOT/scripts/installer.sh" activate-collab
else
  bootstrap_nas_materials
  sh "$NAS_ROOT/scripts/render-collab.sh" --stopped
fi
info 'collab activation sealed; run start.sh and verify signed READY separately'
