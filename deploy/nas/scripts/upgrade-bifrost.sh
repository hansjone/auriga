#!/bin/sh
# Upgrade an existing database volume without restarting PostgreSQL or clearing data.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'Usage: sh upgrade-bifrost.sh'
load_stack_env
[ -f "$(marker_path)" ] || die 'not initialized'
# A one-off instance of the verified postgres image runs only psql. The existing
# database process remains running; no initdb, role password reset or data delete.
# NAS Compose 2.29 has no run --pull option. The postgres service pins
# pull_policy: never, including for this one-off helper.
# The bootstrap password is root/0600; group 999 reads the separate relay
# passwords. This client uses those existing mounts without changing owners.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" run --rm --no-deps \
 --user 0:999 -e PGHOST=postgres --entrypoint sh postgres /docker-entrypoint-initdb.d/03-bifrost.sh
