#!/bin/sh
# Read-only deploy smoke: core health + portal/admin reachability. No PARTIAL.
# Disabled by site.json server.smoke=false (deploy skips this script).
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd curl

fail=0
log() { info "deploy-smoke: $*"; }

for _svc in postgres nats registry platform edge agent bifrost egress; do
  if run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps --status running --services 2>/dev/null | tr ' ' '\n' | grep -qx "$_svc"; then
    log "PASS $_svc running"
  else
    log "FAIL $_svc not running"
    fail=1
  fi
done

if run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" exec -T platform sh /etc/auriga/iam-healthcheck.sh >/dev/null; then
  log 'PASS platform_health'
else
  log 'FAIL platform_health'
  fail=1
fi
if run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" exec -T egress /auriga-egress --check >/dev/null; then
  log 'PASS egress_listener'
else
  log 'FAIL egress_listener'
  fail=1
fi

if [ -f "$AURIGA_DATA_ROOT/config/materials/access.env" ]; then
  load_nas_access_env "$AURIGA_DATA_ROOT/config/materials/access.env"
  if [ -n "${AURIGA_NAS_PORTAL_URL:-}" ]; then
    _code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' --max-time 15 "$AURIGA_NAS_PORTAL_URL/" || printf '000')
    case "$_code" in
      200|301|302) log "PASS portal_http=$_code" ;;
      *) log "FAIL portal_http=$_code"; fail=1 ;;
    esac
  fi
  if [ -n "${AURIGA_NAS_ADMIN_URL:-}" ]; then
    _code=$(curl --noproxy '*' -s -o /dev/null -w '%{http_code}' --max-time 15 "$AURIGA_NAS_ADMIN_URL/" || printf '000')
    case "$_code" in
      200|301|302) log "PASS admin_http=$_code" ;;
      *) log "FAIL admin_http=$_code"; fail=1 ;;
    esac
  fi
fi

if [ "$fail" -ne 0 ]; then
  log 'FAILED'
  exit 1
fi
log 'PASSED'
exit 0
