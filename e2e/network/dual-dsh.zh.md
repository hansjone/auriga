# SEND-4：两个独立完整 DSH 的收发验收驱动

`source-dual-dsh.mjs --run` 启动两个精确 `@deepseek-ai/dsh@0.2.1-rc.3` CLI 进程，
每端独立 DSH_HOME、web profile、启动 cwd、普通目标会话 cwd、非目标会话 cwd。
复用既有 rc.3 integrity/npm 锁、隔离 pnpm 11.28.6 和环境白名单。
不读写用户 DSH、系统信任或真实部署，也不继承模型密钥。

**当前验证边界：** EDGE-SUBMIT 候选合入工位后，原路由 preflight 已转绿。
双 CLI 的隔离、公开 agent 工厂、原生 session/provider 和正常退出预检已通过；
本 Windows 沙箱的 `pg_ctl` 报 restricted-token 创建失败，全链尚未进入普通登录。
因此以下收发/故障/反证断言是已实现的验收路径，不能据此宣称业务验收通过。
`dual-dsh-blocker.zh.md` 描述的是修复候选合入前的历史首失。

## 执行与证据

在工位根保留派工环境、锁定依赖和新鲜 Java 桥测试 classpath：

```powershell
node --test e2e/network/dual-dsh.test.mjs e2e/network/plugin-send.test.mjs
.\mvnw.cmd -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false'
node e2e/network/support/dual-prepare.mjs --compile
node e2e/network/support/native-install.mjs --install
node e2e/network/dual-cli-check.mjs --run
# 真实 PG/Broker/HTTPS/WSS 与两个完整客户端；失败不会 skip：
node e2e/network/source-dual-dsh.mjs --run
# 提交后由主控在能启动 PG 的环境执行：
$head = (git rev-parse HEAD).Trim()
.\e2e\network\verify-dual-dsh.ps1 -ExpectedHead $head -CompileOnly
.\e2e\network\verify-dual-dsh.ps1 -ExpectedHead $head -Run
```

`dual-cli-check` 只核两个完整 CLI 的公开控制、隔离和释放，未配置平台，不证明认证或 SEND。
直接运行全链默认安装本次独立锁定 runtime；开发时可通过 `AURIGA_DSH_RUNTIME` 指定
已安装的锁定 rc.3 目录。归档验收清除此覆盖，每个变体自行安装，不继承其他工位 runtime。

全链证据位于 `target/dual-dsh-*`：`manifest.json`、真实脱敏进程日志、NATS 应用帧、
原请求/结果、故障丢包、RLS 只读 PG 快照、两端真实 inspect/provider 回读。
`messages.json` 关联 message、delivery、recipient、digest、host_request_id 和签名回执。
记录两个原生进程的 PID/父 PID、Windows 创建时间或 Linux boot/start ticks、对象谱系与 cwd。
凭据/profile 和 TLS 私钥仅在测试私有目录，结束删除；启动票据仅留内存，日志脱敏。

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

公共入口为仅转发的测试 HTTPS 代理，统一固定 Origin/issuer，并向回环 IAM 内跳供应固定
安装入口 authority。网关 Submit HTTPS、真实 Broker WSS 和所有签名/准入/持久事务仍由生产实现完成。
代理不从请求推导 tenant/owner/session，不改请求/响应业务字节；WSS 只重组传输分片并观察应用帧，
不记录 CONNECT 凭据。该代理不是生产 edge 的完整验收，edge 精确路径由既有 preflight 单独验证。

`dual-observer` 是测试插件，随正常 CLI 插件安装流程加载。控制接口经过原生准入和同源校验，
仅调用公开 agents.create、tools.execute、sessionController、SessionPersistence。
它观察并原样委托 prompt；不提供假 controller/agent，不直接调用工具定义 execute。
该接口只存在测试插件目录，不进入 Auriga 成品。关闭时释放真实 agent handle 和根 fiber，
要求 CLI、gateway/Broker、Java/PG 正常退出；强制停止仍计失败。

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
没有模型提供方或自动模型回复，本线证明真实宿主持久接收，不能称为模型往返对话。
进程重启/换 epoch、完整首联、递归子会话、跨租户及容量/HA 仍按后续任务验收。
