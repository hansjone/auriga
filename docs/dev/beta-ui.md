# Windows 原生 Beta 管理台

这是一套真人验收环境，使用本机已有的 PostgreSQL 16，不启动 Docker、NATS、Team 或 DSH。门户固定为 `http://127.0.0.1:18080`，管理台固定为 `http://127.0.0.1:18082`；请在浏览器直接打开这些 URL，不改 hosts。它仅供本机开发，不用于对外部署。

## 准备与初始化

需要 JDK 25、Go 1.27、Node 22.12+、仓库规定的 pnpm，以及 PostgreSQL 16 的 `psql.exe`。构建使用锁文件和独立 node_modules，不升级依赖；需要设置工程工作法中的 `AURIGA_PNPM_STORE`。所有操作从仓库根执行。派工会话之外先 dot-source 主控提供的 auriga-env.ps1；没有该脚本的普通开发环境，应自行设置 AURIGA_PNPM_STORE 指向可写的依赖缓存目录。

```powershell
$betaState = Join-Path $env:LOCALAPPDATA 'Auriga\beta-ui'
.\scripts\beta-ui\beta-ui.ps1 init -StateDir $betaState
```

StateDir 必须是仓库外的本机普通目录。初始化先设置并核验仅当前账号可访问、禁止继承的 ACL，再产生密码、加密密钥和本机证书。不会把 CA 加入系统证书库。初始化已有目录会拒绝重新生成密钥；保留该目录才能重新登录同一个数据库。

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
