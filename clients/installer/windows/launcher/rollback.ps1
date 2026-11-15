$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'maintenance.ps1')
$version = Get-DesktopMaintenanceVersion $root
& (Join-Path $version 'upstream\node\node.exe') (Join-Path $version 'auriga\runtime\cli.mjs') rollback --root $root
exit $LASTEXITCODE
