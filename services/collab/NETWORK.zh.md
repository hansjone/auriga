# collab 网络邮箱接线

来源控制新增 `SourceRegister/SourceGet/SourceRevoke`，沿同一 `svc:ai-gateway` 委托边界。
V14 在 collab schema 保存完整来源操作、原 IAM 请求/回执、native 见证、停发 fence 和撤销 outbox；
后台轮询平台租户中的路由索引，再切入对应业务租户按原请求重试撤销，IAM 调用期间不持数据库事务。
回执只是历史事实，只有在线 Get 经本地最终 CAS 后才保存有界观察；未开放 SEND，签前第五 RPC 保持独立。

当前恢复限制：已保存回执可以重连后重新封装；pending 的原连接仍匹配时可凭新鲜见证原 op 重试。
尚无回执且原连接已换的无见证 reconcile 保持 `network.outcome_unknown`。
现有 Resolve 的拒绝同时包含可恢复的配置停用，无法证明旧连接永久失效；不能据此盲重放可能签发的 Register。
这一分支需要具备“绝不新建”语义的权威回执读取/原连接失效围栏后才能安全放开。
备份恢复或历史完整性不明时，须先持久设置节点 `source_operation_recovery_gap=true`；核实前不可清除。
未完成真 IAM 联调：需与 IAMSOURCEV2 合入后验证无版本整链撤销、Register 拒绝回执和 Get 字段 6 presence。

`NetworkMailboxService` 的内部 gRPC 入口由 common `GrpcServer` 自动收集，八个 RPC
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
- `ResolveNetworkTarget`：仅核 live connection 和 local=0/sys。非零 local 必须拒绝；
  collab 通过自身 NetworkLocalTargets 端口查询 opaque 登记的精确 realm/node/local/type/revision。
  独立 collab 已装配 local 包的 LocalTargets；缺失/旧 NULL/retired 登记统一拒绝，不跨库读 IAM、不推断 session。
  核整个 `connection` 对应同一份成功兑换记录，且当前仍活跃；
  realm/node/tenant 不可跨记录拼接，ingress 不可只核“存在”。local=0 类型为 system，
  允许时必须完整回显绑定和 local，target_type 恒为 system；
  `valid_until` 不超过当前原凭据和连接授权期限。
- `AuthorizeNetworkDelivery`：从已存信封与 source_context 验证来源绑定、版本、策略、
  广播授予与目标读取权；JSON 只作为待核证据，不能据其自报字段直接返回允许。
  允许响应完整回显目标绑定，并返回当前授权截止时间。collab 仍保有邮箱/关系/配额事务。

IAM 服务端没有在本变更实现；未实现 RPC 返回 UNIMPLEMENTED 时 collab 失败关闭。
所有 IAM 调用都沿自己的 svc:collab 客户端传播业务租户，且在 collab 事务外执行。
wire 不通过挂起持锁事务做网络调用：先用短只读事务取得 route/正文快照，再完成在线决定。
READY/HEAD/FETCH/RECEIPT 提交时重锁 node，比较原 binding/epoch/nonce/READY 指纹；旧决定不跨接管。
核心 preparePage/finishPage 把逐项核权与签名/游标审计提交拆开，保留 #147 的正文/dispatch 指纹复读。
决定仅在当前 Java 调用作用域有效，最长 30 秒，按服务实例/tenant/node/operation 及 epoch/incarnation/nonce/request_id 绑定，提交前再查期限。
收到外层数据库事务时失败关闭；需要原子回滚审计的内部调用应先 preparePage，再在自己的短事务 finishPage。
V13 仅为 network_local_registration 新增 nullable address_type CHECK（session/subagent），无默认值、不回填历史。
LocalRegistry 新登记强制类别并同原操作/指纹绑定；重试保留原查重顺序，换型冲突；READY 只恢复完整 typed 原操作。
ContactAssign 要求既有登记类别与普通 session 地址一致，旧 NULL/retired 不得取得创建许可。
LocalTargets.lookup 使用当前业务 tenant 的短事务，仅返回健康 node 下 active 且类型完整的登记。
lock 必须在同 collab datasource 的调用者事务内，先锁 node 再锁登记行，复读完整原见证并比对 type/revision；缺失、关闭、变更均拒绝。
内部只读 Witness 一次读取 registration_id/allocation_op_id/fingerprint/revision/realm/node/local/address_type，端口仍投影既有五字段。
这些操作不查宿主映射、不做锁内 RPC，也不导出登记或执行许可。旧 NULL 不补型，沿既有诊断/新 local 流程处理。

## 当前 wire 边界

READY 以 PG node 行锁原子绑定 ingress/nonce/incarnation/epoch；同 ingress 同请求重试不加 epoch，
接管前要求 features 与 adapter_capabilities 均支持协议三项必需能力，返回相同 required_features。
显式配置 auriga.collab.network.broadcast-enabled=true 时还要求双侧 broadcast_grants_v1。
新 ingress 接管后旧 ingress 不能再次接管。HEAD 和 FETCH 使用当前绑定签名，FETCH 完整转交核心
`fetchPage` 返回的签名页及 item。RECEIPT 逐项核目标、正文摘要、版本和状态，保留整份证明，
与核心游标提交同事务保存 operation_id 结果，内容变化的重试拒绝。
V7_1 对 (tenant,recipient,accepted_by) 保存不可变稳定接受证明：跨 operation 同证明重试不推进状态，异体冲突。
transport request_id/epoch/operation 与 expected_state_version 不属于稳定证明，原 operation 整帧异体仍拒绝。
按已存完整目标与类别核接受阶段；sys/广播仅 system，非零 local 仅 session；不允许任意 assigned_address。
CONTACT_ASSIGN/首联 session 阶段尚未接入，显式拒绝；首联 system 还必须有已允许的 contact 记录。

正式 READY 已装配 LocalRegistry 的登记恢复：严格读取原帧的 boot、计数和 typed pending，
使用已验证连接与委托 tenant，返回平台耐久 high-water/revision 和现恢复状态。
流程分为三个阶段：短事务接管 ingress/epoch 并保持 `reconciling`，事务外完成 IAM CONNECT
并独立提交登记恢复/隔离诊断，最后在同 datasource 的 node 锁内重核原关联、boot、计数版本和健康状态。
只有登记恢复和现有 receipt 条件都通过才提交 `ready`；最终签名/提交失败不回滚已提交的隔离诊断。
准备后崩溃保持业务关闭，同 ingress 同帧可重试；新 ingress 接管后旧准备/恢复结果不能复活。
已完成的同帧重放不再次按旧计数恢复，以免把其后合法登记误判为备份回滚；仍重核当前健康和计数。
登记恢复期间高水位/revision/健康变化会拒绝本次完成，不以旧快照开放业务；所有业务 RPC 最终仍核健康。
匹配 pending 只核原 typed 见证，READY 不返回登记回执、不授创建许可；未知 pending 不隐式登记。
原登记回执以原 LOCAL_REGISTER 操作重试取得；内部 gRPC/wire 已接线，生产 NATS/provider 接线尚未实现。
checkpoint 证据对账仍未实现：非零 checkpoint、非空 evidence 或不匹配的 cursor 独立返回
`recovery_conflict` 并封锁业务，不能由登记恢复成功覆盖。登记身份回滚返回 `identity_rollback`。
FETCH 的非空 continuation、held_ids、paused、state_barrier，以及 RECEIPT 的 retryable /
outcome_unknown 尚不被核心支持，入口明确拒绝而不忽略。旧 epoch 回执暂按 stale_epoch 拒绝，
跨 epoch 证据对账需要后续实现。这些边界不能宣称为完整 NATS/插件恢复链路已交付。

每个携帧 RPC 都在严格 JSON 解析后校验对应 type 与完整 schema；顶层或嵌套重复键直接拒绝。
RECEIPT 的 target 必须包含地址契约的 version=1，合法同 operation_id 异体才进入 conflict 判定。

内部 `LocalRegister` 沿相同 `svc:ai-gateway` 平台服务身份和受信非平台 tenant 委托，
保留原始 UTF-8 字节的 64KiB 上限、严格解析及正式 LocalFrames schema；不接受自报 owner。
先在线核 connection/currentTarget/IAM CONNECT，再由 LocalRegistry 独立事务锁 node 复核
当前 READY/ingress/epoch/boot 后登记，沿原操作先查、不可变类别、高水位和 revision CAS。
Registry 的 CONNECT 检查在借用 PG 连接之前完成，不挂起外层事务或持锁调用 IAM。
登记提交后再次锁外重核连接和 CONNECT，最后短事务在同 node 锁内复核原关联、nonce、boot、
健康与耐久登记事实，签完整 `RegisterResult`；签名受众只取当前关联，保留原决定的截止期限。
仅登记 session/subagent 粗类别，不授来源、SEND、首联、递归宿主关系或执行权限。
登记提交与最终签名/响应之间可崩溃：签名、最终提交或回包失败均不抹掉已提交登记和隔离诊断；
同原 op/fingerprint/type 重试恢复原 registration_id/计数。跨 READY 恢复仍须先通过现有登记和 receipt
恢复门槛，再以新 epoch/boot 重试原操作。最终事务提交完成前不向 RPC 调用者返回成功。
无法建立受众、鉴权或签名时沿既有 gRPC 错误映射拒绝，不返回未签成功。

V7__network_wire_association.sql 添加 node 的 wire 字段和两张 RLS 表，不改旧迁移。
V6 保留给 #147 的 network_audit_decisions；wire 迁移尚未发行，仅本分支改名。
已见 ingress 与回执证明当前长期保留，
后续清理必须保留 fencing/去重所需见证，不能简单按连接过期删除。
