Auriga DSH installer (linux-x64)

Unpack the tar.gz, then run:
  chmod +x install.sh
  ./install.sh

Options:
  --install-dir DIR          default: $XDG_OPT_HOME/auriga-desktop or ~/.local/opt/auriga-desktop
  --data-home DIR            default: $XDG_DATA_HOME/auriga-desktop/home or ~/.local/share/auriga-desktop/home
  --platform-origin URL
  --no-desktop-shortcut
  --no-shortcuts
  --autostart | --no-autostart

Upgrade: run a newer package's install.sh against the same --install-dir.
Rollback: <InstallDir>/bin/rollback.sh
Remove:   <InstallDir>/bin/uninstall.sh [--purge-data]

Based on the open-source DSH (MIT); see licenses/NOTICE.txt inside the package.
Tray and Unix shell-auth helper land in later commits on this branch; until then
the launcher starts DSH in the browser without the Windows tray shell.
