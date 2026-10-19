# 0017 团队面板：Web Component 的两种加载方式

- 状态：已采纳
- 日期：2026-10-19
- 依据：`spikes/s2-panel/`（可运行原型 + 32 个 Node 测试，DSH `0.2.0-rc.2`）

## 背景

团队面板由平台用 Vue 写成 Web Component（`auriga-team-panel`，Shadow DOM），跟平台一起发版；DSH 插件只负责在 DSH 界面里挂载它。托管模式浏览器经 edge 访问 `/i/<实例 id>/`；连接模式用户电脑上的 DSH（浏览器或 DSH Desktop，来源 `dsh-app://app`）要从平台取面板包、验签后本机加载。S2 要回答：DSH 给插件的扩展点是什么、两种模式怎么加载、样式隔不隔得住、令牌怎么处理、协议要改什么。

## 调查结论

### 1. DSH 的扩展点（源码引用均指 `D:\project\chatgpt\DeepSeekHarness`）

- 插件是一个 npm 包：Node 侧入口（Cordis 插件，`apply(ctx)`）+ 浏览器侧 `./client` 导出，`package.json` 里声明 `"dsh": { "client": { "platform": "web" } }`。浏览器侧是一个脚本，调用 `window.__ModuleLoader__.load({ id, factory(require) { return { inject, apply(ctx) } } })`；`require('react')` 等基线模块由外壳提供。样例：`apps/web/tests/fixtures/plugins/fixture-live-client/client.js`、`fixture-input-extension/client.js`；契约说明 `packages/client/AGENTS.md`（"Shared modules and the module graph"）、`docs/subsystems/client-modules.md`。
- 加面板/标签页/侧栏入口靠 Slots（`docs/subsystems/slots.md:109-188` 的"Current hierarchy"）：
  - 侧栏入口：`sidebar.panellist`（list，root 作用域，需 `id`、`label`，`packages/client/ui-sidebar/src/client/contract/slots.ts:35`；渲染见 `SidebarRoot.tsx:59-77`）。
  - 主区域页面：`main`（keyed，key 与 panellist 的 `id` 相同，`packages/client/ui-layout/src/client/index.ts:73`、`AppFrame.tsx:40-42`）。
  - 现成的参照：`ui-schedule` 同时注册这两个槽（`packages/client/ui-schedule/src/client/index.ts:168-184`）。
  - 其他可用：右侧栏标签 `sidebar.right.pane.tab`（keyed，session 作用域，用 `ctx.sidebarRightTabs.register`，同文件 155-167）、`shell.overlay`（通知）。
- 贡献方式：`ctx.slots.inject(<槽名>, () => ctx.slots.register({ name, id|key, locale, label }, Component))`；插件拿到的是 React 组件，所以挂 Web Component 就是渲染 `<auriga-team-panel ref>` 并用 ref 设属性。
- 宿主侧（Node）：`ctx.webServer.register({kind:'prefix', path, handler})`（`packages/host/webserver/src/index.ts:166`）注册路由；`ctx.connection.requestRejection(req)` 明确写着"把 Connection 的 Host/Origin 检查和浏览器认证用到另一个 Web 路由上"（`packages/client/connection` 的 `rpc.d.ts`），插件路由不需要自己做认证。
- 加载方式：开发期 `dsh web --patch <overlay.yml>`（`docs/user/develop/basic/index.md`），发布期 `dsh plugin --profile <名> add <包>`，包里 `dsh.bundle.patch` 带补丁（`publish.md`）。
- **稳定性**：这套客户端扩展点只存在于 `0.2.0-rc.2`，文档把 UI 包称为"expected to be rewritten wholesale"（`packages/client/AGENTS.md`，Presentation components），没有语义化版本承诺或弃用策略，也没有"第三方扩展点"的稳定性声明。风险见下"风险"。

### 2. 托管模式：同源加载、SRI、样式隔离（浏览器里实测，原型 `--mode managed`）

- 浏览器页面在 `http://<edge>/i/demo/`，面板文件在同源 `http://<edge>/panel/`；`<script type="module" src integrity>` 加载成功，SRI 由浏览器执行（`--fault file` 时脚本被拦，不执行）。
- DSH 的外壳用 `base: './'` 和相对 URL（`apps/web/vite.config.ts:171`），所以 `/i/<id>/` 前缀下整个 DSH 可用；插件的浏览器侧用 `new URL('auriga/bootstrap', document.baseURI)` 自动适配前缀。
- DSH 页面**没有任何 CSP**（响应头和 `<meta>` 都没有，实测；`apps/web`、`webserver` 源码里也没有）。
- Shadow DOM 样式隔离，两个方向都实测（面板里的"运行隔离自检"，全部 PASS）：
  - 面板 → 宿主：`document.styleSheets` 里没有 `.el-*` 规则，`document.querySelector('.el-button')` 为空；面板里的 Element Plus 不改变 DSH 任何样式。
  - 宿主 → 面板：宿主加 `.el-button,button{background:red!important}`、`*{font-family:cursive!important;color:lime!important}`，面板按钮、字体、颜色都不变。
  - 两个坑，已在原型里解决：
    1. **继承穿透**：`*{font-family:…!important}` 会命中 host 元素，继承进 Shadow；且宿主的 `!important` 压得过 `:host{}` 里的普通声明。复位（`all: initial`）必须放在 Shadow 内部的根节点上（`.auriga-root`），不能放 `:host`。
    2. **Element Plus 的变量写在 `:root` / `html.dark`**，Shadow 里不生效。构建时改写成 `:host` / `.auriga-root.dark`（`panel/src/main.ts`）。
- Element Plus 弹层：默认挂到 `document.body`，Shadow 里没有样式。设 `:teleported="false"`（`ElSelect` 等）和 `ElDialog :append-to="<shadow 内容器元素>"` 后，下拉、对话框、遮罩都在 Shadow 内，样式、键盘、定位正常（`.el-popper` 在 body 下为 0，在 shadow 内为 1）。注意：`position:fixed` 的遮罩以 DSH 主区域为包含块，对话框遮罩只盖主区域，不盖侧栏（可接受）。**`ElMessage` / `ElMessageBox` / `ElNotification` 会直接挂 body，面板内禁用**，通知一律走 `auriga-notify` 事件。
- 自包含产物：Vue + Element Plus + 全部 CSS 打成一个 ES module（1.9 MB，gzip 465 KB），清单里只有一个文件要 SRI，没有"样式另外注入"的问题。构建时 CSS 用 `adoptedStyleSheets`（不受将来可能出现的 `style-src` CSP 影响），无此 API 时退回 `<style>`。体积可后续按需引入组件样式优化。

### 3. 连接模式：浏览器里怎么加载

对照（DSH 页面 `http://127.0.0.1:<p1>`，平台 `http://127.0.0.1:<p2>`，实测）：

| 做法 | 结果 |
|---|---|
| 页面直接 `import(平台 URL)` / `<script type=module crossorigin>` | 没有 CORS 头时被拦（`Failed to fetch dynamically imported module`）。要平台给 `/panel/*` 配 CORS 白名单（DSH Desktop 的来源 `dsh-app://app` 必须在内）。`import()` 不支持 SRI。 |
| 页面 `fetch` 平台 + 浏览器里算 SRI + Blob URL `import()` | Blob URL `import()` 可行（无 CSP）。但验签/验摘要要 `crypto.subtle`，只在安全上下文里有：`localhost`/`127.0.0.1`/`dsh-app://` 是，`http://<局域网 IP>` 不是；Ed25519 的 WebCrypto 支持也不普遍。验证放进页面也意味着验证代码和被验证的代码在同一个信任域。 |
| **经插件的本机 HTTP 路由转发（采用）** | 插件（Node）下载、JCS + Ed25519 验签、逐文件 SRI，全通过才放进内存包；浏览器只访问同源 `/auriga/panel/*`。不需要平台 CORS，不依赖 WebCrypto，`dsh-app://` 下同样可用，浏览器还会再按清单 SRI 校验一次。 |

- 验证全部在 Node 里做（`node:crypto`，Ed25519 + SHA-384），代码 `plugin/lib/verify.js`、`panel-store.js`；失败给稳定错误码（`signature_invalid`、`integrity_mismatch`、`key_unknown`、`protocol_unsupported`、`plugin_too_old`、`path_invalid`…），整包要么可用要么一个文件都不出。测试覆盖：RFC 8785 向量、改一个字节的清单、改签名、错密钥、kid 未知、密钥过期、协议/最低版本、缺摘要、路径穿越、SRI 不匹配、超大文件、重定向、平台不可达、文件缺失。
- 路由由 `ctx.webServer.register` 挂在 `/auriga`，每次请求先过 `ctx.connection.requestRejection`（DSH 自己的 Host/Origin 围栏 + 会话 cookie），所以本机别的网页（DNS 重绑定/CSRF）打不到。
- **DSH CSP 会不会挡**：不会，DSH Web 页面和 Desktop 都没有对主文档设 CSP（Desktop 自带页面 `renderer/*.html` 有 CSP，但那是更新对话框等自带窗口，不是应用文档）。
- **DSH Desktop**：本机安装了（`%LOCALAPPDATA%\Programs\DeepSeek Harness`，0.2.0-rc.2）。没有在里面实际加载外壳插件：要加载就得改用户真实的 DSH 配置目录/配置，这超出了原型的边界。用源码论证：`apps/desktop/src/main.ts:132-142` 注册 `dsh-app` 为 `standard, secure, supportFetchAPI, corsEnabled`；`main.ts:662-677` 把应用文档以外的所有请求（含 `/auriga/*`）交给 `forwardWebRequest`（`web-document.ts:77-93`），原样转发到 Host 并带上 Host 的认证 cookie，只放行 `Origin: dsh-app://app`。所以 `dsh-app://app/auriga/panel/…` 在 Desktop 里等价于同源，且 `/plugins/` 动态客户端包（插件本身）本来就走这条路。结论：同一份代码在 Desktop 里应可用；落地前需要在 Desktop 里验一次（列入 S2 后续项）。
- 还需注意：Desktop 转发会丢掉 `set-cookie`（`WITHHELD_RESPONSE_HEADERS`），所以**插件路由不能靠 `Set-Cookie` 做任何会话**，只能依赖 DSH 自己的认证。

### 4. 面板调平台接口：令牌怎么处理

- **托管**：浏览器直接 `fetch('/api/...')`，带平台会话 cookie（`HttpOnly; SameSite=Lax`，edge 校验）。令牌根本不在浏览器脚本里；edge 转发到实例前剥掉 `auriga_*` cookie 和 `Authorization`（原型的模拟 edge 已做，平台日志证明实例侧看不到）。
- **连接**：`context.fetch` 指向插件的同源路由 `auriga/platform/<平台路径>`；插件在 Node 里加 `Authorization: Bearer <设备访问令牌>`。令牌从不进入页面（测试：`bootstrap`、代理响应里都不含令牌；页面带来的 `cookie`/`Authorization` 被丢弃；`set-cookie`/`www-authenticate` 不回传；只允许平台 `/api/` 路径，拒绝 `..`、绝对 URL、反斜杠；重定向一律拒绝；请求体 ≤ 1 MiB）。这条路径还有一个好处：不需要平台为 `dsh-app://app`/loopback 配 CORS。
- 对比"注入 `context.fetch` 的闭包里持有令牌"：闭包里的令牌页面里任何脚本都能通过调试/猴子补丁取到；代理做法把令牌留在 Node 进程，**最简单且不暴露**。两种模式面板代码相同——都只调 `context.fetch('/api/…')`，区别在宿主怎么实现 `fetch`。
- 访问令牌 5 分钟过期怎么刷新：插件 Node 侧持有刷新令牌，代理在 401 前主动刷新（原型用固定令牌，`getToken()` 是每次现取的函数，留好了接口）。

### 5. 面板和宿主的约定够不够

原型用的 `context` 和事件够用，但需要这些修改（见下）：`mode`、`protocol`、`locale`、`theme`、`apiBase`、`fetch` 都用上了；`auriga-navigate`（`session:new`、`https://…`）、`auriga-notify`、`auriga-auth-required` 都在 DSH 里实测可用。

## 决定（提议）

1. **统一用"插件验证 + 浏览器 SRI"**：托管、连接两种模式都由插件（Node）对面板清单验签（托管模式只验清单，连接模式验清单并下载校验全部文件）；浏览器侧永远用 `<script type="module" integrity>` 加载。托管模式文件在 edge 同源 `/panel/`，连接模式由插件在 DSH 同源 `/auriga/panel/` 提供。
2. 插件对外提供三条路由（都在 DSH 认证之后）：`GET /auriga/bootstrap`（模式、面板位置、已验证清单）、`GET /auriga/panel/*`（仅连接）、`ANY /auriga/platform/*`（仅连接，加设备令牌）。
3. 浏览器侧外壳用 `sidebar.panellist` + `main` 两个槽挂载（参照 `ui-schedule`），`shell.overlay` 显示通知。
4. 样式隔离规则：自包含产物；`all:initial` 放在 Shadow 内部根节点；Element Plus 变量改写到 `:host`；弹层 `teleported=false` / `append-to=<shadow 内元素>`；面板内禁用 `ElMessage*`。
5. 令牌只在插件 Node 进程里，页面脚本不接触。

## 对插件协议第 5 节和 `panel-manifest.schema.json` 的修改建议

协议第 5 节：
- 加载流程改写为上面的决定 1、2；明确"连接模式不经页面跨源加载，所以不需要平台为 DSH 来源开 CORS"；第 4 节第 7 条（CORS 白名单）对面板不再必要。
- `context` 的 `fetch` 语义写清：只接受以 `/` 开头的平台 API 路径（拒绝绝对 URL）；凭据由宿主决定；面板**不得**自己读取或保存任何令牌。
- `context.apiBase` 降级为"仅用于显示和拼外链"（连接模式不是 fetch 的目标）。
- 事件补充：`auriga-navigate` 的 `detail.to` 取值需要枚举（原型用 `session:new` 和 `http(s)://`，其他忽略）；`auriga-notify` 的 `level` 枚举 `info|success|warning|error`；`auriga-auth-required` 之后宿主负责提示重新登录，面板自己不弹窗。
- `locale` 的取值（原型：`zh-CN`、`en`）；`theme` 变化时宿主**重新赋值整个 `context` 对象**（面板的响应式在属性赋值上）。
- 元素属性须在元素定义前后都有效：面板要处理"定义前宿主已设置 `context`"（原型 `upgradeProperty` 已处理，需要写进约定）。
- 新增启动路由 `GET /auriga/bootstrap` 属于插件内部，不属于平台协议，不必进契约；但面板错误码列表要给界面做文案。
- 面板必须是**自包含**的单入口 ES module（可再带懒加载分片，但都要在 `integrity` 里），禁止运行时再从别的源取代码。
- 密钥：连接清单 `panel.keys` 用 identity-keys 同形的 JWKS 子集（原型就是这样），支持 `kid`、`nbf`、`exp` 轮换。

`panel-manifest.schema.json`：
- `styles` 删除（自包含后不需要；若保留则必须说明由谁注入、是否也进 `integrity`）。
- 注明 `integrity` 的键必须覆盖 `entry` 和所有懒加载分片，路径只允许 `[A-Za-z0-9._-]` 段、无 `..`（插件已按此校验）。
- `entry` 和 `integrity` 的键加 `pattern`，防止路径穿越；`integrity` 加 `minProperties: 1`。
- `signature` 增加可选 `signedAt`（Unix 秒），用于回滚检测；`version` 建议加 semver 的 `pattern`。
- 本原型生成的清单已用 Ajv 校验符合当前 schema（`test/verify.test.mjs`）。

## 后果与风险

- DSH 客户端扩展点还在 rc 阶段、无稳定承诺：外壳的浏览器侧（`client.js`）会随 DSH 升级变动。缓解：外壳极薄（约 200 行），固定支持的 DSH 版本区间，CI 里用本原型的 harness 做冒烟（启动 DSH → 看到面板），升级 DSH 前先跑；和 DSH 上游沟通是否把 `main` + `sidebar.panellist` 作为稳定扩展点。
- 连接模式的面板在内存里整包缓存 60 秒（失败缓存 3 秒）；平台发新版后最多 60 秒内换新。
- DSH Desktop 未实际验证（见上）。
- 托管模式下浏览器直连 edge `/panel/`：文件被篡改时靠 SRI 拦截并显示错误（已实测），但清单本身要靠插件验签，所以插件必须能拿到面板公钥（托管模式的密钥文件，沿用 `AURIGA_IDENTITY_KEYS_FILE` 的做法，另加面板密钥文件）。
- 设备令牌刷新、实例令牌等真实凭据存储不在本原型范围（用环境变量/固定令牌代替）。

## 采纳说明（主控，2026-10-19）

- ADR 0020 之后实例使用独立的源：托管模式下 `/panel/` 由 edge 在实例自己的源上提供，仍是同源加载；
- 协议第 5 节和面板清单 schema 按上面的建议修改（契约同步更新）；
- DSH Desktop 里的实际加载留到 P3（连接模式）时验证。
