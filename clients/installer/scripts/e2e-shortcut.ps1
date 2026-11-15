param([Parameter(Mandatory)][string]$LinkPath, [string]$TargetPath)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
. (Join-Path $PSScriptRoot '..\windows\launcher\shell-integration.ps1')
if (-not $TargetPath -and -not (Test-Path -LiteralPath $LinkPath -PathType Leaf)) {
  throw 'Expected shortcut is missing.'
}
if ($TargetPath) {
  Initialize-DesktopShortcuts
  [Auriga.Windows.DesktopShortcut]::Save($LinkPath, $TargetPath, (Split-Path -Parent $TargetPath), '', '')
} else {
  Get-DesktopShortcutTarget $LinkPath
}
