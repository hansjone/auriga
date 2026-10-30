#!/bin/sh
# Optional single collab service + disposable PostgreSQL fixture. This proves
# signed publication and DB roles, never plugin READY or cloud/desktop delivery.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
DATA=$1
IMAGE=$2
PROJECT=auriga-nas-collab-probe-$$
. "$ROOT/scripts/lib.sh"
export AURIGA_DATA_ROOT="$DATA"
load_stack_env
export AURIGA_DATA_ROOT="$DATA" AURIGA_IMAGE_PLATFORM="$IMAGE" AURIGA_IMAGE_EDGE="$IMAGE" AURIGA_IMAGE_AGENT="$IMAGE"
export AURIGA_IMAGE_POSTGRES=${AURIGA_NAS_COLLAB_POSTGRES_IMAGE:-postgres:17}
export AURIGA_IMAGE_NATS="$IMAGE" AURIGA_IMAGE_REGISTRY="$IMAGE" AURIGA_IMAGE_LITELLM="$IMAGE"
export AURIGA_INTERNAL_SUBNET=192.0.2.128/26 AURIGA_INTERNAL_EDGE_IP=192.0.2.138 AURIGA_EDGE_LAN_IP=192.0.2.10 AURIGA_LAN_NETWORK=unused-probe-lan
export AURIGA_HOST_PUBLISH_NODECONTROL=17443 AURIGA_HOST_PUBLISH_PLATFORM_HTTP=18081 AURIGA_HOST_PUBLISH_REGISTRY=15000
TMP=$(mktemp -d "$DATA/results/collab-probe.XXXXXX")
chmod 0700 "$TMP"
cat >"$TMP/override.yml" <<EOF
networks:
  internal:
    name: $PROJECT
  publish:
    name: $PROJECT-publish
EOF
compose() { docker compose -f "$ROOT/docker-compose.yml" -f "$ROOT/collab-compose.yml" -f "$TMP/override.yml" -p "$PROJECT" "$@"; }
cleanup() {
  _exit=$?
  trap - EXIT
  for _cid in "${COLLAB:-}" "${JWKS:-}" "${PG:-}"; do
    [ -n "$_cid" ] || continue
    if [ "$_exit" != 0 ]; then docker logs --tail 80 "$_cid" >&2 || :; fi
    docker rm -f -v "$_cid" >/dev/null
  done
  # This exact unique project owns the network; no shared project is touched.
  for _network in "$PROJECT" "$PROJECT-publish"; do
    if [ "$(docker network inspect -f '{{index .Labels "com.docker.compose.project"}}' "$_network" 2>/dev/null || :)" = "$PROJECT" ]; then docker network rm "$_network" >/dev/null; fi
  done
  exit "$_exit"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
compose config --format json >"$TMP/compose.json"
python3 - "$TMP/compose.json" <<'PY'
import json,sys
s=json.load(open(sys.argv[1]))['services']
assert s['collab']['user']=='10002:10002'
assert not s['collab'].get('ports')
assert s['collab']['depends_on']['collab-db']['condition']=='service_completed_successfully'
assert '65534' in s['platform']['group_add']
assert '/etc/auriga/network' in [v['target'] for v in s['platform']['volumes']]
print('PASS nas_collab_compose_model')
PY
# A non-authorizing fixture satisfies only the entrypoint's file-exists check.
# No IAM, admission, READY, or request forwarding assertion is made here.
mkdir "$DATA/collab-tokens/collab"
printf '%s' publication-only-fixture >"$DATA/collab-tokens/collab/service.jwt"
chown -R 10001:65534 "$DATA/collab-tokens/collab"
chmod 0750 "$DATA/collab-tokens/collab"
chmod 0640 "$DATA/collab-tokens/collab/service.jwt"
PG=$(compose run -d --no-deps --use-aliases --pull never postgres)
_deadline=$(($(date +%s)+90))
until docker exec "$PG" pg_isready -U auriga_bootstrap -d auriga >/dev/null 2>&1; do
  [ "$(date +%s)" -lt "$_deadline" ] || exit 1
  sleep 1
done
compose run --rm --no-deps --pull never collab-db
compose run --rm --no-deps --pull never collab-db
mkdir "$TMP/jwks"
chown 10001:10001 "$TMP/jwks"
JWKS=$(docker run -d --pull never --network "$PROJECT" --network-alias platform \
  --user 10001:10001 --cap-drop ALL --security-opt no-new-privileges:true --read-only \
  --memory 64m --cpus 0.25 --pids-limit 32 \
  --env "ISSUER=$AURIGA_NAS_ISSUER" --env TENANT=00000000-0000-0000-0000-000000000001 \
  --mount "type=bind,src=$ROOT/test/collab-jwks-fixture.mjs,dst=/fixture.mjs,readonly" \
  --mount "type=bind,src=$DATA/config/materials/pki,dst=/pki,readonly" \
  --mount "type=bind,src=$TMP/jwks,dst=/fixture" \
  "${AURIGA_NAS_COLLAB_NODE_IMAGE:-node:22-bookworm}" node /fixture.mjs)
_deadline=$(($(date +%s)+20))
until [ -s "$TMP/jwks/request.config" ]; do [ "$(date +%s)" -lt "$_deadline" ] || exit 1; sleep 1; done
COLLAB=$(compose run -d --no-deps --pull never collab)
_deadline=$(($(date +%s)+120))
until docker exec -i "$COLLAB" curl --config - --fail --silent --show-error --noproxy '*' \
  --cacert /etc/auriga/collab/ca.crt --cert /etc/auriga/collab/client.crt --key /etc/auriga/collab/client.key \
  https://127.0.0.1:8085/api/collab/v1/network/keys <"$TMP/jwks/request.config" >"$TMP/keys.json" 2>"$TMP/probe.log"; do
  [ "$(date +%s)" -lt "$_deadline" ] || exit 1
  sleep 1
done
python3 - "$TMP/keys.json" "$DATA/config/collab-source/collab-installation/signed.json" <<'PY'
import json,sys
assert json.load(open(sys.argv[1]))==json.load(open(sys.argv[2]))
print('PASS nas_collab_keys_200_exact_signed_publication')
PY
STATUS=$(docker exec "$COLLAB" curl --silent --show-error --noproxy '*' --output /dev/null --write-out '%{http_code}' \
  --cacert /etc/auriga/collab/ca.crt --cert /etc/auriga/collab/client.crt --key /etc/auriga/collab/client.key \
  https://127.0.0.1:8085/api/collab/v1/network/keys)
[ "$STATUS" = 401 ]
STATUS=$(docker exec -i "$COLLAB" curl --config - --silent --show-error --noproxy '*' --output /dev/null --write-out '%{http_code}' \
  --cacert /etc/auriga/collab/ca.crt --cert /etc/auriga/collab/client.crt --key /etc/auriga/collab/client.key \
  https://127.0.0.1:8085/api/collab/v1/network/keys <"$TMP/jwks/request-other.config")
[ "$STATUS" = 403 ]
printf 'PASS nas_collab_keys_unauthenticated_401_cross_tenant_403\n'
