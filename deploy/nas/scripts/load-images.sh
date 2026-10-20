#!/bin/sh
# Load CI image tarballs into local Docker (NAS). Does not pull from registries by default.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

BUNDLE=${1:-}
case "${1:-}" in
  --help)
    echo 'Usage: sh load-images.sh /path/to/bundle-dir'
    echo 'Expects images/*.tar and images/manifest.json from nas-bundle workflow.'
    exit 0 ;;
esac
[ -n "$BUNDLE" ] || die "bundle directory required"
[ -d "$BUNDLE" ] || die "not a directory: $BUNDLE"
require_cmd docker sha256sum

MANIFEST="$BUNDLE/images/manifest.json"
[ -f "$MANIFEST" ] || die "missing $MANIFEST"

# Verify sha256 sums if present.
if [ -f "$BUNDLE/images/SHA256SUMS" ]; then
  (CDPATH= cd -P -- "$BUNDLE/images" && sha256sum -c SHA256SUMS)
fi

for tar in "$BUNDLE"/images/*.tar; do
  [ -f "$tar" ] || continue
  info "docker load: $(basename "$tar")"
  docker load -i "$tar"
done

load_stack_env
mkdir -p "$AURIGA_DATA_ROOT"
if [ -f "$MANIFEST" ]; then
  cp "$MANIFEST" "$(release_marker_path)"
  chmod 0644 "$(release_marker_path)"
fi
info "images loaded; update stack.env digests to match manifest before start"
