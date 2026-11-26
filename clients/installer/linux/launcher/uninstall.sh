#!/usr/bin/env bash
# Removes Auriga Desktop. User data is kept unless --purge-data is given.
set -euo pipefail
Root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
PurgeData=0
Quiet=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --purge-data) PurgeData=1; shift ;;
    --quiet) Quiet=1; shift ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done

Cur=$(tr -d '\r\n' <"$Root/current.txt")
Ver="$Root/versions/$Cur"
Node="$Ver/upstream/node/bin/node"
Cli="$Ver/auriga/runtime/cli.mjs"
Tray="$Ver/auriga/tray/auriga-desktop-tray"

if [[ -x "$Tray" ]]; then
  "$Tray" --stop "$Root" || true
fi

Args=("$Cli" uninstall --root "$Root")
[[ $PurgeData -eq 1 ]] && Args+=(--purge-data)
"$Node" "${Args[@]}"

ShellHelper="$Root/bin/shell-integration.sh"
if [[ -f "$ShellHelper" ]]; then
  # shellcheck disable=SC1090
  source "$ShellHelper"
  remove_desktop_entries "$Root" || true
fi

[[ $Quiet -eq 1 ]] || echo "Uninstalled Auriga Desktop from $Root"
