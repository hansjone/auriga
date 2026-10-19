# 插件协议 v1

DSH 插件 `auriga-dsh`（npm，MIT）和平台之间的约定。三种模式见 `docs/architecture/platform.zh.md` 第六节：离线版 `standalone` 不连平台（只用身份源协议，见 `contracts/identity`）；托管 `managed` 和连接 `connect` 用本文的协议。

## 1. 协议版本

- 当前主版本 **1**。插件发往平台的每个请求都带 `Auriga-Protocol: 1`，并带 `User-Agent: auriga-dsh/<插件版本> dsh/<DSH 版本>`。
- 平台同时支持当前和上一个主版本。更旧的版本返回 **426**，`Problem.code = plugin.protocol_unsupported`，`params = { "current": 1, "minSupported": 1 }`；插件提示用户升级，团队功能停用，本地功能照常。
- 心跳响应里也带 `protocol.current` 和 `protocol.minSupported`，插件据此提前提示升级。

## 2. 身份头（平台 → 实例，托管模式）

浏览器访问实例地址（`https://<实例 id>.<实例域>/…`，只有 IP 的部署是 `/i/<实例 id>/…`，见 contracts/README.md 的路由一节）时，edge 校验实例 cookie 和对应的门户会话、确认会话账号是实例主人，然后经节点代理把请求转给实例，并加上身份头：

```
Auriga-Identity: <JWS 紧凑格式>
```

- JOSE 头：`{"alg":"EdDSA","kid":"<密钥编号>","typ":"auriga-id+jwt"}`，Ed25519 签名。
- 声明见 `identity-token.schema.json`：`iss="auriga-edge"`、`aud=<实例 id>`、`sub=<账号 id>`、`tid`、`sid`、`iat`、`exp`（不超过 iat + 60 秒）、`jti`、`htm`、`htu`、`pv=1`。
- edge 转发前**删除**客户端带来的 `Auriga-Identity`、`Authorization`，以及名字含 `auriga_` 的 cookie（包括 `__Host-auriga_inst`，平台凭据不进实例）。

插件（门锁）对每个 HTTP 请求和 WebSocket 升级请求必须：

1. 按 `kid` 从身份头公钥文件里找公钥，验签；找不到 kid 时重新读一次文件再试；
2. 检查 `aud` 等于本实例 id、`sub` 等于主人 id（`AURIGA_OWNER_ID`）、`pv` 是支持的版本；
3. 检查 `iat`、`exp`，允许 5 秒时钟偏差；
4. 检查 `htm` 等于请求方法、`htu` 等于请求路径。`htu` 的定义：节点代理转发给实例时请求行里的**原始** path——同源模式下已去掉 `/i/<实例 id>` 前缀、不做百分号解码、不含查询串；
   `jti` 只用于日志关联，插件不要求去重（重放窗口由 `exp`、`aud`、`htm`、`htu` 限制）；
5. 任一项不通过返回 401，不泄露原因细节；节点代理的健康检查路径 `/_auriga/health` 除外。

WebSocket 只在升级时校验一次身份头；edge 收到 `identity.session_revoked` 后必须立即关闭该会话（`sid`）的全部 WebSocket 连接。

### 身份头公钥文件

节点代理把校验公钥写进 `AURIGA_IDENTITY_KEYS_FILE`（实例目录 `meta/identity-keys.json`），格式见 `identity-keys.schema.json`（JWKS 子集）。文件里是"当前 + 下一把"两把公钥；轮换时 edge 先切到下一把签名，旧公钥过期后再移除。插件监听文件变化并重新读取，**不需要重启实例**。

## 3. 托管模式的实例环境

节点代理渲染的环境变量：

| 变量 | 含义 |
|---|---|
| `AURIGA_MODE` | `managed` |
| `AURIGA_PROTOCOL` | `1` |
| `AURIGA_INSTANCE_ID` / `AURIGA_OWNER_ID` / `AURIGA_TENANT_ID` | 实例、主人账号、租户 |
| `AURIGA_PLATFORM_URL` | 实例调平台用的内部地址（edge 内部入口），例如 `https://edge.internal` |
| `AURIGA_IDENTITY_KEYS_FILE` | 身份头公钥文件路径 |
| `AURIGA_INSTANCE_TOKEN_FILE` | 实例令牌文件路径（只有实例账号能读） |
| `DSH_HOME` | DSH 数据目录 |
| `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` | 出网代理 |

- **健康检查**：插件在 DSH 端口上提供 `GET /_auriga/health`（节点代理设计文档里的 `/_health` 即指它）（不要求身份头，只回 `{"status":"ok","ready":true,"protocol":1,"pluginVersion":"…"}`），节点代理据此判断 ready。
- **心跳**：插件每 30 秒、以及活动计数变化时（2 秒去抖）调用 `POST {AURIGA_PLATFORM_URL}/api/workspace/v1/instance/heartbeat`，`Authorization: Bearer <实例令牌>`，上报执行中的轮次、打开的终端、在线的 IM 机器人数量，见 `contracts/openapi/workspace.yaml`。workspace 合并节点代理的 CPU 观测后判断闲置。
- **团队 SDK**：插件向其他 DSH 插件提供 `ctx.get('auriga')`，只有它是对插件承诺稳定的接口；SDK 的接口定义随 P1 发布，版本跟协议主版本走。

## 4. 设备登录（连接模式）

用户电脑上的 DSH 接入平台，用 OAuth 2.0 设备授权流程（RFC 8628），客户端是内置的公共客户端 `auriga-dsh`：

1. 插件 `POST {平台}/oauth2/device_authorization`：`client_id=auriga-dsh`、`scope=openid offline_access auriga.connect`；
2. 在 DSH 里显示 `user_code` 和 `verification_uri`（门户的 `/device` 页面）；用户在已登录的浏览器里核对设备信息并确认（门户调用 `POST /api/iam/v1/device/{userCode}/approve`）；
3. 插件按 `interval` 轮询 `POST {平台}/oauth2/token`（`grant_type=urn:ietf:params:oauth:grant-type:device_code`），拿到访问令牌（JWT，`ptyp=device`、带 `did`）和刷新令牌；
4. **刷新令牌就是设备凭据**：每次刷新都轮换；插件在本机加密保存（Windows 用 DPAPI，macOS 用钥匙串，Linux 用权限 0600 的文件 + 本机密钥）；
5. 用户或管理员在"我的设备"里吊销后，刷新返回 `invalid_grant`，插件清除本地凭据并提示重新登录；
6. 登录后插件调用 `GET /api/workspace/v1/connect/manifest` 取团队 MCP、团队模型和面板信息，按清单接入；
7. 连接模式下插件只放行来自本机 loopback 的访问，并校验 `Host` 必须是 `127.0.0.1`、`[::1]` 或 `localhost`（加端口），防 DNS 重绑定。平台的 CORS 白名单放行 DSH Desktop 的来源 `dsh-app://app` 和 loopback 来源，但对 loopback 来源不返回 `Access-Control-Allow-Credentials`，只接受设备 Bearer 令牌。
8. 批准设备时，门户的确认页必须让用户**手动输入或逐字核对** `user_code`，并显示发起端的 IP 和大致位置；批准要求 10 分钟内做过二次验证（防设备码钓鱼）。
9. 调团队 MCP 和团队模型用 `aud=auriga.ai-gateway` 的访问令牌（用刷新令牌按 RFC 8707 `resource` 参数换取）；清单里的 MCP 和模型地址必须与平台 `apiBase` 同源，插件拒绝其他地址。

## 5. 团队面板加载

团队面板是 Vue 组件编译成的 Web Component，跟平台一起发版，插件只负责挂载（**草案，S2 验证后定稿**）。

- 平台在 `/panel/` 下提供面板文件，入口是 `GET /panel/manifest.json`，格式见 `panel-manifest.schema.json`：版本、协议、入口脚本、每个文件的 SRI 摘要、平台签名。
- 签名：Ed25519，对去掉 `signature` 字段后的清单做 JCS（RFC 8785）规范化再签。面板签名公钥由连接清单 `panel.keys` 下发。
- 防回滚：清单带 `issuedAt` 和 `expiresAt`；插件拒绝过期的清单，也拒绝版本号低于已加载版本的清单。
- 文件路径只能是 `integrity` 里列出的相对路径（不含 `..`），`entry` 和 `styles` 必须都在 `integrity` 里。
- **托管模式**：同源加载，按清单里的 `integrity` 用 SRI 校验脚本。
- **连接模式**：插件下载清单和文件，先验签、再逐个校验摘要，然后从本机加载（例如经 `dsh-app://`）；校验失败拒绝加载。
- 自定义元素名 `auriga-team-panel`，用 Shadow DOM 隔离样式。宿主（插件的面板外壳）设置元素属性 `context`：

```ts
interface AurigaPanelContext {
  mode: 'managed' | 'connect';
  protocol: 1;
  locale: string;               // 'zh-CN' | 'en' …
  theme: 'dark' | 'light';
  apiBase: string;              // 平台地址
  fetch(path: string, init?: RequestInit): Promise<Response>; // 宿主负责带凭据
}
```

  面板向宿主发出的事件（`CustomEvent`，冒泡、可穿过 Shadow DOM）：`auriga-navigate`（`detail.to`）、`auriga-notify`（`detail.level`、`detail.message`）、`auriga-auth-required`。
