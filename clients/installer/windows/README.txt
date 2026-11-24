Auriga DSH installer (test build, unsigned)

Single-file: run Auriga-Desktop-Setup-<version>.exe. Zip: unpack, run install.cmd.
Options (zip): install.cmd [-InstallDir <dir>] [-DataHome <dir>] [-PlatformOrigin <https://...>] [-NoShortcuts]
  Default install dir: %ProgramFiles%\Auriga DSH
  Default data home  : %LOCALAPPDATA%\AurigaDesktop\home (separate from any existing ~/.dsh)
Single-file setup requests administrator rights (UAC) so you can install to Program Files or any local drive, and grants the installing user maintenance permissions on the install root (while keeping the embedded Python runtime read-only).
Installs Start menu and optional desktop shortcuts, optional autostart, and an Apps & features entry.
Run a newer package to upgrade; <InstallDir>\bin\rollback.cmd goes back one version.
Remove: Apps & features, or powershell -File <InstallDir>\bin\uninstall.ps1 [-PurgeData]
Based on the open-source DSH (MIT); see licenses\NOTICE.txt inside the package.
