#!/bin/sh
# Renew only the public manifest, using the existing receipted installation key.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
require_cmd docker
[ -f "$(marker_path)" ] || die "not initialized; run init.sh first"
case "${1:-}" in
  '') ;;
  *) exit 2 ;;
esac
# Periodic NAS checks are owned by panel-renew, never a host cron job. A manual
# check uses the same signer OS lock and remains an explicitly short-lived task.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up --no-deps --no-build --pull never \
  --force-recreate --abort-on-container-exit --exit-code-from panel-prepare panel-prepare
