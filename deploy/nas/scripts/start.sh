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

sh "$NAS_ROOT/scripts/render.sh"

if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  sh "$NAS_ROOT/scripts/installer.sh" prepare-socket
else
  prepare_socket_parent
fi

if project_is_running; then
  info "project already running — compose up -d is idempotent"
fi

info "starting project $AURIGA_NAS_PROJECT (no build, no pull)"
# Fail closed, including when Compose does not support --pull never.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --no-build --pull never

# Compose may replace edge while leaving an unchanged agent running. Its old
# instance-network endpoints disappear with that edge. Agent startup recovers
# only its verified instance bridges, preserving the existing DSH processes.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" restart --no-deps agent

# Messages are an optional availability domain. Their PG/state/key checks never
# authorize a core login/model service or weaken the network signature guards.
if [ -d "$AURIGA_DATA_ROOT/config/collab-source" ]; then
  if [ -d "$AURIGA_DATA_ROOT/config/collab-operations" ]; then
    if [ "${AURIGA_MESSAGE_NETWORK_OFFLINE:-0}" = 1 ]; then _schedule=pause; else _schedule=resume; fi
    sh "$NAS_ROOT/scripts/rotate-collab-keys.sh" "$_schedule" || printf '%s\n' 'message_network=degraded scheduler_control_failed' >&2
  fi
  _history_ready=1
  if [ -e "$AURIGA_DATA_ROOT/config/collab-operations" ] || [ -L "$AURIGA_DATA_ROOT/config/collab-operations" ]; then
    sh "$NAS_ROOT/scripts/rotate-collab-keys.sh" history-check || _history_ready=0
  fi
  if [ "${AURIGA_MESSAGE_NETWORK_OFFLINE:-0}" != 1 ] && [ "$_history_ready" = 1 ] \
    && sh "$NAS_ROOT/scripts/rotate-collab-keys.sh" check; then
    if ! run_compose --profile collab-network --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never collab network; then
      printf '%s\n' 'message_network=degraded startup_failed; core services remain available' >&2
      run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop collab network network-broker || :
    fi
  else
    printf '%s\n' 'message_network=degraded expired_or_incomplete_or_maintenance; operator action required; core services remain available' >&2
    run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop collab network network-broker || :
  fi
fi

info "start requested; use status.sh / smoke.sh to verify"
info "after first seed start, set AURIGA_PLATFORM_SEED=false in stack.env and re-render"

# Compose waits for platform health before starting its dependents, so seed has
# finished here. Keep recovery material out of redirected installer/Compose logs.
if ! sh "$NAS_ROOT/scripts/emergency.sh" --first-start; then
  printf '%s\n' 'emergency handoff pending; in a private TTY run sh deploy-nas/scripts/emergency.sh show (requires root or Docker permission)' >&2
fi
