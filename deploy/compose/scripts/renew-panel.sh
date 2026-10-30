#!/bin/sh
set -eu
scripts=$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)
out="$scripts/../var/panel"
helper="$scripts/../../nas/scripts/panel-renew-cron.sh"
case "${1:-}" in
  --install) exec sh "$helper" install "$scripts/renew-panel.sh" "$out" ;;
  --scheduled)
    # A stopped installation must remain stopped.
    running=$(bash "$scripts/compose.sh" ps --status running -q edge) || {
      exec sh "$helper" run "$out" false
    }
    [ -n "$running" ] || exit 0
    ;;
  '') ;;
  *) exit 2 ;;
esac
exec sh "$helper" run "$out" bash "$scripts/compose.sh" up --no-deps --no-build --pull never \
  --force-recreate --abort-on-container-exit --exit-code-from panel-prepare panel-prepare
