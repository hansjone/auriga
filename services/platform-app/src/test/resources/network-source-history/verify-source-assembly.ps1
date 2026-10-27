param([string]$ExpectedHead, [switch]$CompileOnly, [switch]$Run)
$ErrorActionPreference = 'Stop'
if (($CompileOnly -and $Run) -or (-not $CompileOnly -and -not $Run)) { throw 'Choose CompileOnly or Run' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root' }
if ($ExpectedHead -notmatch '^[a-f0-9]{40}$' -or (& git rev-parse HEAD).Trim() -cne $ExpectedHead) { throw 'Exact HEAD required' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
if (-not $env:AURIGA_PNPM_STORE) { throw 'Keep the dispatched locked pnpm store environment' }
$output = Join-Path $root ('e2e/network/target/verify-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
& git archive --format=tar --output=$archive $ExpectedHead
if ($LASTEXITCODE -ne 0) { throw 'Cannot archive exact HEAD' }
$manifest = [ordered]@{ head = $ExpectedHead; archiveSha256 = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash; mode = $(if ($Run) {'Run'} else {'CompileOnly'}); variants = @(); complete = $false }
$testPath = 'e2e/network/support/assembly-plugin.mjs'
$sourcePath = 'clients/dsh-plugin/src/network/source-control/connection.js'
$utf8 = New-Object Text.UTF8Encoding($false)
function Save-Manifest { [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 12), $utf8) }
function Invoke-Recorded([string]$Name, [scriptblock]$Action) {
  $log = Join-Path $output ($Name + '.log')
  # Native stderr includes JAVA_TOOL_OPTIONS; preserve it without terminating the shell.
  $saved = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try { & $Action *> $log; $code = $LASTEXITCODE } finally { $ErrorActionPreference = $saved }
  if ($null -eq $code) { throw "Missing native exit for $Name" }
  return [int]$code
}
try {
  foreach ($mode in @('Control', 'SignatureBypass')) {
    $directory = Join-Path $output $mode
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if ($LASTEXITCODE -ne 0) { throw 'Cannot extract own source archive' }
    $testHash = (Get-FileHash -LiteralPath (Join-Path $directory $testPath) -Algorithm SHA256).Hash
    $sourceFile = Join-Path $directory $sourcePath
    $originalHash = (Get-FileHash -LiteralPath $sourceFile -Algorithm SHA256).Hash
    if ($mode -eq 'SignatureBypass') {
      & node (Join-Path $directory 'e2e/network/support/assembly-verdict.mjs') --mutate $sourceFile
      if ($LASTEXITCODE -ne 0) { throw 'Overlay was not exact' }
    }
    $entry = [ordered]@{ name = $mode; testSha256 = $testHash; originalSourceSha256 = $originalHash; sourceSha256 = (Get-FileHash -LiteralPath $sourceFile -Algorithm SHA256).Hash; compileExit = $null; runExit = $null; evidence = $null }
    $manifest.variants += $entry
    Save-Manifest
    Push-Location $directory
    try {
      $ciBefore = $env:CI
      $env:CI = 'true'
      try { $install = Invoke-Recorded ($mode + '-install') { & pnpm install --frozen-lockfile --filter 'auriga-dsh...' --store-dir $env:AURIGA_PNPM_STORE } } finally { $env:CI = $ciBefore }
      if ($install -ne 0) { throw 'Locked independent dependencies failed' }
      $entry.compileExit = Invoke-Recorded ($mode + '-typecheck') { & pnpm --filter auriga-dsh typecheck }
      if ($entry.compileExit -ne 0) { throw 'Mutant/control must typecheck before business execution' }
      if ((Get-FileHash -LiteralPath (Join-Path $directory $testPath) -Algorithm SHA256).Hash -cne $testHash) { throw 'Test source changed' }
      if ($Run) {
        $compiled = Invoke-Recorded ($mode + '-java') { & .\mvnw.cmd -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false' }
        if ($compiled -ne 0) { throw 'Fresh Java non-PG bridge compile/test failed' }
        $entry.runExit = Invoke-Recorded ($mode + '-run') { & node e2e/network/source-assembly.mjs --run }
        $evidence = @(Get-ChildItem -LiteralPath (Join-Path $directory 'e2e/network/target') -Directory -Filter 'assembly-*')
        if ($evidence.Count -ne 1) { throw 'Expected one exact run manifest' }
        $entry.evidence = Join-Path $evidence[0].FullName 'manifest.json'
        & node e2e/network/support/assembly-verdict.mjs --verdict $entry.evidence $mode $entry.runExit
        if ($LASTEXITCODE -ne 0) { throw 'Business evidence rejected; initialization/compile/SQL/cleanup failures never count' }
      }
    } finally { Pop-Location; Save-Manifest }
  }
  if ($manifest.variants[0].testSha256 -cne $manifest.variants[1].testSha256 -or
      $manifest.variants[0].originalSourceSha256 -cne $manifest.variants[1].originalSourceSha256 -or
      $manifest.variants[1].originalSourceSha256 -ceq $manifest.variants[1].sourceSha256) { throw 'Control/mutant identity mismatch' }
  $manifest.complete = $true
} finally { Save-Manifest }
Write-Output "Source assembly evidence: $output"
if ($CompileOnly) { Write-Output 'CompileOnly: no PG/business acceptance or counterevidence claimed' }
