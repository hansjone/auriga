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
_edge_before=$(run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps -q edge) || degraded edge_inspection_failed
# A degraded core start uses the base edge configuration. Recover its actual
# consumer as well as the message producers; Compose leaves an unchanged edge alone.
if ! run_compose --profile collab-network --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never collab network edge; then
  degraded startup_failed
fi
_edge_after=$(run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps -q edge) || degraded edge_inspection_failed
[ -n "$_edge_after" ] || degraded edge_missing
if [ "$_edge_before" != "$_edge_after" ]; then
  # Edge replacement removes its instance bridges. Reuse the public agent
  # bridge recovery; do not restart any DSH instance or rotate its credentials.
  run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" restart --no-deps agent || degraded instance_bridge_recovery_failed
fi
# An internal service JWT cannot impersonate a member/device for this check.
# The named acceptance hook owns its existing short-lived user authorization;
# it must check the actual edge announcement HTTP200 and installation root.
_announcement_check=${AURIGA_NETWORK_ANNOUNCEMENT_CHECK:-}
if [ -z "$_announcement_check" ]; then
  printf '%s\n' 'message_network=SERVICES_READY announcement_verification=pending; configure AURIGA_NETWORK_ANNOUNCEMENT_CHECK with an authorized read-only consumer check'
  exit 2
fi
case "$_announcement_check" in /*) ;; *) degraded announcement_check_path_invalid ;; esac
[ -f "$_announcement_check" ] && [ ! -L "$_announcement_check" ] && [ -x "$_announcement_check" ] \
  && [ "$(readlink -f -- "$_announcement_check")" = "$_announcement_check" ] || degraded announcement_check_path_invalid
_check_owner=$(stat -c '%u' "$_announcement_check")
_check_mode=$(stat -c '%a' "$_announcement_check")
[ "$_check_owner" = 0 ] || [ "$_check_owner" = "$(id -u)" ] || degraded announcement_check_owner_invalid
[ "$((0$_check_mode & 022))" = 0 ] || degraded announcement_check_permissions_invalid
"$_announcement_check" || degraded announcement_verification_failed
printf '%s\n' 'message_network=READY'
