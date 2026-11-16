# PowerShell 5.1 entry. No downloads, execution-policy changes or shell strings.
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
if ($args.Count -gt 0 -and $args[0] -eq 'client') {
    $clientCli = Join-Path $PSScriptRoot 'scripts\client-release\cli.mjs'
    if (-not (Test-Path -LiteralPath $clientCli -PathType Leaf)) {
        [Console]::Error.WriteLine('客户端命令需要同批批准的客户端发行扩展，请下载对应源码归档。')
        exit 1
    }
    if ($env:AURIGA_CLIENT_DEVKIT) {
        $node = Join-Path $env:AURIGA_CLIENT_DEVKIT 'build-node\node.exe'
    } elseif ($env:AURIGA_CLIENT_NODE) {
        $node = $env:AURIGA_CLIENT_NODE
    } else {
        $node = (Get-Command node.exe -CommandType Application -ErrorAction Stop).Source
    }
    $clientExitCode = 1
    Push-Location -LiteralPath $PSScriptRoot
    try {
        & $node $clientCli @args
        $clientExitCode = $LASTEXITCODE
    } finally { Pop-Location }
    exit $clientExitCode
}
$exe = Join-Path $PSScriptRoot 'bin\auriga-cli.exe'
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
    [Console]::Error.WriteLine('缺少bin\auriga-cli.exe，请下载带签名的auriga-source归档，无需本机安装Go。')
    exit 1
}
$exitCode = 1
Push-Location -LiteralPath $PSScriptRoot
try {
    & $exe @args
    $exitCode = $LASTEXITCODE
} finally {
    Pop-Location
}
exit $exitCode
