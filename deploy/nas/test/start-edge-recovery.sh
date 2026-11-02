#!/bin/sh
# The public start path must recover instance bridges after Compose replaces edge.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir -p "$temp/scripts" "$temp/data"
cp "$root/scripts/start.sh" "$root/scripts/start-collab-network.sh" "$temp/scripts/"
touch "$temp/data/marker" "$temp/data/stack.env"
export TEST_START_ROOT="$temp"
cat >"$temp/scripts/lib.sh" <<'EOF'
set -eu
NAS_ROOT="$TEST_START_ROOT"
AURIGA_DATA_ROOT="$NAS_ROOT/data"
AURIGA_NAS_PROJECT=fixture
die() { echo "$*" >&2; exit 1; }
info() { :; }
load_stack_env() { :; }
require_cmd() { :; }
marker_path() { echo "$AURIGA_DATA_ROOT/marker"; }
prepare_socket_parent() { :; }
project_is_running() { return 0; }
run_compose() {
  printf '%s\n' "$*" >>"$NAS_ROOT/compose.log"
  [ ! -f "$NAS_ROOT/fail" ] || return 73
}
EOF
for script in preflight network-preflight render render-collab emergency rotate-collab-keys; do
  printf '#!/bin/sh\nexit 0\n' >"$temp/scripts/$script.sh"
done
sh "$temp/scripts/start.sh"
grep -q ' up -d --no-build --pull never$' "$temp/compose.log"
tail -n 1 "$temp/compose.log" | grep -q ' restart --no-deps agent$'
[ "$(wc -l <"$temp/compose.log")" -eq 2 ]
echo 'PASS public_start_recovers_agent_after_edge_is_started'
: >"$temp/compose.log"
touch "$temp/fail"
if sh "$temp/scripts/start.sh"; then exit 1; else [ "$?" -eq 73 ]; fi
[ "$(wc -l <"$temp/compose.log")" -eq 1 ]
echo 'PASS failed_start_does_not_recover_or_report_success'

rm "$temp/fail"
: >"$temp/compose.log"
mkdir -p "$temp/data/config/collab-source"
printf '#!/bin/sh\nexit 9\n' >"$temp/scripts/rotate-collab-keys.sh"
if ! sh "$temp/scripts/start.sh" >"$temp/degraded.log" 2>&1; then cat "$temp/degraded.log"; exit 1; fi
grep -q 'message_network=degraded' "$temp/degraded.log"
grep -q ' up -d --no-build --pull never$' "$temp/compose.log"
grep -q ' stop collab network network-broker$' "$temp/compose.log"
printf 'PASS expired_network_does_not_block_core_start\n'
