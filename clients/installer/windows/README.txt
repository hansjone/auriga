Auriga Desktop installer (test build, unsigned)

Single-file: run Auriga-Desktop-Setup-<version>.exe. Zip: unpack, run install.cmd.
Options (zip): install.cmd [-InstallDir <dir>] [-DataHome <dir>] [-PlatformOrigin <https://...>] [-NoShortcuts]
  Default install dir: %LOCALAPPDATA%\Programs\AurigaDesktop
  Default data home  : %LOCALAPPDATA%\AurigaDesktop\home (separate from any existing ~/.dsh)
Installs Start menu and desktop shortcuts and an Apps & features entry (current user, no administrator).
Run a newer package to upgrade; <InstallDir>\bin\rollback.cmd goes back one version.
Remove: Apps & features, or powershell -File <InstallDir>\bin\uninstall.ps1 [-PurgeData]
Based on the open-source DSH (MIT); see licenses\NOTICE.txt inside the package.
