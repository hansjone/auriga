#!/usr/bin/env bash
# Fixed recipe selection for all lifecycle/status calls; never source an env file.
set -euo pipefail
COMPOSE="$(cd "$(dirname "$0")/.." && pwd)"
VAR="$COMPOSE/var"
[ "$#" -gt 0 ] || exit 2
ARGS=(-f "$COMPOSE/docker-compose.yml")
if [ "$1" = bootstrap-registry ]; then
  shift; [ "$#" -eq 0 ] || exit 2
  if [ ! -e "$VAR/compose.env" ] && [ ! -L "$VAR/compose.env" ]; then
    exec docker compose "${ARGS[@]}" up -d registry
  fi
  set -- up -d registry
fi
[ -f "$VAR/compose.env" ] && [ ! -L "$VAR/compose.env" ] || { echo 'missing compose installation env' >&2; exit 1; }
ARGS+=(--env-file "$VAR/compose.env")
# Fixed stop/inspection escape for interrupted activation; these actions cannot start.
if [ "$1" = activation-stop ] || [ "$1" = activation-running ]; then
  ACTION=$1; shift; [ "$#" -eq 0 ] || exit 2
  ARGS+=(--profile network-collab)
  if [ -d "$VAR/collab-activation" ] && [ ! -L "$VAR/collab-activation" ]; then
    ARGS+=(-f "$COMPOSE/scripts/collab-activation.yml")
  fi
  if [ "$ACTION" = activation-stop ]; then
    exec docker compose "${ARGS[@]}" stop platform edge network collab
  fi
  exec docker compose "${ARGS[@]}" ps --status running -q platform edge network collab
fi
if [ -e "$VAR/.collab-activation.pending" ] || [ -L "$VAR/.collab-activation.pending" ] || [ -e "$VAR/.collab-activation.lock" ] || [ -L "$VAR/.collab-activation.lock" ]; then
  echo 'activation is incomplete or locked; inspect before lifecycle operations' >&2; exit 1
fi
if [ -e "$VAR/collab-activation" ] || [ -L "$VAR/collab-activation" ]; then
  go -C "$COMPOSE/bootstrap" run . -check-collab-activation -out "$VAR"
  if [ "$(id -u)" -eq 0 ]; then
    sh "$COMPOSE/scripts/collab-materials.sh" --check "$VAR/collab-activation" "$VAR/collab-activation-runtime"
  else
    sudo -n sh "$COMPOSE/scripts/collab-materials.sh" --check "$VAR/collab-activation" "$VAR/collab-activation-runtime"
  fi
  ARGS+=(-f "$COMPOSE/scripts/collab-activation.yml" --profile network-collab)
elif [ -e "$VAR/collab-installation" ] || [ -L "$VAR/collab-installation" ]; then
  [ -d "$VAR/collab-installation" ] && [ ! -L "$VAR/collab-installation" ] || exit 1
  ARGS+=(-f "$COMPOSE/scripts/collab-required.yml" --profile network-collab)
fi
case "$1" in up|start|stop|restart|down|ps|logs|config|build|exec) ;; *) exit 2 ;; esac
# Do not let a lifecycle call silently replace this recipe or environment.
for ARG in "$@"; do
  case "$ARG" in -f|--file|--file=*|--env-file|--env-file=*|--profile|--profile=*|--project-directory|--project-directory=*) exit 2 ;; esac
done
exec docker compose "${ARGS[@]}" "$@"
