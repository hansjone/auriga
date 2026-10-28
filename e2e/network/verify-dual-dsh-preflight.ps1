param([Parameter(Mandatory=$true)][string]$ExpectedHead, [switch]$Run)
$ErrorActionPreference = 'Stop'
if (-not $Run) { throw 'Explicit -Run required; this runs only the SEND-4 routing precondition, not dual DSH acceptance' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root' }
if ($ExpectedHead -notmatch '^[a-f0-9]{40}$' -or (& git rev-parse HEAD).Trim() -cne $ExpectedHead) { throw 'Exact HEAD required' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
$output = Join-Path $root ('e2e/network/target/verify-dual-dsh-preflight-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
& git archive --format=tar --output=$archive $ExpectedHead
if ($LASTEXITCODE -ne 0) { throw 'Cannot archive exact HEAD' }
$directory = Join-Path $output 'Control'
New-Item -ItemType Directory -Path $directory | Out-Null
& tar -xf $archive -C $directory
if ($LASTEXITCODE -ne 0) { throw 'Cannot extract own archive' }
$utf8 = New-Object Text.UTF8Encoding($false)
$test = Join-Path $directory 'e2e/network/support/send4-platform-origin_test.go'
$overlay = Join-Path $output 'overlay.json'
$replacement = @{}
$replacement[(Join-Path $directory 'infra/edge/send4_platform_origin_test.go')] = $test
[IO.File]::WriteAllText($overlay, (@{Replace=$replacement} | ConvertTo-Json -Depth 5), $utf8)
$sources = [ordered]@{}
foreach ($path in @('infra/edge/gateway.go', 'infra/edge/device_proxy.go', 'clients/dsh-plugin/src/network/device-provider.js')) {
  $sources[$path] = (Get-FileHash -LiteralPath (Join-Path $directory $path) -Algorithm SHA256).Hash
}
$log = Join-Path $output 'edge-routing.log'
$manifest = [ordered]@{
  head = $ExpectedHead
  scope = 'routing precondition only; no dual DSH, login, PG, SEND acceptance or business mutant'
  archiveSha256 = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
  testSha256 = (Get-FileHash -LiteralPath $test -Algorithm SHA256).Hash
  sources = $sources
  command = "go test -overlay <overlay.json> -count=1 -run ^TestSEND4PlatformOrigin$ -v ."
  cwd = (Join-Path $directory 'infra/edge')
  exit = $null
  log = $log
  complete = $false
}
try {
  Push-Location $manifest.cwd
  try {
    $saved = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & go test -overlay $overlay -count=1 -run '^TestSEND4PlatformOrigin$' -v . *> $log; $code = $LASTEXITCODE } finally { $ErrorActionPreference = $saved }
    if ($null -eq $code) { throw 'Missing native exit code' }
    $manifest.exit = [int]$code
    $manifest.complete = $code -eq 0
  } finally { Pop-Location }
} finally {
  [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 10), $utf8)
  Write-Output "SEND-4 routing precondition evidence: $output"
}
if (-not $manifest.complete) { throw 'SEND-4 routing precondition failed; inspect edge-routing.log; no E2E success claimed' }
Write-Output 'Routing precondition passed only; full SEND-4 remains a separate acceptance'
