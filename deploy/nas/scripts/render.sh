#!/bin/sh
# Render config files from secrets + stack.env. Idempotent; does not rotate secrets.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd sed awk tr
require_root_or_capable_chown

SECRETS="$AURIGA_DATA_ROOT/secrets"
CONFIG="$AURIGA_DATA_ROOT/config"
[ -d "$SECRETS" ] || die "secrets directory missing; run init.sh first"
[ -f "$CONFIG/ca-sha256.txt" ] || die "ca-sha256.txt missing; run init.sh first"

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
CA_SHA=$(tr -d '\r\n' <"$CONFIG/ca-sha256.txt")

PORTAL=${AURIGA_PORTAL_HOST:-portal.example.com}
ADMIN=${AURIGA_ADMIN_HOST:-admin.example.com}
INSTANCE_PARENT=${AURIGA_INSTANCE_PARENT_DOMAIN:-i.example.com}
ISSUER=${AURIGA_ISSUER:-https://portal.example.com}
ORIGIN=${AURIGA_TEAM_ORIGIN:-$ISSUER}
RP_ID=${AURIGA_TEAM_RP_ID:-$PORTAL}
TRUSTED=${AURIGA_TRUSTED_PROXIES:-127.0.0.1}
SECURE=${AURIGA_SECURE_COOKIES:-true}
SEED=${AURIGA_PLATFORM_SEED:-false}
case "$SEED" in
  true) PROFILES=combined,e2e ;;
  false) PROFILES=combined ;;
  *) die "AURIGA_PLATFORM_SEED must be true or false" ;;
esac
HOST_NC=${AURIGA_HOST_PUBLISH_NODECONTROL:-17443}
HOST_REG=${AURIGA_HOST_PUBLISH_REGISTRY:-15000}

mkdir -p "$CONFIG/postgres" "$CONFIG/nats" "$CONFIG/templates" "$CONFIG/pki-edge"

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
AURIGA_IAM_JWK_SET_URI=http://127.0.0.1:8081/oauth2/jwks
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
AURIGA_PLATFORM_SEED=$SEED
EOF
chmod 0600 "$CONFIG/platform.env"
chown_required 10001:10001 "$CONFIG/platform.env"

TRUSTED_YAML=$(printf '%s' "$TRUSTED" | tr ',' '\n' | sed '/^$/d' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | sed 's/^/      - "/;s/$/"/')
cat >"$CONFIG/platform-runtime.yaml" <<EOF
auriga:
  platform:
    seed: $SEED
    team:
      rp-id: "$RP_ID"
      origin: "$ORIGIN"
  iam:
    issuer: "$ISSUER"
    oidc:
      allow-local-http: false
    hosts:
      "[$PORTAL]":
        tenant: 00000000-0000-0000-0000-000000000001
        audience: portal
        secure: $SECURE
      "[$ADMIN]":
        tenant: 00000000-0000-0000-0000-000000000001
        audience: admin
        secure: $SECURE
    trusted-proxies:
$TRUSTED_YAML
  security:
    jwt:
      issuer: "$ISSUER"
      jwk-set-uri: "http://127.0.0.1:8081/oauth2/jwks"
      allow-insecure-jwks-uri: true
EOF
chmod 0644 "$CONFIG/platform-runtime.yaml"

TP_JSON=$(printf '%s' "$TRUSTED" | tr ',' '\n' | sed '/^$/d' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | sed 's/^/"/;s/$/"/' | awk '{s=(s?s",":"")$0} END{print s}')
[ -n "$TP_JSON" ] || TP_JSON='"127.0.0.1"'

cat >"$CONFIG/edge.config.json" <<EOF
{
  "listen": "0.0.0.0:8080",
  "portalHost": "$PORTAL",
  "adminHost": "$ADMIN",
  "instanceParentDomain": "$INSTANCE_PARENT",
  "secureCookies": $SECURE,
  "allowLocalIngress": true,
  "trustedProxies": [$TP_JSON],
  "upstreams": {
    "iam": "http://platform:8081",
    "org": "http://platform:8081",
    "workspace": "http://platform:8081"
  },
  "static": {
    "portalRoot": "/srv/auriga/static/portal",
    "adminRoot": "/srv/auriga/static/admin"
  },
  "tls": { "mode": "off" },
  "rateLimit": { "requestsPerSecond": 50, "burst": 100 },
  "identityKeysFile": "/etc/auriga/identity-signing-keys.json",
  "sessionStore": "nats",
  "natsUrl": "nats://edge:$NATS_EDGE@nats:4222",
  "nodeTLS": {
    "certFile": "/etc/auriga/pki/edge.crt",
    "keyFile": "/etc/auriga/pki/edge.key",
    "caFile": "/etc/auriga/pki/ca.crt"
  }
}
EOF
chmod 0600 "$CONFIG/edge.config.json"
chown_required 65532:65532 "$CONFIG/edge.config.json"

# Keep pki-edge in sync if re-render
if [ -f "$CONFIG/pki/edge.key" ]; then
  cp -f "$CONFIG/pki/ca.crt" "$CONFIG/pki-edge/ca.crt"
  cp -f "$CONFIG/pki/edge.crt" "$CONFIG/pki-edge/edge.crt"
  cp -f "$CONFIG/pki/edge.key" "$CONFIG/pki-edge/edge.key"
  chown_required 65532:65532 "$CONFIG/pki-edge/edge.key" "$CONFIG/pki-edge/edge.crt" "$CONFIG/pki-edge/ca.crt"
  chmod 0600 "$CONFIG/pki-edge/edge.key"
  chmod 0644 "$CONFIG/pki-edge/edge.crt" "$CONFIG/pki-edge/ca.crt"
fi

REG_PREFIX="127.0.0.1:${HOST_REG}/auriga/"
cat >"$CONFIG/agent-policy.yaml" <<EOF
allowedImageRepos:
  - ${REG_PREFIX}
  - localhost:${HOST_REG}/auriga/
maxCPUMillis: 500
maxMemoryMB: 512
maxPIDs: 128
maxArtifactBytes: 2147483648
EOF
chmod 0644 "$CONFIG/agent-policy.yaml"

cat >"$CONFIG/agent.env" <<EOF
AURIGA_AGENT_SERVER=https://127.0.0.1:${HOST_NC}
AURIGA_AGENT_CA_SHA256=$CA_SHA
AURIGA_AGENT_ALLOW_INSECURE=false
# DNS alias on the trusted edge inside each isolated instance bridge.
AURIGA_AGENT_INSTANCE_PLATFORM_URL=http://${PORTAL}:8080
AURIGA_AGENT_INSTANCE_PARENT_DOMAIN=$INSTANCE_PARENT
EOF
chmod 0600 "$CONFIG/agent.env"

printf '%s' "$PG_BOOT" >"$SECRETS/pg_bootstrap_password"
chmod 0600 "$SECRETS/pg_bootstrap_password"

if [ ! -f "$CONFIG/templates/README.md" ]; then
  cat >"$CONFIG/templates/README.md" <<'EOF'
# Instance templates

Place signed InstanceSpec protobuf files from the W3 real-DSH pipeline here.
This NAS pack refuses to ship CI fake-dsh templates or placeholder signatures.
Without a real template the stack can start for health/static checks but must not
be marked as real acceptance complete.
EOF
  chmod 0644 "$CONFIG/templates/README.md"
fi

info "render complete (configs under $CONFIG; secrets not printed)"
info "note: platform.env embeds DB/NATS secrets as env vars — visible to anyone with docker inspect (M5)"
