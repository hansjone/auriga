param(
    [ValidateSet('Control','NoDurable','LiveC')][string]$Mode = 'Control',
    [switch]$CompileOnly
)
$ErrorActionPreference = 'Stop'
function Get-FenceHash([string]$Path) {
    $hash = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($Path)
    try { return [BitConverter]::ToString($hash.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
    finally { $stream.Dispose(); $hash.Dispose() }
}
function Invoke-FenceMaven([string]$Arguments, [string]$Log) {
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $env:ComSpec
    $start.Arguments = '/d /s /c ""' + (Join-Path (Get-Location) 'mvnw.cmd') + '" ' + $Arguments + '"'
    $start.WorkingDirectory = (Get-Location).Path
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    [IO.File]::WriteAllText($Log + '.command.txt', $start.Arguments)
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    $stdout = [IO.File]::Create($Log)
    $stderr = [IO.File]::Create($Log + '.stderr.log')
    try {
        if (!$process.Start()) { throw 'Maven subprocess did not start' }
        $outTask = $process.StandardOutput.BaseStream.CopyToAsync($stdout)
        $errTask = $process.StandardError.BaseStream.CopyToAsync($stderr)
        $process.WaitForExit()
        [void]$outTask.GetAwaiter().GetResult()
        [void]$errTask.GetAwaiter().GetResult()
        return $process.ExitCode
    } finally { $stdout.Dispose(); $stderr.Dispose(); $process.Dispose() }
}
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..\..\..\..\..')).Path
Push-Location $repo
try {
    $head = (git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Cannot resolve HEAD' }
    if (git status --porcelain --untracked-files=no) { throw 'Commit tracked/index changes before isolated verification' }
    # Preserve every archive, overlay, log and report; never mutate the source worktree.
    $run = Join-Path $repo ('services/iam/impl/target/fv/' + [guid]::NewGuid().ToString('N').Substring(0,8))
    New-Item -ItemType Directory -Path $run | Out-Null
    $archive = Join-Path $run 'source.zip'
    git archive --format=zip "--output=$archive" $head
    if ($LASTEXITCODE -ne 0) { throw 'git archive failed' }
    $tree = Join-Path $run 'tree'
    Expand-Archive -LiteralPath $archive -DestinationPath $tree
    $kernelPath = 'services/iam/impl/src/main/java/auriga/iam/IamNetworkSources.java'
    $proofPath = 'services/iam/impl/src/main/java/auriga/iam/IamNetworkConnectionAnchors.java'
    $paths = @($kernelPath, $proofPath)
    $original = @{}
    foreach ($path in $paths) {
        $original[$path] = Get-FenceHash (Join-Path $repo $path)
        $blob = (git rev-parse "${head}:$path").Trim()
        $archivedBlob = (git hash-object --no-filters (Join-Path $tree $path)).Trim()
        if ($archivedBlob -ne $blob) { throw 'Archive/HEAD blob mismatch' }
    }
    $file = $null
    if ($Mode -eq 'NoDurable') {
        $file = Join-Path $tree $kernelPath
        $needle = 'saveOperation("Register", request.getOperationId(), request, response, null, anchor.id());'
        $replacement = '// Deliberately bad isolated overlay: report failure without a durable operation.'
    } elseif ($Mode -eq 'LiveC') {
        $file = Join-Path $tree $proofPath
        $needle = 'check(!now.isBefore(ingress.getFirst()));'
        $replacement = '// Deliberately bad isolated overlay: live original C is falsely treated as expired.'
    }
    if ($file) {
        $content = [IO.File]::ReadAllText($file)
        if (($content.Split(@($needle), [StringSplitOptions]::None).Length - 1) -ne 1) { throw 'Mutation must match exactly once' }
        [IO.File]::WriteAllText($file, $content.Replace($needle, $replacement), [Text.UTF8Encoding]::new($false))
    }
    $overlay = @{}
    foreach ($path in $paths) { $overlay[$path] = Get-FenceHash (Join-Path $tree $path) }
    $result = [ordered]@{ head=$head; mode=$Mode; archiveSHA=(Get-FenceHash $archive); sourceSHA=$original; overlaySHA=$overlay; compileExit=$null; testExit=$null; failures=0; errors=0; cases=0; sourceUnchanged=$false }
    Push-Location $tree
    try {
        $result.compileExit = Invoke-FenceMaven '-B -pl services/iam/api,services/iam/impl -am test-compile -DskipTests=true' (Join-Path $run 'compile.log')
        if ($result.compileExit -ne 0) { throw 'Compilation failed; this is NOT a behavioral counterexample' }
        if (!$CompileOnly) {
            $tests = switch ($Mode) {
                'NoDurable' { 'NetworkSourceFencePostgresTest#expiredOrActuallySweptMissIsPermanentAndReadable' }
                'LiveC' { 'NetworkSourceFencePostgresTest#liveMissAndTemporaryDisablingNeverCreateAFence' }
                default { 'NetworkSourceFencePostgresTest,NetworkSourceAuthorityPostgresTest,NetworkConnectionAnchorPostgresTest' }
            }
            $result.testExit = Invoke-FenceMaven ('-B -pl services/iam/api,services/iam/impl -am test -Dtest=' + $tests + ' -Dsurefire.failIfNoSpecifiedTests=false') (Join-Path $run 'test.log')
            $reports = Join-Path $tree 'services/iam/impl/target/surefire-reports'
            if (!(Test-Path -LiteralPath $reports)) { throw 'No fresh test reports' }
            foreach ($xmlFile in Get-ChildItem $reports -Filter 'TEST-*.xml') {
                [xml]$xml = [IO.File]::ReadAllText($xmlFile.FullName)
                foreach ($case in $xml.SelectNodes('//testcase')) {
                    $result.cases++
                    if ($case.failure) { $result.failures++ }
                    if ($case.error) { $result.errors++ }
                }
            }
            if ($result.cases -eq 0 -or $result.errors -ne 0) { throw 'No behavior verdict: missing cases or initialization/runtime errors' }
            if ($Mode -eq 'Control' -and $result.testExit -ne 0) { throw 'Control tests failed' }
            if ($Mode -ne 'Control' -and ($result.testExit -eq 0 -or $result.failures -eq 0)) { throw 'Mutant survived: no assertion failure' }
        }
    } finally {
        Pop-Location
        $result.sourceUnchanged = $true
        foreach ($path in $paths) {
            if ((Get-FenceHash (Join-Path $repo $path)) -ne $original[$path]) { $result.sourceUnchanged = $false }
        }
        $result | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 (Join-Path $run 'result.json')
        Write-Output "Evidence: $run"
        if (!$result.sourceUnchanged) { throw 'Source worktree changed during verification' }
    }
} finally { Pop-Location }
