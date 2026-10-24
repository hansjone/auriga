# DSH 插件（auriga-dsh）

包名 `auriga-dsh`（MIT），目录 `clients/dsh-plugin`。Node 22+，纯 ESM + JSDoc；类型来自 `contracts/identity/steps.schema.json`（`pnpm --filter auriga-dsh gen:steps`）。

## 模式

| `AURIGA_MODE`        | 含义                                                                  |
| -------------------- | --------------------------------------------------------------------- |
| `standalone`（默认） | 不连平台；应急账号 + 配置的 JS 连接器；单一主人                       |
| `managed`            | 平台托管实例：门锁、守卫、活动探针、团队面板外壳、`ctx.get('auriga')` |
| `connect`            | 本机 DSH 接入团队平台（后续任务）                                     |

`initialize()` 返回模式占位对象。standalone / managed 用 `.create(options)` 建运行时。

Cordis bundle 行使用包根 `auriga-dsh`（根 export 与 `main` 均指向 `src/plugin.js`）；`auriga-dsh/plugin` 显式 Node 入口仍保留。库入口为 `auriga-dsh/library`，浏览器面板外壳为 `auriga-dsh/client`。宿主浏览器模块收集器不收集子路径行，所以只加载 `/plugin` 不能证明浏览器插件已挂载。

## 本地目录与发行包安装

包通过 `dsh.bundle.patch: ./cordis.patch.yml` 声明 bundle；补丁只插入公开的 `auriga-dsh` 包根行，不携带旧 Team 地址、凭据或认证默认值。使用真实 DSH 插件管理器添加准备好的目录或 `.tgz`，沿宿主包管理与脚本审批策略。

开发目录 link 前，先按仓库开发说明准备 workspace 及插件运行依赖，再在仓库根执行 `node clients/dsh-plugin/scripts/package-ui.mjs`，然后添加 `clients/dsh-plugin`。干净检出的 ignored `ui/` 目录不存在，不能省略此步骤后宣称目录开箱可用。脚本使用当前仓库原生 panel 源码生成资产和实际摘要，不复用旧工位产物。

发行准备在插件目录执行 `npm pack`，既有 prepack 构建并打入 UI、Node/browser 源码与补丁。成品 tar 消费者无须仓库源码或 Vite，不执行新的安装构建钩子。安装、enabled/active、Node 路由可达、实际 browser graph/模块可达分别核验；默认只加载尚未配置的界面，不能当登录/真实账号接通。

```js
import initialize, { enrollEmergency, createManaged } from 'auriga-dsh/library';

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

Loader 行配置（节点代理渲染进 `cordis.patch.yml` 的 `auriga-dsh` 行）：

| 键                | 含义                                                                                                                                                                                                                               |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `trustedHosts`    | edge 实例域名（`host` 或 `host:port`，无端口匹配任意端口），节点代理独占                                                                                                                                                           |
| `imBots`          | 只接受 `"none"`：显式声明本构建没有 IM 服务，`activeImBots` 按已知 0 上报；其他值启动失败。由模板规格补丁 `plugins.auriga-dsh.imBots` 给出                                                                                         |
| `terminalSources` | 只接受 `"browser-only"`：固定 web profile 声明仅配置原生浏览器终端，允许已确认缺席的模型 PTY 来源为无；仍读取实际存在的全局与 Agent 级 `terminals`。由固定模板补丁 `plugins.auriga-dsh.terminalSources` 给出，不能用于跳过未知来源 |

### 门锁

每个 HTTP / WebSocket 升级请求校验 `Auriga-Identity`（EdDSA / Ed25519，`typ=auriga-id+jwt`）：

1. 按 `kid` 从 `AURIGA_IDENTITY_KEYS_FILE` 取公钥（找不到则限流重读文件；**不**从远程 URL 拉密钥）；
2. `aud` = 本实例 id、`sub` = `AURIGA_OWNER_ID`、`pv` 为支持的协议主版本；
3. `iat` / `exp`（允许 5 秒时钟偏差；`exp ≤ iat + 60`）；
4. `htm` / `htu` 与请求方法和**原始** path 一致（不经 `URL` 规范化）；
5. `jti` 在有效期内去重（有数量上限；任务要求防重放；契约原文仅用于日志关联）。

失败一律 **401** + `common.unauthenticated`。拿不到 `ctx.webServer.server` 时插件 **启动失败**（失败关闭）。仅精确路径 `GET|HEAD /_auriga/health` 免校验。

免校验只适用于普通 HTTP 健康请求；向健康路径发起 WebSocket 升级仍须身份头。门锁在原生插件写接口守卫之前执行，未认证的写请求也返回 401。

DSH 0.2.0-rc.2 还会通过 `connection.authorizeIndex` / `requestRejection` 检查原生启动令牌与浏览器 cookie，`requestRejection` 同时是 Host/Origin 栅栏（防 DNS 重绑定、跨站请求/WS 劫持）。托管模式用门锁内部的 WeakSet 标记门锁放行的请求对象（健康检查也标记，但不算交互；它只能落到插件自己的精确健康路由），对标记请求只替换原生**令牌**检查，栅栏由插件重做：Host 必须是 IP 字面量、loopback 或 `trustedHosts`；`Sec-Fetch-Site: cross-site` 拒绝；带 Origin 时必须等于 Host 或属于 `trustedHosts`；不满足返回 403（`authorizeIndex` 写 403 并返回 false）。之所以不直接调原生栅栏：节点 ingress 把 Host 改成实例上游地址，原生 `Origin === Host` 会拒绝所有合法的 edge 来源。客户端头、手写 `__aurigaIdentity` 属性都不能产生标记；未标记请求仍完全走原生检查。卸载时恢复方法。

门锁还拒绝宿主自己注册了监听器的 `checkContinue` / `checkExpectation`（417）和 `connect`（断开），这些事件不能绕过门锁。edge 侧：实例域名上的 WebSocket 升级要求 Origin 等于实例域名（`checkUpgradeOriginInstance`），普通请求不查 Origin，因此插件这一层仍是必要的。

### 守卫

- 拦截原生 `pluginManager` 变更：`installBundle` / `removeBundle` / `setBundleEnabled` / `setPluginEnabled` / `setVersionExemption` / `cancelInstall`（服务层 + HTTP 写方法 `/api/pluginManager/<方法>`，含尾斜杠、重复斜杠、`.` 段、`;` 路径参数、`%00` 等归一化变体），错误码 `common.forbidden`。
- 拦截越界新建工作区：`workspaceRegistry.create` 路径必须在 `AURIGA_WORKSPACE_ROOT`（默认 `/home/dsh/workspace`）之下（字符串约束，不解析 symlink）。

### 活动与心跳

- `GET /_auriga/health`（免鉴权，仅 GET/HEAD）→ 契约字段 `{ status, ready, protocol, pluginVersion }`，不含活动计数。
- 活动计数来自 `agents`（执行中轮次）、终端 registry、`dshIm.listDeliveryCatalog`（IM 机器人）和浏览器 `upgrade` 套接字数（`connectedClients`，对应原型 `browsers` 回调）。接不上的指标在快照里保留 `null` / `known=false`，不转成 0。
- **固定 DSH 0.2.0-rc.2 的两类终端**：浏览器 `terminalController.owners` 与模型 PTY `terminals` 是独立来源。浏览器逐个保留的 Session owner 调用 `list(sessionId)`，覆盖离线 Agent、无浏览器附着的后台终端；仅明确 `running` 且未在清理的终端计数。`pending`、失败 allocation、关闭或清理中、`failed`/`exited` 保留记录均为未知，直到实际清理移出 registry 后恢复已知空闲，不假造正计数。模型 PTY 同时检查全局与每个在线 Agent 的服务，按 `sessions` Map 去重，使用 registry 中的精确 owner（含保留的离线 owner）调用 `list(owner)`，计数 `status.kind=running`；未完成 spawn、关闭中、已释放 owner 的保留记录、`exited` 但仍未从 registry 移除的记录或服务 teardown 均为未知。`kill` 失败可能清除 closing 标志而保留已退出记录，因此退出状态也不能替代完整进程范围的成功清理。任一来源读取失败、形状不符或 owner 枚举失败，整个终端计数为未知。
- 固定真实 web fixture 没有模型 PTY 插件，模板以 `terminalSources: "browser-only"` 显式声明；只有宿主与 Agent 服务查询成功确认缺席且浏览器 registry 可读时，空 registry 才是已知 0。没有声明的缺席仍为未知。该声明不会忽略实际出现的全局或 Agent 级模型 PTY。此适配绑定当前固定 DSH registry 形状，升级宿主后必须重新验收；形状变化会保守回报未知。
- **纯 DSH 的 IM 计数**：宿主服务查询成功确认没有 `dshIm`，且行配置声明 `imBots: "none"` 时，`activeImBots` 为已知 0；没有声明、查询接口不可用、查询抛错或服务读取失败时仍为未知。
- `HeartbeatRequest.activity` 的四个字段必须存在，非负整数为已知，显式 `null` 为未知；未知时照常发送心跳，保持已有 `lastInteractionAt`，不伪造空闲 0。采集过程整体失败会把历史计数改回未知。未知字段集合变化时用宿主 logger 告警一次（只含字段名），恢复全部已知时记录恢复；不输出令牌、计数或请求体。`health.ok` 独立反映已绑定的宿主 HTTP 服务是否实际监听，未绑定、停止监听或检查抛错均为 false；没有健康检查回调时也不能默认健康。
- workspace 刷新插件存活时间，将任意未知或 `health.ok=false` 作为回收保护，仍仅用真实正计数或 `lastInteractionAt` 推进活动时间。持续未知/故障不会因猜测超时而停机；恢复四个 0 且健康后立即清除保护，按原活动时间及新鲜的 CPU 观测正常回收。此契约接线不代替真实 DSH 隔离网络、原始心跳和完整生命周期的运行验收。
- 计数只在数值真正变化时通知心跳；`lastInteractionAt` 定义为"最近一次通过门锁的已验签 HTTP 请求或 WS 升级"，包括页面后台轮询和资源加载，**不是**严格意义的用户操作；已建立的 WS 内的帧（打字、发消息）不经门锁，不推进它（此时 `connectedClients>0`，平台按活跃处理）。计数初始化、正数增减、心跳与轮询刷新都不得推进该时间戳；免鉴权健康请求也不推进。
- 实例令牌与身份公钥文件对容器内同一 uid 的终端、Agent 可读，它们因此能伪造本实例心跳；只影响主人自己的实例，在威胁模型内接受。
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

fake-ip DNS 环境下，OIDC 域名可能被解析成保留的基准测试网段地址（如 `198.18.0.0/15`）；离线 OIDC 的 SSRF 地址校验会拒绝它。应在代理/DNS 侧给该 IdP 配真实 DNS 解析或使用明确支持的本地测试 IdP，不能为了测试关闭地址校验。`standalone` 只代表不连接 Auriga 平台，外部 OIDC 的 discovery、JWKS 和 token 交换仍需可达网络，不代表外部 IdP 可断网登录。

## 真实宿主验证

`deploy/compose/real-dsh` 固定 S4 的 DSH 0.2.0-rc.2 / Node 22.19.0 / npm 10.9.3 与完整依赖锁；使用本仓库 `auriga-dsh` 的正式 Cordis 入口。构建期安装，启动期只建实例状态目录并运行 DSH，不装依赖。运行、镜像摘要交付、严格 e2e 和当前阻塞见 [m2.md](m2.md#真实-dsh-与托管插件w3)。

纯 DSH 基线不包含原型的 `dshIm` 服务，不能把它缺席**默认**解释为 0 个在线机器人；真实模板用规格补丁 `plugins.auriga-dsh.imBots = "none"` 显式声明，同时为固定 web 组成声明 `plugins.auriga-dsh.terminalSources = "browser-only"`。终端采集回归按固定宿主 registry 形状构造，并经本机真实 HTTP fetch 检查心跳载荷；这些单元测试没有启动实际 PTY，不能据此宣称真实终端生命周期验收通过。Windows 没有 Docker CLI，WSL Ubuntu 已确认有本机 Docker；W3 的真实运行验收状态以脱敏证据为准，已有假 DSH CI 不能替代真实交互、闲置回收和闹钟唤醒验收。

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
