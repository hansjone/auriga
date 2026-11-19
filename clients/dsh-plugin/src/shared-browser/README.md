# 共享原生浏览器组合

`auriga-dsh/shared-browser` 只用于共享 profile。与 `auriga-dsh/shared` 一起加载，配置 `distIndex` 为既有 `frontend-static` 的真实绝对 `index.html`；保留正式 DSH frontend dist 与 client 模块。独立实例继续使用正式单成员 profile，不加载本插件。

入口 GET `/`、`/index.html` 需要当前 Edge 签名 Identity，读取真实页面后用公开 `webServer.renderIndex` 注入非 owner 权限快照和 Connection `__DSH_TRANSPORT__`。不使用 Connection owner browser admission，不生成 owner cookie。消息设置安装在原生 sidebar/main slots，原有 Auriga 页面保持原行为；资源管理链接使用门户 `/resources`。

原生调用经同源 POST `/auriga/shared/browser/rpc`，流经 `/auriga/shared/browser/stream`；Edge 必须为每次请求签发精确 method/path 的新 Identity。浏览器不持 Identity 或签名密钥，不从请求体选择成员。Host 的 `admitBrowser` 返回 runtime 正式登记的非 owner Peer；新建会话经 Host prepare/register/publish，已有会话前后在线授权。流用真实 Gateway `wireStream.open`，每帧及空闲期间重新验证，取消、退出、epoch 变化或授权服务故障会终止旧流。

使用锁定 rc.3 的公开扩展点：Connection `__DSH_TRANSPORT__.rpc.call/open`、Gateway `dispatchRpc` / `wireStream.open/failure`、RemoteAccess `snapshotFor/invalidate`、webServer `renderIndex/register`、原生 slots。未修改上游包或升级 baseline。只允许会话所需动作与本人消息设置，拒绝 owner 回执 `$events/result`、MCP、凭据管理、任意插件和终端能力。模型目录沿原生拒绝结果返回，不伪造模型或回复。

必要 Host 接口为 `admitBrowser,authorize,refresh,create,messagingSessions,messagingSettings`。`refresh` 必须在线校验本人 placement，并从实时及公开持久化会话列表预热已登记本人绑定；旧 owner 无 W 登记的会话不加入。native policy 的本人 session role 需支持 `session/page,prompt,cancel,follow,projections,list,control` 及 `event:api-session/{activity,added,error,removed,status}`。`list/control` 原生控制器入口需要额外有限 global role：`resource:{kind:'global'}`、`role.resource:'global'`、`actions:['session/list','session/control']`；进入后控制器再逐会话按同动作 `remoteAccess.allows()` 过滤。其余 global action 均不授予，create 永远由本 carrier 截到可信 Host 流程。

**部署前必须配置 Edge 围栏**：共享 Open URL 为 `/`，私有监听只允许 Node 转发；对外禁止 `/api/**`、`/connect`，包含 `/api/remote.mux` 的 WebSocket upgrade。rc.3 原生升级 admission 会认旧 owner cookie，不能靠增加 Identity 头修复。插件另拒绝旧 `/api` HTTP connection/request，但不能替代升级围栏。

纯模块验证：设置 `AURIGA_DSH_HOST_ROOT` 指向锁定 rc.3 的 app，设置 `AURIGA_REACT_ROOT` 指向可解析已安装 React 18/React DOM 的目录，然后 `node --test test/shared-browser/*.test.js`。测试实际使用公开 Peer、RemoteAccess、Gateway、原生事件源及 SessionController.list/control 实现；持久化行、projection 行和模型执行为 fixture，没有启动 DSH、PG、NAS 或供应商。正式正常页面聊天／取消／恢复由主控串行验证。
