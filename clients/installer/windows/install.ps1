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
  [switch]$NoShortcuts
)
$ErrorActionPreference = 'Stop'
$BrandName = 'Auriga DSH'
$payload = Join-Path $PSScriptRoot 'payload'
$node = Join-Path $payload 'upstream\node\node.exe'
$cli = Join-Path $payload 'auriga\runtime\cli.mjs'
$argList = @($cli, 'install', '--payload', $payload, '--root', $InstallDir, '--data-home', $DataHome)
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
      $link.Save()
      $oldPath = Join-Path $dir 'Auriga Desktop.lnk'
      if (Test-Path -LiteralPath $oldPath) {
        $oldLink = $shell.CreateShortcut($oldPath)
        if ($oldLink.TargetPath -eq $launcher) { Remove-Item -LiteralPath $oldPath -Force }
      }
      $links += $path
    }
  }
  $key = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$RegistryKey"
  New-Item -Path $key -Force | Out-Null
  $uninstall = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$(Join-Path $InstallDir 'bin\uninstall.ps1')`""
  $values = @{
    DisplayName = $BrandName
    DisplayVersion = $manifest.bundleVersion
    Publisher = $BrandName
    InstallLocation = $InstallDir
    UninstallString = $uninstall
    QuietUninstallString = "$uninstall -Quiet"
  }
  foreach ($name in $values.Keys) { New-ItemProperty -Path $key -Name $name -Value $values[$name] -PropertyType String -Force | Out-Null }
  New-ItemProperty -Path $key -Name NoModify -Value 1 -PropertyType DWord -Force | Out-Null
  New-ItemProperty -Path $key -Name NoRepair -Value 1 -PropertyType DWord -Force | Out-Null
  @{ shortcuts = $links; registryKey = $RegistryKey } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $InstallDir 'shell.json') -Encoding UTF8
} catch {
  Write-Warning "Shell integration failed: $_"
}
Write-Host "Installed $BrandName to $InstallDir. Start with: $InstallDir\bin\auriga-desktop.cmd"
