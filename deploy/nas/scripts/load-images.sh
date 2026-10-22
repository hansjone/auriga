#!/bin/sh
# Load CI image tarballs into local Docker. Does not create/claim AURIGA_DATA_ROOT (M3).
# Writes release marker next to the bundle (or AURIGA_RELEASE_DIR).
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

BUNDLE=${1:-}
case "${1:-}" in
  --help)
    echo 'Usage: sh load-images.sh /path/to/bundle-dir'
    echo 'Expects images/*.tar and images/manifest.json from nas-bundle workflow.'
    echo 'Updates stack.env image lines to sha256:<id> when STACK_ENV is set.'
    exit 0 ;;
esac
[ -n "$BUNDLE" ] || die "bundle directory required"
[ -d "$BUNDLE" ] || die "not a directory: $BUNDLE"
require_cmd docker sha256sum

MANIFEST="$BUNDLE/images/manifest.json"
[ -f "$MANIFEST" ] || die "missing $MANIFEST"

if [ -f "$BUNDLE/images/SHA256SUMS" ]; then
  (CDPATH= cd -P -- "$BUNDLE/images" && sha256sum -c SHA256SUMS)
fi

for tar in "$BUNDLE"/images/*.tar; do
  [ -f "$tar" ] || continue
  info "docker load: $(basename "$tar")"
  docker load -i "$tar"
done

# Release marker beside bundle — never mkdir/claim data root.
AURIGA_RELEASE_DIR=${AURIGA_RELEASE_DIR:-$BUNDLE}
export AURIGA_RELEASE_DIR
cp "$MANIFEST" "$(release_marker_path)"
chmod 0644 "$(release_marker_path)"
if [ -f "$BUNDLE/versions.txt" ]; then
  # The default release directory is the bundle itself. Do not fail a valid
  # workflow artifact by copying versions.txt onto the same inode.
  _bundle_dir=$(CDPATH= cd -P -- "$BUNDLE" && pwd)
  _release_dir=$(CDPATH= cd -P -- "$AURIGA_RELEASE_DIR" && pwd)
  if [ "$_bundle_dir" != "$_release_dir" ]; then
    cp "$BUNDLE/versions.txt" "$AURIGA_RELEASE_DIR/versions.txt"
  fi
fi

# Emit suggested stack.env image lines (sha256:id) for operator paste (H5).
info "suggested AURIGA_IMAGE_* after load (local ids, no pull):"
if command -v python3 >/dev/null 2>&1; then
  python3 - "$MANIFEST" <<'PY'
import json, sys
m = json.load(open(sys.argv[1], encoding="utf-8"))
imgs = m.get("images") or {}
for name, meta in imgs.items():
    iid = meta.get("id") or ""
    # Docker Id is often sha256:...
    if iid.startswith("sha256:"):
        print(f"AURIGA_IMAGE_{name.upper()}={iid}")
    elif iid:
        print(f"AURIGA_IMAGE_{name.upper()}=sha256:{iid}")
PY
else
  info "(install python3 to auto-print sha256 image lines from manifest)"
fi

info "images loaded; set AURIGA_IMAGE_PLATFORM/EDGE/AGENT to sha256:<id> in stack.env before start"
