[CmdletBinding()]
param(
    [switch]$CIDisposableVM,
    [switch]$SystemPhase,
    [string]$RunId,
    [string]$OutputDirectory = (Join-Path $PSScriptRoot 'out'),
    [string]$NodePath = (Get-Command node.exe -ErrorAction Stop).Source
)
$ErrorActionPreference = 'Stop'
if (-not $CIDisposableVM -or $env:GITHUB_ACTIONS -ne 'true' -or $env:RUNNER_ENVIRONMENT -ne 'github-hosted') {
    throw '仅可在 GitHub-hosted 一次性 Windows VM 中使用 -CIDisposableVM。禁止本机/自托管 runner。'
}
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw '需要管理员权限。' }
if (-not $RunId) { $RunId = [Guid]::NewGuid().ToString('N').Substring(0, 10) }
if ($RunId -notmatch '^[a-f0-9]{10}$') { throw '无效 RunId。' }
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
$root = Join-Path $env:ProgramData "auriga-s3-$RunId"
$probe = Join-Path $PSScriptRoot 's3probe.exe'
if (-not $SystemPhase -and (Test-Path -LiteralPath $OutputDirectory) -and @(Get-ChildItem -LiteralPath $OutputDirectory -Force).Count) { throw '输出目录必须为空，避免复用旧的 done/report 文件。' }
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null

function Invoke-Probe([string]$Experiment, [string]$Suffix = '') {
    $stderr = Join-Path $OutputDirectory "$Experiment$Suffix.stderr.txt"
    $stdout = Join-Path $OutputDirectory "$Experiment$Suffix.stdout.json"
    $probeArgs = @($Experiment, '--ci-disposable-vm', '--root', ('"{0}"' -f $root), '--id', $RunId, '--node', ('"{0}"' -f $NodePath))
    $child = Start-Process -FilePath $probe -ArgumentList $probeArgs -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
    $timeoutMs = 75000
    if ($Experiment -in @('job','escape')) { $timeoutMs = 120000 }; if ($Experiment -in @('cleanup','wfp')) { $timeoutMs = 180000 }
    $timedOut = -not $child.WaitForExit($timeoutMs)
    if ($timedOut) { $child.Kill(); $child.WaitForExit() }
    $child.Refresh()
    $code = $child.ExitCode
    $raw = Get-Content -LiteralPath $stdout -Raw -Encoding utf8
    try {
        if ($timedOut) { throw '实验超时。' }
        $result = $raw | ConvertFrom-Json
        if (-not $result.status -or -not $result.experiment) { throw '结果缺少字段。' }
        $result | Add-Member -NotePropertyName exitCode -NotePropertyValue $code
    } catch {
        $result = [pscustomobject]@{ experiment = $Experiment; status = 'error'; checks = @(); error = "超时或工具没有输出合法 JSON，退出码 $code"; exitCode = $code; timedOut=$timedOut }
    }
    $result | ConvertTo-Json -Depth 30 | Set-Content -LiteralPath (Join-Path $OutputDirectory "$Experiment$Suffix.json") -Encoding utf8
    return $result
}

if (-not $SystemPhase) {
    # Stage only public executable bytes. No passwords or tokens are staged.
    if (Test-Path -LiteralPath $root) { throw '运行目录已存在，拒绝覆盖。' }
    $taskName = "Auriga-S3-$RunId"
    $taskRegistered = $false
    $overallExit = 0
    try {
        New-Item -ItemType Directory -Path (Join-Path $root 'tools') | Out-Null
        Copy-Item -LiteralPath $probe -Destination (Join-Path $root 'tools\s3probe.exe')
        Copy-Item -LiteralPath $NodePath -Destination (Join-Path $root 'tools\node.exe')
        $NodePath = Join-Path $root 'tools\node.exe'
        # The admin-only comparison is evidence, not an assertion that administrators possess service privileges.
        $admin = Invoke-Probe 'identity' '-admin'
        $quote = { param($s) "'" + $s.Replace("'", "''") + "'" }
        $script = & $quote $PSCommandPath
        $out = & $quote $OutputDirectory
        $node = & $quote $NodePath
        # Task Scheduler does not inherit the runner process environment. Forward only the two CI guard values.
        $body = "`$env:GITHUB_ACTIONS='true'; `$env:RUNNER_ENVIRONMENT='github-hosted'; & $script -CIDisposableVM -SystemPhase -RunId '$RunId' -OutputDirectory $out -NodePath $node"
        $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($body))
        $action = New-ScheduledTaskAction -Execute (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') -Argument "-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand $encoded"
        $taskPrincipal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
        $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 21)
        Register-ScheduledTask -TaskName $taskName -Action $action -Principal $taskPrincipal -Settings $settings | Out-Null
        $taskRegistered = $true
        Start-ScheduledTask -TaskName $taskName
        $deadline = [DateTime]::UtcNow.AddMinutes(20)
        $done = Join-Path $OutputDirectory 'system.done'
        while (-not (Test-Path -LiteralPath $done)) {
            if ([DateTime]::UtcNow -gt $deadline) { throw 'SYSTEM 阶段超时。' }
            Start-Sleep -Seconds 2
            $taskInfo = Get-ScheduledTaskInfo -TaskName $taskName
            $taskState = (Get-ScheduledTask -TaskName $taskName).State
            if ($taskState -eq 'Ready' -and $taskInfo.LastRunTime.Year -gt 2000 -and $taskInfo.LastTaskResult -ne 0) { throw "SYSTEM 任务提前退出：$($taskInfo.LastTaskResult)" }
        }
        $report = Get-Content -LiteralPath (Join-Path $OutputDirectory 'report.json') -Raw | ConvertFrom-Json
        if ($report.status -ne 'pass') { $overallExit = 1 }
    } catch {
        $overallExit = 1
        [pscustomobject]@{status='error';error=$_.Exception.Message;runId=$RunId} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $OutputDirectory 'bootstrap-error.json') -Encoding utf8
    } finally {
        if ($taskRegistered) {
            Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
            Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction Continue
        }
        # Second, independent cleanup sweep even if SYSTEM never reached its finally block.
        try { $emergency = Invoke-Probe 'cleanup' '-final' }
        catch { $emergency=[pscustomobject]@{experiment='cleanup';status='error';checks=@();error=$_.Exception.Message} }
        $taskAbsent = -not (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue)
        if ($emergency.status -ne 'pass' -or -not $taskAbsent) { $overallExit=1 }
        $reportPath = Join-Path $OutputDirectory 'report.json'
        if (Test-Path -LiteralPath $reportPath) {
            $finalReport=Get-Content -LiteralPath $reportPath -Raw | ConvertFrom-Json
        } else {
            $finalReport=[pscustomobject]@{schemaVersion=1;runId=$RunId;status='error';results=@();error='SYSTEM 阶段未生成汇总；参见 bootstrap-error.json。'}
        }
        $finalReport | Add-Member -NotePropertyName finalCleanup -NotePropertyValue $emergency -Force
        $finalReport | Add-Member -NotePropertyName scheduledTaskRemoved -NotePropertyValue $taskAbsent -Force
        if ($emergency.status -ne 'pass' -or -not $taskAbsent) { $finalReport.status='fail' }
        $finalReport | ConvertTo-Json -Depth 40 | Set-Content -LiteralPath $reportPath -Encoding utf8
        Add-Content -LiteralPath (Join-Path $OutputDirectory 'summary.md') -Value "`n最终清理：$($emergency.status)；计划任务已删除：$taskAbsent。" -Encoding utf8
    }
    if ($overallExit) { exit 1 }
    return
}

if ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18') { throw 'SystemPhase 只允许 SYSTEM。' }
$results = [Collections.Generic.List[object]]::new()
$fatal = $null
$nodeVersion = & $NodePath --version
try {
    foreach ($experiment in @('identity', 'job', 'escape', 'shim', 'wfp', 'acl', 'quota', 'appcontainer')) {
        try { $results.Add((Invoke-Probe $experiment)) }
        catch { $results.Add([pscustomobject]@{ experiment=$experiment; status='error'; checks=@(); error=$_.Exception.Message }) }
    }
} catch { $fatal = $_.Exception.Message }
finally {
    try { $results.Add((Invoke-Probe 'cleanup')) }
    catch { $results.Add([pscustomobject]@{experiment='cleanup';status='error';checks=@();error=$_.Exception.Message}) }
    $status = 'pass'
    if (@($results | Where-Object status -in @('fail', 'error')).Count -or $fatal) { $status = 'fail' }
    elseif (@($results | Where-Object status -eq 'unsupported').Count) { $status = 'unsupported' }
    $os = Get-CimInstance Win32_OperatingSystem
    $report = [ordered]@{
        schemaVersion = 1; runId=$RunId; status=$status; generatedAt=[DateTime]::UtcNow.ToString('o')
        platform=@{caption=$os.Caption;version=$os.Version;build=$os.BuildNumber;architecture=$env:PROCESSOR_ARCHITECTURE;identity='SYSTEM';node=$nodeVersion}
        adminComparison='identity-admin.json'; results=$results.ToArray(); fatalError=$fatal
        limitations=@('BFE/OS reboot persistence is not exercised.', 'No real DSH workload; synthetic Go and Node workloads only.')
    }
    $report | ConvertTo-Json -Depth 40 | Set-Content -LiteralPath (Join-Path $OutputDirectory 'report.json') -Encoding utf8
    $lines = [Collections.Generic.List[string]]::new()
    $lines.Add('# S3 Windows 隔离实测摘要')
    $lines.Add('')
    $lines.Add("运行：$RunId；总状态：$status；系统：$($os.Caption) $($os.BuildNumber)。")
    $lines.Add('')
    foreach ($result in $results) {
        $lines.Add("## $($result.experiment)：$($result.status)")
        $lines.Add('')
        if ($result.error) { $lines.Add("错误：$($result.error)"); $lines.Add('') }
        foreach ($check in $result.checks) { $lines.Add("- [$($check.status)] $($check.name)") }
        $lines.Add('')
    }
    $lines.Add('observed 为测量记录；unsupported 为环境不支持；两者均不能当作隔离能力通过。管理员比较见 identity-admin.json。完整证据见 report.json。')
    $lines | Set-Content -LiteralPath (Join-Path $OutputDirectory 'summary.md') -Encoding utf8
    Set-Content -LiteralPath (Join-Path $OutputDirectory 'system.done') -Value $status -Encoding ascii
}
