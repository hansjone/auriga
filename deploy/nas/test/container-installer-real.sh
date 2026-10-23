#!/bin/sh
# Opt-in local Docker integration, never called by the daemon-free test suite.
# Run as root to construct/tamper fixtures; every installer/Compose call is uid1000.
# Inputs: approved prebuilt executable and immutable preloaded installer image.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
[ "$(id -u)" = 0 ] || { echo 'fixture setup requires root'; exit 1; }
: "${AURIGA_INSTALL_TEST_BOOTSTRAP:?prebuilt bootstrap required}"
: "${AURIGA_INSTALL_TEST_IMAGE:?preloaded immutable image required}"
[ "$(docker context inspect --format '{{.Endpoints.docker.Host}}')" = unix:///var/run/docker.sock ] || exit 1
TMP=$(mktemp -d /tmp/auriga-installer-real.XXXXXX)
cleanup() { rm -rf -- "$TMP"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
chmod 0777 "$TMP"
mkdir "$TMP/docker-config"
chown 1000:1000 "$TMP/docker-config"
export DOCKER_CONFIG="$TMP/docker-config"
mkdir -p "$TMP/package/bin" "$TMP/container/auriga-data"
cp -R "$ROOT/scripts" "$TMP/package/"
cp "$ROOT/docker-compose.yml" "$TMP/package/"
cp -R "$ROOT/postgres" "$ROOT/nats" "$TMP/package/"
cp "$ROOT/access-ip.example.json" "$TMP/access.json"
cp "$AURIGA_INSTALL_TEST_BOOTSTRAP" "$TMP/package/bin/auriga-nas-bootstrap"
chmod 0755 "$TMP/package/bin/auriga-nas-bootstrap"
cp "$ROOT/env.example" "$TMP/container/auriga-data/stack.env"
sed -i "s|^AURIGA_DATA_ROOT=.*|AURIGA_DATA_ROOT=$TMP/container/auriga-data|;s|^AURIGA_ACCESS_FILE=.*|AURIGA_ACCESS_FILE=$TMP/access.json|;s|^AURIGA_INSTALL_MODE=.*|AURIGA_INSTALL_MODE=container|;s|^AURIGA_INSTALLER_IMAGE=.*|AURIGA_INSTALLER_IMAGE=$AURIGA_INSTALL_TEST_IMAGE|" "$TMP/container/auriga-data/stack.env"
chmod 0600 "$TMP/container/auriga-data/stack.env"
chown -R 1000:1000 "$TMP/container"
socket_gid=$(stat -c '%g' /var/run/docker.sock)
export NAS_ROOT="$TMP/package" AURIGA_DATA_ROOT="$TMP/container/auriga-data"
unset NAS_ALLOW_NONROOT NAS_INSTALLER_ACTIVE
caller() { setpriv --reuid=1000 --regid=1000 --groups="$socket_gid" "$@"; }
expect_reject() {
  name=$1; shift
  if caller sh "$NAS_ROOT/scripts/installer.sh" "$@" >"$TMP/reject" 2>&1; then
    printf 'FAIL %s\n' "$name"; exit 1
  fi
  printf 'PASS %s\n' "$name"
  # Only rejection diagnostics, never env/secret bodies.
  tail -n 2 "$TMP/reject"
}
[ "$(caller id -u)" = 1000 ]
caller sh "$NAS_ROOT/scripts/init.sh"
printf 'PASS real_uid1000_first_install\n'
caller sh "$NAS_ROOT/scripts/init.sh"
printf 'PASS real_uid1000_reentry\n'
caller sh "$NAS_ROOT/scripts/installer.sh" check
printf 'PASS real_helper_check\n'
# These files must remain inaccessible to the actual host caller.
for leaf in platform edge agent; do
  if caller test -r "$AURIGA_DATA_ROOT/config/$leaf.env"; then echo 'FAIL private_leaf_readable'; exit 1; fi
done
caller sh -c '. "$NAS_ROOT/scripts/lib.sh"; load_stack_env; run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" config --quiet'
printf 'PASS real_uid1000_compose_config_private_env\n'
chown 1000:1000 "$AURIGA_DATA_ROOT"
expect_reject reject_existing_root_owner check
chown 0:0 "$AURIGA_DATA_ROOT"
chown 0:0 "$AURIGA_DATA_ROOT/config/materials"
expect_reject reject_existing_material_owner check
chown 10001:10001 "$AURIGA_DATA_ROOT/config/materials"
receipt="$AURIGA_DATA_ROOT/config/materials/iam-installation/bootstrap.receipt"
[ -n "$receipt" ] && [ -f "$receipt" ] || { echo 'FAIL receipt_locator'; exit 1; }
cp -p "$receipt" "$TMP/receipt.backup"
printf '{}\n' >"$receipt"
expect_reject reject_changed_receipt check
cp -p "$TMP/receipt.backup" "$receipt"
ln "$AURIGA_DATA_ROOT/config/edge.env" "$TMP/edge-hardlink"
expect_reject reject_env_hardlink export-env
rm "$TMP/edge-hardlink"
caller sh "$NAS_ROOT/scripts/installer.sh" check
printf 'PASS real_helper_final_check\n'
[ "$(stat -c '%u:%a' "$TMP")" = 0:777 ]
printf 'PASS host_parent_permissions_unchanged\n'
