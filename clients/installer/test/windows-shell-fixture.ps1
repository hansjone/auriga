param([string]$Root, [string]$Other, [string]$Helper)
$ErrorActionPreference = 'Stop'
. $Helper
$valid = Get-DesktopStartupCommand $Root
$otherCmd = Get-DesktopStartupCommand $Other
$legacy = '"' + (Join-Path $Root 'versions\with-tray\auriga\tray\AurigaDesktop.exe') + '" /autostart'
$desktop = Join-Path $Root 'fixture-shortcuts'
New-Item -Path $desktop -ItemType Directory | Out-Null
Initialize-DesktopShortcuts
$ownTarget = Join-Path $Root 'bin\auriga-desktop.cmd'
$otherTarget = Join-Path $Other 'bin\auriga-desktop.cmd'
$removeTarget = Join-Path $Root 'bin\uninstall.cmd'
New-Item -Path (Join-Path $Other 'bin') -ItemType Directory -Force | Out-Null
foreach ($file in @($ownTarget, $otherTarget, $removeTarget)) { Set-Content -LiteralPath $file -Value 'exit /b 0' }
$legacyLink = (New-Object -ComObject WScript.Shell).CreateShortcut((Join-Path $desktop 'App.lnk'))
$legacyLink.TargetPath = $otherTarget
$legacyLink.Save()
$legacyTarget = Get-DesktopShortcutTarget (Join-Path $desktop 'App.lnk')
$chosen = Find-DesktopShortcutPath $desktop 'App' $ownTarget 'A'
[Auriga.Windows.DesktopShortcut]::Save($chosen, $ownTarget, $Root, 'App', '')
Remove-Item -LiteralPath (Join-Path $desktop 'App.lnk')
$retained = Find-DesktopShortcutPath $desktop 'App' $ownTarget 'A'
$label = [Auriga.Windows.DesktopShortcut]::UninstallLabel('App')
$unicodeLink = Join-Path $desktop "$label.lnk"
$foreignLink = Join-Path $desktop "$label (Other).lnk"
[Auriga.Windows.DesktopShortcut]::Save($unicodeLink, $removeTarget, $Root, $label, '')
[Auriga.Windows.DesktopShortcut]::Save($foreignLink, $otherTarget, $Other, $label, '')
$unicodeTarget = Get-DesktopShortcutTarget $unicodeLink
$unicodeName = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([IO.Path]::GetFileName($unicodeLink)))
# The core has already removed bin when the real uninstaller reads these links.
Remove-Item -LiteralPath $ownTarget, $removeTarget
Remove-DesktopOwnedShortcuts $Root @($retained, $unicodeLink, $foreignLink)
$removed = -not ((Test-Path -LiteralPath $retained) -or (Test-Path -LiteralPath $unicodeLink))
$foreignKept = Test-Path -LiteralPath $foreignLink
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
@{
  ownership = $ownership; chosen = $chosen; retained = $retained; new = $new; old = $old; maintenance = $maintenance
  legacyTarget = $legacyTarget; unicodeTarget = $unicodeTarget; unicodeName = $unicodeName; removed = $removed; foreignKept = $foreignKept
} | ConvertTo-Json -Compress -Depth 4
