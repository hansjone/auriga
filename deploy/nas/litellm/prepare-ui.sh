#!/bin/sh
set -eu
# Fixed vendor Python; no host package installation, no network, no master mount.
root=$1
[ ! -L "$root" ] || { echo 'invalid UI secret directory' >&2; exit 1; }
mkdir -p "$root"
root=$(cd "$root" && pwd -P)
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
image=${AURIGA_IMAGE_LITELLM:-ghcr.io/berriai/litellm:v1.106.6@sha256:37e2dafb088a7723beb998590682957386dfc83ffea1848486f8e0e5cfac0b06}
docker run --rm --pull never --network none --cap-drop ALL --cap-add DAC_OVERRIDE --cap-add CHOWN --cap-add FOWNER \
  --security-opt no-new-privileges:true --user 0:0 \
  -v "$root:/ui" -v "$here/prepare-ui.py:/prepare-ui.py:ro" \
  --entrypoint python "$image" /prepare-ui.py /ui
