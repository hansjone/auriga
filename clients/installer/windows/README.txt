Auriga DSH installer (prototype, unsigned)

install.cmd [-InstallDir <dir>] [-DataHome <dir>] [-PlatformOrigin <https://...>]
  Default install dir: %LOCALAPPDATA%\Programs\AurigaDSH
  Default data home  : %LOCALAPPDATA%\AurigaDSH\home (separate from any existing ~/.dsh)
Run again with a newer package to upgrade; <InstallDir>\bin\rollback.cmd goes back one version.
Start: <InstallDir>\bin\auriga-dsh.cmd   Remove: powershell -File <InstallDir>\bin\uninstall.ps1 [-PurgeData]
