#!/bin/sh
# Non-secret status for the Auriga NAS test stack.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd docker

info "project=$COMPOSE_PROJECT_NAME"
info "data_root=$AURIGA_DATA_ROOT"
if [ -f "$(marker_path)" ]; then
  info "initialized=yes"
else
  info "initialized=no"
fi
if [ -f "$(release_marker_path)" ]; then
  # Print only non-secret lines (commit, image ids) if present.
  awk -F= '
    $1 ~ /^(commit|image_|tool_|created)/ { print }
  ' "$(release_marker_path)" 2>/dev/null || info "release_marker=present"
else
  info "release_marker=absent"
fi

run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" ps || true

# Health endpoints via published loopback platform port only (no cookie/token dump).
HP=${AURIGA_HOST_PUBLISH_PLATFORM_HTTP:-}
if [ -n "$HP" ] && command -v curl >/dev/null 2>&1; then
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:${HP}/actuator/health" || true)
  info "platform_health_http=$code"
fi

info "status done (secrets/env contents not printed)"
