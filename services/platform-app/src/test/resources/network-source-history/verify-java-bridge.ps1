param([string]$ExpectedHead, [switch]$CompileOnly, [switch]$Run, [switch]$FunctionsOnly)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'verify-fence-mutants.ps1') -FunctionsOnly

function New-OrgPermissionBypass([string]$Source) {
  $before = '&& effective.getPermissionsList().contains("auriga.connect.collab.use")'
  if ([regex]::Matches($Source, [regex]::Escape($before)).Count -ne 1) {
    throw 'Org permission overlay must match exactly once'
  }
  return $Source.Replace($before, '&& true /* deliberately broken current Org permission check */')
}

function Assert-JavaBridgeVerdict($Summary, [string]$Mode, [int]$ExitCode) {
  $expected = if ($Mode -eq 'Control') { 3 } else { 1 }
  if ($Summary.tests -ne $expected -or $Summary.cases.Count -ne $expected -or
      $Summary.errors -ne 0 -or $Summary.skipped -ne 0 -or $Summary.disabled -ne 0 -or
      @($Summary.cases | Where-Object { $_.SelectNodes('error|skipped|disabled').Count -gt 0 }).Count -gt 0) {
    throw 'Missing/error/skip/disabled evidence cannot count as bridge acceptance'
  }
  $failures = @($Summary.cases | Where-Object { $_.SelectNodes('failure').Count -gt 0 })
  if ($Summary.failures -ne $failures.Count) { throw 'Failure count differs from testcase nodes' }
  if ($Mode -eq 'Control') {
    if ($ExitCode -ne 0 -or $failures.Count -ne 0) { throw 'Unchanged bridge Control must pass first' }
    $names = @($Summary.cases | ForEach-Object { $_.name })
    foreach ($name in @('httpsRedeemOrgRevocationAndShutdown', 'freshServiceTokensWrongServiceAndTenantAreRejected', 'childProcessRefreshStopAndEof')) {
      if (@($names | Where-Object { $_ -ceq $name }).Count -ne 1) { throw 'Control omitted required bridge case' }
    }
    return
  }
  if ($Mode -ne 'OrgBypass' -or $ExitCode -eq 0 -or $failures.Count -ne 1) { throw 'OrgBypass must fail behaviorally' }
  $failure = $failures[0]
  if ($failure.name -ne 'httpsRedeemOrgRevocationAndShutdown' -or
      $failure.failure.type -ne 'org.opentest4j.AssertionFailedError' -or
      $failure.failure.message -notlike '*revoked Org permission must not persist another source*') {
    throw 'Only the real post-revocation persistence assertion is accepted as counterevidence'
  }
}

if ($FunctionsOnly) { return }
if (($CompileOnly -and $Run) -or (-not $CompileOnly -and -not $Run)) { throw 'Choose exactly CompileOnly or Run' }
if ($ExpectedHead -notmatch '^[0-9a-f]{40}$') { throw 'Supply exact 40-character ExpectedHead' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from this worktree root' }
$head = (& git rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $head -ne $ExpectedHead) { throw 'HEAD does not equal ExpectedHead' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
$tracked = @(& git ls-tree -r --name-only $head)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inventory HEAD' }
$output = Join-Path $root ('target/jb-' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
$utf8 = New-Object Text.UTF8Encoding($false)
$production = 'services/iam/impl/src/main/java/auriga/iam/IamNetworkSourceAuthority.java'
$test = 'services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaHostPostgresTest.java'
$manifest = [ordered]@{ head = $head; mode = $(if ($Run) { 'Run' } else { 'CompileOnly' })
  archiveSha256 = $null; sourceHashes = @{}; worktreeHashes = @{}; testBlob = $null
  testSha256 = $null; variants = @(); firstFailure = $null; worktreeUnchanged = $false }

function Invoke-JavaBridgeBuild([string]$Directory, [string]$Name, [string[]]$MavenArgs) {
  $runner = Join-Path $output ($Name + '-command.ps1')
  $argText = ($MavenArgs | ForEach-Object { "'" + $_.Replace("'", "''") + "'" }) -join ','
  [IO.File]::WriteAllText($runner, "`$ErrorActionPreference='Continue'`n& .\mvnw.cmd @($argText)`nexit `$LASTEXITCODE`n", $utf8)
  $shell = Join-Path $PSHOME 'powershell.exe'
  if (-not (Test-Path -LiteralPath $shell)) { $shell = Join-Path $PSHOME 'pwsh.exe' }
  $stdout = Join-Path $output ($Name + '.stdout.log')
  $stderr = Join-Path $output ($Name + '.stderr.log')
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
    return [ordered]@{ args = $MavenArgs; directory = $Directory; runner = $runner
      pid = $process.Id; created = $process.StartTime.ToUniversalTime().ToString('o')
      stdout = $stdout; stderr = $stderr; exitCode = [int]$process.ExitCode }
  } finally { $process.Dispose() }
}

try {
  & git archive --format=tar "--output=$archive" $head
  if ($LASTEXITCODE -ne 0) { throw 'git archive failed' }
  $manifest.archiveSha256 = Get-FenceHash $archive
  $manifest.testBlob = (& git rev-parse "${head}:$test").Trim()
  if ($LASTEXITCODE -ne 0) { throw 'Cannot pin test blob' }
  foreach ($mode in @('Control', 'OrgBypass')) {
    $directory = Join-Path $output $(if ($mode -eq 'Control') { 'c' } else { 'm' })
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if ($LASTEXITCODE -ne 0) { throw 'archive extraction failed' }
    if ($mode -eq 'Control') {
      foreach ($path in $tracked) {
        $manifest.sourceHashes[$path] = Get-FenceHash (Join-Path $directory $path)
        $manifest.worktreeHashes[$path] = Get-FenceHash (Join-Path $root $path)
        # Git attributes intentionally use CRLF for checked-out PowerShell and LF in archive.
        # The clean index/worktree check pins canonical content; retain each byte hash separately.
      }
      $manifest.testSha256 = $manifest.sourceHashes[$test]
    }
    Assert-FenceTree $directory $manifest.sourceHashes $null $null
    $overlay = $null; $overlayHash = $null
    if ($mode -eq 'OrgBypass') {
      $overlay = $production
      $path = Join-Path $directory $production
      [IO.File]::WriteAllText($path, (New-OrgPermissionBypass ([IO.File]::ReadAllText($path))), $utf8)
      $overlayHash = Get-FenceHash $path
    }
    Assert-FenceTree $directory $manifest.sourceHashes $overlay $overlayHash
    $entry = [ordered]@{ mode = $mode; directory = $directory; overlay = $overlay; overlaySha256 = $overlayHash
      testSha256 = (Get-FenceHash (Join-Path $directory $test)); compile = $null; execution = $null; summary = $null; sourceGuardAfter = $false }
    $manifest.variants += $entry
    $entry.compile = Invoke-JavaBridgeBuild $directory ($mode + '-compile') @('-B', '-pl', 'services/platform-app', '-am', 'test-compile')
    Assert-FenceTree $directory $manifest.sourceHashes $overlay $overlayHash
    if ($entry.compile.exitCode -ne 0) { throw "Compile failure is not behavioral evidence: $mode" }
    if ($Run) {
      $selection = 'NetworkSourceJavaHostPostgresTest'
      if ($mode -eq 'OrgBypass') { $selection += '#httpsRedeemOrgRevocationAndShutdown' }
      $entry.execution = Invoke-JavaBridgeBuild $directory ($mode + '-test') @('-B', '-pl', 'services/platform-app', '-am', 'test', "-Dtest=$selection", '-Dsurefire.failIfNoSpecifiedTests=false')
      $xml = Join-Path $directory 'services/platform-app/target/surefire-reports/TEST-auriga.iam.NetworkSourceJavaHostPostgresTest.xml'
      $summary = Get-FenceSummary $xml
      $entry.summary = [ordered]@{ xml = $xml; tests = $summary.tests; failures = $summary.failures; errors = $summary.errors; skipped = $summary.skipped; disabled = $summary.disabled }
      Assert-JavaBridgeVerdict $summary $mode ([int]$entry.execution.exitCode)
    }
    Assert-FenceTree $directory $manifest.sourceHashes $overlay $overlayHash
    $entry.sourceGuardAfter = $true
  }
} catch {
  $manifest.firstFailure = $_.ToString()
  throw
} finally {
  $unchanged = ((& git rev-parse HEAD).Trim() -eq $head)
  foreach ($path in $manifest.worktreeHashes.Keys) {
    if ((Get-FenceHash (Join-Path $root $path)) -ne $manifest.worktreeHashes[$path]) { $unchanged = $false }
  }
  $manifest.worktreeUnchanged = $unchanged
  [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 14), $utf8)
  Write-Output "Evidence: $output"
  if (-not $unchanged) { throw 'Delivered worktree changed during verification' }
}
