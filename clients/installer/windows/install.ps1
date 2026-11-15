# Installs or upgrades Auriga Desktop from this package. Needs no administrator rights.
# Defaults can also come from AURIGA_* environment variables (used by the single-file installer and tests).
param(
  [string]$InstallDir = $(if ($env:AURIGA_INSTALL_DIR) { $env:AURIGA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\AurigaDesktop' }),
  [string]$DataHome = $(if ($env:AURIGA_DATA_HOME) { $env:AURIGA_DATA_HOME } else { Join-Path $env:LOCALAPPDATA 'AurigaDesktop\home' }),
  [string]$PlatformOrigin = $env:AURIGA_PLATFORM_ORIGIN,
  [string]$PlatformAccessMode,
  [string]$ConfirmPlatformOrigin,
  [string]$StartMenuDir = $(if ($env:AURIGA_START_MENU_DIR) { $env:AURIGA_START_MENU_DIR } else { Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs' }),
  [string]$DesktopDir = $(if ($env:AURIGA_DESKTOP_DIR) { $env:AURIGA_DESKTOP_DIR } else { [Environment]::GetFolderPath('Desktop') }),
  [string]$RegistryKey = $(if ($env:AURIGA_REGISTRY_KEY) { $env:AURIGA_REGISTRY_KEY } else { 'AurigaDesktop' }),
  [switch]$NoDesktopShortcut,
  [switch]$NoShortcuts,
  # Setup wizard: machine-readable steps on stdout, and cancellation by creating this file.
  [switch]$Progress,
  [string]$CancelFile
)
$ErrorActionPreference = 'Stop'
$BrandName = 'Auriga DSH'
$payload = Join-Path $PSScriptRoot 'payload'
$node = Join-Path $payload 'upstream\node\node.exe'
$cli = Join-Path $payload 'auriga\runtime\cli.mjs'
# An upgrade keeps the data directory of the existing installation.
$stateFile = Join-Path $InstallDir 'install.json'
$upgrading = Test-Path -LiteralPath $stateFile
if ($upgrading -and -not $PSBoundParameters.ContainsKey('DataHome') -and -not $env:AURIGA_DATA_HOME) {
  $DataHome = (Get-Content -Raw -LiteralPath $stateFile | ConvertFrom-Json).dataHome
}
if ($upgrading -and -not $PSBoundParameters.ContainsKey('NoDesktopShortcut')) {
  $NoDesktopShortcut = -not (
    (Test-Path -LiteralPath (Join-Path $DesktopDir "$BrandName.lnk")) -or
    (Test-Path -LiteralPath (Join-Path $DesktopDir 'Auriga Desktop.lnk'))
  )
}
$argList = @($cli, 'install', '--payload', $payload, '--root', $InstallDir, '--data-home', $DataHome)
if ($Progress) { $argList += '--progress' }
if ($CancelFile) { $argList += @('--cancel-file', $CancelFile) }
if ($PlatformOrigin) { $argList += @('--platform-origin', $PlatformOrigin) }
if ($PlatformAccessMode) { $argList += @('--platform-access-mode', $PlatformAccessMode) }
if ($ConfirmPlatformOrigin) { $argList += @('--confirm-platform-origin', $ConfirmPlatformOrigin) }
& $node @argList
if ($LASTEXITCODE -ne 0) { throw "install failed ($LASTEXITCODE)" }

# Shell integration (best effort: the installation itself already succeeded).
try {
  $manifest = Get-Content -Raw -LiteralPath (Join-Path $payload 'manifest.json') | ConvertFrom-Json
  $launcher = Join-Path $InstallDir 'bin\auriga-desktop.cmd'
  $shell = New-Object -ComObject WScript.Shell
  $links = @()
  if (-not $NoShortcuts) {
    $shortcutDirs = @($StartMenuDir)
    if (-not $NoDesktopShortcut) { $shortcutDirs += $DesktopDir }
    foreach ($dir in $shortcutDirs) {
      New-Item -ItemType Directory -Force -Path $dir | Out-Null
      $path = Join-Path $dir "$BrandName.lnk"
      $link = $shell.CreateShortcut($path)
      $link.TargetPath = $launcher
      $link.WorkingDirectory = $InstallDir
      $link.WindowStyle = 7
      $link.Description = $BrandName
      $ico = Join-Path $InstallDir 'bin\auriga.ico'
      if (Test-Path -LiteralPath $ico) { $link.IconLocation = "$ico,0" }
      $link.Save()
      $oldPath = Join-Path $dir 'Auriga Desktop.lnk'
      if (Test-Path -LiteralPath $oldPath) {
        $oldLink = $shell.CreateShortcut($oldPath)
        if ($oldLink.TargetPath -eq $launcher) { Remove-Item -LiteralPath $oldPath -Force }
      }
      $links += $path
    }
    # Start menu uninstall entry (the Apps & features entry and bin\uninstall.cmd open the same uninstaller).
    $remove = $shell.CreateShortcut((Join-Path $StartMenuDir "卸载 $BrandName.lnk"))
    $remove.TargetPath = Join-Path $InstallDir 'bin\uninstall.cmd'
    $remove.WorkingDirectory = [Environment]::SystemDirectory
    $remove.WindowStyle = 7
    $remove.Description = "卸载 $BrandName"
    $ico = Join-Path $InstallDir 'bin\auriga.ico'
    if (Test-Path -LiteralPath $ico) { $remove.IconLocation = "$ico,0" }
    $remove.Save()
    $links += (Join-Path $StartMenuDir "卸载 $BrandName.lnk")
  }
  $key = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$RegistryKey"
  New-Item -Path $key -Force | Out-Null
  $uninstall = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$(Join-Path $InstallDir 'bin\uninstall.ps1')`""
  $values = @{
    DisplayName = $BrandName
    DisplayVersion = $manifest.bundleVersion
    Publisher = 'Auriga'
    DisplayIcon = (Join-Path $InstallDir 'bin\auriga.ico')
    InstallDate = (Get-Date -Format 'yyyyMMdd')
    InstallLocation = $InstallDir
    UninstallString = $uninstall
    QuietUninstallString = "$uninstall -Quiet"
  }
  foreach ($name in $values.Keys) { New-ItemProperty -Path $key -Name $name -Value $values[$name] -PropertyType String -Force | Out-Null }
  # Apps & features size, in KiB, of the installed version.
  $bytes = 0
  foreach ($file in [IO.Directory]::EnumerateFiles((Join-Path $InstallDir "versions\$($manifest.id)"), '*', 'AllDirectories')) { $bytes += ([IO.FileInfo]$file).Length }
  New-ItemProperty -Path $key -Name EstimatedSize -Value ([int]($bytes / 1024)) -PropertyType DWord -Force | Out-Null
  New-ItemProperty -Path $key -Name NoModify -Value 1 -PropertyType DWord -Force | Out-Null
  New-ItemProperty -Path $key -Name NoRepair -Value 1 -PropertyType DWord -Force | Out-Null
  @{ shortcuts = $links; registryKey = $RegistryKey } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $InstallDir 'shell.json') -Encoding UTF8
} catch {
  Write-Warning "Shell integration failed: $_"
}
Write-Host "Installed $BrandName to $InstallDir. Start with: $InstallDir\bin\auriga-desktop.cmd"
