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

sh "$NAS_ROOT/scripts/renew-panel.sh" --remove || info "warning: could not remove panel cron; run renew-panel.sh --remove as the scheduling user"

info "stopping compose project=$AURIGA_NAS_PROJECT (volumes retained; no orphans sweep)"
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop || true
# No --remove-orphans (M1): only containers defined by this compose file.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" down || true

if [ "$STOP_INSTANCES" -eq 1 ]; then
  DATA_AGENT="$AURIGA_DATA_ROOT/data/agent"
  stop_verified_instance() {
    cid=$1
    case "$cid" in ''|*[!0-9a-fA-F]*) info "skip non-hex container id"; return ;; esac
    [ "${#cid}" -ge 12 ] && [ "${#cid}" -le 64 ] || { info "skip invalid container id length"; return; }
    managed=$(docker inspect -f '{{index .Config.Labels "auriga.managed"}}' "$cid" 2>/dev/null) || return 0
    instance=$(docker inspect -f '{{index .Config.Labels "auriga.instance"}}' "$cid" 2>/dev/null) || return 0
    [ "$managed" = true ] || { info "skip unmanaged container"; return; }
    printf '%s\n' "$instance" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$' \
      || { info "skip invalid instance label"; return; }
    # One mount per line: whitespace or a substring cannot impersonate a path boundary.
    mounts=$(docker inspect -f '{{range .Mounts}}{{if eq .Type "bind"}}{{println .Source}}{{end}}{{end}}' "$cid" 2>/dev/null) || return 0
    matched=0
    while IFS= read -r mount || [ -n "$mount" ]; do
      case "$mount" in
        "$DATA_AGENT/instances/$instance/"*)
          case "$mount" in *'/../'*|*'/./'*|*'//'*) continue ;; esac
          resolved=$(readlink -f "$mount" 2>/dev/null) || continue
          case "$resolved" in "$DATA_AGENT/instances/$instance/"*) matched=1 ;; esac
          ;;
      esac
    done <<EOF
$mounts
EOF
    [ "$matched" -eq 1 ] || { info "skip instance outside data/agent"; return; }
    info "stopping verified instance id_prefix=$(printf '%s' "$cid" | cut -c1-12)"
    docker stop "$cid" >/dev/null 2>&1 || true
    docker rm "$cid" >/dev/null 2>&1 || true
  }
  info "stopping containers with auriga instance labels under $DATA_AGENT"
  docker ps -aq --no-trunc --filter "label=auriga.managed=true" --filter "label=auriga.instance" 2>/dev/null | while IFS= read -r cid || [ -n "$cid" ]; do
    [ -n "$cid" ] || continue
    stop_verified_instance "$cid"
  done
  # The legacy registration file receives exactly the same ownership checks.
  REG="$AURIGA_DATA_ROOT/results/registered-instances.txt"
  if [ -f "$REG" ]; then
    while IFS= read -r cid || [ -n "$cid" ]; do
      case "$cid" in ''|\#*) continue ;; esac
      stop_verified_instance "$cid"
    done <"$REG"
  fi
fi

info "stop complete; data/secrets retained under $AURIGA_DATA_ROOT"
