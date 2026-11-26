#!/usr/bin/env bash
set -euo pipefail
Root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
Cur=$(tr -d '\r\n' <"$Root/current.txt")
Ver="$Root/versions/$Cur"
exec "$Ver/upstream/node/bin/node" "$Ver/auriga/runtime/cli.mjs" rollback --root "$Root"
