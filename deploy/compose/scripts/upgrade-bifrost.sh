#!/bin/sh
set -eu
COMPOSE=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
[ "$#" = 0 ] || { echo 'Usage: sh upgrade-bifrost.sh' >&2; exit 2; }
exec bash "$COMPOSE/scripts/compose.sh" upgrade-bifrost
