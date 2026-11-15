# Shared shell integration rules. Functions are side-effect-free until called.
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

function Find-DesktopShortcutPath($Shell, [string]$Directory, [string]$Label, [string]$Target, [string]$Suffix) {
  $available = $null
  foreach ($name in @("$Label.lnk", "$Label ($Suffix).lnk")) {
    $path = Join-Path $Directory $name
    if (-not (Test-Path -LiteralPath $path)) {
      if (-not $available) { $available = $path }
      continue
    }
    if ((Test-Path -LiteralPath $path -PathType Leaf) -and
        (Test-DesktopPath ($Shell.CreateShortcut($path).TargetPath) $Target)) { return $path }
  }
  if ($available) { return $available }
  throw "Desktop shortcut name already in use: $Label"
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
