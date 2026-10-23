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
  render) exec sh "$NAS_ROOT/scripts/render.sh" ;;
  prepare-socket) prepare_socket_parent ;;
  check)
    [ -f "$(marker_path)" ] || die 'not initialized'
    [ "$(stat -c '%u:%g:%a' "$AURIGA_DATA_ROOT")" = 0:0:755 ] || die 'invalid existing data root metadata; refusing repair'
    bootstrap_nas_materials
    validate_ingress_ancestors ;;
  export-env)
    exec tar -C "$AURIGA_DATA_ROOT/config" -cf - platform.env edge.env agent.env ;;
  *) die 'unsupported installer operation' ;;
esac
