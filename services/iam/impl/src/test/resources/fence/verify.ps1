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
function Get-FenceDeclaredCount($Node, [string]$Attribute) {
    $value = $Node.GetAttribute($Attribute)
    if (!$value -or $value -eq 'false') { return 0 }
    if ($value -eq 'true') { return 1 }
    $number = 0
    if (![int]::TryParse($value, [ref]$number) -or $number -lt 0) { throw "Invalid $Attribute count" }
    return $number
}
function Get-FenceSummary([string]$Reports, [string]$SelectedMode) {
    if (!(Test-Path -LiteralPath $Reports)) { throw 'No fresh test reports' }
    $summary = [ordered]@{
        cases=0; failures=0; errors=0; skipped=0; disabled=0
        declaredSkipped=0; declaredDisabled=0; declaredErrors=0; declaredFailures=0
        assertionFailures=0; nonBehaviorFailures=0; unexpectedCases=0; failureDetails=@(); caseNames=@(); casesByClass=@{}
    }
    foreach ($xmlFile in Get-ChildItem $Reports -Filter 'TEST-*.xml') {
        $xml = [Xml.XmlDocument]::new()
        $xml.XmlResolver = $null
        $xml.LoadXml([IO.File]::ReadAllText($xmlFile.FullName))
        foreach ($suite in $xml.SelectNodes('//testsuite|//testsuites')) {
            $summary.declaredSkipped += Get-FenceDeclaredCount $suite 'skipped'
            $summary.declaredDisabled += Get-FenceDeclaredCount $suite 'disabled'
            $summary.declaredErrors += Get-FenceDeclaredCount $suite 'errors'
            $summary.declaredFailures += Get-FenceDeclaredCount $suite 'failures'
        }
        foreach ($case in $xml.SelectNodes('//testcase')) {
            $summary.cases++
            $class = $case.GetAttribute('classname')
            $name = $case.GetAttribute('name')
            $summary.caseNames += $class + '#' + $name
            $allowedClasses = @('auriga.iam.NetworkSourceFencePostgresTest', 'auriga.iam.NetworkSourceAuthorityPostgresTest', 'auriga.iam.NetworkConnectionAnchorPostgresTest')
            $allowed = $false
            foreach ($candidate in $allowedClasses) {
                if ($class -eq $candidate -or $class.StartsWith($candidate + '$')) {
                    $allowed = $true
                    $summary.casesByClass[$candidate] = 1 + $summary.casesByClass[$candidate]
                }
            }
            $expectedMethod = switch ($SelectedMode) {
                'NoDurable' { 'expiredOrActuallySweptMissIsPermanentAndReadable' }
                'LiveC' { 'liveMissAndTemporaryDisablingNeverCreateAFence' }
                default { '' }
            }
            if ($SelectedMode -ne 'Control') {
                $allowed = $class -eq $allowedClasses[0] -and ($name -match ('^' + $expectedMethod + '(?:\(|\[|$)'))
            }
            if (!$allowed) { $summary.unexpectedCases++ }
            if ($case.SelectNodes('skipped').Count -gt 0 -or (Get-FenceDeclaredCount $case 'skipped') -gt 0) { $summary.skipped++ }
            if ($case.SelectNodes('disabled').Count -gt 0 -or (Get-FenceDeclaredCount $case 'disabled') -gt 0) { $summary.disabled++ }
            $summary.errors += $case.SelectNodes('error').Count
            foreach ($failure in $case.SelectNodes('failure|error')) {
                $type = $failure.GetAttribute('type')
                $message = ($failure.GetAttribute('message') -replace '\s+', ' ').Trim()
                if ($message.Length -gt 400) { $message = $message.Substring(0,400) }
                $behavior = $false
                if ($failure.Name -eq 'failure') {
                    $summary.failures++
                    if ($allowed -and $type -eq 'org.opentest4j.AssertionFailedError') {
                        if ($SelectedMode -eq 'NoDurable') {
                            $behavior = $message.Contains('complete durable original Register receipt') -and
                                $failure.InnerText -match '(?m)^\s*at auriga\.iam\.NetworkSourceFencePostgresTest\.completeFailure\('
                        } elseif ($SelectedMode -eq 'LiveC') {
                            $behavior = $message -match '^Expected io\.grpc\.StatusRuntimeException to be thrown, but nothing was thrown\.' -and
                                $failure.InnerText -match '(?m)^\s*at auriga\.iam\.IamGrpcNetworkAuthoritySpringTest\.grpc\(' -and
                                $failure.InnerText -match '(?m)^\s*at auriga\.iam\.NetworkSourceFencePostgresTest\.(?:lambda\$)?liveMissAndTemporaryDisablingNeverCreateAFence'
                        }
                    }
                    if ($behavior) { $summary.assertionFailures++ } else { $summary.nonBehaviorFailures++ }
                }
                $summary.failureDetails += [ordered]@{ class=$class; name=$name; type=$type; message=$message; behavioral=$behavior }
            }
        }
    }
    return $summary
}
function Assert-FenceVerdict($Result) {
    if ($Result.cases -le 0 -or $Result.unexpectedCases -ne 0) { throw 'Missing or unexpected selected testcases' }
    if ($Result.errors -ne 0 -or $Result.declaredErrors -ne 0 -or
        $Result.skipped -ne 0 -or $Result.disabled -ne 0 -or
        $Result.declaredSkipped -ne 0 -or $Result.declaredDisabled -ne 0) {
        throw 'No behavior verdict: error/skipped/disabled testcases'
    }
    if ($Result.mode -eq 'Control') {
        foreach ($name in @('NetworkSourceFencePostgresTest', 'NetworkSourceAuthorityPostgresTest', 'NetworkConnectionAnchorPostgresTest')) {
            if ($Result.casesByClass['auriga.iam.' + $name] -le 0) { throw "Control selection missing actual cases: $name" }
        }
        if ($Result.testExit -ne 0 -or $Result.failures -ne 0 -or $Result.declaredFailures -ne 0) { throw 'Control tests failed' }
    } elseif ($Result.testExit -eq 0 -or $Result.assertionFailures -le 0 -or $Result.nonBehaviorFailures -ne 0) {
        throw 'No behavioral counterexample: expected original receipt/durability assertion failure'
    }
}
function Get-FenceManifest([string]$Tree, [string[]]$Paths) {
    $manifest = @{}
    foreach ($path in $Paths) { $manifest[$path] = Get-FenceHash (Join-Path $Tree $path) }
    return $manifest
}
function Assert-FenceOverlay($Before, $After, [string]$ExpectedPath, [string]$ExpectedHash) {
    $changed = @($Before.Keys | Where-Object { $Before[$_] -ne $After[$_] })
    if ($Before.Count -ne $After.Count) { throw 'Archive source manifest size changed' }
    if (!$ExpectedPath) {
        if ($changed.Count -ne 0) { throw 'Control archive sources changed' }
    } elseif ($changed.Count -ne 1 -or $changed[0] -ne $ExpectedPath -or $After[$ExpectedPath] -ne $ExpectedHash) {
        throw 'Only the exact single production overlay is allowed; test changes are forbidden'
    }
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
    # The same fixed test sources are attested in Control and both mutant modes.
    $testPaths = @('NetworkSourceFencePostgresTest', 'NetworkSourceAuthorityPostgresTest', 'NetworkConnectionAnchorPostgresTest') |
        ForEach-Object { 'services/iam/impl/src/test/java/auriga/iam/' + $_ + '.java' }
    $paths = @($kernelPath, $proofPath) + $testPaths
    $original = @{}
    $headBlobs = @{}
    foreach ($path in $paths) {
        $original[$path] = Get-FenceHash (Join-Path $repo $path)
        $blob = (git rev-parse "${head}:$path").Trim()
        if ($LASTEXITCODE -ne 0) { throw 'Cannot resolve exact HEAD source blob' }
        $headBlobs[$path] = $blob
        $archivedBlob = (git hash-object --no-filters (Join-Path $tree $path)).Trim()
        if ($archivedBlob -ne $blob) { throw 'Archive/HEAD blob mismatch' }
    }
    $tracked = @(git -c core.quotepath=false ls-tree -r --name-only $head)
    if ($LASTEXITCODE -ne 0 -or $tracked.Count -eq 0) { throw 'Cannot enumerate exact HEAD sources' }
    $beforeOverlay = Get-FenceManifest $tree $tracked
    $testSHA = Get-FenceManifest $tree $testPaths
    $file = $null
    $overlayPath = ''
    if ($Mode -eq 'NoDurable') {
        $file = Join-Path $tree $kernelPath
        $overlayPath = $kernelPath
        $needle = 'saveOperation("Register", request.getOperationId(), request, response, null, anchor.id());'
        $replacement = '// Deliberately bad isolated overlay: report failure without a durable operation.'
    } elseif ($Mode -eq 'LiveC') {
        $file = Join-Path $tree $proofPath
        $overlayPath = $proofPath
        $needle = 'check(!now.isBefore(ingress.getFirst()));'
        $replacement = '// Deliberately bad isolated overlay: live original C is falsely treated as expired.'
    }
    if ($file) {
        $content = [IO.File]::ReadAllText($file)
        if (($content.Split(@($needle), [StringSplitOptions]::None).Length - 1) -ne 1) { throw 'Mutation must match exactly once' }
        [IO.File]::WriteAllText($file, $content.Replace($needle, $replacement), [Text.UTF8Encoding]::new($false))
    }
    $overlay = Get-FenceManifest $tree $paths
    $expectedOverlayHash = if ($overlayPath) { $overlay[$overlayPath] } else { '' }
    $afterOverlay = Get-FenceManifest $tree $tracked
    Assert-FenceOverlay $beforeOverlay $afterOverlay $overlayPath $expectedOverlayHash
    $beforeOverlay | ConvertTo-Json -Depth 3 | Set-Content -Encoding UTF8 (Join-Path $run 'archive-source-sha.json')
    $result = [ordered]@{
        head=$head; mode=$Mode; compileOnly=[bool]$CompileOnly; archiveSHA=(Get-FenceHash $archive)
        sourceSHA=$original; headBlobs=$headBlobs; testSHA=$testSHA; testSHAAfter=$null
        overlaySHA=$overlay; overlayPath=$overlayPath; archiveSourcesUnchanged=$false
        compileExit=$null; testExit=$null; sourceUnchanged=$false; testsUnchanged=$false
        cases=0; failures=0; errors=0; skipped=0; disabled=0; assertionFailures=0
        nonBehaviorFailures=0; unexpectedCases=0; declaredSkipped=0; declaredDisabled=0; declaredErrors=0; declaredFailures=0
        failureDetails=@(); caseNames=@(); casesByClass=@{}; verdict='not_run'; selection=''
    }
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
            $result.selection = $tests
            $result.testExit = Invoke-FenceMaven ('-B -pl services/iam/api,services/iam/impl -am test -Dtest=' + $tests + ' -Dsurefire.failIfNoSpecifiedTests=false') (Join-Path $run 'test.log')
            $summary = Get-FenceSummary (Join-Path $tree 'services/iam/impl/target/surefire-reports') $Mode
            foreach ($key in $summary.Keys) { $result[$key] = $summary[$key] }
            Assert-FenceVerdict $result
            $result.verdict = if ($Mode -eq 'Control') { 'control_pass' } else { 'behavioral_assertion_failure' }
        } else {
            $result.verdict = 'compile_only'
        }
    } finally {
        Pop-Location
        $result.sourceUnchanged = $true
        foreach ($path in $paths) {
            if ((Get-FenceHash (Join-Path $repo $path)) -ne $original[$path]) { $result.sourceUnchanged = $false }
        }
        $result.testSHAAfter = Get-FenceManifest $tree $testPaths
        $result.testsUnchanged = @($testPaths | Where-Object { $result.testSHAAfter[$_] -ne $testSHA[$_] }).Count -eq 0
        try {
            Assert-FenceOverlay $beforeOverlay (Get-FenceManifest $tree $tracked) $overlayPath $expectedOverlayHash
            $result.archiveSourcesUnchanged = $true
        } finally {
            $result | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 (Join-Path $run 'result.json')
            Write-Output "Evidence: $run"
        }
        if (!$result.sourceUnchanged) { throw 'Source worktree changed during verification' }
        if (!$result.testsUnchanged) { throw 'Archive test sources changed during verification' }
    }
} finally { Pop-Location }
