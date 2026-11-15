param(
    [Parameter(Mandatory=$true)][string]$CWorktree,
    [Parameter(Mandatory=$true)][string]$RuntimeImage
)
$ErrorActionPreference = 'Stop'
$module = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))
$root = [IO.Path]::GetFullPath((Join-Path $module '../..'))
Set-Location $root
$cHead = (& git -C $CWorktree rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $cHead -ne '9391478fc28d5dcd9d5a1146f9ac5be829eea764') { throw 'C fixture source HEAD mismatch' }
$nHead = (& git rev-parse HEAD).Trim()
$jar = Join-Path $CWorktree '.dev/msgnet-c/platform-app-k4-tested-exec.jar'
$stream = [IO.File]::OpenRead($jar)
try {
    $digest = [BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($stream)).Replace('-','').ToLowerInvariant()
} finally { $stream.Dispose() }
if ($digest -ne '583aed83e08d644dec8d3455d43a7d78b11cadfe67219b43dfeaa0fa75f520e7') { throw 'C jar SHA256 mismatch' }
$run = Join-Path $module ('target/msgnet-cn/' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($run) | Out-Null
[IO.Directory]::CreateDirectory((Join-Path $run 'tmp')) | Out-Null
Add-Type -AssemblyName System.IO.Compression.FileSystem
[IO.Compression.ZipFile]::ExtractToDirectory($jar, (Join-Path $run 'jar'))
$report = Join-Path $CWorktree 'services/collab/impl/target/surefire-reports/TEST-auriga.collab.network.keys.MsgnetExpectedSelectorsHttpTest.xml'
[xml]$xml = Get-Content -Raw -LiteralPath $report
$testClasspath = ($xml.testsuite.properties.property | Where-Object name -eq 'java.class.path').value -split ';'
$classpath = @((Join-Path $run 'classes'), (Join-Path $run 'jar/BOOT-INF/classes'))
$classpath += Get-ChildItem -LiteralPath (Join-Path $run 'jar/BOOT-INF/lib') -Filter '*.jar' | ForEach-Object FullName
# C test helpers only supply synthetic setup and PG lifecycle. C production
# classes are resolved first from the hash-verified jar extracted above.
$classpath += Join-Path $CWorktree 'services/collab/impl/target/test-classes'
$classpath += $testClasspath | Where-Object { $_.EndsWith('.jar') }
$engine = $testClasspath | Where-Object { $_ -like '*junit-platform-engine*' } | Select-Object -First 1
$launcher = $engine.Replace('junit-platform-engine', 'junit-platform-launcher')
if (!(Test-Path -LiteralPath $launcher)) { throw 'Matching cached JUnit launcher missing' }
$repository = $engine.Substring(0, $engine.IndexOf('\org\junit\platform\'))
$goModules = $env:GOMODCACHE
if (!$goModules) { $goModules = (& go env GOMODCACHE).Trim() }
$classpath += $launcher
$cp = $classpath -join ';'
[IO.Directory]::CreateDirectory((Join-Path $run 'classes')) | Out-Null
$java = (Get-Command java).Source
$javac = Join-Path ([IO.Path]::GetDirectoryName($java)) 'javac.exe'
$arguments = @('-cp', ('"' + $cp.Replace('\','/') + '"'), '-d', ('"' + (Join-Path $run 'classes').Replace('\','/') + '"'), ('"' + (Join-Path $PSScriptRoot 'CNJavaInterop.java').Replace('\','/') + '"'))
[IO.File]::WriteAllLines((Join-Path $run 'javac.args'), $arguments)
$ErrorActionPreference = 'Continue'
& $javac ('@' + (Join-Path $run 'javac.args')) *> (Join-Path $run 'javac.log')
$compileResult = $LASTEXITCODE
$ErrorActionPreference = 'Stop'
if ($compileResult -ne 0) { Get-Content (Join-Path $run 'javac.log'); throw 'CN fixture compilation failed' }
[IO.File]::WriteAllText((Join-Path $run 'source.txt'), "N-production=f5850c69f2a510637889c71bc59960ebe02197c5`nN-HEAD=$nHead`nC=$cHead`nC-jar-sha256=$digest`n")
$linuxCp = $classpath | ForEach-Object {
    $value = $_.Replace($run, '/evidence').Replace($CWorktree, '/c').Replace($repository, '/m2')
    $value.Replace('\','/')
}
[IO.File]::WriteAllText((Join-Path $run 'classpath.txt'), ($linuxCp -join ':'))
function WslPath([string]$path) {
    $absolute = [IO.Path]::GetFullPath($path)
    '/mnt/' + $absolute.Substring(0,1).ToLowerInvariant() + $absolute.Substring(2).Replace('\','/')
}
Write-Output "Evidence directory $run"
$runner = @'
set -u
task_run=$(mktemp -d /tmp/msgnet-cn.XXXXXX)
mkdir "$task_run/tmp"
cd "$task_run"
java -Xmx512m -Djava.io.tmpdir="$task_run/tmp" -cp "$(cat /evidence/classpath.txt)" auriga.collab.network.keys.CNJavaInterop "$task_run" /src/deploy/bootstrap
task_result=$?
for task_file in "$task_run"/go-*.log "$task_run"/status-*.json "$task_run"/fixture.json; do
  if [ -f "$task_file" ]; then cp "$task_file" /evidence/; fi
done
exit "$task_result"
'@
[IO.File]::WriteAllText((Join-Path $run 'run.sh'), $runner.Replace("`r`n","`n"))
# Native container /tmp keeps private test files owned by the nonroot reader.
# Only public metadata and logs are copied back. All test processes share this
# one network namespace and use loopback/random ports.
$ErrorActionPreference = 'Continue'
& wsl.exe -e docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges --memory 2g --cpus 2 --user 65534:65534 -e GOWORK=off -e GOTOOLCHAIN=local -e GOMODCACHE=/gomod -e GOCACHE=/cache -e GOMAXPROCS=2 -v ((WslPath $root)+':/src:ro') -v ((WslPath $CWorktree)+':/c:ro') -v ((WslPath $repository)+':/m2:ro') -v ((WslPath $goModules)+':/gomod:ro') -v ((WslPath (Join-Path $module 'target/msgnet-n-gocache'))+':/cache') -v ((WslPath $run)+':/evidence') $RuntimeImage sh /evidence/run.sh *> (Join-Path $run 'java.log')
$result = $LASTEXITCODE
Get-Content (Join-Path $run 'java.log') -Tail 55
Get-ChildItem -LiteralPath $run -Filter 'go-*.log' | ForEach-Object { Get-Content $_.FullName }
exit $result
