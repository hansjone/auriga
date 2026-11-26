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
Without arguments, auriga-desktop starts the Linux tray supervisor (notify-send +
browser). Platform auth uses Unix-socket shell-auth; managed Python is still
Windows-first on this branch.
