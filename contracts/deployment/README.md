# 通用部署的域名与纯 IP 入口契约

状态：IP0 的配置/接口设计；IP1–IP3 尚须实现及验收。`access.schema.json` 是安装者受信输入的正式结构，
不是现有 edge、IAM、agent 已能读取它的声明。NAS 只是公众部署流程的一个真实验收环境；公开安装不能依赖现场手改、
私有地址、hosts 文件、伪 DNS 或额外 IP。ADR0022 补充 ADR0020/0021，既有内部 JWT、mTLS、实例门锁保持原约束。

## 配置与启动语义

`fixtures/ip-http.json`：单 IPv4，门户/实例一个端口、管理台另一个端口；
`fixtures/domain-proxy.json`：门户/管理台/每实例分源，受信前置代理到 edge 的端口可与浏览器外部端口不同。
`fixtures/domain-root-portal.json`、`fixtures/domain-nested-parent.json`补充合法嵌套域名正例：
门户`example.com`与实例父域`i.example.com`，以及门户`dsh.example.com`与实例父域`i.dsh.example.com`。
示例不能直接视为安装步骤。公网 IPv4/IPv6、多个租户共用一个 IP、IPv6 literal 及每实例公网端口不在首批 IP 模式范围。
`tenantId` 是本部署固定单租户绑定，必须与 IAM/org 实际租户一致；未知 authority 不映射到默认租户。

Origin 采用 `{scheme, host, port}`，避免把 userinfo/路径/查询混入配置。port 永远是有效整数端口；
序列化 `http:80`、`https:443` 时省略端口，其他端口保留，例如 `http://192.0.2.10:8080`。
issuer 序列化为门户 Origin，不加尾斜杠，不随请求或管理入口改变。现有部署若已使用其他 issuer，应显式迁移，
不能在请求间同时猜测多个 issuer。访问令牌 `tid`、ctx、aud 规则不变；入口的 portal/admin audience 不是客户端可选字段。

Ajv 校结构、IPv4/DNS 格式、枚举、类型、范围、mode 与实例模式配对、保留字段。**启动验证器还必须**：

1. 解析并规范化 host：IPv4 只接受四段十进制规范写法，不接受整数、十六进制、八进制、前导零、尾点或 IPv4-mapped IPv6；
   DNS 使用已规范化小写 ASCII A-label，各标签合规；拒绝原始 Unicode、尾点、空标签、wildcard、userinfo、百分号编码和反斜杠。
   配置先拒绝非规范值，不用宽容 URL parser 把歧义输入悄悄修成可信入口。
2. `issuerOrigin` 必须逐字段等于 `portalOrigin`。IP 模式 portal/admin 的规范 IPv4 与 scheme 相同，port 必须不同；
   domain 模式两个固定 host 不同；沿用现edge的实例保留命名空间，固定门户或管理 host 等于parentDomain、
   或以`.`加parentDomain为后缀时拒绝，防止占用保留根/子域或被`<实例id>.<parentDomain>`实例Host路由捕获。
   现有单标签匹配规则也会捕获非UUID标签，不能只因固定 host 的前缀不是UUID就将这类路由碰撞放行。
   实例 scheme/port 必须等于门户外部 scheme/port，实例只挂 portal listener。
   **不能双向检查任意域名后缀就拒绝**：parentDomain位于门户域名之下是正常配置，如上述两组正例；
   共享站点或父子域不等于同Origin，仍按完整scheme/host/有效port隔离、校验Origin和host-only cookie。
   上述保留命名空间检查只按固定host→parentDomain方向执行；nonce cookie的覆盖范围仍须单独正确生成/验证，不能以此重新禁止所有合法嵌套。
3. 三种 listener 角色有独立本地 socket；地址含通配绑定时也不能出现端口冲突或被另一角色匹配。
   direct 时 portal/admin listener 的 TLS 与相应 scheme 一致；允许 NAT 端口映射，所以内外端口不必相同。
   trusted_proxy 时 portal/admin listener 必须 TLS+mTLS，实际 TCP 对端在规范 CIDR 白名单且证书匹配安装的代理身份。
   CIDR 需要真实解析、检查网络边界、排除 /0，不用字符串 pattern 代替；证书文件沿用既有 PKI 配置，不在此存秘密。
4. 出现任何浏览器 HTTP Origin（含 domain 实例）必须显式 `insecureHttpAcknowledged=true`；HTTPS 不因为该值开启降级。
   不从域名故障、TLS 失败或请求头自动切换到 IP/HTTP；访问未配置 scheme 失败，不先携带凭据重定向。
5. `instanceControl.origin.host` 必须等于 `dnsAlias`，端口/TLS 与 instanceControl listener 一致。
   alias 是 Docker 内置 DNS 名，必须是规范 DNS、不能是 IP；不能与任一浏览器 host/实例 parent 冲突。
   edgeContainer 必须对应安装时固定受信 edge 容器身份，再按现有标签、网络归属和 alias 复查，不能只信容器名字。
   runtime 现有“Docker alias 不得为 IP”的校验保留。内部 URL 由这份配置序列化，不能复用 browser Origin。
6. 安装生成的浏览器 allowedOrigin/trustedHosts 仅含相应门户/实例的精确 scheme、host、有效端口。
   IP 模式不允许无端口 IP 表示任意端口，不含管理端口、内部 alias 或通配符；实例 profile、心跳和请求不能覆盖。
   domain 模式继续按已验证实例 ID 派生子域。现插件配置尚缺 scheme 绑定接线，IP2 必须先补契约/实现，不能删 fence。

这些跨字段/URL/CIDR规则是启动失败条件。IP1a 已提供下述 Go 生产加载器及实际负例回归；
edge/IAM/agent 尚未装配消费，不能把配置加载成功当作浏览器或服务入口已实现。
内部 API 的错误/权限沿用 common/IAM/workspace 单一来源；无效配置在启动前报具体字段并退出，不发明运行时权限键。

### 生成与 Go 加载入口（IP1a）

仓库根执行 `./mvnw -Pgo-openapi-models -pl services/contracts -am generate-sources`（Windows 用 `mvnw.cmd`），
再执行 `gofmt -w infra/gen/aiapi infra/gen/resourceapi infra/gen/deployment`。
固定 OpenAPI Generator 7.26.0 生成 `infra/gen/deployment` 的 Go 类型；
生成投影将 `$defs`/`const` 转成 OpenAPI 模型和带 discriminator 的 union，完整原 schema 另生成副本，
不以类型、union 解码器或投影取代原 schema 验证。手写代码在独立的 `infra/gen/deploymentconfig`，不会被生成清理。

消费 API 为 `deploymentconfig.Load(data) (Config, error)`：拒绝重复 JSON 键（含转义后同名）、
尾随内容和无效 UTF-8，输入上限 1 MiB、嵌套上限64；随后以完整 Draft 2020-12 schema 验证未知字段、
必填/null、精确数值、枚举、union 和 format，再解码生成模型并执行上述配置内跨字段规则。
schema 仅从生成副本编译，format 断言显式开启，文件/网络外部引用禁止加载；不会从安装输入选择另一份 schema。
规范 IPv4/DNS A-label/CIDR 必须预先合规，不宽容修复 host；JSON Schema 合法整数的指数或小数表示会在精确校验后规范化，
tenant UUID 统一小写，Origin 默认端口省略，其余有效端口保留。

`Config` 私有持有生成模型，只有加载成功才 `Valid()==true`；零值无效。
`TenantID`、`Mode`、`PortalOrigin`/`AdminOrigin`、`PortalURL`/`AdminURL`、`Issuer`、`Listeners`、
`InstanceControl`、`IngressKind`、`ProxyPeerCIDRs`、`InstanceParentDomain` 按值或副本返回，不能修改已校验身份。
失败返回 `ValidationError`，`Code()` 为既有 `common.validation_failed`，`Field` 为 JSON Pointer，错误文本不回显配置值。

从 `infra/gen` 执行 `go test ./deploymentconfig`，实际覆盖四份原始正例、32项结构负例、12项启动语义负例及歧义/冲突回归；
12项启动负例须先通过完整结构验证，再由相应语义字段报错。合法父子域正例仍须加载成功。
本地加载不证明 IAM 实际租户存在、代理证书身份/TCP 对端匹配、edge 容器身份及网络归属/成员，
这些安装装配与运行时核验仍须由后续消费者完成；尚未接 HTTP listener、会话或 CSRF，不能据此宣称纯 IP 浏览器可用。

### Java 生成与生产加载入口（JC1）

普通 Maven 构建自动从同一 `access.schema.json` 经既有投影、固定 OpenAPI Generator 7.26.0
生成 `auriga.contracts.deploymentconfig.model` 类型，输出只在 `services/contracts/target/generated-sources`。
Java 投影步骤不清理或改写 Go 文件；显式 `go-openapi-models` 的重生流程保持原样。
完整原 schema 按原字节单份打包为 JAR 资源 `auriga/contracts/deployment/access.schema.json`，
既有 `auriga/contracts/catalog/m3.json` 保留。投影、生成类型及模型解码均不代替原 schema 验证。

生产入口 `auriga.contracts.deploymentconfig.DeploymentAccess.load(byte[])` 返回只有成功加载才能创建的已验配置：
先拒绝超过 1 MiB、超过 64 层、无效 UTF-8、重复键（含转义同名）和尾随内容，再以
networknt 3.0.8 完整 Draft 2020-12 原 schema、显式 format 断言及精确十进制数值验证，
禁止任何外部文件/网络 `$ref`；最后才规范合法整数表示、解码生成模型并执行与 Go 同等的启动语义。
ICU4J 78.3 的 Unicode 17 UTS46 非转换处理启用 STD3、Bidi 与 ContextJ 检查，
仅接受已经规范化的 ASCII A-label，不把宽容修复的结果当配置；保留合法嵌套父子域及有向实例命名空间规则。

`tenantId()`、`mode()`、`portalUrl()`、`adminUrl()`、`issuer()`、`ingressKind()`、
`instanceParentDomain()` 返回字符串；`portalOrigin()`/`adminOrigin()`、`listeners()`、
`instanceControl()` 返回相应生成类型的深副本；`proxyPeerCidrs()` 返回不可变字符串列表。
消费者不能通过修改返回模型或列表破坏已验配置。失败抛 `ValidationException`，
`code()` 固定为 `common.validation_failed`、`field()` 为 JSON Pointer，错误文本不回显配置值。
加载器不依赖 common，也不接受客户端指定 tenant、audience 或另一份 schema。

`mvnw.cmd -B -pl services/contracts -am verify` 包含原四份正例、32 项结构负例、
12 项先通过完整结构校验的启动语义负例，以及精确数值、格式、IDNA、外部引用和副本隔离回归。
与 Go 相同，加载成功不证明租户存在、代理身份或 Docker 网络归属；这些仍由后续装配与运行时核验完成。

## authority、Origin 与代理信任

处理顺序固定：本地 listener 角色 → 受信传输/TCP 对端 → 单一实际 authority → 安装映射 tenant/audience → 会话/Origin/权限。
HTTP/1 只能有一个合法 Host，HTTP/2 只能有一个 `:authority`，若另带 Host 必须规范后完全一致；空、多个、逗号拼接、
userinfo、空端口、超范围/非十进制端口、百分号、空白/控制字符、编码分隔符、尾点和 IP 非规范写法拒绝。
默认端口缺省与显式 `:80/:443` 等价；host 大小写按 DNS 规则规范化，IP 不进行名称解析或反查。

请求实际 scheme 来自 direct 的 TLS 状态或已认证代理连接的**固定安装映射**，不是任意 Forwarded 值。
代理必须保留外部 authority、按两种角色分别投递正确 listener；同一 IP 的两个入口不能用一条丢端口规则合并。
edge 丢弃外部 `Forwarded`、`X-Forwarded-*`、audience/tenant/身份等自报头，即使来自受信代理也不以其覆盖安装映射；
edge→IAM 通过既有受信内部 hop 传递经校验的完整入口信息，IAM用同一配置复核，不能只读 Servlet ServerName。
不能把内部 alias 加入门户 Host 别名列表；同一 Host 发到另一个 listener 仍拒绝。

浏览器 cookie 写请求（POST/PUT/PATCH/DELETE）和 WS 握手都验证完整 Origin 的 scheme+规范 host+有效端口：
管理入口只能是 admin Origin；门户只能是 portal Origin；domain 实例只能是该实例 Origin。
IP 实例与门户同 Origin，属于已接受的弱隔离。Origin 必须是单个合法 serialized origin；拒绝缺失、`null`、
多个值、带路径/用户名/查询/片段、错 scheme/端口。Cookie 写请求不会因为任意 Authorization:Bearer、
Sec-Fetch-Site=same-site 或 SameSite cookie 被免检；标准外部服务 Bearer 调用走既有独立鉴权路由，不能混成浏览器 bootstrap。
普通同源 GET fetch 可能没有 Origin，所以不以 GET 作为管理 CSRF 恢复接口。敏感管理 GET 仍不返回 CSRF明文，且不开凭据 CORS。

| 输入/操作 | 规范结果（IP1 必须实现并用具体码验证） |
|---|---|
| 正确 listener + authority + scheme | 仅映射配置 tenant/audience；仍做身份、权限和到期检查 |
| 无效/多个 Host、端口语法或编码歧义 | 400 `common.validation_failed`，不签会话/issuer |
| 合法但未配置 authority、错误 listener/scheme/端口 | 403 `common.forbidden`，不降级到默认租户 |
| 管理 cookie + portal/instance Origin/端口 | 403 `common.forbidden`，不会变成管理 audience |
| cookie写/WS缺失、null、多Origin | 403 `common.forbidden`；下述逐条协议例外除外 |
| 任意伪造 Forwarded/audience/tenant 头 | 丢弃；实际 authority 不合法仍按上列拒绝 |
| 无/坏/吊销会话 | 401 `common.unauthenticated`，不回显CSRF |
| 错误/到期CSRF而会话有效 | 403 `common.forbidden`，不能退回旧cookie值 |
| IAM权威校验不可用 | 503 `common.unavailable`，不使用KV旧正向状态 |

表中 `common.validation_failed` 适用于已进入应用的合法 HTTP 请求所做的 authority 校验。
HTTP 解析器在进入应用前拒绝的非法报文（如重复 Host 或非法字节）返回协议层 400，
正文不保证为 Problem；这些报文仍不得转发上游或签发身份。

登录例外是精确白名单，不是“`/login/**`都免检”：

- `POST /api/iam/v1/login/transactions` 的 purpose=login 尚无会话CSRF，但仍要求对应入口的完整同源Origin、
  JSON、限流并建立audience/tenant/Origin/随机发起端cookie绑定；link/stepup已登录流程仍校该会话CSRF。
- `POST .../login/transactions/{txId}/advance` 与 `DELETE .../login/transactions/{txId}`：
  未登录purpose=login以匹配事务cookie、txId、完整同源Origin、事务tenant/audience/状态/5分钟期限验证；
  link/stepup还要原会话及CSRF。GET查询/SSE需要事务cookie与入口绑定，可无Origin但无跨源CORS。
- `GET /api/iam/v1/login/callback/{providerId}` 只接受已登记provider固定回调；外部顶层GET可缺Origin，
  仍需事务cookie、一次性state/nonce/PKCE和tenant/audience/规范回调Origin。returnTo只能是该入口已验证的本地路由。
- 同路径 `POST` 仅provider已登记的表单回调，校协议state/签名等，只暂存最多5分钟的一次性resume并303到相同规范入口的GET；
  不在POST签发会话。最终GET仍需原事务cookie。`resume`不应可改tenant/audience/Origin。
- 新管理CSRF登记仅下面一个精确POST。OAuth/OIDC其他标准端点沿用各自固定协议客户端/state/redirect校验，
  必须逐操作登记测试，不能从上述例外推导整段OAuth或任意Bearer免检。

## 管理 CSRF：有限的每 tab 令牌

正式操作在 `openapi/iam.yaml`：`POST /api/iam/v1/admin/session/csrf-tokens`，只对IP模式启用。
domain模式保留既有各自host上的CSRF cookie；调用本操作返回403，不引入两套并存的管理CSRF验证后门。
这个bootstrap仅校自己**当前**admin浏览器会话，`AuthenticatedOnly+AdminOnly`，无新增RBAC键。
管理员角色权限仍由普通业务写操作检查，不因拿到CSRF token而获得管理权限。

1. 浏览器在管理Origin同源 `fetch`，`method:POST`、`credentials:same-origin`、`Content-Type:application/json`、
   `X-Auriga-CSRF-Bootstrap:admin-session-v1`，body为 `{token}`。浏览器自动发送POST的Origin，前端不手填禁止修改的Origin头。
   token用 `crypto.getRandomValues`生成32字节，规范无填充base64url；不可用安全随机源时失败，不回退Math.random。
2. edge校admin listener/authority/完整Origin/当前admin cookie；此精确方法、路径、JSON和固定头组合才免旧CSRF。
   header是公开常量，无保密假设；跨源预检不返回允许CORS，简单表单不符合JSON/头要求。拒绝Bearer代替session和请求指定sid/tid。
   向IAM转发只用受信edge hop换发的ctx=admin、绑定当前sid/tid的JWT；IAM控制器相当于common
   `AuthenticatedOnly+AdminOnly`并在线重查相同权威会话。不能只凭缓存JWT或admin角色就登记。
3. IAM在锁定该会话的事务内复查账号、tenant、admin audience、到期/吊销，清理过期条目后登记hash。
   最多32条，有效期不超过900秒且不超过登记时会话绝对/闲置到期；重复有效hash返回原到期，不占第二条、不延期。
   满额429 `common.rate_limited`，Retry-After取最早有效条目到期的剩余整数秒（最小1），不驱逐其它tab。
4. 200 JSON `{token,expiresAt}` 中token精确回显提交值。客户端负责安全随机，服务端格式检查不能证明其熵。
   只持久hash/到期，不需要从不可逆csrfHash恢复明文，不存加密响应缓存；事务成功后响应，response丢失可带同token重试。
   `Cache-Control:no-store`、`X-Content-Type-Options:nosniff`，仅application/json，无重定向、JSONP、可读cookie、凭据CORS。
5. 每tab/document只内存保存自己的token；刷新、新tab、丢失或到期登记新值，不全局rotate其它tab。
   不进localStorage/sessionStorage、URL、模板、日志、跨源postMessage或KV。旧条目过期回收；反复刷新耗尽槽位时等待429期限，
   不能私自把限制改成无限数组。普通会话续期/访问JWT刷新不延长CSRF条目；15分钟后按需登记新值。
6. 后续IP管理写携带 `X-Auriga-CSRF`，edge每次通过 `SessionService.VerifyAdminSessionCsrf` 在线校验。
   RPC只接受svc:edge内部服务JWT及现有mTLS，接收**实际消费**的admin cookie和token，IAM权威检查后返回active及Session。
   edge比对session id、account id、tenant id、ADMIN audience、会话到期，与本次实际cookie会话不一致拒绝；结果仅本请求使用。
   既有IntrospectSession没有此能力，不能假称读旧csrfHash等价。无效/过期/吊销/非admin会话RPC返回UNAUTHENTICATED（HTTP401），
   有效会话但CSRF不符返回active=false且无Session（HTTP403）；控制面UNAVAILABLE映射503失败关闭。
7. 登记/校验/登出/吊销/续期按同一会话权威状态串行，吊销提交后新校验/登记失败，不能旧touch或KV写复活。
   KV `adminCsrfTokens`只是哈希投影，更新按提交顺序，消费者防倒退；丢投影不能授予权限。
   Session已吊销/过期时清空全部条目并传播失效，关闭绑定连接按原会话机制执行。
   已在吊销提交前授权进入业务处理的请求不声称能回滚；IP3要证明提交后新的管理写拒绝。
8. 401回登录。403仅在用户仍有有效会话且token未知/到期时重新登记；**不自动重发可能已执行的非幂等业务写**。
   429遵Retry-After；503可重试相同bootstrap token。一般业务幂等仍按原Idempotency-Key，token登记只按自身hash自然幂等，
   不适用普通24小时响应正文缓存。管理WS若未来存在，必须先有可携带CSRF的正式握手契约；本次不发明浏览器WS自定义请求头能力。
   当前应对未登记的管理WS路径拒绝，不能只凭cookie/Origin开放写通道。

## Cookie、事务和登出范围

所有session/login cookie均HttpOnly、SameSite=Lax、无Domain、Path=/；HTTPS名字带`__Host-`且Secure，HTTP去掉此前缀和Secure。
`__Host-`不能使用非根Path。cookie不按端口隔离：IP浏览器会把管理session带到门户/实例端口，管理入口才允许消费它。
管理session原文不进入实例上游，不能承诺浏览器从不发送。域名模式仍以独立host实现强分源；HTTP明文没有传输保密承诺。

| 范围 | session基本名 | login事务基本名 | CSRF | 登出清理 |
|---|---|---|---|---|
| 门户（两模式） | auriga_session | auriga_portal_login_tx | 原auriga_csrf cookie+X-Auriga-CSRF+csrfHash | 仅门户session/事务/CSRF；门户sid关联实例失效 |
| domain管理台 | auriga_admin_session | auriga_admin_login_tx | 本host的原auriga_csrf cookie流程 | 仅管理host本会话/事务/CSRF |
| IP管理台 | auriga_admin_session | auriga_admin_login_tx | 内存登记token+在线IAM，无CSRF cookie | 本管理session/事务/全部hash；门户CSRF不动 |

事务绑定固定入口Origin、tenant、audience及随机发起端cookiehash。同audience只允许一个当前发起事务，替换时取消旧事务；
门户和管理台可同时建立，各自完成/取消/登出不清另一入口cookie。事务cookie出错仍404且不泄露是否存在。
link/stepup原session也按相同audience绑定。OAuth等回调即使共享同一cookie host也必须核事务audience，不按先找到的cookie选角色。

## 实例 path 模式与内部控制入口

IP浏览器实例 mount 为 `/i/<规范UUIDv7>/`。`/open/<id>`维持主人/portal session检查，票据60秒一次性并绑定sid、instance、nonce、
目标规范Origin及mount；只允许跳 `/i/<id>/_auriga/ticket?t=...`。兑票成功302到**该实例**`/i/<id>/`。
无尾斜杠根先规范化到有尾斜杠，不接受客户端回跳URL；票据查询不进上游/日志，消费后地址栏清除。
同浏览器A/B用同名但各自Path=`/i/<id>/`的cookie：HTTP `auriga_inst`，HTTPS `__Secure-auriga_inst`，
HttpOnly、SameSite=Lax、无Domain；不使用要求Path=/的`__Host-`。edge按已验证mount取cookie并核sid/instance/nonce，重复歧义cookie拒绝。
Path只防正常浏览器选择覆盖，**不是同源恶意实例安全隔离**。domain保持`__Host-auriga_inst`、Path=/和每实例独立host。

前缀代理仅剥一次规范mount，DSH看到`/`、`/assets/...`、`/api/...`，内部节点ingress控制前缀只出现一次。
query/body/WS帧保持，拒绝编码斜杠/反斜杠、双重编码、点段穿越和路径归一化分歧；不以全局SPA fallback覆盖平台API。
所有请求到实例前剥离Auriga全部cookie（含session、CSRF、旧/新login_tx、instance以及`__Host-`/`__Secure-`变体），
Authorization、CSRF/bootstrap及可伪造内部身份/代理头；只加已有签名身份头。实例Set-Cookie不得设置任何Auriga保留名，
保留名按去掉安全前缀后的`auriga_`前缀（防大小写变体）统一判定；不是只维护一个不完整的名字列表。

非Auriga上游cookie只接受无Domain的host-only cookie；Path=/重写到mount，规范子Path拼mount，缺Path按上游请求默认路径再映射。
拒绝编码歧义/越界Path和无法保持前缀语义的`__Host-`上游cookie；保留HttpOnly/SameSite，Secure不得在HTTP上静默降级。
实例响应的Clear-Site-Data剥离，Service-Worker-Allowed不得超过mount；重定向Location只允许映射后的本实例mount，
无法安全映射的重定向失败，不重写任意HTML绝对URL。路径兼容不佳的插件应明确不支持，不能放开平台根路径。
IP同源代码仍能主动调用门户API、读门户CSRF或干扰同源存储；安装提示不能把这些规则包装成不可信代码沙箱。

内部alias仅接 `GET /api/workspace/v1/instance/self` 和 `POST /api/workspace/v1/instance/heartbeat`，
精确方法/规范路径、无重定向；所有静态、IAM/OAuth、admin、open/i以及其它API均404 `common.not_found`。
只消费`Authorization:Bearer ait_...`并由workspace核真实token hash、吊销、tenant/instance；前缀匹配不代表已认证。
缺失/错误/过期/吊销令牌及user/admin/device/service JWT、只有cookie一律401 `common.unauthenticated`。
alias不是秘密，即使外部伪Host也没有门户audience或额外路由。剥全部cookie和自报身份头，不进行cookie换JWT。
agent只把受信edge以alias接入归属明确的internal=true单实例bridge，精确两成员；不让实例进组件控制网、PG/NATS、
不挂Docker socket/控制证书，不借公网/VPS回流。内部issuer/浏览器Origin始终不变。现AURIGA_AGENT_INSTANCE_PLATFORM_URL
后续应仅生成这个内部URL，另由受信browser Origin配置renderer，不能继续一字段双用。

## 后续双模式验收矩阵

| 负责 | 正向 | 必须拒绝/保持 |
|---|---|---|
| IP1 edge/IAM | 真实IPv4门户/管理台口令+TOTP、刷新、同浏览器双会话并存/分别登出；多tab登记/相同token重试/并发提交 | 错listener/authority/scheme/Origin、缺/多个Origin、伪代理头、32条满额、到期/吊销与登记竞态、实例脚本读管理JSON/管理写 |
| IP2 ticket/renderer/plugin | 同浏览器A/B页面、相对资产、上传下载、双向WS；精确trustedOrigin；真实self/持续心跳；重启恢复 | 错nonce/跨用户实例/超时/重放、路径穿越；删A门户session使原A WS断且重连401，B独立session继续；上游实际无Auriga凭据 |
| IP3公开安装/浏览器 | 干净环境按公开参数完成domain和纯IP全链；实际cookie端口行为及DNS bridge两成员/alias、网络隔离证据 | 不用伪Host/hosts/health200代替；内部alias其它API/JWT/cookie拒绝，不能直达控制网或其它实例；domain原分源等价回归 |

每种模式都须覆盖真实浏览器地址、登录/刷新/会话、HTTP/WS与撤销，不用本次Schema/纯函数通过替代。
HTTP纯IP首次验收仅口令/TOTP；Passkey/WebAuthn受secure context和RP ID约束，本模式首批明确不支持，
不能把输入IP填成域名RP ID宣称通过。HTTPS IP也不自动解锁Passkey，OIDC第三方provider是否接受IP/HTTP回调需逐provider实际验收。
