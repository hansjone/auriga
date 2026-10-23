#!/bin/sh
# Opt-in root Linux/Docker regression; fails rather than reporting skipped success.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
[ "$(id -u)" = 0 ] || { echo 'real mount fixture requires Linux root'; exit 1; }
: "${AURIGA_MOUNT_TEST_IMAGE:?set to a preloaded immutable platform image}"
: "${AURIGA_MOUNT_TEST_REGISTRY_IMAGE:?set to the preloaded immutable registry image}"
[ "$(docker context inspect --format '{{.Endpoints.docker.Host}}')" = unix:///var/run/docker.sock ] || exit 1
TMP=$(mktemp -d /tmp/auriga-mount-access.XXXXXX)
cleanup() { rm -rf -- "$TMP"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
chmod 0755 "$TMP"
mkdir -p "$TMP/container/auriga-data"
export NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$TMP/container/auriga-data"
unset NAS_ALLOW_NONROOT NAS_INSTALLER_ACTIVE
cat >"$AURIGA_DATA_ROOT/stack.env" <<EOF
AURIGA_DATA_ROOT=$AURIGA_DATA_ROOT
AURIGA_ACCESS_FILE=$TMP/access.json
AURIGA_IMAGE_PLATFORM=$AURIGA_MOUNT_TEST_IMAGE
AURIGA_IMAGE_REGISTRY=$AURIGA_MOUNT_TEST_REGISTRY_IMAGE
EOF
chmod 0600 "$AURIGA_DATA_ROOT/stack.env"
touch "$AURIGA_DATA_ROOT/.auriga-initialized"
mkdir -p "$AURIGA_DATA_ROOT/config/materials/pki" "$AURIGA_DATA_ROOT/config/pki-edge" \
  "$AURIGA_DATA_ROOT/config/materials/iam-installation" "$AURIGA_DATA_ROOT/config/materials/templates" \
  "$AURIGA_DATA_ROOT/config/materials/runtime-keys" "$AURIGA_DATA_ROOT/config/materials/runtime-proofs/nested" \
  "$AURIGA_DATA_ROOT/config/nats" "$AURIGA_DATA_ROOT/config/postgres" \
  "$AURIGA_DATA_ROOT/internal-tokens/edge" "$AURIGA_DATA_ROOT/enrollment" \
  "$AURIGA_DATA_ROOT/socket-share" "$AURIGA_DATA_ROOT/join" "$AURIGA_DATA_ROOT/secrets" \
  "$AURIGA_DATA_ROOT/data/agent" "$AURIGA_DATA_ROOT/data/nats" \
  "$AURIGA_DATA_ROOT/data/postgres" "$AURIGA_DATA_ROOT/data/registry"
for leaf in access.json platform-access.yaml iam-healthcheck.sh edge.config.json identity-keys.json agent-policy.yaml; do
  printf 'public fixture\n' >"$AURIGA_DATA_ROOT/config/materials/$leaf"
done
for leaf in ca.crt iam.crt workspace.crt; do printf 'public fixture\n' >"$AURIGA_DATA_ROOT/config/materials/pki/$leaf"; done
for leaf in ca.key iam.key workspace.key; do printf 'SECRET_MUST_NOT_APPEAR\n' >"$AURIGA_DATA_ROOT/config/materials/pki/$leaf"; done
for leaf in ca.crt edge.crt edge-iam.crt proxy-ca.crt proxy-edge.crt; do printf 'public fixture\n' >"$AURIGA_DATA_ROOT/config/pki-edge/$leaf"; done
for leaf in edge.key edge-iam.key proxy-edge.key identity-signing-keys.json; do printf 'SECRET_MUST_NOT_APPEAR\n' >"$AURIGA_DATA_ROOT/config/pki-edge/$leaf"; done
for leaf in internal.properties edge.secret; do printf 'SECRET_MUST_NOT_APPEAR\n' >"$AURIGA_DATA_ROOT/config/materials/iam-installation/$leaf"; done
for leaf in config/materials/templates/template.pb config/materials/runtime-keys/root.json config/materials/runtime-proofs/nested/proof.json; do
  printf 'public fixture\n' >"$AURIGA_DATA_ROOT/$leaf"
done
for leaf in config/nats/nats.conf config/postgres/init.sql secrets/pg_bootstrap_password join/token internal-tokens/edge/service.jwt; do
  printf 'SECRET_MUST_NOT_APPEAR\n' >"$AURIGA_DATA_ROOT/$leaf"
done
chown -R 10001:10001 "$AURIGA_DATA_ROOT/config/materials"
chmod 0700 "$AURIGA_DATA_ROOT/config/materials/iam-installation"
chmod 0600 "$AURIGA_DATA_ROOT/config/materials/iam-installation/"* "$AURIGA_DATA_ROOT/config/materials/pki/"*.key
chown -R 65532:65532 "$AURIGA_DATA_ROOT/config/pki-edge" "$AURIGA_DATA_ROOT/config/nats" "$AURIGA_DATA_ROOT/data/nats"
chmod 0600 "$AURIGA_DATA_ROOT/config/pki-edge/"*.key "$AURIGA_DATA_ROOT/config/pki-edge/identity-signing-keys.json" "$AURIGA_DATA_ROOT/config/nats/nats.conf"
chown -R 999:999 "$AURIGA_DATA_ROOT/config/postgres" "$AURIGA_DATA_ROOT/data/postgres"
chmod 0600 "$AURIGA_DATA_ROOT/config/postgres/init.sql"
chmod 0700 "$AURIGA_DATA_ROOT/data/postgres" "$AURIGA_DATA_ROOT/data/agent" "$AURIGA_DATA_ROOT/secrets"
chmod 0600 "$AURIGA_DATA_ROOT/secrets/pg_bootstrap_password" "$AURIGA_DATA_ROOT/join/token"
chown -R 10001:65532 "$AURIGA_DATA_ROOT/internal-tokens"
chmod 0750 "$AURIGA_DATA_ROOT/internal-tokens" "$AURIGA_DATA_ROOT/internal-tokens/edge"
chmod 0640 "$AURIGA_DATA_ROOT/internal-tokens/edge/service.jwt"
# A different owner makes platform's read require its real supplementary group.
chown 65532:65532 "$AURIGA_DATA_ROOT/internal-tokens/edge/service.jwt"
chown 0:65532 "$AURIGA_DATA_ROOT/socket-share"
chmod 0750 "$AURIGA_DATA_ROOT/socket-share"
snapshot() {
  python3 - "$AURIGA_DATA_ROOT" <<'PY'
import hashlib, json, os, stat, sys
rows = []
for base, dirs, files in os.walk(sys.argv[1]):
    for path in [base] + [os.path.join(base, f) for f in files]:
        s = os.lstat(path)
        content = hashlib.sha256(open(path, 'rb').read()).hexdigest() if stat.S_ISREG(s.st_mode) else ''
        attrs = {a: os.getxattr(path, a).hex() for a in os.listxattr(path)}
        rows.append((path, s.st_mode, s.st_uid, s.st_gid, s.st_mtime_ns, content, attrs))
print(json.dumps(sorted(rows)))
PY
}
check() {
  name=$1; expected=$2; diagnostic=$3
  snapshot >"$TMP/before"
  if sh "$ROOT/scripts/mount-access.sh" >"$TMP/output" 2>&1; then result=0; else result=$?; fi
  snapshot >"$TMP/after"
  cmp -s "$TMP/before" "$TMP/after" || { echo "FAIL $name modified fixture"; exit 1; }
  ! grep -q SECRET_MUST_NOT_APPEAR "$TMP/output" || { echo 'FAIL secret disclosure'; exit 1; }
  if [ "$expected" = pass ]; then
    [ "$result" = 0 ] && [ "$(grep -c '^mount-access OK ' "$TMP/output")" = 7 ] || { cat "$TMP/output"; exit 1; }
  else
    [ "$result" != 0 ] && grep -q "$diagnostic" "$TMP/output" || { cat "$TMP/output"; exit 1; }
  fi
  printf 'PASS %s (content/owner/mode/mtime/ACL unchanged)\n' "$name"
}
check all_service_identities_and_supplementary_group pass ''
# Inherited POSIX ACL denies edge despite a visible mode of 0755.
mv "$AURIGA_DATA_ROOT/config/pki-edge" "$TMP/pki-backup"
python3 - "$AURIGA_DATA_ROOT/config" <<'PY'
import os, struct, sys
entries = [(1,7,0xffffffff),(2,0,65532),(4,5,0xffffffff),(16,5,0xffffffff),(32,5,0xffffffff)]
acl = struct.pack('<I', 2) + b''.join(struct.pack('<HHI', *e) for e in entries)
os.setxattr(sys.argv[1], 'system.posix_acl_default', acl)
os.mkdir(sys.argv[1] + '/pki-edge', 0o755)
assert os.stat(sys.argv[1] + '/pki-edge').st_mode & 0o777 == 0o755
os.removexattr(sys.argv[1], 'system.posix_acl_default')
PY
cp -p "$TMP/pki-backup/"* "$AURIGA_DATA_ROOT/config/pki-edge/"
check inherited_acl_755_edge_denied fail 'service=edge uid:gid=65532:65532 supplementary=none path-category=pki operation=traverse'
rm -rf -- "$AURIGA_DATA_ROOT/config/pki-edge"
mv "$TMP/pki-backup" "$AURIGA_DATA_ROOT/config/pki-edge"
chmod 0000 "$AURIGA_DATA_ROOT/config/pki-edge/edge.key"
check edge_secret_open_denied fail 'service=edge.*path-category=pki operation=secret-open'
chmod 0600 "$AURIGA_DATA_ROOT/config/pki-edge/edge.key"
chmod 0000 "$AURIGA_DATA_ROOT/config/materials/edge.config.json"
check edge_public_read_denied fail 'service=edge.*path-category=edge-config operation=public-read'
chmod 0644 "$AURIGA_DATA_ROOT/config/materials/edge.config.json"
chmod 0000 "$AURIGA_DATA_ROOT/config/materials/templates/template.pb"
check platform_public_tree_denied fail 'service=platform uid:gid=10001:10001.*path-category=templates operation=public-read'
chmod 0644 "$AURIGA_DATA_ROOT/config/materials/templates/template.pb"
chmod 0000 "$AURIGA_DATA_ROOT/config/nats/nats.conf"
check nats_secret_open_denied fail 'service=nats.*path-category=nats-config operation=secret-open'
chmod 0600 "$AURIGA_DATA_ROOT/config/nats/nats.conf"
chmod 0600 "$AURIGA_DATA_ROOT/data/nats"
check nats_directory_traverse_denied fail 'service=nats.*path-category=data operation=traverse'
chmod 0755 "$AURIGA_DATA_ROOT/data/nats"
check repaired_fixture_passes pass ''
