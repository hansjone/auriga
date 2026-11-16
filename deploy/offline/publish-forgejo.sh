#!/bin/sh
# Dry-run by default. Only the lead runs --execute [--publish].
# FORGEJO_TOKEN is read from the environment, never passed on argv.
set -eu
ROOT=$(CDPATH= cd -P -- "$(dirname -- "$0")/../.." && pwd)
exec "$ROOT/auriga" release upload "$@"
