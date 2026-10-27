param([string]$ExpectedHead, [switch]$Run, [switch]$CompileOnly, [switch]$ErrorAsFailureOnly, [switch]$FunctionsOnly)
$ErrorActionPreference = 'Stop'

function Get-FenceHash([string]$Path) {
  $algorithm = [Security.Cryptography.SHA256]::Create()
  try { return [BitConverter]::ToString($algorithm.ComputeHash([IO.File]::ReadAllBytes($Path))).Replace('-', '').ToLowerInvariant() }
  finally { $algorithm.Dispose() }
}

function Get-FenceModes([bool]$Execute, [bool]$ReadErrorOnly) {
  if ($ReadErrorOnly) { return @('ErrorAsFailure') }
  if ($Execute) { return @('Control', 'FenceAsRegister', 'ErrorAsFailure') }
  return @('FenceAsRegister', 'ErrorAsFailure')
}

function Assert-FenceReadErrorOverlay([string]$Original, [string]$Mutated) {
  $read = '      return iam.readSourceReceipt(original);'
  $start = $Original.IndexOf($read, [StringComparison]::Ordinal)
  $method = $Original.IndexOf('  private RegisterAgentSourceResponse recoverRegister(', [StringComparison]::Ordinal)
  $end = $Original.IndexOf('  private static void fenceBudget()', [StringComparison]::Ordinal)
  if ([regex]::Matches($Original, [regex]::Escape($read)).Count -ne 1 -or
      $method -lt 0 -or $start -le $method -or $end -le $start) {
    throw 'Read-error overlay must target exactly one read inside recoverRegister'
  }
  $prefix = $Original.Substring(0, $start)
  $suffix = $Original.Substring($start + $read.Length)
  if ($Mutated.Length -le ($prefix.Length + $suffix.Length) -or
      -not $Mutated.StartsWith($prefix, [StringComparison]::Ordinal) -or
      -not $Mutated.EndsWith($suffix, [StringComparison]::Ordinal)) {
    throw 'Read-error overlay changed initial Register/catch or code outside the recovery read'
  }
  $injected = $Mutated.Substring($prefix.Length, $Mutated.Length - $prefix.Length - $suffix.Length)
  if ([regex]::Matches($injected, 'iam\.readSourceReceipt\(original\)').Count -ne 1 -or
      -not $injected.Contains('catch (RuntimeException readError)')) {
    throw 'Read-error overlay must retain the read and intercept only its failure'
  }
}

function New-FenceReadErrorOverlay([string]$Source) {
  $before = '      return iam.readSourceReceipt(original);'
  $after = @'
      try {
        return iam.readSourceReceipt(original);
      } catch (RuntimeException readError) {
        return RegisterAgentSourceResponse.newBuilder().setDefinitivelyUncommitted(true)
            .setOperationId(original.getOperationId())
            .setRequestDigest(pending.get("request_digest").toString())
            .setRejectionReason("original_connection_invalid_uncommitted").build();
      }
'@
  $after = $after.Replace("`r`n", "`n")
  $mutated = $Source.Replace($before, $after)
  Assert-FenceReadErrorOverlay $Source $mutated
  return $mutated
}

function Get-FenceSummary([string]$Path) {
  [xml]$xml = [IO.File]::ReadAllText($Path)
  $suite = $xml.testsuite
  $cases = @($suite.testcase)
  return [ordered]@{
    tests = [int]$suite.tests; failures = [int]$suite.failures; errors = [int]$suite.errors
    skipped = [int]$suite.skipped; disabled = [int]$suite.disabled; cases = $cases
  }
}

function Assert-FenceVerdict($Summary, [string]$Mode, [int]$ExitCode) {
  if ($Summary.tests -lt 1 -or $Summary.tests -ne $Summary.cases.Count -or
      $Summary.errors -ne 0 -or $Summary.skipped -ne 0 -or $Summary.disabled -ne 0 -or
      @($Summary.cases | Where-Object { $_.SelectNodes('error|skipped|disabled').Count -ne 0 }).Count -ne 0) {
    throw 'Empty/error/skip/disabled evidence is not a behavioral verdict'
  }
  $failures = @($Summary.cases | Where-Object { $_.SelectNodes('failure').Count -ne 0 })
  if ($Summary.failures -ne $failures.Count) { throw 'Failure count differs from actual cases' }
  if ($Mode -eq 'Control') {
    if ($ExitCode -ne 0 -or $failures.Count -ne 0) { throw 'Unchanged Control must pass first' }
    return
  }
  $expected = switch ($Mode) {
    'FenceAsRegister' { 'liveOriginalMissCannotCreateSourceOrDurableFence' }
    'ErrorAsFailure' { 'readErrorsNeverEnterFenceOrCreateAnInventedNegativeReceipt' }
    default { throw "Unknown mode: $Mode" }
  }
  $message = if ($Mode -eq 'FenceAsRegister') { 'live original miss must not create an IAM source' }
    else { 'ordinary failure must retain durable pending' }
  if ($ExitCode -eq 0 -or $failures.Count -ne $Summary.tests) { throw 'Every selected mutant case must fail the designated assertion' }
  foreach ($case in $failures) {
    if ($case.name -notlike "$expected*" -or
        $case.failure.type -ne 'org.opentest4j.AssertionFailedError' -or
        $case.failure.message -notlike "*$message*") {
      throw 'Only designated persistent business AssertionFailedError is accepted'
    }
  }
}

function Assert-FenceTree([string]$Directory, $Hashes, [string]$Overlay, [string]$OverlayHash) {
  foreach ($path in $Hashes.Keys) {
    $expected = if ($path -eq $Overlay) { $OverlayHash } else { $Hashes[$path] }
    if ((Get-FenceHash (Join-Path $Directory $path)) -ne $expected) {
      throw "Tree differs from exact HEAD plus single production overlay: $path"
    }
  }
  $pending = New-Object 'Collections.Generic.Stack[string]'
  $pending.Push($Directory)
  while ($pending.Count -gt 0) {
    foreach ($item in Get-ChildItem -LiteralPath $pending.Pop() -Force) {
      if ($item.PSIsContainer) {
        if ($item.Name -notin @('target', 'node_modules', '.git')) { $pending.Push($item.FullName) }
      } else {
        $relative = $item.FullName.Substring($Directory.Length + 1).Replace('\', '/')
        if (-not $Hashes.ContainsKey($relative) -and $relative -ne '.mvn/wrapper/maven-wrapper.jar') {
          throw "Unexpected extra archive source file: $relative"
        }
      }
    }
  }
}

function Assert-FenceOldHistoryCoverage($Summary, $Coverage) {
  $expected = @($Coverage.caseKeys)
  if ($Coverage.oldControlCases -ne 37 -or $expected.Count -ne 37 -or
      @($expected | Select-Object -Unique).Count -ne 37) {
    throw 'Expected exact 37 distinct real IAM joint case keys, not collab local 32+5'
  }
  $actual = @($Summary.cases | ForEach-Object { $_.classname + '#' + $_.name })
  foreach ($key in $expected) {
    if (@($actual | Where-Object { $_ -ceq $key }).Count -ne 1) {
      throw "Control must execute each original real IAM joint case exactly once: $key"
    }
  }
}

if ($FunctionsOnly) { return }
if ($Run -and $CompileOnly) { throw 'Choose Run or CompileOnly' }
if (-not $ExpectedHead -or $ExpectedHead -notmatch '^[0-9a-f]{40}$') { throw 'Supply exact 40-character ExpectedHead' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root after sourcing auriga-env.ps1' }
$head = (& git rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $head -ne $ExpectedHead) { throw 'HEAD does not equal ExpectedHead' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
$tracked = @(& git ls-tree -r --name-only $head)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inventory HEAD blobs' }
$output = Join-Path $root ('target/fm-' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $output | Out-Null
$utf8 = New-Object Text.UTF8Encoding($false)
$archive = Join-Path $output 'source.tar'
$production = 'services/collab/impl/src/main/java/auriga/collab/app/NetworkSourceControl.java'
$test = 'services/platform-app/src/test/java/auriga/iam/NetworkLocalSourceHistoryIntegrationTest.java'
$oldCoveragePath = 'services/platform-app/src/test/resources/network-source-history/old-history-cases.json'
$modes = @(Get-FenceModes ([bool]$Run) ([bool]$ErrorAsFailureOnly))
$manifest = [ordered]@{ head = $head; mode = $(if ($Run) { 'Run' } else { 'CompileOnly' })
  scope = $(if ($ErrorAsFailureOnly) { 'ErrorAsFailureOnly' } else { 'Full' }); requestedModes = $modes
  sourceHashes = @{}; worktreeHashes = @{}; archiveSha256 = $null; testBlob = $null; testSha256 = $null
  variants = @(); compileOnlyExitCodes = @{}; firstFailure = $null; worktreeUnchanged = $false }
$events = Join-Path $output 'events.jsonl'
function Write-FenceEvent($Event) { [IO.File]::AppendAllText($events, ($Event | ConvertTo-Json -Depth 12 -Compress) + "`n", $utf8) }

function Invoke-FenceBuild([string]$Directory, [string]$Name, [string[]]$MavenArgs) {
  $stdout = Join-Path $output ($Name + '.stdout.log')
  $stderr = Join-Path $output ($Name + '.stderr.log')
  $runner = Join-Path $output ($Name + '-command.ps1')
  $argText = ($MavenArgs | ForEach-Object { "'" + $_.Replace("'", "''") + "'" }) -join ','
  [IO.File]::WriteAllText($runner, "`$ErrorActionPreference='Continue'`n& .\mvnw.cmd @($argText)`nexit `$LASTEXITCODE`n", $utf8)
  $shell = Join-Path $PSHOME 'powershell.exe'
  if (-not (Test-Path -LiteralPath $shell)) { $shell = Join-Path $PSHOME 'pwsh.exe' }
  $command = [ordered]@{ directory = $Directory; executable = $shell; script = $runner; args = $MavenArgs; stdout = $stdout; stderr = $stderr }
  Write-FenceEvent @{ phase = 'start'; name = $Name; command = $command }
  $info = New-Object Diagnostics.ProcessStartInfo
  $info.FileName = $shell
  $info.Arguments = '-NoLogo -NoProfile -File "' + $runner + '"'
  $info.WorkingDirectory = $Directory
  $info.UseShellExecute = $false
  $info.CreateNoWindow = $true
  $info.RedirectStandardOutput = $true
  $info.RedirectStandardError = $true
  $process = New-Object Diagnostics.Process
  $process.StartInfo = $info
  try {
    if (-not $process.Start()) { throw 'Build child failed to start' }
    $outTask = $process.StandardOutput.ReadToEndAsync()
    $errTask = $process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    [IO.File]::WriteAllText($stdout, $outTask.GetAwaiter().GetResult(), $utf8)
    [IO.File]::WriteAllText($stderr, $errTask.GetAwaiter().GetResult(), $utf8)
    $code = [int]$process.ExitCode
    Write-FenceEvent @{ phase = 'finish'; name = $Name; exitCode = $code }
    return [ordered]@{ command = $command; exitCode = $code }
  } finally { $process.Dispose() }
}

try {
  & git archive --format=tar "--output=$archive" $head
  if ($LASTEXITCODE -ne 0) { throw 'git archive failed' }
  $manifest.archiveSha256 = Get-FenceHash $archive
  $manifest.testBlob = (& git rev-parse "${head}:$test").Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Cannot pin HEAD test blob' }
  $baseline = Join-Path $output 's'
  New-Item -ItemType Directory -Path $baseline | Out-Null
  & tar -xf $archive -C $baseline
  if ($LASTEXITCODE -ne 0) { throw 'Baseline archive extraction failed' }
  foreach ($path in $tracked) {
    $manifest.sourceHashes[$path] = Get-FenceHash (Join-Path $baseline $path)
    $manifest.worktreeHashes[$path] = Get-FenceHash (Join-Path $root $path)
  }
  $manifest.testSha256 = $manifest.sourceHashes[$test]
  if ($manifest.testSha256 -ne $manifest.worktreeHashes[$test]) { throw 'Delivered Java test bytes differ from HEAD archive blob' }
  foreach ($mode in $modes) {
    $leaf = switch ($mode) { 'Control' { 'c' }; 'FenceAsRegister' { 'a' }; default { 'b' } }
    $directory = Join-Path $output $leaf
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if ($LASTEXITCODE -ne 0) { throw 'archive extraction failed' }
    Assert-FenceTree $directory $manifest.sourceHashes $null $null
    $overlay = $null; $overlayHash = $null
    if ($mode -ne 'Control') {
      $overlay = $production
      $path = Join-Path $directory $production
      $source = [IO.File]::ReadAllText($path).Replace("`r`n", "`n")
      if ($mode -eq 'FenceAsRegister') {
        $before = 'return iam.fenceSourceReceipt(original);'
        $after = 'return iam.registerSource(original);'
        if ([regex]::Matches($source, [regex]::Escape($before)).Count -ne 1) { throw "Overlay must match exactly once: $mode" }
        $mutated = $source.Replace($before, $after)
      } else {
        $mutated = New-FenceReadErrorOverlay $source
      }
      [IO.File]::WriteAllText($path, $mutated, $utf8)
      $overlayHash = Get-FenceHash $path
    }
    Assert-FenceTree $directory $manifest.sourceHashes $overlay $overlayHash
    $entry = [ordered]@{ mode = $mode; directory = $directory; overlay = $overlay; overlaySha256 = $overlayHash
      testSha256 = (Get-FenceHash (Join-Path $directory $test)); compile = $null; execution = $null; summary = $null; sourceGuardAfter = $false }
    $manifest.variants += $entry
    $entry.compile = Invoke-FenceBuild $directory ($mode + '-compile') @('-B', '-pl', 'services/platform-app', '-am', 'test-compile')
    $manifest.compileOnlyExitCodes[$mode] = [int]$entry.compile.exitCode
    Assert-FenceTree $directory $manifest.sourceHashes $overlay $overlayHash
    if ($entry.compile.exitCode -ne 0) { throw "Compile failure is not behavioral evidence: $mode" }
    if ($Run) {
      $selection = 'NetworkLocalSourceHistoryIntegrationTest'
      if ($mode -eq 'FenceAsRegister') { $selection += '#liveOriginalMissCannotCreateSourceOrDurableFence' }
      if ($mode -eq 'ErrorAsFailure') { $selection += '#readErrorsNeverEnterFenceOrCreateAnInventedNegativeReceipt' }
      $entry.execution = Invoke-FenceBuild $directory ($mode + '-test') @('-B', '-pl', 'services/platform-app', '-am', 'test', "-Dtest=$selection", '-Dsurefire.failIfNoSpecifiedTests=false')
      $xml = Join-Path $directory 'services/platform-app/target/surefire-reports/TEST-auriga.iam.NetworkLocalSourceHistoryIntegrationTest.xml'
      $summary = Get-FenceSummary $xml
      $entry.summary = [ordered]@{ xml = $xml; tests = $summary.tests; failures = $summary.failures; errors = $summary.errors; skipped = $summary.skipped; disabled = $summary.disabled }
      Assert-FenceVerdict $summary $mode ([int]$entry.execution.exitCode)
      if ($mode -eq 'Control') {
        $coverage = [IO.File]::ReadAllText((Join-Path $directory $oldCoveragePath)) | ConvertFrom-Json
        Assert-FenceOldHistoryCoverage $summary $coverage
        $entry.summary['oldIamJointCases'] = [int]$coverage.oldControlCases
        $entry.summary['otherCases'] = $summary.tests - [int]$coverage.oldControlCases
      }
    }
    Assert-FenceTree $directory $manifest.sourceHashes $overlay $overlayHash
    $entry.sourceGuardAfter = $true
  }
} catch {
  $manifest.firstFailure = $_.ToString()
  Write-FenceEvent @{ phase = 'first-failure'; error = $_.ToString() }
  throw
} finally {
  $unchanged = $true
  foreach ($path in $manifest.worktreeHashes.Keys) {
    if ((Get-FenceHash (Join-Path $root $path)) -ne $manifest.worktreeHashes[$path]) { $unchanged = $false }
  }
  $manifest.worktreeUnchanged = $unchanged
  # Write once, retaining every attempted mode, raw command, first failure and scalar exit code.
  [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 14), $utf8)
  Write-Output "Evidence: $output"
  if (-not $unchanged) { throw 'Delivered worktree changed during verification' }
}
