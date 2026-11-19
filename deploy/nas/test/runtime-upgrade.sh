#!/bin/sh
# Public command transaction: success commits; service failure rolls back.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir -p "$temp/scripts" "$temp/data"
cp "$root/scripts/runtime-upgrade.sh" "$temp/scripts/"
touch "$temp/data/marker"
export TEST_RUNTIME_ROOT="$temp"
cat >"$temp/scripts/lib.sh" <<'EOF'
set -eu
NAS_ROOT="$TEST_RUNTIME_ROOT"
AURIGA_DATA_ROOT="$NAS_ROOT/data"
die() { echo "$*" >&2; exit 1; }
info() { echo "$*"; }
load_stack_env() { :; }
shared_present() { [ -f "$NAS_ROOT/shared-enabled" ]; }
shared_action() {
  [ "$1" = project ] || return 79
  printf 'project\n' >>"$NAS_ROOT/shared-actions"
  [ ! -f "$NAS_ROOT/shared-project-fails" ] || return 74
}
require_cmd() { :; }
marker_path() { echo "$AURIGA_DATA_ROOT/marker"; }
bootstrap_binary() { echo "$NAS_ROOT/bootstrap"; }
run_compose() {
  printf '%s\n' "$*" >>"$NAS_ROOT/compose.log"
  if [ -f "$NAS_ROOT/fail-once" ]; then rm "$NAS_ROOT/fail-once"; return 73; fi
}
EOF
cat >"$temp/bootstrap" <<'EOF'
#!/bin/sh
set -eu
[ "$1" = runtime-upgrade ] && [ "$2" = -action ]
echo "$3" >>"$TEST_RUNTIME_ROOT/actions"
EOF
printf '#!/bin/sh\nexit 0\n' >"$temp/scripts/preflight.sh"
chmod 0755 "$temp/bootstrap"
sh "$temp/scripts/runtime-upgrade.sh" /approved/bundle registry.example.com/dsh@sha256:fixture
printf 'stage\ncommit\n' >"$temp/expected"
cmp "$temp/actions" "$temp/expected"
echo 'PASS runtime_upgrade_commits_after_consumer_readiness'
: >"$temp/actions"
: >"$temp/compose.log"
touch "$temp/fail-once"
if sh "$temp/scripts/runtime-upgrade.sh" /approved/bundle registry.example.com/dsh@sha256:fixture; then
  echo 'activation failure accepted' >&2; exit 1
else [ "$?" = 73 ]; fi
printf 'stage\nrollback\n' >"$temp/expected"
cmp "$temp/actions" "$temp/expected"
[ "$(wc -l <"$temp/compose.log")" = 2 ]
grep -q -- '--wait --wait-timeout 120 --no-build --pull never platform agent' "$temp/compose.log"
echo 'PASS runtime_upgrade_rolls_back_and_recovers_previous_consumers'

: >"$temp/actions"
: >"$temp/compose.log"
printf '#!/bin/sh\nexit 65\n' >"$temp/scripts/preflight.sh"
if sh "$temp/scripts/runtime-upgrade.sh" /approved/bundle registry.example.com/dsh@sha256:fixture; then
  echo 'failed historical precheck accepted' >&2; exit 1
else [ "$?" = 65 ]; fi
[ ! -s "$temp/actions" ] && [ ! -s "$temp/compose.log" ]
echo 'PASS runtime_upgrade_precheck_precedes_staging_and_service_changes'

mkdir -p "$temp/data/config/collab-operations"
printf '#!/bin/sh\nexit 0\n' >"$temp/scripts/preflight.sh"
cat >"$temp/scripts/rotate-collab-keys.sh" <<'EOF'
#!/bin/sh
echo "$1" >>"$TEST_RUNTIME_ROOT/history-actions"
if [ "$1" = "${TEST_HISTORY_REJECT:-}" ]; then exit 66; fi
EOF
: >"$temp/actions"; : >"$temp/compose.log"
: >"$temp/history-actions"
TEST_HISTORY_REJECT=history-check-pg sh "$temp/scripts/runtime-upgrade.sh" /approved/bundle registry.example.com/dsh@sha256:fixture
[ ! -s "$temp/history-actions" ]
printf 'stage\ncommit\n' >"$temp/expected"
cmp "$temp/actions" "$temp/expected"
echo 'PASS instance_runtime_upgrade_is_independent_of_message_history_and_pg_checks'

: >"$temp/actions"; : >"$temp/compose.log"
touch "$temp/shared-enabled" "$temp/shared-project-fails"
if sh "$temp/scripts/runtime-upgrade.sh" /approved/bundle registry.example.com/dsh@sha256:fixture >"$temp/committed-error" 2>&1; then exit 1
else [ "$?" = 74 ]; fi
printf 'stage\ncommit\n' >"$temp/expected"
cmp "$temp/actions" "$temp/expected"
[ "$(wc -l <"$temp/compose.log")" = 2 ]
tail -n 1 "$temp/compose.log" | grep -q ' stop agent$'
grep -q 'retry this public upgrade command with the same bundle/image' "$temp/committed-error"
printf 'PASS committed_shared_projection_failure_stops_agent_without_lowering_pointer_or_rollback\n'
rm "$temp/shared-project-fails"
: >"$temp/actions"; : >"$temp/compose.log"
sh "$temp/scripts/runtime-upgrade.sh" /approved/bundle registry.example.com/dsh@sha256:fixture
cmp "$temp/actions" "$temp/expected"
[ "$(wc -l <"$temp/compose.log")" = 2 ]
tail -n 1 "$temp/compose.log" | grep -q ' up -d --wait --wait-timeout 120 --no-build --pull never platform agent$'
printf 'PASS committed_shared_upgrade_retry_finishes_current_projection\n'
