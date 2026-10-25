# Edge 接入网关

`infra/edge` 是对外唯一入口的骨架（任务 B7）。进程名 `auriga-edge`。

IP1 增加受信入口模式：`--access-config <access.json>`（或基础配置 `accessConfigFile`、
`AURIGA_EDGE_ACCESS_CONFIG`）通过正式 `deploymentconfig.Load` 校验安装配置，再一次性绑定
portal/admin/instanceControl 三个独立 socket；任何一个失败都关闭此前绑定。开启后旧 listen、
host、secureCookies、trustedProxies 环境值不能覆盖安装身份。下文旧单 listener 配置仅适用于未启用此模式。

基础配置仍提供 upstream、静态目录、NATS、PKI。direct 根据实际 TLS 选择传输；HTTPS listener
使用现 `tls.certFile/keyFile`。trusted_proxy 的 portal/admin 强制实际 TLS+mTLS，额外固定
`proxyTLS.caFile` 与 `proxyTLS.identityUri`，同时校已验证证书的唯一 URI SAN 和实际 TCP peer CIDR；
Forwarded/X-Forwarded 不能决定角色、scheme 或租户。内控 listener 仅精确 self/heartbeat，
要求 `Bearer ait_...` 后仍由 workspace 验真实令牌，其余路由 404。

NAS 生成的 JSON 强制 `instanceControlIngress.denyLocalIPv4`，列出本安装的 LAN IPv4；该字段没有环境覆盖。启用时要求经验证的 wildcard 内控 listener、非空且不重复的规范 IPv4，并在任何服务绑定前核每个 LAN IP 在当前网络命名空间中唯一归属一个 UP 接口；不同活跃接口的 IPv4 直连前缀重叠则拒绝。

内控 Accept 使用内核 TCP 的本地／对端地址，统一 IPv4 映射，要求本地端口精确且目标仍是唯一非 LAN、非 loopback 接口的本机 IPv4；peer 必须属于该目标的同一直连私有前缀。LAN 接口上的目标、外部可路由源（包括经 LAN 投递到组件或实例私有目标）、IPv6、未知或多义上下文都在 HTTP／IAM 前关闭。每个新连接读取两次实际接口快照，只有一致且都通过才允许；动态 Docker 桥的新增无需重启，接口／地址删除、LAN 信息丢失、重叠或读取失败保守拒绝，客户端可重试。

拓扑前提是 NAS edge 只连接声明的 LAN、组件 internal bridge 及 owned 实例 internal bridge，私有链路是互不重叠的直连 L2 前缀，Docker／网络管理者受信。LocalAddr／RemoteAddr 与这些前缀的联校不是实际入接口或 L2 身份检测；两份快照也不使网络管理与 Accept 原子化。portal/admin 不受该过滤器影响，原 authority／AIT 校验继续强制；未启用此字段的其他配方保持原支持。

IAM 专用 mTLS hop、精确 cookie 白名单、标准服务 JWT 的 CSRF gRPC 配置，以及 SPA 公开
`auriga-access-mode` meta 的正式 wire 约束见
[edge-iam-hop.md](../../contracts/deployment/edge-iam-hop.md)。IAM hop 或管理 RPC 未装配时失败关闭；
IP admin 写每次在线核验，绝不退回旧 CSRF cookie/hash。管理 WebSocket 尚无正式握手协议，拒绝。
portal/domain OAuth authorize 仍由 IAM 原同意流程校当前 session/CSRF，IP admin 旧同意表单拒绝。
仅 provider 已登记表单 callback 的精确 POST 可按协议处理跨源 resume，不能整段跳过登录 Origin/CSRF。

受信入口浏览器请求在 KV 缺失或访问 JWT 剩余不超过 60 秒时，使用同一 `iamRpc`
调用 `IntrospectSession`；RPC 的 tenant/audience 来自实际安装入口。续换复核原 sid/account/tenant、
audience、绝对到期和认证时间，只有仍有效的响应才能供本请求转发；控制面错误不会退回旧 JWT。
未装配受信 RPC 或旧单 listener 模式不能续换，访问 JWT 临到期时失败关闭。

Introspect 不包含原 `csrfHash`，edge 必须重新取得绑定匹配的 KV 投影。真实有效的普通 API GET 可按同一
节流调用 Touch 重建投影；缺失 CSRF 投影的写请求返回 503，先通过 GET 恢复后再写。
IAM 的 NATS 投影可能异步可见，未取到投影的本次请求仍返回 503；不轮询等待、不自造 CSRF。
公共登录不会因旧 cookie 无效或续换 RPC 不可用而阻断重新登录。
`/open` 在取得会话前尚不能校验实例 ownership/tenant，缺投影时不 Touch，返回 503；
先通过正常受保护 GET 恢复后，再打开实例。撤销标记只作用于同 sid 的已有限流项，
另一会话的撤销不会被缓存成当前会话五分钟 RPC 故障。

通过入口、会话及适用 Origin/CSRF/ownership 校验的门户/管理 API 和 `/open` 请求才调用
`TouchSession`，按 tenant+audience+sid 合并并发、每 5 分钟最多尝试一次（失败也占用间隔）。
节流表上限 8192 项，只清理已过间隔且无在途 RPC 的项；满时新会话请求返回 503，
不会逐出仍在限流窗口内的项。表内无 cookie、JWT 或 CSRF；每次仍复查 KV，撤销事件继续取消原连接。
health、匿名、纯心跳、实例 cookie 请求不续期。实例路径没有门户 cookie 原值时只校原会话 KV，
不做反向 cookie 索引，因此仅实例活动不能延长门户闲置期限；原长连接的闲置截止不会因其他请求顺延。

门户/管理 index 注入经校验的 `domain|ip`，使用 no-store；静态资产不注入。
IP2 的实例 mount/cookiePath、上游 Location/Service Worker 边界与 IP3 真实浏览器双模式验收仍未完成，
本入口/RPC 单元和真实 socket 回归不能作为“纯 IP 已可用”的结论。

详细契约见 `contracts/README.md`（鉴权、路由、KV）与 `contracts/plugin/README.md` §2（身份头）。
实例源隔离见 ADR 0020；令牌模型见 ADR 0014。

## 运行

```powershell
. D:\project\chatgpt\.agents\auriga-env.ps1   # 若在派活环境
Set-Location infra\edge
go run . -config path\to\edge.config.json
go run . -version
```

缺关键配置（`portalHost` / `adminHost` / 上游 / `identityKeysFile` 等）会拒绝启动。
日志为 JSON slog，字段含 `service=edge`。

## 配置

JSON 文件 + 环境变量覆盖。公开仓示例一律用 `example.com` / `192.0.2.x`。

| 字段 | 环境变量 | 说明 |
|---|---|---|
| `listen` | `AURIGA_EDGE_LISTEN` | 监听地址，如 `127.0.0.1:8080` |
| `portalHost` | `AURIGA_EDGE_PORTAL_HOST` | 门户 Host（决定 portal 会话 cookie） |
| `adminHost` | `AURIGA_EDGE_ADMIN_HOST` | 管理台 Host（独立源） |
| `instanceParentDomain` | `AURIGA_EDGE_INSTANCE_PARENT_DOMAIN` | 可选；`\<id\>.\<域\>` 实例 Host |
| `secureCookies` | `AURIGA_EDGE_SECURE_COOKIES` | `true` 用 `__Host-*` + Secure；纯 HTTP 部署设 `false` |
| `trustedProxies` | `AURIGA_EDGE_TRUSTED_PROXIES` | IP/CIDR 列表；仅这些对端可提供转发客户端 IP |
| `upstreams.iam/org/workspace` | `AURIGA_EDGE_UPSTREAM_*` | 业务上游 base URL |
| `static.portalRoot/adminRoot/panelRoot` | `AURIGA_EDGE_STATIC_*` | SPA 静态根；`/panel/` 未配置则 404 |
| `tls.mode` | `AURIGA_EDGE_TLS_MODE` | `off` \| `files` \| `certmagic` |
| `tls.certFile/keyFile` | `AURIGA_EDGE_TLS_*` | `files` 模式证书路径 |
| `tls.domains` | — | `certmagic` 需要域名列表 |
| `rateLimit.requestsPerSecond/burst` | `AURIGA_EDGE_RATE_*` | 按客户端 IP 令牌桶；超限 429 |
| `identityKeysFile` | `AURIGA_EDGE_IDENTITY_KEYS_FILE` | Ed25519 签名私钥 JWKS 文件 |
| `sessionStore` | `AURIGA_EDGE_SESSION_STORE` | `memory`（默认）或 `nats` |
| `natsUrl` | `AURIGA_EDGE_NATS_URL` | `sessionStore=nats` 时需要 |

示例（脱敏）：

```json
{
  "listen": "127.0.0.1:8080",
  "portalHost": "portal.example.com",
  "adminHost": "admin.example.com",
  "instanceParentDomain": "i.example.com",
  "secureCookies": true,
  "trustedProxies": ["127.0.0.1", "192.0.2.1"],
  "upstreams": {
    "iam": "http://192.0.2.10:8081",
    "org": "http://192.0.2.10:8082",
    "workspace": "http://192.0.2.10:8083"
  },
  "static": {
    "portalRoot": "./web/portal/dist",
    "adminRoot": "./web/admin/dist"
  },
  "tls": { "mode": "off" },
  "rateLimit": { "requestsPerSecond": 50, "burst": 100 },
  "identityKeysFile": "./var/identity-signing-keys.json",
  "sessionStore": "memory",
  "natsUrl": "nats://127.0.0.1:4222",
  "nodeTLS": {
    "certFile": "./var/edge.crt",
    "keyFile": "./var/edge.key",
    "caFile": "./var/platform-ca.crt"
  }
}
```

## Host 模型

- 请求 Host = `portalHost` → 读门户会话 cookie（`__Host-auriga_session` 或 `auriga_session`）。
- Host = `adminHost` → 读管理台会话（`__Host-auriga_admin_session` / `auriga_admin_session`）。
- `/api/*/v1/admin/**` **只**接受管理台 Host；门户访问返回 **403**（`edge.admin_host_required`）。
- 不接受客户端用头/查询覆盖 tenant 或 audience。
- 实例：`<instanceId>.<instanceParentDomain>`；仅当**未**配置 `instanceParentDomain` 且 Host 为门户时，才启用同源 `/i/<instanceId>/…`（弱隔离，见 ADR 0020）。管理台 Host 上 `/i/` 一律 404。
- 路由前会拒绝含 `..`、`//`、`;`、编码斜杠（`%2f`/`%5c`）等未规范化路径（400 `edge.bad_path`）。

## 会话与 CSRF

链路：cookie 原值 → SHA-256 hex → `SessionStore`（可注入；内存实现供测试；NATS KV `auriga-sessions` 适配预留，不可用时回退 memory）。

校验：audience、未过期、未吊销。写请求（POST/PUT/PATCH/DELETE）且非公开路径：要求 `X-Auriga-CSRF` = CSRF cookie，且其 SHA-256 = 会话 `csrfHash`。

转发业务 API：删除名字含 `auriga_` 的 cookie；设置 `Authorization: Bearer <accessToken>`（**公开路径上的写请求不注入**会话 Bearer）；传播/生成 `X-Request-Id` 与 `traceparent`（非法值会丢弃并重生成）；覆盖写入 `X-Auriga-Client-IP`。API 请求体上限 1 MiB。

公开前缀默认：`/oauth2/`、`/.well-known/`、`/userinfo`（精确，不含 `/userinfoXYZ`）、`/api/iam/v1/login/`。其余 API 无会话且无客户端 Bearer → **401** `application/problem+json`。

KV 未命中：预留 `SessionIntrospector`（默认失败，不假造成功）；待 iam gRPC Introspect 接线。

## 可信代理与客户端 IP

仅当 `RemoteAddr` ∈ `trustedProxies` 时采信 `X-Forwarded-For`：从右向左取第一个**不在**可信列表中的地址。
不采信客户端可带的 `X-Auriga-Client-IP` / `X-Real-IP`（前置代理须覆盖或剥离它们）。
非可信直连：用真实 RemoteAddr。发往后端时始终覆盖设置 `X-Auriga-Client-IP`。
`trustedProxies` 拒绝 `0.0.0.0/0` / `::/0`；`rateLimit.requestsPerSecond` 必须 `> 0`。

## 身份头密钥文件

B2 的 `IdentityHeaderKeyService` 尚未实现；本任务从本地文件读私钥（安装器后续可改为调 iam）。

JWKS 子集（含 `d` 私钥种子，选 `state=CURRENT`）：

```json
{
  "keys": [
    {
      "kty": "OKP",
      "crv": "Ed25519",
      "x": "<base64url 公钥>",
      "d": "<base64url 32 字节种子>",
      "kid": "key-1",
      "alg": "EdDSA",
      "use": "sig",
      "state": "CURRENT"
    }
  ]
}
```

签发 `Auriga-Identity`：`iss=auriga-edge`，`exp≤iat+60`，含 `htm`/`htu`/`pv=1` 等（见 `identity-token.schema.json`）。
转发实例上游前删除客户端 `Auriga-Identity`、`Authorization`、含 `auriga_` 的 cookie。

## `/open` 与实例骨架

1. 门户 `GET|POST /open/<instanceId>`：校验门户会话 + 所有权 → 下发一次性 `auriga_open_nonce` cookie（有父域时 Domain 覆盖 portal+实例）→ 票据存 nonce 哈希 → 302 到实例 `/_auriga/ticket?t=…`。
2. 兑换票据：校验 nonce cookie 与票据哈希一致（不能只靠 Sec-Fetch-Site）→ binding 有效期对齐会话 `ExpiresAt` → 设置实例 cookie → 302 `/`。
3. 实例 Host（或无父域时的门户 `/i/<id>/…`）：验实例 cookie + 门户会话仍有效 → 签身份头 → 反代；剥 `auriga_*` / `__Host-*` Set-Cookie。实例路径只拒 `..`，不套用 portal 的严格规范化（允许 `%2f` 等应用路径）。
4. WebSocket：`Upgrade: websocket` 才做 Origin 检查；portal/admin API 只接受自身 Origin；实例只接受该实例自己的 Origin。会话附着到长连接，吊销/到期会 cancel。

内存票据用完即删；限流 bucket 定期/满额淘汰，IPv6 按 /64 归并。`ReadTimeout` 不设（大上传）；实例反代无短 `ResponseHeaderTimeout`；1MiB body 上限仅 `/api/`。

实例生产链路已接入 E2 的节点 mTLS 和 `auriga-routes`；冷启动页仍未实现。

## TLS

| 模式 | 用途 |
|---|---|
| `off` | 纯 HTTP；本地或外置反代终止 TLS |
| `files` | 证书/私钥路径 |
| `certmagic` | 配置校验需要 `domains`；本骨架**不**在 CI 外网申请证书。生产有域名时优先外置反代或安装器接入 CertMagic DNS-01；启用占位需 `AURIGA_EDGE_CERTMAGIC_ENABLE=1`（仍会提示未捆绑 ACME） |

## 与 B2 / E2 边界

以下为 B7 阶段分工；E2 当前实现与配置见文末 E2 节。

| 本任务（B7） | B2（iam） | E2 |
|---|---|---|
| 会话读取接口、cookie→Bearer、CSRF | 写会话 KV、签发 accessToken、csrfHash | 真节点 ingress、WebSocket、吊销关连 |
| 身份头签发（本地密钥文件） | `IdentityHeaderKeyService`（未实现） | 密钥改从 iam 拉取/轮换 |
| `/open` + 假上游反代 | — | 路由 KV、冷启动、mTLS |

## 测试

```powershell
Set-Location infra\edge
gofmt -l .
go vet ./...
go test -count=1 -timeout=10m ./...
go build ./...
```

覆盖：可信代理 IP、伪造客户端 IP、portal/admin Host 隔离、CSRF、会话缺失、Bearer 注入、限流、身份头验签、票据单次使用、剥 cookie。测试只用随机/`httptest` 端口，不启常驻进程。

## E2：节点实例入口

edge 启动必须配置 `natsUrl`（或 `AURIGA_EDGE_NATS_URL`）以及 `nodeTLS.certFile`、`nodeTLS.keyFile`、`nodeTLS.caFile`。环境变量分别是 `AURIGA_EDGE_NODE_CERT_FILE`、`AURIGA_EDGE_NODE_KEY_FILE`、`AURIGA_EDGE_NODE_CA_FILE`。客户端证书必须由平台 CA 签发，带 clientAuth 和 `auriga://edge/<id>` URI SAN；安装器负责签发与更新材料，换证后重启 edge。读取前拒绝链接和非普通文件；POSIX 收紧为 0600，Windows 设置仅当前服务用户、SYSTEM、Administrators 可访问的受保护 DACL。

edge 只读 workspace 发布的 NATS KV `auriga-routes`，不创建 bucket。路由使用独立的 NATS 用户，凭据由 `routesNatsCredsFile` / `AURIGA_EDGE_ROUTES_NATS_CREDS_FILE` 指定；未设置时才回落到 `natsCredsFile`，生产部署必须配置独立凭据，不能复用拥有会话写权限的用户。只有 workspace 可发布路由。未找到、已删除或格式错误时拒绝实例访问；NATS 连接／超时／bucket 不可用返回 503 `common.unavailable` 并记录日志，不用陈旧缓存授权。先验证实例绑定与会话才查路由，未登录统一 401；已登录的不存在／非主人统一 403。每次访问读取当前 KV revision，相同 revision 复用解码缓存；迁移更新 revision，删除会清除对应缓存。此实现没有只靠本地 watch 的断线陈旧窗口，代价是每个请求需要查询 KV。

路由用户权限应只放行以下读取 API 和回复 inbox（NATS 用户配置中的 permissions 示例；凭据由部署流程生成，不在公开配置中填写密码）。不要授予 `$KV.auriga-routes.>` 发布权限或 `$JS.API.>` 通配权限：

```conf
permissions: {
  publish: { allow: [
    "$JS.API.STREAM.INFO.KV_auriga-routes",
    "$JS.API.STREAM.MSG.GET.KV_auriga-routes",
    "$JS.API.DIRECT.GET.KV_auriga-routes",
    "$JS.API.DIRECT.GET.KV_auriga-routes.>"
  ] }
  subscribe: { allow: ["_INBOX.>"] }
}
```

第二轮主控已决定：地址只用于定位，信任以平台 CA 和目标节点证书为准，不另建地址白名单。入口只允许 `https://host:port`、`unix:///absolute/path`、`npipe:////./pipe/name`，拒绝明文 HTTP、userinfo、query、fragment、HTTPS 路径。所有通道都跑 TLS；校验完整证书链、有效期、serverAuth，且叶证书只有一个 URI SAN、精确等于 `auriga://node/<route.nodeId>`。不以 DNS SAN 代替节点身份，也不依赖环境 HTTP 代理。HTTP 请求和身份头只在 TLS 验证完成后发送。URI 身份验证同时支持没有 DNS 名称的本机 socket。

第三轮补充出站限制：`allowLocalIngress` 默认 false（环境变量 `AURIGA_EDGE_ALLOW_LOCAL_INGRESS`），此时不连接 Unix socket／命名管道，也不连接 HTTPS 回环地址。单机部署由 ctl／安装器显式启用该开关后才允许本机入口；ctl 接线不在本任务范围。HTTPS 每次新建连接时解析 DNS，检查全部解析结果，拒绝未指定、链路本地（含云元数据地址）、组播及带 zone 的地址；IPv4-mapped IPv6 先转换为 IPv4 再判断。全部结果通过后只拨号到已验证的数值 IP，不再次解析域名，避免 DNS 重绑定。允许单机模式也不会放开链路本地／未指定地址。完整解析与拨号共用 10 秒期限。私有单播节点地址仍受允许，并须通过节点证书验证。

节点证书现在由 workspace 的 NodeCa 同时签发 clientAuth 和 serverAuth：前者用于控制通道，后者用于实例入口。旧的仅 clientAuth 节点证书必须通过续期／重新入网换证，并重启 agent 入口以重新加载证书；不能放宽 edge 的 serverAuth 校验来兼容。

转发路径统一为 `/i/<instanceId>/<应用路径>`，身份头 `aud` 为实例 ID，`sub/tid/sid` 来自当前门户会话，`htu` 仍是实例最终看到的应用路径，不含查询串。每次检查路由主人、会话账号、实例绑定账号一致，并检查三者租户一致。契约当前只允许主人，没有信息支持的委派访问一律拒绝。WebSocket 与普通 HTTP 使用相同 mTLS Transport；SessionConnHub 在吊销／到期时取消连接，并在登记后再次校验会话以避免登记前的吊销竞态。

实例请求体总体上限 64 MiB（已知长度和未知长度都限制，超限 413），允许超过 1 MiB 的上传；API 的 1 MiB 上限保持原样。拨号／TLS 握手上限 10 秒，不设置实例短响应头超时和服务器整体 ReadTimeout。Transport 按节点 ID 和入口复用并限制池数量，地址变化和进程退出关闭空闲连接。

E2 测试见 `ingress*_test.go`、`local_*_test.go`：正常身份与路径、越权／跨租户、错误节点／CA、非法 scheme、WebSocket 吊销断连、路由迁移删除、2 MiB 上传及未知长度超限、本机 TLS。Windows 假节点使用随机命名管道上的真实 TLS，避开本机 HTTPS 扫描对 loopback TCP 证书的替换；Linux 使用 httptest 随机 TCP 端口，并另测 Unix socket。
## 远程 Team MCP 入口

安装配置可选 `upstreams["ai-gateway"]`：一个固定受信 HTTPS Origin，或同机数字回环 HTTP。
不能带用户信息、查询、片段或路径前缀。未配置时 Team 不可用，不回落到浏览器应用。

只有经正式安装配置校验的 portal 入口转发 `GET/POST /ai/mcp` 和
`POST /ai/team/node/{register,bind,pull,ack,revoke}`。后者仅供受信 Node，不是模型工具。
其他 `/ai` 路径和方法拒绝。每次转发只接收一个设备 Bearer，删除全部浏览器 cookie，
不会把门户或管理会话兑换成 Team 凭据。有 Origin 时须匹配完整门户源；远程网关仍须
逐次核凭据、当前成员权限和端点/会话参与关系，代理成功本身不代表已获授权。

`POST /oauth2/revoke` 只在 portal 入口经既有受认证 IAM hop 转发，保留协议认证，
不转发浏览器会话 cookie。IAM 负责 token/client 验证及设备撤销，不能凭任意 did 撤设备；
其他方法、子路径和 admin 入口没有新公共例外。退出不能停止 DSH 或任务。

本代码改动不等于网关已部署或双 DSH 已通；须用匹配制品验证真实登录、在线准入、
远程 MCP 和宿主投递的整条链。
