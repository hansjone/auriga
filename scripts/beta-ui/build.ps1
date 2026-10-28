param([Parameter(Mandatory=$true)][string]$StateDir)
$ErrorActionPreference = 'Stop'
Push-Location (Resolve-Path (Join-Path $PSScriptRoot '..\..'))
try {
  foreach ($directory in @('node_modules', 'web/node_modules')) {
    if (Test-Path -LiteralPath $directory) {
      if ((Get-Item -LiteralPath $directory).LinkType) { throw 'External dependency link requires lead cleanup' }
    }
  }
  $env:CI = 'true'
  $env:VITE_AURIGA_MOCK = '0'
  if (-not $env:AURIGA_PNPM_STORE) { throw 'AURIGA_PNPM_STORE must identify the approved dependency cache' }
  & pnpm install --frozen-lockfile --store-dir $env:AURIGA_PNPM_STORE
  if ($LASTEXITCODE -ne 0) { throw 'Locked dependency installation failed' }
  foreach ($app in @('admin','portal')) {
    & pnpm --filter "@auriga/$app" build
    if ($LASTEXITCODE -ne 0) { throw "Static build failed: $app" }
    $destination = Join-Path $StateDir "static/$app"
    if (-not (Test-Path -LiteralPath $destination)) { [void](New-Item -ItemType Directory -Path $destination) }
    & (Join-Path $PSScriptRoot 'private.ps1') -Path $destination
    Get-ChildItem -LiteralPath "web/$app/dist" | Copy-Item -Destination $destination -Recurse -Force
  }
  & .\mvnw.cmd -B -pl services/platform-app -am package '-DskipTests=true'
  if ($LASTEXITCODE -ne 0) { throw 'Platform build failed' }
  Copy-Item -LiteralPath 'services/platform-app/target/platform-app-0.1.0-SNAPSHOT-exec.jar' -Destination (Join-Path $StateDir 'bin/platform.jar')
  foreach ($component in @('edge','ai-gateway')) {
    Push-Location "infra/$component"
    try {
      & go build -o (Join-Path $StateDir "bin/$component.exe") .
      if ($LASTEXITCODE -ne 0) { throw "Go build failed: $component" }
      if ($component -eq 'ai-gateway') {
        & go build -o (Join-Path $StateDir 'bin/network-broker.exe') github.com/nats-io/nats-server/v2
        if ($LASTEXITCODE -ne 0) { throw 'Locked network broker build failed' }
      }
    } finally { Pop-Location }
  }
} finally { Pop-Location }
