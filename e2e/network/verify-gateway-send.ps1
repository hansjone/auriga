param([string]$ExpectedHead, [switch]$CompileOnly, [switch]$Run, [string]$MavenCommand = '.\mvnw.cmd')
$ErrorActionPreference = 'Stop'
if (($CompileOnly -and $Run) -or (-not $CompileOnly -and -not $Run)) { throw 'Choose CompileOnly or Run' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root' }
if ($ExpectedHead -notmatch '^[a-f0-9]{40}$' -or (& git rev-parse HEAD).Trim() -cne $ExpectedHead) { throw 'Exact HEAD required' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
if (-not $env:AURIGA_PNPM_STORE) { throw 'Keep dispatched locked pnpm store environment' }
$output = Join-Path $root ('e2e/network/target/verify-send-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
& git archive --format=tar --output=$archive $ExpectedHead
if ($LASTEXITCODE -ne 0) { throw 'Cannot archive exact HEAD' }
$utf8 = New-Object Text.UTF8Encoding($false)
$testPath = 'e2e/network/support/gateway-send.mjs'
$sourcePath = 'infra/ai-gateway/internal/network/submit.go'
$manifest = [ordered]@{ head = $ExpectedHead; archiveSha256 = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash; mode = $(if ($Run) {'Run'} else {'CompileOnly'}); variants = @(); complete = $false }
function Save-Manifest { [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 16), $utf8) }
function Invoke-Recorded([string]$Name, [string]$Command, [scriptblock]$Action) {
  $log = Join-Path $output ($Name + '.log')
  $saved = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try { & $Action *> $log; $code = $LASTEXITCODE } finally { $ErrorActionPreference = $saved }
  if ($null -eq $code) { throw "Missing native exit for $Name" }
  $entry.commands += [ordered]@{ command = $Command; exit = [int]$code; log = $log }
  Save-Manifest
  return [int]$code
}
try {
  foreach ($mode in @('Control', 'TrustClientIdentity')) {
    $directory = Join-Path $output $mode
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if ($LASTEXITCODE -ne 0) { throw 'Cannot extract own archive' }
    $testHash = (Get-FileHash -LiteralPath (Join-Path $directory $testPath) -Algorithm SHA256).Hash
    $sourceFile = Join-Path $directory $sourcePath
    $originalHash = (Get-FileHash -LiteralPath $sourceFile -Algorithm SHA256).Hash
    if ($mode -eq 'TrustClientIdentity') {
      & node (Join-Path $directory 'e2e/network/support/gateway-send-verdict.mjs') --mutate $sourceFile
      if ($LASTEXITCODE -ne 0) { throw 'Identity overlay was not exact' }
    }
    $entry = [ordered]@{ name = $mode; testSha256 = $testHash; originalSourceSha256 = $originalHash; sourceSha256 = (Get-FileHash -LiteralPath $sourceFile -Algorithm SHA256).Hash; commands = @(); evidence = $null }
    $manifest.variants += $entry
    Save-Manifest
    Push-Location $directory
    try {
      $ciBefore = $env:CI
      $env:CI = 'true'
      try { $installed = Invoke-Recorded ($mode + '-install') 'pnpm install --frozen-lockfile --filter auriga-dsh... --store-dir $AURIGA_PNPM_STORE' { & pnpm install --frozen-lockfile --filter 'auriga-dsh...' --store-dir $env:AURIGA_PNPM_STORE } } finally { $env:CI = $ciBefore }
      if ($installed -ne 0) { throw 'Locked independent dependencies failed' }
      $compiled = Invoke-Recorded ($mode + '-java') "$MavenCommand -B -pl services/platform-app -am test -Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest -Dsurefire.failIfNoSpecifiedTests=false" { & $MavenCommand -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false' }
      if ($compiled -ne 0) { throw 'Fresh Java bridge compile/test failed' }
      Push-Location infra/ai-gateway
      try { $goBuilt = Invoke-Recorded ($mode + '-go') 'go build ./...' { & go build ./... } } finally { Pop-Location }
      if ($goBuilt -ne 0) { throw 'Control/mutant must compile before business execution' }
      if ((Get-FileHash -LiteralPath (Join-Path $directory $testPath) -Algorithm SHA256).Hash -cne $testHash) { throw 'Test source changed' }
      if ($Run) {
        $runExit = Invoke-Recorded ($mode + '-run') 'node e2e/network/source-gateway-send.mjs --run' { & node e2e/network/source-gateway-send.mjs --run }
        $evidence = @(Get-ChildItem -LiteralPath (Join-Path $directory 'e2e/network/target') -Directory -Filter 'assembly-*')
        if ($evidence.Count -ne 1) { throw 'Expected one exact run manifest' }
        $entry.evidence = Join-Path $evidence[0].FullName 'manifest.json'
        & node e2e/network/support/gateway-send-verdict.mjs --verdict $entry.evidence $mode $runExit
        if ($LASTEXITCODE -ne 0) { throw 'Business evidence rejected; setup/SQL/cleanup failures never count' }
      }
    } finally { Pop-Location; Save-Manifest }
  }
  if ($manifest.variants[0].testSha256 -cne $manifest.variants[1].testSha256 -or $manifest.variants[0].originalSourceSha256 -cne $manifest.variants[1].originalSourceSha256 -or $manifest.variants[1].originalSourceSha256 -ceq $manifest.variants[1].sourceSha256) { throw 'Control/mutant identity mismatch' }
  $manifest.complete = $true
} finally { Save-Manifest }
Write-Output "Gateway SEND evidence: $output"
if ($CompileOnly) { Write-Output 'CompileOnly: no PG/business acceptance or counterevidence claimed' }
