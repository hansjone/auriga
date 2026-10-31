#!/bin/sh
# Real NAS init/render/activation and UID boundaries; no Docker daemon required.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
BOOT=$1
PARENT=$2
INPUT=$3
MODE=$4
DATA="$PARENT/srv/container/auriga-collab-$MODE"
mkdir -p "$DATA"
cp "$ROOT/access-$MODE.example.json" "$PARENT/access-$MODE.json"
export AURIGA_LINKS_ALLOWED_ORIGINS=https://network.example.com
export NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$DATA" AURIGA_NAS_BOOTSTRAP_BINARY="$BOOT" NAS_ALLOW_NONROOT=0
cat >"$DATA/stack.env" <<EOF
AURIGA_DATA_ROOT=$DATA
AURIGA_ACCESS_FILE=$PARENT/access-$MODE.json
AURIGA_INTERNAL_EDGE_IP=192.0.2.138
AURIGA_INTERNAL_SUBNET=192.0.2.128/26
AURIGA_LAN_SUBNET=192.0.2.0/26
AURIGA_EDGE_LAN_IP=192.0.2.10
AURIGA_COLLAB_INPUT_DIR=$(dirname -- "$INPUT")
AURIGA_PLATFORM_SEED=false
AURIGA_COLLAB_PUBLIC_ORIGIN=https://network.example.com
AURIGA_LINKS_ALLOWED_ORIGINS=https://network.example.com
EOF
chmod 0600 "$DATA/stack.env"
sh "$ROOT/scripts/init.sh"
SOURCE="$DATA/config/collab-source"
RUNTIME="$DATA/config/collab-runtime"
BASE=$(sha256sum "$DATA/config/materials/iam-installation/bootstrap.receipt")
ACTIVATION=$(sha256sum "$SOURCE/activation.receipt")
sh "$ROOT/scripts/render.sh"
[ "$(sha256sum "$SOURCE/activation.receipt")" = "$ACTIVATION" ]
[ "$(sha256sum "$DATA/config/materials/iam-installation/bootstrap.receipt")" = "$BASE" ]
"$BOOT" collab -base "$DATA/config/materials" -out "$SOURCE" -public-origin https://network.example.com -check
# Container operators cannot traverse root's private receipt. The privileged
# export must carry the checked overlay decision with its existing env stream.
EXPORT="$PARENT/collab-env-$MODE.tar"
NAS_INSTALLER_ACTIVE=1 sh "$ROOT/scripts/installer-entry.sh" export-env >"$EXPORT"
tar -tf "$EXPORT" | grep -qx collab.enabled
[ "$(tar -xOf "$EXPORT" collab.enabled)" = 1 ]
setpriv --reuid=65534 --regid=65534 --clear-groups sh -ec '! test -f "$1/activation.receipt"' sh "$SOURCE"
# Exercise the host-side branch as an actual unprivileged operator. Its only
# receipt knowledge is the privileged export; Compose itself is an argv recorder.
MOCK="$PARENT/compose-operator-$MODE"
mkdir -p "$MOCK/scripts" "$MOCK/private"
chown 65534:65534 "$MOCK/private" "$EXPORT"
chmod 0700 "$MOCK/private"
cat >"$MOCK/scripts/installer.sh" <<'EOF'
#!/bin/sh
test "$1" = export-env
cat "$EXPORTED_ENV"
EOF
cat >"$MOCK/compose" <<'EOF'
#!/bin/sh
printf '%s\n' "$@"
EOF
chmod 0755 "$MOCK/scripts/installer.sh" "$MOCK/compose"
EXPORTED_ENV="$EXPORT" NAS_ROOT="$MOCK" LIB="$ROOT/scripts/lib.sh" \
  setpriv --reuid=65534 --regid=65534 --clear-groups sh -ec '
    . "$LIB"
    compose_cmd() { printf "%s\n" "$NAS_ROOT/compose"; }
    installer_private_dir() { mktemp -d "$NAS_ROOT/private/compose.XXXXXX"; }
    container_compose config >"$NAS_ROOT/private/argv"
  '
grep -qx "$MOCK/collab-compose.yml" "$MOCK/private/argv"
printf 'PASS collab_%s_private_receipt_export\n' "$MODE"
# The runtime readers cannot open the installer's source or other service keys.
(cd "$RUNTIME/collab"; setpriv --reuid=10002 --regid=10002 --clear-groups sh -ec 'test -r business.der; ! test -r "$1/collab-installation/business.der"; ! test -r ../network/protocol.key' sh "$SOURCE")
(cd "$RUNTIME/network"; setpriv --reuid=10001 --regid=10001 --clear-groups sh -ec 'test -r protocol.key; ! test -r ../collab/business.der' sh)
(cd "$RUNTIME/edge"; setpriv --reuid=65532 --regid=65532 --clear-groups sh -ec 'test -r edge-collab.key; ! test -r ../network/protocol.key' sh)
printf 'PASS collab_%s_idempotent_and_uid_boundaries\n' "$MODE"
# Runtime corruption, source corruption and changed base must all fail closed.
cp "$RUNTIME/network/protocol.key" "$PARENT/protocol-$MODE.saved"
printf bad >"$RUNTIME/network/protocol.key"
if sh "$ROOT/scripts/render.sh" >/dev/null 2>&1; then echo 'runtime tamper accepted' >&2; exit 1; fi
cat "$PARENT/protocol-$MODE.saved" >"$RUNTIME/network/protocol.key"
cp "$SOURCE/roles.sql" "$PARENT/roles-$MODE.saved"
printf bad >"$SOURCE/roles.sql"
if sh "$ROOT/scripts/render.sh" >/dev/null 2>&1; then echo 'source tamper accepted' >&2; exit 1; fi
cat "$PARENT/roles-$MODE.saved" >"$SOURCE/roles.sql"
cp "$DATA/config/materials/access.json" "$PARENT/base-$MODE.saved"
printf bad >"$DATA/config/materials/access.json"
if "$BOOT" collab -base "$DATA/config/materials" -out "$SOURCE" -public-origin https://network.example.com -check >/dev/null 2>&1; then echo 'changed base accepted' >&2; exit 1; fi
cat "$PARENT/base-$MODE.saved" >"$DATA/config/materials/access.json"
sh "$ROOT/scripts/render.sh"
printf 'PASS collab_%s_tamper_refused\n' "$MODE"
if [ "$MODE" = ip ] && [ -n "${AURIGA_NAS_COLLAB_LIVE_IMAGE:-}" ]; then
  sh "$ROOT/test/collab-live.sh" "$DATA" "$AURIGA_NAS_COLLAB_LIVE_IMAGE"
fi
