#!/bin/sh
# Fixed public sequence and process-identity guards with a fake CLI only.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf -- "$TMP"' EXIT HUP INT TERM
mkdir -p "$TMP/scripts" "$TMP/bin" "$TMP/data/config/shared-source"
cp "$ROOT/scripts/shared-replay-prepare.sh" "$TMP/scripts/"
export SHARED_REPLAY_TEST_ROOT="$TMP" SHARED_REPLAY_TEST_LIB="${AURIGA_TEST_REPLAY_PUBLIC_LIBRARY:-$ROOT/scripts/lib.sh}"
cat >"$TMP/scripts/lib.sh" <<'EOF'
. "$SHARED_REPLAY_TEST_LIB"
NAS_ROOT=$SHARED_REPLAY_TEST_ROOT
AURIGA_DATA_ROOT=$NAS_ROOT/data
AURIGA_INSTALL_MODE=host
load_stack_env() { :; }
bootstrap_binary() { printf '%s/bootstrap' "$NAS_ROOT"; }
shared_replay_action() { printf 'replay %s\n' "$1" >>"$NAS_ROOT/sequence"; }
run_compose() {
  printf 'compose %s\n' "$*" >>"$NAS_ROOT/sequence"
  case "$1" in
    __shared_replay_nats_prepare) printf '%064d' 2 >"$NAS_ROOT/current" ;;
    __shared_replay_nats_final)
      [ "${SHARED_TEST_FINAL_FAIL:-0}" != 1 ] || return 74
      [ "${SHARED_TEST_SAME_PROCESS:-0}" != 1 ] || return 0
      printf '%064d' 3 >"$NAS_ROOT/current" ;;
  esac
}
EOF
cat >"$TMP/bootstrap" <<'EOF'
#!/bin/sh
set -eu
root=$SHARED_REPLAY_TEST_ROOT
[ "$#" = 7 ] && [ "$1" = shared-runtime ] && [ "$2" = -base ] \
  && [ "$3" = "$root/data/config/materials" ] && [ "$4" = -out ] \
  && [ "$5" = "$root/data/config/shared-source" ] && [ "$6" = -action ] && [ "$7" = check ] || exit 91
printf 'source check\n' >>"$root/sequence"
[ -d "$root/data/config/shared-source" ] && [ "$(cat "$root/source-state")" = committed ] || exit 59
EOF
cat >"$TMP/scripts/installer.sh" <<'EOF'
#!/bin/sh
printf 'installer %s\n' "$1" >>"$SHARED_REPLAY_TEST_ROOT/sequence"
if [ "$1" = shared-replay-verify-final ]; then exit "${SHARED_TEST_VERIFY_EXIT:-0}"; fi
EOF
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
set -eu
root=$SHARED_REPLAY_TEST_ROOT
printf '%s\n' "$*" >>"$root/docker.log"
current=$(cat "$root/current")
case "$1" in
  ps)
    case "$*" in
      *id=*) [ "${SHARED_TEST_OLD_RUNNING:-0}" = 1 ] && printf '%064d' 2 || : ;;
      *service=edge*) ;;
      *) [ ! -f "$root/stopped" ] && printf '%s' "$current" || : ;;
    esac ;;
  inspect)
    case "$3" in
      *Config.Labels*) printf 'auriga-nas-test/nats' ;;
      *State.Running*) printf true ;;
      *) exit 91 ;;
    esac ;;
  stop) touch "$root/stopped" ;;
  *) exit 92 ;;
esac
EOF
chmod 0755 "$TMP/bootstrap" "$TMP/bin/docker" "$TMP/scripts/installer.sh"
export PATH="$TMP/bin:$PATH" NAS_ROOT="$TMP"
run_case() {
  name=$1; expected=$2
  require_cleanup=${3:-1}
  : >"$TMP/sequence"; : >"$TMP/docker.log"
  rm -f "$TMP/stopped"
  printf '%064d' 1 >"$TMP/current"
  if sh "$TMP/scripts/shared-replay-prepare.sh" >"$TMP/out" 2>&1; then result=0; else result=$?; fi
  if [ "$result" != "$expected" ]; then cat "$TMP/out"; printf 'FAIL %s exit=%s expected=%s\n' "$name" "$result" "$expected"; exit 1; fi
  if [ "$expected" != 0 ]; then
    ! grep -q '^replay check$' "$TMP/sequence"
    if [ "$require_cleanup" = 1 ]; then grep -q '^replay close$' "$TMP/sequence"
    else ! grep -q '^compose ' "$TMP/sequence"; fi
  fi
  printf 'PASS %s\n' "$name"
}
printf 'committed\n' >"$TMP/source-state"
run_case public_replay_prebuild_recreate_revocation_sequence 0
cat >"$TMP/expected" <<'EOF'
source check
compose --env-file PLACEHOLDER/stack.env stop edge
replay stage
compose __shared_replay_nats_prepare
installer shared-replay-provision
compose __shared_replay_nats_final
installer shared-replay-verify-final
replay check
EOF
sed "s|PLACEHOLDER|$TMP/data|" "$TMP/expected" >"$TMP/expected-path"
cmp "$TMP/expected-path" "$TMP/sequence"
SHARED_TEST_SAME_PROCESS=1; export SHARED_TEST_SAME_PROCESS
run_case unchanged_final_nats_process_cannot_confirm_revocation 1
! grep -q '^installer shared-replay-verify-final$' "$TMP/sequence"
unset SHARED_TEST_SAME_PROCESS
SHARED_TEST_OLD_RUNNING=1; export SHARED_TEST_OLD_RUNNING
run_case old_nats_process_still_running_keeps_ingress_closed 1
unset SHARED_TEST_OLD_RUNNING
SHARED_TEST_VERIFY_EXIT=76; export SHARED_TEST_VERIFY_EXIT
run_case failed_final_network_check_never_opens_ingress 76
unset SHARED_TEST_VERIFY_EXIT
SHARED_TEST_FINAL_FAIL=1; export SHARED_TEST_FINAL_FAIL
run_case failed_final_restore_stops_only_fixed_project_services 74
grep -q '^stop --time 10 ' "$TMP/docker.log"
! grep -Eq 'docker.sock|network connect|--privileged' "$TMP/docker.log"
unset SHARED_TEST_FINAL_FAIL
rmdir "$TMP/data/config/shared-source"
run_case absent_source_refused_by_production_definition_before_stop 1 0
[ ! -s "$TMP/sequence" ]
for evidence in config/shared-runtime config/shared-replay-source agent-tokens; do
  mkdir -p "$TMP/data/$evidence"
  run_case "lost_source_with_${evidence##*/}_refuses_fallback_before_stop" 59 0
  [ "$(cat "$TMP/sequence")" = 'source check' ]
  rmdir "$TMP/data/$evidence"
done
mkdir "$TMP/data/config/shared-source"
printf 'staged\n' >"$TMP/source-state"
run_case uncommitted_source_refused_by_production_check_before_stop 59 0
[ "$(cat "$TMP/sequence")" = 'source check' ]
