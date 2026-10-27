param([Parameter(Mandatory=$true)][string]$StateDir)
$ErrorActionPreference = 'Stop'
Push-Location (Resolve-Path (Join-Path $PSScriptRoot '..\..'))
try {
  foreach ($directory in @('node_modules', 'web/node_modules', 'clients/dsh-plugin/node_modules')) {
    if ((Test-Path -LiteralPath $directory) -and (Get-Item -LiteralPath $directory).LinkType) {
      throw 'External dependency link requires lead cleanup'
    }
  }
  if (-not $env:AURIGA_PNPM_STORE) { throw 'AURIGA_PNPM_STORE must identify the approved dependency cache' }
  $env:CI = 'true'
  $env:VITE_AURIGA_MOCK = '0'
  & pnpm install --frozen-lockfile --filter auriga-dsh... --filter '@auriga/panel...' --store-dir $env:AURIGA_PNPM_STORE
  if ($LASTEXITCODE -ne 0) { throw 'Locked dependency installation failed' }
  & pnpm --filter auriga-dsh build
  if ($LASTEXITCODE -ne 0) { throw 'Plugin typecheck failed' }
  & node clients/dsh-plugin/scripts/package-ui.mjs
  if ($LASTEXITCODE -ne 0) { throw 'Plugin UI build failed' }
  Push-Location clients/dsh-plugin
  try {
    # UI preparation ran explicitly above; published tar installs need no build hooks.
    # Invoke the Node distribution's npm CLI; a stale user npm.ps1 must not redirect it.
    $nodeExecutable = (Get-Command node -ErrorAction Stop).Source
    $npmCli = Join-Path (Split-Path -Parent $nodeExecutable) 'node_modules/npm/bin/npm-cli.js'
    & $nodeExecutable $npmCli pack --ignore-scripts --pack-destination (Join-Path $StateDir 'packages') --cache (Join-Path $StateDir 'cache/npm-build')
    if ($LASTEXITCODE -ne 0) { throw 'Plugin pack failed' }
  } finally { Pop-Location }
} finally { Pop-Location }
