#!/bin/sh
# Explicit operator-managed bind source initialization; never pulls an image.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
case "${1:-}" in
  init|render|check|prepare-socket|export-env|activate-collab|runtime-path|runtime-stage|runtime-commit|runtime-rollback|collab-publish|collab-install|collab-status|collab-history-check|collab-history-check-pg|collab-path|collab-check|collab-stop|collab-prepare|collab-renew|collab-prepare-recovery|collab-recover-expired|collab-queue-prepare|collab-queue-export|collab-queue-import|collab-recover-pending|collab-pause|collab-resume) operation=$1 ;;
  *) die 'Usage: sh installer.sh init|render|check|prepare-socket|export-env|activate-collab' ;;
esac
upgrade_bundle=
upgrade_image=
rotation_input=
case "$operation" in
  collab-publish|collab-install|collab-recover-expired|collab-queue-import)
    [ "$#" = 2 ] || die 'collab rotation requires an approved directory'
    rotation_input=$2 ;;
  runtime-stage|runtime-commit|runtime-rollback)
    [ "$#" = 3 ] || die 'runtime operation requires bundle and exact image'
    upgrade_bundle=$2; upgrade_image=$3 ;;
  *) [ "$#" = 1 ] || die 'installer accepts exactly one operation' ;;
esac
load_stack_env >&2
[ "${AURIGA_INSTALL_MODE:-host}" = container ] || die 'set AURIGA_INSTALL_MODE=container explicitly'
require_cmd docker readlink stat mktemp
validate_input_path() {
  _input=$1
  case "$_input" in /*) ;; *) die 'installer input must be absolute' ;; esac
  case "$_input" in *','*|*':'*|*'\'*|*'
'*) die 'unsupported installer mount path' ;; esac
  [ ! -L "$_input" ] && [ -e "$_input" ] || die 'installer input missing or leaf symlink'
  _resolved=$(readlink -f -- "$_input") || die 'cannot resolve installer input'
  [ "$_resolved" = "$_input" ] || die 'installer inputs must use resolved canonical paths'
  case "$_input/" in "$AURIGA_DATA_ROOT/"*) die 'installer input overlaps writable data root' ;; esac
  case "$AURIGA_DATA_ROOT/" in "$_input/"*) die 'installer input contains writable data root' ;; esac
}
[ -d "$AURIGA_DATA_ROOT" ] && [ ! -L "$AURIGA_DATA_ROOT" ] || die 'operator must create the fresh data directory first'
case "$AURIGA_DATA_ROOT" in *','*|*':'*|*'\'*|*'
'*) die 'unsupported installer data mount path' ;; esac
[ "$(readlink -f -- "$AURIGA_DATA_ROOT")" = "$AURIGA_DATA_ROOT" ] || die 'data root must be canonical'
validate_input_path "$NAS_ROOT"
validate_input_path "$AURIGA_ACCESS_FILE"
_boot=$(bootstrap_binary)
validate_input_path "$_boot"
[ "$(stat -c '%a' "$_boot")" = 755 ] || die 'prebuilt bootstrap must have mode0755'
_image=${AURIGA_INSTALLER_IMAGE:-}
printf '%s\n' "$_image" | grep -Eq '^(sha256:|[^[:space:]]+@sha256:)[a-f0-9]{64}$' || die 'installer image must be an immutable local id or digest'
case "${_image##*sha256:}" in *[!0]*) ;; *) die 'placeholder installer image' ;; esac
[ "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$_image")" = linux/amd64 ] || die 'installer image must already exist locally as linux/amd64'

# --mount never creates a missing host source. Each input retains its absolute
# locator so the receipt remains stable across helper restarts.
set -- --mount "type=bind,src=$AURIGA_DATA_ROOT,dst=$AURIGA_DATA_ROOT" \
  --mount "type=bind,src=$NAS_ROOT,dst=$NAS_ROOT,readonly" \
  --mount "type=bind,src=$AURIGA_ACCESS_FILE,dst=$AURIGA_ACCESS_FILE,readonly"
case "$_boot" in "$NAS_ROOT/"*) ;; *) set -- "$@" --mount "type=bind,src=$_boot,dst=$_boot,readonly" ;; esac
for _input in "${AURIGA_RUNTIME_BUNDLE:-}" "${AURIGA_RUNTIME_ROOT:-}" "${AURIGA_HTTPS_CERT_FILE:-}" "${AURIGA_HTTPS_KEY_FILE:-}" "${AURIGA_COLLAB_INPUT_DIR:-}"; do
  [ -n "$_input" ] || continue
  validate_input_path "$_input"
  set -- "$@" --mount "type=bind,src=$_input,dst=$_input,readonly"
done
if [ -n "$upgrade_bundle" ]; then
  validate_input_path "$upgrade_bundle"
  set -- "$@" --mount "type=bind,src=$upgrade_bundle,dst=$upgrade_bundle,readonly"
fi
if [ -n "$rotation_input" ]; then
  case "$rotation_input" in
    "$AURIGA_DATA_ROOT/config/collab-operations/requests/"*)
      # The host operator cannot traverse root's private archive. Only a single
      # canonical version component is admitted here; the root bootstrap checks
      # every ancestor, inode, owner, permission and material before use.
      _request_version=${rotation_input#"$AURIGA_DATA_ROOT/config/collab-operations/requests/"}
      case "$_request_version" in ''|0*|*[!0-9]*) die 'noncanonical collab request' ;; esac ;;
    *) validate_input_path "$rotation_input"
       set -- "$@" --mount "type=bind,src=$rotation_input,dst=$rotation_input,readonly" ;;
  esac
fi
case "$operation" in
  collab-publish|collab-install|collab-status|collab-history-check-pg|collab-check|collab-stop|collab-prepare|collab-renew|collab-prepare-recovery|collab-recover-expired|collab-queue-prepare|collab-queue-export|collab-queue-import|collab-recover-pending|collab-pause|collab-resume)
    set -- "$@" --mount type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock \
      --label auriga.nas.rotation.controller=true ;;
esac
# A bootstrap located inside the writable root was already rejected above.
# Only explicit collab operations receive the local socket. Normal offline
# installation/render helpers still receive neither a socket nor a network.
(
  umask 077
  _run_tmp=$(installer_private_dir installer)
  cleanup() {
    _exit=$?
    trap - EXIT
    if [ -f "$_run_tmp/cid" ]; then
      _cid=$(cat "$_run_tmp/cid")
      case "$_cid" in ''|*[!0-9a-f]*) ;; *) docker rm -f -v "$_cid" >/dev/null 2>&1 || : ;; esac
    fi
    rm -rf -- "$_run_tmp"
    exit "$_exit"
  }
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  docker run --rm --pull never --cidfile "$_run_tmp/cid" \
    --network none --user 0:0 --cap-drop ALL \
    --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID \
    --security-opt no-new-privileges:true --read-only --tmpfs /tmp:mode=1777 \
    "$@" --env "NAS_ROOT=$NAS_ROOT" --env "AURIGA_DATA_ROOT=$AURIGA_DATA_ROOT" \
    --env "AURIGA_NAS_BOOTSTRAP_BINARY=$_boot" --env NAS_INSTALLER_ACTIVE=1 \
    --env "NAS_RUNTIME_UPGRADE_BUNDLE=$upgrade_bundle" --env "NAS_RUNTIME_UPGRADE_IMAGE=$upgrade_image" \
    --env "NAS_COLLAB_ROTATION_INPUT=$rotation_input" \
    --entrypoint /bin/sh "$_image" "$NAS_ROOT/scripts/installer-entry.sh" "$operation"
)
