# N：共享 DSH 模型与消息适配

契约沿 `contracts/shared-dsh` v1。模型入口已接到 `--bifrost-config` 主程序，使用 IAM SharedTaskService 在线任务权威、用途能力验签、持久调用账本和既有 Bifrost 成员 relay。消息的 typed-local 目录与真实收发、正常门户对话仍待联合接线和部署验收。纯 fixture 与源码装配不能代替正常页面可用证明。

## 能力与模型

`internal/sharedauth.New` 必须注入在线 Authority、用途专属 Ed25519 公钥配置及时间源。`Issue` 仅供经服务认证的内部调用，按可信 transport 的 tenant、服务器登记的 sessionId 和 taskId 查询权威；只签最长600秒的模型/消息能力。`Verify` 严格检查 K 规范 JSON、无 padding base64url、EdDSA/typ/kid、完整 SessionBinding、任务、scope、credentialVersion、期限和实时权威。重复 JSON 键、未知字段、缺失 sessionId、错 audience、旧 epoch 均拒绝。能力无一次性消费语义；每次调用查当前授权，不建并行 token 数据库。

`internal/modelgate.NewSharedProvider` 必须注入 SharedRelay 和 SharedLedger。Invoke 将不可变成员归属传给 relay，按该成员选择既有 Bifrost 路由和权限。SharedLedger.Begin 用稳定的 tenant/member/callId 原子预留并核原绑定及请求摘要，重复或结果不确定时拒绝重新派发。绑定变化的重试返回冲突，原记录继续用于计量和对账。重复调用可用 `errors.Is(err, sharedauth.ErrConflict)` 识别，绑定冲突返回 `ErrBinding`。Finish 使用独立10秒上下文结算已知用量或保存unknown。

能力期限约束开始派发。Invoke在验签及账本预留完成后再次核凭证期限，然后使用独立的150秒最长运行时间，调用方更短的期限与合作取消仍有效。流运行期间每250毫秒在线核对成员、任务、JTI、完整绑定、scope和credentialVersion，快速分片复用本次调用最近250毫秒内的成功检查，watcher持续强制在线检查。凭证自然到期允许已准入的输出继续，撤销、围栏变化或权威不可用仍触发取消并丢弃迟到分片。

生产组装入口为 `bifrost.NewSharedRuntime(gate, database, origin, keyFile, policy)`，其 Invoke 接自有 provider 调用。实现位于 `internal/sharedmodel`，原 modelgate 导出保留为兼容别名。调用方负责数据库生命周期，结束时 Close 释放 relay 空闲连接。

`PostgresSharedLedger` 使用 `0023_shared_model_calls.sql` 保存完整归属及用量，新增 `0024_shared_model_call_fence.sql` 以 tenant/member/callId 建立持久派发围栏。sessionId也归入原始记录核对，切模式创建新会话时同一callId仍命中旧围栏。0024保留全部旧调用记录，为已有相同稳定身份选择最早原记录，原0023迁移校验和不变。部署时先停用旧版本模型入口，再完成迁移并启动新版本，避免旧账本逻辑继续按绑定版本派发。

两个表均强制租户RLS。并发Begin仅原子提交成功的首次调用获得派发权，提交不确定时拒绝派发。unknown以及重启后dispatching记录均阻止再次执行，结合原预算记录对账。Finish对同一终态幂等，迟到或冲突结果拒绝覆盖已知计量。金额预算及计费由现有relay预算账本承担。

`bifrost.SharedRelay` 每次按binding的tenant/member调用既有MemberKey。模型请求经原参数允许表过滤，服务端设置归属元数据，流式请求要求usage。普通JSON与SSE均提取确认用量，断流或未知计数保存不确定结果。Idempotency-Key与调用账本共用稳定的tenant/member/callId身份，HTTP POST禁用请求体重放。上游模型密钥保存在既有relay。

## 模型生产传输

在现有 `auriga-ai-gateway --bifrost-config <path>` 配置中增加可选 `sharedModel`，该对象存在时启动同一进程的独立 mTLS listener。原成员入口仍沿原地址，公开成员请求不能进入内部模型入口。示例中的 UUID 和密钥路径须替换为部署值：

```json
{
  "listen": "127.0.0.1:8086",
  "origin": "http://bifrost:8080",
  "memberPasswordFile": "/run/secrets/relay-member-password",
  "iam": {
    "target": "platform:9090",
    "serviceTokenFile": "/run/secrets/ai-gateway-service-token",
    "certFile": "/run/secrets/ai-gateway-cert.pem",
    "keyFile": "/run/secrets/ai-gateway-key.pem",
    "caFile": "/run/secrets/platform-ca.pem",
    "serverName": "platform"
  },
  "org": {
    "target": "platform:9090",
    "serviceTokenFile": "/run/secrets/ai-gateway-service-token",
    "certFile": "/run/secrets/ai-gateway-cert.pem",
    "keyFile": "/run/secrets/ai-gateway-key.pem",
    "caFile": "/run/secrets/platform-ca.pem",
    "serverName": "platform"
  },
  "sharedModel": {
    "listen": "0.0.0.0:8087",
    "tenantId": "<deployment-tenant-uuid>",
    "allowedNodeIds": ["<node-uuid>"],
    "tlsCertFile": "/run/secrets/ai-gateway-server-cert.pem",
    "tlsKeyFile": "/run/secrets/ai-gateway-server-key.pem",
    "clientCAFile": "/run/secrets/node-client-ca.pem",
    "modelPublicKeys": {"<iam-model-kid>": "/run/secrets/shared-model-public.pem"}
  }
}
```

`tenantId` 为固定部署租户，不能是全零平台租户；监听器不从请求头或未验签 capability 选择租户。Node 使用现有客户端证书和私钥，其唯一 Node URI SAN 必须精确为 `auriga://node/<allowedNodeId>`，证书链由配置的 Node client CA 验证。即使回环也必须 mTLS，Edge/普通客户端证书、其他 Node 和未验证证书均拒绝。监听器与每次请求均检查证书和精确 Node 身份。部署仅向获准 Node 开放该地址。

`modelPublicKeys` 仅装 IAM 的 model 用途 Ed25519 SPKI `PUBLIC KEY` PEM，每个路径须绝对路径，每份最多8192字节，kid 与 IAM 模型签发配置相同。支持同时安装多个轮换公钥；不装 enterprise/message 私钥或公钥。IAM 签名和任务配置见 [共享任务权威](../../services/iam/impl/SHARED-TASKS.zh.md)。新能力只由受信宿主通过 IAM 签发，这个 listener 不提供 Issue。

启用时，N 从 `AURIGA_AI_PG_DSN` 读取现有 AI 运行时连接、从 `AURIGA_AI_MIGRATION_PG_DSN` 读取迁移连接；沿 `store.Open` 完成原迁移和运行时角色/RLS检查，构造真实 `NewSharedRuntime`。迁移和运行时角色必须分开，不能使用超级用户。配置、CA、公钥或数据库不完整时启动失败，不退回内存账本。IAM/Org 使用原 `team.DialRPC` 的 mTLS 与轮换服务令牌文件，服务令牌不交共享 DSH 进程。

Node 请求为 `POST /internal/shared/model/invoke`，无 query，`Content-Type: application/json`，body 是原 OpenAI chat JSON，最大4MiB。下列头各只能出现一次：

- `Auriga-Shared-Capability`：原样 K 签名串，audience=`auriga.shared.model`、scope=`model.invoke`。
- `Auriga-Shared-Session-Id`：可信宿主登记的共享会话 UUID。
- `Auriga-Shared-Task-Id`：原活主任务 UUID。
- `Idempotency-Key`：可信宿主生成并固定的模型 call UUID。

这些值必须来自 Node 已核对的宿主登记对象；浏览器和模型参数不能声明归属。N 验签后通过 IAM Resolve 精确核完整 binding、当前任务/权限/版本和 JTI，再由现有 provider 原子预留持久调用围栏。不能把成员平台 JWT 或内部服务 JWT 放进 capability 头。raw chat 中的 user/metadata 继续由原 relay 用真实归属覆盖。

`stream=true` 返回标准 `text/event-stream`，每个原 JSON chunk 编码成 `data: <json>\n\n`，最终在线复验及账本结算成功后才发 `data: [DONE]\n\n`。失败流发 `data: {"error":{"code":"<existing-code>","message":"<existing-code>"}}\n\n` 并结束，不发 DONE。Node 必须把 error 或缺 DONE 的 EOF 视为失败/结果不确定。非流回复会缓冲到最终复验和结算成功才返回 JSON，失败不泄露已缓冲正文。

尚未发出流 chunk 时，错误使用 HTTP 400/403/409/413/503 和同型 error JSON；只输出既有 shared/common 错误码，不包含上游正文、密钥或原错误详情。同 call 同 body 重复返回409，改变原绑定或请求摘要拒绝。超时和不确定派发不能自动重放，也不缓存旧回复冒充本次成功。

HTTP 断连、Node取消和进程停止沿原 context 取消供应商请求。模型最大运行150秒、独立结算最长10秒；server 另外允许30秒读请求，写期限195秒，Node总超时应覆盖这个窗口并禁止自动重试。停止时先取消活请求，再等待最多15秒关闭 HTTP handlers，随后释放 relay 与数据库。金额预算和实际供应商清理仍由原 Bifrost 链承担。

## 消息

`internal/network.NewSharedBroker` 注入共享能力 Gate、消息权威和既有 msgnet-v2 传输适配器。SharedTarget 只是内部目标描述，不是新增网络协议。发送方取能力绑定；目标每次由权威解析，不能取物理连接 owner。云端目标与桌面 node/local 二选一，同租户且必须明确获权。父子会话、桌面设备和 boot/connection epoch 的现有授权由 SharedMessageAuthority 核对。

Enqueue 复制载荷，按完整发送/接收绑定、目标和 messageId 去重，摘要不同拒绝。单条64KiB、总条数由构造参数限制（1–10000），背压涵盖待发送与终态墓碑。Flush 每条重新核发送方及目标，旧目标绑定拒绝，过期积压丢弃；每条只尝试一次。失败和不确定结果不自动重放副作用命令，已尝试的重复请求返回 outcome_unknown，由现有 v2 收据流程核对。Transport 必须只写单一目标私有邮箱，可以复用同一物理 NATS 连接；禁止将载荷广播到池内成员。

Receive 核对接收能力与精确目标绑定，并再核发送方和现有消息授权；重复副作用执行被抑制。ReceiveDesktop 的 source 必须来自已认证 msgnet-v2 ingress 的 NodeIdentity，不能从用户 body 构造；AuthorizeDesktop 每次查 node/local 授权与当前 boot/epoch。接收去重同样有界，旧能力不能把旧积压注入新会话。

接线时，Receive/ReceiveDesktop 的 consume 必须先完成既有 v2 EXECUTION_ADMIT 与宿主持久幂等接受，并沿 RECEIPT 回报结果。进程重启或能力窗口结束后的重复 delivery 由这条持久准入和收据链拒绝；本层内存去重只覆盖当前进程窗口。真实宿主完成该接线前，共享接收入口保持待联合验收状态。

本层内存队列仅用于准入，不替代 v2 持久邮箱。重启丢弃未发准入项，不恢复执行；跨进程/重启及超过能力期限的持久去重、结果核对仍由 v2 messageId/收据保证。每个实例的总上限会在饱和时对其他成员施加背压，尚无成员公平调度保证。

`Runtime.SharedMessages(gate, directory, limit)` 将 SharedBroker 接到 `SharedV2Adapter`，复用该 Runtime 的 IAM 活连接登记表与 collab 客户端。directory 提供 W/P 的服务器会话归属、typed-local、已登记 source_binding、producer_epoch 和实时授权。适配器核对完整绑定、目标、当前连接 epoch 与 boot，将单一 session 目标组装为既有 durable/atomic SubmitRequest，经原 `NetworkMailboxService.Submit` 调用 collab。collab 继续核来源观察、目标当前接收权限、第五授权 RPC 和持久邮箱事务。`Runtime.SharedV2(directory).Submit` 返回原始完整 SendResult，供宿主沿现有协议核对结果。

真实接收仍经既有 FETCH/ATTEMPT_BEGIN/EXECUTION_ADMIT/RECEIPT。P 在核验原签名和准入结果后，按 directory 的 typed-local 对应会话调用逻辑 Receive，每个正文只进入指定会话。完整节点 FETCH 页只交受信分发器处理，签名帧及原始证明交现有验证链，普通会话只接收本人条目。P 的真实宿主分发和执行收据属于联合验收。

## 联合接线清单（W→IAM→E→R→N→P→U→V）

1. W/IAM：`sharedauth.NewRPCGate` 已接 IAM SharedTaskService.ResolveSharedTask；IAM 组合生产 Workspace SharedDshSessions 和 IAM本人portal session/任务/JTI/权限权威，完整核 binding、ready、authGeneration 和 credentialVersion。部署必须启用该真实组合，不能提供替身或在 N 自建归属表。
2. IAM/模型权限：沿既有 IAM/org 生命周期核成员和模型调用权限；企业 audience 不由本层签发。模型/消息 credentialVersion 变化立即拒绝旧能力。Authority 无法核实则拒绝。
3. Node：宿主到 Node 的 Unix relay 尚需与上述 mTLS Invoke 完成联合接线，保留稳定 task/call映射、取消、错误和流完成语义。撤销连接、停池收据和目录处置仍归 E/R/W。
4. N 主程序已安装模型生产账本、RPC gate和relay；主控部署时设置用途公钥、Node mTLS配置、数据库与既有relay认证文件。补真实 PG 的并发、重启、RLS 和计量验收。
5. N 的 msgnet-v2 发送已通过真实客户端到 gRPC 测试服务验证，复用来源连接和完整协议结果。联合时将 directory 接 P 的服务器会话登记与 W/IAM 的实时授权，补真实 collab/桌面、P 签名帧分发与接收收据验收。
6. P：自有 provider/helper 必须给出服务器登记 sessionId 和 taskId，不接受浏览器自报 owner/member。服务端内部注册这些对象后再签能力；后台任务同样不得缺省回落 owner。
7. U/V：主控串行部署后补正常门户两成员并发、真实账本计量、真实 NATS/桌面双向消息、切换积压及撤销验收。当前产物不包含页面和部署。

## 首期风险

共享池仅限互信团队。成员可能互相看到文件，并可能以 owner 能力操作。成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下。同一进程卡死、崩溃或取消超时会中断全池任务。需要保密或独立故障域请使用独立容器。

上述签名、模型计量及消息检查只约束自研调用路径，不修复原生 Cookie/browserPeer、/connect、原生文件读取或企业 skill 身份冒用。保留设计3.1已接受风险，不得解释为成员隔离证明。
