# Windows 原生 Beta 管理台

这是一套真人验收环境，使用本机已有的 PostgreSQL 16，不启动 Docker、NATS、Team 或 DSH。门户固定为 `http://127.0.0.1:18080`，管理台固定为 `http://127.0.0.1:18082`；请在浏览器直接打开这些 URL，不改 hosts。它仅供本机开发，不用于对外部署。

## 准备与初始化

需要 JDK 25、Go 1.27、Node 22.12+、仓库规定的 pnpm，以及 PostgreSQL 16 的 `psql.exe`。构建使用锁文件和独立 node_modules，不升级依赖；需要设置工程工作法中的 `AURIGA_PNPM_STORE`。所有操作从仓库根执行。派工会话之外先 dot-source 主控提供的 auriga-env.ps1；没有该脚本的普通开发环境，应自行设置 AURIGA_PNPM_STORE 指向可写的依赖缓存目录。

```powershell
$betaState = 'D:\auriga-beta-state\beta-ui' # 仓库外的本机普通目录，按实际位置调整
.\scripts\beta-ui\beta-ui.ps1 init -StateDir $betaState
```

StateDir 必须是仓库外的本机普通目录。Claude 桌面端等 MSIX 宿主可能重定向 `%LOCALAPPDATA%`，子进程看到的路径可能不同，因此使用明确的普通绝对路径，不从 LOCALAPPDATA 推导 StateDir。初始化先设置并核验仅当前账号可访问、禁止继承的 ACL，再产生密码、加密密钥和本机证书。不会把 CA 加入系统证书库。初始化已有目录会拒绝重新生成密钥；保留该目录才能重新登录同一个数据库。

使用已有 PostgreSQL 管理员的受限 pgpass 文件。若尚无该文件，可在普通 PowerShell 中交互创建；不要在命令行输入明文密码，也不要开启终端转录：

```powershell
$betaPgpass = Join-Path $betaState 'postgres.pgpass'
$betaPsql = 'C:\Program Files\PostgreSQL\16\bin\psql.exe' # 按实际安装路径替换
[void](New-Item -ItemType File -Path $betaPgpass)
.\scripts\beta-ui\private.ps1 -Path $betaPgpass
$betaSecure = Read-Host '本机 PostgreSQL postgres 密码' -AsSecureString
try {
  $betaPassword = [Net.NetworkCredential]::new('', $betaSecure).Password
  $betaEscaped = $betaPassword.Replace('\', '\\').Replace(':', '\:')
  [IO.File]::WriteAllText($betaPgpass, "127.0.0.1:5432:*:postgres:$betaEscaped`n", [Text.UTF8Encoding]::new($false))
} finally {
  $betaPassword = $null
  $betaEscaped = $null
  $betaSecure.Dispose()
}
.\scripts\beta-ui\beta-ui.ps1 init-db -StateDir $betaState -PgPassFile $betaPgpass -Psql $betaPsql
```

实际 psql 路径按安装位置指定；非默认数据库端口应在 init 时用 `-PgPort` 指定，并同步 pgpass。脚本仅通过 PGPASSFILE 传递文件位置，SQL 和数据库日志只写私有目录。

`installation.json` 记录本次随机生成的 `auriga_beta_<随机后缀>` 数据库与六个同前缀角色：无登录数据库属主、IAM、Org、AI 迁移、AI 运行、edge 会话投影只读角色。AI 运行角色不是表属主、没有 DDL/BYPASSRLS 权限；IAM/Org 沿当前 combined 的正式同角色迁移路径运行。不会改已有业务库或角色，重名即拒绝。旧版五角色 Beta 在 init-db 时只追加 edge 投影只读角色，保留原库、账号及密钥；再次执行只复核私有配置。中途失败不自动删除或接管部分已建对象，应由主控核对私有日志后处置，不直接重跑为“成功”。

## 已有 Beta 的会话投影升级

旧版把 IAM 会话写入 PostgreSQL，却把 edge 配成独立内存。登录成功后仍会因读不到权威投影返回 503；改证书或伪造内存投影不能修复这个缺口。

已有环境只执行以下步骤，使用当初记录的绝对 StateDir、受限管理员 pgpass 和真实 psql 路径，**不要重新 init 或删除库**。MSIX 宿主可能重定向 LOCALAPPDATA，不要据另一个终端的默认值另建环境。

```powershell
.\scripts\beta-ui\beta-ui.ps1 stop -StateDir $betaState
.\scripts\beta-ui\beta-ui.ps1 init-db -StateDir $betaState -PgPassFile $betaPgpass -Psql $betaPsql
.\scripts\beta-ui\beta-ui.ps1 build -StateDir $betaState
.\scripts\beta-ui\beta-ui.ps1 start -StateDir $betaState
```

启动在平台迁移完成后，以非超级用户 IAM 角色创建受限视图 edge_session_cache.sessions。视图仅发布本安装租户的 SessionKv 投影，仍受底层 FORCE RLS 约束；新 edge 角色只获该视图 SELECT、schema USAGE 和本 Beta 库 CONNECT，不能直接读取 IAM 表或写数据。管理 API 的会话、CSRF、JWT 和撤销逻辑保持原样。视图属主及 RLS 权限语义见 [PostgreSQL 16 CREATE VIEW](https://www.postgresql.org/docs/16/sql-createview.html)。

edge 显式使用 --session-kv-config 指定 sessionKV.json。Beta 适配器无控制台窗口地调用本机 psql，以独立 pgpass 文件认证，每次读取真实 PG 投影；JWT 结果直接写受限临时文件并在读取后删除，SQL 不上 argv，结果不进 stdout/stderr。读取限并发4、3秒超时，任何数据库/格式错误失败关闭；不缓存阳性结果，退出后下一请求即可看见投影删除。stop 补清理已存在的 projection-数字.json，保留目录内其他文件；若强制终止与 psql 启动发生竞态，需在短期客户端结束后再复核受限结果目录。该 CLI 适配器仅供本机低吞吐 Beta，不作为生产高并发存储方案。

每个被拒绝的 503 会记录一条 WARN，包含固定 reason 类别和 requestId；不含原始错误、Cookie、JWT、CSRF、请求体或查询参数。browser_session_unavailable 是投影/RPC加载阶段，browser_session_touch_unavailable 是续期阶段；另外分别记录 CSRF 依赖、上游 transport 和访问票据到期。

## 构建、启动、停止

```powershell
.\scripts\beta-ui\beta-ui.ps1 build -StateDir $betaState
.\scripts\beta-ui\beta-ui.ps1 start -StateDir $betaState
.\scripts\beta-ui\beta-ui.ps1 status -StateDir $betaState
.\scripts\beta-ui\beta-ui.ps1 stop -StateDir $betaState
```

build 生成正式 platform-app jar、edge 和 AI 网关二进制，并复制构建后的 admin/portal 静态文件。日志在 StateDir/logs，不回显构建和服务日志；不要把私有目录加入仓库。重建前先 stop。start/stop 支持重复执行；start 检查端口占用，失败时清理本次已登记进程；stop 同时核对 PID 和 Windows 启动时间，拒绝终止被复用的 PID，也不会按端口杀进程。

使用的进程和端口：platform HTTP 18081、gRPC mTLS 19090、AI 管理网关 18084、edge 门户 18080/管理台 18082/实例控制监听 18083，全部绑定 127.0.0.1。实例路由显式 `--instance-routes memory`，无路由时拒绝访问，不创建伪实例；不传此选项的 edge 仍要求 NATS。

platform 使用 `combined,dev,beta-ui` 和真实 PostgreSQL SessionKv。仅正式 DevBootstrap 创建签名密钥、应急管理员和团队身份源；账号、口令、TOTP 秘钥仅位于私有 `enrollment.json`。edge/AI 服务票据由真实 TokenService 验证各自独立安装秘密后签发，每分钟续发，停止时清理；启动等待真实票据，不从脚本签造 JWT。AI 所用公钥从运行中的 IAM 获取，不使用测试签名夹具。

Windows 发布器要求私有目录属于当前账号、DACL 禁止继承且只授权当前账号；拒绝 UNC、重解析点、非普通文件、硬链接、宽松 ACL。生命周期内用不共享删除的原生 Windows 句柄固定整个祖先链；同目录原子替换并刷盘。数字 reader-group 仅用于 POSIX；Windows 不据此扩大 ACL，installation-owner 只接受 -1。不同 Windows 服务账号的部署不在这套本机脚本范围内。JDK 原生调用使用 `--enable-native-access=ALL-UNNAMED`，无需新增 Java 依赖。

从 PowerShell 7 启动也使用 Windows PowerShell 5.1 执行子脚本；runner 为这些子进程清除继承的 PSModulePath，让 5.1 重建自己的模块路径，其余构建环境变量保留。private.ps1 通过 .NET 显式把所有者设为当前用户 SID，并写入禁止继承、只授权当前用户的 DACL；仅修改 Owner/Access，不读写审计 SACL、不要求 SeSecurityPrivilege。提升的管理员令牌可能默认创建 Administrators 组所有的对象，因此不能只依赖默认所有者。发布器也仅对自身刚创建的输出目录和临时 JWT 文件设置当前用户所有者；对既有目录、秘密和票据仍严格校验，不接受 Administrators 组或其他主体所有权。已有静态目录重复构建使用同一流程，不会修改父会话或系统模块设置。

native OAuth 客户端为 `auriga-beta-native`：公共客户端、nativeConnect=true，唯一 scope 为 auriga.connect，授权为 device_code + refresh_token。插件线使用门户 Origin 和该 client ID，并仅在隔离回环配置中允许 HTTP。应急管理员不能代替团队账号完成插件登录；应走邀请注册、账户安全/TOTP，再登录插件。脚本不改 DSH profile。

## root 真人浏览器验收

1. 用全新浏览器 profile 打开管理台。仅在本机私下查看 enrollment.json；用其中账号密码和 TOTP 登录。不得将该文件、Cookie 或二维码贴到报告。
2. 刷新页面、打开多个 tab，确认身份与管理数据保持；观察票据文件的更新时间跨过两次续发，不能只凭文件存在判定续发。
3. 在成员页或配额页执行一次可回退的真实写入，刷新/另一个 tab 重读确认落库；记下对象 ID 与前后非敏感值。需要二次验证时完成正式 step-up。
4. 退出登录后，用开发者工具保存于内存中的旧会话重放请求，确认服务端拒绝；刷新旧 tab 也应回登录。不要导出包含 Cookie 的 HAR。
5. 打开 AI 模型/provider 管理页。无真实模型时如实为空；可打开页面不等于模型生成与计量通过。需模型配置时经真实管理 API 写入，不 seed 演示数据。
6. 重复 start，确认只有三条原进程（psql 为有超时的短期读取子进程）；重复 stop，确认登记 PID 消失且六个端口均可重新绑定。stop 不删除 Beta 数据库或 enrollment；再次 start 应复用同一身份。

## 验证与故障边界

```powershell
node --test e2e/beta-ui/runner.test.mjs
.\mvnw.cmd -B -pl services/iam/api,services/iam/impl -am verify
Push-Location infra/edge
try { go vet ./...; go test -count=1 ./... } finally { Pop-Location }
```

脚本测试使用随机端口临时进程，验证配置、重复停止、端口释放和拒绝复用 PID；不冒充真人登录。Java 专属测试走真实 TokenService，覆盖 ACL、硬链接、目录固定和失败保留旧票据。沙箱限制 ACL 或嵌入式 PG 时，保留原始失败，由主控在普通 Windows 会话重跑。

命令崩溃可能留下 command.lock：先查看其中 PID/启动时间，核对进程确实已退出后才移走锁，不直接绕过仍活跃命令。PID 身份不符、端口被其他进程占用时脚本拒绝清理，应由主控确认归属。数据库、凭据与日志不随 stop 删除。IAM 签名钥轮换后重启这套环境，重新获取 AI 网关公钥；当前脚本不自动协调公钥轮换。

未含 Docker 实例、Team/通信、用户真实 DSH 接入与模型生产调用验收；它们分别由对应实现线和主控完成。修改 IAM/edge 的候选必须经非作者独审并标【待审查】，工人不自行合并。

可选真实 PG 集成：设置 AURIGA_BETA_TEST_PG_BIN 为本机 PostgreSQL bin 目录，在 infra/edge 执行 go test -count=1 -run TestBetaPostgresProjection -v。它创建随机端口的独立临时集群并停止清理，检查真实 PG 的视图/RLS、测试投影读取、跨租户与只读权限及撤销可见性，不连接既有 Beta 库。沙箱 initdb 失败需保留失败日志，由主控在普通 Windows 会话重跑。该集成测试不执行真人 IAM 登录；CLI 边界单测使用测试进程模拟输出，仅证明参数、结果文件及失败清理，也不计真人 IAM 验收。

## 插件真机验收

在原生 Windows PowerShell 中从仓库根执行。先确认既有 Beta 门户 `http://127.0.0.1:18080` 已运行，已用邀请完成团队账号注册和 TOTP 设置。应急管理员不能代替团队账号。此脚手架只启动隔离的 DSH Web 宿主，不启动或重置 Beta，不创建云端实例。

本脚手架的宿主基线为 npm `@deepseek-ai/dsh@0.2.1-rc.3`。launcher 把 `scripts/beta-ui/dsh-runtime/` 的完整 npm 锁文件复制到 `StateDir/runtime`，用 `npm ci --ignore-scripts --no-audit --no-fund` 在该目录安装本地宿主。锁文件包含所有依赖的精确解析与 integrity，宿主 tar 的 npm integrity 固定为 `sha512-H+E9HLnHf7fJqzen84Z9Bq8ggb5xbrcbyhZjXTJLE9NabHr3zteGvTRvdvGkxPl4ov80is4WLglFmSo+/olJjg==`；版本或 integrity 不符即失败。安装后通过包的公开根导出检查现有插件要求的 `connection.admit/authorizeIndex/isTrusted`，缺失时明确失败，不回退旧版本。`host-capabilities.json` 记录检查结果。只运行本次 StateDir 中由 package.json 的 bin.dsh 声明的本地 CLI，不使用全局 DSH、Desktop 安装或源码工位。

首次启动需要联网下载已锁定的依赖。构建环境沿前述 `auriga-env.ps1`/`AURIGA_PNPM_STORE`，独立 node_modules、frozen lockfile；插件类型检查、面板构建和成品打包完成后，使用正式 `dsh plugin --profile web add <本仓成品.tgz>` 安装，禁用安装生命周期脚本。不会修改全局 npm、用户 DSH 或系统 PATH。

插件安装所用 pnpm 精确取自仓库根 `package.json` 的 `packageManager`（当前 `pnpm@11.28.6`）。launcher 在隔离 `COREPACK_HOME` 执行 `corepack prepare pnpm@11.28.6 --activate`，设置 `COREPACK_DEFAULT_TO_LATEST=0`，并在 StateDir/profile 的 package.json 声明同一版本。准备后禁用项目声明进行一次 `pnpm --version` 检查，确认隔离默认版本本身正确，避免工位上层仓库声明掩盖缺失默认的问题；不符就拒绝继续。实际版本记在 plugin-dsh.json，准备和版本输出分别在 pnpm-prepare.log、pnpm-version.log，不依赖或修改全局 Corepack 缓存。

宿主启动 URL 在内存解析，仅输出到 stdout，不写入 StateDir 文件。独立日志转发进程持续接收宿主 stdout/stderr，完整行在落盘前移除 URL 查询参数和片段；跨分块票据不会写入日志，超长行直接省略。`dsh.log` 只保留脱敏内容，不能用它恢复原始启动 URL。不要将 launcher 的 stdout 重定向或 tee 到持久文件。stop 同时核对并清理本次 DSH 和日志转发进程的 PID/启动时间。

构建子进程也使用环境白名单：保留工具路径、证书/代理和批准的 `AURIGA_PNPM_STORE`、`COREPACK_HOME` 缓存，HOME、DSH_HOME、npm 用户/全局配置与临时目录固定在本次 StateDir；不继承 NODE_OPTIONS、API 凭据、真实用户 DSH 配置或 PSModulePath。批准的构建缓存可复用，验收宿主的数据目录仍独立。

```powershell
# 父目录应已存在；StateDir 本身必须不存在，每轮换一个新目录。
$pluginState = Join-Path 'D:\auriga-beta-state' ('plugin-' + [guid]::NewGuid().ToString('N'))
node scripts/beta-ui/plugin-dsh.mjs launch --state $pluginState
node scripts/beta-ui/plugin-dsh.mjs status --state $pluginState
# 验收结束后执行；可重复 stop，不按端口或进程名杀其他 DSH。
node scripts/beta-ui/plugin-dsh.mjs stop --state $pluginState
```

隔离依据是上游公开的 [DSH_HOME 目录规则](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/util/home-paths/README.md) 和 [CLI/profile/plugin 管理规则](https://github.com/deepseek-ai/deepseek-harness/blob/master/apps/cli/reference/README.md)。脚本把 `DSH_HOME` 固定为 `StateDir/dsh`，profile 为其中新建的 `profiles/web`；HOME、USERPROFILE、XDG、AppData、包缓存与临时目录也全部重定向到 StateDir，子进程从空 workspace 启动，不继承 DSH/Auriga 覆盖、API 凭据、Node 注入或现有用户配置。不复制用户 profile，也不修改系统设置。拒绝真实 DSH_HOME、`.dsh`、用户主目录及其祖先、已有 StateDir、UNC、目录链接和 junction 祖先。推荐仓库外独立私有目录；工位内测试仅允许被 git 忽略的 `.dev/plugin-dsh/` 子目录。StateDir 设置仅当前用户访问的 ACL，stop 保留数据和日志，不删除或复用它。

脚本只读取 Beta 公开身份源列表来选择团队账号 provider，并写入无凭据的 profile patch：平台和 issuer 为 `http://127.0.0.1:18080`，clientId 为 `auriga-beta-native`，显式 `allowLocalHttp: true`。scope `auriga.connect` 由现有本机登录流程申请，不另造可见授权步骤。不配置 Team 地址或网络信任根来伪装通信成功。

1. 用独立浏览器 profile 打开脚本输出的本机 URL。URL 包含 DSH 原生启动票据，只私下打开，不贴报告。先在 DSH 插件管理中确认 `auriga-dsh` 已安装、启用且 active，再确认侧栏 Auriga 功能入口、原生设置入口和登录页面实际可用；路由可达不等于浏览器模块完成挂载。
2. 打开 DSH 设置中的 Auriga，确认平台地址、实际生效地址和本机设备名。可测试连接、保存同一平台并重读；不随意改到其他平台。保存业务设置不会把本次 client/provider 配置发送到另一安装。
3. 进入普通 Auriga 本机登录页，使用已注册团队账号输入口令，再完成真实 TOTP，登录后返回当前 DSH。设备凭据获取在现有内部流程完成；不要求另点“登录总部”，不把门户登录当插件已登录。不要在命令行、脚本配置、报告或截图写入密码、验证码、二维码、Cookie 或令牌。
4. 刷新页面并打开第二个 tab，确认身份保持、设置仍可读。若验收设备刷新令牌续期，需跨过真实 access token 到期时间并观察后续请求仍成功；一次页面刷新只证明浏览器会话保持。不要导出 HAR 或复制凭据文件。
5. Beta 未启动 Team 时，继续完成登录、设置、刷新和退出，功能页应显示“通信未就绪”（或同义的宿主本地化状态），不能显示已连接、模拟成员或消息。身份已登录与通信未就绪可同时成立；双 DSH 收发、模型生成和计量仍另行验收。实际显示缺失或登录被 Team 故障阻断时记录缺口，交主控处理，勿改插件绕过。
6. 在开发者工具内存中保留一个已成功的受保护 DSH 原生 API 请求及当前插件旧会话，执行插件退出。旧 tab 刷新应回登录；重放旧 Cookie 请求必须被拒绝（401/403 或回登录，不能返回受保护数据），旧 WS 应关闭，插件状态应为未登录且不可恢复旧身份。区分仅宿主启动 Cookie 与插件登录会话；不要用公开登录页或公开静态资源验证撤销。若远端撤销仍 pending，等 Beta 可达后确认 complete，不能以本地退出冒远端撤销成功。随后重新登录应走新流程，退出不停止 DSH。
7. 执行 stop，核对 status 为 running=false，本次随机端口释放；用户原 DSH 和 Beta 仍运行。stop 核对登记 PID 与 Windows 启动时间，PID 复用会拒绝，绝不按端口杀进程。失败日志和测试数据保留在 StateDir 供私下排查，重新验收使用另一个全新目录。

```powershell
node --test e2e/beta-ui/plugin-dsh.test.mjs
```

此测试覆盖参数拒绝、真实 profile 禁区、全新目录、junction 逃逸、宿主/构建环境隔离、PID 复用拒杀、真实日志转发及 StateDir 全文件无假票据，以及宿主缺能力、锁定 integrity 不符、隔离 pnpm 版本不符时拒绝。launch 自检检查真实宿主启动、原生启动票据交换、插件本机登录/status、设置状态和设置模块可用，并要求实际生效平台等于 Beta 门户；结果记入 plugin-dsh.json 的 startupChecks。没有代填团队凭据，不计真人 TOTP、刷新续期、退出撤销或完整浏览器视觉通过。构建、安装、宿主初始化或路由失败均非通过；查看对应 pnpm-prepare/pnpm-version/host-install/build/profile-init/plugin-install/profile-config/dsh 日志，保留首次失败。启动未完成时只清理本次自有宿主，仍保留 StateDir。命令崩溃留下 plugin-dsh.lock 时，先核对原命令已退出再由主控处置，不能绕过活跃命令锁。

原 `0.2.0-rc.2` 宿主缺少现有插件要求的原生连接能力。按主控决定，本脚手架统一使用上述精确 rc.3 基线，不修改插件、不提供旧版本兼容；真人团队账号登录、TOTP、刷新和退出撤销仍由 root 按本节完成。
