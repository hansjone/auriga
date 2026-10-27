param([string]$Report = 'services/platform-app/target/surefire-reports/TEST-auriga.iam.NetworkSourceJavaHostTest.xml')
$ErrorActionPreference = 'Stop'
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root' }
[xml]$xml = [IO.File]::ReadAllText((Join-Path $root $Report))
if ([int]$xml.testsuite.tests -lt 2 -or [int]$xml.testsuite.failures -ne 0 -or
    [int]$xml.testsuite.errors -ne 0 -or [int]$xml.testsuite.skipped -ne 0) {
  throw 'Run the non-PG NetworkSourceJavaHostTest successfully before launching'
}
$paths = @($xml.testsuite.properties.property | Where-Object { $_.name -eq 'surefire.test.class.path' })
if ($paths.Count -ne 1) { throw 'Missing exact compiled reactor test classpath' }
$classpath = $paths[0].value
$entries = $classpath.Split([IO.Path]::PathSeparator)
$ownTests = Join-Path $root 'services/platform-app/target/test-classes'
if (-not ($entries -contains $ownTests) -or -not (Test-Path -LiteralPath (Join-Path $ownTests 'auriga/iam/NetworkSourceJavaHost.class'))) {
  throw 'Classpath must refer to this worktree host classes'
}
foreach ($entry in $entries) {
  if (-not (Test-Path -LiteralPath $entry)) { throw 'Stale test classpath; rebuild this worktree' }
}
# No credential value is placed on argv. stdin remains the lifetime/control pipe.
& java -cp $classpath auriga.iam.NetworkSourceJavaHost
exit $LASTEXITCODE
