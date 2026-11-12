#!/bin/sh
# Run after service switch and before materials commit. The old relay remains read-only.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "$#" = 0 ] || die 'Usage: sh migrate-relay.sh'
load_stack_env
[ -f "$(marker_path)" ] || die 'not initialized'
if [ ! -f "$AURIGA_DATA_ROOT/config/materials/litellm-runtime.json" ]; then
  info 'relay_migration=not_required'
  exit 0
fi
image=${AURIGA_IMAGE_LITELLM:?retain the original verified LiteLLM image for its decryptor}
validate_image_ref "$image" || die 'invalid source helper image'
# Fixed leaf mounts let the old release decrypt its own DB without exporting plaintext.
docker run --rm --pull never --network auriga-nas-test_internal --user 10001:10001 \
  --cap-drop ALL --security-opt no-new-privileges:true --read-only --memory 512m --pids-limit 64 \
  --mount "type=bind,src=$NAS_ROOT/scripts/migrate-relay.py,dst=/migrate.py,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/config/materials/access.json,dst=/access.json,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/config/materials/litellm/master,dst=/old-master,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/config/materials/litellm/salt,dst=/old-salt,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/config/materials/litellm/pg_password,dst=/old-pg,readonly" \
  --mount "type=bind,src=$AURIGA_DATA_ROOT/config/bifrost/admin_password,dst=/new-admin,readonly" \
  --tmpfs /tmp:rw,noexec,nosuid,size=32m --env PYTHONDONTWRITEBYTECODE=1 \
  --entrypoint /app/.venv/bin/python "$image" /migrate.py
