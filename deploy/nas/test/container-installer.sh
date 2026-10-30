#!/bin/sh
# CLI boundary tests; no real daemon or bootstrap needed.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
mkdir -p "$TMP/bin" "$TMP/container/auriga-data" "$TMP/package/bin"
cp -R "$ROOT/scripts" "$TMP/package/"
cp "$ROOT/env.example" "$TMP/container/auriga-data/stack.env"
chmod 0600 "$TMP/container/auriga-data/stack.env"
sed -i "s|^AURIGA_DATA_ROOT=.*|AURIGA_DATA_ROOT=$TMP/container/auriga-data|;s|^AURIGA_ACCESS_FILE=.*|AURIGA_ACCESS_FILE=$TMP/access.json|" "$TMP/container/auriga-data/stack.env"
printf 'AURIGA_INSTALL_MODE=container\nAURIGA_INSTALLER_IMAGE=sha256:%064d\n' 1 >>"$TMP/container/auriga-data/stack.env"
printf '{}\n' >"$TMP/access.json"
printf '#!/bin/sh\nexit 0\n' >"$TMP/package/bin/auriga-nas-bootstrap"
chmod 0755 "$TMP/package/bin/auriga-nas-bootstrap"
export NAS_ROOT="$TMP/package" AURIGA_DATA_ROOT="$TMP/container/auriga-data" INSTALL_LOG="$TMP/docker.log"
mkdir "$TMP/export"
for leaf in platform edge agent; do printf 'FIXTURE_KEY=value\n' >"$TMP/export/$leaf.env"; done
printf '0\n' >"$TMP/export/collab.enabled"
tar -C "$TMP/export" -cf "$TMP/export.tar" platform.env edge.env agent.env collab.enabled
export INSTALL_ARCHIVE="$TMP/export.tar" INSTALL_OVERRIDE="$TMP/override-path"
unset NAS_ALLOW_NONROOT NAS_INSTALLER_ACTIVE AURIGA_NAS_BOOTSTRAP_BINARY INSTALL_EXIT INSTALL_PLATFORM INSTALL_COMPOSE_EXIT
cat >"$TMP/bin/docker" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$INSTALL_LOG"
case "$1 ${2:-}" in
  'image inspect') printf '%s\n' "${INSTALL_PLATFORM:-linux/amd64}" ;;
  'run '*)
    case " $* " in *' export-env '*) cat "$INSTALL_ARCHIVE" ;; esac
    exit "${INSTALL_EXIT:-0}" ;;
  'compose version') exit 0 ;;
  'compose -f')
    previous=''
    for arg; do
      case "$arg" in */override.yml) printf '%s\n' "$arg" >"$INSTALL_OVERRIDE"; previous=$arg ;; esac
    done
    [ -n "$previous" ] && [ -r "$previous" ] || exit 92
    [ "$(stat -c '%a' "$(dirname "$previous")")" = 700 ] || exit 93
    for leaf in platform edge agent; do
      [ "$(stat -c '%a' "$(dirname "$previous")/$leaf.env")" = 600 ] || exit 94
    done
    exit "${INSTALL_COMPOSE_EXIT:-0}" ;;
  'rm '*) exit 0 ;;
  *) exit 91 ;;
esac
EOF
chmod 0755 "$TMP/bin/docker"
export PATH="$TMP/bin:$PATH"
fail=0
check() {
  name=$1; expected=$2; shift 2
  : >"$INSTALL_LOG"
  if "$@" >"$TMP/out" 2>&1; then result=0; else result=$?; fi
  if [ "$result" = "$expected" ]; then printf 'PASS %s\n' "$name"
  else printf 'FAIL %s (exit %s expected %s)\n' "$name" "$result" "$expected"; cat "$TMP/out"; fail=1; fi
}
check helper_boundary 0 sh "$NAS_ROOT/scripts/installer.sh" init
if grep -q -- '--network none --user 0:0 --cap-drop ALL' "$INSTALL_LOG" &&
   grep -q -- '--pull never' "$INSTALL_LOG" &&
   grep -q -- '--security-opt no-new-privileges:true' "$INSTALL_LOG" &&
   ! grep -Eq 'docker.sock|--privileged| pull ' "$INSTALL_LOG"; then printf 'PASS helper_restrictions\n'
else printf 'FAIL helper_restrictions\n'; fail=1; fi
INSTALL_EXIT=73; export INSTALL_EXIT
check preserve_helper_exit 73 sh "$NAS_ROOT/scripts/installer.sh" render
unset INSTALL_EXIT
INSTALL_PLATFORM=linux/arm64; export INSTALL_PLATFORM
check reject_helper_arch 1 sh "$NAS_ROOT/scripts/installer.sh" init
unset INSTALL_PLATFORM
check reject_unknown_command 1 sh "$NAS_ROOT/scripts/installer.sh" arbitrary
sed -i 's|^AURIGA_INSTALLER_IMAGE=.*|AURIGA_INSTALLER_IMAGE=postgres:17|' "$AURIGA_DATA_ROOT/stack.env"
check reject_mutable_image 1 sh "$NAS_ROOT/scripts/installer.sh" init
sed -i "s|^AURIGA_INSTALLER_IMAGE=.*|AURIGA_INSTALLER_IMAGE=sha256:$(printf '%064d' 1)|;s|^AURIGA_ACCESS_FILE=.*|AURIGA_ACCESS_FILE=$AURIGA_DATA_ROOT/access.json|" "$AURIGA_DATA_ROOT/stack.env"
printf '{}\n' >"$AURIGA_DATA_ROOT/access.json"
check reject_rw_input_overlap 1 sh "$NAS_ROOT/scripts/installer.sh" init
sed -i "s|^AURIGA_ACCESS_FILE=.*|AURIGA_ACCESS_FILE=$TMP/access.json|" "$AURIGA_DATA_ROOT/stack.env"
check compose_private_exports 0 sh -c '. "$NAS_ROOT/scripts/lib.sh"; load_stack_env; run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" config --quiet'
if [ -f "$INSTALL_OVERRIDE" ] && [ ! -e "$(dirname "$(cat "$INSTALL_OVERRIDE")")" ]; then printf 'PASS compose_private_cleanup\n'
else printf 'FAIL compose_private_cleanup\n'; fail=1; fi
INSTALL_COMPOSE_EXIT=73; export INSTALL_COMPOSE_EXIT
check preserve_compose_exit 73 sh -c '. "$NAS_ROOT/scripts/lib.sh"; load_stack_env; run_compose --env-file "$AURIGA_DATA_ROOT/stack.env" up -d --no-build --pull never'
if [ -f "$INSTALL_OVERRIDE" ] && [ ! -e "$(dirname "$(cat "$INSTALL_OVERRIDE")")" ]; then printf 'PASS compose_failure_cleanup\n'
else printf 'FAIL compose_failure_cleanup\n'; fail=1; fi
unset INSTALL_COMPOSE_EXIT
printf '1\n' >"$TMP/export/collab.enabled"
tar -C "$TMP/export" -cf "$TMP/export.tar" platform.env edge.env agent.env collab.enabled
check compose_activated_overlay 0 sh -c '. "$NAS_ROOT/scripts/lib.sh"; load_stack_env; run_compose config --quiet'
if grep -q -- "$NAS_ROOT/collab-compose.yml" "$INSTALL_LOG"; then printf 'PASS compose_checked_overlay_selected\n'
else printf 'FAIL compose_checked_overlay_selected\n'; fail=1; fi
printf 'invalid\n' >"$TMP/export/collab.enabled"
tar -C "$TMP/export" -cf "$TMP/export.tar" platform.env edge.env agent.env collab.enabled
check reject_invalid_overlay_decision 1 sh -c '. "$NAS_ROOT/scripts/lib.sh"; load_stack_env; run_compose config --quiet'
tar -C "$TMP/export" -cf "$TMP/export.tar" platform.env edge.env agent.env
check reject_missing_overlay_decision 1 sh -c '. "$NAS_ROOT/scripts/lib.sh"; load_stack_env; run_compose config --quiet'
ln -s "$TMP/access.json" "$TMP/access-link.json"
sed -i "s|^AURIGA_ACCESS_FILE=.*|AURIGA_ACCESS_FILE=$TMP/access-link.json|" "$AURIGA_DATA_ROOT/stack.env"
check reject_input_symlink 1 sh "$NAS_ROOT/scripts/installer.sh" init
sed -i "s|^AURIGA_ACCESS_FILE=.*|AURIGA_ACCESS_FILE=$TMP/access.json|" "$AURIGA_DATA_ROOT/stack.env"
chmod 0777 "$TMP/container"
check reject_unsafe_temporary_parent 1 sh "$NAS_ROOT/scripts/installer.sh" init
chmod 0755 "$TMP/container"
exit "$fail"
