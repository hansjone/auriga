# 云端原生退出

`/logout.html` 是独立的门户退出确认页。它只调用现有 IAM 会话接口，不停止 DSH 实例或任务，也不创建新的授权流程。

## 入口与确认

- 页面加载只 GET `/api/iam/v1/me`，要求真实 portal/current 会话；确认前不会 POST logout。查询参数中的 confirm、logout、returnTo、entryUrl 均不参与行为。
- 匿名或 401 显示“尚未登录或登录已失效”，与已成功退出区分。admin/device 或非当前会话不提供退出确认；读取失败或非 JSON 会话响应不允许注销。界面只用普通登录/退出文案，audience 等内部区别留在实现中。
- 用户点击“确认退出”后，复用 shared 纯 API 的 `createIamApi().logout()`，POST `/api/iam/v1/logout`。本页使用独立 client、固定同源路径、浏览器 Cookie、现有 `X-Auriga-CSRF` 和 `redirect:error`。无 owner/body、Bearer、跨源请求或自造注销 token；CSRF Cookie 缺失时不伪造替代。
- 本页专有 fetch 封装要求 logout 成功必须是未重定向的 204；200 JSON、200 HTML、空 200、202 或重定向均不能显示已退出。403、500、网络错误显示受控失败并由用户明确重试，401 则显示会话已失效。错误 detail/title 不进入页面。
- 上述错误分类只依据本页 fetch 实际收到的 HTTP Response.status，由本页私有错误类型传递；不依赖 shared ApiError.status 或错误正文的 status。真实 403/500 即使正文写 401，仍失败封闭并隐藏重新登录；真实 401 即使正文写 403/500，仍显示登录失效。无响应的网络错误和重定向异常不能作为失效证明；不可信错误正文不参与认证状态决策。
- 204 后清本页认证状态，展示“已退出登录”和固定 `/instance-login.html` 重新登录链接，不自动跳转或准备实例。此链接仅在退出成功或已失效时可见；确认、读取、失败、取消和结果未知状态只提供固定 `/` 返回入口及适用的刷新状态/重试操作，避免仍登录时点“重新登录”意外准备实例。真实会话撤销及 HttpOnly Cookie 清理由 IAM 负责，前端不伪造清 Cookie。其他返回参数和响应 Location 不作为导航目标。

新原生登录页 `/instance-login.html` 只在 GET me 已确认门户身份后展示真实 `/logout.html` 锚点，匿名、错误 audience 或会话失效时隐藏。实例准备失败仍可退出；点击锚点离开时沿原 pagehide 清理轮询和晚到导航。旧默认 `/login`、邀请、QR 确认、管理台页面保持原样。

## 生命周期与主题

重复确认、语言变化不会并发提交。取消使当前读取或提交结果失效；请求结束前不能开启另一请求，取消后需要明确重新读取会话再确认。pagehide/卸载移除本页监听和 DOM，晚到成功/失败不恢复视图或导航。

现有 IAM logout API 不提供 AbortSignal；本页没有声称取消能中止服务器注销。取消已提交请求时提示“退出结果尚未确认”，在原 HTTP 结束后允许重新读取状态。重新读取只 GET me，即使得到仍有效会话，也要再次明确确认才 POST。

两个原生 HTML 的入口各为当前 document 维护一套 pagehide/pageshow 监听：初次脚本执行立即挂载，初次 pageshow 不重复挂载；pagehide 处置旧控制器，后续 pageshow 若已无控制器则重新创建并读取当前状态。连续 pageshow 不创建多份页面或请求；入口显式 dispose 会同时移除监听。依据 [MDN pageshow 生命周期](https://developer.mozilla.org/en-US/docs/Web/API/Window/pageshow_event)，该事件既覆盖首次加载，也覆盖浏览历史恢复，不把 persisted 标志本身当身份依据。

退出页恢复只重新 GET me，不恢复旧成功提示、不重放注销，仍须用户确认。登录页恢复则沿其既定 me/open/ready/固定进入流程，未改变默认导航。旧控制器已处置，其未完成 HTTP 的晚到结果不能覆盖新页面或导航。此修正针对真实后退空白问题，组件事件测试不等于测量浏览器实际 BFCache 命中或完成真实后端联调。

页面使用原生 DOM/ESM、标准控件、字体与 DSH tokens 继承，缺 token 中性降级。没有额外配色、主题开关或 OS 监听；独立静态门户页不宣称共享 DSH 实例主题偏好。

## 制品与验证

`web/portal/logout.html` → `src/native-logout/main.ts`；Vite 在保留原 index/instance-login 两入口的基础上增加第三 HTML。`deploy/compose/Dockerfile.edge` 单独显式 COPY logout HTML，沿原 portal dist/edge 静态目录交付，不新增网关 API 或部署配置。

新增测试用真实 DOM、shared API client 与协议形状覆盖只读 GET、会话确认、204/非204、错误、显式重试、重复点击、语言变化、取消/晚到、pagehide/卸载及固定登录锚点。所有退出测试逐项限制请求为 GET me 或 POST logout，不能调用实例 open/start/stop。登录页追加退出链接的身份可见性用例，原 60 项断言保留。

`native-logout-bundle.test.ts` 实际构建三入口，解析退出 HTML 的全部 JS/CSS 与传递模块图，验证纯 IAM 依赖、无 Vue/React/Pinia/Element、无 native-login/workspace 启动模块，并核 Docker 输入。旧门户仍保留自己的框架依赖，不能称整个 portal 无框架。

在 portal 目录集中运行 `vitest run`、`vue-tsc --noEmit`、`eslint .`、`prettier --check .` 与 `vite build`。

## 后续边界

现有 IAM session_revoked → edge 取消长连接、旧 ticket/实例 binding 回查失效链直接复用；此批不新增逐 ticket 撤销协议。测试使用隔离响应，不代表真实后端、实例 Cookie/API 或已建立 DSH WebSocket 失效已联调。

实例插件到门户的可信返回地址投递缺口属另项；本页没有猜测 portal URL、在实例域相对调用 IAM logout 或新增成员代理。新退出页不代表实例内账户按钮已经接通。本地继续按既定 dsh-team 登录/退出复用方案处理，用户不会在此看到“登录总部”或新授权向导。
