param([Parameter(Mandatory)][string]$LinkPath, [string]$TargetPath)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$shell = New-Object -ComObject WScript.Shell
if (-not $TargetPath -and -not (Test-Path -LiteralPath $LinkPath -PathType Leaf)) {
  throw 'Expected shortcut is missing.'
}
$link = $shell.CreateShortcut($LinkPath)
if ($TargetPath) {
  $link.TargetPath = $TargetPath
  $link.Save()
} else {
  $link.TargetPath
}
