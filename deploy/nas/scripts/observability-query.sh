#!/bin/sh
# stdin: one fixed-local Loki query_range URL; stdout: the original Loki JSON.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
IFS= read -r query_url || die 'one newline-terminated query URL required'
[ "${#query_url}" -le 16384 ] || die 'query URL too long'
case "$query_url" in
  http://127.0.0.1:3100/loki/api/v1/query_range\?*) ;;
  *) die 'only the local Loki query_range endpoint is allowed' ;;
esac
case "$query_url" in *'#'*|*' '*|*'\'*) die 'invalid query URL' ;; esac
if IFS= read -r extra || [ -n "${extra:-}" ]; then die 'exactly one query URL required'; fi
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" --profile observability \
  exec -T loki /busybox/wget -T 10 -qO- "$query_url"
