#!/bin/sh
# Literal argument bridge for the public offline CLI.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" "$@"
