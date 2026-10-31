#!/bin/sh
# Activate an independently approved runtime without changing bootstrap inputs.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 2 ] || die 'Usage: sh runtime-upgrade.sh /absolute/public-bundle repo@sha256:digest'
candidate_bundle=$1
candidate_image=$2
load_stack_env
require_cmd docker
[ -f "$(marker_path)" ] || die 'initialize NAS first'
runtime_action() {
  _action=$1
  if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
    sh "$NAS_ROOT/scripts/installer.sh" "runtime-$_action" "$candidate_bundle" "$candidate_image"
  else
    set -- -action "$_action" -base "$AURIGA_DATA_ROOT/config/materials" \
      -out "$AURIGA_DATA_ROOT/config/runtime-source" -image "$candidate_image" -bundle "$candidate_bundle"
    "$(bootstrap_binary)" runtime-upgrade "$@"
  fi
}
runtime_action stage
rollback() {
  _exit=$?
  trap - EXIT
  if [ "$_exit" != 0 ]; then
    info 'runtime activation failed; restoring previous pointer (sequence high water retained)' >&2
    if runtime_action rollback; then
      run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never platform agent \
        || info 'previous control services failed to recover; inspect status before retry' >&2
    else
      info 'rollback refused; inspect activation state before further operations' >&2
    fi
  fi
  exit "$_exit"
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Recreate only control consumers. Existing instance volumes and collab stay put.
sh "$NAS_ROOT/scripts/preflight.sh"
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never platform agent
runtime_action commit
trap - EXIT INT TERM
info 'runtime activation committed; upgrade existing instances through the workspace API and verify them separately'
