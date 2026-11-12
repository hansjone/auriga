#!/bin/sh
# Exercise the public database upgrade boundary without a daemon.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir "$temp/scripts"
cp "$root/scripts/upgrade-admin.sh" "$temp/scripts/"
export TEST_UPGRADE_ROOT="$temp"
touch "$temp/initialized"
cat >"$temp/scripts/lib.sh" <<'EOF'
NAS_ROOT=$TEST_UPGRADE_ROOT
AURIGA_DATA_ROOT=$TEST_UPGRADE_ROOT
load_stack_env() { :; }
marker_path() { printf '%s/initialized' "$NAS_ROOT"; }
die() { echo "$*" >&2; exit 1; }
run_compose() {
  printf '%s\n' "$*" >>"$NAS_ROOT/actions"
  case "$*" in
    *audit-db-prepare) echo audit >>"$NAS_ROOT/steps"; return "${TEST_AUDIT_EXIT:-0}" ;;
    *) echo roles >>"$NAS_ROOT/steps" ;;
  esac
  return "${TEST_UPGRADE_EXIT:-0}"
}
EOF
cat >"$temp/scripts/upgrade-bifrost.sh" <<'EOF'
echo bifrost >>"$TEST_UPGRADE_ROOT/actions"
echo bifrost >>"$TEST_UPGRADE_ROOT/steps"
exit "${TEST_BIFROST_EXIT:-0}"
EOF
for attempt in 1 2; do
  sh "$temp/scripts/upgrade-admin.sh"
done
printf 'roles\naudit\nbifrost\nroles\naudit\nbifrost\n' >"$temp/expected-steps"
cmp "$temp/steps" "$temp/expected-steps"
[ "$(grep -c 'run --rm --no-deps --user 0:999' "$temp/actions")" = 2 ]
[ "$(grep -c 'run --rm --no-deps audit-db-prepare' "$temp/actions")" = 2 ]
[ "$(grep -c '^bifrost$' "$temp/actions")" = 2 ]
grep -q 'psql -X -q -h postgres -U auriga_bootstrap -d auriga -v ON_ERROR_STOP=1' "$temp/actions"
echo 'PASS admin_upgrade_repeatable_helper_sequence'
cp "$temp/actions" "$temp/before"
cat >"$temp/scripts/migrate-relay.sh" <<'EOF'
exit "${TEST_RELAY_EXIT:-0}"
EOF
sh "$temp/scripts/upgrade-admin.sh" migrate-relay
if TEST_RELAY_EXIT=74 sh "$temp/scripts/upgrade-admin.sh" migrate-relay; then exit 1; else [ "$?" = 74 ]; fi
cmp "$temp/actions" "$temp/before"
echo 'PASS admin_upgrade_relay_delegate_and_failure_propagation'
if sh "$temp/scripts/upgrade-admin.sh" unexpected 2>/dev/null; then exit 1; fi
cmp "$temp/actions" "$temp/before"
rm "$temp/initialized"
if sh "$temp/scripts/upgrade-admin.sh" 2>/dev/null; then exit 1; fi
cmp "$temp/actions" "$temp/before"
touch "$temp/initialized"
echo 'PASS admin_upgrade_rejects_arguments_and_uninitialized_root'
: >"$temp/actions"
if TEST_UPGRADE_EXIT=73 sh "$temp/scripts/upgrade-admin.sh"; then exit 1; else [ "$?" = 73 ]; fi
[ "$(grep -c -- '--env-file' "$temp/actions")" = 1 ]
if grep -q 'audit-db-prepare\|^bifrost$' "$temp/actions"; then exit 1; fi
: >"$temp/actions"
if TEST_AUDIT_EXIT=75 sh "$temp/scripts/upgrade-admin.sh"; then exit 1; else [ "$?" = 75 ]; fi
if grep -q '^bifrost$' "$temp/actions"; then exit 1; fi
if TEST_BIFROST_EXIT=74 sh "$temp/scripts/upgrade-admin.sh"; then exit 1; else [ "$?" = 74 ]; fi
echo 'PASS admin_upgrade_preserves_failure_and_stops_next_step'
