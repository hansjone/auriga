#!/bin/sh
# Start the Auriga NAS test compose project (idempotent).
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
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

info "start requested; use status.sh / smoke.sh to verify"
info "after first seed start, set AURIGA_PLATFORM_SEED=false in stack.env and re-render"

# Compose waits for platform health before starting its dependents, so seed has
# finished here. Keep recovery material out of redirected installer/Compose logs.
if ! sh "$NAS_ROOT/scripts/emergency.sh" --first-start; then
  printf '%s\n' 'emergency handoff pending; in a private TTY run sh deploy-nas/scripts/emergency.sh show (requires root or Docker permission)' >&2
fi
