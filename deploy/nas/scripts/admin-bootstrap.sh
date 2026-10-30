#!/bin/sh
# First-admin setup uses the real edge loopback. Stdout is recipient ciphertext.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 1 ] || die 'Usage: sh admin-bootstrap.sh /canonical/recipient-public.pem'
load_stack_env >&2
require_cmd docker readlink stat
[ -f "$(marker_path)" ] || die 'not initialized'
recipient=$1
case "$recipient" in /*) ;; *) die 'recipient key must be absolute' ;; esac
case "$recipient" in *','*|*':'*|*'\'*|*'
'*) die 'unsupported recipient mount path' ;; esac
[ -f "$recipient" ] && [ ! -L "$recipient" ] && [ "$(readlink -f -- "$recipient")" = "$recipient" ] \
  && [ "$(stat -c '%h' "$recipient")" = 1 ] || die 'recipient public key must be a canonical single-link file'
if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  sh "$NAS_ROOT/scripts/installer.sh" check >&2
else
  bootstrap_nas_materials >&2
fi
image=${AURIGA_IMAGE_LITELLM:-ghcr.io/berriai/litellm:v1.106.6@sha256:37e2dafb088a7723beb998590682957386dfc83ffea1848486f8e0e5cfac0b06}
validate_image_ref "$image" || die 'invalid bootstrap helper image'
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64 ] \
  || die 'approved local helper image missing or wrong platform'
edge=$(docker ps -q --no-trunc --filter "label=com.docker.compose.project=$AURIGA_NAS_PROJECT" --filter label=com.docker.compose.service=edge)
case "$edge" in ''|*[!0-9a-f]*) die 'expected exactly one running project edge' ;; esac
[ "${#edge}" = 64 ] || die 'expected full edge container identity'
enrollment="$AURIGA_DATA_ROOT/enrollment/emergency-enrollment.json"
[ -f "$enrollment" ] && [ ! -L "$enrollment" ] || die 'emergency enrollment missing'
set -- --mount "type=bind,src=$NAS_ROOT/scripts/admin-bootstrap.py,dst=/bootstrap.py,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/config/materials/access.json,dst=/access.json,readonly" \
  --mount "type=bind,src=$enrollment,dst=/enrollment.json,readonly" \
  --mount "type=bind,src=$recipient,dst=/recipient.pem,readonly"
if [ -f "$AURIGA_DATA_ROOT/config/materials/browser-tls/browser.crt" ]; then
  set -- "$@" --mount "type=bind,src=$AURIGA_DATA_ROOT/config/materials/browser-tls/browser.crt,dst=/browser-ca.pem,readonly"
  tls_args='--tls-ca /browser-ca.pem'
else tls_args=''; fi
# No published port, Docker socket, host network, private recipient key, writable
# mount or forwarded client-identity header is exposed to this helper.
# shellcheck disable=SC2086
docker run --rm --pull never --network "container:$edge" --user 10001:10001 \
  --cap-drop ALL --security-opt no-new-privileges:true --read-only \
  --pids-limit 64 --memory 128m --cpus 0.5 "$@" \
  --env PYTHONDONTWRITEBYTECODE=1 --entrypoint python "$image" /bootstrap.py \
  --access /access.json --enrollment /enrollment.json --recipient /recipient.pem $tls_args
