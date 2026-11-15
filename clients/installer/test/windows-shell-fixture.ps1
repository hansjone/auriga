param([string]$Root, [string]$Other, [string]$Helper)
$ErrorActionPreference = 'Stop'
. $Helper
$valid = Get-DesktopStartupCommand $Root
$otherCmd = Get-DesktopStartupCommand $Other
$legacy = '"' + (Join-Path $Root 'versions\with-tray\auriga\tray\AurigaDesktop.exe') + '" /autostart'
$desktop = Join-Path $Root 'fixture-shortcuts'
New-Item -Path $desktop -ItemType Directory | Out-Null
New-Item -Path (Join-Path $desktop 'App.lnk') -ItemType File | Out-Null
$shell = [pscustomobject]@{}
$global:fixtureOtherLink = Join-Path $Other 'bin\auriga-desktop.cmd'
$shell | Add-Member ScriptMethod CreateShortcut { param($Path) [pscustomobject]@{ TargetPath = $global:fixtureOtherLink } }
$chosen = Find-DesktopShortcutPath $shell $desktop 'App' (Join-Path $Root 'bin\auriga-desktop.cmd') 'A'
$ownership = @(
  (Test-DesktopPath $Other $Root),
  (Test-DesktopStartupCommand $otherCmd $Root),
  (Test-DesktopStartupCommand $valid $Root),
  (Test-DesktopStartupCommand $legacy $Root)
)
function Start-Process {
  param($FilePath, $ArgumentList, $WindowStyle, $WorkingDirectory)
  @{ file = $FilePath; args = @($ArgumentList); hidden = ($WindowStyle -eq 'Hidden') }
}
Set-Content -LiteralPath (Join-Path $Root 'current.txt') -Value 'with-tray'
$new = & (Join-Path $Root 'bin\auriga-autostart.ps1')
Set-Content -LiteralPath (Join-Path $Root 'current.txt') -Value 'rolled-back'
$old = & (Join-Path $Root 'bin\auriga-autostart.ps1')
@{ ownership = $ownership; chosen = $chosen; new = $new; old = $old } | ConvertTo-Json -Compress -Depth 4
