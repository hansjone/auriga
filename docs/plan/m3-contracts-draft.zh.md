# M3 契约与第一批实施拆分草案

> **2026-10-22 C3落实标记**：下文仍保留C2历史提议的原文；本轮在独立审查前提交正式契约供评审，
> 对应`contracts/openapi/{ai,resource}.yaml`、IAM/workspace/AI/resource protobuf、ctx=instance、
> `contracts/catalog/m3.json`、资源完整签名/撤销/降级与共享JCS向量、Java/Go/TS生成；边界见ADR0021。
> §1.2的AI/ait_、面板同源会话及styles冲突已在新ADR和插件协议消除。
> D3的0=禁止/UTC/并发与unknown已写正式契约；D4的24小时离线停用已定。
> HB1四个必填nullable心跳计数已由#41合并，本轮保留，不重复实现。
> 这只是契约落地，不代表I3a/G1/R1/WR1/AR1/E3/Z1的业务、真实DSH模型联调或任何现场部署已完成。
> 通用流程须支持域名和内网纯IP:port；NAS为真实验收场景，不是硬编码产品目标。

> 状态：**评审基线，未生效，未开工实现**。D1–D7 已由用户于 2026-10-20 按推荐决定（见第 7 节标注），正式契约由 C3 落地。日期：2026-10-20。
> 核对基线：本 worktree `2e19dbf840a95cf5393a1f22ee12176b4c3cc6c9`。正式接口仍以 `contracts/` 为唯一来源。
> 第二轮：在已提交的首轮草案 `9069ebf` 上按独立审查修订，仍是未生效提议；不重做首轮盘点，不落地任何契约或实现。
> 本文只建议下一轮契约与任务边界；没有修改契约、ADR、代码、依赖、CI 或部署。下文标为“提议”的路径、字段、权限、错误码、事件、TaskId 均不是现行契约。

## 1. 范围、依据与现有缺口

优先为 G1 模型网关、R1 资源中心建立可以审查的接口边界，再拆实现。G2 MCP、N1 知识、X1 出网、F1 文件、P2 团队面板只列依赖与契约入口。依据 [M3 开工顺序](m3-start.zh.md)，M2 的 W2V、W3 验收仍要单独完成；本草案不替它们验收，也不把骨架当成 M3 功能。

### 1.1 已有定义：可复用什么，不能据此推断什么

1. **契约与兼容规则已定**：[contracts/README.md](../../contracts/README.md) 规定 REST `/api/<服务>/v1`、内部 protobuf、事件 `dataversion`、插件 `Auriga-Protocol: 1`；UUIDv7、UTC 时间、lowerCamelCase JSON、游标分页；创建 POST 的 `Idempotency-Key` 保留 24 小时；管理资源 ETag / If-Match。它尚未规定幂等键的作用域、请求指纹或流式重放，不能直接推断模型请求可以自动重试。版本规则见 [ADR 0015](../adr/0015-contracts-first.md)。
2. **令牌声明已定**：[access-token.schema.json](../../contracts/auth/access-token.schema.json) 的必需字段是 `iss/sub/aud/exp/iat/jti/tid/ptyp/ctx`；`ptyp` 只有 `user/service_account/device/internal_service`。平台业务 audience 为 `auriga`，AI 为 `auriga.ai-gateway`，内部服务为独立的 `auriga.internal`。已有 `sid/did/perms/roles/scope`，没有托管 AI 令牌的 `instanceId`、签发接口或实例生命周期绑定规则。
3. **信任边界已定**：[ADR 0014](../adr/0014-communication-and-tokens.md) 规定各类通信与凭据的总体分工；`ait_…` 只准调用 `/api/workspace/v1/instance/**` 的精确范围来自 [common.yaml 的 instanceToken](../../contracts/openapi/common.yaml) 与 [spec.proto 的 TokenRef](../../contracts/proto/auriga/node/v1/spec.proto)。[contracts/README.md 鉴权表](../../contracts/README.md) 规定内部 gRPC 的 `sub=svc:<服务名>`、`ptyp=internal_service`、`ctx=service`、平台租户及白名单代租户；[AurigaServerInterceptor](../../services/common/src/main/java/auriga/common/grpc/AurigaServerInterceptor.java) 实施调用方与代租户检查。这些细则不归给 ADR 0014。外部服务账号不是内部服务。浏览器经 edge 换 JWT，业务服务不认 cookie。第三方 OIDC audience 不能用于平台。
4. **连接模式已有入口**：[plugin/README.md §4](../../contracts/plugin/README.md) 规定设备授权、轮换刷新令牌、`ptyp=device/ctx=device/did`；用 RFC 8707 `resource` 换 AI audience。`/api/workspace/v1/connect/manifest` 的 [ConnectManifest](../../contracts/openapi/workspace.yaml) 已有 `mcpServers.*.url`、`modelProviders.*.{baseUrl,models}`、`models[].{id,displayName,contextWindow}`、`panel.{manifestUrl,keys}`。这是接入清单，不是供应商、模型授权和预算的管理接口；URL 必须与平台 `apiBase` 同源。
5. **实例配置已有容器**：[spec.proto](../../contracts/proto/auriga/node/v1/spec.proto) 的 `InstanceSpec` 已有 `instance_id/tenant_id/owner_id/generation/plugins/profile/secrets`；`Profile.model_providers` 含 `base_url/token_ref/models`，`Profile.mcp_servers` 含 `url/token_ref`；`TokenRef` 已分 `INSTANCE_TOKEN` 与 `AI_GATEWAY_TOKEN`；`Secrets.ai_gateway_token` 用 `SealedSecret` 封装。HPKE 的 `info` 绑定实例和机密名，`aad` 绑定规格代数。这解决下发格式，未解决谁签发 AI 令牌、如何续期和吊销。
6. **模型配额只有限额**：[org.proto](../../contracts/proto/auriga/org/v1/org.proto) 的 `QuotaService.GetEffectiveQuota(tenant_id,account_id)` 返回 `ModelQuota.monthly_budget_micros/currency/daily_tokens`，未设 optional 数值表示不限；[org.yaml](../../contracts/openapi/org.yaml) 的 `Quota.model` 用 `monthlyBudgetMicros/currency/dailyTokens`，覆盖层“省略”与 `null` 不同，`null` 表示不限。已有 `AuthorizationService.GetEffectivePermissions/CheckPermission`，后者带 `permission/resource`。没有模型目录、按模型授权、价格、预算预占/结算接口，也没有配额版本或变更事件。
7. **策略与权限已有底座**：[org.yaml](../../contracts/openapi/org.yaml) 的 `PolicyKey` 仅有 `workspace/egress/login`；`WorkspacePolicy.allowPersonalPlugins` 默认 true；`EgressPolicy.categories/allowDomains/denyDomains` 明确解析到内网一律拒绝。已有 `/admin/permissions`、角色绑定、授予权限不能超过自身权限。它们未定义 G1/R1 专用权限，也没有 `resource` 或 `ai` 策略键；不能往任意 JSON 中塞新策略冒充契约。
8. **制品消费格式已定**：[spec.proto](../../contracts/proto/auriga/node/v1/spec.proto) 的 `PluginRef.name/version/level/digest/url/enabled/signed` 与 `PluginLevel.L0–L4` 可复用；`SignedArtifact.statement/signature/signing_kid` 的声明含 `purpose/name/version/os/arch/digest/issued_at`，签名是 JCS JSON + Ed25519。只签 digest 的旧字段已废弃。`ArtifactStatement.purpose` 注释目前只列 `agent/dsh_runtime/plugin`，没有 skill 或环境模板的引用。缺少发行方公钥分发、精确 JCS 字段映射及测试向量、租户绑定、撤销和显式降级授权。
9. **事件与审计已定**：[events/README.md](../../contracts/events/README.md) 使用事务 outbox、至少一次投递、按事件 id 去重，同一 `subject` 有序、跨资源无序；[envelope.schema.json](../../contracts/events/envelope.schema.json) 含 `id/source/type/time/subject/tenantid/dataversion/traceparent/data`。已有 `user.disabled/offboarded/deleted/permissions_changed`、`identity.session_revoked`、`instance.deleted` 等；`resource.* / knowledge.* / usage.*` 仅列为以后增加。审计已有 [AuditRecorded](../../contracts/events/audit.schema.json) 的 `actor/action/target/result/errorCode/changes/details/requestId`，不必另建审计信封。
10. **执行边界已定**：[平台架构 §3–6](../architecture/platform.zh.md)、[节点代理 §4、6、7](../architecture/node-agent.zh.md)、[ADR 0004](../adr/0004-platform-controls-dsh.md) 规定 resource 管资源，workspace 决定实例规格，agent 下载校验、安装和渲染；插件只做门锁、进程内上下文与面板外壳。资源同步、模型配置、文件读写不能搬回插件。`generation=observed_generation` 且 `ready=true` 才算落地，不以“下发成功”代替实际完成。
11. **当前实现只是部分底座**：[ai-gateway/main.go](../../infra/ai-gateway/main.go) 是版本/日志骨架，[ResourceModuleConfiguration](../../services/resource/impl/src/main/java/auriga/resource/ResourceModuleConfiguration.java) 是空装配点。agent 的 [artifacts.Store.Ensure](../../infra/agent/internal/artifacts/store.go) 接收 holder、URL、digest 和大小限制；此接口本身不消费 `SignedArtifact`，不能因已有摘要下载器就宣称发行方验签链闭合。后续必须用签名负向测试证明完整消费链。

数据与实现共同遵循 [开发规范](../dev/conventions.md)、[common 说明](../dev/common.md)、[ADR 0003](../adr/0003-modular-deployment.md)、[0007](../adr/0007-postgresql.md)、[0008](../adr/0008-nats.md)：服务只用自己的 schema，业务表带 `tenant_id`、UUIDv7、RLS，金额用整数；Java 只依赖其他服务 api，Go 不能借 Java common 的存在声称自己已有相同保障。Java 包名按 [ADR 0011](../adr/0011-java-package.md) 使用 `auriga`。

### 1.2 正式评审前必须显式消除的歧义

- **托管 AI 凭据冲突**：[ADR 0016 第 3 条](../adr/0016-mcp-session-context.md) 称托管 MCP 使用“实例令牌”，节点设计 §4 示例也用 `instanceToken`；正式 `TokenRef`、插件协议与访问令牌 audience 已将 AI 和 workspace 的 `ait_` 分离。不过 common.yaml 的 securitySchemes 只有 bearerAuth/sessionCookie/instanceToken，尚无独立 AI 方案，建议 C3 新增明确 AI audience/ctx 限制的 securityScheme。`ait_` 不扩权，必须补齐托管签发/轮换契约，不能靠解释旧注释实现。
- **面板与独立源冲突**：[plugin/README.md §5](../../contracts/plugin/README.md) 仍说托管面板“同源会话”，而 [ADR 0020](../adr/0020-instance-origin-isolation.md) 禁止实例来源获得平台 cookie 或被平台 CORS 放行。实例源上的 `/panel/` 静态文件可用，不代表 `/api/*` 自动具备成员身份。P2 的数据请求桥接是单独决策，不能把门户 cookie 或通用 JWT 注入实例页面。
- **面板 schema/文字不一致**：插件协议文字提及 `styles`，但 [panel-manifest.schema.json](../../contracts/plugin/panel-manifest.schema.json) 没有该属性且生产方 `additionalProperties=false`。第一批建议 CSS 自包含在模块中；若要外置样式先由主控补契约。
- **资源签名不等于 ACL**：当前声明没有 `tenantId/resourceId/visibility`；同摘要缓存不授予跨租户下载或安装权。“大家可见”应解释为当前租户全体，而非所有租户或匿名公网。
- **历史业务仅为参考**：旧设计中的插件心跳同步、旧 Hub 路径、个人访问令牌和任意回滚不移植。按现行 workspace→agent 边界、设备协议和反回滚规则重写；等级细则及可见范围的完整 API 仍是下文提议。

## 2. 第一批共享契约提议

本节及第 3–6 节的新增名称全部为**提议**，需主控登记到正式契约后才可编码。不得在实现中硬编码本文候选名称形成第二个定义来源。

### 2.1 主体、租户、audience 与凭据生命周期

- **G1 数据面**：只接受 iam 签发、`typ=at+jwt/alg=ES256`、issuer 和时间有效、audience 专用于 `auriga.ai-gateway` 的令牌。提议 ctx 白名单仅为 `instance`（新增的托管上下文，ptyp=user）和 `device`（ptyp=device）；拒绝 portal/admin/service、未知 ctx、主体类型不匹配，以及普通 `auriga`、第三方 audience、`auriga.internal`、`ait_` 和 `Auriga-Identity`。不接受客户端 `tenantId/userId/instanceId` 覆盖已验证身份。第一批不支持外部服务账号调用模型；需要时另定机器预算归属。
- **G1/R1 管理面**：经 edge，使用 `aud=auriga`、`ctx=admin` 和显式权限；供应商 key 写入/轮换、签名密钥操作和高风险发布建议要求近期二次验证。普通门户与设备令牌不能调用 `/admin/**`。Go 管理面也要实现与 common 等价的校验和错误映射，不能把所有 AI HTTP 路由都用同一 audience。
- **托管 AI JWT（重大提议 D1）**：推荐 iam 以实例主人为 `sub`，保持 `ptyp=user`，新增 `ctx=instance` 及签名声明 `instanceId/credentialVersion`；不复用现行含义为服务账号的 `ctx=service`。`perms` 必须是本人有效权限与固定白名单 `{ai.models.invoke, ai.usage.read}` 的交集，不能提升未授予的权限，也不能携带角色附带的任何管理权限。`tid/sub/instanceId` 三元组由 workspace 权威记录校验；不伪造 `sid/did`。当前 [TokenService.allowedPermission](../../services/iam/impl/src/main/java/auriga/iam/TokenService.java) 只过滤 device/portal，ctx=service 等其他上下文直接 return true；仅改签发 ctx 或新加枚举仍会走放行分支，**I3a 必须修改专用签发与权限过滤**。G1 对托管令牌含白名单外 perms 的情况拒绝，不从 roles 或其他权限条目扩权。新增 ctx 的 schema/各语言解码枚举与普通业务服务拒收规则须先 C3 评审；这不是现行生效方案。
- **签发入口（候选内部 RPC）**：iam 新增 `IssueInstanceAiToken`，只准 `svc:workspace` 代租户；请求绑定 `tenant_id/owner_id/instance_id/credential_version`，响应 token 与 expires_at。owner 不能由实例调用者指定；iam 必须验证受信 workspace 的委派范围和 org 有效成员身份。gateway 另需 workspace 的内部 `GetInstanceAiBinding`，确认租户、主人、当前凭据代次、实例是否已删除/停用；不得把实例 HTTP 的 `getInstanceSelf` 当作内部代查接口。
- **轮换建议**：短期 JWT 保持现有 5 分钟上限，workspace 提前续签，经 `Secrets.ai_gateway_token` 封装下发；agent 原子换文件并触发原生 MCP/模型配置重载，插件转发客户端每请求重读文件。初议正常轮换重叠不超过 60 秒、以节点应用确认与到期时间共同约束；紧急吊销无重叠。具体时间和流中 token 到期策略见 D1，不能把短期 JWT 改成长效静态 key。
- **设备 AI JWT**：`ptyp=device/ctx=device/sub/tid/did` 沿用设备协议；校验 `auriga.connect` 范围的细分权限并执行同一用户模型授权/预算，不能给设备发管理员权限。当前 [TokenService.allowedPermission](../../services/iam/impl/src/main/java/auriga/iam/TokenService.java) 会过滤非 `auriga.connect.*` 设备权限，新增 `ai.*` 权限不会自然进入设备 JWT，须正式定义映射。
- **bearer 风险与来源约束（D1，用户 2026-10-20 已决定：托管调用只经 edge 内部通道）**：实例中的不完全可信代码能读 AI token 文件；tid/instanceId/凭据代次是声明绑定，不是发送者约束，窃取者在到期/吊销前仍可能冒用。推荐托管调用只经 edge 内部入口；G1 验证 edge 的 mTLS 身份和受信转发标记，edge 丢弃客户端同名头且公网入口拒收 ctx=instance。仅内部入口不证明具体实例，必须由受认证的节点/实例通道确定并绑定 instanceId，不能信任来源 IP、Host 或任意头。C3/WR1/AR1/E3 要补通道和标记契约；设备调用独立走设备入口。即使通道闭合，已失陷的本实例仍能花本人的额度，不能宣称持有者证明；用户已决定实现该通道，不接受纯 bearer 的实例外冒用残余风险。
- **失效建议**：成员停用、离职、权限变化、设备吊销、实例删除以及新增的实例 AI 凭据撤销，触发失效缓存与取消受影响的流；没有确认过的绑定或撤销信息时拒绝新请求。提议数据面每次准入在线核对有效成员/模型授权/配额，运行中的流在 token 到期或撤销时取消。正常重叠只容纳连接切换，不允许以旧版本无限续流。控制面不可用不妨碍 DSH 自身运行，但团队 AI 失败关闭；完整停机/离线策略由 D1 确认。
- **内部调用**：gateway/resource/workspace 调 org 等仍用独立内部服务 JWT + mTLS 和代租户白名单；RPC 请求中的租户必须等于已授权的委派租户。resource 下载的节点身份见 §4.4，不能拿模型令牌下载任意包。

### 2.2 权限 key 候选与单一归属

现有权限体系与格式可复用，但以下 key **均待登记**。主控需在正式权限目录为每个 key 标 `adminOnly: true/false`（新增元数据提议），并给出中英文描述、敏感标记、内置角色映射及 org 授予上限测试。当前 portal 过滤只排除 iam/org/ops 前缀及 admin/grant 后缀，不能挡住新的 write/rotate/review/publish。I3a 必须按已批准的管理权限目录过滤门户令牌，不能仅依赖管理路径检查兜底；客户端不能指定 adminOnly。

- G1：`ai.providers.read`（脱敏供应商元数据）、`ai.providers.write`（端点/模型映射）、`ai.credentials.rotate`（只写 key 与轮换）、`ai.models.grant`（模型授权）、`ai.models.invoke`（托管调用）、`ai.usage.read`（本人）、`ai.usage.admin`（本租户汇总）。管理路径仍独立要求 `ctx=admin`，持有 `.write` 不等于管理会话。
- 连接模式：`auriga.connect.models.invoke` 映射到“本用户可调用的模型”，与托管 `ai.models.invoke` 进入同一策略判定；两者都必须再过具体模型 ACL，`auriga.connect` 总 scope 本身不授权所有模型。资源/文件等设备写操作先不开放，避免与设备禁止 `*.write/admin/grant` 的现行约定冲突。
- R1：`resource.packages.read`（可见目录）、`resource.packages.upload`（本人草稿和 L4）、`resource.versions.submit`（送审）、`resource.versions.review`（审核）、`resource.versions.publish`（审核后发布）、`resource.levels.admin`（L1/L2 等级与全局开关）、`resource.distributions.manage`（本人实例的允许操作）。审核/发布在管理面；本人送审/分发也必须检查归属、等级、版本状态和近期验证规则。
- 以上 key 的 adminOnly=true 集合为 `ai.providers.read/ai.providers.write/ai.credentials.rotate/ai.models.grant/ai.usage.admin/resource.versions.review/resource.versions.publish/resource.levels.admin`；其余上述成员/连接模式 key 为 false。管理员 token 也须具有权限，不能因 ctx=admin 自动获得所有能力。托管 AI 令牌只能取 §2.1 的两项交集，portal 不含任何 adminOnly=true key，device 继续限连接范围。`resource.levels.admin` 操作叠加近期验证，按 [access-token.schema.json](../../contracts/auth/access-token.schema.json) 的 `stepup_time` 检查，缺失或超过默认 600 秒返回已有 `iam.step_up_required`；权限与 step-up 不能互相替代。
- 模型 ACL 与资源可见范围由各自服务管理，org 管成员、组、角色与有效权限。候选 `CheckPermission.resource` 使用 `model/<id>`、`resource/<id>`，但细粒度规则须补正式接口；不能假设现行 org 已存有这些对象的 ACL。

### 2.3 错误与响应候选

普通 REST 复用 [Problem](../../contracts/openapi/common.yaml)，鉴权失败使用已有 `common.unauthenticated`（401）、无权限 `common.forbidden`（403），隐藏跨租户对象使用 `common.not_found`（404），参数/幂等冲突/乐观锁分别用 `common.validation_failed/common.conflict/common.precondition_failed`。新域错误候选如下，HTTP 与内部 gRPC 的映射也应一处登记：

- G1：`ai.model_not_allowed`（403 / PERMISSION_DENIED）、`ai.budget_exceeded`（429 / RESOURCE_EXHAUSTED，返回下一周期等待秒数）、`ai.budget_unavailable`（503 / UNAVAILABLE）、`ai.request_in_progress`（409 / ABORTED）、`ai.replay_unavailable`（409 / FAILED_PRECONDITION）；`ai.upstream_unavailable`（502 / UNAVAILABLE）、`ai.upstream_timeout`（504 / DEADLINE_EXCEEDED）、`ai.upstream_rejected`（502 / FAILED_PRECONDITION）、`ai.stream_interrupted`（已开始的流内错误，不能再改 HTTP 状态）。客户端格式无效使用 400；供应商 key 无效不能伪装成用户令牌 401。
- 429 的区别：提议主体并发/在途预占上限触发使用已有 `common.rate_limited`，实际日/月额度不足使用 `ai.budget_exceeded`。仅已结算用量导致周期额度耗尽时给下一周期等待秒数；仅因在途/unknown 冻结导致余额不足时，不虚构可恢复时间，说明需等待结算/对账。两者都不得触发未知执行结果的自动重放。
- R1：`resource.version_immutable`（409 / ALREADY_EXISTS）、`resource.review_required`（409 / FAILED_PRECONDITION）、`resource.state_conflict`（409 / ABORTED）、`resource.level_locked`（403 / PERMISSION_DENIED）、`resource.digest_mismatch`（422 / INVALID_ARGUMENT）、`resource.signature_invalid`（422 / INVALID_ARGUMENT）、`resource.download_denied`（403 / PERMISSION_DENIED）、`resource.storage_unavailable`（503 / UNAVAILABLE）。签名/摘要错误默认不重试；同一租户不可见的包仍按 404 隐藏。
- G1 的 `/ai/v1` 客户端兼容面需要单列错误 envelope 提议：普通失败使用 `{error:{code,message,type},requestId}`，流中以 SSE `data` 的同类 error 结束；message 脱敏且可本地化，code 与 Problem 相同。它是对默认 problem+json 的局部格式例外（D2），必须由主控确认、用目标 DSH 客户端测试。本文没有将它宣布为既定 OpenAI 全量兼容格式。
- 供应商响应体、认证头、完整 URL 查询串不能透传给调用者或日志。超时、限流与不可重试错误要区分；`Retry-After` 只给确定可重试的准入失败，未知是否执行的模型请求不因收到该头就自动重放。

### 2.4 事件、幂等与并发候选

- **沿用现有事件**：G1 订阅 `user.disabled/offboarded/permissions_changed`、`identity.session_revoked`、`instance.deleted`；R1 订阅 `user.updated`（组与成员类型变化）、`user.offboarded`（`handoverTo`）、`user.deleted`。事件只是加速失效，不能用旧缓存单独批准一次新付费动作。
- **新增事件候选**：`usage.recorded`（ai-gateway 发布，`requestId/accountId/instanceId?/deviceId?/modelId/outcome/inputTokens/outputTokens/usageQuality/costMicros/currency/periodStart/priceVersion`）；`resource.version_published`、`resource.version_withdrawn`（resource，`packageId/versionId/version/digest/revision`）；`resource.distribution_requested`（resource，`distributionId/instanceId/ownerId/selectionRevision`，workspace 消费）；`instance.ai_credential_revoked`（workspace，`instanceId/ownerId/credentialVersion`）。`usageQuality` 候选 confirmed/estimated/unknown，unknown 数量用缺失或 null，不得写成 0。
- org 的配额现无变更事件；第一批在线读取现有 `GetEffectiveQuota`，由 G1 持久化本次限额快照。若要支持严格按新配额即时阻断所有并发请求，需另提带版本的配额变更与授权快照契约（D3），不能把 `user.permissions_changed` 曲解成所有配额变化。
- 每种新事件正式登记生产方 source、subject、schema 和 NATS ACL；建议 usage 的 subject 为 requestId，资源版本事件为 versionId，分发请求为 instanceId 并携带单调 revision。业务事务写 outbox，消费事务按 event id 去重，并按聚合 revision 拒绝过期事件。事件不携带 key、token、签名私钥、prompt、响应正文或任意下载 bearer URL。
- **幂等细化提议**：创建/送审/发布/分发等作用域为 `(tid,已鉴权主体,HTTP 方法,规范路径,Idempotency-Key)`，比较规范化请求内容摘要；同键不同请求返回 `common.conflict`。管理元数据 ETag 防并发覆盖；发布版本的 `(tenant,package,version)` 与分发业务 id 在数据库另设唯一约束，不能只靠 24 小时缓存。
- **模型特例提议**：同键只允许一个执行者、一次预算预占和一次上游提交；并发同键返回 `ai.request_in_progress`。不保存正文来重放流，已终态重试返回 `ai.replay_unavailable` 并附原 requestId/终态元数据，不再次执行。这与创建 POST 通用“同结果”约定有差异，须 D2 明确例外。未提供幂等键时每次调用是新请求，也不由 gateway 自动重试。

## 3. G1 模型网关第一批提议

### 3.1 数据所有权与接口边界

ai-gateway 使用已规划的 PostgreSQL `ai` schema，拥有供应商配置、托管 key 的加密版本、模型别名/授权、请求用量与预算预占账本；org 拥有限额与成员/组/角色，iam 拥有平台令牌签发，workspace 拥有实例归属。G1 不读写其他服务 schema，不新增数据库或预算中间件。

建议正式契约入口 `contracts/openapi/ai.yaml`（新文件，提议）与需要的 `contracts/proto/auriga/ai/v1/`；类型生成方式和输出位置由契约负责人统一接线。候选接口最小集：

- `GET/POST /api/ai/v1/admin/providers`，`GET/PATCH /api/ai/v1/admin/providers/{providerId}`：供应商 id、类型（首批只实现一个兼容适配器）、baseUrl、enabled、连接/首字节/空闲/总超时和并发上限；响应仅含 key 版本状态，不含 key。POST 支持幂等，修改支持 ETag。
- `POST /api/ai/v1/admin/providers/{providerId}/credentials`，`POST .../credentials/{credentialId}/activate`、`.../revoke`：只写 secret、返回 credentialId/state/createdAt；不得提供读回 secret 的 GET。激活/撤销用幂等键和版本条件，须 `ai.credentials.rotate` + 近期验证。
- `GET/POST /api/ai/v1/admin/models`、`PATCH /api/ai/v1/admin/models/{modelId}`、`PUT .../models/{modelId}/grants`：模型别名到 provider/upstreamModel 的受控映射；授权对象为成员、组或角色引用，全部限定租户。别名、contextWindow、最大输出、输入/输出单价、币种、价格版本与生效时间必须可核对；grants 用 ETag 整体替换，权限为 `ai.models.grant`，禁止借角色名绕授予上限。
- `GET /ai/v1/models`：仅返回本主体当前有权调用且可用的模型别名；不泄露 upstream key 或端点。`POST /ai/v1/chat/completions`：首批限定 messages、model、stream、受控 max_tokens 与明确列出的 tool/采样字段；未知/不支持字段返回 400，不盲透任意参数。允许的非流式响应与 SSE chunk 结构要正式建 schema 和 golden fixture。
- `GET /api/ai/v1/me/usage`、`GET /api/ai/v1/admin/usage`：按 UTC 周期、模型、终态查询账本，分别仅本人/本租户，游标分页；不返回 prompt/生成正文。取消先随 HTTP 连接关闭传播，不先增加任意 requestId 的远程取消 API。
- 内部 `ListEffectiveModels`：workspace 用受信内部身份按 owner/tid 获取受控模型清单以生成 `Profile.model_providers` 和 ConnectManifest。是否给 resource 之外再加一个 Java API 包装由主控确定；不得由 workspace 直接读取 `ai` 表。

第一批不声称支持 Responses、图片、音频、embedding、任意供应商协议或自动模型切换；新增能力必须扩明确的字段和测试。测试中的 `model=example-model`、价格和 token 数全是合成数据。

### 3.2 供应商、key 托管与轮换

- 供应商端点只能由有权限的管理面配置，数据面只能提交 model 别名，不能提交 baseUrl、代理地址、认证头或任意绝对 URL。限制协议、DNS/IP、重定向与响应大小，防止用 key 托管器访问元数据服务、其他租户或任意主机。默认不跟随重定向。
- secret 在应用层加密，主密钥从部署环境或受保护密钥文件读取，不存入数据库同一行或仓库。建议 AES-256-GCM，AAD 绑定 tid/providerId/credentialId/加密版本；算法、nonce 和主密钥轮换格式也需评审，测试现生成密钥。第一批无需 Vault、KMS 或云账户。
- key 状态建议 staged→active→retiring→revoked；每次请求固定一个 credentialId。正常轮换只让新请求用新 key，已开始流按旧 key 完成；紧急撤销取消旧 key 的流并禁止新请求。主密钥轮换与供应商 API key 轮换是两件事，不能只改一个状态字段就声称完成加密重包。
- upstream key 只在 gateway 发起上游连接时解密，从不进入 `InstanceSpec`、ConnectManifest、renderer、插件、浏览器或审计正文；各实例拿到的是平台 AI 令牌。个人模型与个人 key 继续由 DSH 自己管理，workspace/renderer 不覆盖、不汇集。
- 第一批只用内存 Transport/可控假 upstream 和临时密钥证明行为。若后续要支持真实内网模型，须另定端点信任策略；不得静默放宽现有 egress“内网一律拒绝”的实例出网规则。

### 3.3 用户到模型的授权

准入顺序建议为：验证 JWT 与租户→核对实例/设备绑定和成员有效状态→检查调用权限及模型 ACL→校验模型能力/输入上限→读取 org 有效配额→在 `ai` 事务内抢占预算→只发一次上游请求。任何失败都不发送上游请求；调用者传入的用户字段仅可作为普通业务参数，不能影响计费主体。

角色/组授权必须查当前租户的权威信息，禁用/离职和权限撤销使缓存失效。未定义授权快照版本前，第一批每次新请求执行在线授权与配额查询，查询失败返回 503；JWT 的 `perms` 是第一层筛选，不能保证具体模型的权限从签发后一直未变。设备/托管实例并发使用同一 `(tid,sub)` 预算，不按 token、实例或设备各领一份额度。

### 3.4 流式、取消与失败语义

- 以 `stream=false` JSON、`stream=true` SSE 各实现一条受限路径。保留 chunk 顺序、工具增量和结束原因；按字节/事件限额做有界背压，不把整段输出缓存在内存后假装流式。分片 UTF-8、跨块事件边界必须正确处理。
- 下游取消/断连、token 到期、授权撤销、gateway deadline 均取消上游 context 并关闭 body；仍要执行一次终态结算。取消不能保证供应商已停止计算或不计费，不能直接把账单置零。首字节、流中空闲、总超时分别配置，慢客户端不能无限占并发和预占。
- 发响应头前的错误按 §2.3；响应开始后不能补发 problem+json 或切换 200 为 5xx。只能尽力发送脱敏流错误并关闭，失败流不发成功的 `[DONE]`；断线客户端通过用量查询看本 requestId 的终态。目标客户端是否把 error chunk 正确显示列入 D2 验收。
- 第一批不做上游自动重试、跨供应商故障切换、断点续流或业务工具重放；尤其“发出请求但未收到响应”的场景视为结果未知。以后需要重试必须引入 attempt 与预算、计费重复风险的正式约定。

### 3.5 用量、预算与崩溃一致性

以下是实施前应确认的账本方案（D3），不是现有 org 配额的已实现能力：

1. 按 `(tid,accountId,UTC 日/月,币种)` 聚合已结算与未释放预占，所有 gateway 副本通过 PostgreSQL 行锁/条件更新原子判断。禁止仅进程内计数、事件异步扣费或用 NATS KV 做余额权威。月预算与日 token 限制需在同一准入事务同时通过；未设置 optional 限额表示不限是既有定义，**模型预算/token 配置 0 视为禁止是本草案提议，须在 org 契约补注释并测试后才生效**，不能从实例 max_instances=0 的注释推导模型语义。
2. 用模型输入上界、明确最大输出和固定的价格版本计算保守预占，整数 token、货币 micros，向上取整且检查溢出。不能以字符数假装精确 token；若适配器无法提供可信上界或总价包含未声明项目，拒绝“硬预算”调用，先用可精确控制 token 的假 upstream 验证。
3. 请求记录建议含 requestId、认证主体来源、instanceId/did、providerId/modelId/credentialId、授权/配额快照、价格版本、periodStart、预占、状态、终态原因、用量质量。`reserved→dispatching→completed/failed/cancelled/unknown` 的转换带版本，终态只能结算一次，唯一 requestId 防止重复扣账；明细与 `usage.recorded` outbox 同事务提交。
4. 在发上游前持久化 dispatching；崩溃后仅 reserved 且从未提交上游的记录可按状态机释放。dispatching 或流已开始而缺最终 usage 时进入 unknown，**不因租约超时自动退款**。保留最坏预占或按已批准的保守上界结算，等待对账；不得用 0 伪造真实 token。没有供应商对账接口的第一批可保守冻结额度并明确展示 unknown。
5. 成功且有可信最终 usage 时，以实际用量一次结算并释放差额。失败/取消有 usage 也记账；超出预占、负数、溢出或币种/价格不符进入异常对账并阻止继续错误结算。不能承诺供应商在断流后不超算；硬上限需要供应商支持的明确输出限制和上述保守冻结策略。
6. 跨日/月的调用按准入时固定周期结算，重试终态更新仍写原周期；建议 UTC，不能随租户时区或价格修改回写旧账。第一批单一配置币种，不自动汇率换算。配额降低后阻止新的超额准入，已在途预占不抹掉；若要立即取消在途流，由 D3 明确策略与事件。
7. PostgreSQL/授权/配额服务故障在上游调用前返回 503；上游已工作但结算失败时保留未决记录供恢复器重试，本次不宣称完成持久记账。NATS 不可用不丢账，恢复后投递 outbox；消费者重复投递不重复计量。Go 的数据库角色/RLS、outbox/inbox、审计脱敏均须独立验收。
8. **主体并发与预占上限（D3 提议）**：按 `(tid,sub)` 跨实例/设备/gateway 统一限制在途流数量和总预占，初议 `maxActiveRequestsPerSubject=2`、月金额与日 token 各自的在途预占不超过对应周期配置限额的 25%；值由主控/用户确认后写正式配置契约，不能让模型或实例自调。单次请求也不能超过该预占上限；限额“不限”时比例不可计算，要求部署另给正整数的绝对在途金额/token 上限，未配置则拒绝该类调用。并发槽和预占在同一数据库准入事务领取，跨所有入口聚合；超额在发上游前返回 `common.rate_limited`，不冻结更多余额。取消/完成只释放一次槽，unknown 的保守预占仍计入占用上限，不能靠重复断流/换 token 清零；超时槽回收不等于退还 unknown 金额。此策略限制一次突发的自我耗尽，但不能保证抵御持续使用已窃取 bearer 的预算消耗，残余风险仍属 D1。

### 3.6 最小测试矩阵（未来任务验收，不是本次已运行）

- **身份 1**：有效托管/设备令牌可用；错 issuer、audience、typ、算法、过期、伪造签名、未知 kid 更新失败、`ait_`、内部服务 JWT 均在上游前被拒。ctx=portal/admin/service、设备/托管主体组合不符、托管 perms 含白名单外项、伪造 edge 转发头或绕过受信入口也被拒，断言具体 code。
- **身份 2**：跨租户、错主人/实例/设备、成员停用、设备吊销、实例删除、凭据版本撤销；同一实例不能用参数冒充另一个用户，设备不能管理 provider/key。
- **授权**：仅授权模型出现在 models；组/角色变化后下次调用拒绝；无权限、控制面不可用上游计数始终为 0；两个实例和设备共享用户额度。
- **凭据**：新 key 只供新请求、旧流正常完成；紧急撤销取消旧流；数据库密文换 tid/provider 解密失败；配置、日志、审计和响应都无 secret。
- **转发**：JSON、SSE、多字节切块、tool-call 增量、有界背压、慢客户端、格式错误、超大请求/响应；上游错误不泄露 body/认证头。
- **取消**：下游 AbortSignal/断连、首字节/空闲/总超时、授权撤销、token 到期；证明假 upstream 收到取消，活动请求和 goroutine 回到基线。
- **错误**：上游拒绝、429、无响应断线、流中断、畸形 chunk；未开始/已开始响应分别断言 HTTP 与流内语义，不自动提交第二次请求。
- **预算**：按批准后的 0/不限语义、货币整数舍入/溢出、输入与最大输出上界、UTC 换日/月、配额降低、同币种价格快照；两个独立 gateway 实例并发压同一数据库，不能超卖额度。跨设备/实例并发压同一主体时，最多批准配置数量的流、预占不超过批准的比例/绝对上限，拒绝请求上游计数为 0；断流、unknown、重复完成与进程恢复不能重复释放槽/金额，分别断言 rate_limited 与 budget_exceeded。
- **幂等与恢复**：同键同请求并发一次 dispatch，同键异体冲突；断电点覆盖预占前后、dispatching 后、收到 usage 后提交前、提交后投递前；取消与完成竞争只结算一次，unknown 不自动退款，重复事件只处理一次。

单测优先内存 HTTP Transport 和可控时钟；集成测试用嵌入式 PostgreSQL及假 upstream（只用随机临时端口并回收进程）。实施阶段也不接付费模型、不用真实 key、不访问真实部署。

## 4. R1 资源中心第一批提议

### 4.1 包、版本、摘要与签名

- resource 使用自己的 `resource` schema，管理 Package、Version、Review、Publication、Distribution 和对象索引。Package 候选字段 `id/tenantId/name/kind/ownerId/level/visibility/groupIds`；Version 候选 `id/packageId/version/digest/sizeBytes/mediaType/manifest/state/revision/signed/createdAt`。数据库 ID 与包名分开，npm 名不能充当跨租户主键。
- 第一批落地 plugin，验证 package→version→分发全链；kind 为 skill/environment_template 的元数据与职责入口先留提议，不把它们塞成 `PluginRef` 或运行 npm 安装。skill 的 `SKILL.md/scripts/manifest`、模板 patch 能力要各自补 schema、消费规格和安全验证后再派；没有正式类型不接受安装请求。
- 版本发布后 `(tid,packageId,version)` 内容不可覆盖；同版本换 digest 返回 `resource.version_immutable`。修改草稿重新校验、递增 revision 并清空此前审核。可见范围和发布状态的变化不修改原始归档字节，另保留审计和修订号。
- 归档以 sha256 内容寻址，上传/读回均核对 size 与 digest。平台审核/签名服务签完整 `ArtifactStatement`，节点同时核对 purpose/name/version/os/arch/digest 与 workspace 期望值，不能只验“某个包有平台签名”。不复用 edge 身份头私钥或面板私钥；发行方私钥留在 resource 的受保护签名边界，公钥通过受信控制面下发。
- 当前 `ArtifactStatement` 无 tenant/resource 身份，只有 issued_at 而无失效时间。推荐新增资源签名 envelope，必含 `schemaVersion/tenantId/packageId/versionId/notAfter/statement`，其中 statement 对应现有完整制品声明；外层 `signature.{alg,kid,value}` 对去掉 signature 的整个 envelope 做 JCS + Ed25519，租户/身份/有效期都在签名覆盖内（D4）。notAfter 为 RFC 3339 UTC，须晚于 statement.issuedAt，安装时过期就拒绝；重签可延长有效期但不得变更包字节、versionId 或 digest，需重新授权并审计。现有 `PluginRef.name` 是 npm 名，不能悄悄改变为带租户 ID 的字符串；新增 envelope 不替代或改义旧 signed 字段，C3 应明确两层关系并同时校验。
- **撤销清单格式与通道（D4 提议）**：新增 schemaVersion=1 的租户级签名清单，正文 `tenantId/revision/issuedAt/expiresAt/revokedKids/revokedArtifacts`，后者每项含 packageId/versionId/digest；signature 的 alg/kid/value 签署完整正文。清单由独立的受信撤销授权密钥签发，根公钥经受保护部署信任配置提供，不能相信归档自带公钥或只由被撤销的制品 key 自证。resource 向 workspace 提供最新版，workspace 经 `ApplySpec` 的新增字段下发，C3 同时定义字段、schema、签名向量与大小上限；不是现有 ApplySpec 已有此字段。agent 按 tenantId 持久化最高 revision，拒绝回滚、未来签发时间、篡改/过期清单，核对 envelope 未过期且 kid/制品不在撤销清单后才安装或启用，包括缓存命中。清单过期/不可达时拒绝新安装/启用，已运行实例按 D4 已定策略处理（离线节点撤销最迟 24 小时生效），不暗示立即终止。purpose 增加 skill/template 前先定专用枚举/规格，绝不沿用 plugin 冒充。
- spec.proto 已规定“声明的 JCS（RFC 8785）规范化 JSON + Ed25519”原则，尚需定义 protobuf→JCS 的精确字段名、时间精度、缺省字段处理和编码：例如候选 JSON 使用 `issuedAt` 的 RFC 3339 UTC 字符串、外层 signature.value 用 base64url 无填充；这是待评审映射，不以默认 protobuf JSON 序列化器输出当标准。生成跨 Java/Go golden vectors，空字段和时间小数也要一致，覆盖 envelope 与撤销清单。
- 节点持久化每类制品最高允许版本；正常升级不得回滚，降级必须有单独、可审计且限实例/版本/时效的授权（D4）。普通管理员选择旧版本不能自动绕过高水位；不能拿 SemVer build metadata 当单调安全版本。key 撤销后新下载/新安装失败关闭，已运行包如何隔离/停用见 §4.4。

### 4.2 等级、可见范围与审核发布

L0–L4 枚举已定；下面是待写入正式 API/策略的**细则提议**：

- L0 核心：随部署固定，成员不能关闭/卸载，资源 API 不能更换门锁；管理面只读版本，升级仍经部署/运行时流程。
- L1 系统：授权管理者统一版本与开关，成员只读、不能卸载；L2 团队默认：管理者决定默认安装版本，成员可关闭、不可卸载；L3 可选：审核发布后由成员自行安装/关闭/卸载；L4 个人：只供本人实例，显示未审核，可自行关闭/删除，且受 `WorkspacePolicy.allowPersonalPlugins` 限制。
- 等级决定“谁可操作”，可见范围决定“谁可发现/取得”，二者必须同时满足。候选 visibility 为 `tenant/members/groups/private`：tenant 含本租户外部协作者；members 仅正式成员；groups 仅当前组成员；private 仅 owner。没有全局公共/匿名发布，L4 强制 private，客户端不能通过 patch 把它升成 L1 或 tenant。
- 正常状态机建议 `draft→pending_review→approved→published→deprecated/withdrawn`，另有 rejected→draft。审核绑定精确 digest+revision，published 必须先 approved 且对象验证/签名成功；deprecated 保留固定版本下载但不作新默认，withdrawn 拒绝新的下载与分发。L4 可走本人私有保存/平台签名支路，仍无“已审核”徽标，也不能从 private 直接上架共享。
- 第一批不开放“可信作者免审”。审核者与发布者的权限、是否强制双人、L1/等级调整限何种 owner 权限须由主控落实角色映射；普通成员不得自审共享发布。作者离职按 `handoverTo` 转交，跨租户接手拒绝；没有接手人先冻结变更，不把私人包自动扩成团队可见。
- 归档检查大小、条目数、解压总量、压缩比、路径规范化、绝对路径/`..`、符号链接/硬链接逃逸、重复文件、文件类型、清单与兼容版本；不执行上传包的安装脚本来“验证”。路径合法、摘要有效、平台签名只证明来源和完整性，不证明任意插件代码可信。

### 4.3 管理、上传与分发接口候选

建议正式入口 `contracts/openapi/resource.yaml`、`contracts/proto/auriga/resource/v1/` 和资源事件 schema（均新建提议）。最小候选：

- `GET/POST /api/resource/v1/packages`、`GET/PATCH /packages/{packageId}`：可见目录、本人新草稿及元数据；响应带 ETag，列表不能泄露不可见包名。管理等级/租户默认另走 `/admin/packages/{packageId}`，带 admin context 与专用权限。
- `POST /packages/{packageId}/versions`：流式上传归档及声明 metadata，返回 versionId/digest/size/revision/state；先暂存检查再登记，客户端给出的 digest 只是待验证值。第一批不接受“给一个任意 URL 让服务代下载”。
- `POST /packages/{packageId}/versions/{versionId}/submissions`：本人送审；`POST /admin/reviews/{reviewId}/decisions`：approve/reject、原因与待审 revision；`POST /admin/packages/{packageId}/versions/{versionId}/publications`：检查审核/签名后发布。创建支持幂等，状态流转带 If-Match。
- `POST /admin/packages/{packageId}/versions/{versionId}/withdrawals`：下架及原因；不删正在使用的对象，不宣称马上从所有实例卸载。审核、发布、下架、等级调整均记审计。
- `POST /api/resource/v1/distributions`：成员提交本人 instanceId、versionId、install/enable/disable/remove；服务端验证归属、版本、等级、可见范围。响应 202 + distributionId，不能返回“已安装”；`GET /distributions/{id}` 只供本人/授权管理者看 requested/resolved/applying/applied/failed 和规格代数。
- 内部 `ResolveInstanceResources`：只允许 workspace 代租户，输入 owner/instance、资源选择及 selectionRevision，返回固定版本、digest、签名和受控下载描述。它不修改 workspace 表，不直接向节点发命令。返回精确锁定结果，重试同 revision 不重新解析“latest”。
- 下载分元数据读取与包字节访问，候选为 `GET /api/resource/v1/packages/{packageId}/versions/{versionId}/content`。浏览器只取得有权限内容；节点下载不依赖浏览器 cookie，采用下节独立的受限授权。禁止仅提供 `/blobs/{digest}` 无鉴权下载。

### 4.4 resource→workspace→agent→renderer 与下载边界

1. resource 在事务中保存分发意图并写 `resource.distribution_requested`；workspace 去重消费后在线复核实例主人、资源状态和策略，通过内部解析接口得到确定资源清单，保存自己的期望规格并递增 generation。旧 revision 到达不能覆盖新选择；跨服务不用分布式事务或共享表。
2. workspace 将 plugin 精确引用放进既有 `InstanceSpec.plugins`，下发 `ApplySpec`。未来 skill/模板需要新增规格入口，不能让 renderer 猜文件位置或让插件轮询同步。resource 读取状态要经 workspace 的候选 `GetResourceApplyStatus`（返回 generation/observed_generation/phase/ready/error），不能直查 workspace schema。
3. agent 是唯一执行者：本地策略校验、取得受信发行公钥、校验声明/签名/摘要、安全解压到暂存区、原子安装到只读共享缓存、按实例引用计数。renderer 只消费已校验规格与制品路径，确定性生成平台拥有的 profile/patch；不做资源审核/选版本/拉网包，不改用户模型/设置，不运行 `npm install`。
4. 插件只显示申请/应用状态、拦截原生越界管理，并处理必要进程内 reload；不承担下载、升级、拷贝、资源同步。需要重启的变化先 pending_restart，由 workspace 决策；`observed_generation` 达目标且 ready 才反馈 applied。下载、渲染、启动失败上报 failed，不把原先可运行版本直接破坏。
5. **节点下载授权提议**：resource 提供受控内部下载入口，验证节点 mTLS SAN，并核验 workspace 授予的 `downloadId/nodeId/tid/instanceId/versionId/digest/expiresAt` 绑定。downloadId 仅为定位符，单独泄露不授予下载权；不开放任意节点枚举租户包。授权/节点吊销/调度迁移后重新校验；下载断点重试、Range 和过期刷新要正式约定。当前协议没有这套授权，应先补契约，不能默认 agent 下载器已有节点 mTLS 客户端能力。
6. 先走平台内下载代理与本地存储；将来 S3 预签名 URL 是 bearer 能力，需限对象/方法/时效并防日志泄漏，不能复用模型/实例令牌。缓存命中仍需 workspace 的合法规格及撤销/策略检查，不因同 digest 已在节点就绕过租户 ACL。
7. 下架默认阻止新的安装/升级，已运行实例保留已应用版本并通知管理者。紧急恶意包撤销要 workspace 下发显式禁用/替换、agent 应用本地拒绝规则，并由生命周期策略处理运行进程；仅发 resource 事件不能保证撤销正在执行的代码。离线节点何时停用和强制重启属于 D4 决策，不能承诺即时全局清除。

### 4.5 对象存储接口与一致性

平台已规划本地盘/S3 兼容抽象，第一批只实现本地盘和测试适配器，不创建云 bucket。候选进程内 `ObjectStore` 接口为 `PutStaged(stream,maxBytes)→stagingId/digest/size`、`CommitIfAbsent(stagingId,digest)`、`Open(digest,range?)`、`Stat(digest)`、`Abort(stagingId)`、`DeleteUnreferenced(digest)`；这不是给浏览器的直接文件系统 API。

- 对象 key 从校验后的 digest 推导，不能接受用户文件路径或外部 URI；对象物理去重不合并租户元数据与 ACL。Open 之前由 resource 完成业务鉴权，不能让 S3 key 自身充当权限。
- 先暂存、限额/摘要/归档检查、提交不可变对象，再在 resource 事务中保存版本/审核引用和 outbox；数据库失败留下孤儿对象，由带宽限期的清理器处理。存储提交失败不能发布元数据。重复 Commit 必须校验内容并幂等，半写对象不可被读作已完成版本。
- 删除先确认无发布版本/分发/保留策略引用，再清理孤儿；不能收到一次撤回就直接删除共享 digest。resource、节点缓存各自管理引用和保留期，不共享引用计数表。
- 测试覆盖本地盘原子性与磁盘满/中断/并发，S3 首批只固定接口预期，不宣称已通过真实 S3、HTTP Range 或大文件性能验收。

### 4.6 R1 最小验收

- 包、版本、digest、签名身份字段逐项篡改，错 tenant/包/版本/purpose/os/arch、未知/撤销 kid、envelope.notAfter 过期、撤销清单过期/篡改/回滚及被撤销制品缓存命中均被拒；Java 签名→Go 验签的 envelope/清单 golden vectors 必须一致。
- 跨租户同名/同 digest 不泄露、不授权；tenant/members/groups/private、外部成员和组退出各有负向测试；L0–L4 安装/开关/卸载/升级规则逐项验证，服务端拒绝越级与 L4 共享发布。
- 修改待审归档使旧批准失效；未审核不可共享发布、重复发布/并发 ETag/同键异体、发布后换 digest、下架后新下载均断言明确 code；L4 只限本人且关闭个人上传策略后拒绝新增。
- 对象写中断/磁盘满/DB 回滚/NATS 暂停/重复事件/事件乱序不产生已发布空对象、重复分发或回滚旧规格；离职接手不跨租户扩大范围。
- 恶意归档路径/链接/重复条目/压缩炸弹失败关闭；agent 假驱动端到端证明旧可运行版本保留、pending_restart 受 workspace 控制、相同 generation 幂等、最终 observed+ready 才 applied；插件没有同步代码。

## 5. 其余 M3 模块的依赖与契约入口

- **G2 MCP 网关**：复用 G1 的 AI audience、绑定/撤销和权限底座；入口 `/ai/mcp`、Streamable HTTP 来自 [ADR 0016](../adr/0016-mcp-session-context.md)。新契约需要工具目录、每工具权限、输入/输出、错误、超时、取消和写工具幂等。`params._meta` 的 `auriga/sessionId/toolCallId/rootCallId` 仅是关联信息，先认证实例再检查会话—任务绑定；原生 MCP Session ID 不是业务会话。不重复暴露同一工具、不假设自动重放安全；G2 不要求先实现七类工具。
- **N1 知识**：knowledge 管空间 ACL、提议/审核/贡献状态和策略，kb-server 仍用既有 memwiki 远程模式；候选入口 `contracts/openapi/knowledge.yaml` 与 G2 工具 schema。依赖 G1 的受控模型预算、G2 鉴权、团队知识后端；首次只做显式提交/待审边界。个人记忆不全量抓取，自动提炼/默认贡献、告知与退出策略须另审，不能借旧原型业务把个人库自动导入。
- **X1 出网**：入口先复用 org `PolicyService.GetPolicy(key=egress)`、`EgressPolicy` 与 `InstanceSpec.network.egress_proxy/allow`；缺实例到代理认证、分类清单/版本、审计与拒绝原因契约。依赖 workspace/agent 的实例网络绑定与防绕行，验证 DNS 重绑定、重定向、私网拒绝和非代理直连。G1 到供应商的流量与实例出网是不同主体，不允许拿通用 HTTP_PROXY 代替授权；真实内网目标例外见 D5。
- **F1 文件**：按节点设计由 workspace 授短票、edge 到 agent ingress，字节不经 Java 中转。候选入口 workspace 文件票据 OpenAPI 与新 JSON Schema；当前 [ticket.go](../../infra/agent/internal/ingress/ticket.go) 已有实现字段 `iss/aud/iid/path_prefix/ops/iat/exp/jti`、`typ=auriga-file+jwt`，但它们是实现事实，未形成完整跨进程正式 schema，不能直接宣布为 M3 契约。需确认租户/主人/节点绑定、key 分发、范围/操作/时效、写操作重放和路径安全；F1 的用户文件不是 R1 包仓库，不共享任意对象读取权限。
- **P2 团队面板**：加载清单、JCS/Ed25519、SRI、Web Component、`context.fetch`、主题/语言和事件已有 [插件协议 §5](../../contracts/plugin/README.md)、[ADR 0017](../adr/0017-team-panel-loading.md)。依赖 R1 目录/分发状态、G1 本人用量、F1 文件、设备与账户接口；第一批只显示已定义数据。先解决 §1.2 的独立源桥接，再接写操作，不能开放实例 CORS或下发门户 cookie；连接模式继续由插件 Node 代发，设备 token 不进页面。

**TaskId 重名核查**：[progress.md](progress.md) 的 M0 “P1–P6 开工准备”包含环境探测，M3 路线图又用 P2 指团队面板；P1 开工准备也与 P1 插件托管模式冲突。建议今后使用 `auriga-M3-PANEL-01`（面板），历史环境探测完整标为 `auriga-M0-ENV-P2`；M3 文档引用 P0/P1/P2 等任何短码时一律补里程碑与任务类型前缀，不能靠上下文猜同名任务。同样在任务文件、worktree、报告、审查记录中用完整 ID。本文不修改旧进度或旧任务名。

## 6. 按依赖拆首批任务（建议，均未派发）

### 6.1 开工门槛与文件所有权

先主控裁定 §7 决策项，随后正式契约及类型生成经独立审查定稿，完成 D0b 前置依赖接线后再派 G1/R1 实现。下面“只能改”是未来任务白名单提议，不扩大本次 C2 的两文件授权。`**` 表示该子树；列出的任务互不写同一路径，依赖清单归 D0b、最终运行装配归 Z1。每个任务还可写自己的根 `.agent-report.md`，主控汇总时不能用报告覆盖他人源文件。

**外部任务串行门槛：WR1、AR1、E3、Z1 一律等 W2H、W3 均合并后才派。** W2H 涉及 edge、agent ingress、platform-app、workspace；W3 的 `e2e/**` 包含 Z1 的新 `e2e/m3/**`，目录尚不存在也不代表无 glob 冲突。因此不能与它们并行；合并后主控核对最新路径/基线再派，不能把“已评审”或“CI 绿”当成已合并。Z1 不改既有 M2 脚本/Compose，新增目录也只在上述门槛后开发。D0 的 `deploy/nas/**` 不在本文任何白名单内。W2V 仅为历史验收背景，不替代 W2H/W3 这项新门槛。

所有任务均需服务端负向测试、具体错误码、中英文错误文案、脱敏测试；新增数据库业务表必须 tenant_id + RLS，并用无 BYPASSRLS 的运行角色测跨租户。以下命令只是验收建议，不在本次 C2 执行。共享接口须来自已批准契约；发现缺字段就报主控，不能扩白名单。

### 6.2 契约与身份底座

1. **`auriga-M3-C3`（主控正式契约与生成）**
   - 只能改：`contracts/**`、`services/contracts/**`、`infra/gen/auriga/**`、`web/shared/src/generated/**`、`web/shared/scripts/gen-api.mjs`、`docs/adr/0021-m3-contract-boundaries.md`（新 ADR 建议名，编号须先核对未占用）。TS 输出目录依据现有 gen-api.mjs；Java 生成物位于 services/contracts 的 target，不手工维护。Go 只写生成包，不写 gen/go.mod/go.sum。现有 `infra/buf.gen.yaml` 的目录输入自动覆盖新增 proto 包；若该配置或 `infra/generate.go` 需改，先补报主控重写白名单，不能擅改。
   - 依赖：D1–D4 等关键决策；单一契约负责人串行维护 G1/R1/绑定/下载/事件，不把 auth/spec/common 分给两个并行作者。
   - 验收：OpenAPI lint、buf lint/breaking、JSON Schema strict 编译、Java/Go/TS 生成编译；跨语言签名 golden vectors；逐操作权限/错误/事件来源清单齐全，明示流式幂等例外。破坏性变更先 ADR/版本策略。实现开始前冻结一版契约基线。
2. **`auriga-M3-D0b`（前置依赖接线，新增建议任务）**
   - 只能改：`infra/ai-gateway/go.mod`、`infra/ai-gateway/go.sum`、`infra/gen/go.mod`、`infra/gen/go.sum`、`infra/go.work`、`infra/go.work.sum`；`services/resource/api/pom.xml`、`services/resource/impl/pom.xml`、根 `pom.xml`。不写业务代码、生成包、main 或 platform-app 装配。
   - 依赖：C3 定稿与主控预先批准依赖清单及固定版本；**必须在 G1a/b/c、R1a/b 派发前合并或提供统一已审查基线**，不等这些实现通过才补依赖。若批准清单不足，先修订本任务再派实现，不把 go.mod/pom 移回最后 Z1。
   - Go 候选清单（全部待主控批准，不是本次新增依赖）：JWT 验签 `github.com/golang-jwt/jwt/v5 v5.3.0`，JWKS 获取/限速缓存用 Go 1.27.0 的 net/http 与 encoding/json（不另引 JWKS 库）；PostgreSQL `github.com/jackc/pgx/v5 v5.7.6`；NATS `github.com/nats-io/nats.go v1.47.0`；测试 `github.com/fergusstrange/embedded-postgres v1.34.0`、`github.com/nats-io/nats-server/v2 v2.12.0`；内部 RPC `google.golang.org/grpc v1.76.0`、`google.golang.org/protobuf v1.36.10`。JWT/pgx 是新候选版本，需主控核实可用性后固定；后五项版本取现有 ctl/edge/gen 模块。若批准沿用现有 `lib/pq v1.10.9` 而不选 pgx，须在 D0b 定稿时二选一并同步仓储接口，工人不能自行替换驱动。
   - 本地生成模块按现有 agent 做法 `require github.com/hansjone/auriga/infra/gen v0.0.0`、`replace .../infra/gen => ../gen`，保留 go.work 的 use 条目；验证 worktree 内模式与 `GOWORK=off` 模式均能解析，不能只有 workspace 隐式解析成功。
   - Java 清单：resource-api 按生成类型需要依赖 `auriga:contracts:${project.version}`；resource-impl 保留 resource-api/common/contracts、增加测试范围 `auriga:test-support:${project.version}` 与 spring-boot-starter-test/webmvc-test。Web/JDBC/Security/Flyway/PostgreSQL/MyBatis/NATS 优先复用 common 的现有依赖链，不重复选新版本；需直接声明则仍由根 dependencyManagement 管理。基线 Spring Boot 4.1.1、MyBatis-Plus 3.5.17、jnats 2.26.4、Zonky embedded-postgres 2.2.2 及测试 PG binaries 17.11.0 均为已有根属性，不在子 pom 硬编码另一个版本。
   - 验收：受批准版本/许可证及可解析性核对、Go 模块校验与独立模块编译、Java resource reactor test-compile/边界检查通过；确保 JWT/JWKS、pgx、NATS、嵌入式 PG 测试所需依赖在前序任务开始时已可用。骨架尚未导入的预声明库通过模块下载/校验验证，不能在此时用 tidy 删除它们；不能用 work sync 重写未授权的其他模块 go.mod。空骨架通过编译不等于业务验收；任何后续新增库都退回主控重审此清单。本次 C2 不安装或升级任何依赖。
3. **`auriga-M3-I3a`（IAM 签发与权限登记）**
   - 只能改：`services/iam/**`、`services/org/**`；其中 org 只改正式批准权限目录、角色/设备映射与所需授权接口适配，不重写配额规则。
   - 依赖：C3，所需构建基线为 D0b；必须修 `TokenService.allowedPermission` 与托管签发专用分支。验收即使实例主人是 owner/admin，ctx=instance 的 perms 也仅为本人权限与 `ai.models.invoke/ai.usage.read` 的交集；portal 令牌不含 §2.2 全部 adminOnly=true key（特别 ai.providers.write/ai.credentials.rotate/resource.versions.review/resource.versions.publish/resource.levels.admin），device 仍只限连接范围。验证工作空间独占签发、tid/sub/instance 绑定、设备 resource audience 交换、错 ctx/越权/重放/吊销与 key 轮换负向测试。不得通过给设备补所有 `ai.*` 权限绕 scope。
   - common 现有校验器不放宽；若 C3 要求改 common，单独回主控补任务，不混入本任务。

### 6.3 可并行的 G1 / R1 小任务

4. **`auriga-M3-G1a`（G1 鉴权、目录与 key 托管）**
   - 只能改：`infra/ai-gateway/internal/auth/**`、`internal/catalog/**`、`internal/credentials/**`、`internal/contracttest/**`（后面三项均相对 `infra/ai-gateway/`）。仅新增这些子树，含各自测试与脱敏错误文案；不改 main、go.mod 或共享生成类型。
   - 依赖：C3、D0b，完整签发联调等 I3a；验收 §3.6 身份/授权/凭据项、管理与数据 audience 分离、数据 ctx 仅 instance/device、托管权限白名单、无供应商 key 下发、跨租户 RLS。输出可由装配注入的服务，不自行占端口启动常驻服务。
5. **`auriga-M3-G1b`（受限模型转发与取消）**
   - 只能改：`infra/ai-gateway/internal/inference/**`、`infra/ai-gateway/internal/upstream/**`（含假 upstream、流测试）。
   - 依赖：C3 定义的准入/计量接口与 D0b，可先用该接口假实现，与 G1a/G1c 并行；验收 §3.6 转发/取消/错误项、一次上游提交、响应前后错误语义、有界背压，实际 DSH 客户端兼容留给集成任务。
6. **`auriga-M3-G1c`（预算、用量与 outbox 恢复）**
   - 只能改：`infra/ai-gateway/internal/budget/**`、`internal/usage/**`、`internal/persistence/**`、`internal/events/**`、`migrations/**`（均相对 `infra/ai-gateway/`）。G1a 只能通过 C3 冻结的仓储端口调用，不改这几棵树。
   - 依赖：C3/D3、D0b；持久化接口先冻结，迁移还集中负责 G1a 的供应商/模型/key 元数据表，G1a 不另建迁移目录。G1a 可并行写接口 fake 测试，数据库验收等待本任务。验收嵌入式 PostgreSQL 的真实角色/RLS、双 gateway 并发不超卖、主体并发与预占比例/绝对上限、幂等唯一结算、全部崩溃点、unknown 保守处理和 NATS 断连 outbox。没有真实供应商账单，不把假数据写成付费计量已验证。
7. **`auriga-M3-R1a`（本地对象存储）**
   - 只能改：`services/resource/impl/src/main/java/auriga/resource/objectstore/**` 与 `src/test/java/auriga/resource/objectstore/**`（后项相对同一 impl 模块）。
   - 依赖：C3 的 ObjectStore/归档边界与 D0b；先提供唯一的 ObjectStore 端口声明供 R1b 使用，R1b 不另写同名接口。验收暂存/提交/读回摘要、限额/路径/磁盘满/并发与孤儿回收测试。对象存储不决策 ACL、审核和节点分发，不增加 S3 SDK/云账户。
8. **`auriga-M3-R1b`（资源版本、审核、发布与分发意图）**
   - 只能改：`services/resource/api/src/**`；`services/resource/impl/src/main/java/auriga/resource/{web,grpc,app,domain,persistence,events,config}/**`；该 impl 的 `src/main/resources/db/resource/**`、`src/main/resources/messages/**`；`src/test/java/auriga/resource/workflow/**`。
   - 上述花括号是明确列举子目录，不包含 R1a 的 objectstore，也不包含根 `ResourceModuleConfiguration.java`。若要调整测试包布局先由主控细化白名单。
   - 依赖：C3、D0b，存储接口可用 fake 与 R1a 并行，集成等 R1a；验收 §4.6 的资源/ACL/等级/审签发布、有效期与签名撤销清单、节点下载授权和分发意图事务。只记录意图，不能写 workspace 表或改 agent。

### 6.4 顺序接线与集成

9. **`auriga-M3-WR1`（workspace 绑定、规格与分发状态）**
   - 只能改：`services/workspace/**`。
   - 派发门槛：等 W2H、W3 均合并后才派。依赖 C3、D0b、I3a 与 G1a/R1b 提供的已冻结服务接口；门槛通过后可先写接口 fake 测试。验收实例 AI 签发/轮换/删除撤销、受信实例通道绑定、资源解析锁定版本、单调 revision/generation、签名撤销清单 ApplySpec 下发、重复乱序事件、本人分发隔离、GetResourceApplyStatus。不是收到请求便返回已安装。
10. **`auriga-M3-AR1`（agent 制品信任与 renderer 接入）**

- 只能改：`infra/agent/**`；一个负责人管理 artifacts/renderer/reconciler/信任公钥与凭据下载接线，避免多个任务交叉修改。
- 派发门槛：等 W2H、W3 均合并后才派。依赖 C3 的签名/下载/撤销、D0b、WR1 的规格；验收 Java→Go envelope/撤销清单验签向量、有效期/清单 revision、节点下载身份、缓存篡改/越权/恶意归档、反回滚、只读引用、故障回退、AI token 轮换触发重载与受认证实例通道，假驱动下 observed+ready。真实 DSH 冒烟不替代 W3，不能改其脚本。

11. **`auriga-M3-E3`（edge 新路由与源隔离）**
    - 只能改：`infra/edge/**`。
    - 派发门槛：等 W2H、W3 均合并后才派。依赖 C3、D0b 与 G1/R1 端点；验收 `/api/ai/v1`、`/api/resource/v1`、`/ai/v1` 的精确路由/凭据策略、流式取消、管理源限制、不接收实例来源的门户凭据；托管调用只认受信内部通道与 mTLS edge 标记，公网拒收托管 JWT、客户端伪造头无效，不能为了面板放开实例 CORS。
12. **`auriga-M3-Z1`（运行装配与首批集成）**
    - 只能改：`infra/ai-gateway/main.go`、`infra/ai-gateway/main_test.go`、`infra/ai-gateway/internal/bootstrap/**`；`services/resource/impl/src/main/java/auriga/resource/ResourceModuleConfiguration.java`；`services/platform-app/**`、`infra/ctl/**`；`e2e/m3/**`（新目录）、`deploy/m3-fake/**`（新目录）、`docs/dev/m3.md`（新文档）。
    - 派发门槛：等 W2H、W3 均合并后才派。依赖 D0b 前置接线已完成，G1a/b/c、R1a/b、I3a、WR1、AR1、E3 全部独立测试通过。这里只做运行装配、启动配置和集成，不能接管 D0b 的依赖清单/pom/锁文件；发现缺依赖退回主控修订 D0b，不能要求业务工人扩白名单。
    - 验收假模型与测试 plugin 完成授权→预算预占→流式/取消→一次结算、上传→审签→发布→规格→假驱动应用，覆盖跨租户拒绝和故障恢复。全量适用 Java/Go 套件、race、现有 CI 必要检查不得减少测试数；不调用付费服务，不触及真实部署。
    - W3 的 `e2e/**` 白名单包含新 `e2e/m3/**`，因此新目录也必须在 W3 合并后才开发，不能以“新目录”主张可并行。现有 M2 e2e/Compose 文件、CI 工作流仍不在 Z1 白名单；新测试若需工作流登记，由主控在 W3 后另派单一接线任务。`infra/go.work` 与 `infra/go.work.sum` 明确归 D0b，Z1 不修改。

推荐顺序：C3 审查定稿→D0b 批准清单并完成前置依赖接线→I3a 与 G1a/b/c、R1a/b 并行→等待 W2H/W3 均合并→WR1→AR1/E3→Z1。其中 G1b/c 与 R1a 的纯接口测试也须先取得 D0b 构建基线，真实接线只按已批准依赖推进；G2/N1/X1/F1/PANEL 另开后续任务，不混进首批。本文仅列候选依赖供批准，本次不安装依赖。

每个 Auriga PR 合并前均需非作者会话独立审查，意见落 `.agents/runs/<完整TaskId>-review.md`。只要改 `services/iam`、`services/common`、`infra/edge|node|agent|egress` 或 `contracts/`，一律标 **【待审查】**，作者/当前主控均不得自行合并；其余 PR 也不能用作者自查替代独立审查。C2 首轮不提交/推送；第二轮按追加任务说明只提交并推送本分支草案修订，留 PR #33 复审，不合并。

## 7. 用户决定（2026-10-20 已定）与主控正式评审事项

以下 D1–D7 已由用户于 2026-10-20 按推荐决定，各条标注了修正；C3 据此落正式契约，执行中如出现超出本节的新取舍再交用户。

- **D1：托管 AI 主体与失效语义**（用户 2026-10-20 已决定：按本条推荐，ctx=instance，托管调用只经 edge 内部通道）。推荐专用 AI JWT、主人 sub、ptyp=user 与新增 ctx=instance，签名绑定 instanceId/credentialVersion，perms 只取本人有效权限与 `{ai.models.invoke,ai.usage.read}` 的交集；I3a 必修当前 ctx=service 等分支无过滤的风险，G1 数据 ctx 仅 instance/device，portal/admin/service 拒绝。用户已批准新增上下文及各解码器变更、受认证的 edge 内部实例通道、“控制面不可用拒绝团队 AI”、流到期取消与正常轮换重叠的取舍。内部通道不能防止已失陷实例自身滥用；不采用纯 bearer 方案。ait_ 不扩权。
- **D2：模型兼容范围、错误与幂等例外**。推荐首批只覆盖经 DSH 验证的 models/chat-completions，流式重试只返回原执行状态、不存正文重放、无自动上游重试；须批准相对 REST Problem 和创建 POST 幂等约定的局部差异。若要求完整兼容/缓存重放/故障切换，必须扩大契约、保留数据与隐私评审，不能只换接口名。
- **D3：硬预算与不确定用量**。推荐 UTC 周期、单一币种、准入价格快照、保守预占、unknown 冻结、配额变化影响新准入；模型额度 0=禁止须正式补定义。还需批准每 `(tid,sub)` 并发流数和在途预占比例/绝对上限（§3.5 初议 2 流、25%，不限额度必须有绝对预占上限），由同一数据库原子执行，区分 rate_limited 与 budget_exceeded。需确认是否接受取消/断线暂不退额度和长流到期取消；若要求即时新配额、自动账单对账、多币种或租户/团队共享总预算，先定义版本/预占/冲正接口，不新建外部计费服务。
- **D4：资源签名与撤销/降级**（用户 2026-10-20 已决定：按本条推荐，离线节点撤销最迟 24 小时生效）。推荐 §4.1 的新 envelope 签名覆盖 tenantId/packageId/versionId/notAfter 与完整制品 statement，独立发行签名 key、公钥信任配置及独立授权签名的撤销清单；清单带 tenantId/revision/issuedAt/expiresAt/revokedKids/revokedArtifacts，经 workspace 的新增 ApplySpec 字段下发，agent 验签并持久化 revision 防回滚。过期 envelope/清单拒绝新安装/启用，普通下架与恶意强制停用分开，只允许显式审计降级；skill/template 不复用 plugin purpose。已定：离线撤销最迟 24 小时；有效期、恶意包停用时是否重启运行中实例、skill/模板规格扩展和资源包审查强度按本条推荐，由 C3 细化。它涉及 spec 与信任链大改，不能自行合并。
- **D5：内网模型与出网边界**。默认维持 egress 内网拒绝；真实私有模型若必须用内网地址，需明确仅 gateway 的受信上游端点例外、配置权限和 DNS/IP 固定策略，不能扩大实例直连白名单。第一批假 Transport 不需要申请真实例外。
- **D6：托管团队面板的成员 API 桥接**。推荐先保留门户源上的账户/敏感操作入口，实例里的团队面板只接经过单独批准的最小、只读能力；完整面板可选受隔离 iframe + 校验 origin 的消息协议，或限路径/动作/主体的代理能力。两种都需要新契约与威胁评审；不能用通用“同源会话代理”给实例全部成员 API。若要恢复实例与门户同源，将偏离 ADR 0020，须用户决定。
- **D7：N1 自动贡献与外部服务费用**。工作知识提炼、自动贡献的启用/退出与内容范围另行确认，个人记忆不自动收集；真实模型、S3、KMS、扫描云服务等全部未授权、未接入。现有 PostgreSQL/NATS/本地盘足够首批假上游验证，不能为便捷引入 Redis、向量库、新消息总线或订阅费用。

主控可直接评审的常规细化包括：最终权限命名/角色映射、状态机字段、错误 HTTP/gRPC 对照、事件目录/ACL、上传大小与保留期、对象存储端口、测试白名单、TaskId 与 C3 生成位置。若这些细化改变已定架构或暴露新的安全能力，再升级为用户决策。本文不承诺日期、不宣布 M3 实现开工，也不改变 W2V/W2H/W3 的验收状态。
