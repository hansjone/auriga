#!/bin/sh
# Exercise the actual renderer; unrelated material generation/ownership is stubbed.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf -- "$TMP"' EXIT INT TERM
mkdir -p "$TMP/package/scripts" "$TMP/package/postgres" "$TMP/package/nats"
cp "$ROOT/scripts/render.sh" "$TMP/package/scripts/"
cp "$ROOT/postgres/init.sql.tmpl" "$TMP/package/postgres/"
cp "$ROOT/nats/nats.conf.tmpl" "$TMP/package/nats/"
cat >"$TMP/package/scripts/lib.sh" <<'EOF'
die() { printf '%s\n' "$*" >&2; exit 1; }
info() { :; }
load_stack_env() { :; }
require_cmd() { :; }
require_root_or_capable_chown() { :; }
bootstrap_nas_materials() { :; }
install_litellm_materials() { :; }
install_edge_material() { :; }
chown_required() { :; }
load_nas_access_env() {
  AURIGA_NAS_ISSUER=http://portal.example.com:8080
  AURIGA_NAS_PORTAL_URL=$AURIGA_NAS_ISSUER
  AURIGA_NAS_PORTAL_HOST=portal.example.com
}
EOF
for script in render-collab render-managed; do printf '#!/bin/sh\nexit 0\n' >"$TMP/package/scripts/$script.sh"; done
export NAS_ROOT="$TMP/package"
unset AURIGA_INSTALL_MODE NAS_INSTALLER_ACTIVE AURIGA_IAM_EMERGENCY_REMOTE_ENABLED
index=0
for value in default true false empty invalid; do
  index=$((index + 1))
  export AURIGA_DATA_ROOT="$TMP/data-$index"
  mkdir -p "$AURIGA_DATA_ROOT/secrets" "$AURIGA_DATA_ROOT/config/materials" \
    "$AURIGA_DATA_ROOT/config/postgres" "$AURIGA_DATA_ROOT/config/nats"
  for name in pg_bootstrap_password pg_iam_password pg_org_password pg_ws_owner_password pg_ws_app_password \
    nats_sys_password nats_platform_password nats_routes_rw_password nats_edge_password iam_encryption_key workspace_encryption_key; do
    openssl rand -hex 16 >"$AURIGA_DATA_ROOT/secrets/$name"
  done
  openssl rand -hex 32 >"$AURIGA_DATA_ROOT/config/materials/ca-sha256.txt"
  case "$value" in
    default) unset AURIGA_IAM_EMERGENCY_REMOTE_ENABLED ;;
    empty) AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=; export AURIGA_IAM_EMERGENCY_REMOTE_ENABLED ;;
    *) AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=$value; export AURIGA_IAM_EMERGENCY_REMOTE_ENABLED ;;
  esac
  if [ "$value" = invalid ]; then
    if sh "$NAS_ROOT/scripts/render.sh" >"$TMP/output" 2>&1; then exit 1; fi
    grep -qx 'AURIGA_IAM_EMERGENCY_REMOTE_ENABLED must be true or false' "$TMP/output"
    [ ! -e "$AURIGA_DATA_ROOT/config/platform.env" ]
  else
    sh "$NAS_ROOT/scripts/render.sh"
    expected=true
    [ "$value" != false ] || expected=false
    grep -qx "AURIGA_IAM_EMERGENCY_REMOTE_ENABLED=$expected" "$AURIGA_DATA_ROOT/config/platform.env"
  fi
  printf 'PASS emergency_remote_render_%s\n' "$value"
done
