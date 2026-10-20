#!/bin/sh
# Stop only this Compose project and optionally verified instance containers.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

STOP_INSTANCES=0
case "${1:-}" in
  --help)
    echo 'Usage: sh stop.sh [--with-registered-instances]'
    echo 'Stops compose project auriga-nas-test only; preserves data and secrets.'
    exit 0 ;;
  --with-registered-instances) STOP_INSTANCES=1 ;;
  '') ;;
  *) die "unknown argument" ;;
esac

load_stack_env
require_cmd docker

# M1: project name is fixed in lib.sh; refuse mismatch if somehow exported.
[ "$COMPOSE_PROJECT_NAME" = "$AURIGA_NAS_PROJECT" ] \
  || die "refusing stop: COMPOSE_PROJECT_NAME=$COMPOSE_PROJECT_NAME != $AURIGA_NAS_PROJECT"

info "stopping compose project=$AURIGA_NAS_PROJECT (volumes retained; no orphans sweep)"
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop || true
# No --remove-orphans (M1): only containers defined by this compose file.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" down || true

if [ "$STOP_INSTANCES" -eq 1 ]; then
  # Prefer label filter over free-form ID file (M2).
  DATA_AGENT="$AURIGA_DATA_ROOT/data/agent"
  info "stopping containers with auriga instance labels under $DATA_AGENT"
  docker ps -aq --filter "label=auriga.instance=true" 2>/dev/null | while IFS= read -r cid || [ -n "$cid" ]; do
    [ -n "$cid" ] || continue
    mounts=$(docker inspect -f '{{range .Mounts}}{{.Source}} {{end}}' "$cid" 2>/dev/null || true)
    case " $mounts " in
      *" $DATA_AGENT "*|*"$DATA_AGENT/"*)
        info "stopping verified instance id_prefix=$(printf '%s' "$cid" | cut -c1-12)"
        docker stop "$cid" >/dev/null 2>&1 || true
        docker rm "$cid" >/dev/null 2>&1 || true
        ;;
      *)
        info "skip container $(printf '%s' "$cid" | cut -c1-12): mounts not under data/agent"
        ;;
    esac
  done
  # Legacy file: only accept 64-hex or 12+ hex ids, still verify mounts.
  REG="$AURIGA_DATA_ROOT/results/registered-instances.txt"
  if [ -f "$REG" ]; then
    while IFS= read -r cid || [ -n "$cid" ]; do
      case "$cid" in ''|\#*) continue ;; esac
      case "$cid" in
        *[!0-9a-fA-F]*)
          info "skip non-hex id in registered-instances.txt"
          continue
          ;;
      esac
      [ "${#cid}" -ge 12 ] || { info "skip short id"; continue; }
      mounts=$(docker inspect -f '{{range .Mounts}}{{.Source}} {{end}}' "$cid" 2>/dev/null || true)
      case " $mounts " in
        *" $DATA_AGENT "*|*"$DATA_AGENT/"*)
          docker stop "$cid" >/dev/null 2>&1 || true
          docker rm "$cid" >/dev/null 2>&1 || true
          ;;
        *)
          info "skip unverified id_prefix=$(printf '%s' "$cid" | cut -c1-12)"
          ;;
      esac
    done <"$REG"
  fi
fi

info "stop complete; data/secrets retained under $AURIGA_DATA_ROOT"
