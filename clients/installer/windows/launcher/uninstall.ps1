# Removes Auriga Desktop. User data (profile, sessions) is kept unless -PurgeData is given.
# Only files this installer created are removed; unknown files in the install folder stay.
# Interactive runs (Apps & features) open the graphical uninstaller: confirmation, progress, keep-data choice.
param([switch]$PurgeData, [switch]$Quiet, [switch]$ReportResult)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$tempRoot = [IO.Path]::GetTempPath()
Set-Location -LiteralPath $tempRoot
[Environment]::CurrentDirectory = $tempRoot
. (Join-Path $PSScriptRoot 'maintenance.ps1')
$tray = Join-Path (Get-DesktopMaintenanceVersion $root) 'auriga\tray\AurigaDesktop.exe'
if (Test-Path -LiteralPath $tray) {
  # Run from a temporary copy so the tray executable does not lock the install directory.
  $gui = Join-Path $tempRoot ('auriga-desktop-uninstall-ui-' + [guid]::NewGuid().ToString('N'))
  try {
    New-Item -ItemType Directory -Path $gui | Out-Null
    Copy-Item -LiteralPath $tray -Destination $gui
    $action = if ($Quiet -or $PurgeData) { '/stop' } else { '/uninstall' }
    $p = Start-Process -FilePath (Join-Path $gui 'AurigaDesktop.exe') -ArgumentList @($action, "`"$root`"") -WorkingDirectory $gui -PassThru
    $p.WaitForExit()
    $exitCode = $p.ExitCode
    $p.Dispose()
    if ($action -eq '/uninstall') { exit $exitCode }
    if ($exitCode -ne 0) { throw 'Auriga DSH is still running. Close its tray or console and retry.' }
  } finally {
    Remove-Item -LiteralPath $gui -Recurse -Force -ErrorAction SilentlyContinue
  }
} elseif (-not $Quiet -and -not $PurgeData) {
  Add-Type -AssemblyName System.Windows.Forms
  $choice = [System.Windows.Forms.MessageBox]::Show('是否保留用户数据（配置、会话和登录信息）？选择“取消”终止卸载。', '卸载 Auriga DSH', 'YesNoCancel', 'Question', 'Button1')
  if ($choice -eq 'Cancel') { exit 0 }
  $PurgeData = $choice -eq 'No'
}
. (Join-Path $PSScriptRoot 'shell-integration.ps1')
Invoke-DesktopShellChange {
$ver = Get-DesktopMaintenanceVersion $root
# Shortcuts and the Apps & features entry recorded at install time (read before the root is cleaned).
$shellFile = Join-Path $root 'shell.json'
$shellInfo = $null
if (Test-Path -LiteralPath $shellFile) { $shellInfo = Get-Content -Raw -Encoding UTF8 -LiteralPath $shellFile | ConvertFrom-Json }
# Load the shortcut code while its source is still in the installation.
if ($shellInfo.shortcuts) { Initialize-DesktopShortcuts }
# Run from a temp copy so the running node.exe and scripts do not lock the install root.
$tmp = Join-Path $tempRoot ('auriga-desktop-uninstall-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'uninstall-cleanup.ps1') -Destination $tmp
  Copy-Item (Join-Path $ver 'upstream\node\node.exe') $tmp
  Copy-Item (Join-Path $ver 'auriga\runtime\*.mjs') $tmp
  # The isolated runtime also needs the frozen state-machine/schema assets.
  $schemas = Join-Path $ver 'auriga\runtime\schemas'
  if (Test-Path -LiteralPath $schemas) { Copy-Item -LiteralPath $schemas -Destination (Join-Path $tmp 'schemas') -Recurse }
  $argList = @((Join-Path $tmp 'cli.mjs'), 'uninstall', '--root', $root)
  if ($PurgeData) { $argList += '--purge-data' }
  $output = & (Join-Path $tmp 'node.exe') @argList
  if ($LASTEXITCODE -ne 0) { throw "uninstall failed ($LASTEXITCODE)" }
  # Anything still locked is removed at the next sign-in (per-user RunOnce, no administrator rights).
  $result = @($output | Where-Object { $_ -like '{*' })[-1] | ConvertFrom-Json
  $pending = @($result.pending).Count -gt 0
  if ($pending) {
    $id = [guid]::Parse($result.cleanupId).ToString()
    $quarantine = Join-Path $root ".uninstall-$id"
    if (@($result.pending).Count -ne 1 -or $result.pending[0] -ne $quarantine) {
      throw 'Repudiated unexpected cleanup path.'
    }
    $job = Join-Path $env:LOCALAPPDATA "AurigaDesktopCleanup\$id"
    New-Item -ItemType Directory -Path $job -Force | Out-Null
    $script = Join-Path $job 'uninstall-cleanup.ps1'
    Copy-Item -LiteralPath (Join-Path $tmp 'uninstall-cleanup.ps1') -Destination $script
    @{ schema = 1; root = $root; id = $id } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $job 'cleanup.json') -Encoding UTF8
    $command = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$script`""
    # CreateSubKey opens an existing RunOnce key with its values intact.
    $runOnce = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Software\Microsoft\Windows\CurrentVersion\RunOnce')
    try { $runOnce.SetValue("!AurigaDesktopCleanup-$id", $command, [Microsoft.Win32.RegistryValueKind]::String) }
    finally { $runOnce.Dispose() }
  }
} finally {
  Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
# Only after the core succeeded: shortcuts, autostart and registry entry.
if ($shellInfo) {
  Remove-DesktopOwnedShortcuts $root $shellInfo.shortcuts
  if ($shellInfo.registryKey -match '^[A-Za-z0-9._-]{1,64}$') {
    $name = $shellInfo.registryKey
    $key = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$name"
    $registered = Get-ItemProperty -LiteralPath $key -ErrorAction SilentlyContinue
    if ($registered -and (Test-DesktopPath $registered.InstallLocation $root)) {
      Remove-Item -LiteralPath $key -Recurse -Force -ErrorAction SilentlyContinue
    }
    $runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
    $auto = Get-ItemProperty -LiteralPath $runKey -Name $name -ErrorAction SilentlyContinue
    if ($auto -and (Test-DesktopStartupCommand $auto.$name $root)) {
      Remove-ItemProperty -LiteralPath $runKey -Name $name -ErrorAction SilentlyContinue
    }
  }
}
if ($ReportResult) { @{ dataKept = [bool]$result.dataKept } | ConvertTo-Json -Compress }
if ($pending) { exit 3010 }
}
