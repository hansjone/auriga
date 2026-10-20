#!/bin/sh
# Non-secret status for the Auriga NAS test stack.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd docker

info "project=$AURIGA_NAS_PROJECT"
info "data_root=$AURIGA_DATA_ROOT"
if [ -f "$(marker_path)" ]; then
  info "initialized=yes"
else
  info "initialized=no"
fi

_rel=$(release_marker_path)
if [ -f "$_rel" ]; then
  info "release_marker=$_rel"
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$_rel" <<'PY'
import json, sys
try:
    m = json.load(open(sys.argv[1], encoding="utf-8"))
except Exception as e:
    print(f"release_parse_error={e}")
    sys.exit(0)
print(f"commit={m.get('commit','')}")
for k, v in (m.get("images") or {}).items():
    print(f"image_{k}_id={v.get('id','')}")
for k, v in (m.get("tools") or {}).items():
    print(f"tool_{k}={v}")
PY
  else
    info "release_marker=present (install python3 to pretty-print JSON)"
  fi
else
  info "release_marker=absent"
fi

run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps || true

HP=${AURIGA_HOST_PUBLISH_PLATFORM_HTTP:-}
if [ -n "$HP" ] && command -v curl >/dev/null 2>&1; then
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:${HP}/actuator/health" || true)
  info "platform_health_http=$code"
fi

info "status done (secrets/env contents not printed)"
