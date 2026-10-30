#!/usr/bin/env bash
# Local installer root only; never sources caller environment or starts a stack.
set -euo pipefail
[ "$#" -eq 2 ] || { echo 'expected publish|install|status input.json' >&2; exit 2; }
case "$1" in publish|install|status) ;; *) exit 2 ;; esac
[ "$(uname -s)" = Linux ] && [ "$(id -u)" -eq 0 ] || exit 1
COMPOSE="$(cd "$(dirname "$0")/.." && pwd)"
INPUT="$(cd -- "$(dirname -- "$2")" && pwd)/$(basename -- "$2")"
exec go -C "$COMPOSE/bootstrap" run . key-rotation "$1" "$INPUT"
