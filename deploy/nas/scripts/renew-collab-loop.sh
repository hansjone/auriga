#!/bin/sh
# No root key or external network: publish only explicitly approved schedules.
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
