param([switch]$Run)
$ErrorActionPreference = 'Stop'
function Get-SourceHash([string]$path) {
  $algorithm = [Security.Cryptography.SHA256]::Create()
  try {
    return [pscustomobject]@{ Hash = [BitConverter]::ToString($algorithm.ComputeHash([IO.File]::ReadAllBytes($path))).Replace('-', '') }
  } finally { $algorithm.Dispose() }
}
# Run from the worktree root, with the normal build environment already sourced.
# Default: compile both isolated mutations. -Run: first require the unchanged control to pass,
# then require actual result-disclosure assertion failures from both identical-source test runs.
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from the worktree root' }
$head = (& git rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Cannot determine exact HEAD' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
$output = Join-Path $root ('services/platform-app/target/history-mutants-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
& git archive --format=tar "--output=$archive" $head
if ($LASTEXITCODE -ne 0) { throw 'git archive failed' }
$controlPath = 'services/collab/impl/src/main/java/auriga/collab/app/NetworkSourceControl.java'
$historyPath = 'services/collab/impl/src/main/java/auriga/collab/app/NetworkLocalSourceHistory.java'
$testPath = 'services/platform-app/src/test/java/auriga/iam/NetworkLocalSourceHistoryIntegrationTest.java'
$paths = @($controlPath, $historyPath, $testPath,
  'services/platform-app/src/test/java/auriga/collab/app/LocalHistoryTestAccess.java')
$original = @{}
foreach ($path in $paths) { $original[$path] = (Get-SourceHash (Join-Path $root $path)).Hash }
$manifest = [ordered]@{ head = $head; mode = $(if ($Run) { 'run' } else { 'compile-only' });
  sourceSha256 = $original; variants = @(); productionRestored = $false }
$utf8 = New-Object Text.UTF8Encoding($false)
function Invoke-Build([string]$directory, [string]$name, [string[]]$arguments) {
  Push-Location -LiteralPath $directory
  try {
    $log = Join-Path $output ($name + '.log')
    & .\mvnw.cmd @arguments *> $log
    $code = $LASTEXITCODE
    return [ordered]@{ log = $log; exitCode = $code }
  } finally { Pop-Location }
}
try {
  $variants = @('omit-historical-read', 'omit-complete-c-ref-equality')
  if ($Run) { $variants = @('control') + $variants }
  foreach ($name in $variants) {
    $directory = Join-Path $output $name
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if ($LASTEXITCODE -ne 0) { throw 'archive extraction failed' }
    foreach ($path in $paths) {
      if ((Get-SourceHash (Join-Path $directory $path)).Hash -ne $original[$path]) {
        throw "Archive differs from exact delivered source: $path"
      }
    }
    $changed = $null
    if ($name -eq 'omit-historical-read') {
      $changed = $controlPath
      $before = 'var proof = iam.readConnectionAnchor(prepared.history().anchor(), gate.anchor());'
      $after = 'var proof = auriga.contracts.iam.v1.ReadNetworkConnectionAnchorResponse.newBuilder().setOriginal(prepared.history().anchor()).setCurrent(gate.anchor()).build();'
    } elseif ($name -eq 'omit-complete-c-ref-equality') {
      $changed = $historyPath
      $before = 'original.equals(anchor.getConnection())'
      $after = 'true'
    }
    if ($changed) {
      $file = Join-Path $directory $changed
      $source = [IO.File]::ReadAllText($file)
      if ([regex]::Matches($source, [regex]::Escape($before)).Count -ne 1) { throw "Mutation did not match exactly once: $name" }
      [IO.File]::WriteAllText($file, $source.Replace($before, $after), $utf8)
    }
    $entry = [ordered]@{ name = $name; directory = $directory; changed = $changed;
      mutatedSha256 = $(if ($changed) { (Get-SourceHash (Join-Path $directory $changed)).Hash } else { $null });
      testSha256 = (Get-SourceHash (Join-Path $directory $testPath)).Hash }
    $entry.compile = Invoke-Build $directory ($name + '-compile') @('-B','-pl','services/platform-app','-am','test-compile')
    $manifest.variants += $entry
    if ($entry.compile.exitCode -ne 0) { throw "Compilation failure is NOT a killed mutant: $name" }
    if ($Run) {
      $selection = 'NetworkLocalSourceHistoryIntegrationTest'
      if ($name -eq 'omit-historical-read') { $selection += '#changedOriginalAssociationCannotRecoverFromAnOtherwiseAllowedC2' }
      if ($name -eq 'omit-complete-c-ref-equality') { $selection += '#completeOriginalTupleMustMatchItsAuthoritativeReferenceEvenWithResealedDigest' }
      # verify would stop at Spotless on deliberately unformatted mutants. test executes the exact
      # compiled production variant and identical test source without changing any build setting.
      $entry.execution = Invoke-Build $directory ($name + '-test') @('-B','-pl','services/platform-app','-am','test',
        "-Dtest=$selection",'-Dsurefire.failIfNoSpecifiedTests=false')
      $xmlPath = Join-Path $directory 'services/platform-app/target/surefire-reports/TEST-auriga.iam.NetworkLocalSourceHistoryIntegrationTest.xml'
      if (-not (Test-Path -LiteralPath $xmlPath)) { throw "No actual test XML: $name" }
      [xml]$xml = Get-Content -LiteralPath $xmlPath -Raw
      $entry.tests = [int]$xml.testsuite.tests
      $entry.failures = [int]$xml.testsuite.failures
      $entry.errors = [int]$xml.testsuite.errors
      $entry.skipped = [int]$xml.testsuite.skipped
      if ($entry.errors -ne 0 -or $entry.skipped -ne 0) { throw "Initialization/error/skip is NOT evidence: $name" }
      if ($name -eq 'control') {
        if ($entry.execution.exitCode -ne 0 -or $entry.failures -ne 0 -or $entry.tests -eq 0) { throw 'Unchanged control must first pass' }
      } else {
        $disclosure = @($xml.testsuite.testcase | Where-Object {
          $_.failure -and $_.failure.type -eq 'org.opentest4j.AssertionFailedError' -and
          $_.failure.message -match 'expected: <error> but was: <source_register_result>'
        })
        $entry.disclosureFailures = $disclosure.Count
        if ($entry.execution.exitCode -eq 0 -or $disclosure.Count -eq 0) { throw "No real old-result disclosure assertion failed: $name" }
      }
    }
  }
} finally {
  $unchanged = $true
  foreach ($path in $paths) {
    if ((Get-SourceHash (Join-Path $root $path)).Hash -ne $original[$path]) { $unchanged = $false }
  }
  $manifest.productionRestored = $unchanged
  $manifest | ConvertTo-Json -Depth 12 | Set-Content -Encoding utf8 -LiteralPath (Join-Path $output 'result.json')
  Write-Output "Evidence: $output"
  if (-not $unchanged) { throw 'Original worktree source changed' }
}
