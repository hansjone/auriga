# Desktop 认证 broker

本目录由 C 维护。`build.mjs` 生成同一个自包含制品，含 Windows helper、冻结 schema、锁定 Ajv 闭包、NATS/WSS 与验签代码闭包，以及逐文件 SHA-256 清单。Desktop 放在 `auriga/shell-auth/`，独立插件由 D 负责引用及打包。网络闭包复用仓库现有协议实现，运行时无需插件源码目录；未增加或升级依赖。

**Desktop 目前只出 Windows 版。** Unix 尚无真实对端校验，平台认证继续失败关闭；独立插件在 Unix 上也不能宣称具备该平台通道。

`startDesktopAuth({root,dataHome})` 读取安装级 `desktop-origin.json`，启动本用户 broker、随机端口控制页及 OS IPC。平台未配置或 helper 不可用时保留本地 DSH。设备登录只打开 HTTPS 门户 `/device`，不创建云端实例。控制页同步显示本窗口尝试的用户码、期限和人工核对提示；其他窗口及插件快照不接收该尝试。设备码及平台令牌始终留在 broker。取消、跳过、完成和超时清除用户码；之后可重新登录。退出后仅附加减少自动行为的 `mode=manual`，不自动填码或批准设备。

Windows helper 使用当前 SID 的命名管道 DACL、禁远程标志、真实客户端 SID、受控子进程及其启动时刻和随机能力准入，每条请求和存活检查重核受控进程。客户端以 Identification 级别连接，并在发送能力前核实服务端 PID 的用户 SID 与本 helper 路径；服务端首实例独占管道名。能力经 DSH 的继承 fd3 交给插件后端，不写 argv、env、配置或页面。D 从 fd3 读取首行描述符后调用 `connectIpc(descriptor)`，可用 `call`、`subscribe`、`hostContext`、`enterprise`、`streamCommand` 和 `subscribeStream`。IPC 断开或订阅返回 null 时必须立即停用平台能力。真实跨用户账号拒绝尚未验收。

`hostContext(message)` 接收 K2 的 `ShellAuthHostContextCommand`，返回完整 `ShellAuthHostContextResult`。D 从可信宿主回调先登记 session，再登记携带父句柄的 tool；不能把模型或 skill 输入当宿主来源。owner 为 broker 按真实连接生成的 UUID，句柄不能跨连接使用。上下文最长 600 秒且不超过认证/来源期限；撤销 session 会撤其 tool 并关闭关联流。请求必须带当前 epoch/revision 和最多 30 秒的期限；相同请求不续期，时钟回拨不能恢复已到期请求。每连接最多保留 512 个未过期请求，到期自动释放，不累计历史请求总数。

`enterprise(message)` 接收 `ShellAuthHostCredentialsRequest`，返回完整 `ShellAuthHostCredentialsResult`，不再支持无上下文读取。D 先核本人任务能力和 raw/outbound scope，再提交当前句柄。C 每次向 IAM 读取权威当前凭据，校验来源绑定及 credentialVersion；相同 requestId 也重读。`allowRawAgentToken` 从受信安装配置实时读取，缺省 true，读取失败关闭 raw；提交前再次检查，outbound 不受该开关影响。凭据不缓存、广播或落盘；D 只将其用于对应本人 handler。版本更新不改变认证来源代次。

`Broker` 负责 epoch/revision、请求 ID 幂等、设备轮询、取消归属、晚结果撤销、立即本地退出、DPAPI 撤销队列和刷新单飞。页面只开放冻结的 snapshot/open_login/cancel_login/skip_login，不能读取凭据。控制页用 30 秒一次性票据和 15 分钟内存双令牌保护；每次 POST 同时核对 `X-Auriga-Shell-Nonce` 与交换时的 clientNonce，页面仅在内存保存它们；刷新需重新从启动器打开。启动时已认证则不自动弹出控制页。

安装器中，认证尚未就绪或 unreachable 只暂停平台请求，不因这些状态清除现有共享视图或重启 DSH。主动退出、撤销和已认证身份变化仍停用共享视图并要求新 DSH 会话。资源本身的签名、租约及撤销验证独立保留，离线不会绕过这些门禁。

固定 JSON 代理直接读取 AUTH-K 操作清单，校验请求及响应，按 audience 内部取票、按操作白名单转发头，重核身份和期限后才交结果，不自动重放写请求。SSE、流额度控制和上传引用已实现。上传存于本用户私有目录，每块 fsync 后应答，结束核摘要，消费后删除。

Team 的 session 句柄依次执行 node register、bind，C 保存远端返回的 endpoint/binding；工具只接受同会话的 tool 句柄，由 C 构造 SessionMeta。输入中的 endpoint/binding/hostSession 只能作为权威记录的断言。MCP 初始化、工具调用和原始响应流保持现有协议。

网络安装信息通过受信配置的 `network: {realm,wssUrl,roots}` 提供。每连接 owner 的多个会话复用一条 NATS/WSS 链路，并发 open 合并；票据、subject 和平台凭据不交插件。上行仅接受冻结帧，下行校验根签名清单及公告、响应签名、连接 nonce/epoch/请求关联和 FETCH 页成员；原始字节经有界流转交。信任水位由同一设备的单写者持久保存。网络 submit 只接受当前宿主会话、已验签且未过期的 source GET 观察；创建回执不授予发送许可。撤销先停发，晚到观察不能恢复已停来源。连接过期、身份改变或宿主断线后关闭，业务写操作不自动重放。

随 Desktop 安装器导出诊断：

```text
auriga-desktop.cmd diagnostics export --output <目录> --tenant <UUID> --provider <UUID>
```

使用随包 Node，默认两小时、最大 24 小时。仅读取固定本地 broker/插件日志，使用与落盘相同的正向脱敏投影；不含 journal、vault、资源或完整环境。配置仅投影本地生效布尔值，调度信息仅取已脱敏日志，并标明 IAM 配置/调度材料是否可用。ZIP 清单含摘要和缺源；IAM/audit/ops/Loki 未接时明确 partial=true、退出码 3，不声称服务器观测链已验收。重解析路径拒绝，临时目录用户独享，原子发布且不覆盖。

工作树根目录验证：

```powershell
node --test clients/shell-auth/test/*.test.mjs
node node_modules/eslint/bin/eslint.js --config clients/shell-auth/eslint.config.mjs clients/shell-auth
node node_modules/prettier/bin/prettier.cjs --check clients/shell-auth
$f=Get-ChildItem clients/shell-auth -Filter '*.mjs' | Where-Object {$_.Name -ne 'network-core.mjs'} | Select-Object -ExpandProperty FullName
node node_modules/typescript/bin/tsc --allowJs --checkJs --noEmit --target es2024 --module nodenext --types node --skipLibCheck @f
node node_modules/typescript/bin/tsc -p clients/dsh-plugin/tsconfig.json --noEmit
node --test clients/installer/test/*.test.mjs
node clients/shell-auth/build.mjs clients/shell-auth/target/<空目录>
```

`network-core.mjs` 仅重导出现有 D 项目代码，由 D 项目 tsconfig 检查其实现，并由制品构建核实闭包。测试用运行时生成密钥和合成身份。已覆盖真实 Windows DPAPI/ACL/管道、K2 凭据与句柄、真实 NATS/WSS、用户码页面行为、独立打包后的签名设备流和凭据恢复；安装器另测随包 Node 的诊断命令。替身与页面组件测试不能代替正式 Desktop 启动器、门户 SSO/扫码及真实 DSH 的跨线正常入口验收，由主控串行安排。
