param([string]$ExpectedHead, [switch]$CompileOnly, [switch]$Run, [string]$MavenCommand = '.\mvnw.cmd')
$ErrorActionPreference = 'Stop'
if (($CompileOnly -and $Run) -or (-not $CompileOnly -and -not $Run)) { throw 'Choose CompileOnly or Run' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root' }
if ($ExpectedHead -notmatch '^[a-f0-9]{40}$' -or (& git rev-parse HEAD).Trim() -cne $ExpectedHead) { throw 'Exact HEAD required' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
if (-not $env:AURIGA_PNPM_STORE) { throw 'Keep dispatched locked pnpm store environment' }
$output = Join-Path $root ('e2e/network/target/verify-plugin-send-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
& git archive --format=tar --output=$archive $ExpectedHead
if ($LASTEXITCODE -ne 0) { throw 'Cannot archive exact HEAD' }
$utf8 = New-Object Text.UTF8Encoding($false)
$testPaths = @('e2e/network/support/plugin-send.mjs', 'e2e/network/support/assembly-plugin.mjs', 'e2e/network/source-assembly.mjs')
$sourcePaths = @('clients/dsh-plugin/src/network/durable/outbox.js', 'clients/dsh-plugin/src/network/durable/inbox.js')
$manifest = [ordered]@{ head = $ExpectedHead; archiveSha256 = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash; mode = $(if ($Run) {'Run'} else {'CompileOnly'}); variants = @(); complete = $false }
function Save-Manifest { [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 20), $utf8) }
function Hashes([string]$Directory, [string[]]$Paths) {
  $result = [ordered]@{}
  foreach ($path in $Paths) { $result[$path] = (Get-FileHash -LiteralPath (Join-Path $Directory $path) -Algorithm SHA256).Hash }
  return $result
}
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
  foreach ($mode in @('Control', 'SkipSubmissionCommit', 'LocalReceiptVersion')) {
    $directory = Join-Path $output $mode
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if ($LASTEXITCODE -ne 0) { throw 'Cannot extract own archive' }
    $testHashes = Hashes $directory $testPaths
    $originalHashes = Hashes $directory $sourcePaths
    if ($mode -ne 'Control') {
      $changed = $(if ($mode -eq 'SkipSubmissionCommit') { $sourcePaths[0] } else { $sourcePaths[1] })
      & node (Join-Path $directory 'e2e/network/support/plugin-send-verdict.mjs') --mutate (Join-Path $directory $changed) $mode
      if ($LASTEXITCODE -ne 0) { throw 'Exact single-production mutation failed' }
    }
    $entry = [ordered]@{ name = $mode; tests = $testHashes; originalSources = $originalHashes; sources = (Hashes $directory $sourcePaths); commands = @(); evidence = $null }
    $manifest.variants += $entry
    Save-Manifest
    if (($testHashes | ConvertTo-Json -Compress) -cne ($manifest.variants[0].tests | ConvertTo-Json -Compress) -or ($originalHashes | ConvertTo-Json -Compress) -cne ($manifest.variants[0].originalSources | ConvertTo-Json -Compress)) { throw 'Archive source/test identity mismatch' }
    $changedCount = @($sourcePaths | Where-Object { $entry.sources[$_] -cne $entry.originalSources[$_] }).Count
    if ($changedCount -ne $(if ($mode -eq 'Control') {0} else {1})) { throw 'Only one production file may change per mutant' }
    Push-Location $directory
    try {
      $ciBefore = $env:CI; $env:CI = 'true'
      try { $installed = Invoke-Recorded ($mode + '-install') 'pnpm install --frozen-lockfile --filter auriga-dsh... --store-dir $AURIGA_PNPM_STORE' { & pnpm install --frozen-lockfile --filter 'auriga-dsh...' --store-dir $env:AURIGA_PNPM_STORE } } finally { $env:CI = $ciBefore }
      if ($installed -ne 0) { throw 'Locked independent dependencies failed' }
      Push-Location clients/dsh-plugin
      try { $typed = Invoke-Recorded ($mode + '-typecheck') 'node ../../node_modules/typescript/bin/tsc --noEmit -p tsconfig.json' { & node ../../node_modules/typescript/bin/tsc --noEmit -p tsconfig.json } } finally { Pop-Location }
      if ($typed -ne 0) { throw 'Control/mutant typecheck must pass' }
      $compiled = Invoke-Recorded ($mode + '-java') "$MavenCommand -B -pl services/platform-app -am test -Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest -Dsurefire.failIfNoSpecifiedTests=false" { & $MavenCommand -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false' }
      if ($compiled -ne 0) { throw 'Fresh Java bridge compile/test failed' }
      Push-Location infra/ai-gateway
      try { $built = Invoke-Recorded ($mode + '-go') 'go build ./...' { & go build ./... } } finally { Pop-Location }
      if ($built -ne 0) { throw 'Gateway bridge build must pass' }
      if (((Hashes $directory $testPaths) | ConvertTo-Json -Compress) -cne ($testHashes | ConvertTo-Json -Compress)) { throw 'Test sources changed' }
      if ($Run) {
        $runExit = Invoke-Recorded ($mode + '-run') 'node e2e/network/source-plugin-send.mjs --run' { & node e2e/network/source-plugin-send.mjs --run }
        $evidence = @(Get-ChildItem -LiteralPath (Join-Path $directory 'e2e/network/target') -Directory -Filter 'assembly-*')
        if ($evidence.Count -ne 1) { throw 'Expected one exact run manifest' }
        $entry.evidence = Join-Path $evidence[0].FullName 'manifest.json'
        & node e2e/network/support/plugin-send-verdict.mjs --verdict $entry.evidence $mode $runExit
        if ($LASTEXITCODE -ne 0) { throw 'Business evidence rejected; setup/SQL/cleanup failures never count' }
      }
    } finally { Pop-Location; Save-Manifest }
  }
  $manifest.complete = $true
} finally { Save-Manifest }
Write-Output "Plugin SEND evidence: $output"
if ($CompileOnly) { Write-Output 'CompileOnly: no PG/business acceptance or counterevidence claimed' }
