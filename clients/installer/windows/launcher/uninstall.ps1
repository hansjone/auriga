# Removes Auriga Desktop. User data (profile, sessions) is kept unless -PurgeData is given.
param([switch]$PurgeData)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$cur = (Get-Content -LiteralPath (Join-Path $root 'current.txt') -TotalCount 1).Trim()
$ver = Join-Path $root "versions\$cur"
# Shortcuts and the Apps & features entry recorded at install time.
$shellFile = Join-Path $root 'shell.json'
if (Test-Path -LiteralPath $shellFile) {
  $shellInfo = Get-Content -Raw -LiteralPath $shellFile | ConvertFrom-Json
  foreach ($link in @($shellInfo.shortcuts)) { if ($link) { Remove-Item -LiteralPath $link -Force -ErrorAction SilentlyContinue } }
  if ($shellInfo.registryKey) { Remove-Item -LiteralPath "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$($shellInfo.registryKey)" -Recurse -Force -ErrorAction SilentlyContinue }
}
# Run from a temp copy so the running node.exe and scripts do not lock the install root.
$tmp = Join-Path ([IO.Path]::GetTempPath()) ('auriga-desktop-uninstall-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Copy-Item (Join-Path $ver 'upstream\node\node.exe') $tmp
  Copy-Item (Join-Path $ver 'auriga\runtime\*.mjs') $tmp
  $argList = @((Join-Path $tmp 'cli.mjs'), 'uninstall', '--root', $root)
  if ($PurgeData) { $argList += '--purge-data' }
  & (Join-Path $tmp 'node.exe') @argList
  if ($LASTEXITCODE -ne 0) { throw "uninstall failed ($LASTEXITCODE)" }
} finally {
  Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
