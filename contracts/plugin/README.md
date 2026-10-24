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
- **心跳**：插件每 30 秒、以及活动计数变化时（2 秒去抖）调用 `POST {AURIGA_PLATFORM_URL}/api/workspace/v1/instance/heartbeat`，`Authorization: Bearer <实例令牌>`，上报执行中的轮次、打开的终端、在线的 IM 机器人和浏览器连接数量，见 `contracts/openapi/workspace.yaml`。四个计数字段始终必需；非负整数表示已知计数，显式 `null` 表示未知，缺字段和负数无效。未知时继续上报，不伪造 0。workspace 仅在四项明确为 0、`health.ok=true` 且 CPU 观测也新鲜空闲时回收；未知或健康故障保护运行并刷新插件存活时间，但不会推进用户活动时间。恢复已知健康空闲后按原闲置时间恢复回收。原有全整数请求继续有效。
- **团队工具（ADR 0016）**：
  - 不需要 DSH 会话上下文的团队工具，由 DSH 自带的 mcp-client 连 ai-gateway 的 MCP（`transport: streamable-http`），节点代理按实例规格的 `mcp_servers` 渲染：键即 DSH 配置里的 `serverName`（`[A-Za-z0-9_-]{1,32}`），请求头固定 `Authorization: Bearer <令牌>`，令牌用 `!!js` 表达式从 `token_ref` 对应的文件读取，不把令牌值写进 YAML。原生客户端的请求头在配置求值时固定：令牌轮换由节点代理触发插件配置重载（新旧令牌有重叠期），不能指望 401 后自动重读。
  - 需要会话上下文的团队工具（例如团队任务）由 `auriga-dsh` 用 `ctx.tools` 注册，执行时读取 `exec.agent.session.id`，用插件自己的 MCP 客户端转发给同一个 ai-gateway，每个请求重读令牌文件；上下文放在 `params._meta`：`auriga/sessionId`（DSH 会话 id，不透明字符串，不按 UUID 校验）、`auriga/toolCallId`、`auriga/rootCallId`。缺上下文时拒绝执行。
  - 这些元数据只是关联信息，不是凭据：平台先用令牌确定实例、主人和租户，再校验"会话—任务"绑定。同一个业务工具只从一条路径暴露，不在两边重复注册。
- **团队 SDK**：插件向其他 DSH 插件提供 `ctx.get('auriga')`，只有它是对插件承诺稳定的接口；SDK 的接口定义随 P1 发布，版本跟协议主版本走。

### 受信托管 Loader 配置

`managed-config.schema.json` 定义节点渲染的托管插件配置，`browserOrigin` 直接引用
`deployment/access.schema.json#/$defs/Origin`，必须完整给出 `{scheme, host, port}`。
这是受信安装配置，严格拒绝未知字段、null、缺失字段以及旧 `trustedHosts` 配置；不是可宽容消费的跨版本业务消息。
插件启动还校验规范 IPv4 或小写 ASCII DNS/A-label，不接受 URL、userinfo、路径、查询、片段、编码 host、尾点或歧义 IP。
`imBots: none` 和 `terminalSources: browser-only` 仍是显式活动观测声明。

agent 从 `AURIGA_DEPLOYMENT_ACCESS_FILE` 加载与 edge 相同的只读 `access.json`，使用既有 `deploymentconfig.Load`；
无受信文件的 managed 实例运行期启动失败。仅 `fake` 控制驱动保持无实例 HTTP 配置的原行为。
旧 `AURIGA_AGENT_INSTANCE_PLATFORM_URL`、`AURIGA_AGENT_INSTANCE_PARENT_DOMAIN` 和
`AURIGA_AGENT_DOCKER_EDGE_CONTAINER` 设置时明确报迁移错误，须删除并迁移至同一 access 文件，不能覆盖安装边界。
域名模式按 `instances` 的 scheme、port 和每实例子域派生 browserOrigin；IP 模式使用 portalOrigin。
`AURIGA_PLATFORM_URL` 仅序列化 `instanceControl.origin`，供原 self/heartbeat 内部通道使用，内部 alias 不进入浏览器白名单。
公开 Compose 目前仍使用 bootstrap 的固定 domain 测试配方；公开用户输入和双模式真实浏览器验收由后续 IP2d/IP3 交付。

renderer 拥有 `browserOrigin` 整个子树，profile/spec patch 不能覆盖；`webserver`、`web-runtime`、`connection`
行继续受保护。DSH 0.2.0-rc.2 的 `web-runtime.trustedHosts` 只接原生支持的精确 `host:有效port`，包括默认端口。
Auriga 在合法签名门锁通过后仍校验完整 scheme/host/有效port；原生 authority 白名单不能替代 scheme fence。
上游 loopback/IP Host 仍可由节点重写，无 Origin 的已签名导航或 server-to-server 行为不变；
带 Origin 时必须与唯一 browserOrigin 完全匹配，不能因本地 Host、内部 alias 或有效签名放行别的浏览器源。
重复/opaque/null/畸形 Origin、错误 scheme/端口和 cross-site Fetch Metadata 均拒绝；未通过门锁仍走原生认证拒绝。

类型使用固定的 `json-schema-to-typescript 16.0.0` 生成；在仓库根执行 `pnpm --filter auriga-dsh gen:steps`。
其新增部分的准确命令（插件目录执行）为：

```sh
json2ts -i ../../contracts/plugin/managed-config.schema.json -o src/generated/managed-config.d.ts
prettier --write src/generated/managed-config.d.ts
```

`portalOrigin` 可选，直接引用同一部署 `Origin`，由 renderer 从已验证的 `access.portalOrigin`
完整写入 Loader row；profile 不能覆盖该字段或任意子字段。插件启动严格校验并保存独立快照，
仅在已通过实例签名门锁与原有 browserOrigin 检查的 `GET /auriga/bootstrap` 返回结构化地址。
它只用于后续账户操作导航，不是浏览器身份白名单或平台 API 代理授权；不含凭据与内部平台地址。
兼容旧 row 时缺失该字段意味着禁用门户入口，不从 hostname、issuer、请求头、query 或
`AURIGA_PLATFORM_URL` 推导。当前只交付配置链路，尚未接入退出 UI 或真实 IAM/WS 联调。

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

团队面板是 Vue 组件编译成的 Web Component，跟平台一起发版，插件只负责挂载（ADR 0017）。DSH 里的入口用客户端扩展点：侧栏 `sidebar.panellist` 加"团队"，主区域 `main` 挂面板页（这套扩展点在 DSH 0.2 还没有稳定承诺，外壳要尽量薄，并在 CI 里用 DSH 冒烟测试）。

- 面板必须是**自包含**的 ES module（可带懒加载分片，都要列进 `integrity`），运行时不从别的源取代码。
- 平台在 `/panel/` 下提供面板文件，入口是 `GET /panel/manifest.json`，格式见 `panel-manifest.schema.json`：版本、协议、入口脚本、每个文件的 SRI 摘要、平台签名。
- 签名：Ed25519，对去掉 `signature` 字段后的清单做 JCS（RFC 8785）规范化再签。面板签名公钥由连接清单 `panel.keys` 下发。
- 防回滚：清单带 `issuedAt` 和 `expiresAt`；插件拒绝过期的清单，也拒绝版本号低于已加载版本的清单。
- 文件路径只能是 `integrity` 里列出的相对路径（不含 `..`），`entry` 和全部懒加载分片必须都在 `integrity` 里；不支持独立 `styles`，CSS自包含在模块中。
- **托管模式**：edge 在实例自己的源上提供 `/panel/`（同源静态加载），插件验清单签名，浏览器按 SRI 校验脚本。静态文件同源不授予平台成员API身份。账户与敏感操作跳转门户源；实例源不获得门户cookie/通用JWT、平台不为实例源开放CORS。首批未批准任何托管成员API桥接，`context.fetch` 对该模式须拒绝平台API调用；未来最小只读能力需单独契约和威胁评审（D6、ADR0021）。纯IP路径弱隔离入口也不能借本段自动增加面板代理权限。
- **连接模式**：插件（Node 侧）下载清单和文件，先验签、再逐个校验摘要，然后经插件自己的本机路由同源提供给页面；校验失败拒绝加载并显示错误。面板调平台接口也经插件的本机路由代发，设备令牌只在 Node 侧，**页面脚本永远拿不到**，因此平台不需要为 DSH 的来源开 CORS。
- 自定义元素名 `auriga-team-panel`，用 Shadow DOM 隔离样式。宿主（插件的面板外壳）设置元素属性 `context`：

```ts
interface AurigaPanelContext {
  mode: 'managed' | 'connect';
  protocol: 1;
  locale: string;               // 'zh-CN' | 'en' …
  theme: 'dark' | 'light';
  apiBase: string;              // 平台地址，只用于显示和拼外链，不是 fetch 的目标
  fetch(path: string, init?: RequestInit): Promise<Response>; // 只接受以 / 开头的平台 API 路径，凭据由宿主处理；面板不得读取或保存令牌
}
```

  - 宿主在主题或语言变化时**重新赋值整个 `context` 对象**；面板要处理"元素定义前宿主已设置 `context`"的情况。
  - 面板向宿主发出的事件（`CustomEvent`，冒泡、可穿过 Shadow DOM）：`auriga-navigate`（`detail.to`：`session:new` 或 `http(s)://` 外链，其他忽略）、`auriga-notify`（`detail.level`：`info` | `success` | `warning` | `error`，`detail.message`）、`auriga-auth-required`（宿主负责提示重新登录，面板自己不弹窗）。
  - Element Plus 的弹层必须留在 Shadow DOM 里（`teleported=false`、对话框 `append-to` 面板内元素），不用挂到 body 的 `ElMessage`。
