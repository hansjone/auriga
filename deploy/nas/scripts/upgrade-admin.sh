#!/bin/sh
# Add current admin roles/schemas to an existing database, without a server restart.
# Run render.sh first. This command neither switches services nor commits materials.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'Usage: sh upgrade-admin.sh'
load_stack_env
[ -f "$(marker_path)" ] || die 'not initialized'
# Use the candidate bundle mounts in a disposable client. The running old
# postgres container can still have the old bundle's entrypoint bind mounts.
# Password stays in the helper process environment, never in Docker argv.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" run --rm --no-deps \
  --user 0:999 --entrypoint sh postgres -ec '
    export PGPASSWORD="$(cat "$POSTGRES_PASSWORD_FILE")"
    exec psql -X -q -h postgres -U auriga_bootstrap -d auriga -v ON_ERROR_STOP=1 -f /docker-entrypoint-initdb.d/01-roles.sql
  '
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" run --rm --no-deps audit-db-prepare
sh "$NAS_ROOT/scripts/upgrade-bifrost.sh"
