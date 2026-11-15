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
$global:fixtureOwnLink = Join-Path $Root 'bin\auriga-desktop.cmd'
$shell | Add-Member ScriptMethod CreateShortcut {
  param($Path)
  $target = if ($Path -like '*(A).lnk') { $global:fixtureOwnLink } else { $global:fixtureOtherLink }
  [pscustomobject]@{ TargetPath = $target }
}
$chosen = Find-DesktopShortcutPath $shell $desktop 'App' $global:fixtureOwnLink 'A'
New-Item -Path $chosen -ItemType File | Out-Null
Remove-Item -LiteralPath (Join-Path $desktop 'App.lnk')
$retained = Find-DesktopShortcutPath $shell $desktop 'App' $global:fixtureOwnLink 'A'
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
$old = @(& (Join-Path $Root 'bin\auriga-autostart.ps1'))
@{ current = 'rolled-back'; previous = 'with-tray' } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $Root 'install.json')
. (Join-Path $Root 'bin\maintenance.ps1')
$maintenance = Get-DesktopMaintenanceVersion $Root
@{ ownership = $ownership; chosen = $chosen; retained = $retained; new = $new; old = $old; maintenance = $maintenance } | ConvertTo-Json -Compress -Depth 4
