# collab 网络邮箱接线

`NetworkMailboxService` 的内部 gRPC 入口由 common `GrpcServer` 自动收集，四个 RPC
只接受 `svc:ai-gateway` 内部服务 JWT、平台服务租户和已授权的业务租户委托。
`connection.tenant_id` 必须与 `auriga-tenant-id` 相同。每次 RPC 均远程核 IAM
持久保存的 realm/node/ingress/server/client/account 绑定及当前会话、设备、组织状态。
没有从 JSON、NATS header 或模型参数补身份的分支。

## 启动配置

独立 collab（`auriga.service.schema=collab`）必须配置：

```yaml
auriga:
  service:
    schema: collab
  collab:
    network:
      iam-target: "127.0.0.1:9091"
      service-token-file: "/run/secrets/collab-service.jwt"
      iam-timeout-ms: 5000
      key-state-file: "/var/lib/auriga/collab/key-publication.json"
      signing-keys:
        - realm: "2"
          purpose: protocol_response
          kid: mailbox-response-v1
          public-file: "/run/secrets/mailbox-response.spki"
          private-file: "/run/secrets/mailbox-response.pkcs8"
          not-before: "2026-01-01T00:00:00Z"
          not-after: "2027-01-01T00:00:00Z"
          state: ACTIVE
  grpc:
    server:
      enabled: true
      host: "127.0.0.1"
      port: 9092
      tenant-delegates: ["svc:ai-gateway"]
```

示例的端口、时间和 realm 都须按安装配置设置；`iam-target` 是 IAM 的内部端口。
跨主机连接必须同时配置 `auriga.collab.network.tls-cert-chain`、`tls-private-key`、
`tls-trust-cert-collection`，使用 common mTLS 通道。无 TLS 只接受数字回环地址。
服务端同样沿 common 配置内部 JWT issuer/JWKS 和 mTLS，不把服务方法列为 public。
`service-token-file` 是 **svc:collab** 自己的令牌，逐次读取以支持原子轮换；不能放网关令牌。
缺 IAM 目标、令牌、签名元数据或密钥文件时启动失败。IAM 暂不可达时服务可启动，
所有操作在有界 deadline 内失败关闭，不使用本地 allow 缓存。

签名私钥只从受保护的 DER PKCS#8 文件挂载，公钥是 DER SPKI；私钥不写 PG 或 key-state-file。
`signing-keys` 可追加各 realm 的 `business_delivery`、`management_state` 等既有用途，
相同 realm/purpose 的 ACTIVE 密钥不可重复。持久发布状态沿现有 `NetworkSigningKeys`。
旧 Team 通信的 `auriga.collab.control-plane.*` 配置独立，不能代替这些网络配置。

## 留给 IAM 工位的服务端实现

契约：`contracts/proto/auriga/iam/v1/network_authority.proto`，生成 Java/Go 类型。
新增 `NetworkAuthorityService`，不扩大旧 `NetworkGatewayService` 的调用白名单。
所有方法只接受 `svc:collab`、平台服务租户和有效的业务租户委托，RLS/审计沿现有 IAM。

- `CheckNetwork`：映射 `NetworkAuthority.check`；UNKNOWN sender/operation 拒绝，
  逐目标核授权，`allowed=false` 是拒绝，不能把检查当配额预留。
- `NetworkAuthorityActive`：映射 `NetworkAuthority.active`，重核 backing session/account/device/node。
- `NetworkLimits`：返回当前租户所有 `NetworkAuthority.Limit` 的有效组织上限，
  key 是 Java 枚举名，值是非负 int64；缺项、未知项、溢出一律视为协议失败。
  collab 会再按平台最大值取较小者，事务预留仍归 collab。
- `ResolveNetworkTarget`：核整个 `connection` 对应同一份成功兑换记录，且当前仍活跃；
  realm/node/tenant 不可跨记录拼接，ingress 不可只核“存在”。local=0 类型为 system，
  其他 local 从权威目录解析 session/subagent。允许时必须完整回显绑定和 local，
  `valid_until` 不超过当前原凭据和连接授权期限。
- `AuthorizeNetworkDelivery`：从已存信封与 source_context 验证来源绑定、版本、策略、
  广播授予与目标读取权；JSON 只作为待核证据，不能据其自报字段直接返回允许。
  允许响应完整回显目标绑定，并返回当前授权截止时间。collab 仍保有邮箱/关系/配额事务。

IAM 服务端没有在本变更实现；未实现 RPC 返回 UNIMPLEMENTED 时 collab 失败关闭。
所有 IAM 调用都沿自己的 svc:collab 客户端传播业务租户，且在 collab 事务外执行。

## 当前 wire 边界

READY 以 PG node 行锁原子绑定 ingress/nonce/incarnation/epoch；同 ingress 同请求重试不加 epoch，
新 ingress 接管后旧 ingress 不能再次接管。HEAD 和 FETCH 使用当前绑定签名，FETCH 完整转交核心
`fetchPage` 返回的签名页及 item。RECEIPT 逐项核目标、正文摘要、版本和状态，保留整份证明，
与核心游标提交同事务保存 operation_id 结果，内容变化的重试拒绝。

当前核心尚无 local 登记/高水位恢复和 checkpoint 证据对账能力。非零登记/证据恢复 READY
返回签名 `recovery_conflict` 并封锁业务，不把客户端数值冒充平台见证。
FETCH 的非空 continuation、held_ids、paused、state_barrier，以及 RECEIPT 的 retryable /
outcome_unknown 尚不被核心支持，入口明确拒绝而不忽略。旧 epoch 回执暂按 stale_epoch 拒绝，
跨 epoch 证据对账需要后续实现。这些边界不能宣称为完整 NATS/插件恢复链路已交付。

每个携帧 RPC 都在严格 JSON 解析后校验对应 type 与完整 schema；顶层或嵌套重复键直接拒绝。
RECEIPT 的 target 必须包含地址契约的 version=1，合法同 operation_id 异体才进入 conflict 判定。

V7__network_wire_association.sql 添加 node 的 wire 字段和两张 RLS 表，不改旧迁移。
V6 保留给 #147 的 network_audit_decisions；wire 迁移尚未发行，仅本分支改名。
已见 ingress 与回执证明当前长期保留，
后续清理必须保留 fencing/去重所需见证，不能简单按连接过期删除。
