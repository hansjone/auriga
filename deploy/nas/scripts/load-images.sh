#!/bin/sh
# Load fixed CI image archives without creating/claiming a deployment data root.
# Source identity stays in the release marker; suggestions use verified local IDs.
# shellcheck shell=sh
set -eu

. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"

case "${1:-}" in
  --help)
    echo 'Usage: sh load-images.sh /path/to/bundle-dir'
    echo 'Requires the checksummed nas-bundle artifact with config_id metadata and prebuilt bootstrap.'
    echo 'Prints verified target-Engine AURIGA_IMAGE_* suggestions after all three loads succeed.'
    echo 'Does not edit stack.env or STACK_ENV, or create/claim AURIGA_DATA_ROOT.'
    exit 0 ;;
esac
[ "$#" -eq 1 ] || die "one bundle directory required"
BUNDLE=$1
[ -d "$BUNDLE" ] && [ ! -L "$BUNDLE" ] || die "not a regular bundle directory: $BUNDLE"
# Make load arguments absolute without resolving away symlinks before validation.
case "$BUNDLE" in /*) ;; *) BUNDLE="$(pwd -P)/$BUNDLE" ;; esac
require_cmd docker

# Image operations use only the explicit binary or the public bundle's binary.
# In particular, never discover tools through an installation/data directory.
_boot=${AURIGA_NAS_BOOTSTRAP_BINARY:-$BUNDLE/deploy-nas/bin/auriga-nas-bootstrap}
[ -f "$_boot" ] && [ -x "$_boot" ] && [ ! -L "$_boot" ] \
  || die "prebuilt auriga-nas-bootstrap required; verify bundle checksums and restore its executable bit"
"$_boot" image-check "$BUNDLE"

for _image in platform edge agent; do
  info "docker load: $_image.tar" >&2
  docker load -i "$BUNDLE/images/$_image.tar" >&2
done

# Capture the complete result. No partial suggestions or marker on identity,
# platform, export, checksum or child-process failure.
_resolved=$("$_boot" image-resolve "$BUNDLE")

AURIGA_RELEASE_DIR=${AURIGA_RELEASE_DIR:-$BUNDLE}
export AURIGA_RELEASE_DIR
[ -d "$AURIGA_RELEASE_DIR" ] && [ ! -L "$AURIGA_RELEASE_DIR" ] || die "release directory must already exist and not be a symlink"
_marker=$(release_marker_path)
_versions="$AURIGA_RELEASE_DIR/versions.txt"
regular_or_absent() {
  [ ! -L "$1" ] && { [ ! -e "$1" ] || [ -f "$1" ]; } \
    || die "release metadata must be a regular file or absent: $1"
}
regular_or_absent "$_marker"
regular_or_absent "$_versions"

# Prepare both files before publishing either. Never open a final inode for
# writing: a metadata path may be a hard link to an operator's env file.
_marker_tmp=
_versions_tmp=
cleanup_metadata() {
  [ -z "$_marker_tmp" ] || rm -f "$_marker_tmp"
  [ -z "$_versions_tmp" ] || rm -f "$_versions_tmp"
}
trap cleanup_metadata 0
trap 'exit 1' HUP INT TERM
_marker_tmp=$(mktemp "$AURIGA_RELEASE_DIR/.auriga-marker.XXXXXXXX")
_versions_tmp=$(mktemp "$AURIGA_RELEASE_DIR/.auriga-versions.XXXXXXXX")
cp "$BUNDLE/images/manifest.json" "$_marker_tmp"
cp "$BUNDLE/versions.txt" "$_versions_tmp"
chmod 0644 "$_marker_tmp"
chmod 0644 "$_versions_tmp"
# Recheck before rename; mv must never interpret a directory as a container.
regular_or_absent "$_versions"
regular_or_absent "$_marker"
# Keep default release and independent paths sharing the source versions inode.
if [ ! "$BUNDLE/versions.txt" -ef "$_versions" ]; then
  mv -f "$_versions_tmp" "$_versions"
  _versions_tmp=
fi
# The marker is the last commit point, after every other preparation succeeds.
mv -f "$_marker_tmp" "$_marker"
_marker_tmp=

printf '%s\n' "$_resolved"
info "images verified; paste these target-Engine local IDs into stack.env before start" >&2
