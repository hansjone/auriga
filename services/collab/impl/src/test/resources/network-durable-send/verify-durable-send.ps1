param([string]$ExpectedHead, [switch]$CompileOnly, [switch]$Run, [switch]$FunctionsOnly)
$ErrorActionPreference = 'Stop'

function Get-SendArchiveBlobs([string]$Directory, [string[]]$Files) {
  # Let cmd open a BOM-free file as Git stdin: neither PowerShell's encoding nor
  # an early Git exit can corrupt a path or interrupt a managed pipe writer.
  $encoding = New-Object Text.UTF8Encoding($false)
  $inventoryName = 'send-inventory-' + [Guid]::NewGuid().ToString('N')
  $evidence = Join-Path $Directory ('target/' + $inventoryName)
  New-Item -ItemType Directory -Path $evidence | Out-Null
  $inputPath = Join-Path $evidence 'paths.utf8'
  [IO.File]::WriteAllLines($inputPath, $Files, $encoding)
  $info = New-Object Diagnostics.ProcessStartInfo
  $info.FileName = $env:ComSpec
  # Only this generated ASCII basename enters the shell expression. Repository
  # filenames are data in paths.utf8 and never become shell commands.
  $info.Arguments = '/d /s /c "git.exe hash-object --stdin-paths < target\' + $inventoryName + '\paths.utf8"'
  $info.WorkingDirectory = $Directory
  $info.UseShellExecute = $false
  $info.CreateNoWindow = $true
  $info.RedirectStandardOutput = $true
  $info.RedirectStandardError = $true
  $info.StandardOutputEncoding = $encoding
  $info.StandardErrorEncoding = $encoding
  $process = New-Object Diagnostics.Process
  $process.StartInfo = $info
  $diagnosticPath = Join-Path $evidence 'diagnostics.json'
  $diagnostic = [ordered]@{ executable=$info.FileName; arguments=$info.Arguments;
    directory=$Directory; input=$inputPath; fileCount=$Files.Count; exitCode=$null; failure=$null }
  try {
    if (-not $process.Start()) { throw 'Cannot start archive hash inventory' }
    $stdout = $process.StandardOutput.ReadToEndAsync()
    $stderr = $process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    $result = $stdout.GetAwaiter().GetResult()
    $failure = $stderr.GetAwaiter().GetResult()
    $diagnostic.exitCode = [int]$process.ExitCode
    [IO.File]::WriteAllText((Join-Path $evidence 'stdout.log'), $result, $encoding)
    [IO.File]::WriteAllText((Join-Path $evidence 'stderr.log'), $failure, $encoding)
    if ($process.ExitCode -ne 0) {
      throw ('Cannot hash archive inventory: Git exit code ' + $process.ExitCode + '; stderr: ' + $failure.Trim() + '; diagnostics: ' + $diagnosticPath)
    }
    return @($result -split '\r?\n' | Where-Object { $_ -ne '' })
  } catch {
    $diagnostic.failure = $_.Exception.Message
    throw
  } finally {
    [IO.File]::WriteAllText($diagnosticPath, ($diagnostic | ConvertTo-Json -Depth 4), $encoding)
    $process.Dispose()
  }
}

function New-NoDedup([string]$Source) {
  $before = 'var idempotency = request.path("idempotency");'
  if ([regex]::Matches($Source, [regex]::Escape($before)).Count -ne 1) { throw 'NoDedup overlay must match exactly once' }
  # A broken implementation loses the original producer key before lookup/reservation.
  # It still compiles and stores normal bodies/mailbox rows, but cannot deduplicate.
  return $Source.Replace($before, 'var idempotency = ((ObjectNode) request.path("idempotency")).deepCopy().put("key", UuidV7.next().toString());')
}

function Assert-SendVerdict([xml]$Report, [string]$Mode, [int]$ExitCode) {
  $suite = $Report.testsuite
  $cases = @($suite.testcase)
  $expected = if ($Mode -eq 'Control') { 5 } else { 1 }
  if ([int]$suite.tests -ne $expected -or $cases.Count -ne $expected -or
      [int]$suite.errors -ne 0 -or [int]$suite.skipped -ne 0 -or [int]$suite.disabled -ne 0 -or
      @($cases | Where-Object { $_.SelectNodes('error|skipped|disabled').Count -gt 0 }).Count -gt 0) {
    throw 'Missing/error/skip/disabled SEND evidence is never acceptance'
  }
  $failures = @($cases | Where-Object { $_.SelectNodes('failure').Count -gt 0 })
  if ([int]$suite.failures -ne $failures.Count) { throw 'SEND failure count mismatch' }
  if ($Mode -eq 'Control') {
    if ($ExitCode -ne 0 -or $failures.Count -ne 0) { throw 'Unchanged Control must pass first' }
    foreach ($name in @('sendConcurrentSameKeyAndLostReplyRecoverOneOriginalResult',
        'sendFifthDenialHasNoBodyOrMailboxAndRetainsOriginalPending',
        'sendFinalCasRejectsStopPermissionObservationConnectionAndReceiveRaces',
        'sendReceiveControlRequiresFullWitnessAndCrossNodeCannotReplay',
        'sendFetchContinuationAndAttemptAdmissionExposeServerStateVersion')) {
      if (@($cases | Where-Object { $_.name -ceq $name }).Count -ne 1) { throw 'Control omitted required SEND case' }
    }
    return
  }
  if ($Mode -ne 'NoDedup' -or $ExitCode -eq 0 -or $failures.Count -ne 1 -or
      $failures[0].name -ne 'sendConcurrentSameKeyAndLostReplyRecoverOneOriginalResult' -or
      $failures[0].failure.type -ne 'org.opentest4j.AssertionFailedError' -or
      $failures[0].failure.message -notlike '*duplicate body must never be stored*') {
    throw 'NoDedup must fail the same real duplicate-body persistence assertion'
  }
}

if ($FunctionsOnly) { return }
if (($CompileOnly -and $Run) -or (-not $CompileOnly -and -not $Run)) { throw 'Choose exactly CompileOnly or Run' }
if ($ExpectedHead -notmatch '^[0-9a-f]{40}$') { throw 'Supply exact 40-character ExpectedHead' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root' }
$head = (& git rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $head -ne $ExpectedHead) { throw 'HEAD differs from ExpectedHead' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
$tree = @(& git -c core.quotepath=false ls-tree -r $head)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inventory HEAD' }
$files = @($tree | ForEach-Object { ($_ -split "`t", 2)[1] })
$blobs = @($tree | ForEach-Object { (($_ -split "`t", 2)[0] -split ' ')[2] })
$output = Join-Path $root ('target/durable-send-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
$utf8 = New-Object Text.UTF8Encoding($false)
$production = 'services/collab/impl/src/main/java/auriga/collab/app/NetworkDurableSubmit.java'
$test = 'services/collab/impl/src/test/java/auriga/collab/NetworkSourceControlPostgresTest.java'
function Get-SendHash([string]$Path) { return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant() }
$before = @{}
foreach ($file in $files) { $before[$file] = Get-SendHash (Join-Path $root $file) }
$manifest = [ordered]@{ head = $head; mode = $(if ($Run) { 'Run' } else { 'CompileOnly' })
  archiveSha256 = $null; productionBlob = $null; testBlob = $null; variants = @(); firstFailure = $null; worktreeUnchanged = $false }
try {
  & git archive --format=tar "--output=$archive" $head
  if ($LASTEXITCODE -ne 0) { throw 'git archive failed' }
  $manifest.archiveSha256 = Get-SendHash $archive
  $manifest.productionBlob = (& git rev-parse "${head}:$production").Trim()
  $manifest.testBlob = (& git rev-parse "${head}:$test").Trim()
  foreach ($mode in @('Control', 'NoDedup')) {
    $directory = Join-Path $output $mode
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if ($LASTEXITCODE -ne 0) { throw 'archive extraction failed' }
    # git archive applies declared eol attributes; normalize with the same Git filters
    # before comparing blob IDs (.ps1/.cmd deliberately export CRLF on Windows).
    $archiveBlobs = @(Get-SendArchiveBlobs $directory $files)
    if ($archiveBlobs.Count -ne $files.Count) { throw 'Cannot hash archive inventory' }
    for ($i = 0; $i -lt $files.Count; $i++) {
      if ($archiveBlobs[$i] -ne $blobs[$i]) { throw "Archive differs from HEAD blob: $($files[$i])" }
    }
    if ($mode -eq 'NoDedup') {
      $path = Join-Path $directory $production
      [IO.File]::WriteAllText($path, (New-NoDedup ([IO.File]::ReadAllText($path))), $utf8)
    }
    $selector = if ($mode -eq 'Control') { 'NetworkSourceControlPostgresTest#send*' } else { 'NetworkSourceControlPostgresTest#sendConcurrentSameKeyAndLostReplyRecoverOneOriginalResult' }
    $phase = if ($Run) { 'test' } else { 'test-compile' }
    $mavenArgs = @('-B','-pl','services/collab/api,services/collab/impl','-am',$phase,"-Dtest=$selector",'-Dsurefire.failIfNoSpecifiedTests=false')
    $runner = Join-Path $output ($mode + '-command.ps1')
    $argText = ($mavenArgs | ForEach-Object { "'" + $_.Replace("'", "''") + "'" }) -join ','
    [IO.File]::WriteAllText($runner, "`$ErrorActionPreference='Continue'`n& .\mvnw.cmd @($argText)`nexit `$LASTEXITCODE`n", $utf8)
    $shell = Join-Path $PSHOME 'powershell.exe'
    if (-not (Test-Path -LiteralPath $shell)) { $shell = Join-Path $PSHOME 'pwsh.exe' }
    $stdout = Join-Path $output ($mode + '.stdout.log')
    $stderr = Join-Path $output ($mode + '.stderr.log')
    $process = Start-Process -FilePath $shell -ArgumentList @('-NoLogo','-NoProfile','-File',('"' + $runner + '"')) -WorkingDirectory $directory -WindowStyle Hidden -PassThru -Wait -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    $entry = [ordered]@{ name=$mode; args=$mavenArgs; directory=$directory; stdout=$stdout; stderr=$stderr; exitCode=[int]$process.ExitCode
      productionSha256=(Get-SendHash (Join-Path $directory $production)); testSha256=(Get-SendHash (Join-Path $directory $test)); accepted=$false }
    $manifest.variants += $entry
    if ($Run) {
      $report = Join-Path $directory 'services/collab/impl/target/surefire-reports/TEST-auriga.collab.NetworkSourceControlPostgresTest.xml'
      if (-not (Test-Path -LiteralPath $report)) { throw 'Missing SEND Surefire report' }
      $entry.report = $report
      $entry.reportSha256 = Get-SendHash $report
      Assert-SendVerdict ([xml][IO.File]::ReadAllText($report)) $mode ([int]$process.ExitCode)
    } elseif ($process.ExitCode -ne 0) { throw "$mode must compile before root PG run" }
    $entry.accepted = $true
  }
} catch {
  $manifest.firstFailure = $_.Exception.Message
  throw
} finally {
  $unchanged = $true
  foreach ($file in $files) {
    if (-not (Test-Path -LiteralPath (Join-Path $root $file)) -or (Get-SendHash (Join-Path $root $file)) -ne $before[$file]) { $unchanged = $false }
  }
  if (& git status --porcelain --untracked-files=no) { $unchanged = $false }
  $manifest.worktreeUnchanged = $unchanged
  [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 10), $utf8)
  Write-Output (Join-Path $output 'manifest.json')
  if (-not $unchanged) { throw 'Tracked/index worktree changed during verification' }
}
