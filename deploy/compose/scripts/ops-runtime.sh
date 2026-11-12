#!/bin/sh
# Generic Compose deployments use the same bounded host worker and public protocol.
set -eu
[ "$#" = 2 ] || { printf '%s\n' 'Usage: sh ops-runtime.sh /absolute/worker.json /absolute/state-directory' >&2; exit 2; }
script_root=$(CDPATH= cd -P -- "$(dirname -- "$0")/../../nas/scripts" && pwd)
exec python3 "$script_root/ops-runtime-worker.py" --config "$1" --directory "$2"
