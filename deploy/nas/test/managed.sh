#!/bin/sh
set -eu
ROOT=$1
PARENT=$2
DATA="$PARENT/auriga-managed-tls-data"
mkdir -p "$DATA/config/materials/pki" "$DATA/config/pki-edge"
chmod 0755 "$DATA"
cat >"$DATA/stack.env" <<EOF
AURIGA_DATA_ROOT=$DATA
AURIGA_ACCESS_FILE=$ROOT/access-ip.example.json
AURIGA_MANAGED_PLATFORM_ORIGIN=https://platform.internal:18446
AURIGA_MANAGED_PLATFORM_HOST=platform.internal
AURIGA_LINKS_ALLOWED_ORIGINS=https://platform.internal:18446
EOF
chmod 0600 "$DATA/stack.env"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj /CN=managed-test-ca \
  -addext basicConstraints=critical,CA:TRUE -keyout "$DATA/config/materials/pki/ca.key" \
  -out "$DATA/config/materials/pki/ca.crt" >/dev/null 2>&1
before=$(sha256sum "$DATA/config/materials/pki/ca.key")
NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$DATA" sh "$ROOT/scripts/render-managed.sh"
first=$(sha256sum "$DATA/config/managed-source/server.key")
NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$DATA" sh "$ROOT/scripts/render-managed.sh"
[ "$before" = "$(sha256sum "$DATA/config/materials/pki/ca.key")" ]
[ "$first" = "$(sha256sum "$DATA/config/managed-source/server.key")" ]
[ "$(stat -c '%u:%g:%a' "$DATA/config/managed-edge/server.key")" = 65532:65532:600 ]
openssl verify -CAfile "$DATA/config/managed-ca/ca.crt" -verify_hostname platform.internal "$DATA/config/managed-edge/server.crt" >/dev/null
for origin in 'http://platform.internal:18446' 'https://platform.internal:99999' 'https://foreign.internal:18446'; do
  sed "s|^AURIGA_MANAGED_PLATFORM_ORIGIN=.*|AURIGA_MANAGED_PLATFORM_ORIGIN=$origin|" "$DATA/stack.env" >"$DATA/rejected.env"
  cp "$DATA/stack.env" "$DATA/saved.env"; cp "$DATA/rejected.env" "$DATA/stack.env"
  if NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$DATA" sh "$ROOT/scripts/render-managed.sh" >/dev/null 2>&1; then echo 'unsafe managed origin accepted' >&2; exit 1; fi
  cp "$DATA/saved.env" "$DATA/stack.env"
done
chmod 0644 "$DATA/config/managed-source/server.key"
if NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$DATA" sh "$ROOT/scripts/render-managed.sh" >/dev/null 2>&1; then echo 'managed private key widening accepted' >&2; exit 1; fi
echo 'PASS managed_tls_idempotent_root_preserved_origin_and_permission_fences'
