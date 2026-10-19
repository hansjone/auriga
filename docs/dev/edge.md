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
  "sessionStore": "memory"
}
```

## Host 模型

- 请求 Host = `portalHost` → 读门户会话 cookie（`__Host-auriga_session` 或 `auriga_session`）。
- Host = `adminHost` → 读管理台会话（`__Host-auriga_admin_session` / `auriga_admin_session`）。
- `/api/*/v1/admin/**` **只**接受管理台 Host；门户访问返回 **403**（`edge.admin_host_required`）。
- 不接受客户端用头/查询覆盖 tenant 或 audience。
- 实例：`<instanceId>.<instanceParentDomain>`，或只有 IP 时的同源 `/i/<instanceId>/…`（弱隔离，见 ADR 0020）。

## 会话与 CSRF

链路：cookie 原值 → SHA-256 hex → `SessionStore`（可注入；内存实现供测试；NATS KV `auriga-sessions` 适配预留，不可用时回退 memory）。

校验：audience、未过期、未吊销。写请求（POST/PUT/PATCH/DELETE）且非公开路径：要求 `X-Auriga-CSRF` = CSRF cookie，且其 SHA-256 = 会话 `csrfHash`。

转发业务 API：删除名字含 `auriga_` 的 cookie；设置 `Authorization: Bearer <accessToken>`；传播/生成 `X-Request-Id` 与 `traceparent`；覆盖写入 `X-Auriga-Client-IP`。

公开前缀默认：`/oauth2/`、`/.well-known/`、`/userinfo`、`/api/iam/v1/login/`。其余 API 无会话且无客户端 Bearer → **401** `application/problem+json`。

KV 未命中：预留 `SessionIntrospector`（默认失败，不假造成功）；待 iam gRPC Introspect 接线。

## 可信代理与客户端 IP

仅当 `RemoteAddr` ∈ `trustedProxies` 时采信 `X-Auriga-Client-IP` / `X-Real-IP` / `X-Forwarded-For`（取第一跳）。
非可信直连：用真实 RemoteAddr，**忽略**客户端伪造的 `X-Auriga-Client-IP`。
发往后端时始终覆盖设置该头。

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

1. 门户 `GET|POST /open/<instanceId>`：校验门户会话 + 所有权（`RouteStore.ownerId`）→ 一次性票据（60s、单次、绑 sid+instance）→ 302 到实例源 `/_auriga/ticket?t=…`。
2. 兑换票据 → 设置实例 cookie → 302 `/`。
3. 实例 Host 或 `/i/<id>/…`：验实例 cookie + 门户会话仍有效 → 签身份头 → 反代到路由里的 `ingress`（测试用 `httptest`）。

**不做**（留给 E2）：经节点代理 ingress 的生产长连接 / WebSocket / mTLS 会话、冷启动页、NATS `auriga-routes` 实时同步。

## TLS

| 模式 | 用途 |
|---|---|
| `off` | 纯 HTTP；本地或外置反代终止 TLS |
| `files` | 证书/私钥路径 |
| `certmagic` | 配置校验需要 `domains`；本骨架**不**在 CI 外网申请证书。生产有域名时优先外置反代或安装器接入 CertMagic DNS-01；启用占位需 `AURIGA_EDGE_CERTMAGIC_ENABLE=1`（仍会提示未捆绑 ACME） |

## 与 B2 / E2 边界

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
