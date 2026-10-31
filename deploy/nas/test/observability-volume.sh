#!/bin/sh
# Real locked-image regression. Only a local Unix Docker endpoint is accepted.
set -eu
: "${AURIGA_IMAGE_ALLOY:?set the independently verified local image id or digest}"
endpoint=${DOCKER_HOST:-$(docker context inspect --format '{{.Endpoints.docker.Host}}')}
case "$endpoint" in unix://*) ;; *) echo 'local Unix Docker endpoint required' >&2; exit 1 ;; esac
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$AURIGA_IMAGE_ALLOY")" = linux/amd64 ]
token="$(date +%s)-$$"
volume="auriga-nas-observe-test-$token"
cleanup() {
  if [ "$(docker volume inspect --format '{{index .Labels "auriga.observability-test"}}' "$volume" 2>/dev/null || :)" = "$token" ]; then
    docker volume rm "$volume" >/dev/null
  fi
}
trap cleanup EXIT HUP INT TERM
docker volume create --label "auriga.observability-test=$token" "$volume" >/dev/null
run() {
  docker run --rm --network none --read-only --cap-drop ALL \
    --security-opt no-new-privileges --memory 64m --cpus 0.5 --pids-limit 32 \
    --user 0:0 --mount "type=volume,source=$volume,target=/var/lib/alloy" "$@"
}
# Reproduce an image-owned volume that a capless UID0 cannot write.
run --cap-add CHOWN --entrypoint /bin/sh "$AURIGA_IMAGE_ALLOY" -ec 'chown 0:0 /var/lib/alloy; chmod 0755 /var/lib/alloy; chown 473:473 /var/lib/alloy'
if run --entrypoint /bin/sh "$AURIGA_IMAGE_ALLOY" -ec 'mkdir /var/lib/alloy/remotecfg' >/dev/null 2>&1; then
  echo 'fixture failed to reproduce capless volume ownership boundary' >&2; exit 1
fi
# Exact public alloy-prepare command; no recursive mutation or extra capability.
run --cap-add CHOWN --entrypoint /bin/sh "$AURIGA_IMAGE_ALLOY" -ec 'chown 0:0 /var/lib/alloy'
run --entrypoint /bin/sh "$AURIGA_IMAGE_ALLOY" -ec 'mkdir /var/lib/alloy/remotecfg; echo ready > /var/lib/alloy/remotecfg/state; test -s /var/lib/alloy/remotecfg/state'
printf 'PASS capless Alloy writes its prepared state volume\n'
