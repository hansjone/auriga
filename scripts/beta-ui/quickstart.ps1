<#
.SYNOPSIS
  Windows 单机前后端联调一键脚本：检查工具，按需初始化，然后构建并启动。
.DESCRIPTION
  所有实际工作都调用同目录的 beta-ui.ps1（init、init-db、build、start、status），本脚本不重复实现其逻辑。
  步骤：检查 java 25、go 1.27、node 22.12 及以上、pnpm、psql.exe；
  StateDir 未初始化则 init；数据库未就绪则交互创建受保护的 pgpass 并 init-db；
  然后 build、start、status，最后打印管理台和门户地址。
  详细说明见 docs/dev/windows-local-dev.zh.md。
.PARAMETER StateDir
  状态目录，必填。必须在源码目录之外。密钥、配置、日志、应急账号都放这里。
.PARAMETER Psql
  psql.exe 的路径或命令名，默认 psql.exe（需要在 PATH 中）。
.PARAMETER PgPort
  本机 PostgreSQL 端口，默认 5432。
.PARAMETER CheckOnly
  只检查工具和 StateDir 状态，不做任何修改，然后退出。
.EXAMPLE
  .\scripts\beta-ui\quickstart.ps1 -StateDir D:\auriga-local\beta-ui -Psql 'C:\Program Files\PostgreSQL\16\bin\psql.exe'
#>
param(
  [Parameter(Mandatory = $true)][string]$StateDir,
  [string]$Psql = 'psql.exe',
  [int]$PgPort = 5432,
  [switch]$CheckOnly
)
$ErrorActionPreference = 'Stop'

$betaScript = Join-Path $PSScriptRoot 'beta-ui.ps1'
$privateScript = Join-Path $PSScriptRoot 'private.ps1'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$stateFull = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($StateDir).TrimEnd('\')
$logsDir = Join-Path $stateFull 'logs'

function Write-Step([string]$Message) { Write-Host ''; Write-Host "==> $Message" -ForegroundColor Cyan }
function Write-Ok([string]$Message) { Write-Host "  [通过] $Message" -ForegroundColor Green }
function Write-Bad([string]$Message) { Write-Host "  [缺少] $Message" -ForegroundColor Red }

# 运行原生命令并返回文本。java -version 把版本写到 stderr，PowerShell 5.1 下需要放宽错误处理。
function Get-ToolText([string]$Name, [string[]]$Arguments) {
  $previous = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    $lines = & $Name @Arguments 2>&1 | ForEach-Object { $_.ToString() }
    return (($lines) -join "`n").Trim()
  } catch {
    return ''
  } finally {
    $ErrorActionPreference = $previous
  }
}

$problems = New-Object System.Collections.Generic.List[string]

function Test-Tools {
  Write-Step '检查本机工具'

  # java：需要 25
  if (Get-Command java -ErrorAction SilentlyContinue) {
    $text = Get-ToolText 'java' @('-version')
    if ($text -match 'version "(\d+)') {
      if ([int]$Matches[1] -eq 25) { Write-Ok "java $($Matches[1])" }
      else { Write-Bad "java 版本是 $($Matches[1])，需要 JDK 25。安装 JDK 25，并让 java -version 显示 25（检查 JAVA_HOME 和 PATH）。"; $problems.Add('java') }
    } else { Write-Bad '无法识别 java -version 的输出。需要 JDK 25。'; $problems.Add('java') }
  } else { Write-Bad '找不到 java。需要安装 JDK 25 并加入 PATH。'; $problems.Add('java') }

  # go：需要 1.27.x
  if (Get-Command go -ErrorAction SilentlyContinue) {
    $text = Get-ToolText 'go' @('version')
    if ($text -match 'go(\d+)\.(\d+)') {
      if ([int]$Matches[1] -eq 1 -and [int]$Matches[2] -eq 27) { Write-Ok ($text -replace '^go version ', 'go ') }
      else { Write-Bad "go 版本是 $($Matches[1]).$($Matches[2])，需要 1.27.x。"; $problems.Add('go') }
    } else { Write-Bad '无法识别 go version 的输出。需要 Go 1.27.x。'; $problems.Add('go') }
  } else { Write-Bad '找不到 go。需要安装 Go 1.27.x 并加入 PATH。'; $problems.Add('go') }

  # node：需要 22.12 及以上
  if (Get-Command node -ErrorAction SilentlyContinue) {
    $text = Get-ToolText 'node' @('-v')
    if ($text -match 'v(\d+\.\d+\.\d+)') {
      if ([version]$Matches[1] -ge [version]'22.12.0') { Write-Ok "node v$($Matches[1])" }
      else { Write-Bad "node 版本是 v$($Matches[1])，需要 22.12 及以上。"; $problems.Add('node') }
    } else { Write-Bad '无法识别 node -v 的输出。需要 Node.js 22.12 及以上。'; $problems.Add('node') }
  } else { Write-Bad '找不到 node。需要安装 Node.js 22.12 及以上并加入 PATH。'; $problems.Add('node') }

  # pnpm：用 corepack 启用仓库 package.json 指定的版本
  if (Get-Command pnpm -ErrorAction SilentlyContinue) {
    $text = Get-ToolText 'pnpm' @('-v')
    if ($text) { Write-Ok "pnpm $($text.Split("`n")[0])" } else { Write-Ok 'pnpm 已找到（未能读出版本，构建时会再次校验）' }
  } elseif (Get-Command corepack -ErrorAction SilentlyContinue) {
    Write-Bad '找不到 pnpm，但 corepack 可用。请先执行一次：corepack enable'
    $problems.Add('pnpm')
  } else {
    Write-Bad '找不到 pnpm 和 corepack。请安装 Node.js 22.12 及以上，然后执行：corepack enable'
    $problems.Add('pnpm')
  }

  # psql.exe：init-db 需要
  $psqlFound = $false
  if ($Psql -match '[\\/]') { $psqlFound = Test-Path -LiteralPath $Psql -PathType Leaf }
  else { $psqlFound = [bool](Get-Command $Psql -ErrorAction SilentlyContinue) }
  if ($psqlFound) { Write-Ok "psql：$Psql" }
  else { Write-Bad "找不到 psql：$Psql。请安装 PostgreSQL 16，并用 -Psql 指定 psql.exe 的完整路径，例如 'C:\Program Files\PostgreSQL\16\bin\psql.exe'。"; $problems.Add('psql') }

  # 仓库里自带 Maven 包装器
  if (Test-Path -LiteralPath (Join-Path $repoRoot 'mvnw.cmd')) { Write-Ok '源码根目录有 mvnw.cmd（Maven 不必另装）' }
  else { Write-Bad "源码根目录找不到 mvnw.cmd：$repoRoot"; $problems.Add('mvnw') }
}

function Get-StateStatus {
  $installation = Join-Path $stateFull 'installation.json'
  if (-not (Test-Path -LiteralPath $installation)) { return 'new' }
  try { $manifest = Get-Content -LiteralPath $installation -Raw -Encoding UTF8 | ConvertFrom-Json }
  catch { return 'broken' }
  if ($manifest.dbReady -eq $true) { return 'ready' }
  return 'db-pending'
}

function Invoke-Beta([string]$Action, [string[]]$Extra) {
  Write-Step "beta-ui.ps1 $Action"
  $arguments = @($Action, '-StateDir', $stateFull, '-Psql', $Psql, '-PgPort', "$PgPort") + $Extra
  try {
    & $betaScript @arguments
  } catch {
    Write-Host ''
    Write-Host "失败：beta-ui.ps1 $Action" -ForegroundColor Red
    Write-Host "原因：$($_.Exception.Message)" -ForegroundColor Red
    Write-Host "详细日志在：$logsDir（构建日志是 build.log）。端口占用、数据库未启动、JDK 版本等常见原因见 docs/dev/windows-local-dev.zh.md 的“排错”。" -ForegroundColor Yellow
    exit 1
  }
}

function New-PgPassFile([string]$Path) {
  # 与 docs/dev/windows-local-dev.zh.md 的手动步骤相同：口令只在内存里转换，不出现在命令行。
  if (Test-Path -LiteralPath $Path) { Remove-Item -LiteralPath $Path -Force }
  [void](New-Item -ItemType File -Path $Path)
  & $privateScript -Path $Path
  $secure = Read-Host '本机 PostgreSQL postgres 用户的密码' -AsSecureString
  try {
    $password = [Net.NetworkCredential]::new('', $secure).Password
    $escaped = $password.Replace('\', '\\').Replace(':', '\:')
    [IO.File]::WriteAllText($Path, "127.0.0.1:${PgPort}:*:postgres:$escaped`n", [Text.UTF8Encoding]::new($false))
  } finally {
    $password = $null; $escaped = $null; $secure.Dispose()
  }
}

# ---- 开始 ----
Write-Host "源码目录：$repoRoot"
Write-Host "状态目录：$stateFull"

Test-Tools

$status = Get-StateStatus
Write-Step '检查状态目录'
switch ($status) {
  'new' { Write-Host '  未初始化：将执行 init，再创建 pgpass 并执行 init-db。' }
  'db-pending' { Write-Host '  已 init，但数据库未就绪：将创建 pgpass 并执行 init-db。' }
  'ready' { Write-Host '  已初始化，数据库就绪：跳过 init 和 init-db。' }
  'broken' { Write-Host '  installation.json 无法读取。' -ForegroundColor Red; $problems.Add('state') }
}

if (-not $env:AURIGA_PNPM_STORE) {
  $storeParent = Split-Path -Parent $stateFull
  if (-not $storeParent) { $storeParent = $stateFull }
  $defaultStore = Join-Path $storeParent 'pnpm-store'
  Write-Host "  AURIGA_PNPM_STORE 未设置：本次将使用 $defaultStore（pnpm 依赖缓存，与状态目录同级）。想固定到别处，先在当前窗口设置该环境变量。"
} else {
  Write-Host "  AURIGA_PNPM_STORE 已设置：$($env:AURIGA_PNPM_STORE)"
}

if ($problems.Count -gt 0) {
  Write-Host ''
  Write-Host "检查未通过：$($problems -join ', ')。请按上面的提示处理后重新运行。" -ForegroundColor Red
  exit 1
}
if ($CheckOnly) {
  Write-Host ''
  Write-Host '检查全部通过（-CheckOnly，未做任何修改）。' -ForegroundColor Green
  exit 0
}

if (-not $env:AURIGA_PNPM_STORE) { $env:AURIGA_PNPM_STORE = $defaultStore }

if ($status -eq 'new') {
  Invoke-Beta 'init' @()
  $status = 'db-pending'
}
if ($status -eq 'db-pending') {
  $pgpass = Join-Path $stateFull 'postgres.pgpass'
  Write-Step '创建受保护的 pgpass（口令不会出现在命令行）'
  try { New-PgPassFile $pgpass }
  catch {
    Write-Host "失败：创建 pgpass。$($_.Exception.Message)" -ForegroundColor Red
    exit 1
  }
  Invoke-Beta 'init-db' @('-PgPassFile', $pgpass)
  Write-Host "  pgpass 保存在 $pgpass，仅当前用户可访问。以后不需要可自行删除。"
}

Invoke-Beta 'build' @()
Invoke-Beta 'start' @()
Invoke-Beta 'status' @()

Write-Host ''
Write-Host '启动完成。用浏览器打开（请用 127.0.0.1，不要换成局域网 IP）：' -ForegroundColor Green
Write-Host '  管理台：http://127.0.0.1:18082'
Write-Host '  门户：  http://127.0.0.1:18080'
Write-Host ''
Write-Host "应急管理员账号在 $(Join-Path $stateFull 'enrollment.json')（含登录名、口令和 TOTP 密钥）。"
Write-Host '只在本机查看，不要截图、外发或提交；本脚本不会打印口令。'
Write-Host "停止：.\scripts\beta-ui\beta-ui.ps1 stop -StateDir $stateFull"
