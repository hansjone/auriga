# Edge 接入网关

`infra/edge` 是对外唯一入口的骨架（任务 B7）。进程名 `auriga-edge`。

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

edge 只读 workspace 发布的 NATS KV `auriga-routes`，不创建 bucket。NATS 用户须允许 bucket 查询和读取，禁止写入 `$KV.auriga-routes.>`；只有 workspace 可发布路由。`natsCredsFile` 沿用原配置。未找到、已删除、格式错误或 NATS 不可用时拒绝实例访问，不使用陈旧缓存授权。每次访问读取当前 KV revision，相同 revision 复用解码缓存；迁移更新 revision，删除会清除对应缓存。此实现没有只靠本地 watch 的断线陈旧窗口，代价是每个请求需要查询 KV。

第二轮主控已决定：地址只用于定位，信任以平台 CA 和目标节点证书为准，不另建地址白名单。入口只允许 `https://host:port`、`unix:///absolute/path`、`npipe:////./pipe/name`，拒绝明文 HTTP、userinfo、query、fragment、HTTPS 路径。所有通道都跑 TLS；校验完整证书链、有效期、serverAuth，且叶证书只有一个 URI SAN、精确等于 `auriga://node/<route.nodeId>`。不以 DNS SAN 代替节点身份，也不依赖环境 HTTP 代理。HTTP 请求和身份头只在 TLS 验证完成后发送。URI 身份验证同时支持没有 DNS 名称的本机 socket。

转发路径统一为 `/i/<instanceId>/<应用路径>`，身份头 `aud` 为实例 ID，`sub/tid/sid` 来自当前门户会话，`htu` 仍是实例最终看到的应用路径，不含查询串。每次检查路由主人、会话账号、实例绑定账号一致，并检查三者租户一致。契约当前只允许主人，没有信息支持的委派访问一律拒绝。WebSocket 与普通 HTTP 使用相同 mTLS Transport；SessionConnHub 在吊销／到期时取消连接，并在登记后再次校验会话以避免登记前的吊销竞态。

实例请求体总体上限 64 MiB（已知长度和未知长度都限制，超限 413），允许超过 1 MiB 的上传；API 的 1 MiB 上限保持原样。拨号／TLS 握手上限 10 秒，不设置实例短响应头超时和服务器整体 ReadTimeout。Transport 按节点 ID 和入口复用并限制池数量，地址变化和进程退出关闭空闲连接。

E2 测试见 `ingress*_test.go`、`local_*_test.go`：正常身份与路径、越权／跨租户、错误节点／CA、非法 scheme、WebSocket 吊销断连、路由迁移删除、2 MiB 上传及未知长度超限、本机 TLS。Windows 假节点使用随机命名管道上的真实 TLS，避开本机 HTTPS 扫描对 loopback TCP 证书的替换；Linux 使用 httptest 随机 TCP 端口，并另测 Unix socket。
