#!/bin/sh
# Read-only checks of runtime bind sources; never repairs permissions or pulls.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'Usage: sh mount-access.sh'
load_stack_env
require_cmd docker
[ -f "$(marker_path)" ] || die 'mount access check requires an initialized installation'
case "$AURIGA_DATA_ROOT$NAS_ROOT" in *','*|*':'*|*'\'*|*'
'*) die 'unsupported mount access probe path' ;; esac
validate_image_ref "${AURIGA_IMAGE_PLATFORM:?}" || die 'bad mount access probe image ref'
case "${AURIGA_IMAGE_PLATFORM##*sha256:}" in *[!0]*) ;; *) die 'placeholder mount access probe image' ;; esac
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$AURIGA_IMAGE_PLATFORM")" = linux/amd64 ] \
  || die 'mount access probe requires the configured local linux/amd64 platform image'

# The platform image supplies sh/cat (edge is distroless). Numeric identities,
# supplementary groups and source mount boundaries match docker-compose.yml.
# Do not mount the whole data root: its host-only ancestors are not runtime binds.
probe() (
  service=$1; identity=$2; group=$3; shift 3
  specs=$*
  set -- --mount "type=bind,src=$NAS_ROOT/scripts/mount-access-entry.sh,dst=/probe-entry.sh,readonly"
  [ "$group" = none ] || set -- "$@" --group-add "$group"
  for spec in $specs; do
    category=${spec%%:*}; rest=${spec#*:}; source=${rest#*:}
    set -- "$@" --mount "type=bind,src=$AURIGA_DATA_ROOT/$source,dst=/probe/$category,readonly"
  done
  # No entrypoint, environment, socket, port, extra capability or writable bind
  # from the service is inherited. Missing bind sources are never created.
  if ! docker run --rm --pull never --network none --user "$identity" \
    --cap-drop ALL --security-opt no-new-privileges:true --read-only \
    --pids-limit 32 --memory 64m --cpus 0.25 "$@" \
    --entrypoint /bin/sh "$AURIGA_IMAGE_PLATFORM" /probe-entry.sh "$service" "$identity" "$group" $specs; then
    die "mount-access FAILED service=$service uid:gid=$identity supplementary=$group path-category=service-mounts (see category above; verify local probe image sh/cat and bind access)"
  fi
)

# Existing bootstrap already validates source metadata/trust and reads runtime
# inputs as uid10001 with no supplementary groups. These are runtime access
# checks only, including the separately owned edge copies and NATS config.
probe platform 10001:10001 65532 \
  access:public:config/materials/access.json platform-config:public:config/materials/platform-access.yaml \
  healthcheck:public:config/materials/iam-healthcheck.sh installation:installation:config/materials/iam-installation \
  pki:platform-pki:config/materials/pki templates:tree:config/materials/templates \
  runtime-keys:tree:config/materials/runtime-keys tokens:tokens:internal-tokens enrollment:dir:enrollment
probe edge 65532:65532 none \
  access:public:config/materials/access.json edge-config:public:config/materials/edge.config.json \
  identity-signing:secret:config/pki-edge/identity-signing-keys.json pki:edge-pki:config/pki-edge \
  tokens:tokens:internal-tokens ingress:dir:socket-share
probe nats 65532:65532 none nats-config:secret:config/nats/nats.conf data:dir:data/nats
# PostgreSQL entrypoint reads the bootstrap password as root; init SQL and data
# are consumed after its normal drop to uid999. Do not ask uid999 to read secrets/.
probe postgres-entrypoint 0:0 none password:secret:secrets/pg_bootstrap_password
probe postgres 999:999 none init-sql:secret:config/postgres/init.sql data:dir:data/postgres
probe agent 0:0 none \
  access:public:config/materials/access.json identity-public:public:config/materials/identity-keys.json \
  policy:public:config/materials/agent-policy.yaml proofs:tree:config/materials/runtime-proofs \
  join:join:join data:dir:data/agent ingress:dir:socket-share
# Registry has no Compose user override. Refuse an unexpected image identity
# instead of silently checking a different uid (the packaged registry runs root).
registry_user=$(docker image inspect --format '{{.Config.User}}' "${AURIGA_IMAGE_REGISTRY:?}")
case "$registry_user" in ''|0|0:0|root|root:root) ;; *) die 'mount-access registry image identity differs from packaged root identity' ;; esac
probe registry 0:0 none data:dir:data/registry
info 'SERVICE-MOUNT-ACCESS-PASSED (read/traverse only; no write or ACL repair)'
