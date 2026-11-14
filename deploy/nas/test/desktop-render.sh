#!/bin/sh
# Reuse the isolated full render fixture; never touches a real deployment.
set -eu
[ "$(id -u)" = 0 ] || { echo "FAIL desktop_render_requires_root" >&2; exit 1; }
root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
. "$root/test/device-presence.sh"
rm "$temp/data/config/materials/network/edge-presence.key"
mv "$temp/key" "$temp/data/config/materials/network/edge-presence.key"
! grep -q '^AURIGA_RESOURCE_DESKTOP_' "$temp/data/config/platform.env"
export AURIGA_DESKTOP_PUBLISHER_TENANT_ID=00000000-0000-0000-0000-000000000001
if sh "$temp/scripts/render.sh" >"$temp/desktop.log" 2>&1; then exit 1; fi
grep -q 'provision Desktop release public key' "$temp/desktop.log"
openssl genpkey -algorithm Ed25519 -out "$temp/key.pem" 2>/dev/null
openssl pkey -in "$temp/key.pem" -pubout -out "$temp/data/config/desktop/release-public.pem" 2>/dev/null
sh "$temp/scripts/render.sh" >"$temp/desktop.log" 2>&1 || { cat "$temp/desktop.log"; exit 1; }
grep -qx 'AURIGA_RESOURCE_DESKTOP_DIRECTORY=/var/lib/auriga/desktop' "$temp/data/config/platform.env"
grep -qx 'AURIGA_RESOURCE_DESKTOP_PUBLIC_KEY_FILE=/etc/auriga/desktop/release-public.pem' "$temp/data/config/platform.env"
grep -qx "AURIGA_RESOURCE_DESKTOP_PUBLISHER_TENANT_ID=$AURIGA_DESKTOP_PUBLISHER_TENANT_ID" "$temp/data/config/platform.env"
[ "$(stat -c '%u:%g:%a' "$temp/data/desktop")" = '10001:10001:700' ]
# All pre-existing fixture secrets remain provisioned; only Desktop needs OpenSSL.
mkdir "$temp/no-openssl"
for tool in sh dirname stat sed awk tr cmp grep mkdir chmod chown rm cp mv mktemp id cat readlink ln find sort; do
  ln -s "$(command -v "$tool")" "$temp/no-openssl/$tool"
done
if PATH="$temp/no-openssl" /bin/sh "$temp/scripts/render.sh" >"$temp/no-openssl.log" 2>&1; then exit 1; fi
grep -qx 'missing required command: openssl' "$temp/no-openssl.log"
echo 'PASS Desktop missing openssl reports the missing command'
export AURIGA_DESKTOP_PUBLISHER_TENANT_ID=invalid
if sh "$temp/scripts/render.sh" >"$temp/invalid-tenant.log" 2>&1; then exit 1; fi
grep -q 'invalid Desktop publisher tenant UUID' "$temp/invalid-tenant.log"
echo 'PASS Desktop disabled, missing key refused, provisioned env and ownership, invalid tenant refused'
