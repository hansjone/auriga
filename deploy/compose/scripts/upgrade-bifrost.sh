#!/bin/sh
set -eu
COMPOSE=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
exec sh "$COMPOSE/scripts/compose.sh" run --rm --no-deps --pull never \
 --user 999:999 -e PGHOST=postgres --entrypoint sh postgres /docker-entrypoint-initdb.d/03-bifrost.sh
