#!/bin/sh
# First-time initialization for the Auriga NAS test stack.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

load_stack_env
if [ "${AURIGA_INSTALL_MODE:-host}" = container ] && [ "${NAS_INSTALLER_ACTIVE:-}" != 1 ]; then
  exec sh "$NAS_ROOT/scripts/installer.sh" init
fi
require_cmd openssl mkdir chmod cmp stat
validate_nas_access
require_root_or_capable_chown

MARKER=$(marker_path)
SECRETS="$AURIGA_DATA_ROOT/secrets"
CONFIG="$AURIGA_DATA_ROOT/config"
DATA="$AURIGA_DATA_ROOT/data"

if [ -f "$MARKER" ]; then
  info "already initialized ($MARKER); rendering configs only"
  sh "$NAS_ROOT/scripts/render.sh"
  exit 0
fi

# Refuse foreign tree without our marker (M3). Allow only stack.env as a pre-seed file.
if [ -e "$AURIGA_DATA_ROOT" ] || [ -L "$AURIGA_DATA_ROOT" ]; then
  if [ -L "$AURIGA_DATA_ROOT" ]; then
    die "AURIGA_DATA_ROOT is a symlink; refusing"
  fi
  if [ -d "$AURIGA_DATA_ROOT" ]; then
    _extra=0
    for _e in "$AURIGA_DATA_ROOT"/* "$AURIGA_DATA_ROOT"/.[!.]* "$AURIGA_DATA_ROOT"/..?*; do
      [ -e "$_e" ] || continue
      _bn=$(basename -- "$_e")
      case "$_bn" in
        .|..|stack.env) continue ;;
      esac
      _extra=1
      break
    done
    if [ "$_extra" -ne 0 ]; then
      die "data root exists, has content other than stack.env, and has no .auriga-initialized; refusing"
    fi
  fi
fi

if [ -d "$DATA/postgres" ] && [ ! -f "$SECRETS/pg_bootstrap_password" ]; then
  die "postgres data exists but secrets are missing; refusing to generate new keys"
fi
if [ -d "$DATA/nats" ] && [ ! -f "$SECRETS/nats_platform_password" ]; then
  die "nats data exists but secrets are missing; refusing to generate new keys"
fi

mkdir -p \
  "$SECRETS" \
  "$CONFIG/pki-edge" \
  "$CONFIG/postgres" \
  "$CONFIG/nats" \
  "$AURIGA_DATA_ROOT/internal-tokens" \
  "$AURIGA_DATA_ROOT/network-tokens" \
  "$DATA/postgres" \
  "$DATA/nats" \
  "$DATA/registry" \
  "$DATA/agent/agent" \
  "$DATA/agent/instances" \
  "$DATA/agent/artifacts" \
  "$DATA/agent/backups" \
  "$AURIGA_DATA_ROOT/socket-share" \
  "$AURIGA_DATA_ROOT/enrollment" \
  "$AURIGA_DATA_ROOT/join" \
  "$AURIGA_DATA_ROOT/results" \
  "$AURIGA_DATA_ROOT/bin"

chmod_strict 0700 "$SECRETS" "$DATA/agent"
chmod_strict 0750 "$AURIGA_DATA_ROOT/enrollment" "$AURIGA_DATA_ROOT/join"
prepare_socket_parent
chmod_strict 0755 "$CONFIG" "$CONFIG/pki-edge"
chown_required 10001:65532 "$AURIGA_DATA_ROOT/internal-tokens"
chmod 0750 "$AURIGA_DATA_ROOT/internal-tokens"
chown_required 10001:10001 "$AURIGA_DATA_ROOT/network-tokens"
chmod 0700 "$AURIGA_DATA_ROOT/network-tokens"

write_secret() {
  _name=$1
  _value=$2
  _path="$SECRETS/$_name"
  refuse_secret_overwrite "$_path"
  case "$_value" in
    '') die "refusing empty secret: $_name" ;;
  esac
  [ "${#_value}" -ge 16 ] || die "secret too short: $_name"
  umask 077
  printf '%s' "$_value" >"$_path"
  chmod 0600 "$_path" || die "chmod secret failed: $_name"
}

# Generate into variables first (M4) — never write empty.
_v=$(rand_alnum 32); write_secret pg_bootstrap_password "$_v"
_v=$(rand_alnum 32); write_secret pg_iam_password "$_v"
_v=$(rand_alnum 32); write_secret pg_org_password "$_v"
_v=$(rand_alnum 32); write_secret pg_ws_owner_password "$_v"
_v=$(rand_alnum 32); write_secret pg_ws_app_password "$_v"
_v=$(rand_alnum 32); write_secret nats_sys_password "$_v"
_v=$(rand_alnum 32); write_secret nats_platform_password "$_v"
_v=$(rand_alnum 32); write_secret nats_routes_rw_password "$_v"
_v=$(rand_alnum 32); write_secret nats_edge_password "$_v"
_v=$(openssl rand -base64 32 | tr -d '\n') || die "openssl rand failed"
[ -n "$_v" ] || die "empty iam encryption key"
write_secret iam_encryption_key "$_v"
_v=$(openssl rand -base64 32 | tr -d '\n') || die "openssl rand failed"
[ -n "$_v" ] || die "empty workspace encryption key"
write_secret workspace_encryption_key "$_v"

bootstrap_nas_materials
# Source materials/receipt stay installer-owned. Edge receives only exact leaf copies.
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
chown_required 10001:10001 "$AURIGA_DATA_ROOT/enrollment"
chown_required 65532:65532 "$AURIGA_DATA_ROOT/data/nats"

: >"$AURIGA_DATA_ROOT/join/token"
chmod 0600 "$AURIGA_DATA_ROOT/join/token"

sh "$NAS_ROOT/scripts/render.sh" || die "render failed after secrets created — fix and re-run render.sh (marker not written)"

{
  printf 'initialized_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date)"
  printf 'data_root=%s\n' "$AURIGA_DATA_ROOT"
  printf 'note=do-not-delete-secrets\n'
} >"$MARKER"
chmod 0644 "$MARKER"

info "initialization complete"
info "emergency enrollment is written by platform on first seed start (path only; contents never logged)"
