# 网络 IAM 内部权威

HTTPS `POST /api/iam/v1/network/register` 沿已审契约返回原身份五字段和布尔 `created`。
`NetworkAuthority.registerWithOutcome` 只由最终节点事务的实际 INSERT 计数构造结果，
节点、首次政策版本递增和审计同事务提交，提交失败不返回成功；号码预留仍是原独立事务。
同设备已有节点、重试或并发落败均返回 false 和稳定身份，不重放历史 true。
旧 `register` 继续只返回原 Node；不支持提交结果的替代实现由 HTTPS 明确返回 unavailable。
prepare/tickets 继续使用原五字段身份，不再次注册，也不含 created。
首次回包丢失或未保存后重试只能得到 false；刷新仍是同设备，真实重新登录才取得新 device/node，
旧节点吊销边界不变。created 不是授权票据，不提供来源/SEND/FIRST_CONTACT 或第五 RPC 放行。
本接线不证明生产来源 provider、首次初始化全链或真实双 DSH。

`IamGrpcNetworkAuthority` 由 `IamRuntimeConfiguration` 装配到现有内部 gRPC 服务，
使用正式生成的 `NetworkAuthorityService` 类型。每个方法要求平台租户的真实
`svc:collab` 服务 JWT，以及非平台业务租户委托；Resolve/Authorize 请求里的 tenant
必须与委托上下文完全一致。仍需部署方配置现有的租户委托白名单；非回环通信须 mTLS。
普通用户/设备 Bearer、浏览器 Cookie、自报用户均不能代替服务身份。

- `CheckNetwork`：复用当前组织策略、节点/设备/底层会话和已有授权规则，仅核能力上限。
  不创建 source_binding、人意图、关系或模型执行权限，不预留配额。
- `NetworkAuthorityActive`：逐次复用 IAM 当前节点、设备及 backing session 核验。
  它不是 Broker 在线状态，也不能替代 Resolve 的完整连接核验。
- `NetworkLimits`：返回所有 `NetworkAuthority.Limit` 的有效组织上限，名称为正式枚举名。
  无组织覆盖时使用现有平台上限；非法持久值、缺失投影、负值或越界投影失败关闭。
- `ResolveNetworkTarget`：仅支持 `local=0`，目标类型固定 `system`。
  精确校验委托 tenant、规范十进制 uint64 realm/node，以及持久兑换行的
  ingress/serverId/clientId/account；不把 opaque 字段转换成 UUID 或显示地址。
  同时校验已存 device/userNkey/audience、当前节点 owner、设备与底层会话归属。
  使用现有设备权威核验，并在返回前锁定重读兑换/设备/会话/账号快照；
  核验期间记录变化（包括有效期延长）要求重试，过期/撤销/缺记录统一不允许。
  期限取已存连接和当前 backing session 绝对/空闲期限的最短值，事务完成后再次检查。

版本来源沿现有不可重用的原生 device ID、Broker serverId 与兑换 ingress；
IAM 原生设备没有独立 credentialVersion 计数器，也没有为本 RPC 新增版本字段。
OAuth 每次授权新建 device，现有流程不重绑其 owner/session。
旧 ingress 不会被新 tuple 替代或从 node 重建；同节点多个仍有效的兑换按既有设计共存，
collab READY 负责其 epoch 围栏。本服务不冒充 collab 的最新连接/来源/会话登记权威。

`AuthorizeNetworkDelivery` 只支持已有关系的普通 agent/session 来源、durable 单播
message 或 private reply、information/request。严格有界解析两份原始 JSON，消费 schema
保留未知可选字段，对完整 Message 做 JCS/SHA-256；不授 human、subagent 来源、组播、
广播、首联、ephemeral 或 control 权限。两端独立核真实兑换、当前身份与 org 的
ACTIVE MEMBER/EXTERNAL 和 `auriga.connect.collab.use`，再锁定重读全部本库快照、政策版本、
许可链与消息命名的来源代。终态/停链/旧 revision/原归属变化统一拒绝；后继不延长旧引用。
目标 connection 精确回显，期限夹紧短政策、双方身份/兑换、原来源与消息期限并取整秒。

兑换时新增内部 sessionId 证据；第五 RPC 不为缺证据的历史 ingress 补造关联，须重新兑换。
源连接仅从原耐久锚点和同 owner/device/backing-session 的本库真实兑换选择，不取自目标
connection 或 Message。多身份按 session→device→account、同类 ID 排序取锁，然后政策/ingress、
chain/source；全部远程核验在事务外。组织接口仍无跨服务提交屏障，不能声称撤权零窗口。
非零目标 local、线程/recipient/关系权限及最终 CAS 由 collab 另行接线；本任务不证明完整交付、
执行准入或真实双 DSH 闭环。

所有 RPC 的错误仅返回固定公共错误码，不带底层错误原因、证据或绑定详情；
合法拒绝使用 `allowed=false` / `active=false`。每次调用在线重核，不缓存 allow，
不 touch idle、不刷新令牌/票，不调用 collab 或读取其他服务 schema。
原 `NetworkGatewayService` 的 Redeem/Active/BatchActive 仍仅允许 `svc:ai-gateway`。

## 来源事务内核与受限三 RPC

V9 和包内 `IamNetworkSources` 仅提供本库持久事实：许可链固定登记八字段和
owner/device/IAM backing-session 关联，保存 head/永久停权；来源代固定原连接、
时间窗与前代，仅状态/revision 可单向改变。每前代最多一个后继；只有未停权链的
当前 EXPIRED head 经完整新鲜核验和正 revision CAS 才可建新代。同健康连接、同登记
可以取得下一有界代，旧绑定、旧期限和旧操作响应保持不变。

操作按 tenant/method/operation 唯一保存完整 protobuf 请求、摘要和原响应。新 Register 使用契约显式
snake_case JSON 的 JCS/SHA-256 摘要（含默认值、uint64 十进制串）；旧不可变回执仍按原字节核对。
V11 允许拒绝操作不关联来源，改为永久关联独立核验并保存的原兑换/归属锚点；仍受租户外键、RLS 和不可变触发器约束。
原连接有效且全量当前核验通过后，错 revision、非 head、链已停分别在原 op 锁内持久保存
revision_mismatch、predecessor_not_head、lineage_stopped 拒绝回执；提交后才返回
definitively_uncommitted、原 operation_id、request_digest 和 rejection_reason，不含 source。
同 op 同体先返回原成功或拒绝回执，异体冲突；普通错误不是确定未提交证明。
回执先由操作找到原来源或拒绝操作的耐久兑换/归属锚点，再核本库原关联；不以原 ingress 在线、
当前 CAS、身份过期或当前来源状态阻止原回执对账。锚点固化真实 ingress 行主键、
精确兑换元组及 IAM 账号/设备/session 行 ID，V7 清理 ingress 不删除这些记录。
缺锚点或归属关联变化拒绝；回执无当前授权效力。Revoke 使用留存的原精确连接，
同事务停链并终态化命名代及当前 head；命名过期前代保持 EXPIRED。
Revoke 的 expected_revision 仅保留线兼容，缺失、零或任意旧值均不阻碍停权；新 op 对终态来源
也确认永久停链，同 op 仍要求原请求完全相同。来源版本锁、单调终态更新和整链原子性保持不变。

调用方拥有一个 `IamStore` 事务：先完成事务外远程核验，再以 tenant 的
`network-authority` advisory 为第一把业务锁，按 session→device→account→政策版本行→精确 ingress
锁序重核，构造同包、绑定该事务的 `VerifiedSnapshot`，内核随后取 operation→chain→source
锁。新建的固锚点、写代、推 head、写操作不可拆开提交；变更使用同事务保存点，
调用方即使捕获异常也不能提交半成品。回执/原连接撤销仅核归属关联，
不要求活跃身份；也必须在事务内调用。多来源锁按 UUID 稳定排序，竞争等待上限 5 秒。
Get 只返回 `View(source, stopped, observedAt)`，没有伪造的 policy_revision/valid_until。

`IamNetworkSourceAuthority` 与 `IamGrpcNetworkSources` 接线上述三 RPC。新建与所有 Get（包括终态）
先以短事务取得真实七字段兑换、node/owner/device/backing-session、完整身份/兑换/配置快照
及本库正政策版本，释放全部事务、同步与 DataSource 绑定，再复用真实 credential 检查，
经现有 `InstanceAiAuthorization`（同进程 org 或现有 gRPC 通道）核当前 ACTIVE MEMBER/EXTERNAL
及 `auriga.connect.collab.use`，最后按上述首锁协议重新比较全部本库材料及原身份期限。
材料或版本变化失败关闭；不使用 JWT perms、安装 PermissionResolver 或 resolveTarget 的返回期限冒充来源权威。
明确非成员/无权限 forbidden，未知枚举、缺依赖或超时 unavailable。组织接口没有 revision，
因此仍不能保证跨服务撤权零窗口。测试 org adapter 仅证明接口边界，不证明真实 org 联通。

只有已提交 Register 同 op 同体原回执和 Revoke 不依赖当前发行开关、org 可达性或已清理的原 ingress；
必须核耐久来源/兑换锚点及原 node/device/session/account 关联，缺失或变主失败关闭。
所有 Get 均要求本次真实有效兑换连接、原 owner/device、仍有效的原 backing-session、完整登记、
当前 org 权限及最终快照/政策版本重核；REVOKED/EXPIRED、链 stop 和 TTL 配置关闭都不能跳过。
原身份失效或当前连接失效时 Get 拒绝且不回原事实，不适用回执的离线恢复例外。
Get 允许同设备、同有效 backing-session 的真实新连接重连，始终保留原 connection；
终态经上述门禁后由原内核在 chain/source 锁下单向观察，保留命名代当前 state/revision，valid_until 为空。
所有 Get 均显式设置 optional lineage_stopped（包括 false），区分自然到期与停链前代，不改变命名代状态。
终态 Get 无授权效力，第五 RPC 仍独立锁链/来源并拒绝终态或停链。未知 op 不适用回执例外。

部署必须显式设置可信启动参数，例如：

```properties
auriga.iam.network.source-max-seconds=30
auriga.iam.network.source-decision-seconds=5
```

任一缺失或为 0 关闭新建及 Get 的在线有效决定；合法终态 Get 仍须完整活体门禁且仅返回空期限，
不为读取而伪造正的决定期限。原回执/单向停权保持上述离线恢复边界；不存在默认许可。
仅在 Environment 启动时解析为包内不可变策略，无热更新；改配置须重启。
负数、小数、非法或 long 溢出导致启动失败；decision 不得大于 300，两个值为正时 decision 不得超过 source。
大 source 值不会延长真实身份期限，也不会使 Instant 加法溢出成为许可。
首次 expires_at 取 source 上限、真实 credential/session/device 与连接期限的最短值；
Get valid_until 再取 now+decision、原 source 期限及当前身份/连接期限的最短值。
时间向下取 UTC 整秒，只有严格非空时间窗才能发行；原回执绝不改写/延期。
policy_revision 不观测进程安装参数的变更，不将重启说成跨服务版本屏障。

每方法 `@AuthenticatedOnly`；只接受平台认证租户的 svc:collab 服务主体及另行委托的非平台业务 tenant，
业务 tenant 必须与 connection 一致。Bean 显式依赖原 IamGrpcSessions transportGuard，沿现可信传输与 mTLS 限制。
validation/conflict/forbidden/unavailable 沿现通用错误精确保留，不回底层原因。
collab 当前 native/健康/epoch/typed-local 见证与最终 CAS 仍是调用者职责；三 RPC 不证明宿主或真实双 DSH。
第五 RPC、SEND、外部 API 和可见授权流程均未扩展，第五仍固定 unavailable。

## 永久连接兑换出处

V12 新增独立 `network_connection_anchor`：每笔成功兑换同事务写入 UUIDv7、真实 ingress
主键/key、完整七字段 C bytea、realm/node、锁内真实 owner/device/backing-session 与兑换时刻。
tenant+ID/ingress 主键/key 唯一，FORCE RLS，SECURITY INVOKER 触发器拒绝 UPDATE/DELETE；
不依赖短期 ingress/ticket 外键，无 TTL/GC，不存原票、token 或宿主 ID。写入失败兑换整体回滚。
这是每真实 connection 一行永久成本，尚未证明 5000 连接容量。

Resolve 仍核当前活体连接，在最终短事务精确核已有锚点后回 field6；旧 ingress 缺锚点不回填，
fields1–5 保持原行为。坏锚点或真实关联不匹配不返回有效引用。
内部 Read 仅 svc:collab+受信非平台 tenant 委托，原/当前引用都必须属于该 tenant。
逐项核完整 C/不可变出处及原/当前 tenant/realm/node/owner/device/session 相同，随后核本库
node→owner/device、device→account/session、session→account、account 存在，关联缺失/重绑拒绝。
历史 C1 可离线且已实际清理 ingress；expiry/logout/revoked 不当作归属变化，Read 也不证明 C2 活着。

锁序为 tenant advisory→稳定排序 session→device→account→node/realm；仅必要锁，无同值 UPDATE。
Read 不调用 credential/online/远程 org/Get/Register，不写 anchor/source/op/chain/失败围栏/许可观察；
返回原/当前完整引用，无 ACTIVE/valid_until。坏引用/缺记录/错归属为 forbidden，存储故障 unavailable，
不暴露 owner 详情、不从未命中推断 not_committed。普通 Fetch/Receipt/紧急 Revoke 与 #216 Read 未变。
契约详见 `contracts/network/iam-directory.md`；collab 保存引用、缺引用 UNKNOWN、恢复最终 CAS 另行接线。

## 本库政策版本底座（无来源出口）

V10 的 `iam.network_policy_revision` 以 tenant 为主键，正 `numeric(20,0)` 限于
1..18446744073709551615，沿用 FORCE RLS 与迁移角色/运行角色分离。
包内 `IamNetworkPolicyRevision` 只复用调用方当前同 tenant 事务，不装 Bean、不另开事务；
调用方必须先取得上述 tenant advisory，随后 lazy 初始化 1、锁行读取或递增。
该行锁保持到同事务提交。溢出以现有 `common.unavailable` 失败关闭，业务写入一起回滚。

configure/nodePolicy/grant/withdraw/revoke 成功事务推进版本；成功幂等写可保守递增。
register 只在最终事务实际插入新 node 时递增，重复返回不递增；号码预留及 realm 计数
仍沿原独立事务，不单独推进政策版本。prepare/redeem/cleanup 等连接事实不使用政策版本
替代真实身份、完整连接快照及期限核验。

账户事件在原 tenant 事务先取同 advisory，再无锁检查账号存在性；
permissions_changed 先真实失效会话 access token，disabled/offboarded 先真实撤销会话及设备，
之后才 account FOR UPDATE 重读并按原语义写状态，最后递增版本。enabled 仅锁账号写状态，
再递增版本；不存在账号仍无操作。会话投影、撤销事件、审计及版本沿原事务原子提交。

这个版本只表示 **IAM 已提交观察到的本库变化**；远端 org 事件未送达时，没有跨服务
全局强一致保证。后续真实来源 Authority 仍须事务外查询当前 org member/external 及
collab.use，回到本库按首锁协议比较版本和完整身份/连接快照；安装配置 PermissionResolver
或旧 JWT 权限都不能冒充实时 org 权威。来源三 RPC 按上节接线，第五 RPC 仍 unavailable。
