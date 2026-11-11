# Removes the installation. User data (profile, sessions) is kept unless -PurgeData is given.
param([switch]$PurgeData)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$cur = (Get-Content -LiteralPath (Join-Path $root 'current.txt') -TotalCount 1).Trim()
$ver = Join-Path $root "versions\$cur"
# Run from a temp copy so the running node.exe and scripts do not lock the install root.
$tmp = Join-Path ([IO.Path]::GetTempPath()) ('auriga-dsh-uninstall-' + [guid]::NewGuid().ToString('N'))
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
