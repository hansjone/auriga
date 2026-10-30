#!/bin/sh
# Renew only the public manifest, using the existing receipted installation key.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
require_cmd docker
[ -f "$(marker_path)" ] || die "not initialized; run init.sh first"
out="$AURIGA_DATA_ROOT/config/panel"
helper="$NAS_ROOT/scripts/panel-renew-cron.sh"
case "${1:-}" in
  --install) exec sh "$helper" install "$NAS_ROOT/scripts/renew-panel.sh" "$out" "$AURIGA_DATA_ROOT" ;;
  --scheduled)
    running=$(run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps --status running -q edge) || {
      exec sh "$helper" run "$out" false
    }
    [ -n "$running" ] || exit 0
    ;;
  --invoke)
    run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up --no-deps --no-build --pull never \
      --force-recreate --abort-on-container-exit --exit-code-from panel-prepare panel-prepare
    exit $?
    ;;
  '') ;;
  *) exit 2 ;;
esac
exec sh "$helper" run "$out" sh "$NAS_ROOT/scripts/renew-panel.sh" --invoke
