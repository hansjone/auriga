# Windows 单机前后端联调

本页给只有 Windows 电脑的现场开发者：在一台机器上把 Auriga 后端、edge 和前端一起跑起来，用浏览器正常登录门户与管理台，边改代码边调试。不需要 Linux、Docker、WSL，也不改 hosts。

它使用仓库自带的 `scripts/beta-ui/beta-ui.ps1`，技术细节见 [beta-ui.md](beta-ui.md)。本页只讲怎么用。

## 能调什么、不能调什么

| 范围                                                                           | 本机联调                         |
| ------------------------------------------------------------------------------ | -------------------------------- |
| 平台后端（IAM、组织、成员、角色、配额、资源、审计等，`services/platform-app`） | 可以                             |
| 管理台、门户前端（`web/admin`、`web/portal`）                                  | 可以                             |
| edge 网关（登录会话、CSRF、`auriga-access-mode` 注入）                         | 可以，真实链路                   |
| 模型中转站（`/ai/v1`）                                                         | 默认不启动，需要另行接入，见文末 |
| 云端实例、DSH 接入、Team/通信                                                  | 不支持，只能在 Linux 部署中验证  |

最终交付仍按 [MAINTAIN.md](../../MAINTAIN.md) 在 Linux 上构建、签名、安装。本机联调只用于开发自测。

## 为什么不要直接 `java -jar platform-app`

单独启动 platform 时需要手工准备安装配置（`AURIGA_DEPLOYMENT_ACCESS_FILE`）、IAM 加密密钥、各服务数据库角色和 IAM 签名材料；前端也必须经过 edge：

- 登录会话、CSRF 和续期都在 edge 上完成，前端直连 platform 无法登录；
- 管理台写操作依赖 edge 注入的 `<meta name="auriga-access-mode">`，直连拿不到会按设计失败关闭。

`beta-ui.ps1` 会一次生成全部材料，并同时启动 platform、edge 和配套服务，浏览器走的就是“前端 → edge → platform”的真实路径。不要再自己拼这些环境变量。

## 一、准备工具

| 工具       | 版本                                             | 说明                                                                                      |
| ---------- | ------------------------------------------------ | ----------------------------------------------------------------------------------------- |
| JDK        | 25                                               | `java -version` 能看到 25；Maven 用仓库自带的 `mvnw.cmd`，不必另装                        |
| Go         | 1.27.x                                           | `go version`                                                                              |
| Node.js    | 22.12 及以上                                     | `node -v`                                                                                 |
| pnpm       | 仓库 `package.json` 的 `packageManager` 指定版本 | 执行一次 `corepack enable` 即可自动使用该版本                                             |
| PostgreSQL | 16                                               | 本机服务，监听 `127.0.0.1:5432`（其他端口见下文 `-PgPort`），需要 `postgres` 超级用户口令 |

构建会联网下载 Maven、Go、pnpm 依赖，首次较慢。所有命令在 **Windows PowerShell** 中、从**源码根目录**执行。如果提示脚本被禁止运行，先在当前窗口执行：

```powershell
Set-ExecutionPolicy -Scope Process Bypass
```

本机以下端口必须空闲（全部只绑定 `127.0.0.1`）：`18080`–`18087`、`19090`。

## 二、首次初始化（每台机器一次）

先选一个**源码目录之外**的普通目录作为状态目录（StateDir），本机所有密钥、配置、日志、账号都放在这里。不要放进源码目录，也不要提交或外发。

```powershell
$betaState = 'D:\auriga-local\beta-ui'          # 按实际位置修改，目录不必预先存在
.\scripts\beta-ui\beta-ui.ps1 init -StateDir $betaState
```

再让脚本用 `postgres` 超级用户建库建角色。口令写入一个受保护的 pgpass 文件，不要直接写在命令行：

```powershell
$betaPgpass = Join-Path $betaState 'postgres.pgpass'
$betaPsql = 'C:\Program Files\PostgreSQL\16\bin\psql.exe'   # 按实际安装路径修改
[void](New-Item -ItemType File -Path $betaPgpass)
.\scripts\beta-ui\private.ps1 -Path $betaPgpass
$betaSecure = Read-Host '本机 PostgreSQL postgres 密码' -AsSecureString
try {
  $betaPassword = [Net.NetworkCredential]::new('', $betaSecure).Password
  $betaEscaped = $betaPassword.Replace('\', '\\').Replace(':', '\:')
  [IO.File]::WriteAllText($betaPgpass, "127.0.0.1:5432:*:postgres:$betaEscaped`n", [Text.UTF8Encoding]::new($false))
} finally {
  $betaPassword = $null; $betaEscaped = $null; $betaSecure.Dispose()
}
.\scripts\beta-ui\beta-ui.ps1 init-db -StateDir $betaState -PgPassFile $betaPgpass -Psql $betaPsql
```

PostgreSQL 不在 5432 时，`init` 加 `-PgPort <端口>`，pgpass 里的端口同步修改。`init-db` 会新建一个随机名字的 `auriga_beta_*` 数据库和专用角色，不碰已有的库。

## 三、构建并启动

```powershell
$env:AURIGA_PNPM_STORE = 'D:\auriga-local\pnpm-store'   # pnpm 依赖缓存目录，任选，保持固定即可
.\scripts\beta-ui\beta-ui.ps1 build  -StateDir $betaState
.\scripts\beta-ui\beta-ui.ps1 start  -StateDir $betaState
.\scripts\beta-ui\beta-ui.ps1 status -StateDir $betaState
```

`build` 依次构建管理台和门户前端、platform jar、edge 与网络组件，输出到 StateDir。依赖已缓存时约 3 分钟。`start` 启动 platform、network-broker、network、edge 并等待就绪；`status` 中各进程 `running` 均为 `true` 即正常。

打开浏览器：

- 管理台：<http://127.0.0.1:18082>
- 门户：<http://127.0.0.1:18080>

请用 `127.0.0.1` 访问，不要换成本机局域网 IP：浏览器把 `127.0.0.1` 视为安全上下文，登录相关的同源校验才完整可用。

## 四、首次登录

首次 `start` 时会生成一个应急管理员，账号在 `StateDir\enrollment.json`：

- `username`、`password`：登录名和口令；
- `totpSecret`：TOTP 密钥（Base32），添加到手机验证器（Microsoft Authenticator、Google Authenticator 等，选“手动输入密钥”、基于时间）。

管理台登录页选“应急账号”，填账号、口令和验证器上的 6 位码。登录后即可在“成员与权限”等页面做真实读写，数据落在本机 PostgreSQL。

`enrollment.json` 只在本机查看，不要截图、外发或提交。`stop` 再 `start` 会沿用同一账号和数据。

## 五、日常开发循环

| 改了什么                       | 怎么生效                                                                                                                                                  |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 只改管理台前端                 | `pnpm --filter "@auriga/admin" build`，再 `Copy-Item web\admin\dist\* "$betaState\static\admin" -Recurse -Force`，浏览器刷新即可，无须重启（约 10–20 秒） |
| 只改门户前端                   | 同上，把 `admin` 换成 `portal`                                                                                                                            |
| 改了后端 Java、edge 或 Go 组件 | `stop` → `build` → `start`                                                                                                                                |

前端没有 Vite 开发服务器代理，所有页面都要经 edge 提供，所以按上表“构建并复制”。后端重新 `build` 会连前端一起重建，无须另外复制。

停止全部进程：

```powershell
.\scripts\beta-ui\beta-ui.ps1 stop -StateDir $betaState
```

`stop` 只停进程，不删数据库和账号。

## 六、排错

- **日志**：`StateDir\logs\` 下按进程分别是 `platform.log`、`edge.log`、`network.log`、`network-broker.log`，构建日志是 `build.log`。命令行失败时只提示去看日志，不在终端打印。
- **`A Beta port is occupied`**：有端口被别的程序占用。用 `netstat -ano | findstr 1808` 找到占用进程，关掉后重试。
- **`Run init-db first`**：还没执行 `init-db`，或 `init-db` 失败了。
- **`AURIGA_PNPM_STORE must identify ...`**：`build` 前没设置 `$env:AURIGA_PNPM_STORE`。
- **`Platform did not become healthy`**：看 `platform.log`，常见原因是 PostgreSQL 没启动或 JDK 不是 25。
- **登录后页面报 503**：先刷新一次（普通查询会恢复会话投影），再重试写操作。
- **`command.lock` 残留**：上一条命令被强行中断。确认锁文件里记录的进程已经退出，再删除该文件。
- **升级源码后旧 StateDir 启动报缺材料**：新版本可能新增了配置。保留旧目录，换一个新的 StateDir 从第二步重来（会建一个新库，旧库不受影响）。

## 七、可选：接入模型中转站

默认不启动 AI 网关，所以“模型与用量”页面能打开，但成员无法通过 `/ai/v1` 调模型。需要调试中转站时，先按 [中转站架构](../architecture/relay-bifrost.zh.md) 在本机准备 relay 后端，再把它接入：

```powershell
node scripts\beta-ui\runner.mjs relay --state $betaState --origin http://127.0.0.1:<后端端口>/ `
  --admin-password-file <管理口令文件> --member-password-file <成员口令文件>
```

两份口令文件必须不同；HTTP 后端只允许 `127.0.0.1`。接入后 `stop` → `start`，`status` 会多出 `ai` 进程。
