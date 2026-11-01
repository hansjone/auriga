#!/bin/sh
# Append an explicit Bifrost material activation; never overwrite the base seal.
set -eu
. "$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)/lib.sh"
load_stack_env
[ -f "$(marker_path)" ] || die 'initialize NAS first'
case "${1:-}" in
  stage|commit|rollback)
    [ "$#" = 2 ] || die 'Usage: materials-upgrade.sh stage|commit|rollback ORIGINAL_RECEIPT_SHA256'
    printf '%s\n' "$2" | grep -Eq '^[a-f0-9]{64}$' || die 'canonical original receipt SHA256 required'
    materials_upgrade "$1" "$2" ;;
  status)
    [ "$#" = 1 ] || die 'status takes no arguments'
    materials_upgrade status ;;
  *) die 'Usage: materials-upgrade.sh stage|commit|rollback ORIGINAL_RECEIPT_SHA256, or status' ;;
esac
