#!/bin/sh
# First-time initialization for the Auriga NAS test stack.
# Generates random DB/NATS passwords, encryption keys, CA/signing material.
# Re-runs are idempotent when the marker exists; refuse if data exists without secrets.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
require_cmd openssl mkdir chmod

is_absolute_path "$AURIGA_DATA_ROOT" || die "AURIGA_DATA_ROOT must be an absolute path"
case "$AURIGA_DATA_ROOT" in
  *..*) die "AURIGA_DATA_ROOT must not contain .." ;;
esac

MARKER=$(marker_path)
SECRETS="$AURIGA_DATA_ROOT/secrets"
CONFIG="$AURIGA_DATA_ROOT/config"
DATA="$AURIGA_DATA_ROOT/data"

if [ -f "$MARKER" ]; then
  info "already initialized ($MARKER); rendering configs only"
  "$NAS_ROOT/scripts/render.sh"
  exit 0
fi

if [ -d "$DATA/postgres" ] && [ ! -f "$SECRETS/pg_bootstrap_password" ]; then
  die "postgres data exists but secrets are missing; refusing to generate new keys"
fi
if [ -d "$DATA/nats" ] && [ ! -f "$SECRETS/nats_platform_password" ]; then
  die "nats data exists but secrets are missing; refusing to generate new keys"
fi

mkdir -p \
  "$SECRETS" \
  "$CONFIG/pki" \
  "$CONFIG/postgres" \
  "$CONFIG/nats" \
  "$CONFIG/templates" \
  "$DATA/postgres" \
  "$DATA/nats" \
  "$DATA/registry" \
  "$DATA/agent/agent" \
  "$DATA/agent/instances" \
  "$DATA/agent/artifacts" \
  "$DATA/agent/backups" \
  "$DATA/agent/ingress" \
  "$AURIGA_DATA_ROOT/enrollment" \
  "$AURIGA_DATA_ROOT/join" \
  "$AURIGA_DATA_ROOT/results" \
  "$AURIGA_DATA_ROOT/bin"

chmod_strict 0700 "$SECRETS"
chmod_strict 0750 "$DATA/agent" "$DATA/agent/ingress" "$AURIGA_DATA_ROOT/enrollment" "$AURIGA_DATA_ROOT/join"
chmod_strict 0755 "$CONFIG" "$CONFIG/pki" "$CONFIG/templates"

write_secret() {
  _name=$1
  _value=$2
  _path="$SECRETS/$_name"
  refuse_secret_overwrite "$_path"
  umask 077
  printf '%s' "$_value" >"$_path"
  chmod 0600 "$_path"
}

write_secret pg_bootstrap_password "$(rand_alnum 32)"
write_secret pg_iam_password "$(rand_alnum 32)"
write_secret pg_org_password "$(rand_alnum 32)"
write_secret pg_ws_owner_password "$(rand_alnum 32)"
write_secret pg_ws_app_password "$(rand_alnum 32)"
write_secret nats_sys_password "$(rand_alnum 32)"
write_secret nats_platform_password "$(rand_alnum 32)"
write_secret nats_routes_rw_password "$(rand_alnum 32)"
write_secret nats_edge_password "$(rand_alnum 32)"
write_secret iam_encryption_key "$(openssl rand -base64 32 | tr -d '\n')"
write_secret workspace_encryption_key "$(openssl rand -base64 32 | tr -d '\n')"

# PKI + identity keys via bundled linux amd64 bootstrap (built by CI), or local go run for tests.
BOOT=""
if [ -x "$NAS_ROOT/bin/auriga-nas-bootstrap" ]; then
  BOOT="$NAS_ROOT/bin/auriga-nas-bootstrap"
elif [ -x "$AURIGA_DATA_ROOT/bin/auriga-nas-bootstrap" ]; then
  BOOT="$AURIGA_DATA_ROOT/bin/auriga-nas-bootstrap"
elif command -v go >/dev/null 2>&1; then
  info "building bootstrap with local go (dev/test only; NAS uses CI binary)"
  (CDPATH= cd -P -- "$NAS_ROOT/bootstrap" && go build -o "$AURIGA_DATA_ROOT/bin/auriga-nas-bootstrap" .)
  BOOT="$AURIGA_DATA_ROOT/bin/auriga-nas-bootstrap"
else
  die "auriga-nas-bootstrap binary missing and go unavailable"
fi
"$BOOT" -out "$CONFIG" -mode full

chmod 0600 "$CONFIG/pki/ca.key" "$CONFIG/pki/workspace.key" "$CONFIG/identity-signing-keys.json" "$CONFIG/pki/edge.key"
chmod 0644 "$CONFIG/pki/ca.crt" "$CONFIG/pki/workspace.crt" "$CONFIG/pki/edge.crt" "$CONFIG/identity-keys.json" "$CONFIG/ca-sha256.txt"
# Edge (uid/gid 65532) must read its leaf key + signing JWKS; CA stays root-only.
if command -v chown >/dev/null 2>&1; then
  chown 65532:65532 "$CONFIG/pki/edge.key" "$CONFIG/pki/edge.crt" "$CONFIG/identity-signing-keys.json" 2>/dev/null \
    || info "warning: could not chown edge materials to 65532; fix ownership before start"
fi
# Re-apply 0600 after chown for edge private material.
chmod 0600 "$CONFIG/pki/edge.key" "$CONFIG/identity-signing-keys.json"
chmod 0644 "$CONFIG/pki/edge.crt"

: >"$AURIGA_DATA_ROOT/join/token"
chmod 0600 "$AURIGA_DATA_ROOT/join/token"

"$NAS_ROOT/scripts/render.sh"

{
  printf 'initialized_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date)"
  printf 'data_root=%s\n' "$AURIGA_DATA_ROOT"
  printf 'note=do-not-delete-secrets\n'
} >"$MARKER"
chmod 0644 "$MARKER"

info "initialization complete"
info "emergency enrollment is written by platform on first seed start (path only; contents never logged)"
