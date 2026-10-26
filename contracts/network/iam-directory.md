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
的独立 `NetworkSourceAuthorityService`。本节是该契约的规范性语义；
历史回执只读入口由 IAM 实现，collab 恢复接线及原连接失效的确定失败围栏仍须分别验收。
缺实现/配置时维持 `UNIMPLEMENTED`/拒绝；旧
`NetworkGatewayService` 的允许调用者、旧五 RPC 的请求/响应及 SEND 外部接口均不变。

### 调用与权威边界

- 四 RPC 都只接受标准内部 JWT（`aud=auriga.internal`、`ptyp=internal_service`、
  `ctx=service`、`sub=svc:collab`）、受信 `auriga-tenant-id` 非平台业务租户和既有可信内部传输。
  `connection.tenant_id`、IAM 兑换行、来源行和委托 tenant 必须完全相同。外部用户、设备 token、
  模型、普通 SEND JSON 不能调用，也不能因知道 binding/op ID 获权；未列入的主体默认拒绝。
- `RegisterAgentSource` 新建许可代际/`GetAgentSource` 前，插件在真实受信执行上下文实际调用 native resolver，
  经 README §5.2 推送当次见证；collab 验证见证来源及有效期，核当前节点健康、连接 epoch/boot 与正式 typed-local lookup；
  同库短事务 lock 确认 registration/type/revision，持久记录见证/操作后退出事务再调用 IAM。
  新的 Get 同样要求当次插件见证；发件可按下述 60 秒/valid_until 双条件复用最近一次 source_get 观察，不能只对两份 JSON 做相等比较。
  IAM 不回调 collab、不跨库读表；这是受信服务职责声明，不能声称 IAM 凭 JSON 独立证明宿主。
  已提交 Register 的原响应受限读取只恢复操作结果，不是新建/当前来源使用；下文规定独立的 IAM 留存锚点核验，
  读取后仍须当前 native/健康/epoch/typed-local 见证、Get 与最终本地 CAS，不降低使用路径要求。
- `ReadAgentSourceReceipt(ReadAgentSourceReceiptRequest)` 是独立的无新建内部读取入口，
  wrapper 只含完整不可变 `RegisterAgentSourceRequest original_request`，复用
  `RegisterAgentSourceResponse` 返回完整原成功或已提交拒绝回执，不新增未知成功分支。
  collab 从耐久 pending 取原请求（仍为原 C），不得改成当前 C2、改 op 或自报 owner。
  本方法不要求 native 或当前 transport 有效；网关、用户、设备与模型均不可调用。
  IAM 在短事务复用 Register 的完整字节/摘要及真实原归属锚点核验，绝不进入
  online/authorize/register、新建来源/操作/墓碑/代际路径，也不改期限、状态或 revision。
  缺记录/锚点、不可判定或依赖故障沿既有 `UNAVAILABLE`/`FORBIDDEN` 失败关闭；
  同 op 异原体仍为 `CONFLICT`。查询未命中不证明 not_committed；查询与 Register
  竞态只能返回已提交原回执或未知错误，不能自行写拒绝围栏。
  历史 ACTIVE 仅是原回执，不能激活或授当前许可；本接口不解决未提交 pending 的全部收敛，
  `original_connection_invalid_uncommitted` 有写围栏留待整链接线。
- 新建许可或当前 Get 时，IAM 精确核 `NetworkAuthorityConnection` 七字段对应的真实兑换记录，重查当前节点、设备、
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
  `scope`、`revision`、`state`、IAM 推导的 `owner_user_id`、`not_before`、`expires_at`、`predecessor_binding`。
  时间均 UTC 整秒 RFC3339，`not_before < expires_at`；device/backing-session 锚点只保存在 IAM，不回传。
  绑定 ID 由 IAM 生成；不可变元组为 tenant + 原精确兑换连接 + 原登记八字段 + owner/device/backing-session
  + 固定 principal_kind=agent + scope + 原时间窗 + 前代引用。只有状态/revision 可单调变更，不能 UPDATE 重绑。
  许可链键为 (tenant, 原 registration_id, scope)，登记八字段和 IAM owner/device/backing-session 锚点固定；
  链独立于 transport，IAM 持久保存当前 head、显式停权标记及每代不可变前代引用（首代为空）。它不是宿主父子树。
  首代唯一、每个前代最多一个后继；唯一性/CAS 覆盖同代并发，**不永久禁止同健康连接/同登记的下一有界许可**。
  只有当前 head 自然 EXPIRED、链未停权，且全部当前真实权限/身份/登记检查重新通过，才可创建新 binding；
  不能修改旧 binding/时间窗/原 op 回执，也不要求断开仍健康连接、变更登记 revision 或重新分配 local。
  collab 已持久停发/撤销的 local 不得因换连接、换 op、自然到期或迟到回包自动申请/激活后继；
  IAM 显式 Revoke 的链停权同样禁止后继，旧授权到期/撤销始终有效。
  原 binding/local 登记及操作摘要保留永久防重绑语义，不按消息去重期清除后复用。
- 唯一非零 scope `NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND` 只覆盖普通 agent/session、
  已有关系中的 durable 单播：`kind=message` 或 `reply`（reply 仅 private）、`intent=information/request`。
  不授消息/线程/接收/读取/执行权；来源与目标具体业务许可仍逐次核验。human/human-intent、subagent 来源、
  system/group 来源、广播/组播、control、first_contact、ephemeral、未知 kind/intent/type/scope 都明确拒绝。
  未指定/未知枚举不按默认值放行，非零 local 不猜 session，body 内控制指令不升级为执行许可。

### 三 RPC 与当前状态

- `RegisterAgentSourceRequest(connection, registration, operation_id, scope, expected_revision, predecessor_binding)`：
  首代 predecessor 为空且 expected_revision=0，仅在该链从未建立时创建；
  后继 predecessor 必须是同链当前 EXPIRED head，expected_revision 等于该前代当前正 revision。
  IAM 锁同链 head/停权状态及前代版本，以 CAS 原子创建新 binding（新行 revision=1）、固定新时间窗并推进 head。
  同连接 C/同登记 R 完全可以创建下一代，换 op 仅表达新操作，不代替任何真实新鲜权限/许可检查。
  ACTIVE/REVOKED 前代、非 head、错版本、跨链/换元组/身份锚点或已停权均拒绝，不能从旧前代分叉。
  成功 `RegisterAgentSourceResponse.source` 始终是该代 revision=1 的原创建回执。
  幂等键为 (tenant, RPC 方法, operation_id)，每个操作先持久保存规范请求摘要与完整原响应。
  同键同体返回原结果，异体 `common.conflict`；同体包含全部字段，不能忽略连接、type、revision、scope 或前代。
  并发相同 op 由唯一约束/版本锁收敛为同记录；不同 op 争同一前代/CAS 仅一方创建，另一方冲突，不得自动改前代重试。
  所有分支先完成内部 JWT/委托 tenant 鉴权；随后**先识别已提交操作**，不能先要求原 transport 在线、
  先按当前状态/过期/CAS 判新建或猜测原 binding。
  已提交且原请求完全相同：核标准 svc:collab、受信业务 tenant，再按 IAM 留存的“操作→原来源行→原真实兑换证据
  →原 node/device/backing-session/account 归属锚点”核原归属，才能读取保存的完整原响应。
  这些锚点是 IAM 创建时从真实权威推导并耐久保存的内部 ID/关联与兑换出处，不是请求自报 owner、op 字符串或 JSON 相等。
  创建事务须固化已核验的原兑换元组/身份锚点快照并与操作/来源关联保存，留存覆盖回执对账生命周期；
  不依赖已过期 ingress/ticket 行仍在短期表中，更不能因 transport 清理而丢失对账依据。
  留存的原归属链必须可核且未换 owner/device/node/backing-session 关联；仅 transport/身份已过期、退出或吊销不阻止
  这个无授权效力的受限回执读取。跨 tenant/归属不符拒绝；缺锚点失败关闭，不能按请求补造。
  原请求 C 必须原封重放，不改成 C2；读出的 B/原期限/原 ACTIVE 回执也不代表现在有效，随后用 C2 Get(B) 并最终本地 CAS。
  未找到已提交操作时不能凭不存在推断未提交。IAM 核标准 svc:collab/tenant 及留存原兑换 C 的 node/device/account/backing-session 归属，
  在 (tenant, RegisterAgentSource, op) 键锁/唯一约束下与所有 Register 提交串行化：
  若无成功来源提交，且 C 已失效，或 C 仍有效但精确 CAS 不符、前代非 head、链已停，
  原子保存不可再提交的确定拒绝回执/失败墓碑及完整响应，才可回 `definitively_uncommitted=true`、
  原 `operation_id`、`request_digest=SHA-256(JCS(冻结的完整 IAM Register 请求))` 和 `rejection_reason`；source 缺失。
  rejection_reason 仅允许 original_connection_invalid_uncommitted/revision_mismatch/predecessor_not_head/lineage_stopped，
  分别对应上述四种情况。业务拒绝回执本身必须已提交；普通 conflict/forbidden 不包含这个证明。
  摘要 JSON 固定使用 proto 字段 snake_case、scope 的正式枚举名、expected_revision 十进制字符串及显式空 predecessor_binding，
  connection 七字段、registration 八字段全部输出，registration_revision 同样为十进制字符串；不得直接摘要默认会丢字段的 protojson。
  已成功提交先回原 source；在途未决、锁等待超时、原锚点缺失、普通 forbidden/unavailable 均不能给这个确认。
  C 仍有效的全新操作仍须全量实时权限/native/typed-local/新建 CAS；原失效 C 绝不新建来源。
  失败墓碑与原响应永久防复用；迟到同 op 同体回原失败、异体冲突，不允许随后提交或改 C。collab 验证并转存为 not_committed，
  插件才可另存新 op/新鲜见证重试；旧错误码或没有 source 的空响应不证明失败终态。插件不掌握原内部连接，
  对 not_committed.request_digest 只作不透明保存；复核原 op/body/digest 的职责在 collab。
- `GetAgentSourceRequest(source_binding, connection, registration)`：核当前真实兑换源连接、同 tenant/realm/node、
  原 owner/device/仍有效的原 backing-session 及当前完整登记见证；跨 owner/device/node 或换 tuple/type/指纹不能借 ID 读。
  还须核该代所属链与停权状态，不得因后继存在而把 EXPIRED 的旧代当成有效许可；链停权不能返回有效期限。
  正常同设备、同底层 session 的 transport 重连可使用新的 ingress/server/client/account 兑换元组；
  `source.original_connection` 仍返回原值，响应 `connection` 精确回显本次核验元组。新登录替换 backing-session
  不自动继承原授权。IAM 返回的是 collab 已见证登记的原值，不能据此声称 IAM 已查询 collab 当前库。
- `GetAgentSourceResponse(source, connection, policy_revision, observed_at, valid_until, lineage_stopped)` 回当前状态及版本。
  `policy_revision` 是 IAM 当前业务 tenant 网络政策的正 uint64 单调快照版本；任何影响来源/目标节点策略或
  collab 使用权限的政策变更都推进它，不能用固定 0/常量伪造。它仅供绑定现有 authorization_ref，绝非目标 allow。
  `observed_at` 是 IAM 本次在线观察时间。只有当前 ACTIVE 且在时间窗内才回非空 `valid_until`；
  合法归属查询 REVOKED/EXPIRED 回原元组、当前状态/revision、空 `valid_until`，不得再当来源使用。
  新增 optional bool lineage_stopped=6，必须显式返回 true 或 false；缺 presence 表示依赖未升级，不能推断未停链。
  这是链事实，不能改变命名代的状态/revision；用于区分自然 EXPIRED 与停链中的 EXPIRED 前代。
  collab 将 IAM 命名代 REVOKED 投影为 revoked；否则已停链/本地已持久停发为 stopped；否则 EXPIRED 为 expired。
  三种终态均正常回 source_get_result，保留当前命名代 revision，省略 JSON valid_until；历史原时间窗不作为许可。
  stopped 若来自本地 fence 只表示本地不可用，仍须 Revoke 已提交确认才能宣称全局撤销；不使用本地版本冒充 IAM revision。
  只有自然 expired 且链/本地未停、当前为 head，才允许用本次 revision 作后继 CAS；revoked/stopped 不得自动新代。
  调用者/身份/当前连接不合法、未知 ID、跨租户/跨归属统一拒绝且不回原事实；不能靠终态查询泄露他人身份。
- `RevokeAgentSourceRequest(source_binding, connection, expected_revision, operation_id)`：
  同可信调用者/tenant/原归属核验后，在许可链/来源行锁下原子停权；**不要求调用方精确 revision，也不要求 native/Get**。
  expected_revision=3 号字段仅保留线兼容且废弃，接收任意旧值/0 均不能据此拒绝停权；collab 新请求恒填 0 并冻结原体。
  这里按本次主控裁定覆盖此前“正 revision CAS/其他 stale CAS 拒绝”的前置语义；本库版本锁和单调更新仍保留。
  当前实现若仍按旧 CAS 拒绝，属于待接线实现差距，不能冒宿主关闭后撤销已可用。成功回终态原回执。
  这是单向停权操作：可用 IAM 留存的原精确兑换连接或核过同归属的当前连接；原连接/身份失效不授新权，
  也不阻碍 svc:collab 对本 tenant 原绑定停权。不能因过期删除身份锚点而允许换主人撤销/读取。
  ACTIVE→REVOKED 的提交递增 revision；过期来源仍终态，不能改回 ACTIVE。
  显式 Revoke 同时持久设置该链停权，并原子撤销当前 ACTIVE head（若有）；即使命名的是早已 EXPIRED 的前代，
  前代仍保持 EXPIRED，但其已存在后继不能继续有效、未来后继也被禁止。这些修改和原响应在同事务提交，
  不靠换 transport 或对已过期前代发新 op 洗掉显式撤销；没有隐式清停权/恢复接口。
  同键同体重放先返回原结果，不因 expected_revision 已旧而新建或续期；异体 conflict。
  新 op 对已终态来源也可记录终态/停链确认，不需要匹配版本，不改变原时间/绑定、不再增加授权。
- IAM 在线时钟判到期，不依赖清理任务。首次观察到期在来源版本锁下终态化为 EXPIRED 并递增 revision；
  已 REVOKED 保持 REVOKED。终态吸收，不因旧回包、身份恢复、时间回拨或再次登记恢复为 ACTIVE。
  不可判定时钟/身份/状态时拒绝。mutation response 是原回执，可能早于后续撤销/到期；必须 Get 当前状态再本地 CAS，
  每次签前还须第五 RPC，绝不能从 Register 的 ACTIVE 原回执直接恢复停用来源。

### 许可代际与回执状态/操作矩阵（规范，不是运行时实证）

- 无历史链 + 空前代/0 + 全量当前检查通过：首代 B；再以空前代另 op 创建，冲突。
- head=B/ACTIVE：Get 返回其当前有界观察；以 B 请求后继拒绝，原 op 仅受限读取原回执。
- head=B/EXPIRED/未停权：Get 返回原 B、终态 revision 和空期限；新 op + B/该 revision + 全量新鲜检查可建 B2。
  B2.predecessor_binding=B、revision=1，固定新期限；旧 B、旧期限与 op1 原结果不变。同代并发只有一个 B2。
- 非 head、REVOKED 或链显式停权：新 op/新连接均不能建后继；collab 本地停发也不得申请/激活。旧回包被本地版本 CAS 拒绝。
- 已提交 op1、首次回包丢失、原 C 已失效：仅凭已耐久 op1+原请求 C/R 重试 Register，经上述受限归属核验取同一原 B；
  C2 Get(B) 再核当前权威。若 B 已到期，先对账再按 EXPIRED head CAS 取得新代，不能跳过未知原操作结果。
- 同 op 异体（包括把 C 改 C2、改前代/CAS）、跨 tenant/原归属不符拒绝且不泄露原响应；未提交/未知 op 无回执读取例外。
- C 已失效或 C 有效但原 op 因 CAS/非 head/停链已被耐久确定拒绝：原键锁下保存失败围栏后回 definitively_uncommitted/rejection_reason，
  不生成 source，允许插件另 op 重新全量申请；不绕链停权。普通 RPC 失败不具此含义。
  collab 从未接受的 op 同样须原子保存失败围栏再回 not_accepted；任何未知结果不能按上述终态处理。

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
自然到期后经全量当前核验取得的新代，有独立的新 binding/not_before/expires_at；旧代始终 EXPIRED。
新代也取同样最小期限，前代只是并发协调引用，绝不是新的许可、无限续期或放松当前检查的凭据。

IAM 本库先按既有 session→device 锁序核身份，再锁 tenant 政策版本、许可链与来源行/版本（同类多行稳定排序）；
政策修改须与该政策版本锁串行化，不能只在另一快照读取一个 revision 后当提交屏障；
新代 Register/Revoke/Get/第五 RPC 共用许可链/来源版本锁/CAS 序列；显式撤销与创建后继按该序列串行，
撤销先提交则不能建后继；后继先提交则撤销必须一并停该 ACTIVE head。远程核验前退出事务，回来后重读身份、政策与来源版本，
任何变动重试或拒绝。Revoke 先提交则后续 Get 不得回有效期限、第五 RPC 不得允许签发；授权已先提交并返回
后的网络窗口不构成跨服务零窗口保证，collab 仍须最终短事务重核和有界期限。不要持 DB 事务等待远程 IAM/credential。

collab 先在短事务持久 pending 见证/稳定 op/outbox→无锁 Register/Get→短事务 CAS 激活引用；
首次未知结果先用原 op/原体取回执；自然到期新代须另存新 op/前代绑定/预期 revision，不改尚未对账的旧操作。
CAS 同时核原 pending/停发版本、当前健康/epoch、typed local 和宿主许可未变。撤销先本地持久停发及撤销 outbox→
无锁 Revoke→短事务确认终态；回包丢失重放原 op。撤销标记压过所有迟到激活回包；旧任务/重连不能重置停用版本。
新代激活还核回包 predecessor_binding 等于耐久 pending 的前代、binding 是本操作原响应里的新 ID，不能把其他代的回包移用。
本地停发可立即生效，**仅收到 IAM 已提交终态确认才可称全局撤销**；失联只报待确认并继续停发。

### native 控制搬运与本节义务的对应

远程 collab 取得真实插件见证的唯一新增 wire 是
[`README §5.2`](README.md#52-来源-native-见证搬运契约已定义实现待接线) 与
[`source-control.schema.json`](source-control.schema.json)，沿已有 LOCAL_REGISTER mailbox 帧通道。
它是对本节 native 前置的搬运细化，不给 IAM 增加 RPC、native JSON、用户入口或服务调用者。
以下对应均为强制条件：

- 本节“实际 native resolver”：插件在真实受信执行上下文调用；wire `native_witness` 五字段为
  witness_id、opaque binding_id、耐久 binding_revision、observed_at、valid_until；当前帧 epoch/boot、
  registration 和认证 ingress 共同绑定该观察，最多 30 秒，细则见 §5.2。collab 只见受信插件声明，
  仍不证明 IAM 独立见证宿主；不向 IAM 转发原生 ID/cwd/对象，不把 native 摘要当认证材料。
- 本节“八字段 lookup/lock”：wire registration 六字段（原 ID/op/fingerprint/revision/local/type）
  与 ingress 权威 realm/node 合成，逐项比对 collab 的完整持久见证；IAM 请求只用实际库中八字段。
  native binding_revision、来源 revision、node registration_revision 与 typed-local 原版本含义各自独立。
- 本节“先查原 op”：source_register.operation_id 对应 IAM Register.operation_id；首次 issue 冻结
  当前精确 connection 与库中登记、scope、前代/CAS 成为完整 IAM 原体。重连 mode=reconcile 时仅从
  collab pending 取旧体，不能把新 envelope 的 connection 改进旧体；null native_witness 只允许这个无授权回执读取。
  标准 mailbox 当前鉴权和持久原操作归属核验仍在；IAM 留存锚点规则完全不变。
  无已存 pending/原体不能凭 op 补建；在主库共用 op 键锁下无记录/无 outbox 且 source_operation_recovery_gap=false，
  才能先保存未受理墓碑再回 not_accepted；缺口/持久化不确定则 outcome_unknown。
  pending 已持久化后的处理/RPC 失败一律未知，只有 IAM 原 op 已提交的成功/确定拒绝业务回执才能收敛；详见 README 分界表。
- 本节“Get 当前来源”：source_get 必须重新真实 native 核验，以当前连接调用 IAM，严格校 connection 回显；
  binding_id/binding_revision 必须等于 collab 永久保存的签发 native_binding，后继 issue 也不能换；差异 conflict、先撤销。
  source_get_result 的 witness_id 与有效期绑定这一次观察，期限再夹紧 native/本地许可；只回带有效时间的 active。
  IAM 终态回内部原事实/空期限及显式停链事实；collab 正常回 expired/revoked/stopped 和当前 revision，不带有效期。
  正常来源查询仅对不存在、跨租户/身份不符拒绝，不因终态拒绝；通用协议/见证与依赖校验不变。active 观察期限失效仍为 network.expired。
  source_register_result 的 active/revision=1 仍仅历史原创建回执；不能因为外层新签名而变成当前 Get。
- 本节“pending/outbox 与最终 CAS”：collab 持久完整 IAM 原体/摘要、见证、连接/boot/epoch、typed-local、
  停发版本、许可 fence、前代与预期版本；事务外 IAM，最终短事务 lock/CAS 重核所有这些事实。
  超过见证期限、断线/换代、typed-local/许可改变、停发或旧代回包均不得激活。重新观察不用改旧 mutation 原体。
- 本节“停发与撤销”：source_revoke 不要求仍能打开宿主或掌握精确 revision，先本地/服务端耐久停发并保存原 op/原请求 outbox；
  当前 mailbox 不可用时既有服务 outbox 用原归属锚点继续停权，不能要求旧 transport 在线。
  source_revoke_result 的 lineage_stopped=true 仅在 IAM 原终态回执已提交后发出；命名 expired 前代也停 active 后继。
  未确认继续停发，只能重放原 Revoke op/原体；不知道结果不得换 op 越过原操作。
  撤销使用独立保留名额，不能排在 Register/Get 后；每个请求 30 秒超时释放名额，但不丢失 pending/outbox。
- 本节“第五 RPC”：所有 source wire 成功都没有 allowed、消息/线程/目标权、人工审批或 SEND 许可。
  来源映射与签前第五 RPC 保持下节定义，目标 connection 语义不变；后续最终业务版本重核不能省略。

mailbox 出口仅映射现有 network catalog，IAM 保持上文 common 错误语义；对应关系见 README §5.2。
响应给插件只投影 source 的 opaque 事实，不输出 IAM owner/原兑换连接；collab 内部保存的完整 IAM 原响应不得因此裁剪。

### 与原第五 RPC 的精确映射

`AuthorizeNetworkDeliveryRequest.connection` 仍是**目标**精确认证兑换连接，不改成 source；
`message_json` 和 `source_context_json` 仍是既有 Message/SourceContext 原始 UTF-8 字节。
映射只使用最近一次 source_get 的有效事实，调用方必须检查完整响应、正版本/active state、connection 回显和期限；
允许复用仅当 observed_at <= now < valid_until 且 now-observed_at <=60 秒，同时重核健康/epoch/boot、typed-local、
签发 native_binding、停发/许可版本。不得刷新缓存时间；不满足时先由插件重新推送 source_get，再调用 IAM Get/最终 CAS。
每次签前第五 RPC 仍独立在线核 IAM，不把缓存观察当作 allow：

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
  必须核所属许可链未停权；来源仍按本消息引用的那一代核，绝不能借后继的有效状态延长旧引用。
  源连接从 IAM 已存原兑换锚点及同 node 当前真实兑换关联读取；同设备重连只可延续原有效 owner/device/backing-session，
  不能将目标 connection 或 Message 自报值当源连接，也不能覆盖原来源元组。
  source_context 的 user/realm/node/sender_kind 必须与权威事实一致；任何未知/缺失/过期都拒绝。
- 目标核验须读精确兑换记录与当前 owner/account/device/backing-session、现行 collab 使用/读取权限；
  `CheckNetwork(SEND/RECEIVE)` 只核组织/node 策略，不能替代它，也不能把 response.allowed 改成仅政策部分通过。
  IAM 不从 source_context 推导或猜出目标 local 权限。非零目标 typed-local、线程/recipient/关系版本由 collab
  在远程调用前取快照、返回后最终短事务 lock/CAS 重核，变动则重试或拒绝，再夹紧期限才可签名/落正文。
  三 RPC 不另加“已安全”布尔；Get ACTIVE 也不是允许某条消息或收件/执行的决定。

### 后续实现必需的真实 PG/鉴权验收（本次未执行）

后续最小 IAM 实现包括 tenant RLS 来源行/永久墓碑、许可链 head/停权与每前代唯一后继、操作唯一约束与原响应、来源版本锁/CAS、现行兑换/设备身份
及权限重核、可靠政策版本、短期限计算和三 RPC 受限装配；再接完整第五 RPC 与 collab durable outbox/最终重核。
现 common 委托配置是主体/租户白名单，新增 service 不能绕过；若安装/装配需调整由主控另派，本任务不改 common。
必须经真实服务入口和真实 PostgreSQL 提交/回读验证以下负向，不能抄一份算法或 mock 当实证：

- 同 binding/local ID 换 tuple/type/registration/fingerprint/owner/device/node/backing-session，跨 tenant/平台 tenant，
  用户/模型/非 collab JWT，空/未知枚举、非 session/非 durable 单播均拒绝且不泄露原字段。
- 同 op 并发同体只一行/一回执，异体 conflict；不同 op 争同一前代/CAS 仅一后继。健康 C/原 R 不变，B 提前自然到期，
  全量当前权限仍有效时可建 B2；旧 B/原 op/原期限不变，不强制断线、不改登记。不新鲜/停发/显式撤销则禁止新代。
- 首次已提交 Register 回包丢失且原 C 失效，仅以原 op/原请求恢复同一 B/原期限，再 C2 Get/本地 CAS；
  跨 tenant/原归属不符/异体拒绝；未提交/未知 op 不能绕过完整当前身份检查，回执无当前授权效力。
- Revoke 与 Get/第五 RPC 在真实来源版本锁争用：撤销先提交必拒绝；session/device/账号/权限失效或换绑
  与授权竞态，远程核验后旧快照不得过关；无锁远程调用和 session→device→source 锁序不死锁。
- 正常同设备同 backing-session 重连保持原映射；新 owner/device/node/新登录不得继承；到期不靠清理、恢复身份不复活旧代。
- 新代 Register 与命名过期前代的 Revoke 竞态：先撤销则不建后继；先建后继则撤销同事务停该 head，链永久停权；
  同连接/新连接/新 op 不能绕过；collab 已持久停发与迟到原代/新代回包竞态不能激活。
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
