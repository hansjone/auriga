#!/bin/sh
# Exercise the real init/render and material-copy helpers with disposable sources.
set -eu
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
[ "$(id -u)" = 0 ] || { echo 'SKIP device_presence_requires_chown'; exit 0; }
temp=$(mktemp -d)
trap 'rm -rf -- "$temp"' EXIT HUP INT TERM
export PRESENCE_SCRIPT_ROOT="$root" PRESENCE_FIXTURE_ROOT="$temp"
mkdir -p "$temp/scripts" "$temp/data/config/materials/pki" "$temp/data/config/materials/network" "$temp/data/config/pki-edge" "$temp/data/config/postgres" "$temp/data/config/nats" "$temp/data/secrets" "$temp/postgres" "$temp/nats"
cp "$root/scripts/render.sh" "$root/scripts/init.sh" "$temp/scripts/"
touch "$temp/initialized"
cat >"$temp/scripts/lib.sh" <<'EOF'
. "$PRESENCE_SCRIPT_ROOT/scripts/lib.sh"
NAS_ROOT=$PRESENCE_FIXTURE_ROOT
AURIGA_DATA_ROOT=$NAS_ROOT/data
load_stack_env() { :; }
validate_nas_access() { :; }
require_root_or_capable_chown() { :; }
bootstrap_nas_materials() { :; }
install_bifrost_materials() { :; }
marker_path() { printf '%s/initialized' "$NAS_ROOT"; }
load_nas_access_env() {
  AURIGA_NAS_ISSUER=https://portal.example.com
  AURIGA_NAS_PORTAL_URL=$AURIGA_NAS_ISSUER
  AURIGA_NAS_PORTAL_HOST=portal.example.com
}
EOF
for script in render-collab render-managed; do
  printf '#!/bin/sh\nexit 0\n' >"$temp/scripts/$script.sh"
done
for leaf in ca.crt edge.crt edge-iam.crt proxy-ca.crt proxy-edge.crt edge.key edge-iam.key proxy-edge.key; do
  printf fixture >"$temp/data/config/materials/pki/$leaf"
done
for leaf in server-ca.pem edge.pem edge.key; do
  printf fixture >"$temp/data/config/materials/network/$leaf"
done
printf '{}' >"$temp/data/config/materials/identity-signing-keys.json"
printf '{}' >"$temp/data/config/materials/edge.config.json"
printf fixture >"$temp/data/config/materials/ca-sha256.txt"
for leaf in pg_bootstrap_password pg_iam_password pg_org_password pg_ws_owner_password pg_ws_app_password nats_sys_password nats_platform_password nats_routes_rw_password nats_edge_password iam_encryption_key workspace_encryption_key; do
  printf fixture >"$temp/data/secrets/$leaf"
done
: >"$temp/postgres/init.sql.tmpl"
cp "$root/postgres/audit.sql.tmpl" "$temp/postgres/audit.sql.tmpl"
: >"$temp/nats/nats.conf.tmpl"
find "$temp/data/config/materials" -type f -exec sha256sum {} \; | sort >"$temp/source-before"
sh "$temp/scripts/render.sh" >"$temp/render.log" 2>&1 || { cat "$temp/render.log"; exit 1; }
for name in pg_audit_owner_password pg_audit_app_password nats_audit_password; do
  [ "$(stat -c '%a:%h' "$temp/data/secrets/$name")" = '600:1' ]
  [ "$(wc -c <"$temp/data/secrets/$name")" -eq 32 ]
  cp "$temp/data/secrets/$name" "$temp/$name.saved"
done
sh "$temp/scripts/init.sh" >>"$temp/render.log" 2>&1 || { cat "$temp/render.log"; exit 1; }
grep -q 'device_presence=disabled' "$temp/render.log"
[ -s "$temp/data/config/edge.env" ]
[ ! -e "$temp/data/config/pki-edge/network-presence.pem" ]
[ -z "$(find "$temp/data/config/pki-edge" -name '.install.*' -print)" ]
find "$temp/data/config/materials" -type f -exec sha256sum {} \; | sort >"$temp/source-after"
cmp "$temp/source-before" "$temp/source-after"
echo 'PASS old_network_materials_render_and_init_without_rotation'

grep -q 'CREATE SCHEMA IF NOT EXISTS audit AUTHORIZATION auriga_audit_owner' "$temp/data/config/postgres/audit.sql"
if grep -q '__PG_AUDIT_' "$temp/data/config/postgres/audit.sql"; then exit 1; fi
[ "$(stat -c '%u:%g:%a:%h' "$temp/data/config/postgres/audit.sql")" = '999:999:600:1' ]
[ "$(stat -c '%u:%g:%a:%h' "$temp/data/config/audit-db.env")" = '999:999:600:1' ]
[ "$(stat -c '%u:%g:%a:%h' "$temp/data/config/bifrost-audit.env")" = '10003:10003:600:1' ]
grep -q '^AURIGA_AUDIT_ENABLED=true$' "$temp/data/config/platform.env"
grep -Fxq "AURIGA_AUDIT_DB_PASSWORD=$(cat "$temp/data/secrets/pg_audit_app_password")" "$temp/data/config/platform.env"
grep -Fxq "AURIGA_AUDIT_MIGRATION_PASSWORD=$(cat "$temp/data/secrets/pg_audit_owner_password")" "$temp/data/config/platform.env"
grep -Fxq "AURIGA_AUDIT_NATS_URL=nats://bifrost_audit:$(cat "$temp/data/secrets/nats_audit_password")@nats:4222" "$temp/data/config/bifrost-audit.env"
echo 'PASS audit_materials_generated_with_presence_disabled'

printf fixture >"$temp/data/config/materials/network/edge-presence.pem"
printf fixture >"$temp/data/config/materials/network/edge-presence.key"
# A partial optional identity must not leave any copied fragment.
sh "$temp/scripts/render.sh" >"$temp/partial.log" 2>&1 || { cat "$temp/partial.log"; exit 1; }
[ ! -e "$temp/data/config/pki-edge/network-presence.pem" ]
[ -z "$(find "$temp/data/config/pki-edge" -name '.install.*' -print)" ]
echo 'PASS partial_presence_materials_degrade_without_temporary_files'

printf fixture >"$temp/data/config/materials/network/edge-presence.password"
sh "$temp/scripts/render.sh" >"$temp/enabled.log" 2>&1 || { cat "$temp/enabled.log"; exit 1; }
[ "$(stat -c '%u:%g:%a:%h' "$temp/data/config/pki-edge/network-presence.pem")" = '65532:65532:644:1' ]
for leaf in key password; do
  [ "$(stat -c '%u:%g:%a:%h' "$temp/data/config/pki-edge/network-presence.$leaf")" = '65532:65532:600:1' ]
done
stat -c '%i:%u:%g:%a:%h' "$temp/data/config/pki-edge"/network-presence.* >"$temp/leaves-before"
sh "$temp/scripts/render.sh" >>"$temp/enabled.log" 2>&1 || { cat "$temp/enabled.log"; exit 1; }
stat -c '%i:%u:%g:%a:%h' "$temp/data/config/pki-edge"/network-presence.* >"$temp/leaves-after"
cmp "$temp/leaves-before" "$temp/leaves-after"
echo 'PASS complete_presence_materials_keep_permissions_and_inodes'

for name in pg_audit_owner_password pg_audit_app_password nats_audit_password; do
  cmp "$temp/data/secrets/$name" "$temp/$name.saved"
done
echo 'PASS audit_credentials_preserved_across_presence_upgrade'

mv "$temp/data/config/materials/network/edge-presence.key" "$temp/key"
ln -s "$temp/key" "$temp/data/config/materials/network/edge-presence.key"
if sh "$temp/scripts/render.sh" >"$temp/invalid.log" 2>&1; then exit 1; fi
grep -q 'invalid device presence source' "$temp/invalid.log"
echo 'PASS invalid_presence_materials_rejected'
