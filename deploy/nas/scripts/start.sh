#!/bin/sh
# Start the Auriga NAS test compose project (idempotent).
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
case "${1:-}" in '') ;; --network-offline) [ "$#" = 1 ] || die 'unexpected start arguments'; export AURIGA_MESSAGE_NETWORK_OFFLINE=1 ;; *) die 'Usage: start.sh [--network-offline]' ;; esac
require_cmd docker

[ -f "$(marker_path)" ] || die "not initialized; run init.sh first"
[ -f "$AURIGA_DATA_ROOT/stack.env" ] || {
  if [ -f "$NAS_ROOT/stack.env" ]; then
    cp "$NAS_ROOT/stack.env" "$AURIGA_DATA_ROOT/stack.env"
    chmod 0600 "$AURIGA_DATA_ROOT/stack.env"
  else
    die "missing $AURIGA_DATA_ROOT/stack.env"
  fi
}

sh "$NAS_ROOT/scripts/preflight.sh"
sh "$NAS_ROOT/scripts/network-preflight.sh" || {
  _ec=$?
  [ "$_ec" -eq 4 ] && die "network preflight inconclusive (exit 4); verify LAN IP before start"
  exit "$_ec"
}

sh "$NAS_ROOT/scripts/render.sh" --core-only

# Invariant: message configuration/check failures may only degrade messages,
# never abort core startup. Keep core rendering strict; isolate message render
# in its own process and omit its overlay if any projection/check fails, so
# missing or rejected message bind sources cannot prevent core Compose up.
export NAS_COLLAB_CONFIG_DEGRADED=0
if ! sh "$NAS_ROOT/scripts/render-collab.sh"; then
  export NAS_COLLAB_CONFIG_DEGRADED=1
  printf '%s\n' 'message_network=degraded configuration_failed; continuing core startup without message projections' >&2
fi

if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  sh "$NAS_ROOT/scripts/installer.sh" prepare-socket
else
  prepare_socket_parent
fi

if project_is_running; then
  info "project already running — compose up -d is idempotent"
fi

# Apply idempotent role/schema additions also when the PG data volume already exists.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --no-build --pull never --wait postgres
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" exec -T postgres psql -U auriga_bootstrap -d auriga -v ON_ERROR_STOP=1 -f /docker-entrypoint-initdb.d/01-roles.sql

info "starting project $AURIGA_NAS_PROJECT (no build, no pull)"
# Fail closed, including when Compose does not support --pull never.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --no-build --pull never

# Compose may replace edge while leaving an unchanged agent running. Its old
# instance-network endpoints disappear with that edge. Agent startup recovers
# only its verified instance bridges, preserving the existing DSH processes.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" restart --no-deps agent

# Messages are an optional availability domain. Their PG/state/key checks never
# authorize a core login/model service or weaken the network signature guards.
if [ -d "$AURIGA_DATA_ROOT/config/collab-source" ] || [ "$NAS_COLLAB_CONFIG_DEGRADED" = 1 ]; then
  sh "$NAS_ROOT/scripts/start-collab-network.sh" || :
fi

info "start requested; use status.sh / smoke.sh to verify"
info "after first seed start, set AURIGA_PLATFORM_SEED=false in stack.env and re-render"

# Compose waits for platform health before starting its dependents, so seed has
# finished here. Keep recovery material out of redirected installer/Compose logs.
if ! sh "$NAS_ROOT/scripts/emergency.sh" --first-start; then
  printf '%s\n' 'emergency handoff pending; in a private TTY run sh deploy-nas/scripts/emergency.sh show (requires root or Docker permission)' >&2
fi
