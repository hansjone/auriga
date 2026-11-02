#!/bin/sh
# Shell orchestration fixture, not a real Docker/Compose/NAS acceptance.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir -p "$temp/scripts" "$temp/data/config/collab-source"
cp "${R20_START_SCRIPT:-$root/scripts/start-collab-network.sh}" "$temp/scripts/start-collab-network.sh"
export RECOVERY_FIXTURE="$temp"
cat >"$temp/scripts/lib.sh" <<'EOF'
NAS_ROOT="$RECOVERY_FIXTURE"
AURIGA_DATA_ROOT="$NAS_ROOT/data"
load_stack_env() { :; }
require_cmd() { :; }
run_compose() {
 printf '%s\n' "$*" >>"$NAS_ROOT/calls"
 case "$*" in
  *'ps -q edge') if [ -e "$NAS_ROOT/rebound" ]; then echo edge-new; else echo edge-core; fi ;;
  *'up '*'collab network edge') [ ! -e "$NAS_ROOT/deny" ] || return 41; touch "$NAS_ROOT/rebound" ;;
  *'up '*) echo 'missing edge consumer in recovery' >&2; return 42 ;;
 esac
}
EOF
printf '#!/bin/sh\n[ ! -e "$RECOVERY_FIXTURE/foreign" ]\n' >"$temp/scripts/rotate-collab-keys.sh"
printf '#!/bin/sh\n[ ! -e "$RECOVERY_FIXTURE/invalid" ]\n' >"$temp/scripts/render-collab.sh"
printf '#!/bin/sh\n[ ! -e "$RECOVERY_FIXTURE/announcement-denied" ]\n' >"$temp/approved-consumer"
chmod 0700 "$temp/approved-consumer"
export AURIGA_NETWORK_ANNOUNCEMENT_CHECK="$temp/approved-consumer"
sh "$temp/scripts/start-collab-network.sh" >"$temp/result" 2>&1 || { cat "$temp/result"; exit 1; }
grep -q '^message_network=READY$' "$temp/result"
grep -q 'collab network edge$' "$temp/calls"
grep -q 'restart --no-deps agent$' "$temp/calls"
printf 'PASS fixture_degraded_to_rebound_edge_and_approved_consumer\n'
: >"$temp/calls"
sh "$temp/scripts/start-collab-network.sh" >"$temp/result" 2>&1
if grep -q 'restart' "$temp/calls"; then echo 'unchanged edge caused bridge restart' >&2; exit 1; fi
printf 'PASS fixture_unchanged_edge_not_restarted\n'
for reason in foreign invalid deny announcement-denied; do
 : >"$temp/calls"; touch "$temp/$reason"
 if sh "$temp/scripts/start-collab-network.sh" >"$temp/result" 2>&1; then cat "$temp/result"; exit 1; fi
 if grep -q '^message_network=READY$' "$temp/result"; then exit 1; fi
 if [ "$reason" = foreign ] || [ "$reason" = invalid ]; then
  if grep -q 'up ' "$temp/calls"; then echo 'rejected authority reached Compose up' >&2; exit 1; fi
 fi
 rm "$temp/$reason"
 printf 'PASS fixture_refuses_%s\n' "$reason"
done
unset AURIGA_NETWORK_ANNOUNCEMENT_CHECK
if sh "$temp/scripts/start-collab-network.sh" >"$temp/result" 2>&1; then exit 1; else [ "$?" = 2 ]; fi
grep -q 'announcement_verification=pending' "$temp/result"
printf 'PASS fixture_no_authorized_probe_is_pending_not_ready\n'
