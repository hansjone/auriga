#!/bin/sh
# Stop only this Compose project and optionally registered instance containers.
# Default: keep volumes/data/secrets. Never wipe volumes or match unrelated containers.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

STOP_INSTANCES=0
case "${1:-}" in
  --help)
    echo 'Usage: sh stop.sh [--with-registered-instances]'
    echo 'Stops compose project only by default; preserves data and secrets.'
    exit 0 ;;
  --with-registered-instances) STOP_INSTANCES=1 ;;
  '') ;;
  *) die "unknown argument" ;;
esac

load_stack_env
require_cmd docker

info "stopping compose project=$COMPOSE_PROJECT_NAME (volumes retained)"
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop || true
# Remove containers but keep named/anonymous volumes and bind mounts.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" down --remove-orphans || true

if [ "$STOP_INSTANCES" -eq 1 ]; then
  REG="$AURIGA_DATA_ROOT/results/registered-instances.txt"
  if [ -f "$REG" ]; then
    while IFS= read -r cid || [ -n "$cid" ]; do
      case "$cid" in ''|\#*) continue ;; esac
      info "stopping registered instance container id_prefix=$(printf '%s' "$cid" | cut -c1-12)"
      docker stop "$cid" >/dev/null 2>&1 || true
      docker rm "$cid" >/dev/null 2>&1 || true
    done <"$REG"
  else
    info "no registered-instances.txt; skipped instance stop"
  fi
fi

info "stop complete; data/secrets retained under $AURIGA_DATA_ROOT"
