#!/bin/sh
# Smoke checks for the isolated NAS test stack (no fake-positive CI shortcuts).
# Covers: platform health, portal/admin static, unauthenticated API rejection,
# optional one real instance + restart retention when W2V/W3 hooks are present.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd curl

PORTAL=${AURIGA_PORTAL_HOST:-portal.example.com}
ADMIN=${AURIGA_ADMIN_HOST:-admin.example.com}
INSTANCE_PARENT=${AURIGA_INSTANCE_PARENT_DOMAIN:-i.example.com}
EDGE_IP=${AURIGA_EDGE_LAN_IP:?}
EDGE_PORT=${AURIGA_EDGE_PORT:-8080}
HP=${AURIGA_HOST_PUBLISH_PLATFORM_HTTP:?}
RESULTS="$AURIGA_DATA_ROOT/results"
mkdir -p "$RESULTS"
STAMP=$(date -u +%Y%m%dT%H%M%SZ 2>/dev/null || date +%s)
LOG="$RESULTS/smoke-$STAMP.log"

log() { printf '%s\n' "$*" | tee -a "$LOG" >/dev/null; info "$*"; }

fail=0
check() {
  _name=$1
  _expect=$2
  shift 2
  _code=$(curl -s -o /tmp/auriga-smoke-body.$$ -w '%{http_code}' --max-time 15 "$@" || printf '000')
  if [ "$_code" = "$_expect" ]; then
    log "PASS $_name http=$_code"
  else
    log "FAIL $_name http=$_code expected=$_expect"
    fail=1
  fi
  rm -f /tmp/auriga-smoke-body.$$
}

check_body_has() {
  _name=$1
  _needle=$2
  shift 2
  _code=$(curl -s -o /tmp/auriga-smoke-body.$$ -w '%{http_code}' --max-time 15 "$@" || printf '000')
  if [ "$_code" = "200" ] && grep -q "$_needle" /tmp/auriga-smoke-body.$$ 2>/dev/null; then
    log "PASS $_name http=$_code body_has=$_needle"
  else
    log "FAIL $_name http=$_code body_missing=$_needle"
    fail=1
  fi
  rm -f /tmp/auriga-smoke-body.$$
}

log "smoke begin edge=$EDGE_IP:$EDGE_PORT"

# Platform health via loopback publish (not public).
check platform_health 200 "http://127.0.0.1:${HP}/actuator/health"

# Three origins via curl --resolve (no DNS change required).
RESOLVE_PORTAL="${PORTAL}:${EDGE_PORT}:${EDGE_IP}"
RESOLVE_ADMIN="${ADMIN}:${EDGE_PORT}:${EDGE_IP}"
RESOLVE_INST="smoke.${INSTANCE_PARENT}:${EDGE_PORT}:${EDGE_IP}"

check_body_has portal_static index.html \
  --resolve "$RESOLVE_PORTAL" -H "Host: $PORTAL" \
  "http://${PORTAL}:${EDGE_PORT}/"

check_body_has admin_static index.html \
  --resolve "$RESOLVE_ADMIN" -H "Host: $ADMIN" \
  "http://${ADMIN}:${EDGE_PORT}/"

# Unauthenticated API must not succeed (401/403).
_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
  --resolve "$RESOLVE_PORTAL" -H "Host: $PORTAL" \
  "http://${PORTAL}:${EDGE_PORT}/api/iam/v1/me" || printf '000')
case "$_code" in
  401|403) log "PASS unauth_me http=$_code" ;;
  *) log "FAIL unauth_me http=$_code expected=401|403"; fail=1 ;;
esac

# Full lifecycle (login → instance → idle/wake → revoke) lives in W2V/W3 public e2e.
# Invoke when operator provides the harness path; do not fork fake-dsh assertions here.
if [ -n "${AURIGA_E2E_HOOK:-}" ] && [ -x "${AURIGA_E2E_HOOK}" ]; then
  log "running AURIGA_E2E_HOOK=$AURIGA_E2E_HOOK"
  if EDGE_BASE="http://${EDGE_IP}:${EDGE_PORT}" \
    PORTAL_HOST="$PORTAL" ADMIN_HOST="$ADMIN" \
    INSTANCE_PARENT_DOMAIN="$INSTANCE_PARENT" \
    ENROLLMENT_FILE="$AURIGA_DATA_ROOT/enrollment/emergency-enrollment.json" \
    JOIN_TOKEN_FILE="$AURIGA_DATA_ROOT/join/token" \
    AGENT_VOLUME="$AURIGA_DATA_ROOT/data/agent" \
    "$AURIGA_E2E_HOOK"; then
    log "PASS e2e_hook"
  else
    log "FAIL e2e_hook"
    fail=1
  fi
else
  log "SKIP full_lifecycle (set AURIGA_E2E_HOOK to W2V/W3 runner when ready)"
  log "NOTE: without W3 real DSH digest+template this pack must NOT claim real acceptance"
fi

# Restart retention: if stack was restarted, postgres data dir must still exist.
if [ -d "$AURIGA_DATA_ROOT/data/postgres" ] && [ -f "$(marker_path)" ]; then
  log "PASS data_dirs_present"
else
  log "FAIL data_dirs_present"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  log "SMOKE FAILED (details in $LOG; no secrets)"
  exit 1
fi
log "SMOKE PASSED"
exit 0
