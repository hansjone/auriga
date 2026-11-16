#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"
TEMP=$(mktemp -d)
trap 'rm -rf -- "$TEMP"' EXIT INT TERM
# DrvFS exposes executable modes differently from native Linux. Run the actual
# prebuilt producer from a private native copy, preserving its required 0755.
cp deploy/nas/bin/auriga-nas-bootstrap "$TEMP/bootstrap"
chmod 0755 "$TEMP/bootstrap"
export AURIGA_NAS_BOOTSTRAP_BINARY="$TEMP/bootstrap"
sh deploy/nas/test/run-tests.sh
