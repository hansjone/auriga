# Installs or upgrades Auriga DSH from this package. Needs no administrator rights.
param(
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\AurigaDSH'),
  [string]$DataHome = (Join-Path $env:LOCALAPPDATA 'AurigaDSH\home'),
  [string]$PlatformOrigin = ''
)
$ErrorActionPreference = 'Stop'
$payload = Join-Path $PSScriptRoot 'payload'
$node = Join-Path $payload 'upstream\node\node.exe'
$cli = Join-Path $payload 'auriga\runtime\cli.mjs'
$argList = @($cli, 'install', '--payload', $payload, '--root', $InstallDir, '--data-home', $DataHome)
if ($PlatformOrigin) { $argList += @('--platform-origin', $PlatformOrigin) }
& $node @argList
if ($LASTEXITCODE -ne 0) { throw "install failed ($LASTEXITCODE)" }
Write-Host "Installed to $InstallDir. Start with: $InstallDir\bin\auriga-dsh.cmd"
