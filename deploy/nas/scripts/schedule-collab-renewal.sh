#!/bin/sh
# Start only the fixed hourly request/expiry observer, before a stop-signing batch.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'schedule-collab-renewal accepts no arguments'
load_stack_env
run_compose --profile collab-renew --env-file "$AURIGA_DATA_ROOT/stack.env" \
  up -d --no-deps --no-build --pull never collab-renew
