#!/usr/bin/env bash
# Creates/removes XDG desktop entries and optional autostart for Auriga Desktop.
set -euo pipefail

desktop_apps_dir() {
  echo "${XDG_DATA_HOME:-$HOME/.local/share}/applications"
}

autostart_dir() {
  echo "${XDG_CONFIG_HOME:-$HOME/.config}/autostart"
}

desktop_file_id() {
  local root=$1
  local digest
  digest=$(printf '%s' "$root" | sha256sum | awk '{print substr($1,1,16)}')
  echo "auriga-desktop-${digest}.desktop"
}

write_desktop_file() {
  local path=$1
  local name=$2
  local exec_line=$3
  local icon=$4
  cat >"$path" <<EOF
[Desktop Entry]
Type=Application
Version=1.0
Name=$name
Comment=Auriga DSH desktop client
Exec=$exec_line
Icon=$icon
Terminal=false
Categories=Development;Office;
StartupNotify=false
EOF
}

install_desktop_entries() {
  local root=$1
  local brand=$2
  local no_desktop_shortcut=${3:-0}
  local autostart=${4:-0}
  local no_autostart=${5:-0}
  local launcher="$root/bin/auriga-desktop"
  local icon="$root/bin/auriga.png"
  local id
  id=$(desktop_file_id "$root")
  local apps
  apps=$(desktop_apps_dir)
  mkdir -p "$apps"
  chmod 755 "$launcher" 2>/dev/null || true
  write_desktop_file "$apps/$id" "$brand" "\"$launcher\"" "$icon"
  if [[ $no_desktop_shortcut -eq 0 ]]; then
    local desk="${XDG_DESKTOP_DIR:-$HOME/Desktop}"
    if [[ -d "$desk" ]]; then
      write_desktop_file "$desk/$id" "$brand" "\"$launcher\"" "$icon"
      chmod 755 "$desk/$id" 2>/dev/null || true
    fi
  fi
  if [[ $autostart -eq 1 && $no_autostart -eq 0 ]]; then
    local auto
    auto=$(autostart_dir)
    mkdir -p "$auto"
    write_desktop_file "$auto/$id" "$brand" "\"$launcher\"" "$icon"
  fi
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "$apps" >/dev/null 2>&1 || true
  fi
}

remove_desktop_entries() {
  local root=$1
  local id
  id=$(desktop_file_id "$root")
  rm -f "$(desktop_apps_dir)/$id"
  rm -f "${XDG_DESKTOP_DIR:-$HOME/Desktop}/$id"
  rm -f "$(autostart_dir)/$id"
}
