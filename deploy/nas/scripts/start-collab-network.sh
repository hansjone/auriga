#!/bin/sh
# Message-only startup/recovery. PostgreSQL and core services must already run.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'Usage: sh start-collab-network.sh'
load_stack_env
# This command retries message checks independently of an earlier core render.
unset NAS_COLLAB_CONFIG_DEGRADED
require_cmd docker
[ -e "$AURIGA_DATA_ROOT/config/collab-source" ] || [ -L "$AURIGA_DATA_ROOT/config/collab-source" ] || [ -e "$AURIGA_DATA_ROOT/config/collab-source.pending" ] || exit 0
degraded() {
  printf '%s\n' "message_network=degraded $1; core services remain available" >&2
  _recovery_data=$(printf '%s' "$AURIGA_DATA_ROOT" | sed "s/'/'\"'\"'/g")
  _recovery_script=$(printf '%s' "$NAS_ROOT/scripts/start-collab-network.sh" | sed "s/'/'\"'\"'/g")
  printf "message_network_recovery: AURIGA_DATA_ROOT='%s' sh '%s'\n" "$_recovery_data" "$_recovery_script" >&2
  run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop collab network network-broker || :
  exit 1
}
if [ "${AURIGA_MESSAGE_NETWORK_OFFLINE:-0}" = 1 ]; then
  if [ -d "$AURIGA_DATA_ROOT/config/collab-operations" ]; then
    sh "$NAS_ROOT/scripts/rotate-collab-keys.sh" pause || :
  fi
  degraded maintenance_requested
fi
# One lock budget (30s), then only an already running PG may be awaited (120s).
sh "$NAS_ROOT/scripts/rotate-collab-keys.sh" startup-check || degraded readiness_failed
sh "$NAS_ROOT/scripts/render-collab.sh" || degraded render_failed
if ! run_compose --profile collab-network --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never collab network; then
  degraded startup_failed
fi
printf '%s\n' 'message_network=READY'
