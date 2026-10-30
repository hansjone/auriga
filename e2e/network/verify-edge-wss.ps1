param([string]$ExpectedHead, [switch]$CompileOnly, [switch]$Run, [string]$MavenCommand = '.\mvnw.cmd')
$ErrorActionPreference = 'Stop'
if (($CompileOnly -and $Run) -or (-not $CompileOnly -and -not $Run)) { throw 'Choose CompileOnly or Run' }
$root = (Get-Location).Path
if ($ExpectedHead -notmatch '^[a-f0-9]{40}$' -or (& git rev-parse HEAD).Trim() -cne $ExpectedHead) { throw 'Exact HEAD required' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
$output = Join-Path $root ('e2e/network/target/verify-edge-wss-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
& git archive --format=tar --output=$archive $ExpectedHead
if ($LASTEXITCODE -ne 0) { throw 'Archive failed' }
$tests = @(& git ls-files 'e2e/network/*' 'clients/dsh-plugin/test/network/*')
$sources = @(& git ls-files 'infra/edge/*.go' 'infra/ai-gateway/*.go' 'services/platform-app/src/test/java/auriga/iam/NetworkSource*' 'clients/dsh-plugin/src/network/*')
$manifest = [ordered]@{ head = $ExpectedHead; archiveSha256 = (Get-FileHash $archive -Algorithm SHA256).Hash; mode = $(if ($Run) {'Run'} else {'CompileOnly'}); variants = @(); complete = $false }
$utf8 = New-Object Text.UTF8Encoding($false)
function Save { [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 20), $utf8) }
function Hashes($directory, $paths) { $result = [ordered]@{}; foreach ($path in $paths) { $result[$path] = (Get-FileHash (Join-Path $directory $path) -Algorithm SHA256).Hash }; return $result }
function Recorded($name, $command, [scriptblock]$action) {
  $log = Join-Path $output ($mode + '-' + $name + '.log')
  $saved = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
  try { & $action *> $log; $code = $LASTEXITCODE } finally { $ErrorActionPreference = $saved }
  if ($null -eq $code) { throw 'Missing process exit' }
  $entry.commands += [ordered]@{ command = $command; exit = [int]$code; log = $log }; Save
  return [int]$code
}
try {
  foreach ($mode in @('Control', 'BypassUpstreamTLS')) {
    $directory = Join-Path $output $mode; New-Item -ItemType Directory $directory | Out-Null
    & tar -xf $archive -C $directory; if ($LASTEXITCODE -ne 0) { throw 'Extract failed' }
    $entry = [ordered]@{ name = $mode; tests = (Hashes $directory $tests); originalSources = (Hashes $directory $sources); sources = $null; commands = @(); evidence = $null }
    if ($mode -eq 'BypassUpstreamTLS') {
      & node (Join-Path $directory 'e2e/network/support/edge-wss-verdict.mjs') --mutate (Join-Path $directory 'infra/edge/network_wss_tls.go')
      if ($LASTEXITCODE -ne 0) { throw 'Exact single production overlay failed' }
    }
    $entry.sources = Hashes $directory $sources; $manifest.variants += $entry; Save
    $changed = @($sources | Where-Object { $entry.sources[$_] -cne $entry.originalSources[$_] })
    if ($changed.Count -ne $(if ($mode -eq 'Control') {0} else {1}) -or ($changed.Count -eq 1 -and $changed[0] -ne 'infra/edge/network_wss_tls.go')) { throw 'Wrong production mutation' }
    if (($entry.tests | ConvertTo-Json -Compress) -cne ($manifest.variants[0].tests | ConvertTo-Json -Compress)) { throw 'Tests differ' }
    if (($entry.originalSources | ConvertTo-Json -Compress) -cne ($manifest.variants[0].originalSources | ConvertTo-Json -Compress)) { throw 'Original sources differ' }
    Push-Location $directory
    try {
      $savedCI = $env:CI; $env:CI = 'true'
      try { $code = Recorded 'install' 'pnpm frozen filtered install' { & pnpm install --frozen-lockfile --filter 'auriga-dsh...' --store-dir $env:AURIGA_PNPM_STORE } } finally { $env:CI = $savedCI }
      if ($code -ne 0) { throw 'Dependencies failed' }
      $code = Recorded 'guards' 'node --test e2e/network/edge-wss.test.mjs' { & node --test e2e/network/edge-wss.test.mjs }; if ($code -ne 0) { throw 'Guards failed' }
      Push-Location clients/dsh-plugin
      try { $code = Recorded 'typecheck' 'tsc --noEmit' { & node ../../node_modules/typescript/bin/tsc --noEmit -p tsconfig.json } } finally { Pop-Location }
      if ($code -ne 0) { throw 'Typecheck failed' }
      $code = Recorded 'build' 'node edge-wss-build --build' { & node e2e/network/support/edge-wss-build.mjs --build e2e/network/target/edge-wss-bin }; if ($code -ne 0) { throw 'Go compile failed' }
      $code = Recorded 'java' 'Maven non-PG bridge tests' { & $MavenCommand -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false' }; if ($code -ne 0) { throw 'Java compile/non-PG tests failed' }
      if ($Run) {
        $code = Recorded 'run' 'node e2e/network/source-edge-wss.mjs --run' { & node e2e/network/source-edge-wss.mjs --run }
        $evidence = @(Get-ChildItem (Join-Path $directory 'e2e/network/target') -Directory -Filter 'edge-wss-*' | Where-Object { Test-Path (Join-Path $_.FullName 'manifest.json') })
        if ($evidence.Count -ne 1) { throw 'One exact business manifest required' }
        $entry.evidence = Join-Path $evidence[0].FullName 'manifest.json'
        if ((Get-Content -Raw $entry.evidence | ConvertFrom-Json).head -cne $ExpectedHead) { throw 'Business manifest HEAD mismatch' }
        & node e2e/network/support/edge-wss-verdict.mjs --verdict $entry.evidence $mode $code
        if ($LASTEXITCODE -ne 0) { throw 'Business verdict rejected; initialization/cleanup do not count' }
      }
      if (((Hashes $directory $tests) | ConvertTo-Json -Compress) -cne ($entry.tests | ConvertTo-Json -Compress)) { throw 'Tests mutated during run' }
    } finally { Pop-Location; Save }
  }
  $manifest.complete = $true
} finally { Save }
Write-Output "Edge WSS evidence: $output"
if ($CompileOnly) { Write-Output 'CompileOnly: no PG, business acceptance or counterevidence claimed' }
