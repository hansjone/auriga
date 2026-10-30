#!/bin/sh
# Render config files from secrets + stack.env. Idempotent; does not rotate secrets.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
if [ "${AURIGA_INSTALL_MODE:-host}" = container ] && [ "${NAS_INSTALLER_ACTIVE:-}" != 1 ]; then
  exec sh "$NAS_ROOT/scripts/installer.sh" render
fi
require_cmd sed awk tr cmp stat
require_root_or_capable_chown

SECRETS="$AURIGA_DATA_ROOT/secrets"
CONFIG="$AURIGA_DATA_ROOT/config"
[ -d "$SECRETS" ] || die "secrets directory missing; run init.sh first"
bootstrap_nas_materials
load_nas_access_env "$CONFIG/materials/access.env"

for _name in ca.crt edge.crt edge-iam.crt proxy-ca.crt proxy-edge.crt; do
  install_edge_material "$CONFIG/materials/pki/$_name" "$CONFIG/pki-edge/$_name" 644
done
for _name in edge.key edge-iam.key proxy-edge.key; do
  install_edge_material "$CONFIG/materials/pki/$_name" "$CONFIG/pki-edge/$_name" 600
done
install_edge_material "$CONFIG/materials/identity-signing-keys.json" "$CONFIG/pki-edge/identity-signing-keys.json" 600
install_edge_material "$CONFIG/materials/network/server-ca.pem" "$CONFIG/pki-edge/network-server-ca.pem" 644
install_edge_material "$CONFIG/materials/network/edge.pem" "$CONFIG/pki-edge/network-edge.pem" 644
install_edge_material "$CONFIG/materials/network/edge.key" "$CONFIG/pki-edge/network-edge.key" 600
if [ -f "$CONFIG/materials/browser-tls/browser.key" ]; then
  install_edge_material "$CONFIG/materials/browser-tls/browser.crt" "$CONFIG/pki-edge/browser.crt" 644
  install_edge_material "$CONFIG/materials/browser-tls/browser.key" "$CONFIG/pki-edge/browser.key" 600
fi


read_secret() {
  _f="$SECRETS/$1"
  [ -f "$_f" ] || die "missing secret: $1"
  _v=$(tr -d '\r' <"$_f" | awk '{printf "%s", $0}')
  [ -n "$_v" ] || die "empty secret file: $1"
  printf '%s' "$_v"
}

escape_sed() {
  printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

PG_BOOT=$(read_secret pg_bootstrap_password)
PG_IAM=$(read_secret pg_iam_password)
PG_ORG=$(read_secret pg_org_password)
PG_WS_OWNER=$(read_secret pg_ws_owner_password)
PG_WS_APP=$(read_secret pg_ws_app_password)
NATS_SYS=$(read_secret nats_sys_password)
NATS_PLATFORM=$(read_secret nats_platform_password)
NATS_ROUTES_RW=$(read_secret nats_routes_rw_password)
NATS_EDGE=$(read_secret nats_edge_password)
IAM_KEY=$(read_secret iam_encryption_key)
WS_KEY=$(read_secret workspace_encryption_key)
CA_SHA=$(tr -d '\r\n' <"$CONFIG/materials/ca-sha256.txt")

ISSUER=$AURIGA_NAS_ISSUER
ORIGIN=$AURIGA_NAS_PORTAL_URL
RP_ID=$AURIGA_NAS_PORTAL_HOST
SEED=${AURIGA_PLATFORM_SEED:-false}
case "$SEED" in
  true) PROFILES=combined,e2e ;;
  false) PROFILES=combined ;;
  *) die "AURIGA_PLATFORM_SEED must be true or false" ;;
esac
HOST_NC=${AURIGA_HOST_PUBLISH_NODECONTROL:-17443}
HOST_REG=${AURIGA_HOST_PUBLISH_REGISTRY:-15000}



umask 077
sed \
  -e "s|__PG_IAM_PASSWORD__|$(escape_sed "$PG_IAM")|g" \
  -e "s|__PG_ORG_PASSWORD__|$(escape_sed "$PG_ORG")|g" \
  -e "s|__PG_WS_OWNER_PASSWORD__|$(escape_sed "$PG_WS_OWNER")|g" \
  -e "s|__PG_WS_APP_PASSWORD__|$(escape_sed "$PG_WS_APP")|g" \
  "$NAS_ROOT/postgres/init.sql.tmpl" >"$CONFIG/postgres/init.sql"
chmod 0600 "$CONFIG/postgres/init.sql"
# The selected Debian postgres image runs psql as uid 999 (not root).
chown_required 999:999 "$CONFIG/postgres/init.sql"

sed \
  -e "s|__NATS_SYS_PASSWORD__|$(escape_sed "$NATS_SYS")|g" \
  -e "s|__NATS_PLATFORM_PASSWORD__|$(escape_sed "$NATS_PLATFORM")|g" \
  -e "s|__NATS_ROUTES_RW_PASSWORD__|$(escape_sed "$NATS_ROUTES_RW")|g" \
  -e "s|__NATS_EDGE_PASSWORD__|$(escape_sed "$NATS_EDGE")|g" \
  "$NAS_ROOT/nats/nats.conf.tmpl" >"$CONFIG/nats/nats.conf"
chmod 0600 "$CONFIG/nats/nats.conf"
chown_required 65532:65532 "$CONFIG/nats/nats.conf"

cat >"$CONFIG/platform.env" <<EOF
SPRING_PROFILES_ACTIVE=$PROFILES
AURIGA_IAM_JDBC_URL=jdbc:postgresql://postgres:5432/auriga
AURIGA_IAM_DB_USER=auriga_iam
AURIGA_IAM_DB_PASSWORD=$PG_IAM
AURIGA_ORG_JDBC_URL=jdbc:postgresql://postgres:5432/auriga
AURIGA_ORG_DB_USER=auriga_org
AURIGA_ORG_DB_PASSWORD=$PG_ORG
AURIGA_WORKSPACE_ENABLED=true
AURIGA_WORKSPACE_JDBC_URL=jdbc:postgresql://postgres:5432/auriga
AURIGA_WORKSPACE_DB_USER=auriga_workspace_app
AURIGA_WORKSPACE_DB_PASSWORD=$PG_WS_APP
AURIGA_WORKSPACE_MIGRATION_USER=auriga_workspace_owner
AURIGA_WORKSPACE_MIGRATION_PASSWORD=$PG_WS_OWNER
AURIGA_IAM_ENCRYPTION_KEY=$IAM_KEY
AURIGA_WORKSPACE_ENCRYPTION_KEY=$WS_KEY
AURIGA_IAM_ISSUER=$ISSUER
AURIGA_EVENTS_NATS_URL=nats://platform:$NATS_PLATFORM@nats:4222
AURIGA_WORKSPACE_ROUTES_NATS_URL=nats://routes_rw:$NATS_ROUTES_RW@nats:4222
AURIGA_ENROLLMENT_FILE=/var/auriga/emergency-enrollment.json
AURIGA_TEAM_RP_ID=$RP_ID
AURIGA_TEAM_ORIGIN=$ORIGIN
AURIGA_WORKSPACE_CA_CERTIFICATE_FILE=/etc/auriga/pki/ca.crt
AURIGA_WORKSPACE_CA_PRIVATE_KEY_FILE=/etc/auriga/pki/ca.key
AURIGA_WORKSPACE_TLS_CERTIFICATE_FILE=/etc/auriga/pki/workspace.crt
AURIGA_WORKSPACE_TLS_PRIVATE_KEY_FILE=/etc/auriga/pki/workspace.key
AURIGA_WORKSPACE_CONTROL_ENDPOINT=https://127.0.0.1:${HOST_NC}
AURIGA_WORKSPACE_NODE_SERVER_HOST=0.0.0.0
AURIGA_WORKSPACE_NODE_SERVER_PORT=7443
AURIGA_WORKSPACE_TEMPLATES_DIRECTORY=/etc/auriga/templates
AURIGA_WORKSPACE_ARTIFACT_KEYS_DIRECTORY=/etc/auriga/runtime-keys
AURIGA_PLATFORM_SEED=$SEED
EOF
chmod 0600 "$CONFIG/platform.env"
chown_required 10001:10001 "$CONFIG/platform.env"

# Runtime-only edge environment; credentials stay out of the source receipt/public JSON.
printf 'AURIGA_EDGE_NATS_URL=nats://edge:%s@nats:4222\n' "$NATS_EDGE" >"$CONFIG/edge.env"
chmod 0600 "$CONFIG/edge.env"
chown_required 65532:65532 "$CONFIG/edge.env"

cat >"$CONFIG/agent.env" <<EOF
AURIGA_AGENT_SERVER=https://127.0.0.1:${HOST_NC}
AURIGA_AGENT_CA_SHA256=$CA_SHA
AURIGA_AGENT_ALLOW_INSECURE=false
AURIGA_DEPLOYMENT_ACCESS_FILE=/etc/auriga/access.json
AURIGA_AGENT_RUNTIME_PROOFS_DIR=/etc/auriga/runtime-proofs
EOF
chmod 0600 "$CONFIG/agent.env"

printf '%s' "$PG_BOOT" >"$SECRETS/pg_bootstrap_password"
chmod 0600 "$SECRETS/pg_bootstrap_password"

info "render complete (configs under $CONFIG; secrets not printed)"
info "note: platform.env embeds DB/NATS secrets as env vars — visible to anyone with docker inspect (M5)"
