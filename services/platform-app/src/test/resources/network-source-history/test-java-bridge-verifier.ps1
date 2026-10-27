$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'verify-java-bridge.ps1') -FunctionsOnly
$script:checks = 0
function Accept([scriptblock]$Action) { & $Action; $script:checks++ }
function Reject([scriptblock]$Action) {
  $failed = $false
  try { & $Action } catch { $failed = $true }
  if (-not $failed) { throw 'Guard accepted invalid evidence' }
  $script:checks++
}
function Summary([string]$Xml) {
  [xml]$doc = $Xml
  $suite = $doc.testsuite
  return @{ tests = [int]$suite.tests; failures = [int]$suite.failures; errors = [int]$suite.errors
    skipped = [int]$suite.skipped; disabled = [int]$suite.disabled; cases = @($suite.testcase) }
}
$control = '<testsuite tests="3" failures="0" errors="0" skipped="0"><testcase name="httpsRedeemOrgRevocationAndShutdown"/><testcase name="freshServiceTokensWrongServiceAndTenantAreRejected"/><testcase name="childProcessRefreshStopAndEof"/></testsuite>'
$mutant = '<testsuite tests="1" failures="1" errors="0" skipped="0"><testcase name="httpsRedeemOrgRevocationAndShutdown"><failure type="org.opentest4j.AssertionFailedError" message="revoked Org permission must not persist another source expected: 1 but was: 2"/></testcase></testsuite>'
Accept { Assert-JavaBridgeVerdict (Summary $control) Control 0 }
Accept { Assert-JavaBridgeVerdict (Summary $mutant) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary $control) Control 1 }
Reject { Assert-JavaBridgeVerdict (Summary $control) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary $mutant) OrgBypass 0 }
Reject { Assert-JavaBridgeVerdict (Summary ($mutant.Replace('failures="1"', 'failures="0"'))) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary ($mutant.Replace('errors="0"', 'errors="1"'))) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary ($mutant.Replace('skipped="0"', 'skipped="1"'))) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary ($mutant.Replace('AssertionFailedError', 'SQLException'))) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary ($mutant.Replace('revoked Org permission must not persist another source', 'initialization failed'))) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary ($mutant.Replace('httpsRedeemOrgRevocationAndShutdown', 'other'))) OrgBypass 1 }
Reject { Assert-JavaBridgeVerdict (Summary '<testsuite tests="0" failures="0" errors="0" skipped="0"/>') OrgBypass 1 }
$production = Join-Path (Get-Location).Path 'services/iam/impl/src/main/java/auriga/iam/IamNetworkSourceAuthority.java'
$source = [IO.File]::ReadAllText($production)
Accept {
  $overlay = New-OrgPermissionBypass $source
  if ($overlay -eq $source -or -not $overlay.Contains('deliberately broken current Org permission check')) { throw 'No production overlay' }
}
Reject { New-OrgPermissionBypass ($source + $source) }
Reject { New-OrgPermissionBypass '' }
Write-Output "Java bridge verifier guards: $script:checks passed"
