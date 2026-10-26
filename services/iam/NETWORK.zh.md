# 网络 IAM 内部权威

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

`AuthorizeNetworkDelivery` **明确未装配且不可批准**：通过 caller/委托 tenant 检查后，
固定返回 `UNAVAILABLE`（`common.unavailable`），诊断参数为
`reason=network_delivery_authority_not_configured`。不解析 message/source JSON，
不以 JSON 一致或 Check(SEND) 代替真实 source_binding/revision、人意图和关系权威。
最终交付授权、SEND、非零 local 登记、执行准入与双 DSH 闭环仍需后续任务。

四个已实现 RPC 的错误仅返回固定公共错误码，不带底层错误原因、证据或绑定详情；
合法拒绝使用 `allowed=false` / `active=false`。每次调用在线重核，不缓存 allow，
不 touch idle、不刷新令牌/票，不调用 collab 或读取其他服务 schema。
原 `NetworkGatewayService` 的 Redeem/Active/BatchActive 仍仅允许 `svc:ai-gateway`。

## 来源事务内核（未接线）

V9 和包内 `IamNetworkSources` 仅提供本库持久事实：许可链固定登记八字段和
owner/device/IAM backing-session 关联，保存 head/永久停权；来源代固定原连接、
时间窗与前代，仅状态/revision 可单向改变。每前代最多一个后继；只有未停权链的
当前 EXPIRED head 经完整新鲜核验和正 revision CAS 才可建新代。同健康连接、同登记
可以取得下一有界代，旧绑定、旧期限和旧操作响应保持不变。

操作按 tenant/method/operation 唯一保存规范 protobuf 请求、SHA-256 摘要和完整原响应。
回执先由操作找到原来源及耐久兑换/归属锚点，再核本库原关联；不以原 ingress 在线、
当前 CAS、身份过期或当前来源状态阻止原回执对账。锚点固化真实 ingress 行主键、
精确兑换元组及 IAM 账号/设备/session 行 ID，V7 清理 ingress 不删除这些记录。
缺锚点或归属关联变化拒绝；回执无当前授权效力。Revoke 使用留存的原精确连接，
同事务停链并终态化命名代及当前 head；命名过期前代保持 EXPIRED。

调用方拥有一个 `IamStore` 事务：先完成事务外远程核验，再以 tenant 的
`network-authority` advisory 为第一把业务锁，按 session→device→account→政策版本行→精确 ingress
锁序重核，构造同包、绑定该事务的 `VerifiedSnapshot`，内核随后取 operation→chain→source
锁。新建的固锚点、写代、推 head、写操作不可拆开提交；变更使用同事务保存点，
调用方即使捕获异常也不能提交半成品。回执/原连接撤销仅核归属关联，
不要求活跃身份；也必须在事务内调用。多来源锁按 UUID 稳定排序，竞争等待上限 5 秒。
Get 只返回 `View(source, stopped, observedAt)`，没有伪造的 policy_revision/valid_until。

**尚无生产快照提供方、Bean、三 RPC 装配或 SEND 放行。** 当前 resolveTarget/credential
不能替代完整 collab.use、组织版本及政策提交屏障；下一任务必须补真实当前身份/权限核验、
锁下版本屏障、期限计算、服务 JWT/委托租户鉴权和 collab 最终 CAS，再接三 RPC/第五 RPC。
新增 PostgreSQL 用例验证内核事务/RLS，核验快照为显式测试夹具，不证明真实身份或宿主。

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
或旧 JWT 权限都不能冒充实时 org 权威。当前没有来源 Bean/三 RPC 出口，第五 RPC 仍 unavailable。
