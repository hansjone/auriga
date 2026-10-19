# IAM 核心与连接器接入

## 装配和安装

`IamRuntimeConfiguration` 在配置 `auriga.iam.issuer` 后启用，仍受
`auriga.services.iam.enabled` 开关控制。`platform-app` 的 `iam` profile 配置
iam 的 common outbox、Flyway 和资源服务器。运行时角色和迁移角色按
[common.md](common.md) 分开，运行时角色不能是超级用户或拥有 BYPASSRLS。
IAM 所有查询都经过 `IamStore` 的租户事务；JDBC 使用显式租户条件，数据库使用
FORCE RLS。JSON 数据的身份关系同时由生成列、复合外键和唯一约束保护。

部署配置的形状如下；密钥通过安装器或环境注入，不能把真实值提交到仓库。

```yaml
auriga:
  iam:
    issuer: https://portal.example.com
    encryption-key: ${AURIGA_IAM_ENCRYPTION_KEY} # 32 个随机字节的标准 Base64
    hosts:
      "[portal.example.com]":
        tenant: 00000000-0000-0000-0000-000000000001
        audience: portal
        secure: true
      "[admin.example.com]":
        tenant: 00000000-0000-0000-0000-000000000001
        audience: admin
        secure: true
    trusted-proxies: [127.0.0.1]
    jwks-propagation-seconds: 300
    emergency-remote-enabled: false
```

只有 IP / HTTP 的安装可以把 host 配成 IP，issuer 使用 HTTP，`secure: false`。
对应 cookie 自动去掉 `__Host-` 和 Secure；不启用跨源凭据。
反向代理必须保留原始 Host；信任列表中的代理必须**覆盖**
`X-Auriga-Client-IP`，缺失或格式不正确时 IAM 拒绝请求。客户端不能指定租户或会话
audience。生产必须把所有反向代理列进信任列表，否则代理的 loopback 地址可能被
误当成本机用户；不要让代理直接转发客户端伪造的地址头。

安装器在受保护的本地安装流程中调用：

1. `SigningKeys.publishNext()`：提交新公钥，发现端点和 JWKS 即可返回它。
2. 等待配置的传播期（至少 30 秒；默认 300 秒），调用 `activate(kid)`。
3. `EmergencyAccounts.install(tenant)`：一次性创建随机用户名、随机口令、TOTP
   密钥、账号、内置身份源及外部身份。返回的 Enrollment 只能经安装器的受保护
   一次性通道交给操作者；`toString()` 不输出凭据。重复安装会失败。
4. 按安装策略设置应急账号的配置权限；默认 `PermissionResolver` 返回空集合，
   不隐式授予任意账号管理权限。

安装、密钥激活没有公开 HTTP 入口。ctl 的安装向导须调用这些本地 bean；本任务不改
ctl。安装没有完成或没有活动签名密钥时签发失败，不降级到临时密钥。

密钥轮换重复前两步。旧公钥在停止签名后至少保留 360 秒。签名私钥、身份源配置和
登录私有状态使用 AES-256-GCM，加上租户和用途作为 AAD；主密钥由部署方保管并备份。
应急账号口令使用 Argon2id（64 MiB、3 次迭代），每次登录必须校验 TOTP；已接受的
TOTP counter 在锁定的数据库记录中递增，不能重复使用。

## 登录和身份源

SPI 位于 `iam-api`。`IdentityConnector` 的 descriptor、context、result 和 input
直接使用 JSON Schema 验证的 JsonNode；结构来自 `contracts/identity`，没有另外
手写一套步骤 DTO。普通 HTTP 请求使用已生成的 `CreateLoginTransaction`、
`ProviderInput`；生成器将 StepInput 的 oneOf 展平成相互矛盾的 required 字段，
因此 advance 入口读取原始 JSON，直接按原始 StepInput schema 校验。

连接器作为 Spring `IdentityConnector` bean 注册，或在 `plugins/identity/` 放置
带 `META-INF/services/auriga.iam.api.IdentityConnector` 的 jar。
每个 jar 使用独立 URLClassLoader。SPI 主版本不为 1、重复类型或非法 descriptor
被拒绝；插件加载失败记录于 `ConnectorRegistry.loadErrors()`，保留应急连接器。
Java 插件是受信代码，不是可执行不可信代码的沙箱。

连接器只返回步骤和 ExternalProfile。核心负责事务的 5 分钟期限、浏览器绑定、
一次性完成、输入类型匹配、并发锁、准入、账号停用、多因素、会话和事件。
redirect 步骤必须有 state；核心固定 callback URL 并校验回调 state，具体协议的
nonce、上游 PKCE、签名或 WebAuthn 校验由对应连接器实现。公开 advance 不接受
callback 和 event 输入。POST 回调先加密暂存，再 303 到同源 GET，GET 验证浏览器
cookie 后一次性取出，避免放宽 SameSite。

SSE 在事务变化时发 step，结束时发 end 并关闭；浏览器取消和应用关闭会停止工作。
qr/device 连接器可通过 poll input 推进；内置 QR 手机端的 challenge/confirm 端点
留给团队账号任务，B2 没有凭空信任来自浏览器的带外 event。

新身份按 provider+subject 匹配，绝不按邮箱自动合并。auto_provision 必须命中规则，
email 规则必须 emailVerified=true。invite_only 使用 `IdentityPolicyHooks.consumeInvitation`
兑换邀请；默认拒绝。link 要求已登录且近期二次验证；stepup 不能切换账号。
默认不信任外部 mfaPerformed，`trustExternalMfa` 显式打开才采用。
需要平台补充 MFA 时返回 platform_totp 表单，由 `IdentityPolicyHooks.verifyMfa` 校验。
默认验证器拒绝，团队账号模块提供真实实现。new_device 当前保守地每次要求 MFA。
姓名、已验证邮箱由核心同步；`synchronize` 提供组织属性集成入口。组映射在 org
权限上限检查接入前拒绝配置。不能通过属性映射绕过授权。

远程连接器可通过 `auriga.iam.remote-connectors` 配置：每项 `url` 为 HTTPS 地址，
`shared-keys` 为至少 32 字节随机密钥的 Base64 列表。类型必须以 remote- 开头。
请求和响应均按 remote-connector.yaml 做 HMAC-SHA256；支持轮换期间多个密钥，
拒绝过期时间戳、无效响应签名、重定向及超大响应。连接和请求超时为 10 秒。
只记录调用方法、路径、耗时、结果；不记录配置、凭据、响应内容。
DirectoryCapability、SessionCheckCapability、CredentialCapability 均有 SPI 和远程适配。
外部会话定期复核的调度尚未接线，externalSessionCheckSeconds 大于 0 时拒绝创建配置，
避免启用了安全策略却被静默忽略。
管理台动态登记远程连接器及持久化注册信息不在当前实现中。

## 会话、令牌和 OAuth

portal/admin 使用不同的 cookie 和服务端 audience。SessionKv 的 PostgreSQL
实现按会话 schema 写入，并在读取时执行绝对/闲置期限；键是 cookie 的 SHA-256。
会话包含 csrfHash，原始 cookie 不落库。edge 负责普通写请求的 CSRF 校验；OAuth
授权确认页面自己校验绑定在会话中的 CSRF，且禁止嵌入 iframe。

`Sessions` 和 `AccountDirectory` 本地接口复用生成的 protobuf 类型。
gRPC SessionService 只允许 edge introspect/touch、org/ops revoke；AccountService
允许内部服务。调用方必须先绑定租户，不能用请求中的 tenant_id 切换租户。
common 拦截器负责服务令牌和代租户白名单。

访问令牌统一 ES256、typ=at+jwt、5 分钟有效期；包含 tid/ptyp/ctx/jti/iat/exp，
浏览器含 sid，设备含 did，二次验证包含 stepup_time。用户和设备带 mtyp。
portal 权限去掉 iam/org/ops 管理域和 admin/grant；设备只允许 auriga.connect
命名空间且过滤 write/admin/grant。配置权限以 `auriga.iam.permissions` 的
`<tenant UUID>/<subject>` 为键、逗号分隔权限为值；后续替换 PermissionResolver
调用 org。收到用户停用/离职事件会停用账号并吊销会话和设备；权限变化使当前会话
令牌立即需要刷新。outbox 和审计与业务数据处于同一个事务。

`InternalTokenIssuer.issue(service, installationSecret)` 单独验证安装凭据的 SHA-256，
签发 aud 只有 auriga.internal、sub=svc:服务名、ptyp=internal_service、ctx=service、
平台租户的令牌。配置 `internal-secret-hashes`；这些凭据不属于 OAuth clients。
`local-service-name` 和 `local-service-secret` 可装配 common 的 ServiceTokenProvider。
不要把内部安装凭据放进普通 client_credentials 注册。

OIDC/OAuth 的发现、JWKS、authorize、token、device_authorization、userinfo 均已实现。
本次没有引入 Spring Authorization Server：授权、刷新和设备凭据直接复用核心的
PostgreSQL 会话、锁、RLS 和吊销，避免再维护一套独立授权会话。协议控制器使用
Spring Security；这不是通过 OIDC 官方认证的完整实现。

客户端由部署配置 `auriga.iam.clients` 登记，各项字段为 id、tenant、secret-hash、
redirect-uris、scopes、grants、platform。grants 使用标准 grant_type 字符串；设备码
为 `urn:ietf:params:oauth:grant-type:device_code`。public client 的 secret-hash 留空。
机密客户端使用安装生成的高熵随机 secret 的 SHA-256。client_credentials 要求机密
客户端。platform 只能由可信安装配置指定，客户端不能通过 audience/resource 参数
索取令牌。第三方令牌的 aud 只有客户端 id，不含 auriga。

GET authorize 显示授权确认表单；POST 验证绑定会话的 CSRF 后签发 60 秒一次性授权码。
要求 S256 PKCE 和精确 redirect URI。ID token 使用 typ=JWT，与访问令牌分开；
userinfo 接受带 openid scope 的用户访问令牌，按 profile/email scope 控制字段。
刷新令牌一次性轮换，重放会吊销关联会话；会话或设备吊销后拒绝刷新。
设备码是 5 分钟、5 秒起始轮询间隔，过快轮询返回 slow_down 并增加 5 秒间隔。
设备确认走 `/api/iam/v1/device/{userCode}/approve|deny`，确认需要近期二次验证。

## 验证与边界

`IamFlowTest` 验证真实 Argon2/TOTP/JWT、会话及 grant 流程；测试库仅替换持久化 I/O。
`HttpBoundaryTest` 覆盖 Host、代理信任、cookie、CSRF；`ConnectorContractTest` 覆盖
SPI、配置和步骤 schema。`IamPostgresTest` 用真实 owner/runtime 两角色验证迁移、
RLS、事务回滚和限速。数据库测试在 Windows 沙箱可能因 pg_ctl restricted token
失败，不能把内存测试当成 RLS 已通过。

安装器、edge 的代理地址头、org 的邀请兑换/属性映射，以及多服务独立 DataSource
装配需要主控接线。NATS KV 适配、身份头 Ed25519 密钥服务、完整管理面 CRUD 和 OIDC
认证测试尚未提供。现有 KV TTL 在读取时执行，过期数据的物理清理由后续维护任务处理。

## 团队账号（team-account）

内置连接器类型 `team-account`，与应急账号同级，经 `ConnectorRegistry` 注册（Spring bean
`teamAccountConnector`）。`configSchema` 必填：

| 字段 | 说明 |
|---|---|
| `rpId` | WebAuthn RP ID（主机名或 IP） |
| `origins` | 允许的 WebAuthn origin 列表（如 `https://portal.example.com`） |
| `primaryStep` | 可选：`form`（默认）/ `qr` / `webauthn`，决定 begin 第一步 |

口令使用 Argon2id（与应急账号相同：64 MiB、3 次迭代）。登录事务内多步：
`form` 口令 → 若已绑定 TOTP 则 `form`/`totp` → `done(profile)`；连接器上报的
`mfaPerformed` 对 team-account 直接采信（不必开 `trustExternalMfa`）。
`invite_only` 时必须由 `IdentityPolicyHooks.validateInvitation` 只读预校验通过，
才展示 `register` 表单；提交注册时再次校验，避免无效邀请触发 Argon2 或账号探测。
该 hook 默认拒绝；org 的 `OrgIdentityPolicyHooks` 已接入租户内的只读预校验，检查
邀请存在、active、未过期、未耗尽使用次数及 provider 约束，不刷新状态或核销。
最终兑换仍走 `consumeInvitation`，并在 profile 上
附带 `accountId`。兑换失败会回滚已写入的 `team_account` 凭据行。

本人接口（需登录会话）：

- `/me`、`/me/sessions/**`、`/me/identities/**`、`/me/password`
- `/me/mfa`、`/me/mfa/totp/enrollment/**`、`DELETE /me/mfa/totp`
- `/me/passkeys/**`（WebAuthn，依赖 `com.yubico:webauthn-server-core`）
- `/me/devices/**`（复用 B2 `device` 表；吊销后 OAuth 刷新拒绝）

扫码入口目前返回 `iam.external_denied`：只读契约固定两位码且没有 POST 扫描操作，
尚不能安全启用。GET challenge 仅查看，不再标记已扫描。内部状态机采用六位码，
要求扫描者与确认者一致、仅接受 scanned 状态，失败和拒绝均不可恢复。确认端还会
检查活动会话的 MFA 状态；错码失败状态先提交，再返回 `iam.qr_match_mismatch`，
避免异常回滚导致挑战复活。poll 检查挑战过期，不计入凭据限速桶。
主控需更新 QR 契约及客户端，再增加 POST 扫描操作并恢复入口。

存储表（V3）：`team_account`、`team_totp`、`team_passkey`、`team_ceremony`、
`qr_challenge`。策略为 always 或 new_device 时，尚未登记任何二因子的团队账号在
口令通过后进入 `first_totp` 表单，只能登记首个 TOTP，确认成功前不发放会话。
新注册账号同样在邀请码预校验、口令设置后进入此流程，最终兑换在登记确认时执行。
登记密钥加密保存在绑定浏览器的五分钟登录事务中，仅在读取该事务的表单时解密显示；
不写入持久化 step 明文。确认时再次加锁检查没有 TOTP 或 Passkey，防止覆盖已有因子。
尚未完成注册的口令记录绑定登记事务：失败或取消时清理，超时后可重新注册；旧事务的
延迟清理只能删除属于自己的临时记录，不会删除替代事务的凭据或已绑定身份的账号。
new_device 当前保守地每次验证。仅有 Passkey
的账号在口令后必须完成对应账号的断言。stepup 即使策略为 never 也要求二因子。

本人接口的 TOTP / Passkey 登记开始、确认均复用 `RequiresRecentStepUp`，业务入口同时校验同租户、
同账号、带 sessionId 的十分钟内 step-up；新增二因子后撤销其他会话，并经 outbox 发出
`identity.mfa_changed`。用户通知订阅方仍需主控接线。已有 TOTP 不允许直接覆盖，返回
`common.conflict`；当前只读契约没有旧码字段，不能安全实现旧码核验式重置。
首次登记例外仅在已验证口令的登录事务中有效，不放宽本人接口的权限，也不签发临时
普通会话。首个 TOTP 校验成功后才签发 MFA 会话或完成原有会话的 step-up，并吊销
其他旧会话。Passkey 登记和断言均要求 UV=required，
yubico 校验认证器 UV 标志；凭据查询还校验 userHandle。

限速用户名使用 strip + 小写；后续 TOTP 优先按服务端解析的账号分桶（五次/五分钟），
无账号步骤按事务分桶，用户名阶段十次/五分钟，IP 额度保留。缺失账号仍执行一次
真实 Argon2 matches。显示名服务端限制 64 字符并拒绝控制字符和格式控制符。

## 通用 OIDC / OAuth 2.0（oidc）

内置连接器类型 `oidc`，经 `ConnectorRegistry` 注册（Spring bean `oidcConnector`）。
同一租户可配置多个实例。登录为 authorization code + PKCE（S256）；回调地址固定为
`ctx.callbackUrl`（`{issuer}/api/iam/v1/login/callback/{providerId}`）。

`configSchema` 字段：

| 字段 | 必填 | 说明 |
|---|---|---|
| `issuer` | 是 | IdP issuer URL；元数据从 `{issuer}/.well-known/openid-configuration` 懒加载并缓存约 10 分钟 |
| `clientId` | 是 | 客户端 id |
| `clientSecret` | 是 | 客户端密钥（`x-secret`，加密保存、管理台不回显） |
| `scopes` | 否 | 默认 `openid profile email` |
| `claimMapping` | 否 | 将 ExternalProfile 字段映射到 IdP claim 名；默认 `sub` / `name` / `email` / `email_verified` / `phone_number` |

`claimMapping` 可选键：`subject`、`displayName`、`email`、`emailVerified`、`phone`。

保存配置时会校验 JSON Schema，并尝试拉取 issuer 发现文档；发现失败返回
`iam.connector_unavailable` / `common.validation_failed`。回调里坏 state、坏 PKCE、IdP
`error` 参数或拒绝换票 → `iam.external_denied`。外部调用只记 host / 结果 / 耗时，
不记凭据与响应体。

管理台：`POST/GET/PATCH/DELETE /api/iam/v1/admin/providers` 可创建、查看、更新
（含启停）、停用 `connectorType=oidc` 实例。PATCH 对 config 做 merge；未提交的
`x-secret` 占位字段保持原值。

B34 安全约束：OIDC 上游默认必须 HTTPS；从 issuer discovery 返回的
authorization/token/userinfo/JWKS 端点允许跨域，各请求仍执行相同的地址过滤和响应限制。
当前配置 schema 不支持手工覆盖端点，传入这类字段会拒绝；不能借手工覆盖绕过发现。
仅本地开发可显式配置
`auriga.iam.oidc.allow-local-http=true`，允许 localhost / 127.0.0.1 / ::1 的回环端点；
该开关不放行其他内网地址。平台自己的 HTTP/IP 部署不受此上游限制影响。
出站使用 Apache HttpClient 5 的连接层 DnsResolver，逐个检查解析出的地址，拒绝私有、
回环、链路本地、CGNAT 和 IPv6 过渡地址。连接直接使用已校验的解析结果，禁止重定向、
自动重试和 cookie。每次请求关闭连接，响应解压后至多读取 1 MiB，超限立即取消请求。

id_token 使用发现文档里的同源 JWKS 验签，接受 RS256（至少 2048 位）和 ES256/P-256，
拒绝 none、HMAC、私钥 JWK、错误 kid/用途/算法和未理解的 crit，并校验 issuer、aud、
azp、nonce、exp、iat、nbf、sub；openid 流程缺少 id_token 时拒绝。JWKS 每次验证重新读取，
可跟随上游密钥轮换。userinfo.sub 必须等于签名 sub，已有签名 claim 不允许覆盖，
协议保留 claim 不从 userinfo 导入。参照
[OIDC Core 3.1.3.7 和 5.3.2](https://openid.net/specs/openid-connect-core-1_0.html)。

PATCH 改 issuer 必须同时提供非空且不同于旧值的 clientSecret，掩码占位不算重新提供；
审计只记录修改字段名，不记录配置值。既有契约未定义的 username_taken 已折叠为
`iam.invalid_credentials`；invalid_username/invalid_password/invalid_config 使用
`common.validation_failed`，provider_unavailable 使用 `common.not_found`，
builtin_provider 使用 `common.forbidden`。设备和 Passkey 的名称校验同样返回
`common.validation_failed`，没有新增错误码。

限速与找不到对象分别使用 `common.rate_limited`、`common.not_found`，登录准入拒绝仍为
`iam.admission_denied`，该码已补入身份连接器契约的通用码列表。
