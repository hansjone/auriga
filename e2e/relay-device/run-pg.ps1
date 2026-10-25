param(
  [Parameter(Mandatory=$true)][string]$PgBin,
  [ValidatePattern('^[A-Za-z0-9-]+$')][string]$RunTag = (Get-Date -Format 'yyyyMMdd-HHmmss'),
  [switch]$All,
  [string]$DshAnchor
)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath (Resolve-Path (Join-Path $PSScriptRoot '../..'))
$worktree = (Get-Location).Path
$evidence = Join-Path $PSScriptRoot "evidence/$RunTag"
if (Test-Path -LiteralPath $evidence) { throw 'Use a new RunTag; evidence is retained.' }
[IO.Directory]::CreateDirectory($evidence) | Out-Null
$data = Join-Path $evidence 'data'
$ErrorActionPreference = 'Continue'
& (Join-Path $PgBin 'initdb.exe') -D $data --username=postgres --auth=trust --encoding=UTF8 --locale=C *> (Join-Path $evidence 'initdb.log')
$initExit = $LASTEXITCODE
$ErrorActionPreference = 'Stop'
if ($initExit -ne 0) { throw "initdb failed ($initExit); original log retained at $evidence" }
$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = ([Net.IPEndPoint]$listener.LocalEndpoint).Port
$listener.Stop()
$postgres = $null
$testExit = 1
$stopExit = $null
try {
  $postgres = Start-Process -FilePath (Join-Path $PgBin 'postgres.exe') -WindowStyle Hidden -ArgumentList @('-D', $data, '-h', '127.0.0.1', '-p', "$port") -RedirectStandardOutput (Join-Path $evidence 'postgres.out.log') -RedirectStandardError (Join-Path $evidence 'postgres.err.log') -PassThru
  $ready = $false
  $startup = [Diagnostics.Stopwatch]::StartNew()
  while ($startup.Elapsed.TotalSeconds -lt 10) {
    $postgres.Refresh()
    if (Select-String -LiteralPath (Join-Path $evidence 'postgres.err.log') -SimpleMatch 'database system is ready to accept connections' -Quiet) { $ready = $true; break }
    if ($postgres.HasExited) { break }
    Start-Sleep -Milliseconds 100
  }
  if (-not $ready) { throw 'Temporary PG startup failed; this guard is not a business deadline.' }
  $env:AURIGA_AI_TEST_PG_DSN = "postgres://postgres@127.0.0.1:$port/postgres?sslmode=disable"
  $env:CI = 'true'
  if ($DshAnchor) { $env:AURIGA_RELAY_DSH_ANCHOR = $DshAnchor }
  Set-Location -LiteralPath (Join-Path $worktree 'infra/ai-gateway')
  $scope = if ($All) { './...' } else { './internal/relayhttp' }
  & go test -count=1 -timeout=10m -json $scope *> (Join-Path $evidence 'tests.jsonl')
  $testExit = $LASTEXITCODE
} finally {
  if ($postgres) {
    $postgres.Refresh()
    if (-not $postgres.HasExited) {
      & (Join-Path $PgBin 'pg_ctl.exe') -D $data -m fast -w -t 20 stop *> (Join-Path $evidence 'stop.log')
      $stopExit = $LASTEXITCODE
      [void]$postgres.WaitForExit(2000)
      $postgres.Refresh()
    }
  }
  $stillRunning = if ($postgres) { -not $postgres.HasExited } else { $false }
  [ordered]@{head=(git rev-parse HEAD); testExit=$testExit; stopExit=$stopExit; stillRunning=$stillRunning; scope=$scope; port=$port} | ConvertTo-Json | Set-Content -Encoding UTF8 (Join-Path $evidence 'result.json')
  Set-Location -LiteralPath $worktree
  Write-Output "evidence=$evidence testExit=$testExit stopExit=$stopExit stillRunning=$stillRunning"
}
if ($stillRunning -or $stopExit -ne 0) { throw 'Temporary PG shutdown not confirmed.' }
exit $testExit
