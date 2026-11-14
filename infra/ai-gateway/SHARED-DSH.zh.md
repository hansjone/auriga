# N：共享 DSH 模型与消息适配

基线：main `f6001a02`，K `contracts/shared-dsh` v1。本批实现可独立测试的适配层；未挂载公网路由、未启用共享运行时，也未宣称正常页面已可用。现有独立/桌面认证入口保持原协议，不能把共享请求送入旧 owner/account 认证当作兼容路径。

## 能力与模型

`internal/sharedauth.New` 必须注入在线 Authority、用途专属 Ed25519 公钥配置及时间源。`Issue` 仅供经服务认证的内部调用，按可信 transport 的 tenant、服务器登记的 sessionId 和 taskId 查询权威；只签最长600秒的模型/消息能力。`Verify` 严格检查 K 规范 JSON、无 padding base64url、EdDSA/typ/kid、完整 SessionBinding、任务、scope、credentialVersion、期限和实时权威。重复 JSON 键、未知字段、缺失 sessionId、错 audience、旧 epoch 均拒绝。能力无一次性消费语义；每次调用查当前授权，不建并行 token 数据库。

`internal/modelgate.NewSharedProvider` 必须注入 SharedRelay 和 SharedLedger。Invoke 将不可变成员归属传给 relay；relay 只按该成员选择既有 Bifrost 路由和权限，上游 Key 留在网关。SharedLedger.Begin 用完整绑定+callId 原子预留并核请求摘要，重复或结果不确定不再调用模型。Finish 使用独立10秒上下文结算已知用量或隔离未知用量；取消不免除计量。所有流式分片发布前重新查权威，静默流每250毫秒查权威，撤销/过期触发合作取消，迟到分片丢弃。relay/authority 必须响应 context；这不提供进程级强杀或全池故障隔离。

## 消息

`internal/network.NewSharedBroker` 注入共享能力 Gate、消息权威和既有 msgnet-v2 传输适配器。SharedTarget 只是内部目标描述，不是新增网络协议。发送方取能力绑定；目标每次由权威解析，不能取物理连接 owner。云端目标与桌面 node/local 二选一，同租户且必须明确获权。父子会话、桌面设备和 boot/connection epoch 的现有授权由 SharedMessageAuthority 核对。

Enqueue 复制载荷，按完整发送/接收绑定、目标和 messageId 去重，摘要不同拒绝。单条64KiB、总条数由构造参数限制（1–10000），背压涵盖待发送与终态墓碑。Flush 每条重新核发送方及目标，旧目标绑定拒绝，过期积压丢弃；每条只尝试一次。失败和不确定结果不自动重放副作用命令，已尝试的重复请求返回 outcome_unknown，由现有 v2 收据流程核对。Transport 必须只写单一目标私有邮箱，可以复用同一物理 NATS 连接；禁止将载荷广播到池内成员。

Receive 核对接收能力与精确目标绑定，并再核发送方和现有消息授权；重复副作用执行被抑制。ReceiveDesktop 的 source 必须来自已认证 msgnet-v2 ingress 的 NodeIdentity，不能从用户 body 构造；AuthorizeDesktop 每次查 node/local 授权与当前 boot/epoch。接收去重同样有界，旧能力不能把旧积压注入新会话。

本层内存队列仅用于准入，不替代 v2 持久邮箱。重启丢弃未发准入项，不恢复执行；跨进程/重启及超过能力期限的持久去重、结果核对仍由 v2 messageId/收据保证。每个实例的总上限会在饱和时对其他成员施加背压，尚无成员公平调度保证。

## 联合接线清单（W→IAM→E→R→N→P→U→V）

1. W/P：Authority.Resolve 必须按 tenant+sessionId 查询已登记完整归属，核对 ready placement、成员及平台登录、task 存活、撤销 JTI、authGeneration 和 audience credentialVersion。K 当前 ResolveSharedDsh RPC 只查 placement，无 session 查询或 task/JTI 查询接口；需要主控交 K 冻结新增端口，或由 P 已登记会话权威与 W/IAM 组合实现。不得在 N 自建归属真源。
2. IAM/模型权限：沿既有 IAM/org 生命周期核成员和模型调用权限；企业 audience 不由本层签发。模型/消息 credentialVersion 变化立即拒绝旧能力。Authority 无法核实则拒绝。
3. E/R：只允许受信内部认证调用 Issue 和 Invoke，可信 tenant 来自 transport，不能从未验签 claims 选租户。撤销连接、停池收据和目录处置仍归 E/R/W。
4. N：将 SharedRelay 接既有 Bifrost 成员路由，SharedLedger 接 durable budget 预留、结算及恢复流程，保留完整绑定+callId 去重。当前只有必需端口与测试替身，没有生产账本实现，不能直接启用。
5. N/collab：将 SharedMessageAuthority、AuthorizeDesktop 和 SharedTransport 接现有 v2 authority、单目标签名帧、邮箱及收据；保留原协议，不修改 collab 表或另开协议。测试已验证双向桌面端口替身，真实 NATS/桌面接线尚未验收。
6. P：自有 provider/helper 必须给出服务器登记 sessionId 和 taskId，不接受浏览器自报 owner/member。服务端内部注册这些对象后再签能力；后台任务同样不得缺省回落 owner。
7. U/V：主控串行部署后补正常门户两成员并发、真实账本计量、真实 NATS/桌面双向消息、切换积压及撤销验收。当前产物不包含页面和部署。

## 首期风险

共享池仅限互信团队。成员可能互相看到文件，并可能以 owner 能力操作。成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下。同一进程卡死、崩溃或取消超时会中断全池任务。需要保密或独立故障域请使用独立容器。

上述签名、模型计量及消息检查只约束自研调用路径，不修复原生 Cookie/browserPeer、/connect、原生文件读取或企业 skill 身份冒用。保留设计3.1已接受风险，不得解释为成员隔离证明。
