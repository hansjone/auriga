#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
mkdir -p "$TMP/bin" "$TMP/bundle/images"
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
[ "$1" = load ] || exit 1
printf '%s\n' "$*" >>"$NAS_LOAD_TEST_LOG"
EOF
chmod 0755 "$TMP/bin/docker"
printf 'test-image\n' >"$TMP/bundle/images/platform.tar"
(cd "$TMP/bundle/images" && sha256sum platform.tar >SHA256SUMS)
printf '{"images":{}}\n' >"$TMP/bundle/images/manifest.json"
printf 'test-version\n' >"$TMP/bundle/versions.txt"
export PATH="$TMP/bin:$PATH" NAS_LOAD_TEST_LOG="$TMP/docker.log"
export AURIGA_DATA_ROOT="$TMP/must-not-create-data"
unset AURIGA_RELEASE_DIR
sh "$ROOT/scripts/load-images.sh" "$TMP/bundle" >"$TMP/load.log"
cmp "$TMP/bundle/images/manifest.json" "$TMP/bundle/.auriga-release"
grep -q '^load -i ' "$TMP/docker.log"
[ ! -e "$AURIGA_DATA_ROOT" ]
printf 'PASS load_images_default_release_with_versions\n'
# An explicitly separate release directory still receives both metadata files.
mkdir "$TMP/release"
AURIGA_RELEASE_DIR="$TMP/release" sh "$ROOT/scripts/load-images.sh" "$TMP/bundle" >"$TMP/load-separate.log"
cmp "$TMP/bundle/versions.txt" "$TMP/release/versions.txt"
cmp "$TMP/bundle/images/manifest.json" "$TMP/release/.auriga-release"
printf 'PASS load_images_separate_release_with_versions\n'
