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
    _source="$AURIGA_DATA_ROOT/config/collab-source"
    if [ -e "$_source" ] || [ -L "$_source" ] || [ -e "$_source.pending" ]; then
      "$(bootstrap_binary)" collab -base "$AURIGA_DATA_ROOT/config/materials" \
        -out "$_source" -public-origin "${AURIGA_COLLAB_PUBLIC_ORIGIN:-}" -check >&2
      _collab=1
    fi
    _export=$(mktemp -d)
    trap 'rm -rf -- "$_export"' EXIT
    printf '%s\n' "$_collab" >"$_export/collab.enabled"
    runtime_directory >"$_export/runtime.path"
    chmod 0600 "$_export/collab.enabled"
    tar -C "$AURIGA_DATA_ROOT/config" -cf - platform.env edge.env agent.env \
      -C "$_export" collab.enabled runtime.path ;;
  *) die 'unsupported installer operation' ;;
esac
