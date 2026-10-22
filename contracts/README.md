# Auriga 契约（v1）

`contracts/` 是组件之间接口的**唯一来源**（ADR 0015）。Java、Go、TypeScript 类型都从这里生成，不手写；实现和契约不一致时以契约为准。

## 目录

| 路径 | 内容 | 格式 |
|---|---|---|
| `openapi/common.yaml` | 公共组件：错误、分页、鉴权方式 | OpenAPI 3.1 |
| `openapi/iam.yaml` | iam 对外接口：登录事务、会话、本人的身份 / 多因素 / 设备、身份源管理、服务账号 | OpenAPI 3.1 |
| `openapi/org.yaml` | org 对外接口：租户、成员、组、角色、策略、配额、邀请 | OpenAPI 3.1 |
| `openapi/workspace.yaml` | workspace 对外接口：实例、节点、入网令牌；实例和连接模式插件调用的接口 | OpenAPI 3.1 |
| `openapi/ai.yaml`、`openapi/resource.yaml` | M3首批模型管理/JSON/SSE及资源目录/审签/分发/节点下载 | OpenAPI 3.1 |
| `ai/README.md`、`resource/` | AI身份通道/预算、资源签名与撤销/降级JSONSchema及共享JCS向量 | Markdown、JSON Schema、JSON |
| `catalog/m3.json` | M3新增权限/域错误/事件生产方与ACL的单一定义 | JSON |
| `proto/auriga/node/v1` | workspace ↔ 节点代理：入网、NodeControl 双向流、实例规格 | protobuf（buf） |
| `proto/auriga/iam/v1`、`proto/auriga/org/v1` | iam、org 的内部 gRPC 接口 | protobuf |
| `proto/auriga/ai/v1`、`proto/auriga/resource/v1`、`proto/auriga/workspace/v1` | M3模型目录、原子准入消息、资源锁定解析与签名、实例AI绑定/资源应用状态 | protobuf |
| `events/` | NATS 事件：信封和 `user.*`、`identity.*`、`instance.*`、`audit.*` | JSON Schema 2020-12 |
| `identity/` | 身份源步骤协议、连接器描述、远程连接器 HTTP 接口 | JSON Schema、OpenAPI |
| `plugin/` | 插件协议：身份头、身份头公钥文件、实例环境变量、心跳、设备登录、面板加载 | Markdown、JSON Schema |
| `auth/` | 访问令牌（JWT）声明 | JSON Schema |
| `kv/` | NATS KV 里跨服务共享的数据（会话缓存、实例路由） | JSON Schema |
| `deployment/` | domain/单IPv4受信入口schema、启动语义、cookie/CSRF/实例路径及双模式验收矩阵（IP0，待IP1–IP3接线） | JSON Schema、JSON、Markdown |
| `runtime/` | 正式 DSH 发行/standalone/背书/根/高水位契约及 Java→Go 向量（RT0，生产待 RT1/RT2） | JSON Schema、JSON、Markdown |

## 版本与兼容

- **主版本**写在路径里：REST 是 `/api/<服务>/v1`，protobuf 包是 `auriga.<域>.v1`，插件协议是 `Auriga-Protocol: 1`，事件用信封的 `dataversion`。
- 一个主版本内**只做兼容修改**：加可选字段、加接口、加枚举值、加事件类型。不能删字段、改字段含义或类型、把可选改成必填、改 URL。
- 客户端必须忽略不认识的字段和枚举值（枚举值未知时按"未知"处理，不报错）。
- JSON Schema 里的 `additionalProperties: false` 只给**生产方**自测用（防止写错字段名）；消费方（服务、edge、插件、连接器）校验收到的数据时必须放宽为允许额外字段，否则以后加字段就成了不兼容修改。
- 受签名的资源envelope/撤销/降级对象是明确例外：其完整JSON映射严格，未知签名字段必须升级schemaVersion，不能丢弃字段后继续验签。见resource/README.md。
- 正式 runtime envelope 与 standalone ArtifactStatement 同属严格受签对象例外：未知字段/重复键拒绝，所有必需空字段显式输出，禁止直接签 protojson。见 runtime/README.md；不改变既有 resource 时间精度。
- `deployment/access.schema.json`是受信安装配置而非跨版本消息消费者；按schemaVersion严格拒绝未知字段，再执行启动语义规则，防配置拼写错误静默放宽入口。
- 不兼容的修改要开新主版本，旧版本至少保留一个发布周期。插件协议例外：平台同时支持当前和上一个主版本（用户电脑上的插件升级慢）。
- CI 检查：`buf lint`、`buf breaking`（FILE 级）、OpenAPI lint 和 diff、JSON Schema 校验。

## 通用约定（REST 和事件）

| 项 | 约定 |
|---|---|
| ID | UUIDv7，小写带连字符的字符串（`format: uuid`）。账号 id 和成员 id 是同一个值 |
| 时间 | RFC 3339，UTC，例如 `2026-10-19T08:00:00Z`；时长用整数秒，字段名带 `Seconds` |
| JSON 命名 | 字段 lowerCamelCase；枚举值小写 snake_case（`pending_restart`） |
| 分页 | 游标分页：请求 `limit`（默认 50，最大 200）和 `cursor`；响应 `{ "items": [...], "nextCursor": "…" }`，没有下一页时 `nextCursor` 省略 |
| 错误 | RFC 9457 `application/problem+json`，见 `openapi/common.yaml#/components/schemas/Problem`。`code` 是机器可读的错误码，前端按它显示中英文文案 |
| 错误码 | `<域>.<snake_case>`，例如 `iam.login_tx_expired`、`org.grant_exceeds_own`。通用码：`common.unauthenticated`、`common.forbidden`、`common.not_found`、`common.validation_failed`、`common.conflict`、`common.precondition_failed`、`common.rate_limited`、`common.internal`、`common.unavailable` |
| 语言 | 请求头 `Accept-Language`（`zh-CN`、`en`）；`Problem.detail` 和带 `LocalizedText` 的字段按它返回 |
| 乐观锁 | 可修改的管理类资源返回 `ETag`；`PATCH` / `PUT` 带 `If-Match` 时检查，不匹配返回 412 |
| 幂等 | 创建类 `POST` 支持 `Idempotency-Key` 请求头，24 小时内同一个键返回同一结果 |
| 追踪 | edge 生成 `X-Request-Id`，并传播 W3C `traceparent`；服务的日志和审计都带上 |

## 鉴权

| 调用方 | 凭据 | 说明 |
|---|---|---|
| 浏览器（门户） | cookie `__Host-auriga_session` | 不透明随机值；HttpOnly，Secure，SameSite=Lax，Path=/，不带 Domain。纯 HTTP 部署（只有 IP、没有 TLS）浏览器不接受 `__Host-` 前缀，退回名字 `auriga_session`（不带 Secure），由 edge 配置决定；服务看不到 cookie |
| 浏览器（管理台，独立Origin） | cookie `__Host-auriga_admin_session` | 同上，仅管理入口消费。edge按受信listener及完整scheme/authority映射tenant/audience，不接受客户端指定；IP两端口不隔离cookie，转实例前必须剥离 |
| 门户和domain管理台的写请求 | cookie `__Host-auriga_csrf` + 请求头 `X-Auriga-CSRF` | `POST` / `PUT` / `PATCH` / `DELETE`时头等于cookie，且SHA-256等于会话csrfHash；同时校完整Origin |
| IP管理台的写请求（IP0，待实现） | 仅内存CSRF token + `X-Auriga-CSRF` | 精确同源POST登记，IAM存最多32条15分钟hash，管理写逐次内部RPC核权威会话；不下发可读CSRF cookie、不退回旧csrfHash。接口、重试/并发/到期见deployment/README.md |
| edge → 业务服务 | `Authorization: Bearer <访问令牌>` | edge 校验会话后换成 iam 签发的访问令牌（JWT，5 分钟，见 `auth/access-token.schema.json`）。业务服务只认 Bearer，不认 cookie |
| 服务账号、外部系统 | OAuth 2.0 client credentials 换到的访问令牌 | `ptyp=service_account`、`ctx=service`；角色由 org 的角色绑定管理 |
| 服务 → 服务（gRPC） | 元数据 `authorization: Bearer <内部服务令牌>` | 内部服务令牌：`aud` 只有 `auriga.internal`、`ptyp=internal_service`、`ctx=service`、`sub=svc:<服务名>`，iam 只发给平台内置服务（安装时配置的凭据，不经 org 的服务账号）。gRPC 只接受这种令牌，外部 client credentials 令牌一律拒绝。代租户调用用元数据 `auriga-tenant-id`，被调方按配置的 `sub` 白名单决定谁可以代哪些租户。合并部署时是本地调用，不经过网络；分开部署时必须 mTLS，没配 TLS 只能绑回环地址 |
| DSH 实例 → 平台 | `Authorization: Bearer ait_…`（实例令牌） | 不透明，每实例一个，可吊销；edge 原样转给 workspace 校验 |
| 平台 → DSH 实例 | 请求头 `Auriga-Identity`（edge 签名的身份头） | 见 `plugin/README.md` |
| 用户电脑上的插件 | 设备凭据：设备授权流程（RFC 8628）换到的刷新令牌 | 访问令牌 `ptyp=device`、`ctx=device`、带 `did`，权限只限 `auriga.connect` 范围；调 ai-gateway 时另换 `aud=auriga.ai-gateway` 的令牌。见 `plugin/README.md` |
| 节点代理、edge、workspace 之间 | 证书 URI SAN | 内置 CA 签发的证书用 SAN 区分身份：`auriga://node/<id>`、`auriga://edge/<id>`、`auriga://workspace`。节点代理的 ingress 只接受 edge 的 SAN；workspace 以证书 SAN 为准认定节点 id |
| 节点代理 | 节点证书（mTLS） | 入网时用一次性令牌换取，见 `proto/auriga/node/v1/enrollment.proto` |

- 访问令牌按 RFC 9068：JOSE 头 `typ: at+jwt`，ES256；iam 在 `/.well-known/openid-configuration` 和 `/oauth2/jwks` 发布公钥；服务自行校验 typ、iss、aud、exp，不回调 iam。发给第三方 OIDC 客户端的令牌 `aud` 只有客户端 id，平台服务拒绝。
- 访问令牌的 `ctx` 区分来源（portal / admin / device / service / instance）。instance仅用于受认证内部AI通道，普通业务服务和公网拒收，不能与ait_混淆；详见ai/README.md及ADR0021。OpenAPI 里所有 `/admin/**` 操作都标了 `x-admin-only: true`，只接受 `ctx=admin`；"近期做过二次验证"用令牌的 `stepup_time` 判断（默认 10 分钟）。
- 身份头用 Ed25519（EdDSA），密钥由 iam 管理、edge 签名、节点代理把公钥写进实例。
- OIDC 和 OAuth 2.0 的标准端点（`/oauth2/authorize`、`/oauth2/token`、`/oauth2/device_authorization`、`/oauth2/jwks`、`/userinfo`、`/.well-known/openid-configuration`）由 iam 提供，按标准实现，不在 OpenAPI 里重复定义。
- 租户：令牌里的`tid`决定租户。登录前先验证完整入口authority/受信listener再按安装映射取租户；domain可调用org `TenantService.ResolveTenantByHost`，IP首批明确固定单租户。未知入口不能落到默认租户。
- 登录事务使用portal/admin分名cookie并绑定规范Origin/tenant/audience，TLS的`__Host-`均Path=/、Secure、无Domain，HTTP去此前缀。两入口可并行登录/分别登出；详见iam OpenAPI和deployment/README.md（IP0，接线待IP1）。

## 路由（edge 对外）

| 路径前缀 | 转到 |
|---|---|
| `/api/iam/v1/`、`/oauth2/`、`/.well-known/`、`/userinfo` | iam |
| `/api/org/v1/` | org |
| `/api/workspace/v1/` | workspace |
| `/api/ai/v1/`、`/ai/v1/` | ai-gateway；管理/数据audience及内部instance入口分别校验 |
| `/api/resource/v1/` | resource；`/internal/resource/**`只在内部节点mTLS入口，公网不路由 |
| `/open/<实例 id>` | edge：校验门户会话和主人，签一次性票据，302 到实例地址 |
| `/panel/` | 团队面板静态文件（见 `plugin/README.md`） |
| 其他 | 门户 / 管理台静态文件 |

**实例用独立的源**（ADR 0020）：实例地址是 `https://<实例 id>.<实例域>/`（例如实例域 `i.example.com`，需要通配符 DNS 和通配符证书），edge 按 Host 把请求经节点代理 ingress 转给实例并加身份头。
- 进入实例：门户打开 `/open/<实例 id>` → edge 校验门户会话、确认是主人 → 签一次性票据（60 秒、只能用一次、绑定会话 sid 和实例 id）→ 302 到 `https://<实例 id>.<实例域>/_auriga/ticket?t=<票据>` → edge 换成实例 cookie `__Host-auriga_inst`（HttpOnly、Secure、SameSite=Lax、只对这个实例的源有效，内容绑定 sid 和实例 id）→ 302 到 `/`。
- edge每次请求都检查实例cookie对应的门户会话仍有效；会话吊销后实例cookie失效。domain分源时实例host拿不到门户/管理台host-only cookie；IP共享host时浏览器可能发送两者，edge必须剥离全部Auriga凭据再转实例，不能声称端口隔离cookie。
- domain平台CORS永不放行实例来源；写请求仍校CSRF/完整Origin。IP实例与门户同源，不能依赖CORS隔离门户；独立管理Origin不开实例CORS，管理CSRF只内存交付。
- 只有 IP、没有域名的部署无法做子域：退回同源路径 `/i/<实例 id>/`，实例里的代码可以借用户的门户会话调非管理接口——这是已知的弱隔离，和 Windows 原生隔离一样只面向互相信任的团队，安装向导和文档里要写明。

管理台用独立的源（例如 `admin.example.com`），`/api/*/v1/admin/**` 只接受管理台源上的会话（edge 检查）和 `ctx=admin` 的令牌（服务检查）。

纯IP具体边界见[部署入口契约](deployment/README.md)及[ADR0022](../docs/adr/0022-ip-access-boundaries.md)：
同IPv4门户/实例固定端口、管理台另一端口；实例兑票回`/i/<id>/`，每实例Path cookie不使用`__Host-`。
公开浏览器Origin与仅self/heartbeat的内部Docker DNS alias入口解耦，保留alias不得为IP及内部网络隔离。
HTTP首验口令/TOTP，Passkey首批不支持；配置schema通过不等于安装、登录、真实浏览器HTTP/WS或心跳已验收。

单机安装、在 localhost 访问时，平台对 loopback 来源**不返回** `Access-Control-Allow-Credentials`，只放行带设备 Bearer 令牌的跨源请求。

## NATS

| 名称 | 类型 | 写入方 | 读取方 | 内容 |
|---|---|---|---|---|
| `AURIGA_EVENTS` | JetStream 流，主题 `auriga.evt.>` | 各服务（经 outbox） | 各服务 | 业务事件，见 `events/README.md` |
| `AURIGA_AUDIT` | JetStream 流，主题 `auriga.audit.>` | 各服务（经 outbox） | audit | 审计记录 |
| `auriga-sessions` | KV | iam | edge | 会话缓存，键是 cookie 值的 SHA-256（小写十六进制），见 `kv/session.schema.json` |
| `auriga-routes` | KV | workspace | edge | 实例路由，键是实例 id，见 `kv/route.schema.json` |

## 代码生成

| 来源 | Java | Go | TypeScript |
|---|---|---|---|
| protobuf | protobuf-java + grpc-java，包 `auriga.contracts.<域>.v1` | protoc-gen-go + protoc-gen-go-grpc，模块 `github.com/hansjone/auriga/infra/gen` | 需要时用 `@bufbuild/protobuf` |
| OpenAPI | openapi-generator（Spring 接口 + 模型） | 需要时用 oapi-codegen | openapi-typescript |
| JSON Schema | jsonschema2pojo 或等价工具 | 需要时用 go-jsonschema | json-schema-to-typescript |

Java与TS的HTTP生成包含iam/org/workspace/ai/resource。Go protobuf仍由infra/generate.go生成；
新增Go HTTP模型复用现有固定版OpenAPI Generator，显式Maven profile与命令见infra/gen/README.md，
生成Go代码提交，Java Linux CI重生并检查新增HTTP两包是否最新。普通Java构建不改跟踪的Go文件。

## 校验

```
buf lint && buf breaking --against <上一版本>          # 在 contracts/ 下
redocly lint --config redocly.yaml openapi/iam.yaml openapi/org.yaml openapi/workspace.yaml openapi/ai.yaml openapi/resource.yaml identity/remote-connector.yaml
```

JSON Schema 用 Ajv（2020-12，strict）编译全部 `*.schema.json`。OpenAPI 生成 Java 前先用 `redocly bundle` 把外部引用打包成单文件；跳转回调端点（`/login/callback/*`）手写，不用生成的接口。

## 修改流程

1. 改契约和改实现放在同一个变更里；
2. 跑 lint 和兼容性检查；
3. 不兼容的修改先在 ADR 里说明理由，再开新主版本。
