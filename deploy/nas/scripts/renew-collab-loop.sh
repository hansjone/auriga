#!/bin/sh
# No root key and no network: prepare immutable requests and report expiry only.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
while :; do
  if ! "$(bootstrap_binary)" collab-rotation renew -data "$AURIGA_DATA_ROOT" \
      -recipe "$NAS_ROOT" -image "$AURIGA_IMAGE_PLATFORM" -operator-jar-sha256 "${AURIGA_COLLAB_OPERATOR_JAR_SHA256:-}"; then
    printf '%s\n' 'NAS collab renewal check failed; operator must inspect preserved evidence' >&2
  fi
  sleep 3600
done
