#!/usr/bin/env bash
# Explicit operation, after native register, offline approval and DBA role setup.
set -euo pipefail
[ "$#" -eq 3 ] || { echo 'expected input.json base-receipt-sha256 base-identity-sha256' >&2; exit 2; }
[ "$(uname -s)" = Linux ] || { echo 'activation requires Linux' >&2; exit 1; }
COMPOSE="$(cd "$(dirname "$0")/.." && pwd)"
INPUT="$(cd -- "$(dirname -- "$1")" && pwd)/$(basename -- "$1")"
ARGS=(-activate-collab -out "$COMPOSE/var" -network-collab-file "$INPUT" -base-receipt-sha256 "$2" -base-identity-sha256 "$3")
go -C "$COMPOSE/bootstrap" run . "${ARGS[@]}" -validate-access
bash "$COMPOSE/scripts/compose.sh" activation-stop
RUNNING="$(bash "$COMPOSE/scripts/compose.sh" activation-running)"
[ -z "$RUNNING" ] || { echo 'activation refused: control processes still running' >&2; exit 1; }
go -C "$COMPOSE/bootstrap" run . "${ARGS[@]}" -stopped-confirmed
if [ "$(id -u)" -eq 0 ]; then
  sh "$COMPOSE/scripts/collab-materials.sh" "$COMPOSE/var/collab-activation" "$COMPOSE/var/collab-activation-runtime"
else
  sudo -n sh "$COMPOSE/scripts/collab-materials.sh" "$COMPOSE/var/collab-activation" "$COMPOSE/var/collab-activation-runtime"
fi
bash "$COMPOSE/scripts/compose.sh" config --quiet
echo 'activation sealed; start through compose.sh and verify signed READY separately'
