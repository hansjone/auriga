#!/bin/sh
# Public material command arguments and failures, without a Docker daemon.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir "$temp/scripts"
cp "$root/scripts/materials-upgrade.sh" "$temp/scripts/"
touch "$temp/initialized"
export TEST_MATERIALS_ROOT="$temp"
cat >"$temp/scripts/lib.sh" <<'EOF'
NAS_ROOT=$TEST_MATERIALS_ROOT
load_stack_env() { :; }
marker_path() { printf '%s/initialized' "$NAS_ROOT"; }
die() { echo "$*" >&2; exit 1; }
materials_upgrade() {
  printf '%s\n' "$*" >>"$NAS_ROOT/actions"
  return "${TEST_MATERIALS_EXIT:-0}"
}
EOF
receipt=$(printf '%064d' 1)
for action in stage commit rollback; do
  sh "$temp/scripts/materials-upgrade.sh" "$action" "$receipt"
done
sh "$temp/scripts/materials-upgrade.sh" status
printf 'stage %s\ncommit %s\nrollback %s\nstatus\n' "$receipt" "$receipt" "$receipt" >"$temp/expected"
cmp "$temp/actions" "$temp/expected"
echo 'PASS materials_public_actions_bound'
for bad in '' '../x' AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA; do
  if sh "$temp/scripts/materials-upgrade.sh" stage "$bad" >/dev/null 2>&1; then exit 1; fi
done
if sh "$temp/scripts/materials-upgrade.sh" status extra >/dev/null 2>&1; then exit 1; fi
if TEST_MATERIALS_EXIT=73 sh "$temp/scripts/materials-upgrade.sh" stage "$receipt"; then exit 1; else [ "$?" = 73 ]; fi
echo 'PASS materials_public_rejects_input_and_preserves_failure'
