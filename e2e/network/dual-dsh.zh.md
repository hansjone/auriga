# SEND-4：两个独立完整 DSH 的收发验收驱动

`source-dual-dsh.mjs --run` 启动两个精确 `@deepseek-ai/dsh@0.2.1-rc.3` CLI 进程，
每端独立 DSH_HOME、web profile、启动 cwd、普通目标会话 cwd、非目标会话 cwd。
复用既有 rc.3 integrity/npm 锁、隔离 pnpm 11.28.6 和环境白名单。
不读写用户 DSH、系统信任或真实部署，也不继承模型密钥。

**当前验证边界：** 已合入 EDGE-SUBMIT 独立 submit 上游修正（40846a8），
路由 preflight 使用分别独立的 model HTTP 与 network HTTPS 上游，并核固定信任。
主控对eab3674的Control已完成两个独立DSH普通登录、双向收发及四条SEND-4检查，进程全部正常退出。
随后原始基线因源码文件丢失未能启动，SkipSubmissionCommit尚未执行，因此尚未完成整套验收。
已用独占微型夹具确认Windows Java Files.walk会穿透profile中的pnpm junction并删除源码目标；
本轮将profile移出Java清理根，交Node单独清理，并在基线之前核对清理后源码哈希。
本工位PG此前报restricted-token 87/3，完整基线及业务反证仍需root在可启动PG的环境复验。
`dual-dsh-blocker.zh.md` 描述的是修复候选合入前的历史首失。

## 执行与证据

在工位根保留派工环境、锁定依赖和新鲜 Java 桥测试 classpath：

```powershell
node --test e2e/network/dual-dsh.test.mjs e2e/network/plugin-send.test.mjs
node --test e2e/network/dual-cleanup.test.mjs
.\mvnw.cmd -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false'
node e2e/network/support/dual-prepare.mjs --compile
# 无PG，仅核代理的真实入口头满足生产IAM校验；不认证或生成凭据：
node e2e/network/entry-check.mjs --run
node e2e/network/support/native-install.mjs --install
node e2e/network/dual-cli-check.mjs --run
# 启用真实连接配置，仅核两个隔离 profile 的 activeOrigin/status 与登录前准入，不认证：
node e2e/network/dual-cli-check.mjs --run --connect
# 故障诊断探针：真实CLI分别核受信HTTPS上游503与不受信TLS，均应失败；不是登录成功验收：
node e2e/network/dual-cli-check.mjs --run --connect --diagnostic
# 真实 PG/Broker/HTTPS/WSS 与两个完整客户端；失败不会 skip：
node e2e/network/source-dual-dsh.mjs --run
# 提交后由主控在能启动 PG 的环境执行：
$head = (git rev-parse HEAD).Trim()
.\e2e\network\verify-dual-dsh.ps1 -ExpectedHead $head -CompileOnly
.\e2e\network\verify-dual-dsh.ps1 -ExpectedHead $head -Run
```

`dual-cli-check` 只核两个完整 CLI 的公开控制、隔离和释放，未配置平台，不证明认证或 SEND。
其 `--connect` 模式使用随机回环 HTTPS 安装 Origin、每端独立 network 配置及公开根公钥，
不附加认证后端、不登录、不请求模型。要求两端 settings.activeOrigin 生效、status200且
available=true/loggedIn=false，并确认登录前业务控制仍被原生准入拒绝401。
`--diagnostic` 在同一真实CLI登录begin调用中，A使用受信CA并收到无后端代理的503，B故意使用
另一份CA而被TLS拒绝。它只核诊断能区分上游HTTP失败和证书失败，不生成成功IAM响应、凭据或session。

### 登录故障诊断

本机登录路由保留兜底503的公共响应与鉴权规则；宿主stderr增加一行
`[auriga-dsh] local route failure`，只含固定操作名、类别、白名单错误码及上游状态。
类别包括tls、network、platform_http、native_step、timeout、cancelled、unexpected。
不记录原异常message/stack、URL、请求体/头、任意上游code或任何凭据。
`A/B-host.log`已保存脱敏stderr；错误日志自身失败也不改变原HTTP响应。
root重跑后先核operation为begin还是advance/resume，再依据类别检查CA/网络、IAM状态或原生步骤。
这些分类不构成根因推测，也不放宽任何登录或业务断言。
上游400且code为common.validation_failed时，路由现返回相同400/code，避免误报为服务不可用；
其他未知上游code或服务端状态保留原映射，不向客户端暴露平台异常细节。
直接运行全链默认安装本次独立锁定 runtime；开发时可通过 `AURIGA_DSH_RUNTIME` 指定
已安装的锁定 rc.3 目录。归档验收清除此覆盖，每个变体自行安装，不继承其他工位 runtime。

### 共享宿主 npm 缓存

隔离安装仍使用原锁文件和 SHA512 校验。`npm ci` 加 `--prefer-offline`，公共包缓存跨工位、
归档及变体复用；`node_modules`、DSH_HOME、profile、cwd、npmrc 和 Corepack 目录仍独立。
该设置也用于 Beta 的 `scripts/beta-ui/plugin-dsh.mjs` 宿主安装，不用于其构建或用户 DSH。
缓存只接纳锁定的 `registry.npmjs.org` 包，不保存任何 DSH 凭据或 profile。

可在运行 verify 前指定获准的持久缓存，例如与已有 pnpm store 并列：

```powershell
$env:AURIGA_DSH_HOST_NPM_CACHE = Join-Path (Split-Path $env:AURIGA_PNPM_STORE -Parent) 'npm-dsh-host'
# 可选；仅改变 npm 宿主安装期限，默认仍为 300000 ms，不改变业务超时/重试：
$env:AURIGA_DSH_HOST_INSTALL_TIMEOUT_MS = '600000'
```

未指定缓存时使用父进程用户目录下 `.cache/auriga/npm-dsh-host`，默认在仓库外；首次运行
仍可能下载缺失包，后续优先复用完整性缓存。`installNativeRuntime(runtime, {npmCache, timeoutMs})`
还允许调用方显式传入选项，优先于环境变量。不要把缓存指向真实 DSH profile。
失败的 `host-install.log` 保存完整命令、缓存、期限、耗时、退出码、signal 和 stderr，异步安装另记录 killed；
超时明确写为 `timed out after ... ms`，不会再只显示 `exit null`。没有自动重试或跳过安装。

全链证据位于 `target/dual-dsh-*`：`manifest.json`、真实脱敏进程日志、NATS 应用帧、
原请求/结果、故障丢包、RLS 只读 PG 快照、两端真实 inspect/provider 回读。
`messages.json` 关联 message、delivery、recipient、digest、host_request_id 和签名回执。
记录两个原生进程的 PID/父 PID、Windows 创建时间或 Linux boot/start ticks、对象谱系与 cwd。
凭据/profile 和 TLS 私钥仅在测试私有目录，结束删除；启动票据仅留内存，日志脱敏。
每端安装及 profile patch 后再次调用公开 `--dump-config`，保存 `A/B-configured-profile.log`；
不要用安装前的 `A/B-profile.log` 判断最终插件启用或连接配置。
驱动等待观察插件实际获得原生 profile 服务，并在既有30秒界限内核公开 settings 的
platformOrigin/deviceName/activeOrigin，随后只请求一次初始 status；错误配置、HTTP错误及
status503仍立即失败，不重试登录、不延长业务期限。`A/B-profile-ready.json` 只保存这些公开
设置与 available/loggedIn，不保存 CSRF、启动票据或任何凭据。
归档运行的 Java/PG 临时目录放在原工位 `e2e/network/target/dual-private-*`，独占创建并
校验真实父路径，避免 Windows 原生 PG 的归档嵌套长路径问题；不删除其他运行的目录。
Node的profile私有根与Java私有目录平级，内部每端DSH状态独立。该根复用现有Java私有目录
创建器，以保持原有Windows ACL/POSIX 0700，不把凭据放入继承额外读取权限的普通临时目录。
CompileOnly实际调用该创建器并验证私有权限，完成后由Node清理。profile含指向归档源码的pnpm
junction，绝不能置于Java Files.walk清理范围内；Windows JVM默认会将junction视为目录。
Java只删除自己的私有子目录；CLI正常退出、Java关闭后，Node删除其自有根及链接条目，保留链接目标。
dual-cleanup回归仅用独占微型源码哨兵，运行真实JVM和Node两阶段清理，确认目标原字节不变。
先确认真实 Java/PG/gateway 可启动，再准备锁定 DSH runtime；启动失败不下载宿主，也不 skip。

## 真实服务与测试辅助边界

生产插件经普通本机 HTTP 登录接口完成账号口令、原生首个 TOTP 登记、设备凭据换取与验签，
随后生产 Team MCP 的在线鉴权、endpoint 登记完成，默认网络 lifecycle 才开始连接。
不会预填插件 vault/device，或通过注入全业务 provider 假造认证成功。
测试预置仅含团队账号/身份/角色和安装密钥，不预置 session、设备、source、消息或接收关系。

`dual-prepare.mjs` 只在 target 生成原 **测试桥** 的副本：测试 TokenService 改用本轮随机
公共 Origin；原网络 HTTPS 测试 Web 改为受信回环 HTTP 内跳，导入生产登录控制器；
原 IAM/mailbox gRPC 装配追加生产 Org authorization 和 Collab communication 服务。
追加的 Team listener 使用生产 DeviceAuthenticator、IAM/Org generated RPC、真实 collab admission。
保存原测试源码哈希和完整生成源码；不改产品文件或既有测试断言，也不发行替代业务回包。

另以 `go test -c -overlay` 编译独立 edge 测试进程，加载生产配置校验和 `NewGateway/Handler`，
portal 测试 TLS listener 使用与生产 listener 相同的固定角色上下文。
`upstreams.network-submit` 指向本轮 network runtime 的真实 HTTPS listener；
`networkSubmitHop` 显式配置本轮私有证书、密钥、CA 和固定 DNS `localhost`，
不借用 IAM hop 或 ai-device/model 上游，不关闭证书验证，也不允许 HTTP 降级。
edge-ready 保存 PID、入口地址、实际 submit 上游和 serverName；每条提交证据关联 edge PID。
验收同时核 edge 与 gateway/DSH 不同进程、正确上游及 edge 正常关闭。

公共入口为仅转发的测试 HTTPS 代理，统一固定 Origin/issuer，并向回环 IAM 内跳供应固定
安装入口 authority。Submit 路径固定为测试代理→生产 edge Handler→network runtime HTTPS listener；
IAM跳的X-Auriga-Client-IP从实际回环socket peer产生；任意传入的同名头、入口及forwarded身份头均先剥离。
entry-check把这些头交给生产RequestIdentity（含sameOrigin/tenant/clientIp）验证：旧缺头请求稳定400，
新请求入口校验通过。该探针不运行LoginManager/PG，不返回模拟认证成功。
真实 Broker WSS 和所有签名/准入/持久事务仍由生产实现完成。
代理不从请求推导 tenant/owner/session，不改请求/响应业务字节；WSS 只重组传输分片并观察应用帧，
不记录 CONNECT 凭据。登录与 Team 的测试代理路径保持原范围，这不是 edge 全产品验收。
preflight 另外运行真实 model/network handler 分流、IP/域名路由和上述 edge 子进程生命周期检查；
这些未授权/传输探针不伪造 SEND 成功，仍不能代替双 DSH/PG 全链。

`dual-observer` 是测试插件，随正常 CLI 插件安装流程加载。控制接口经过原生准入和同源校验，
仅调用公开 agents.create、tools.execute、sessionController、SessionPersistence。
它观察并原样委托 prompt；不提供假 controller/agent，不直接调用工具定义 execute。
该接口只存在测试插件目录，不进入 Auriga 成品。关闭时释放真实 agent handle 和根 fiber，
要求 CLI、edge、gateway/Broker、Java/PG 正常退出；强制停止仍计失败。
父进程持有的 IPC 只接收原生依赖就绪通知、发送单一停机命令；停机同样释放 agent handle
并 dispose 根 fiber。它使登录前失败也能正常清理，不经 IPC 执行业务，也不绕过 HTTP 准入。

## 正控、故障和反证

- 两端普通登录、真实 ToolRuntime join、签名 Get、原生会话/typed-local 映射。
- 用户接收开关经正式本机 messaging/receive HTTP、CSRF 和 receive_set 生效。
- A→B：丢第一份真实 RECEIPT；丢包前同步回读真实文件账本的 accepted/原 receipt。
  后续真正 RECEIPT_COMMITTED、持久正文及 host_request_id 核对通过前不算成功；同 recipient 仅一次 prompt。
- B→A：生产 HTTPS 上游完整提交并返回 200 后丢响应；第一次工具调用保留未知原操作，
  同体再次调用真实工具，核原键/原字节、原结果字节、PG 四表各单份及对端持久接受。
- 关闭 B 接收后发送新正文：拒绝、两次新签名 FETCH、消息/邮箱/recipient 零新增，
  目标零额外 prompt；两端非目标会话始终零输入，各自 cwd 保持不变。
- SkipSubmissionCommit 只在归档内去掉生产 outbox 的原请求耐久提交。
  同一双客户端测试必须命中 `SEND-4 original submission must be durable before HTTPS`。
  其他初始化、SQL、超时、编译或清理错误均拒绝计为反证。

verify 分别解包相同精确 HEAD，独立锁定依赖、typecheck、编译两种测试装配，
核测试/源码 SHA256 和命令退出码。Control 全链通过后，必须原样运行原 T3b/T4/SEND-2/SEND-3
驱动及其原判定；变体不修改这些测试。CompileOnly 不启动 PG，也不证明收发或业务反证。
每个变体的双DSH清理结束后，先记录afterDualTests并核对所有列入门禁的源码/测试哈希，
其中包含基线导入的nats-binary.js；完整性核对通过后才可启动原始基线。任何缺失/改动均失败，
不以恢复文件、跳过基线或放宽反证判据掩盖清理问题。
驱动不配置模型凭据，也不把模型回复作为断言；本线证明真实宿主持久接收，不能称为模型往返对话。
进程重启/换 epoch、完整首联、递归子会话、跨租户及容量/HA 仍按后续任务验收。
