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
