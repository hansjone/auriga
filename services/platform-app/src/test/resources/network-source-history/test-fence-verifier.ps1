$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'verify-fence-mutants.ps1') -FunctionsOnly
$root = (Get-Location).Path
$output = Join-Path $root ('target/fg-' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $output | Out-Null
$passed = 0
function Accept([scriptblock]$Check) { & $Check; $script:passed++ }
function Reject([scriptblock]$Check) {
  $rejected = $false
  try { & $Check } catch { $rejected = $true }
  if (-not $rejected) { throw "Guard accepted deliberately invalid tooling evidence after $script:passed checks" }
  $script:passed++
}
function Summary([string]$Attributes, [string]$Cases) {
  $path = Join-Path $output 'fixture.xml'
  [IO.File]::WriteAllText($path, "<testsuite $Attributes>$Cases</testsuite>")
  return Get-FenceSummary $path
}
Accept { Assert-FenceVerdict (Summary 'tests="1" failures="0" errors="0" skipped="0"' '<testcase name="positive"/>') 'Control' 0 }
foreach ($attr in @('tests="0" failures="0" errors="0" skipped="0"',
    'tests="1" failures="0" errors="1" skipped="0"',
    'tests="1" failures="0" errors="0" skipped="1"',
    'tests="1" failures="0" errors="0" skipped="0" disabled="1"',
    'tests="2" failures="0" errors="0" skipped="0"')) {
  Reject { Assert-FenceVerdict (Summary $attr '<testcase name="positive"/>') 'Control' 0 }
}
foreach ($child in @('<error/>', '<skipped/>', '<disabled/>')) {
  Reject { Assert-FenceVerdict (Summary 'tests="1" failures="0" errors="0" skipped="0"' "<testcase name='positive'>$child</testcase>") 'Control' 0 }
}
Reject { Assert-FenceVerdict (Summary 'tests="1" failures="0" errors="0" skipped="0"' '<testcase name="positive"/>') 'Control' 1 }
foreach ($mode in @('FenceAsRegister', 'ErrorAsFailure')) {
  $name = if ($mode -eq 'FenceAsRegister') { 'liveOriginalMissCannotCreateSourceOrDurableFence' } else { 'readErrorsNeverEnterFenceOrCreateAnInventedNegativeReceipt(String)[1]' }
  $message = if ($mode -eq 'FenceAsRegister') { 'live original miss must not create an IAM source' } else { 'ordinary failure must retain durable pending' }
  $valid = "<testcase name='$name'><failure type='org.opentest4j.AssertionFailedError' message='$message'/></testcase>"
  Accept { Assert-FenceVerdict (Summary 'tests="1" failures="1" errors="0" skipped="0"' $valid) $mode 1 }
  Reject { Assert-FenceVerdict (Summary 'tests="1" failures="1" errors="0" skipped="0"' $valid) $mode 0 }
  Reject { Assert-FenceVerdict (Summary 'tests="1" failures="1" errors="0" skipped="0"' $valid.Replace($name, 'wrong-case')) $mode 1 }
  Reject { Assert-FenceVerdict (Summary 'tests="1" failures="1" errors="0" skipped="0"' $valid.Replace('org.opentest4j.AssertionFailedError', 'java.sql.SQLException')) $mode 1 }
  Reject { Assert-FenceVerdict (Summary 'tests="1" failures="1" errors="0" skipped="0"' $valid.Replace($message, 'unrelated assertion')) $mode 1 }
  Reject { Assert-FenceVerdict (Summary 'tests="2" failures="1" errors="0" skipped="0"' ($valid + '<testcase name="unexpected-pass"/>')) $mode 1 }
}
$utf8 = New-Object Text.UTF8Encoding($false)
$tree = Join-Path $output 'tree'
New-Item -ItemType Directory -Path $tree | Out-Null
[IO.File]::WriteAllText((Join-Path $tree 'production.java'), 'original', $utf8)
[IO.File]::WriteAllText((Join-Path $tree 'test.java'), 'fixed-test', $utf8)
$hashes = @{ 'production.java' = (Get-FenceHash (Join-Path $tree 'production.java')); 'test.java' = (Get-FenceHash (Join-Path $tree 'test.java')) }
Accept { Assert-FenceTree $tree $hashes $null $null }
[IO.File]::WriteAllText((Join-Path $tree 'production.java'), 'single-overlay', $utf8)
$overlay = Get-FenceHash (Join-Path $tree 'production.java')
Accept { Assert-FenceTree $tree $hashes 'production.java' $overlay }
Reject { Assert-FenceTree $tree $hashes $null $null }
[IO.File]::WriteAllText((Join-Path $tree 'test.java'), 'changed-test', $utf8)
Reject { Assert-FenceTree $tree $hashes 'production.java' $overlay }
[IO.File]::WriteAllText((Join-Path $tree 'test.java'), 'fixed-test', $utf8)
[IO.File]::WriteAllText((Join-Path $tree 'extra.java'), 'untracked-extra-test', $utf8)
Reject { Assert-FenceTree $tree $hashes 'production.java' $overlay }
$coverage = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'old-history-cases.json')) | ConvertFrom-Json
$oldCases = @($coverage.caseKeys | ForEach-Object {
  $parts = $_ -split '#', 2
  "<testcase classname='$([Security.SecurityElement]::Escape($parts[0]))' name='$([Security.SecurityElement]::Escape($parts[1]))'/>"
})
Accept { Assert-FenceOldHistoryCoverage (Summary 'tests="37" failures="0" errors="0" skipped="0"' ($oldCases -join '')) $coverage }
Reject { Assert-FenceOldHistoryCoverage (Summary 'tests="36" failures="0" errors="0" skipped="0"' (($oldCases | Select-Object -Skip 1) -join '')) $coverage }
Reject { Assert-FenceOldHistoryCoverage (Summary 'tests="38" failures="0" errors="0" skipped="0"' (($oldCases -join '') + $oldCases[0])) $coverage }
Reject { Assert-FenceOldHistoryCoverage (Summary 'tests="37" failures="0" errors="0" skipped="0"' (($oldCases -join '').Replace('auriga.iam.', 'auriga.collab.'))) $coverage }

Accept {
  if ((@(Get-FenceModes $true $false) -join ',') -cne 'Control,FenceAsRegister,ErrorAsFailure') {
    throw 'Default Run must still execute Control and both mutants'
  }
}
Accept {
  if ((@(Get-FenceModes $false $false) -join ',') -cne 'FenceAsRegister,ErrorAsFailure') {
    throw 'Default CompileOnly must still compile both mutants'
  }
}
foreach ($execute in @($true, $false)) {
  Accept {
    if ((@(Get-FenceModes $execute $true) -join ',') -cne 'ErrorAsFailure') {
      throw 'ErrorAsFailureOnly must not select Control or the first mutant'
    }
  }
}
$production = 'services/collab/impl/src/main/java/auriga/collab/app/NetworkSourceControl.java'
$original = [IO.File]::ReadAllText((Join-Path $root $production)).Replace("`r`n", "`n")
$readMutant = New-FenceReadErrorOverlay $original
$readTree = Join-Path $output 'read-overlay-tree'
New-Item -ItemType Directory -Path $readTree | Out-Null
[IO.File]::WriteAllText((Join-Path $readTree 'production.java'), $original, $utf8)
[IO.File]::WriteAllText((Join-Path $readTree 'test.java'), 'fixed-test', $utf8)
$readHashes = @{ 'production.java' = (Get-FenceHash (Join-Path $readTree 'production.java')); 'test.java' = (Get-FenceHash (Join-Path $readTree 'test.java')) }
[IO.File]::WriteAllText((Join-Path $readTree 'production.java'), $readMutant, $utf8)
Accept {
  Assert-FenceReadErrorOverlay $original $readMutant
  $recovery = $original.IndexOf('  private RegisterAgentSourceResponse recoverRegister(')
  if ($original.Substring(0, $recovery) -cne $readMutant.Substring(0, $recovery)) {
    throw 'Initial Register, pending preparation and total catch must be byte-identical'
  }
  Assert-FenceTree $readTree $readHashes 'production.java' (Get-FenceHash (Join-Path $readTree 'production.java'))
}
Reject {
  $totalCatch = '      // No ordinary RPC, storage or post-pending validation failure is a negative receipt.'
  Assert-FenceReadErrorOverlay $original ($readMutant.Replace($totalCatch, '      return inventedNegativeReceipt();'))
}
Reject { Assert-FenceReadErrorOverlay $original ($readMutant + "`n// unrelated production change") }
Reject { New-FenceReadErrorOverlay ($original.Replace('  private RegisterAgentSourceResponse recoverRegister(', '  private RegisterAgentSourceResponse otherRecovery(')) }
Reject { New-FenceReadErrorOverlay ($original + "`n      return iam.readSourceReceipt(original);") }
Write-Output "Verifier guard checks: $passed passed; synthetic tooling checks only; $output"
