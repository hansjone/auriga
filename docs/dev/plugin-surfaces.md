# 插件原生页面与安装边界

Auriga 插件有登录、功能、设置三部分；管理台是独立应用。本批完成原生功能页、独立设置页及默认安装的未配置页面，**未完成插件原生登录接线**。

## 宿主承载与设计对应

- 功能页沿 `window.__ModuleLoader__` 取得宿主 React。`sidebar.panellist` 与 `main` 共用 `auriga-team` id/key，侧栏只有一个 Auriga 入口。`auriga-team-panel` 是原生 Custom Element，内部只有“资源市场／我的资源”两个水平页签。
- 设置页单独注册 `settings.section`，id 为 `auriga-team`、order 为 22。DSH 拥有设置弹窗、左侧列表、关闭按钮、Esc 与焦点管理，插件只提供右侧 Auriga 内容，不拦截全局导航或自行制造弹窗。
- 两页分别对照设计目录的 `index.html`、`resources-2026-10-23.html` 及 README。设置采用“总部连接／工具／关于”的分组与细分隔设置行，复用 DSH 原生按钮和 `--dsw-alias-*` 变量，不导入管理台主题、Vue、Element Plus 或私带 React。
- 设置打开、刷新和关闭不更新功能组件的 bootstrap/context，不重建资源元素。功能元素在同一连接只改变语言／主题时保留当前页签、搜索与已读数据；真实连接变化则取消请求并清空旧数据。

## 真实状态与版本

managed 沿原有受保护 `/auriga/bootstrap` 读取状态；服务端仍先经原生 `requestRejection`，再取得已验签、未过期、未回滚的 manifest。新增 `pluginVersion` 来自插件实际 package/runtime 版本，界面版本来自已验证 `manifest.version`。二者独立取值，不复用示例常量。

设置只显示系统托管、协议、版本、界面来源及加载状态。“已读取托管配置与已验证的界面清单”只表示该次 bootstrap 成功，不证明成员资源授权、安装能力或账户安全接线。内部平台 URL、身份与设备令牌、刷新令牌、密钥均不进入设置状态、DOM 或浏览器持久化。响应 detail、未知错误码与 fetch 抛出的任意 reason 被固定中英文类别代替。

错误区分登录失效、无权访问、宿主入口未接、协议不支持、清单验证失败与不可达。重试仅重新读取，不保存、不自动重放写请求。两页各自有请求代次、超时和 AbortController，晚到结果不能更新已卸载页面。脚本 SRI、元素定义等待、通知计时器及 slot/locale/theme/面板事件注册均有清理。

每个已加载 Custom Element 构造器绑定该次来源、入口与 SRI；不同包或签名清单不能复用先前构造器。浏览器无法注销 Custom Element，跨版本／来源更新需刷新 DSH 页面；managed 失败不会回退到本地包。

## 默认独立安装

发布准备在 monorepo 的 `clients/dsh-plugin` 执行 `npm pack`，`prepack` 运行 `scripts/package-ui.mjs`：构建 `web/panel` 的同一份原生模块，将 JS 与 `metadata.json` 放进包内 `ui/`。metadata 的 UI 版本来自 panel package，SRI 为实际文件 SHA-384；生成目录不纳入源码版本控制，`files` 明确包含它。

已发布包安装时没有 install/postinstall 钩子，不需要 Vue、前端构建链、monorepo 或总部页面。原有 Node 依赖仍只有 `@noble/hashes` 与 `undici`，没有新增第三方运行依赖。`prepack` 是发布准备步骤，不能误当作安装端步骤；源码 checkout 若尚未准备 UI，路由返回明确 503，而不是伪造可用页面。

未设置 `AURIGA_MODE` 时 Cordis 默认提供 standalone 的只读本机页面，不再直接 no-op；显式 connect 也只显示未配置状态。本机 `/auriga/bootstrap` 与固定 `/auriga/local-panel.js` 复用 DSH 原生 `requestRejection` 鉴权，只支持 GET。缺少宿主鉴权服务则拒绝安装路由；未知路径、遍历与写请求不放行。包内文件缺失或摘要不符返回脱敏 503。

本机 bootstrap 明确 `status=unconfigured`、`localPanel.source=package`、`fetchBase=null`。包内 SRI 证明安装资产一致性，**不是平台签名**。它不会产生 Auriga 身份、平台会话或资源 fetch 能力。托管模式仍只用原有签名 manifest/SRI 加载链，不使用本机路由作为降级通道。

## 资源能力保持边界

保留既有 resource reader、契约类型及 33 条资源行为测试：只读列表／游标／详情／版本、身份匹配、路径拒绝、负向 HTTP 分类、HTML/坏 JSON 拒绝、取消／晚到／超时、失联清旧数据、401 composed 事件等。原 2 条面板测试迁到原生挂载，并新增语言／主题保状态、键盘页签测试。

只有已有 connect 宿主提供真实 `context.fetch` 时，reader 才能发资源 GET；现插件 connect 尚未建立该通道。managed 资源读取仍封闭。我的资源明确无法读取安装清单，不从市场推断本机安装状态；安装／启停／上传／发布／工具部署没有模拟按钮或假空目录。

## 登录、认证与原设置待接线

旧 DSH 体验只读依据为 `oclaw/dsh-team/lib/web/gate/gate.js`、`login/desk-view.js`／`desk.js`：共享登录卡片、二维码与过期重试、Passkey 交给其所属 Hub 域的系统浏览器、应急账号，并在成功后通知宿主。旧 `AuthSettingsSection`／`AccountSettings` 含账号、认证关联、安全、设备与协作偏好。

Auriga 当前真实组件分布如下：

- `clients/dsh-plugin/src/identity/core.js` 的 `beginLogin`／`advanceLogin` 是已有独立身份核心；Cordis 未挂这些登录事务的 HTTP 路由，也未挂原生登录页面。
- 现公共门户入口为 `web/portal/src/views/LoginView.vue`、`router.ts`，由 `web/shared/src/steps/StepContainer.vue` 呈现 form、qr、redirect、webauthn、device、fail、complete。它们仍为门户 Vue，不能算作插件原生第三页完成。
- managed 主人身份仍由既有 edge 门户会话／实例入口及插件门锁确认；`infra/edge/access_proxy.go` 的公开登录操作与 `access.go` 的 Origin/CSRF 边界未改。插件只显示重新登录提示，没有猜测总部 URL 或直接把平台身份放进 DSH。
- connect 的真实设备授权、受保护凭据保存、清单与资源传输、独立身份 HTTP/CSRF/session 装配，以及旧账号／安全功能迁移需另项完成。设置保留这些能力的明确说明；不把 URL＋Key 演示表单或 localStorage“已配置”标志当作后台能力。
- 默认工具部署位置尚无真实保存与创建接口，设置说明保留设计归属，不实现假保存。

后续原生登录应复用已有 ClientStep/StepInput 与原 DSH 登录体验，需要主控协调插件身份 HTTP、门户／edge 返回入口及会话装配。此批不改上述保护路径或新增凭据协议。

## 验证与限制

插件 Node 测试含原有 140 项与新增 bootstrap／本机路由 6 项。壳的 21 条 Node 行为测试放在 `web/panel/test/shell-surfaces.node.mjs`，由 panel test 脚本在原生 Vitest 37 项后显式执行，文件名避开 Vitest glob。这样 Plugin workflow 的 `--filter auriga-dsh...` 不会依赖根开发用 jsdom；Web workflow 原有完整 workspace 安装提供锁定 jsdom，无新依赖。

`test:bundle` 在空白 JSDOM 执行实际产物，核 Shadow DOM、主题、资源封闭、无 Vue 标记、无 runtime package dependencies 与 40 kB 预算。隔离 npm tar 安装验证包内资产、SRI、无安装钩子、默认未配置 bootstrap、鉴权拒绝及卸载注销。组件／路由夹具不代表真实 DSH 登录、前端视觉、平台身份或部署验收；宿主设置的真实关闭／Esc／焦点仍需主控在真实 DSH 验收。

旧规范需主控另项同步：`contracts/plugin/README.md:108`、`docs/adr/0017-team-panel-loading.md:9` 与 `:38` 的 Vue 表述。本批按用户明确原生化要求实现，不擅自修改这些受保护规范。
