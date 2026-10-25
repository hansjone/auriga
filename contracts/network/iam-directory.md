# IAM N1 系统目录与服务凭证

`openapi/iam-network.yaml` 是 IAM 对外 API 的补充文档，入口为
`GET /api/iam/v1/network/systems?limit=50&cursor=...`。页面遵循
`network/system-directory.schema.json`；Java 内部投影与 collab RPC 类型由
`proto/auriga/iam/v1/network_directory.proto` 生成。

- `limit` 默认 50，允许 1–200；非法值返回 `common.validation_failed`。
- `nextCursor` 仅是绑定认证 tenant 的节点位置，不代表授权；每次翻页重查策略。
  每页最多检查 1000 个节点（另读 1 条前瞻），大量过滤时允许部分页或空页带游标；客户端必须继续到游标消失，不能按 items 为空结束。查询另设 2 秒语句超时。
- 只返回已注册、节点及设备未吊销、归属账号有效的 DSH。底层凭据绝对/空闲有效期任一到期超过 30 天，或底层会话缺失、吊销时隐藏；Resolve 同样统一为不存在。宽限期内仍可离线发现。
- `onlineState` / `statusObservedAt` 是兼容追加字段；当前 IAM 没有可靠连接观测，明确返回 `unknown` / `null`，不使用查询时间或凭据状态伪造在线观测。
- `displayName` 来自设备展示名，`ownerUserId` / `ownerDisplayName` 来自 IAM 账号。
  不输出设备 id、原始设备/账号 JSON 或任何会话 local 信息；名称不授予权限。
- 接受 portal/admin 用户和持有 `auriga.connect.collab.use` 的 device；均需有效的底层会话。
  device 必须已注册，来源取认证 did；用户按本人全部未吊销源节点的禁用策略保守过滤。
  组织网络关闭或对应来源→目标策略禁用时隐藏目标；不接收调用方自报来源或 tenant。

`NetworkDirectoryService.ResolveNetworkNode` 仅供内部 `svc:collab` 服务 JWT，
需既有内部可信传输及 `auriga-tenant-id` 委托业务租户，且安装端在 common gRPC
委托白名单中允许此主体。请求仅含十进制字符串 `realm` / `node`；响应 `exists`
和 `NETWORK_DIRECTORY_NODE_TYPE_DSH` 说明已注册 DSH。未知、外域、已吊销及组织关闭
统一返回不存在；本查询不授发送权，不查询非零 local。COLLABIAM 应在 `local=0` 时
先核节点，再返回 `system`；非零 local 的类型从 collab 自己的 opaque local 登记核实，
不得回退为猜测 `session`。

## Agent source authority（agent 来源权威）

正式内部入口是 [`network_source.proto`](../proto/auriga/iam/v1/network_source.proto)
的独立 `NetworkSourceAuthorityService`。本节是该契约的规范性语义；本次只交付契约和生成类型，
**尚未实施服务、来源链或发件接线**。缺实现/配置时维持 `UNIMPLEMENTED`/拒绝；旧
`NetworkGatewayService` 的允许调用者、旧五 RPC 的请求/响应及 SEND 外部接口均不变。

### 调用与权威边界

- 三 RPC 都只接受标准内部 JWT（`aud=auriga.internal`、`ptyp=internal_service`、
  `ctx=service`、`sub=svc:collab`）、受信 `auriga-tenant-id` 非平台业务租户和既有可信内部传输。
  `connection.tenant_id`、IAM 兑换行、来源行和委托 tenant 必须完全相同。外部用户、设备 token、
  模型、普通 SEND JSON 不能调用，也不能因知道 binding/op ID 获权；未列入的主体默认拒绝。
- `RegisterAgentSource`/`GetAgentSource` 前，collab 必须实际调用可信插件 native resolver，
  取得当前真实执行上下文，核当前节点健康、连接 epoch/boot 与正式 typed-local lookup；
  同库短事务 lock 确认 registration/type/revision，持久记录见证/操作后退出事务再调用 IAM。
  Get 供发送路径重建当前来源时同样要求此实时见证；不能只对两份 JSON 做相等比较。
  IAM 不回调 collab、不跨库读表；这是受信服务职责声明，不能声称 IAM 凭 JSON 独立证明宿主。
- IAM 精确核 `NetworkAuthorityConnection` 七字段对应的真实兑换记录，重查当前节点、设备、
  归属账号、底层 session、现行 collab 使用权限及组织网络状态，从本库推导 owner/device/backing-session。
  请求没有这些自报身份字段；`client_account` 只是既有兑换元组的一部分，不能单独当 owner。
  远程 credential/身份核验全部在事务外；随后本库短事务重核相关版本，不能用过期快照落库。
- IAM 只持久拥有最小 opaque 发件事实。collab 仍独占不可变 local type、宿主映射、线程、
  recipient、精确地址关系及其版本；IAM 不分配非零 local，不保存宿主 sessionId、父子树、正文、密钥。
  没有新增挑战、逐来源人工批准、登录或 HTTP API；同机隔离已整体后移，本契约不证明物理真人/宿主安全。

### 消息、字段和不可变元组

- `NetworkAgentSourceRegistration` 的八字段全部必填：`registration_id`、`allocation_op_id`、
  `allocation_fingerprint`、`registration_revision`、`realm`、`node`、`local`、`address_type`。
  前三项分别是原 local 登记编号/原分配操作/原不可变指纹；revision 是该 typed-local 登记见证版本，
  不是 node 总高水位、transport epoch 或来源行 revision。realm/node/local 是规范正 uint64 十进制串；
  proto revision 用正 uint64，投影现有 JSON 时转十进制字符串，不经浮点。type 只接受字面量 `session`。
  ID 遵循网络 `OpaqueId`（1–128 ASCII），摘要为 `sha256:` 加 64 位小写 hex；平台编号用 UUIDv7。
- `NetworkAgentSource` 回内部最小保存事实：`source_binding`、`original_connection`、`registration`、
  `scope`、`revision`、`state`、IAM 推导的 `owner_user_id`、`not_before`、`expires_at`。
  时间均 UTC 整秒 RFC3339，`not_before < expires_at`；device/backing-session 锚点只保存在 IAM，不回传。
  绑定 ID 由 IAM 生成；不可变元组为 tenant + 原精确兑换连接 + 原登记八字段 + owner/device/backing-session
  + 固定 principal_kind=agent + scope + 原时间窗。只有状态/revision 可单调变更，不能 UPDATE 重绑。
  同一 tenant/原精确连接/登记八字段/scope 的创建事实唯一；不同 op 不能重复建行或刷新该事实。
  后续合法认证连接若需新授权，必须重新满足全部真实登记前置、生成新 binding，不能改旧来源行或重绑 local；
  collab 已停发/撤销的 local 不得因换连接、换 op 自动申请替代来源绕过停发，旧授权到期/撤销始终有效。
  原 binding/local 登记及操作摘要保留永久防重绑语义，不按消息去重期清除后复用。
- 唯一非零 scope `NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND` 只覆盖普通 agent/session、
  已有关系中的 durable 单播：`kind=message` 或 `reply`（reply 仅 private）、`intent=information/request`。
  不授消息/线程/接收/读取/执行权；来源与目标具体业务许可仍逐次核验。human/human-intent、subagent 来源、
  system/group 来源、广播/组播、control、first_contact、ephemeral、未知 kind/intent/type/scope 都明确拒绝。
  未指定/未知枚举不按默认值放行，非零 local 不猜 session，body 内控制指令不升级为执行许可。

### 三 RPC 与当前状态

- `RegisterAgentSourceRequest(connection, registration, operation_id, scope, expected_revision)`：
  `expected_revision` 必须为 0，仅首次创建；成功 `RegisterAgentSourceResponse.source` 为 revision=1 的原回执。
  幂等键为 (tenant, RPC 方法, operation_id)，每个操作先持久保存规范请求摘要与完整原响应。
  同键同体返回原结果，异体 `common.conflict`；同体包含全部字段，不能忽略连接、type、revision 或 scope。
  并发相同 op 由唯一约束/版本锁收敛为同记录；不同 op 争同一创建事实仅一方创建，另一方冲突。
  重试须先识别已提交操作，再核新建 CAS，不能把丢回包后的原重试误当新建；仍须核调用者/tenant/原归属，
  身份已失效可拒绝，绝不把回执当当前 allow。原连接失效而无法重放时，用同设备当前连接 Get 原 binding 对账，
  不换 op 创建替代授权。
- `GetAgentSourceRequest(source_binding, connection, registration)`：核当前真实兑换源连接、同 tenant/realm/node、
  原 owner/device/仍有效的原 backing-session 及当前完整登记见证；跨 owner/device/node 或换 tuple/type/指纹不能借 ID 读。
  正常同设备、同底层 session 的 transport 重连可使用新的 ingress/server/client/account 兑换元组；
  `source.original_connection` 仍返回原值，响应 `connection` 精确回显本次核验元组。新登录替换 backing-session
  不自动继承原授权。IAM 返回的是 collab 已见证登记的原值，不能据此声称 IAM 已查询 collab 当前库。
- `GetAgentSourceResponse(source, connection, policy_revision, observed_at, valid_until)` 回当前状态及版本。
  `policy_revision` 是 IAM 当前业务 tenant 网络政策的正 uint64 单调快照版本；任何影响来源/目标节点策略或
  collab 使用权限的政策变更都推进它，不能用固定 0/常量伪造。它仅供绑定现有 authorization_ref，绝非目标 allow。
  `observed_at` 是 IAM 本次在线观察时间。只有当前 ACTIVE 且在时间窗内才回非空 `valid_until`；
  合法归属查询 REVOKED/EXPIRED 回原元组、当前状态/revision、空 `valid_until`，不得再当来源使用。
  调用者/身份/当前连接不合法、未知 ID、跨租户/跨归属统一拒绝且不回原事实；不能靠终态查询泄露他人身份。
- `RevokeAgentSourceRequest(source_binding, connection, expected_revision, operation_id)`：
  同可信调用者/tenant/原归属核验，正 revision CAS；成功回 `RevokeAgentSourceResponse.source` 终态原回执。
  这是单向停权操作：可用 IAM 留存的原精确兑换连接或核过同归属的当前连接；原连接/身份失效不授新权，
  也不阻碍 svc:collab 对本 tenant 原绑定停权。不能因过期删除身份锚点而允许换主人撤销/读取。
  ACTIVE→REVOKED 的提交递增 revision；过期来源仍终态，不能改回 ACTIVE。
  同键同体重放先返回原结果，不因 expected_revision 已旧而新建或续期；异体 conflict，其他 stale CAS 拒绝。
  新 op 对已终态来源且 revision 匹配可记录同一终态确认，不改变原时间/绑定、不再增加授权。
- IAM 在线时钟判到期，不依赖清理任务。首次观察到期在来源版本锁下终态化为 EXPIRED 并递增 revision；
  已 REVOKED 保持 REVOKED。终态吸收，不因旧回包、身份恢复、时间回拨或再次登记恢复为 ACTIVE。
  不可判定时钟/身份/状态时拒绝。mutation response 是原回执，可能早于后续撤销/到期；必须 Get 当前状态再本地 CAS，
  每次签前还须第五 RPC，绝不能从 Register 的 ACTIVE 原回执直接恢复停用来源。

结构/范围非法沿既有 `common.validation_failed`，异体/占用/CAS 冲突沿 `common.conflict`；
未知或不具读取资格统一 `common.forbidden`，依赖未接线/无法在线核权威用 `common.unavailable`，
认证失败沿现内部 gRPC 鉴权映射。均复用既有通用错误及 gRPC 转换，不新增错误码或回传敏感冲突详情。

### 期限、锁序和跨服务撤销

首次保存的 `expires_at` 由受信 IAM 策略上限、当前身份（含绝对/空闲 session、设备/账号）、原兑换连接及
当前登记来源许可期限取最小值；调用方不能传任意有效期。登记见证本身没有独立时间字段，collab 已到期的
local/宿主许可不得进入登记/使用路径；有额外本地期限时 collab 还须夹紧本地 Source 和最终签名期限。
源记录最大时间窗固定不变。Get 及每次第五 RPC 的决定期限再取“现在的 IAM 短授权策略期限、当前身份/连接期限、
原来源 expires_at”的最小值；签名另受 message.expires_at 与既有 300 秒封装上限夹紧。新观察可产生新的短决定，
但不得越过原 expires_at；原操作重放原封不动，不延长任何期限，不把保存的来源事实变成永久授权。

IAM 本库先按既有 session→device 锁序核身份，再锁 tenant 政策版本与来源行/版本（同类多行稳定排序）；
政策修改须与该政策版本锁串行化，不能只在另一快照读取一个 revision 后当提交屏障；
Revoke/Get/第五 RPC 共用来源版本锁/CAS 序列。远程核验前退出事务，回来后重读身份、政策与来源版本，
任何变动重试或拒绝。Revoke 先提交则后续 Get 不得回有效期限、第五 RPC 不得允许签发；授权已先提交并返回
后的网络窗口不构成跨服务零窗口保证，collab 仍须最终短事务重核和有界期限。不要持 DB 事务等待远程 IAM/credential。

collab 先在短事务持久 pending 见证/稳定 op/outbox→无锁 Register/Get→短事务 CAS 激活引用；
CAS 同时核原 pending 版本、当前健康/epoch、typed local 和宿主许可未变。撤销先本地持久停发及撤销 outbox→
无锁 Revoke→短事务确认终态；回包丢失重放原 op。撤销标记压过所有迟到激活回包；旧任务/重连不能重置停用版本。
本地停发可立即生效，**仅收到 IAM 已提交终态确认才可称全局撤销**；失联只报待确认并继续停发。

### 与原第五 RPC 的精确映射

`AuthorizeNetworkDeliveryRequest.connection` 仍是**目标**精确认证兑换连接，不改成 source；
`message_json` 和 `source_context_json` 仍是既有 Message/SourceContext 原始 UTF-8 字节。
映射只使用 Get 的当前有效事实，调用方必须检查完整响应、正版本/合法 state、connection 回显和期限：

- `source.registration.{realm,node,local,address_type}` → `Message.from.{realm,node,local,type}`，version=1；
  `reply_to=from`，`sender_kind=agent`；`source.source_binding/revision` →
  `Message.authorization_ref.source_binding/source_revision`（revision 规范十进制串）。
- `Get.policy_revision` → `authorization_ref.policy_revision`；`broadcast_grant_id=null`；
  human_intent_id/grant_revision/subject_type 不得用来扩权，首期不填。来源 scope 不产生 thread、目标或 reply 引用。
- `SourceContext.realm/node` 等于原注册源、`user=source.owner_user_id`、`sender_kind=agent`；
  `message_digest=SHA-256(JCS(完整 Message))`，包括未知可选字段；`observed_at=Get.observed_at`。
  `display_label` 由 collab 从受信展示事实生成并做既有长度/保留词检查，不来自模型；可选 identity_revision
  无真实 IAM 身份版本时省略，不能把 source/policy/epoch 冒充 identity_revision。Get.valid_until 只作为内部 Source 上限。
- IAM 第五 RPC 必须严格有界解析两 JSON（各不超过现有 64KiB，body 16KiB，深度16/对象128/数组256/
  总值8192/单串16KiB；另受当前有效组织上限夹紧），拒绝重复键、非法 Unicode/数值/尾随字节，
  核现有 schema/语义及 JCS digest。不能仅比对两份 JSON；必须用 authorization_ref 查真实 IAM 来源行并锁版本，
  重核原元组、source_revision、状态、期限、owner/device/backing-session、sender policy/scope、policy_revision。
  源连接从 IAM 已存原兑换锚点及同 node 当前真实兑换关联读取；同设备重连只可延续原有效 owner/device/backing-session，
  不能将目标 connection 或 Message 自报值当源连接，也不能覆盖原来源元组。
  source_context 的 user/realm/node/sender_kind 必须与权威事实一致；任何未知/缺失/过期都拒绝。
- 目标核验须读精确兑换记录与当前 owner/account/device/backing-session、现行 collab 使用/读取权限；
  `CheckNetwork(SEND/RECEIVE)` 只核组织/node 策略，不能替代它，也不能把 response.allowed 改成仅政策部分通过。
  IAM 不从 source_context 推导或猜出目标 local 权限。非零目标 typed-local、线程/recipient/关系版本由 collab
  在远程调用前取快照、返回后最终短事务 lock/CAS 重核，变动则重试或拒绝，再夹紧期限才可签名/落正文。
  三 RPC 不另加“已安全”布尔；Get ACTIVE 也不是允许某条消息或收件/执行的决定。

### 后续实现必需的真实 PG/鉴权验收（本次未执行）

后续最小 IAM 实现包括 tenant RLS 来源行/永久墓碑、操作唯一约束与原响应、来源版本锁/CAS、现行兑换/设备身份
及权限重核、可靠政策版本、短期限计算和三 RPC 受限装配；再接完整第五 RPC 与 collab durable outbox/最终重核。
现 common 委托配置是主体/租户白名单，新增 service 不能绕过；若安装/装配需调整由主控另派，本任务不改 common。
必须经真实服务入口和真实 PostgreSQL 提交/回读验证以下负向，不能抄一份算法或 mock 当实证：

- 同 binding/local ID 换 tuple/type/registration/fingerprint/owner/device/node/backing-session，跨 tenant/平台 tenant，
  用户/模型/非 collab JWT，空/未知枚举、非 session/非 durable 单播均拒绝且不泄露原字段。
- 同 op 并发同体只一行/一回执，异体 conflict；不同 op 争同一创建事实；原回包丢失重试不续期。
- Revoke 与 Get/第五 RPC 在真实来源版本锁争用：撤销先提交必拒绝；session/device/账号/权限失效或换绑
  与授权竞态，远程核验后旧快照不得过关；无锁远程调用和 session→device→source 锁序不死锁。
- 正常同设备同 backing-session 重连保持原映射；新 owner/device/node/新登录不得继承；到期不靠清理、恢复身份不复活。
- collab pending/outbox 崩溃恢复、迟到 Register/Get 回包与本地撤销 CAS、旧 epoch/typed-local/关系版本不复活；
  IAM 未确认不能宣称全局撤销；JSON 一致但真实行缺失/版本旧、目标精确连接或读取权失效必须拒签。

## 网关服务 JWT 文件出口

与 edge 相同，仅支持同机 POSIX 合并安装，默认关闭；Windows 文件系统拒绝启动。
新增 IAM 配置前缀 `auriga.iam.ai-gateway-token-export`，支持：

- `enabled=true`，并要求 `auriga.platform.combined=true` 和 IAM 启用。
- `installation-secret-file`：绝对、规范路径，文件为 43 字符安装秘密，无换行；
  上级目录仅安装属主可访问，文件仅属主可读写。安装秘密不得挂载给网关。
- `token-file`：绝对、规范路径，与 secret 不同；输出目录 0750，文件 0640，
  必须为 IAM 进程属主并由配置的 reader group 读取。与 edge 使用独立输出目录/组。
- 网关 `reader-group` 必填且非负；`installation-owner` 默认 -1（IAM 进程属主），
  可显式指定安装秘密属主 UID，语义与 edge 相同。

安装时在既有 `auriga.iam.internal-secret-hashes` 配置中提供 `ai-gateway` 的
SHA-256 摘要，摘要必须对应本次独立生成的安装秘密。固定签发
`sub=svc:ai-gateway`、`aud=auriga.internal`、`ptyp=internal_service`、`ctx=service`，
有效期 300 秒；启动后立即尝试，随后每 60 秒重新认证、原子替换输出文件。签发或文件校验失败
保留旧文件且不延长有效期；停止时删除文件。网关必须每次读取文件并按有效期拒绝旧 JWT。
输出 JWT 不能反过来用于签发其他服务身份；TokenService 启动时拒绝不同服务复用相同秘密摘要。
两出口同时启用时，启动前要求两个出口的文件目录互不重叠（含秘密目录、输出目录及其祖先关系），读取组不同；任一冲突时两个发布器均不得启动。

部署接线由部署任务完成：IAM 写入路径、网关只读挂载、reader group、安装秘密与摘要、
网关/ collab 委托租户白名单及 mTLS。此任务不修改部署文件、不操作现场部署。
