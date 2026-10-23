#!/bin/sh
# Unit tests with a fake Docker CLI, not evidence from a real Engine.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
BOOT=${AURIGA_NAS_BOOTSTRAP_BINARY:-$ROOT/bin/auriga-nas-bootstrap}
[ -x "$BOOT" ] || { echo 'prebuilt bootstrap required for loader unit tests' >&2; exit 1; }
mkdir -p "$TMP/bin"
python3 "$ROOT/test/image-fixture.py" "$TMP/bundle" "$BOOT"
printf 'must never load an unrelated tar\n' >"$TMP/bundle/images/unrelated.tar"
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$NAS_LOAD_TEST_LOG"
case "$1 $2" in
  'load -i') [ "${NAS_LOAD_FAIL:-}" != "$(basename "$3")" ]; exit ;;
  'image inspect')
    case "$5" in
      auriga/platform:nas) h=a ;;
      auriga/edge:nas) h=b ;;
      auriga/agent:nas) h=c ;;
      *) exit 92 ;;
    esac
    id=$(printf '%064d' 0 | tr 0 "$h")
    arch=amd64
    if [ "${NAS_WRONG_PLATFORM:-}" = 1 ] && [ "$h" = c ]; then arch=arm64; fi
    printf '{"Id":"sha256:%s","Os":"linux","Architecture":"%s"}\n' "$id" "$arch"
    ;;
  'image save')
    # Only immutable IDs are accepted. Tags must never be reused for save.
    case "$3" in
      sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa) name=platform ;;
      sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb) name=edge ;;
      sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc) name=agent ;;
      *) exit 93 ;;
    esac
    if [ "${NAS_WRONG_IMAGE:-}" = 1 ] && [ "$name" = agent ]; then name=edge; fi
    cat "$NAS_FIXTURE_BUNDLE/images/$name.tar"
    [ "${NAS_SAVE_FAIL:-}" != 1 ]
    ;;
  *) exit 94 ;;
esac
EOF
chmod 0755 "$TMP/bin/docker"
export PATH="$TMP/bin:$PATH" NAS_LOAD_TEST_LOG="$TMP/docker.log" NAS_FIXTURE_BUNDLE="$TMP/bundle"
export AURIGA_NAS_BOOTSTRAP_BINARY="$BOOT" AURIGA_DATA_ROOT="$TMP/must-not-create-data"
export STACK_ENV="$TMP/stack.env"
printf 'AURIGA_IMAGE_PLATFORM=operator-value\n' >"$STACK_ENV"
cp "$STACK_ENV" "$TMP/stack.before"
unset AURIGA_RELEASE_DIR
sh "$ROOT/scripts/load-images.sh" "$TMP/bundle" >"$TMP/load.log"
cmp "$TMP/bundle/images/manifest.json" "$TMP/bundle/.auriga-release"
[ "$(grep -c '^load -i ' "$TMP/docker.log")" = 3 ]
grep -qx 'AURIGA_IMAGE_PLATFORM=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' "$TMP/load.log"
[ "$(grep -c '^AURIGA_IMAGE_' "$TMP/load.log")" = 3 ]
[ ! -e "$AURIGA_DATA_ROOT" ]
cmp "$STACK_ENV" "$TMP/stack.before"
printf 'PASS load_images_default_release_with_versions_and_verified_local_ids\n'
# A separate release directory still receives both original metadata files.
mkdir "$TMP/release"
AURIGA_RELEASE_DIR="$TMP/release" sh "$ROOT/scripts/load-images.sh" "$TMP/bundle" >"$TMP/load-separate.log"
cmp "$TMP/bundle/versions.txt" "$TMP/release/versions.txt"
cmp "$TMP/bundle/images/manifest.json" "$TMP/release/.auriga-release"
printf 'PASS load_images_separate_release_with_versions\n'
# A separate path referring to the same versions inode must also succeed.
mkdir "$TMP/hardlink-release"
ln "$TMP/bundle/versions.txt" "$TMP/hardlink-release/versions.txt"
AURIGA_RELEASE_DIR="$TMP/hardlink-release" sh "$ROOT/scripts/load-images.sh" "$TMP/bundle" >"$TMP/load-hardlink.log"
printf 'PASS load_images_same_versions_inode\n'

reject_load() {
  scenario=$1
  mkdir "$TMP/reject-$scenario"
  : >"$TMP/docker.log"
  if AURIGA_RELEASE_DIR="$TMP/reject-$scenario" sh "$ROOT/scripts/load-images.sh" "$TMP/bundle" >"$TMP/reject-$scenario.log" 2>&1; then
    echo "FAIL $scenario accepted" >&2; exit 1
  fi
  [ ! -e "$TMP/reject-$scenario/.auriga-release" ]
  ! grep -q '^AURIGA_IMAGE_' "$TMP/reject-$scenario.log"
  [ ! -e "$AURIGA_DATA_ROOT" ]
  cmp "$STACK_ENV" "$TMP/stack.before"
  printf 'PASS load_images_reject_%s_without_marker_or_success_lines\n' "$scenario"
}
export NAS_WRONG_IMAGE=1
reject_load wrong-loaded-config
unset NAS_WRONG_IMAGE
# A failed retry also preserves an existing release marker byte for byte.
mkdir "$TMP/previous-release"
printf 'previous verified release\n' >"$TMP/previous-release/.auriga-release"
cp "$TMP/previous-release/.auriga-release" "$TMP/previous-marker"
if NAS_WRONG_IMAGE=1 AURIGA_RELEASE_DIR="$TMP/previous-release" sh "$ROOT/scripts/load-images.sh" "$TMP/bundle" >"$TMP/previous-failure.log" 2>&1; then exit 1; fi
cmp "$TMP/previous-marker" "$TMP/previous-release/.auriga-release"
! grep -q '^AURIGA_IMAGE_' "$TMP/previous-failure.log"
printf 'PASS load_images_failure_preserves_previous_marker\n'
export NAS_WRONG_PLATFORM=1
reject_load wrong-loaded-platform
unset NAS_WRONG_PLATFORM
export NAS_SAVE_FAIL=1
reject_load save-process-failure
unset NAS_SAVE_FAIL
export NAS_LOAD_FAIL=edge.tar
reject_load load-process-failure
unset NAS_LOAD_FAIL
mv "$TMP/bundle/images/SHA256SUMS" "$TMP/image-sums"
reject_load missing-checksum
[ ! -s "$TMP/docker.log" ]
mv "$TMP/image-sums" "$TMP/bundle/images/SHA256SUMS"
cp "$TMP/bundle/images/manifest.json" "$TMP/original-manifest"
cp "$TMP/bundle/SHA256SUMS" "$TMP/original-root-sums"
python3 - "$TMP/bundle" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
file = root / "images/manifest.json"
manifest = json.loads(file.read_bytes())
for record in manifest["images"].values():
    del record["config_id"]
file.write_text(json.dumps(manifest))
sums = root / "SHA256SUMS"
sums.write_text("".join((hashlib.sha256(file.read_bytes()).hexdigest() + "  images/manifest.json\n")
    if line.endswith("  images/manifest.json\n") else line for line in sums.read_text().splitlines(keepends=True)))
PY
reject_load missing-config-metadata
[ ! -s "$TMP/docker.log" ]
cp "$TMP/original-manifest" "$TMP/bundle/images/manifest.json"
cp "$TMP/original-root-sums" "$TMP/bundle/SHA256SUMS"
printf 'tampered' >>"$TMP/bundle/images/agent.tar"
reject_load bad-source
[ ! -s "$TMP/docker.log" ]
sh "$ROOT/scripts/load-images.sh" --help >"$TMP/help.log"
! grep -q 'Updates stack.env' "$TMP/help.log"
printf 'PASS load_images_help_does_not_promise_env_writes\n'
