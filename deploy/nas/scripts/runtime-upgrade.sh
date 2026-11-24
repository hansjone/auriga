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
sh "$NAS_ROOT/scripts/preflight.sh"
runtime_action stage
_runtime_committed=0
stop_failed_agent() {
  run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop agent \
    || info 'Agent stop failed; inspect the fixed project before retry' >&2
}
rollback() {
  _exit=$?
  trap - EXIT
  if [ "$_exit" != 0 ]; then
    if [ "$_runtime_committed" = 1 ]; then
      stop_failed_agent
      info 'runtime committed; shared projection/restart failed. The new pointer, sequence and proofs remain. Existing DSH processes are retained; retry this public upgrade command with the same bundle/image to finish activation.' >&2
      exit "$_exit"
    fi
    info 'runtime activation failed; restoring previous pointer (sequence high water retained)' >&2
    if runtime_action rollback; then
      run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never platform agent \
        || info 'previous control services failed to recover; inspect status before retry' >&2
    else
      # stage may have idempotently reused an already committed candidate, or
      # commit may have renamed active.json before its directory fsync failed.
      # The current process flag cannot distinguish these durable states.
      stop_failed_agent
      info 'rollback refused; Agent stop requested, pointer/sequence/proofs retained. Inspect activation state and retry the same approved bundle/image.' >&2
    fi
  fi
  exit "$_exit"
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Recreate only control consumers. Existing instance volumes and collab stay put.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never platform agent
runtime_action commit
_runtime_committed=1
# Pending projection above retained only the verified committed predecessor.
# Switch Agent's Host inputs to the newly committed original runtime now.
if shared_present; then
  shared_action project >/dev/null
  run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --wait --wait-timeout 120 --no-build --pull never platform agent
fi
trap - EXIT INT TERM
info 'runtime activation committed; upgrade existing instances through the workspace API and verify them separately'
info 'next step for existing instances: GET /api/workspace/v1/admin/instances to read each {id} and generation, then send PATCH /api/workspace/v1/admin/instances/{id} with Content-Type: application/merge-patch+json, If-Match: "<generation>", body {"dshVersionPin": null} (requires admin session with workspace.instances.admin; browser cookie writes also require matching Origin and X-Auriga-CSRF; 401 means session expired, 403 request/entry rejected usually means CSRF/Origin mismatch rather than missing permission). See deploy/nas/README.md and docs/dev/runtime-release.md.'
