#!/bin/sh
# Renew only the public manifest, using the existing receipted installation key.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
require_cmd docker
[ -f "$(marker_path)" ] || die "not initialized; run init.sh first"
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up --no-deps --no-build --pull never \
  --force-recreate --abort-on-container-exit --exit-code-from panel-prepare panel-prepare
