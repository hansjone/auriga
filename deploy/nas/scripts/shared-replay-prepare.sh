#!/bin/sh
# Install the fixed replay bucket and prove installer revocation before ingress.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'Usage: shared-replay-prepare.sh'
load_stack_env
shared_present || die 'install and commit the shared source first'
require_cmd docker
shared_source_committed
# Shutdown does not depend on accepting the source about to be replaced.
run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop edge
shared_replay_action stage >/dev/null
restore() {
  _shared_exit=$?
  trap - EXIT INT TERM
  if [ "$_shared_exit" != 0 ]; then
    shared_replay_action close >/dev/null 2>&1 || :
    # Recreate only NATS with the checked permanent configuration. If that
    # fails, stop both fixed services; no temporary installer may stay live.
    if _shared_restore_old=$(shared_nats_container) && run_compose __shared_replay_nats_final && shared_confirm_nats_replaced "$_shared_restore_old"; then :
    elif ! shared_emergency_stop; then
      printf '%s\n' 'fixed project Edge/NATS stop could not be confirmed; keep ingress closed and inspect Docker before retry' >&2
    fi
    run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" stop edge >/dev/null 2>&1 || shared_emergency_stop || :
    printf '%s\n' 'shared replay preparation failed; ingress remains closed; inspect the preserved source and retry the public prepare command' >&2
  fi
  exit "$_shared_exit"
}
trap restore EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
_shared_old_nats=$(shared_nats_container)
run_compose __shared_replay_nats_prepare
shared_confirm_nats_replaced "$_shared_old_nats"
sh "$NAS_ROOT/scripts/installer.sh" shared-replay-provision
_shared_old_nats=$(shared_nats_container)
run_compose __shared_replay_nats_final
shared_confirm_nats_replaced "$_shared_old_nats"
sh "$NAS_ROOT/scripts/installer.sh" shared-replay-verify-final
shared_replay_action check
trap - EXIT INT TERM
info 'shared replay bucket checked and temporary installer revoked; ingress may now start'
