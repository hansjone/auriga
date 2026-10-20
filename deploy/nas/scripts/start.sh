#!/bin/sh
# Start the Auriga NAS test compose project (idempotent).
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd docker

[ -f "$(marker_path)" ] || die "not initialized; run init.sh first"
[ -f "$AURIGA_DATA_ROOT/stack.env" ] || {
  # Allow stack.env beside data root copy from operator.
  if [ -f "$NAS_ROOT/stack.env" ]; then
    cp "$NAS_ROOT/stack.env" "$AURIGA_DATA_ROOT/stack.env"
    chmod 0600 "$AURIGA_DATA_ROOT/stack.env"
  else
    die "missing $AURIGA_DATA_ROOT/stack.env"
  fi
}

"$NAS_ROOT/scripts/preflight.sh"
"$NAS_ROOT/scripts/network-preflight.sh" || {
  _ec=$?
  [ "$_ec" -eq 4 ] && die "network preflight inconclusive (exit 4); verify LAN IP before start"
  exit "$_ec"
}

# Re-render so host/domain changes apply without rotating secrets.
"$NAS_ROOT/scripts/render.sh"

# Ensure ingress dir permissions for edge gid.
mkdir -p "$AURIGA_DATA_ROOT/data/agent/ingress"
chmod 0750 "$AURIGA_DATA_ROOT/data/agent/ingress" || true

info "starting project $COMPOSE_PROJECT_NAME (no build)"
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --no-build

info "start requested; use status.sh / smoke.sh to verify"
info "after first seed start, set AURIGA_PLATFORM_SEED=false in stack.env and re-render"
