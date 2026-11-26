#!/usr/bin/env bash
# Installs or upgrades Auriga Desktop from this package (linux-x64).
# Defaults follow XDG: ~/.local/opt/auriga-desktop and ~/.local/share/auriga-desktop/home
set -euo pipefail

BrandName='Auriga DSH'
ScriptDir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
Payload="$ScriptDir/payload"
XdgData="${XDG_DATA_HOME:-$HOME/.local/share}"
XdgOpt="${XDG_OPT_HOME:-$HOME/.local/opt}"

InstallDir="${AURIGA_INSTALL_DIR:-$XdgOpt/auriga-desktop}"
DataHome="${AURIGA_DATA_HOME:-$XdgData/auriga-desktop/home}"
PlatformOrigin="${AURIGA_PLATFORM_ORIGIN:-}"
PlatformAccessMode="${AURIGA_PLATFORM_ACCESS_MODE:-}"
ConfirmPlatformOrigin="${AURIGA_CONFIRM_PLATFORM_ORIGIN:-}"
NoDesktopShortcut=0
NoShortcuts=0
Autostart=0
NoAutostart=0
Progress=0
MovePayload=0
CancelFile=

usage() {
  cat <<EOF
Usage: ./install.sh [options]
  --install-dir DIR
  --data-home DIR
  --platform-origin URL
  --platform-access-mode MODE
  --confirm-platform-origin URL
  --no-desktop-shortcut
  --no-shortcuts
  --autostart | --no-autostart
  --progress
  --move-payload
  --cancel-file PATH
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-dir) InstallDir=$2; shift 2 ;;
    --data-home) DataHome=$2; shift 2 ;;
    --platform-origin) PlatformOrigin=$2; shift 2 ;;
    --platform-access-mode) PlatformAccessMode=$2; shift 2 ;;
    --confirm-platform-origin) ConfirmPlatformOrigin=$2; shift 2 ;;
    --no-desktop-shortcut) NoDesktopShortcut=1; shift ;;
    --no-shortcuts) NoShortcuts=1; shift ;;
    --autostart) Autostart=1; shift ;;
    --no-autostart) NoAutostart=1; shift ;;
    --progress) Progress=1; shift ;;
    --move-payload) MovePayload=1; shift ;;
    --cancel-file) CancelFile=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage; exit 1 ;;
  esac
done

Node="$Payload/upstream/node/bin/node"
Cli="$Payload/auriga/runtime/cli.mjs"
StateFile="$InstallDir/install.json"
Upgrading=0
if [[ -f "$StateFile" ]]; then
  Upgrading=1
  if [[ -z "${AURIGA_DATA_HOME:-}" ]]; then
    DataHome=$(
      "$Node" -e 'const s=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")); process.stdout.write(s.dataHome)' "$StateFile"
    )
  fi
fi

Args=("$Cli" install --payload "$Payload" --root "$InstallDir" --data-home "$DataHome")
[[ $Progress -eq 1 ]] && Args+=(--progress)
[[ $MovePayload -eq 1 ]] && Args+=(--move-payload)
[[ -n "$CancelFile" ]] && Args+=(--cancel-file "$CancelFile")
[[ -n "$PlatformOrigin" ]] && Args+=(--platform-origin "$PlatformOrigin")
[[ -n "$PlatformAccessMode" ]] && Args+=(--platform-access-mode "$PlatformAccessMode")
[[ -n "$ConfirmPlatformOrigin" ]] && Args+=(--confirm-platform-origin "$ConfirmPlatformOrigin")

set +e
"$Node" "${Args[@]}"
Status=$?
set -e
if [[ $Status -eq 2 && -n "$CancelFile" ]]; then
  exit 2
fi
if [[ $Status -ne 0 ]]; then
  echo "install failed ($Status)" >&2
  exit "$Status"
fi

# Shell integration (best effort after a successful install).
ShellHelper="$Payload/auriga/launcher/shell-integration.sh"
if [[ -f "$ShellHelper" ]]; then
  # shellcheck disable=SC1090
  source "$ShellHelper"
  if [[ $NoShortcuts -eq 0 ]]; then
    install_desktop_entries \
      "$InstallDir" \
      "$BrandName" \
      "$NoDesktopShortcut" \
      "$Autostart" \
      "$NoAutostart" || true
  fi
fi

echo "Installed $BrandName into $InstallDir (data: $DataHome)"
