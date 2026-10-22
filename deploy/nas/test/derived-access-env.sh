#!/bin/sh
# Ordinary malformed-data rejection tests; no executable values or script probes.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
DATA=$1
TMP=$(mktemp -d)
chmod 0755 "$TMP"
trap 'rm -rf "$TMP"' EXIT INT TERM
export NAS_ROOT="$ROOT" AURIGA_DATA_ROOT="$DATA"
. "$ROOT/scripts/lib.sh"

SOURCE="$DATA/config/materials/access.env"
cp "$SOURCE" "$TMP/valid.env"
load_nas_access_env "$SOURCE"
[ "$AURIGA_NAS_ISSUER" != "" ] && [ "$AURIGA_NAS_PORTAL_HOST" != "" ]
printf 'PASS generated_derived_access_env_%s\n' "${NAS_TEST_ACCESS_MODE:-domain}"

snapshot() {
  (
    CDPATH= cd -P -- "$DATA"
    find . -printf '%p %y %u %g %m %n %s %i %T@ %C@\n' | LC_ALL=C sort
    find . -type f -exec sha256sum {} + | LC_ALL=C sort
  ) | sha256sum | awk '{print $1}'
}

no_partial_import() {
  [ "$AURIGA_NAS_ISSUER" = unchanged ] \
    && [ "$AURIGA_NAS_PORTAL_HOST" = unchanged ] \
    && [ "$AURIGA_NAS_ADMIN_HOST" = unchanged ] \
    && [ "$AURIGA_NAS_PORTAL_URL" = unchanged ] \
    && [ "$AURIGA_NAS_ADMIN_URL" = unchanged ] \
    && [ "$AURIGA_NAS_PORTAL_PORT" = unchanged ] \
    && [ "$AURIGA_NAS_ADMIN_PORT" = unchanged ] \
    && [ "$AURIGA_NAS_PORTAL_TARGET_PORT" = unchanged ] \
    && [ "$AURIGA_NAS_ADMIN_TARGET_PORT" = unchanged ] \
    && printf 'NO_PARTIAL_IMPORT\n'
}

for kind in unknown unknown-instead-of-field duplicate duplicate-instead-of-field missing empty bad-host bad-origin bad-port; do
  case "$kind" in
    unknown) { cat "$TMP/valid.env"; printf 'AURIGA_NAS_UNUSED_FIELD=ordinary\n'; } >"$TMP/bad.env" ;;
    unknown-instead-of-field) sed 's/^AURIGA_NAS_ADMIN_TARGET_PORT=.*/AURIGA_NAS_UNUSED_FIELD=ordinary/' "$TMP/valid.env" >"$TMP/bad.env" ;;
    duplicate) { cat "$TMP/valid.env"; sed -n '1p' "$TMP/valid.env"; } >"$TMP/bad.env" ;;
    duplicate-instead-of-field) sed '/^AURIGA_NAS_ADMIN_TARGET_PORT=/d' "$TMP/valid.env" >"$TMP/bad.env"; sed -n '1p' "$TMP/valid.env" >>"$TMP/bad.env" ;;
    missing) sed '/^AURIGA_NAS_ADMIN_TARGET_PORT=/d' "$TMP/valid.env" >"$TMP/bad.env" ;;
    empty) sed 's/^AURIGA_NAS_ADMIN_HOST=.*/AURIGA_NAS_ADMIN_HOST=/' "$TMP/valid.env" >"$TMP/bad.env" ;;
    bad-host) sed 's/^AURIGA_NAS_ADMIN_HOST=.*/AURIGA_NAS_ADMIN_HOST=ordinary invalid/' "$TMP/valid.env" >"$TMP/bad.env" ;;
    bad-origin) sed 's|^AURIGA_NAS_ADMIN_URL=.*|AURIGA_NAS_ADMIN_URL=https://admin.example.com/ordinary|' "$TMP/valid.env" >"$TMP/bad.env" ;;
    bad-port) sed 's/^AURIGA_NAS_ADMIN_TARGET_PORT=.*/AURIGA_NAS_ADMIN_TARGET_PORT=65536/' "$TMP/valid.env" >"$TMP/bad.env" ;;
  esac
  if (
    for field in AURIGA_NAS_ISSUER AURIGA_NAS_PORTAL_HOST AURIGA_NAS_ADMIN_HOST AURIGA_NAS_PORTAL_URL AURIGA_NAS_ADMIN_URL AURIGA_NAS_PORTAL_PORT AURIGA_NAS_ADMIN_PORT AURIGA_NAS_PORTAL_TARGET_PORT AURIGA_NAS_ADMIN_TARGET_PORT; do
      export "$field=unchanged"
    done
    trap no_partial_import EXIT
    load_nas_access_env "$TMP/bad.env"
  ) >"$TMP/parse.log" 2>&1; then
    echo "malformed derived environment accepted: $kind" >&2
    exit 1
  fi
  grep -q '^invalid derived access environment$' "$TMP/parse.log"
  grep -q '^NO_PARTIAL_IMPORT$' "$TMP/parse.log"

  # Use an installed disposable test tree. Reject before any configuration or
  # material write; hashes also cover owner/mode/link count/inode/timestamps.
  cat "$TMP/bad.env" >"$SOURCE"
  before=$(snapshot)
  for script in init render; do
    if sh "$ROOT/scripts/$script.sh" >"$TMP/$script.log" 2>&1; then
      echo "installed malformed derived environment accepted: $kind/$script" >&2
      exit 1
    fi
    grep -q '^invalid derived access environment$' "$TMP/$script.log"
    [ "$before" = "$(snapshot)" ] || { echo "rejection changed installed tree: $kind/$script" >&2; exit 1; }
  done
  cat "$TMP/valid.env" >"$SOURCE"
  printf 'PASS derived_access_env_reject_%s_before_import_and_writes\n' "$kind"
done

# A structurally valid record is still not an authenticated receipt. The
# unchanged bootstrap must reject its altered bytes independently afterwards.
sed 's/^AURIGA_NAS_ADMIN_TARGET_PORT=.*/AURIGA_NAS_ADMIN_TARGET_PORT=65534/' "$TMP/valid.env" >"$SOURCE"
(load_nas_access_env "$SOURCE")
before=$(snapshot)
for script in init render; do
  if sh "$ROOT/scripts/$script.sh" >"$TMP/receipt-$script.log" 2>&1; then
    echo "valid-format altered receipt material accepted: $script" >&2
    exit 1
  fi
  grep -q 'installation inputs or identity materials changed; refusing automatic rotation' "$TMP/receipt-$script.log"
  [ "$before" = "$(snapshot)" ] || { echo "receipt rejection changed installed tree: $script" >&2; exit 1; }
done
cat "$TMP/valid.env" >"$SOURCE"
printf 'PASS derived_access_env_valid_format_still_requires_receipt\n'
sh "$ROOT/scripts/render.sh" >"$TMP/restored.log" 2>&1

# Default HTTP port and canonical DNS A-labels use actual public bootstrap output.
# HTTPS default443 is covered by the independently generated proxy-mode suite.
if [ "${NAS_TEST_ACCESS_MODE:-domain}" = domain ]; then
  sed -e 's/8080/80/g' -e 's/portal.example.com/xn--bcher-kva.example/g' \
    "$ROOT/access-domain.example.json" >"$TMP/default-http.json"
  bootstrap=$(bootstrap_binary)
  (umask 022; "$bootstrap" -out "$TMP/default-http" -access-file "$TMP/default-http.json" \
    -edge-peer-ip 192.0.2.138 -internal-subnet 192.0.2.128/26 \
    -lan-ip 192.0.2.10 -lan-subnet 192.0.2.0/26) >/dev/null
  load_nas_access_env "$TMP/default-http/access.env"
  [ "$AURIGA_NAS_PORTAL_URL" = http://xn--bcher-kva.example ]
  [ "$AURIGA_NAS_PORTAL_PORT" = 80 ]
  printf 'PASS generated_derived_default_http_and_dns_alabel\n'
fi
