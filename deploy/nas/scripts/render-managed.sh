#!/bin/sh
# Separate TLS leaf for the isolated instance network; sealed roots stay intact.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
require_root_or_capable_chown
CONFIG="$AURIGA_DATA_ROOT/config"
directory() {
  if [ -e "$1" ] || [ -L "$1" ]; then
    [ -d "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a' "$1")" = "$2:$3:$4" ] || die 'invalid managed directory'
  else
    mkdir "$1"; chown "$2:$3" "$1"; chmod "$4" "$1"
  fi
}
directory "$CONFIG/managed-edge" 0 65532 750
directory "$CONFIG/managed-ca" 0 0 755
origin=${AURIGA_MANAGED_PLATFORM_ORIGIN:-}
[ -n "$origin" ] || exit 0
require_cmd openssl cmp
case "$origin" in https://*) ;; *) die 'managed origin requires HTTPS' ;; esac
authority=${origin#https://}
printf '%s\n' "$authority" | grep -Eq '^[a-z][a-z0-9.-]*:[1-9][0-9]{0,4}$' || die 'managed origin requires a canonical DNS alias and explicit TLS port'
host=${authority%:*}; port=${authority##*:}
[ "${AURIGA_MANAGED_PLATFORM_HOST:-}" = "$host" ] || die 'managed platform host must match the configured origin'
[ "$port" -le 65535 ] || die 'invalid managed TLS port'
case ",${AURIGA_LINKS_ALLOWED_ORIGINS:-}," in *",$origin,"*) ;; *) die 'managed origin must be explicitly allowed' ;; esac
source="$CONFIG/managed-source"
[ ! -e "$source.pending" ] && [ ! -L "$source.pending" ] || die 'interrupted managed TLS preparation; inspect pending source'
umask 077
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
if [ ! -e "$source" ] && [ ! -L "$source" ]; then
  mkdir -m 700 "$source.pending"
  printf '%s\n' "$origin" >"$source.pending/origin"
  cp "$CONFIG/materials/pki/ca.crt" "$source.pending/ca.crt"
  openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=$host" \
    -keyout "$source.pending/server.key" -out "$scratch/server.csr" >/dev/null 2>&1
  printf 'subjectAltName=DNS:%s\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\nextendedKeyUsage=serverAuth\n' "$host" >"$scratch/server.ext"
  serial=$(openssl rand -hex 16)
  openssl x509 -req -in "$scratch/server.csr" -CA "$CONFIG/materials/pki/ca.crt" \
    -CAkey "$CONFIG/materials/pki/ca.key" -set_serial "0x$serial" -days 365 \
    -extfile "$scratch/server.ext" -out "$source.pending/server.crt" >/dev/null 2>&1
  chmod 0600 "$source.pending/"*
  mv -T "$source.pending" "$source"
fi
[ -d "$source" ] && [ ! -L "$source" ] && [ "$(stat -c '%u:%g:%a' "$source")" = 0:0:700 ] || die 'invalid managed source'
for file in "$source/"* "$source/".[!.]* "$source/"..?*; do
  [ -e "$file" ] || [ -L "$file" ] || continue
  case "${file##*/}" in origin|ca.crt|server.crt|server.key) ;; *) die 'unexpected managed source file' ;; esac
  [ -f "$file" ] && [ ! -L "$file" ] && [ "$(stat -c '%u:%g:%a:%h' "$file")" = 0:0:600:1 ] || die 'invalid managed source file'
done
[ "$(cat "$source/origin")" = "$origin" ] || die 'managed origin changed; explicit TLS rotation required'
cmp -s "$source/ca.crt" "$CONFIG/materials/pki/ca.crt" || die 'managed TLS root changed'
openssl verify -CAfile "$source/ca.crt" -purpose sslserver -verify_hostname "$host" "$source/server.crt" >/dev/null
openssl pkey -in "$source/server.key" -pubout -out "$scratch/key.pub" >/dev/null 2>&1
openssl x509 -in "$source/server.crt" -pubkey -noout >"$scratch/cert.pub"
cmp -s "$scratch/key.pub" "$scratch/cert.pub" || die 'managed TLS key mismatch'
install_edge_material "$source/server.crt" "$CONFIG/managed-edge/server.crt" 644
install_edge_material "$source/server.key" "$CONFIG/managed-edge/server.key" 600
if [ -e "$CONFIG/managed-ca/ca.crt" ] || [ -L "$CONFIG/managed-ca/ca.crt" ]; then
  [ -f "$CONFIG/managed-ca/ca.crt" ] && [ ! -L "$CONFIG/managed-ca/ca.crt" ] \
    && [ "$(stat -c '%u:%g:%a:%h' "$CONFIG/managed-ca/ca.crt")" = 0:0:644:1 ] \
    && cmp -s "$source/ca.crt" "$CONFIG/managed-ca/ca.crt" || die 'managed public CA changed'
else
  cp "$source/ca.crt" "$CONFIG/managed-ca/ca.crt"; chmod 0644 "$CONFIG/managed-ca/ca.crt"
fi
echo 'managed internal TLS prepared'
