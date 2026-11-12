#!/bin/sh
# One host scheduler tick, using the existing NAS Compose and upgrade workflow.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
require_cmd python3 docker
[ "${AURIGA_OPS_RUNTIME_ENABLED:-false}" = true ] || die 'runtime operations are disabled'
export AURIGA_DATA_ROOT NAS_ROOT
exec python3 "$NAS_ROOT/scripts/ops-runtime-worker.py" \
  --config "$AURIGA_DATA_ROOT/config/ops-runtime-worker.json" \
  --directory "$AURIGA_DATA_ROOT/ops-runtime"
