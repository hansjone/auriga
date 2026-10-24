# 云端原生 DSH 登录入口

`/instance-login.html` 是新增的云端静态入口，供成员完成表单认证并进入本人的 DSH。现有 `/login`、邀请、QR 确认、回调和独立管理台保持原入口；此批没有切换默认登录，也没有接入客户端插件或总部系统 DSH。

## 静态制品

入口为 `web/portal/instance-login.html` → `src/native-login/main.ts`。Vite 同时构建旧 `index.html` 和新 HTML；新入口直接复用插件 `src/login` 的原生 DOM 视图及 shared 的纯 API 子模块，不引入 Vue、React、Pinia、Element Plus 或完整 DSH 运行时。旧门户继续使用原有框架，不能将新入口依赖图的结论扩展到整个 portal 产物。

`deploy/compose/Dockerfile.edge` 显式复制新增 HTML，沿原构建流程生成 `web/portal/dist` 并交付到 `/srv/auriga/static/portal`。edge 现有 portal 静态根即可提供该入口及 `/assets/`，不新增网关 API、路由或 CORS 例外。

页面只包含局部布局和标准控件，字体继承，颜色消费已有 `--dsw-alias-*` 并以 inherit/currentColor/transparent 降级。此独立静态页没有 DSH 宿主，不能宣称已同步实例或 Desktop 的主题偏好；没有新增调色板、主题控制或系统偏好监听。DSH 内的原生组件主题行为保持原状。

## 认证和实例生命周期

本页拥有独立 API client，固定同源 cookie、`redirect:error` 与既有 CSRF 规则。它不向 storage、URL 或日志写入口令、TOTP 或令牌，也不创建平台会话。

1. 初次进入先 GET IAM me；真实 portal/current 会话且账号 active 时，进入实例准备。401 才读取现有提供者列表；其他错误留在本页，不作为匿名启动许可。
2. 选择提供者后创建 `purpose=login` 事务，returnTo 固定 `/instance-login.html`。整个事务保留在本页内存，仅将步骤交给 `createLoginView`。
3. 首批支持 form/fail/complete，包括服务端追加的 TOTP 表单；qr、redirect、webauthn、device 及未知步骤显示不可用，并提供固定 `/login` 的原有门户链接。旧门户按其原流程完成登录与路由，不承诺自动回到本独立 HTML。不能给该链接加本页 returnTo：旧 Vue router.replace 不会为未知 HTML 路由重新加载静态页面。本页不会发起外部跳转、轮询未支持步骤或将其视为 complete。
4. complete 必须同时属于 completed/login 事务，再次 GET me 验证真实浏览器会话，之后 POST `/api/workspace/v1/me/instance/open`。不传 owner 或用户选择的实例 ID，不重放密码/TOTP。
5. 单一生命周期观察单实例，间隔两秒，整个会话确认和实例准备最多两分钟。仅 `ready=true` 且 phase 为 running/pending_restart、无实例错误且没有停止/删除意图时，固定导航到 `/open/<编码后的实例ID>`。服务端 `redirectTo`、`entryUrl` 及用户 returnTo 均不作为外跳目标。
6. 拒绝、容量或频率限制、会话过期、实例失败、停止/删除、超时分别显示受控中英文状态。429 尊重 Retry-After，缺失或非法时至少等待一秒；无自动写重试。准备失败保留已确认身份，手动重试只确认会话并重新准备实例。

入口可恢复单个合法 UUID 的 `tx` query，并经真实 GET 事务验证 purpose、pending/terminal 状态与服务端 Cookie 绑定。pending 不探测旧会话或启动 DSH；expired/cancelled 不进入；completed 仍需 me。此功能不改变 IAM 原有未完成外部回调回 `/login?tx=…` 的行为，也不表示所有外部连接器已在新页接通。

## 取消和边界

workspace 请求传递 AbortSignal；取消、卸载、pagehide、超时均清理定时器与本页监听并使晚到响应失效。语言变化不创建新的认证/准备生命周期；认证 HTTP 在途期间语言选择暂时禁用，避免原生步骤视图的语言更新取消敏感表单。

现有 createIamApi 不接 AbortSignal。本页不会声称视图取消中止了 IAM HTTP：取消使其结果失效，在原请求结束后尽力取消 pending 服务端事务；在清理结束前不允许创建替代事务，避免晚到 Set-Cookie 覆盖新绑定。网络失败时无法证明服务端事务立即消失，仍由其自身到期机制约束；晚到认证成功也不会自动启动或进入 DSH。

客户端 Desktop 的 Set-Cookie 传输和身份会话绑定不在此入口范围。不得把此云端页面、原生步骤库、已有 DSH operator 会话或适配器视为客户端 Auriga 身份登录完成。

## 验证

portal 的 `test/native-login.test.ts` 使用真实原生 DOM、现有 API client 和协议形状，验证口令/TOTP→me→open→ready、刷新、恢复、失败、取消/晚到、重试及固定导航。`native-login-bundle.test.ts` 构建两份真实 HTML，遍历新 HTML 的 JS/CSS 和传递 JS 图，核对原生视图/纯 IAM 模块包含、无框架依赖及 Docker 新 HTML 输入。

在 portal 目录运行 `vitest run`、`vue-tsc --noEmit`、`vite build` 及相关 ESLint/Prettier 检查。测试中的 HTTP 响应是隔离夹具，不能替代真实 IAM/workspace/edge 后端栈、domain/IP+HTTP 浏览器、完整 DSH/WS 或部署验收。依赖 PR #107 未完成全 CI 和独立审查前，不把此实现称为可发布。
