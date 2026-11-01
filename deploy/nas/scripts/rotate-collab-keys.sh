#!/bin/sh
# Fixed NAS key operations. Root signing always remains an offline operator step.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
case "${1:-}" in
  publish|install|recover-expired) [ "$#" = 2 ] || die 'expected action and approved directory' ;;
  status|check|prepare|renew|stop|prepare-recovery) [ "$#" = 1 ] || die 'expected one fixed collab action' ;;
  *) die 'expected publish|install|status|check|prepare|renew|stop' ;;
esac
if [ "${AURIGA_INSTALL_MODE:-host}" = container ]; then
  case "$1" in
    publish|install|recover-expired) exec sh "$NAS_ROOT/scripts/installer.sh" "collab-$1" "$2" ;;
    *) exec sh "$NAS_ROOT/scripts/installer.sh" "collab-$1" ;;
  esac
fi
_action=$1
if [ "$#" = 2 ]; then set -- "$_action" -input "$2"; else set -- "$_action"; fi
exec "$(bootstrap_binary)" collab-rotation "$@" -data "$AURIGA_DATA_ROOT" \
  -recipe "$NAS_ROOT" -image "$AURIGA_IMAGE_PLATFORM" -operator-jar-sha256 "${AURIGA_COLLAB_OPERATOR_JAR_SHA256:-}"
