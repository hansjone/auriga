#!/bin/sh
# Execute the real core/message renderers around a missing disposable projection.
# Fixture helpers replace privileged metadata/receipts, never Docker or services.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
mkdir -p "$temp/scripts" "$temp/data/config/materials" "$temp/data/config/materials-upgrades" "$temp/data/config/collab-source" "$temp/data/config/postgres" "$temp/data/config/nats" "$temp/data/secrets" "$temp/postgres" "$temp/nats"
cp "$root/scripts/render.sh" "$root/scripts/render-collab.sh" "$root/scripts/start.sh" "$root/scripts/start-collab-network.sh" "$temp/scripts/"
touch "$temp/data/marker" "$temp/data/stack.env"
export BIFROST_RENDER_FIXTURE="$temp"
printf '{"source":"sealed-core"}\n' >"$temp/data/config/materials/edge.config.json"
printf '{"source":"sealed-message"}\n' >"$temp/data/config/collab-source/edge.config.json"
printf 'sealed-identity\n' >"$temp/data/config/collab-source/activation.receipt"
printf 'sealed-upgrade\n' >"$temp/data/config/materials-upgrades/upgrade.receipt"
printf 'fixture-ca\n' >"$temp/data/config/materials/ca-sha256.txt"
for leaf in pg_bootstrap_password pg_iam_password pg_org_password pg_ws_owner_password pg_ws_app_password nats_sys_password nats_platform_password nats_routes_rw_password nats_edge_password iam_encryption_key workspace_encryption_key; do printf fixture >"$temp/data/secrets/$leaf"; done
: >"$temp/postgres/init.sql.tmpl"; cp "$root/postgres/audit.sql.tmpl" "$temp/postgres/audit.sql.tmpl"; : >"$temp/nats/nats.conf.tmpl"
cat >"$temp/scripts/lib.sh" <<'EOF'
set -eu
NAS_ROOT=$BIFROST_RENDER_FIXTURE
AURIGA_DATA_ROOT=$NAS_ROOT/data
AURIGA_NAS_ISSUER=https://portal.example.com
AURIGA_NAS_PORTAL_URL=$AURIGA_NAS_ISSUER
AURIGA_NAS_PORTAL_HOST=portal.example.com
AURIGA_NAS_PROJECT=fixture
load_stack_env() { :; }
require_cmd() { :; }
rand_alnum() { printf fixture-password-0123456789012345; }
marker_path() { echo "$AURIGA_DATA_ROOT/marker"; }
prepare_socket_parent() { :; }
project_is_running() { return 0; }
run_compose() {
 printf '%s %s\n' "$*" "$(cat "$AURIGA_DATA_ROOT/config/bifrost/edge.config.json")" >>"$NAS_ROOT/compose.log"
 case "$*" in *'ps -q edge') printf 'edge-fixture\n' ;; esac
}
require_root_or_capable_chown() { :; }
bootstrap_nas_materials() { [ ! -e "$NAS_ROOT/bad-core" ]; }
load_nas_access_env() { :; }
install_edge_material() { :; }
install_network_presence_materials() { :; }
chown_required() { printf '%s %s\n' "$1" "$2" >>"$NAS_ROOT/chown.log"; }
info() { :; }
die() { echo "$*" >&2; exit 1; }
stat() {
 case "$3" in
  */panel) printf '10001:10001:755\n' ;;
  */collab-tokens) printf '10001:65534:750\n' ;;
  *) printf '0:0:755\n' ;;
 esac
}
chown() { :; }
install_bifrost_materials() { mkdir -p "$AURIGA_DATA_ROOT/config/bifrost"; }
materials_upgrade() {
 case "$1" in
  project) source="$AURIGA_DATA_ROOT/config/collab-runtime/edge/edge.config.json" ;;
  project-core) source="$AURIGA_DATA_ROOT/config/materials/edge.config.json" ;;
  *) return 81 ;;
 esac
 cat "$source" >"$AURIGA_DATA_ROOT/config/bifrost/edge.config.json"
}
bootstrap_binary() { echo "$NAS_ROOT/bootstrap"; }
rand_alnum() { printf 0123456789abcdef0123456789abcdef; }
EOF
cat >"$temp/bootstrap" <<'EOF'
#!/bin/sh
case "$1" in
 collab) [ ! -e "$BIFROST_RENDER_FIXTURE/bad-message" ] ;;
 collab-rotation) : ;;
 network-budget) : ;;
 *) exit 82 ;;
esac
EOF
chmod +x "$temp/bootstrap"
for script in render-managed preflight network-preflight emergency rotate-collab-keys; do
 printf '#!/bin/sh\nexit 0\n' >"$temp/scripts/$script.sh"
done
# The sealed-source projection helper supplies disposable message bytes.
cat >"$temp/scripts/collab-materials.sh" <<'EOF'
#!/bin/sh
mkdir -p "$2/edge"
cp "$1/edge.config.json" "$2/edge/edge.config.json"
EOF
sh "$temp/scripts/render.sh" --core-only
# Upgrade rendering creates isolated credentials and preserves them on rerender.
for name in pg_audit_owner_password pg_audit_app_password nats_audit_password; do
 [ "$(/usr/bin/stat -c '%a' "$temp/data/secrets/$name")" = 600 ]
 cp "$temp/data/secrets/$name" "$temp/$name.original"
done
grep -q '^AURIGA_AUDIT_NATS_URL=nats://bifrost_audit:' "$temp/data/config/bifrost-audit.env"
grep -q '^AURIGA_AUDIT_ENABLED=true$' "$temp/data/config/platform.env"
grep -q '^AURIGA_AUDIT_DB_USER=auriga_audit_app$' "$temp/data/config/platform.env"
grep -q '^PGPASSWORD=fixture$' "$temp/data/config/audit-db.env"
[ "$(/usr/bin/stat -c '%a' "$temp/data/config/audit-db.env")" = 600 ]
grep -q "^999:999 $temp/data/config/postgres/audit.sql$" "$temp/chown.log"
grep -q "^999:999 $temp/data/config/audit-db.env$" "$temp/chown.log"
grep -q '__PG_AUDIT_' "$temp/data/config/postgres/audit.sql" && exit 1
sh "$temp/scripts/render.sh" --core-only
for name in pg_audit_owner_password pg_audit_app_password nats_audit_password; do
 cmp "$temp/data/secrets/$name" "$temp/$name.original"
done
printf 'PASS audit_upgrade_credentials_are_created_and_preserved\n'
cmp "$temp/data/config/materials/edge.config.json" "$temp/data/config/bifrost/edge.config.json"
[ ! -e "$temp/data/config/collab-runtime/edge/edge.config.json" ]
printf 'PASS bifrost_core_render_without_message_projection\n'
cp "$temp/data/secrets/ops_notification_key" "$temp/ops-key.saved"
sh "$temp/scripts/render-collab.sh"
cmp "$temp/data/config/collab-source/edge.config.json" "$temp/data/config/bifrost/edge.config.json"
printf 'PASS bifrost_message_render_rebuilds_and_rebinds_projection\n'
rm "$temp/data/config/collab-runtime/edge/edge.config.json"
touch "$temp/bad-message"
sh "$temp/scripts/render.sh" --core-only
if sh "$temp/scripts/render-collab.sh"; then exit 1; fi
cmp "$temp/data/config/materials/edge.config.json" "$temp/data/config/bifrost/edge.config.json"
printf 'PASS rejected_message_preserves_core_bifrost_projection\n'
# Normal start must reach message reconstruction after the real core renderer.
rm "$temp/bad-message"
sh "$temp/scripts/start.sh" >"$temp/start.log" 2>&1
cmp "$temp/data/config/collab-source/edge.config.json" "$temp/data/config/bifrost/edge.config.json"
grep -q 'up -d --no-build --pull never.*sealed-message' "$temp/compose.log"
printf 'PASS normal_start_repairs_bifrost_message_projection\n'
# A message failure remains non-fatal to normal start, then standalone recovery
# runs the same real message renderer and replaces the core Bifrost projection.
rm "$temp/data/config/collab-runtime/edge/edge.config.json"
touch "$temp/bad-message"
: >"$temp/compose.log"
sh "$temp/scripts/start.sh" >"$temp/degraded.log" 2>&1
grep -q 'message_network=degraded' "$temp/degraded.log"
grep -q 'up -d --no-build --pull never.*sealed-core' "$temp/compose.log"
cmp "$temp/data/config/materials/edge.config.json" "$temp/data/config/bifrost/edge.config.json"
rm "$temp/bad-message"
if sh "$temp/scripts/start-collab-network.sh" >"$temp/recovered.log" 2>&1; then exit 1; else [ "$?" = 2 ]; fi
grep -q 'announcement_verification=pending' "$temp/recovered.log"
grep -q 'collab network edge.*sealed-message' "$temp/compose.log"
cmp "$temp/data/config/collab-source/edge.config.json" "$temp/data/config/bifrost/edge.config.json"
printf 'PASS independent_message_recovery_rebinds_bifrost_after_degraded_start\n'
touch "$temp/bad-core"
if sh "$temp/scripts/render.sh" --core-only; then exit 1; fi
printf 'PASS invalid_core_still_rejected\n'
[ "$(cat "$temp/data/config/collab-source/activation.receipt")" = sealed-identity ]
[ "$(cat "$temp/data/config/materials-upgrades/upgrade.receipt")" = sealed-upgrade ]

rm "$temp/bad-core"
cmp "$temp/ops-key.saved" "$temp/data/secrets/ops_notification_key"
grep -q '^AURIGA_OPS_ENABLED=true$' "$temp/data/config/platform.env"
# The database upgrade must precede starting the platform on an existing volume.
grep -q 'exec -T postgres psql -U auriga_bootstrap.*01-roles.sql' "$temp/compose.log"
rm "$temp/data/secrets/ops_notification_key"
if sh "$temp/scripts/render.sh" --core-only >"$temp/ops-missing.log" 2>&1; then exit 1; fi
grep -q 'ops key missing; restore' "$temp/ops-missing.log"
printf 'PASS ops_key_reused_missing_key_refused_and_roles_upgraded\n'
