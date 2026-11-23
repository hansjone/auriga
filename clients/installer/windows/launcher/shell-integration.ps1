# Shared shell integration rules. Functions are side-effect-free until called.

# Normalizes a path that arrived from outside the process: environment variables, command
# lines and dialogs can carry wrapping quotes and a trailing separator. Both make the .NET
# path APIs throw ArgumentException, so strip them before use. Keeps a drive/UNC root intact.
function Format-DesktopPath([string]$Path) {
  if (-not $Path) { return $Path }
  $value = $Path.Trim()
  while ($value.Length -ge 2 -and (($value[0] -eq '"' -and $value[-1] -eq '"') -or ($value[0] -eq "'" -and $value[-1] -eq "'"))) {
    $value = $value.Substring(1, $value.Length - 2).Trim()
  }
  $value = $value.Trim('"').Trim("'").Trim()
  if (-not $value) { return $value }
  # Only strip separators that are not the root itself (C:\ and \\server\share stay whole).
  $root = [IO.Path]::GetPathRoot($value)
  if ($root -and $root.Length -ge $value.Length) { return $value }
  return $value.TrimEnd('\', '/')
}

function Test-DesktopPath([string]$First, [string]$Second) {
  if (-not $First -or -not $Second) { return $false }
  try {
    if (-not [IO.Path]::IsPathRooted($First) -or -not [IO.Path]::IsPathRooted($Second)) { return $false }
    return [IO.Path]::GetFullPath($First).TrimEnd('\') -eq [IO.Path]::GetFullPath($Second).TrimEnd('\')
  } catch { return $false }
}

function Get-DesktopStartupCommand([string]$Root) {
  $ps = Join-Path ([Environment]::SystemDirectory) 'WindowsPowerShell\v1.0\powershell.exe'
  $entry = Join-Path $Root 'bin\auriga-autostart.ps1'
  return ('"{0}" -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "{1}"' -f $ps, $entry)
}

function Test-DesktopStartupCommand([string]$Command, [string]$Root) {
  if (-not $Command) { return $false }
  if ($Command -eq (Get-DesktopStartupCommand $Root)) { return $true }
  if ($Command -notmatch '^"([^"]+)" /autostart$') { return $false }
  $target = $Matches[1]
  $versions = [regex]::Escape((Join-Path $Root 'versions').TrimEnd('\'))
  return $target -match ('^' + $versions + '\\[A-Za-z0-9._-]+\\auriga\\tray\\AurigaDesktop\.exe$')
}

function Get-DesktopRootSuffix([string]$Root) {
  $hash = [Security.Cryptography.SHA256]::Create()
  try {
    $canonical = [IO.Path]::GetFullPath($Root).TrimEnd('\').ToLowerInvariant()
    return [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes($canonical)),0,8).Replace('-','')
  } finally { $hash.Dispose() }
}

function Initialize-DesktopShortcuts {
  if (-not ('Auriga.Windows.DesktopShortcut' -as [type])) {
    Add-Type -LiteralPath (Join-Path $PSScriptRoot 'DesktopShortcut.cs')
  }
}

function Get-DesktopShortcutTarget([string]$Path) {
  Initialize-DesktopShortcuts
  return [Auriga.Windows.DesktopShortcut]::GetTarget($Path)
}

function Find-DesktopShortcutPath([string]$Directory, [string]$Label, [string]$Target, [string]$Suffix) {
  $available = $null
  foreach ($name in @("$Label.lnk", "$Label ($Suffix).lnk")) {
    $path = Join-Path $Directory $name
    if (-not (Test-Path -LiteralPath $path)) {
      if (-not $available) { $available = $path }
      continue
    }
    if ((Test-Path -LiteralPath $path -PathType Leaf) -and
        (Test-DesktopPath (Get-DesktopShortcutTarget $path) $Target)) { return $path }
  }
  if ($available) { return $available }
  throw "Desktop shortcut name already in use: $Label"
}

function Remove-DesktopOwnedShortcuts([string]$Root, [string[]]$Shortcuts) {
  $launcher = Join-Path $Root 'bin\auriga-desktop.cmd'
  $uninstaller = Join-Path $Root 'bin\uninstall.cmd'
  foreach ($link in $Shortcuts) {
    if (-not $link -or -not $link.EndsWith('.lnk', [StringComparison]::OrdinalIgnoreCase) -or
        -not (Test-Path -LiteralPath $link -PathType Leaf)) { continue }
    $target = Get-DesktopShortcutTarget $link
    if ((Test-DesktopPath $target $launcher) -or (Test-DesktopPath $target $uninstaller)) {
      Remove-Item -LiteralPath $link -Force -ErrorAction SilentlyContinue
    }
  }
}

# Use the native user hive API. A clean Windows user may have no Uninstall key.
# Opening or creating the exact leaf does not replace existing values.
function Get-DesktopRegistrationLocation([string]$Name) {
  $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Software\Microsoft\Windows\CurrentVersion\Uninstall\$Name")
  if ($key) {
    try { return @{ InstallLocation = $key.GetValue('InstallLocation') } }
    finally { $key.Dispose() }
  }
}

function Set-DesktopRegistration([string]$Name, [hashtable]$Values) {
  $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey("Software\Microsoft\Windows\CurrentVersion\Uninstall\$Name")
  try {
    foreach ($property in $Values.Keys) { $key.SetValue($property, $Values[$property]) }
  } finally { $key.Dispose() }
}

function Invoke-DesktopShellChange([ScriptBlock]$Action) {
  $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  $mutex = [Threading.Mutex]::new($false, "Global\AurigaDesktop-ShellIntegration-$sid")
  $held = $false
  try {
    try { $held = $mutex.WaitOne(15000) } catch [Threading.AbandonedMutexException] { $held = $true }
    if (-not $held) { throw 'Desktop shell integration is busy.' }
    & $Action
  } finally {
    if ($held) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
  }
}
