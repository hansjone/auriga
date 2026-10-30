# Team MCP 远程入口与持久通信

`collab` 提供持久通信与内部 RPC；`ai-gateway --team-config <file>` 提供独立远程
Streamable HTTP `/ai/mcp` 与受信 Node 私有操作。当前入口只接受在线核验的设备身份；
托管实例与独立系统总部身份尚未开放。两个真实 DSH 的业务收发验收需由整合工位单独完成。
总部是独立 DSH 系统；其系统身份、管理员入口另行确定，不能把管理员当成所有成员。
不创建业务 Task、任务模板、调度器或 AgentBus 工作流。

## 契约与准入

接口为 `contracts/proto/auriga/collab/v1/collab.proto` 的九个 RPC，工具参数形状见
`contracts/plugin/team-mcp.schema.json`。远程路径、协议版本与私有正文见
`contracts/plugin/team-transport.schema.json`；网关从模型 schema 原件加载工具参数。
注册、绑定、拉取及 ACK 为受信 Node 私有操作，不能暴露给模型或浏览器。

只接受内部 `svc:ai-gateway`，经 common 内部 JWT 校验、受认证传输和授权代租户，
RPC 的 `actor.tenant_id` 必须等于实际委托租户。跨主机需要现有 common mTLS；
无 TLS 时 common 服务只允许回环监听，测试使用随机回环端口。
内部服务身份不自动授予其声明的 Actor 权限。

`CollabAdmission.verify(actor,operation)` 必须在线校验凭据状态、凭据主人、有效成员及动作授权。
发送时另对收件端点执行 `RECEIVE` 准入。生产 `OnlineCollabAdmission` 每次经
IAM `VerifyDeviceCredential` 核设备、主人和 backing session，再经 org
`GetEffectivePermissions` 核 ACTIVE 与 `collab.messages.use`；默认未配置仍
`common.unavailable`。数据库端点存在或 JWT 尚未过期均不能替代。
测试只替换外部控制面，不替数据库、RLS 或 JWT/gRPC 认证。
不使用 `admin` 角色替其他人建立端点、会话或 ACK。

会话来源遵循 ADR 0016：受信插件读取 `exec.agent.session.id`，每次请求构造三个既有
`auriga/sessionId`、`auriga/toolCallId`、`auriga/rootCallId` 元数据。
meta 不是身份凭据；平台校验端点、会话绑定与线程参与者。
模型参数 schema 拒绝传入 Actor、tenant、源 session 及 `_meta`。
Node 进程和其凭据是信任边界，不增加会话 capability/nonce 协议。

## 数据、次序与重试

迁移位于 `services/collab/impl/src/main/resources/db/collab`，用 collab schema，先跑
common 迁移，再跑服务 Flyway。表为 endpoint、session_binding、thread、thread_participant、
message、delivery、request_dedup；均带 tenant_id、RLS 与包含 tenant 的外键。
迁移角色拥有表；运行角色只有 DML、无 owner/BYPASSRLS 权限。

来源端点绑定到一个设备或实例凭据主体，主人不可原地更换。会话 ID 为不透明字符串。
源端点创建线程时固定源会话参与者；目标端点在真实创建/恢复 DSH 会话后绑定该线程，
一旦绑定不能静默换会话。同一账号的其他端点或同一端点的其他会话不自动取得线程权限。

Open/Reply 先以不加写锁的查询核来源端点主人/凭据、会话绑定及必要参与关系，
拒绝请求不得先锁其他参与者端点；预检也不能改成源端点优先加锁。
随后把来源与参与端点全部按 UUID 排序加锁，再核端点状态/参与绑定与在线准入，
串行化发送、领取、ACK 与撤销。线程行锁分配单调 sequence；
消息和对方的 delivery 同事务写入。按 sequence 读取，不按本机时间推断顺序。
幂等作用域 `(tenant,source endpoint,source binding,operation,key)`；同键同体返回原消息，
异体 `common.conflict`，未成功事务不留下幂等占位。
本批不清理幂等记录，覆盖全部已保留消息的重试；后续保留策略不能先删除去重记录再接受旧键。

每次 pull 1—100 项，只领取每线程最早尚未确认的消息，活跃租约阻止越过该消息。
租约固定 30 秒；过期重领更换 token，旧 token 不能确认新租约。
ACK 只允许精确接收端点及已绑定会话，要求当前 token、未过期租约。
宿主 prompt 的 `requestId` 必须使用服务端 `delivery.id` 的规范小写 UUID 字符串，
ACK 的 `host_request_id` 必须逐字相等；不匹配时服务端返回 `common.conflict`，不推进投递。
不同 delivery 因而不能复用同一个宿主请求身份，包括同会话跨线程的投递。
同一 delivery 的所有宿主重试、重领租约仍用同一 requestId；相同合法 token/requestId
的成功 ACK 可幂等重试，旧租约仍被拒绝。不得每次随机生成或沿用上一条消息的 requestId。
状态 pending/leased/host_accepted 只表示平台待投、租约或宿主接受，不表示模型已读或完成。
端点离线不伪造 delivered；本批没有心跳在线状态声明，也不启动后台定时器。

本地端点撤销提交后的新 send/pull/ack 被拒。IAM 设备与成员实时撤销由生产
CollabAdmission 在线重查；控制面断网时拒绝。实例身份仍未开放。
已被宿主接受的副作用不能撤回。积压保留/配额运营策略未交付，外部服务未开放。

## 装配与当前限制

`CollabRuntimeConfiguration` 只在 `auriga.service.schema=collab` 且模块启用时装配，
使用现有 DataSource/TenantTransactionManager，注册公共内部 gRPC 服务 bean。
配置 Flyway schema/location 为 collab 与 `classpath:db/collab`。
默认 combined 配置仍 collab=false。明确组合 `combined,combined-collab` profile 才开启
独立 collab 数据源；迁移用户名/密码与运行用户名/密码必须分开，运行角色不得为 owner、
superuser 或 BYPASSRLS。`PlatformCollabConfiguration` 先 common 迁移再 collab 迁移。
缺省 admission 不可替换为 dev-active/skip-auth；新生产实现与保护路径需独立审查。

现有 `SessionService.IntrospectSession` 输入浏览器 cookie，只允许 edge，不是设备 JWT introspect。
`InstanceAiBindingService.GetInstanceAiBinding` 可供未来 gateway 在线核托管实例，但现 instance
权限限模型调用，内部精确路由表也没有 MCP，不得借此直接开放 Team。
`ait_` 心跳令牌不能调用 Team。设备刷新令牌始终仅 Node 持有，不进入模型、浏览器或日志。

## 已核宿主接缝与后续验收

固定 DSH rc.2 源码 HEAD `639ed015397290b3745d163aafe02ffee4aa3f84`：
`packages/api/session-controller/src/index.ts` 的 create(request)、prompt(request,signal)；
types.ts 的请求为 `{requestId,sessionId,mode:'queue',content:[{type:'text',text}]}`，
返回 `{accepted:true}`。commands.ts 用 source.rpcId 在 inbox 与历史事件去重，queue 走 followup。
插件应持久保存 deliveryId→sessionId，并始终令 requestId=deliveryId，串行化同一 delivery
的宿主准入；固定该身份重试，accepted 后再 ACK。不能把自然语言输出自动当线程 reply。

后续真实闭环必须有独立总部与成员的真实 DSH：源工具开聊、指定端点真实入队、成员工具
回复、总部原线程收到，覆盖断网、重连、ACK 丢失和进程恢复；本批内部核心测试不冒充该验收。

## 检查

在 Windows 每次 shell 先点源工作区 `.agents/auriga-env.ps1`。

```powershell
.\mvnw.cmd -pl services/collab/impl -am verify '-Dtest=CollabPostgresTest,CollabGrpcTest' '-Dsurefire.failIfNoSpecifiedTests=false'
node --test contracts/test/team-mcp.test.mjs
```

契约检查还包含 Buf lint/兼容性和 Go 生成一致性；新增 schema 测试已接入现 check-schemas 显式列表。
涉及 contracts 的 PR 标【待审查】，所有 PR 必须由非作者独立审查，作者不自行合并。

## 远程服务与正式身份接线

网关不启动每客户端 stdio，不依赖网关内存中的绑定映射。协议为 2025-06-18 的无状态
Streamable HTTP：POST JSON 请求带 `Accept: application/json, text/event-stream`；
initialize、ping、tools/list、tools/call；notification 接受后 202 空体；GET 405。
后续 `MCP-Protocol-Version` 若提供必须为 2025-06-18；不返回 MCP Session ID。
Node 请求不带 Origin；所有带 Origin 的浏览器请求拒绝。协议参考
[官方 transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports) 与
[tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)。

公开工具只有 `auriga_list_endpoints`、`auriga_open_thread`、`auriga_reply_thread`、
`auriga_read_thread`；输入分别沿用四个既有模型 schema。成功返回 `structuredContent`
和同体 JSON text，都是 collab 对应 RPC 的 protojson 响应封套，uint64 sequence 为字符串。
业务错误以 `isError: true` 和契约 common 错误码文本返回；不泄漏后端异常或令牌。

每条请求携 `Authorization: Bearer <current device access JWT>`，网关每次调用 IAM
`DeviceAccessService.VerifyDeviceAccess({access_token})`。只有 svc:ai-gateway 可调用；
未验证 tid 仅用于内部租户委托路由，必须与 IAM 返回的 tenant/account/device 逐字一致后
才构造 Actor。active=false、到期、返回身份不一致均拒绝；IAM/控制面故障不使用旧结果。
再调用 org 核 ACTIVE 和 `collab.messages.use`。模型、portal/admin、instance、ait_ 均不在
该设备入口授权范围。设备 scope 与 `auriga.connect.collab.use` 的裁剪由 IAM 权威端执行。

MCP 调用的 `Auriga-Endpoint-Id`、`Auriga-Binding-Id` 由 Node 从自己持久保存的私有
register/bind 结果构造；三个 ADR0016 _meta 来自本次执行上下文。引用头和 _meta 都不是
身份，collab 仍在线校验归属、真实绑定和线程参与者。不得把这些字段加入模型 arguments。

Node 私有 POST `/ai/team/node/register|bind|pull|ack|revoke` 的严格正文及路由见传输契约；
响应分别为 RegisterEndpointResponse、BindSessionResponse、PullDeliveriesResponse、
AckDeliveryResponse、RevokeEndpointResponse。pull 不要求宿主会话，因为第一条投递可能
尚未创建真实宿主会话；接收 Node 创建/恢复真实会话并 bind 后，才可按同 deliveryId prompt 和 ACK。
ACK 的 hostRequestId 必须等于该 delivery 的规范小写 UUID，不得随机改 requestId。

网关 JSON 配置示例（所有令牌文件须由安装程序按本服务身份签发并持续轮换）：

```json
{
  "listen": "127.0.0.1:18090",
  "schemaFile": "contracts/plugin/team-mcp.schema.json",
  "iam": { "target": "127.0.0.1:19090", "serviceTokenFile": "/run/auriga/ai-gateway.jwt" },
  "org": { "target": "127.0.0.1:19090", "serviceTokenFile": "/run/auriga/ai-gateway.jwt" },
  "collab": { "target": "127.0.0.1:19090", "serviceTokenFile": "/run/auriga/ai-gateway.jwt" }
}
```

非回环网关 listener 必须配置 tlsCertFile/tlsKeyFile；非回环 RPC 必须配置该 hop 的
certFile/keyFile/caFile，可选 serverName。无 TLS RPC 只准数字 loopback，不能用 DNS
名称冒充回环。每次 RPC 重新读取服务 JWT 文件，轮换不需重启；不签自造内部 token。

collab 专属配置 `auriga.collab.control-plane.enabled=true`，并提供 iam-target、org-target、
service-token-file，后者必须是 svc:collab 的正式 JWT，不共用 gateway 凭据。
跨机还需 tls-cert-chain/tls-private-key/tls-trust-cert-collection。配置创建专属
AurigaClientInterceptor；不覆盖合并进程的全局 ServiceTokenProvider。IAM 和 org 的
common gRPC `tenant-delegates` 必须允许各自的正式调用方；collab 服务只接受 ai-gateway
的内部调用，actor tenant 必须与委托一致。启用但缺配置时启动失败；文件缺失或过期、
权威端不可用时请求失败关闭。服务 token 首发/轮换及 edge 精确路径部署由安装接线负责，
此实现没有静态 token allowlist 或生产准入替身。

额外验证：`go test ./...`（infra/ai-gateway）及
`node --test contracts/test/team-transport.test.mjs`。指定当前已安装 DSH 可执行文件
`AURIGA_TEST_DSH_EXE` 与 ASAR 内 `AURIGA_TEST_DSH_MCP_MODULE` 时，Go 的专属集成测试
真正加载当前客户端的 MCP 模块，随机 HTTP/gRPC 端口握手、列四工具并传 ADR0016 调用；
默认缺该环境会具名跳过，不能把跳过当兼容通过。该测试不使用或改动用户 profile，
也不等于两个真实账号 DSH 的完整收发验收。


## same-host combined 的持续内部令牌文件导出

正式 POSIX 安装可显式开启 `auriga.platform.team-token-export.enabled=true`，同时必须
`auriga.platform.combined=true`、IAM 模块启用。默认不启用，不创建文件或定时任务。
`PlatformTeamTokenExportConfiguration` 用两个固定绑定 adapter 复用未改动的
`EdgeTokenFilePublisher` 安全路径与生命周期；publisher 内部的 `edge` 回调被固定映射为
正式 `InternalTokenIssuer.issue("ai-gateway", secret)` 或 `issue("collab", secret)`。
签出的主体分别是 svc:ai-gateway 和 svc:collab，绝不会把 svc:edge token 交给 Team。
外部请求不能选择 service，没有新增网络签票 API。

安装必须分别配置 `auriga.iam.internal-secret-hashes.ai-gateway` 与 `.collab`，为两个
独立安装 secret 的不同 SHA-256（十六进制小写）摘要；这些是原 TokenService 已有配置，
不是新增凭据类型。secret 由安装端随机生成，匹配原成熟 publisher 的 43 字符 URL-safe
格式。不能共用 secret 路径、摘要、token 路径、token 目录或 reader group。
正式 secret 校验仍由 TokenService 完成；不在安全目录/属主验证前偷读文件。

每个固定服务各有以下安装配置，前缀为
`auriga.platform.team-token-export.ai-gateway.` 或 `.collab.`：

- `installation-secret-file`：绝对、规范化 secret 文件路径；父目录 0700，文件 0600。
- `token-file`：独立私有导出目录内的绝对文件路径；目录由成熟 publisher 校验/创建为
  0750，文件 0640。目录及文件不能为软链接或多重硬链接，属主/组必须匹配。
- `reader-group`：各自固定服务进程的数字组 ID，两组必须不同；不要给 gateway 读取
  collab 令牌的组权限。collab 同进程可由文件属主读取。
- `installation-owner`：可选安装属主数字 UID，默认 -1 沿原 publisher 当前进程属主规则。

这两个文件分别接网关各 hop 的 serviceTokenFile 与 collab control-plane.service-token-file。
启动即尝试正式签发，随后每 60 秒分别重新读取各 secret、正式签发 5 分钟内部 JWT，
经原安全目录流原子替换；消费者每 RPC 重读文件。secret 不匹配、签发或文件写入失败
不延长旧 JWT 的到期时间，消费者最终失效关闭。组合启动第二个 publisher 失败会关闭
第一个；停机与重复关闭均停止定时任务并按原安全检查删除本服务文件。

该安装导出只支持具有 POSIX 属性和 SecureDirectoryStream 的正式文件系统。Windows
无 POSIX 时明确拒绝启动；当前 Windows 本机 QA 不能声称发行导出已支持，应由仅用于
测试的正式安装 issuer fixture 持续签发独立服务票据。本轮 Windows 专属测试验证了
真实 issuer 的不同 subject、凭据隔离、JWT 轮换到期及安装配置拒绝；POSIX 文件发布
与失败分支保留在同一专属测试中供 Linux CI 执行，不能把 Windows 拒绝算文件发布通过。
