param(
  [Parameter(Mandatory=$true)][ValidateSet('init','init-db','build','start','stop','status')][string]$Action,
  [string]$StateDir = (Join-Path $env:LOCALAPPDATA 'Auriga\beta-ui'),
  [string]$PgPassFile,
  [string]$Psql = 'psql.exe',
  [int]$PgPort = 5432
)
$ErrorActionPreference = 'Stop'
Push-Location (Resolve-Path (Join-Path $PSScriptRoot '..\..'))
try {
  $arguments = @((Join-Path $PSScriptRoot 'runner.mjs'), $Action, '--state', [IO.Path]::GetFullPath($StateDir), '--psql', $Psql, '--pg-port', "$PgPort")
  if ($PgPassFile) { $arguments += @('--pg-passfile', [IO.Path]::GetFullPath($PgPassFile)) }
  & node @arguments
  if ($LASTEXITCODE -ne 0) { throw "Beta command failed (exit $LASTEXITCODE). See private logs in StateDir." }
} finally { Pop-Location }
