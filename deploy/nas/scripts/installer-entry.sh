#!/bin/sh
# Internal helper entry. Trust rules remain in the original scripts/bootstrap.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
[ "${NAS_INSTALLER_ACTIVE:-}" = 1 ] || die 'use installer.sh on the host'
load_stack_env >&2
require_root_or_capable_chown
if [ -e "$(marker_path)" ] || [ "${1:-}" != init ]; then
  [ "$(stat -c '%u:%g:%a' "$AURIGA_DATA_ROOT")" = 0:0:755 ] || die 'invalid existing data root metadata; refusing repair'
  if [ -d "$AURIGA_DATA_ROOT/socket-share" ]; then validate_ingress_ancestors; fi
  for _record in platform:10001:10001 edge:65532:65532 agent:0:0; do
    _name=${_record%%:*}; _owner=${_record#*:}
    _file="$AURIGA_DATA_ROOT/config/$_name.env"
    [ -f "$_file" ] && [ ! -L "$_file" ] && [ "$(stat -c '%u:%g:%a:%h' "$_file")" = "$_owner:600:1" ] \
      || die 'invalid private Compose env metadata; refusing repair'
  done
fi
case "${1:-}" in
  materials-stage|materials-commit|materials-rollback|materials-status|materials-check|materials-path|materials-project|materials-project-core)
    _action=${1#materials-}
    set -- -action "$_action" -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/materials-upgrades" -access-file "$AURIGA_ACCESS_FILE"
    [ -z "${NAS_MATERIALS_EXPECTED_BASE:-}" ] || set -- "$@" -expected-base-sha256 "$NAS_MATERIALS_EXPECTED_BASE"
    exec "$(bootstrap_binary)" materials-upgrade "$@" ;;
  collab-publish|collab-install|collab-status|collab-history-check|collab-history-check-pg|collab-startup-check|collab-path|collab-check|collab-stop|collab-prepare|collab-renew|collab-prepare-recovery|collab-recover-expired|collab-queue-prepare|collab-queue-export|collab-queue-import|collab-recover-pending|collab-pause|collab-resume)
    _action=${1#collab-}
    set -- "$_action" -data "$AURIGA_DATA_ROOT" -recipe "$NAS_ROOT"
    [ "$_action" = path ] || set -- "$@" -image "${AURIGA_IMAGE_PLATFORM:?}"
    [ "$_action" = path ] || set -- "$@" -operator-jar-sha256 "${AURIGA_COLLAB_OPERATOR_JAR_SHA256:-}"
    [ -z "${NAS_COLLAB_ROTATION_INPUT:-}" ] || set -- "$@" -input "$NAS_COLLAB_ROTATION_INPUT"
    exec "$(bootstrap_binary)" collab-rotation "$@" ;;
  runtime-path|runtime-stage|runtime-commit|runtime-rollback)
    _action=${1#runtime-}
    set -- -action "$_action" -base "$AURIGA_DATA_ROOT/config/materials" -out "$AURIGA_DATA_ROOT/config/runtime-source"
    [ "$_action" = path ] || set -- "$@" -image "${NAS_RUNTIME_UPGRADE_IMAGE:?}" -bundle "${NAS_RUNTIME_UPGRADE_BUNDLE:?}"
    exec "$(bootstrap_binary)" runtime-upgrade "$@" ;;
  init)
    if [ ! -e "$(marker_path)" ]; then
      # Only an empty directory or a single regular stack.env is adoptable.
      for _entry in "$AURIGA_DATA_ROOT"/* "$AURIGA_DATA_ROOT"/.[!.]* "$AURIGA_DATA_ROOT"/..?*; do
        [ -e "$_entry" ] || [ -L "$_entry" ] || continue
        [ "$_entry" = "$AURIGA_DATA_ROOT/stack.env" ] && [ -f "$_entry" ] && [ ! -L "$_entry" ] && [ "$(stat -c '%h' "$_entry")" = 1 ] \
          || die 'fresh data root contains unknown material; refusing repair'
      done
      chown 0:0 "$AURIGA_DATA_ROOT"
      chmod 0755 "$AURIGA_DATA_ROOT"
    fi
    exec sh "$NAS_ROOT/scripts/init.sh" ;;
  activate-collab)
    bootstrap_nas_materials
    exec sh "$NAS_ROOT/scripts/render-collab.sh" --stopped ;;
  render) exec sh "$NAS_ROOT/scripts/render.sh" ;;
  render-core) exec sh "$NAS_ROOT/scripts/render.sh" --core-only ;;
  render-collab) exec sh "$NAS_ROOT/scripts/render-collab.sh" ;;
  prepare-socket) prepare_socket_parent ;;
  check)
    [ -f "$(marker_path)" ] || die 'not initialized'
    [ "$(stat -c '%u:%g:%a' "$AURIGA_DATA_ROOT")" = 0:0:755 ] || die 'invalid existing data root metadata; refusing repair'
    bootstrap_nas_materials
    validate_ingress_ancestors ;;
  export-env)
    # The host operator cannot traverse root's private collab source. Export a
    # checked decision alongside the already protected env stream, never keys.
    _collab=0
    _managed_identity=0
    _rotation=
    _rotation_available=1
    _source="$AURIGA_DATA_ROOT/config/collab-source"
    # Core Compose export must survive any message material/check failure.
    # Consumers still require available=1; failed material never enables an overlay.
    if [ "${NAS_COLLAB_CONFIG_DEGRADED:-0}" = 1 ]; then
      _rotation_available=0
      if managed_identity_available; then _managed_identity=1; fi
    elif [ -e "$_source" ] || [ -L "$_source" ] || [ -e "$_source.pending" ]; then
      if "$(bootstrap_binary)" collab -base "$AURIGA_DATA_ROOT/config/materials" \
        -out "$_source" -public-origin "${AURIGA_COLLAB_PUBLIC_ORIGIN:-}" -check >&2; then
        _collab=1
        if ! _rotation=$("$(bootstrap_binary)" collab-rotation path -data "$AURIGA_DATA_ROOT" -recipe "$NAS_ROOT"); then
          _rotation_available=0
          printf '%s\n' 'message_network=degraded rotation_snapshot_unavailable; core/stop configuration remains available' >&2
        fi
      else
        _rotation_available=0
        printf '%s\n' 'message_network=degraded source_check_failed; core configuration remains available' >&2
      fi
    fi
    _export=$(mktemp -d)
    trap 'rm -rf -- "$_export"' EXIT
    printf '%s\n' "$_collab" >"$_export/collab.enabled"
    printf '%s\n' "$_managed_identity" >"$_export/managed.identity.enabled"
    runtime_directory >"$_export/runtime.path"
    _bifrost=0
    if [ -e "$AURIGA_DATA_ROOT/config/materials-upgrades" ] || [ -L "$AURIGA_DATA_ROOT/config/materials-upgrades" ]; then
      materials_upgrade check >&2
      _bifrost=1
    fi
    printf '%s\n' "$_bifrost" >"$_export/bifrost.enabled"
    chmod 0600 "$_export/collab.enabled"
    chmod 0600 "$_export/managed.identity.enabled"
    printf '%s' "$_rotation" >"$_export/collab.rotation.path"
    printf '%s' "$_rotation_available" >"$_export/collab.rotation.available"
    chmod 0600 "$_export/collab.rotation.path" "$_export/collab.rotation.available"
    tar -C "$AURIGA_DATA_ROOT/config" -cf - platform.env edge.env agent.env \
      -C "$_export" collab.enabled managed.identity.enabled collab.rotation.path collab.rotation.available runtime.path bifrost.enabled ;;
  *) die 'unsupported installer operation' ;;
esac
