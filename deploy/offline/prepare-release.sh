#!/bin/sh
# Run on the approved Linux/WSL/NAS release machine, never a full-build CI job.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/../.." && pwd)
exec "$ROOT/auriga" release "$@"
