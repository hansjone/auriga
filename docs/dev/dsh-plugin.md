# DSH 插件（auriga-dsh）

包名 `auriga-dsh`（MIT），目录 `clients/dsh-plugin`。Node 22+，纯 ESM + JSDoc；类型来自 `contracts/identity/steps.schema.json`（`pnpm --filter auriga-dsh gen:steps`）。

## 模式

| `AURIGA_MODE`        | 含义                                                                  |
| -------------------- | --------------------------------------------------------------------- |
| `standalone`（默认） | 不连平台；应急账号 + 配置的 JS 连接器；单一主人                       |
| `managed`            | 平台托管实例：门锁、守卫、活动探针、团队面板外壳、`ctx.get('auriga')` |
| `connect`            | 本机 DSH 接入团队平台（后续任务）                                     |

`initialize()` 返回模式占位对象。standalone / managed 用 `.create(options)` 建运行时。

Cordis 宿主入口：`auriga-dsh/plugin`（`package.json` `main`）。浏览器面板外壳：`auriga-dsh/client`。

```js
import initialize, { enrollEmergency, createManaged } from 'auriga-dsh';

const mode = initialize({ AURIGA_MODE: 'standalone' });
const emergency = enrollEmergency('alice', process.env.EMERGENCY_PASSWORD);
const runtime = await mode.create({
  config: {
    secure: false, // HTTP / 仅 IP 部署时去掉 __Host- 与 Secure
    callbackBaseUrl: 'http://192.0.2.10/auriga/login/callback',
    connectors: [
      { type: 'emergency', id: 'emergency', config: emergency },
      {
        type: 'oidc',
        id: 'company-oidc',
        config: {
          issuer: 'https://idp.example.com',
          clientId: '…',
          clientSecret: '…',
        },
      },
    ],
  },
});
```

## 托管模式（managed）

环境变量见 `contracts/plugin/README.md` 第 3 节。必需：`AURIGA_INSTANCE_ID`、`AURIGA_OWNER_ID`、`AURIGA_PLATFORM_URL`、`AURIGA_IDENTITY_KEYS_FILE`、`AURIGA_INSTANCE_TOKEN_FILE`。

### 门锁

每个 HTTP / WebSocket 升级请求校验 `Auriga-Identity`（EdDSA / Ed25519，`typ=auriga-id+jwt`）：

1. 按 `kid` 从 `AURIGA_IDENTITY_KEYS_FILE` 取公钥（找不到则限流重读文件；**不**从远程 URL 拉密钥）；
2. `aud` = 本实例 id、`sub` = `AURIGA_OWNER_ID`、`pv` 为支持的协议主版本；
3. `iat` / `exp`（允许 5 秒时钟偏差；`exp ≤ iat + 60`）；
4. `htm` / `htu` 与请求方法和**原始** path 一致（不经 `URL` 规范化）；
5. `jti` 在有效期内去重（有数量上限；任务要求防重放；契约原文仅用于日志关联）。

失败一律 **401** + `common.unauthenticated`。拿不到 `ctx.webServer.server` 时插件 **启动失败**（失败关闭）。仅精确路径 `GET|HEAD /_auriga/health` 免校验。

### 守卫

- 拦截原生 `pluginManager` 变更：`installBundle` / `removeBundle` / `setBundleEnabled` / `setPluginEnabled` / `setVersionExemption` / `cancelInstall`（服务层 + HTTP 写方法 `/api/pluginManager/<方法>`，含尾斜杠、重复斜杠、`.` 段等归一化变体），错误码 `common.forbidden`。
- 拦截越界新建工作区：`workspaceRegistry.create` 路径必须在 `AURIGA_WORKSPACE_ROOT`（默认 `/home/dsh/workspace`）之下（字符串约束，不解析 symlink）。

### 活动与心跳

- `GET /_auriga/health`（免鉴权，仅 GET/HEAD）→ 契约字段 `{ status, ready, protocol, pluginVersion }`，不含活动计数。
- 活动计数来自 DSH 服务 `agents` / `terminals` / `dshIm` / `connection`（与原型 hub-heartbeat 同源）；接不上的指标记为未知，心跳里 `health.ok=false` 且 `problems` 含 `activity.<字段>.unknown`，避免平台把「未知」当成全 0 闲置回收。
- 每 30 秒及活动计数变化（2 秒去抖）`POST …/instance/heartbeat`；发送失败 catch + 指数退避，不抛未处理拒绝。
- 协议：当前与上一个主版本；更旧返回平台侧 `plugin.protocol_unsupported`（426）。
- 工作区根守卫只做路径字符串约束，**不**解析符号链接逃逸。

### 团队面板外壳

- Node：`GET /auriga/bootstrap`（经 DSH `connection.requestRejection`）返回已验签清单与同源 `panelBase=/panel/`。
- 清单：JCS + Ed25519（`AURIGA_PANEL_KEYS_FILE` 或 `AURIGA_PANEL_KEYS`）；拒过期与版本回滚。
- 浏览器：`src/client.js` 挂 `sidebar.panellist` + `main`，SRI 加载 `<auriga-team-panel>`（ADR 0017）。

### SDK

`ctx.provide('auriga', …)` / `ctx.get('auriga')`：`version`、`identity()`、`kb`、`notify`、`files`。不提供原型的 `dshTeam`。

## 连接器 SPI（与契约一致）

接口语义同 `docs/architecture/identity.zh.md` 第五节与 `contracts/identity/`：

- `descriptor()` → `ConnectorDescriptor`（`spiVersion` 必须为 `"1"`）
- `validateConfig(config)`
- `begin(ctx, config)` / `advance(ctx, config, input)` → `{ step, state? }`
- 步骤与 `StepInput` 使用 `steps.schema.json` 的 JSON 形状

内置类型：`emergency`、`oidc`。其它类型通过 `module` 动态 `import()`（企业私有包名）。

连接器只翻译外部协议为 `ExternalProfile`；主人绑定、登录事务、会话、限速由 standalone 核心完成。

## 应急账号

- 口令：Argon2id（默认 `m=65536,t=3,p=1`，与平台 IAM 一致）；`enrollEmergency` 生成 PHC 串
- 配置校验强制下限：`m≥19456`、`t≥2`、`p≥1`、盐≥16 字节，并设上限防 DoS；弱哈希拒绝启动
- 用户名错误时仍对占位哈希做一次 Argon2，避免按耗时探测用户名
- 每次登录强制 TOTP（RFC 6238，6 位 / 30s，窗口 ±1，counter 防重放）
- 登录事务 5 分钟过期、一次性（advance 前占位 `busy`）；按 IP 与规范化账号限速（桶有过期清理与数量上限）

## 通用 OIDC

- authorization code + `state` + `nonce` + PKCE S256；`scopes` 必须含 `openid`
- 回调地址用 `ctx.callbackUrl`（由 `callbackBaseUrl/<providerId>` 固定），不接受参数指定
- `id_token` 用 JWKS 验签（RS256 / ES256）；无 id_token 一律拒绝
- discovery 的 `authorization_endpoint` 等必须 https（或允许的本地 http），且授权主机与 issuer 相同
- 外部 HTTP：undici `Agent` 自定义 `lookup`，连接只用已校验地址（防 DNS 重绑定）；响应体流式限长 1 MiB

## 主人与会话（standalone）

- 第一次成功登录的身份成为主人（`claimOwner` 比较并设置，持久化实现必须原子）
- 可选配置 `owner: { providers?, subject?, email? }`：限定谁可以成为主人。**接公开 OIDC 时务必配置**，否则首个访问者即主人
- 之后只允许主人名下已绑定身份；`linkIdentity(providerId, subject, { cookies, headers })` 要求主人会话 + 近期 MFA（约 10 分钟）+ CSRF
- 非主人 / 策略不符：`iam.admission_denied`
- 会话 cookie：`HttpOnly` + `SameSite`；HTTPS 下用 `__Host-auriga_session` / `__Host-auriga_csrf`
- 写请求（POST/PUT/PATCH/DELETE）校验 `X-Auriga-CSRF` 与 CSRF cookie，且 SHA-256 对应会话内 `csrfHash`

## 错误码

只用契约里出现过的码，例如：

`iam.invalid_credentials`、`iam.external_denied`、`iam.connector_unavailable`、`iam.admission_denied`、`iam.login_tx_expired`、`iam.login_tx_finished`、`common.rate_limited`、`common.forbidden`、`common.unauthenticated`、`common.validation_failed`、`common.not_found`、`common.unavailable`、`common.precondition_failed`、`plugin.protocol_unsupported`

## 开发与测试

```powershell
cd clients/dsh-plugin
node --test
node node_modules/typescript/bin/tsc -p tsconfig.json --noEmit
```

任务说明里的 Vitest 与仓库约定不一致：本包装用 Node 原生测试（`node --test`），与 `docs/dev/web.md` 一致。

包名已是 `auriga-dsh`；是否发布 npm 由用户决定，工人不发布。
