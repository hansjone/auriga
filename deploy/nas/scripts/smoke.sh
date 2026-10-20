#!/bin/sh
# Smoke checks for the isolated NAS test stack.
# Without AURIGA_E2E_HOOK (full lifecycle), exits 2 with PARTIAL unless
# AURIGA_SMOKE_ALLOW_PARTIAL=1 (M10).
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
BODY=$(mktemp "${TMPDIR:-/tmp}/auriga-smoke.XXXXXX")
trap 'rm -f "$BODY"' EXIT INT TERM

log() { printf '%s\n' "$*" | tee -a "$LOG" >/dev/null; info "$*"; }

fail=0
partial=0

check() {
  _name=$1
  _expect=$2
  shift 2
  _code=$(curl -s -o "$BODY" -w '%{http_code}' --max-time 15 "$@" || printf '000')
  if [ "$_code" = "$_expect" ]; then
    log "PASS $_name http=$_code"
  else
    log "FAIL $_name http=$_code expected=$_expect"
    fail=1
  fi
}

check_spa() {
  _name=$1
  shift
  _code=$(curl -s -o "$BODY" -w '%{http_code}' --max-time 15 "$@" || printf '000')
  _h=$(curl -sI --max-time 15 "$@" 2>/dev/null | tr -d '\r' || true)
  case "$_h" in
    *[Cc]ontent-[Tt]ype:*text/html*) ;;
    *) log "FAIL $_name missing content-type text/html"; fail=1; return ;;
  esac
  if [ "$_code" != "200" ]; then
    log "FAIL $_name http=$_code"
    fail=1
    return
  fi
  if grep -Eiq '<div[^>]+id=.?root.?|<title>' "$BODY"; then
    log "PASS $_name spa_shell"
  else
    log "FAIL $_name missing root/title markers"
    fail=1
  fi
}

log "smoke begin edge=$EDGE_IP:$EDGE_PORT"

check platform_health 200 "http://127.0.0.1:${HP}/actuator/health"

RESOLVE_PORTAL="${PORTAL}:${EDGE_PORT}:${EDGE_IP}"
RESOLVE_ADMIN="${ADMIN}:${EDGE_PORT}:${EDGE_IP}"

check_spa portal_static \
  --resolve "$RESOLVE_PORTAL" -H "Host: $PORTAL" \
  "http://${PORTAL}:${EDGE_PORT}/"

check_spa admin_static \
  --resolve "$RESOLVE_ADMIN" -H "Host: $ADMIN" \
  "http://${ADMIN}:${EDGE_PORT}/"

_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
  --resolve "$RESOLVE_PORTAL" -H "Host: $PORTAL" \
  "http://${PORTAL}:${EDGE_PORT}/api/iam/v1/me" || printf '000')
case "$_code" in
  401|403) log "PASS unauth_me http=$_code" ;;
  *) log "FAIL unauth_me http=$_code expected=401|403"; fail=1 ;;
esac

if [ -n "${AURIGA_E2E_HOOK:-}" ] && [ -x "${AURIGA_E2E_HOOK}" ]; then
  log "running AURIGA_E2E_HOOK"
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
  log "NOTE: without W3 real DSH this pack must NOT claim real acceptance"
  partial=1
fi

# Data dirs exist is a prerequisite check, not proof of restart retention.
if [ -d "$AURIGA_DATA_ROOT/data/postgres" ] && [ -f "$(marker_path)" ]; then
  log "PASS data_layout_present"
else
  log "FAIL data_layout_present"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  log "SMOKE FAILED (details in $LOG; no secrets)"
  exit 1
fi
if [ "$partial" -ne 0 ]; then
  if [ "${AURIGA_SMOKE_ALLOW_PARTIAL:-}" = "1" ]; then
    log "SMOKE PARTIAL (lifecycle skipped; AURIGA_SMOKE_ALLOW_PARTIAL=1)"
    exit 0
  fi
  log "SMOKE PARTIAL — full lifecycle not run; set AURIGA_SMOKE_ALLOW_PARTIAL=1 to accept"
  exit 2
fi
log "SMOKE PASSED"
exit 0
