#!/bin/sh
# Operator terminal only. Never run the handoff in Compose or with Docker logging.
# shellcheck shell=sh
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 1 ] || die 'Usage: sh emergency.sh show|rotate'
case "$1" in
  show|--first-start) operation=$1 ;;
  rotate) die 'emergency rotate unavailable: the public rotate API is declared but not implemented; no credentials changed' ;;
  *) die 'Usage: sh emergency.sh show|rotate' ;;
esac
# Refuse before reading configuration, enrollment, or even contacting Docker.
[ -t 1 ] || die 'emergency credentials require stdout to be a terminal; run sh deploy-nas/scripts/emergency.sh show in a private TTY (do not pipe or redirect)'
load_stack_env >&2
require_cmd docker readlink
[ -f "$(marker_path)" ] || die 'not initialized; run init.sh first'
docker info >/dev/null 2>&1 || die 'root or local Docker permission is required'
# Only canonical fixed inputs, never a broad data-root/socket mount. In container
# install mode the operator need not traverse uid10001 private directories.
for input in "$NAS_ROOT" "$AURIGA_DATA_ROOT"; do
  case "$input" in *','*|*':'*|*'\'*|*'
'*) die 'unsupported emergency helper mount path' ;; esac
  [ "$(readlink -f -- "$input")" = "$input" ] || die 'emergency helper paths must be canonical'
done
image=${AURIGA_IMAGE_LITELLM:-ghcr.io/berriai/litellm:v1.106.6@sha256:37e2dafb088a7723beb998590682957386dfc83ffea1848486f8e0e5cfac0b06}
validate_image_ref "$image" || die 'invalid emergency helper image'
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64 ] \
  || die 'approved local helper image missing or wrong platform'
set --
[ "$operation" != --first-start ] || set -- --once
# Enrollment must remain writable for the receipt; the credential leaf is mounted
# again read-only. No plaintext copy is made and the daemon log driver is disabled.
docker run --rm --pull never --log-driver none --tty --network none --user 10001:10001 \
  --cap-drop ALL --security-opt no-new-privileges:true --read-only \
  --pids-limit 32 --memory 64m --cpus 0.5 \
  --mount "type=bind,src=$NAS_ROOT/scripts/emergency.py,dst=/emergency.py,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/config/materials/access.json,dst=/access.json,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/enrollment,dst=/enrollment" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/enrollment/emergency-enrollment.json,dst=/enrollment/emergency-enrollment.json,readonly" \
  --env PYTHONDONTWRITEBYTECODE=1 --env PYTHONIOENCODING=utf-8 \
  --entrypoint python "$image" /emergency.py "$@"
