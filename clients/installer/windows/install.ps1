# Installs or upgrades Auriga Desktop from this package.
# Setup .exe runs elevated (requireAdministrator) by default so users can install to
# Program Files or any local drive. Defaults can also come from AURIGA_* environment variables.
param(
  [string]$InstallDir = $(if ($env:AURIGA_INSTALL_DIR) { $env:AURIGA_INSTALL_DIR } elseif ($env:ProgramFiles) { Join-Path $env:ProgramFiles 'Auriga DSH' } else { Join-Path $env:LOCALAPPDATA 'Programs\AurigaDesktop' }),
  [string]$DataHome = $(if ($env:AURIGA_DATA_HOME) { $env:AURIGA_DATA_HOME } else { Join-Path $env:LOCALAPPDATA 'AurigaDesktop\home' }),
  [string]$PlatformOrigin = $env:AURIGA_PLATFORM_ORIGIN,
  [string]$PlatformAccessMode,
  [string]$ConfirmPlatformOrigin,
  [string]$StartMenuDir = $(if ($env:AURIGA_START_MENU_DIR) { $env:AURIGA_START_MENU_DIR } else { Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs' }),
  [string]$DesktopDir = $(if ($env:AURIGA_DESKTOP_DIR) { $env:AURIGA_DESKTOP_DIR } else { [Environment]::GetFolderPath('Desktop') }),
  [string]$RegistryKey = $(if ($env:AURIGA_REGISTRY_KEY) { $env:AURIGA_REGISTRY_KEY } else { 'AurigaDesktop' }),
  [switch]$NoDesktopShortcut,
  [switch]$NoShortcuts,
  [switch]$Autostart,
  # Setup wizard: machine-readable steps on stdout, and cancellation by creating this file.
  [switch]$Progress,
  [string]$CancelFile
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$BrandName = 'Auriga DSH'
$payload = Join-Path $PSScriptRoot 'payload'
. (Join-Path $payload 'auriga\launcher\shell-integration.ps1')
# A caller may hand us a path that still carries its own quotes (AURIGA_* variables written
# as "D:\X\", or a Windows command line where a trailing backslash escaped the closing quote
# and left it glued to the value). Strip quotes/whitespace and the trailing separator before
# the path APIs see it, otherwise GetFullPath throws ArgumentException ("路径中具有非法字符").
$InstallDir = [IO.Path]::GetFullPath((Format-DesktopPath $InstallDir))
if ($InstallDir -ne [IO.Path]::GetPathRoot($InstallDir)) { $InstallDir = $InstallDir.TrimEnd('\') }
$installArgs = $PSBoundParameters
Invoke-DesktopShellChange {
$node = Join-Path $payload 'upstream\node\node.exe'
$cli = Join-Path $payload 'auriga\runtime\cli.mjs'
# An upgrade keeps the data directory of the existing installation.
$stateFile = Join-Path $InstallDir 'install.json'
$upgrading = Test-Path -LiteralPath $stateFile
$oldShell = $null
if ($upgrading) {
  $previousShell = Join-Path $InstallDir 'shell.json'
  if (Test-Path -LiteralPath $previousShell) {
    $oldShell = Get-Content -Raw -Encoding UTF8 -LiteralPath $previousShell | ConvertFrom-Json
  }
}
if ($upgrading -and -not $installArgs.ContainsKey('DataHome') -and -not $env:AURIGA_DATA_HOME) {
  $DataHome = (Get-Content -Raw -Encoding UTF8 -LiteralPath $stateFile | ConvertFrom-Json).dataHome
}
if ($oldShell -and -not $installArgs.ContainsKey('NoDesktopShortcut')) {
    $oldDesktop = @($oldShell.shortcuts | Where-Object {
      (Test-DesktopPath (Split-Path -Parent $_) $DesktopDir) -and
      (Test-Path -LiteralPath $_ -PathType Leaf) -and
      (Test-DesktopPath (Get-DesktopShortcutTarget $_) (Join-Path $InstallDir 'bin\auriga-desktop.cmd'))
    })
    $NoDesktopShortcut = $oldDesktop.Count -eq 0
}
$argList = @($cli, 'install', '--payload', $payload, '--root', $InstallDir, '--data-home', $DataHome)
if ($Progress) { $argList += '--progress' }
if ($CancelFile) { $argList += @('--cancel-file', $CancelFile) }
if ($PlatformOrigin) { $argList += @('--platform-origin', $PlatformOrigin) }
if ($PlatformAccessMode) { $argList += @('--platform-access-mode', $PlatformAccessMode) }
if ($ConfirmPlatformOrigin) { $argList += @('--confirm-platform-origin', $ConfirmPlatformOrigin) }
& $node @argList
if ($LASTEXITCODE -eq 2 -and $CancelFile) { exit 2 }
if ($LASTEXITCODE -ne 0) { throw "install failed ($LASTEXITCODE)" }

# When installed elevated (e.g. into Program Files or a restricted drive), grant the
# installing user Modify rights on the install root so the standard-privilege tray can
# persist settings, logs, and in-app updates. Inherited rules do not override the
# protected read-only ACL on versions\<id>\runtime\python.
try {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $isAdmin = ([Security.Principal.WindowsPrincipal]::new($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
  if ($isAdmin -and (Test-Path -LiteralPath $InstallDir -PathType Container)) {
    $acl = Get-Acl -LiteralPath $InstallDir
    $inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
    $propagate = [Security.AccessControl.PropagationFlags]::None
    $rule = New-Object Security.AccessControl.FileSystemAccessRule($identity.User, 'Modify', $inherit, $propagate, 'Allow')
    $acl.AddAccessRule($rule)
    Set-Acl -LiteralPath $InstallDir -AclObject $acl
  }
} catch {
  Write-Warning "Failed to grant user maintenance permissions on ${InstallDir}: $_"
}

# Shell integration (best effort: the installation itself already succeeded).
try {
  $manifest = Get-Content -Raw -Encoding UTF8 -LiteralPath (Join-Path $payload 'manifest.json') | ConvertFrom-Json
  $launcher = Join-Path $InstallDir 'bin\auriga-desktop.cmd'
  $suffix = Get-DesktopRootSuffix $InstallDir
  $links = @()
  $removeTarget = Join-Path $InstallDir 'bin\uninstall.cmd'
  if ($oldShell) {
    $links += @($oldShell.shortcuts | Where-Object {
      if (-not $_ -or -not $_.EndsWith('.lnk', [StringComparison]::OrdinalIgnoreCase) -or
          -not (Test-Path -LiteralPath $_ -PathType Leaf)) { return $false }
      $target = Get-DesktopShortcutTarget $_
      (Test-DesktopPath $target $launcher) -or (Test-DesktopPath $target $removeTarget)
    })
  }
  if (-not $NoShortcuts) {
    Initialize-DesktopShortcuts
    $ico = Join-Path $InstallDir 'bin\auriga.ico'
    $shortcutDirs = @($StartMenuDir)
    if (-not $NoDesktopShortcut) { $shortcutDirs += $DesktopDir }
    foreach ($dir in $shortcutDirs) {
      New-Item -ItemType Directory -Force -Path $dir | Out-Null
      $path = Find-DesktopShortcutPath $dir $BrandName $launcher $suffix
      [Auriga.Windows.DesktopShortcut]::Save($path, $launcher, $InstallDir, $BrandName, $ico)
      $oldPath = Join-Path $dir 'Auriga Desktop.lnk'
      if (Test-Path -LiteralPath $oldPath) {
        if (Test-DesktopPath (Get-DesktopShortcutTarget $oldPath) $launcher) { Remove-Item -LiteralPath $oldPath -Force }
      }
      if ($links -notcontains $path) { $links += $path }
    }
    # Start menu uninstall entry (the Apps & features entry and bin\uninstall.cmd open the same uninstaller).
    $removeLabel = [Auriga.Windows.DesktopShortcut]::UninstallLabel($BrandName)
    $removeLink = Find-DesktopShortcutPath $StartMenuDir $removeLabel $removeTarget $suffix
    [Auriga.Windows.DesktopShortcut]::Save($removeLink, $removeTarget, [Environment]::SystemDirectory, $removeLabel, $ico)
    if ($links -notcontains $removeLink) { $links += $removeLink }
  }
  try {
    if ($oldShell -and $oldShell.registryKey -match '^[A-Za-z0-9._-]{1,64}$') { $RegistryKey = $oldShell.registryKey }
    if ($RegistryKey -notmatch '^[A-Za-z0-9._-]{1,64}$') { throw 'Invalid Desktop registry key.' }
    $registered = Get-DesktopRegistrationLocation $RegistryKey
    if ($registered -and -not (Test-DesktopPath $registered.InstallLocation $InstallDir)) {
      $RegistryKey = "AurigaDesktop-$suffix"
      $registered = Get-DesktopRegistrationLocation $RegistryKey
      if ($registered -and -not (Test-DesktopPath $registered.InstallLocation $InstallDir)) {
        throw 'Desktop registry key already belongs to another install.'
      }
    }
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
    # Apps & features size, in KiB, of the installed version.
    $bytes = 0
    foreach ($file in [IO.Directory]::EnumerateFiles((Join-Path $InstallDir "versions\$($manifest.id)"), '*', 'AllDirectories')) { $bytes += ([IO.FileInfo]$file).Length }
    $values.EstimatedSize = [int]($bytes / 1024)
    $values.NoModify = 1
    $values.NoRepair = 1
    Set-DesktopRegistration $RegistryKey $values
    if ($Autostart) {
      $runKey = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Microsoft\Windows\CurrentVersion\Run')
      try { $runKey.SetValue($RegistryKey, (Get-DesktopStartupCommand $InstallDir), [Microsoft.Win32.RegistryValueKind]::String) }
      finally { $runKey.Dispose() }
    }
  } finally {
    @{ shortcuts = $links; registryKey = $RegistryKey } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $InstallDir 'shell.json') -Encoding UTF8
  }
} catch {
  Write-Warning "Shell integration failed: $_"
}
Write-Host "Installed $BrandName to $InstallDir. Start with: $InstallDir\bin\auriga-desktop.cmd"
}
