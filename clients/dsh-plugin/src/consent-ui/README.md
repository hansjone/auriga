# 首联授权 UI（本地可信端口）

界面沿现有 `src/client.js` 的 ModuleLoader/React 和 `main`、`settings.section` 注册。
收件箱是功能页内的折叠区；授权默认值和持续名单在原生统一设置中。
不新增浏览器入口、独立框架、模型工具或平台 API。

## 主控需要完成的真实接线

当前基线未创建 consent 运行时，也没有浏览器到本地 consent 的认证桥接。
界面通过客户端 Cordis `ctx.get('aurigaConsentUI')` 查找**插件自有端口**；
它不是已经存在的 DSH 原生服务。端口缺失时只显示未接入，不显示模拟请求。
后续需在客户端注册这个服务，提供以下异步方法：

- `snapshot()`：使用 `createConsentUIAdapter(...).snapshot()` 的投影。
- `decide(input)`、`setDefault(layer, mode)`、`revoke(input)`：映射同名适配方法。

这要求建立认证的本地宿主桥，不能将 Node 对象直接当作浏览器服务，
不能绕过宿主 Connection/Origin、真实本地用户校验与写请求 CSRF 防护。
服务缺失时不要回退总部 `fetch`、全局对象、localStorage 或 MCP。
`plugin.js` 尚未修改，不能将此 UI 交付描述成真实双 DSH 首联闭环。

## 适配器输入

- `consent`：已经打开并绑定当前安装身份的真实 `openConsent` 实例。
- `context`：运行时提供、由 `authorizeLocal` 校验的真实本机用户上下文；
  不得接受浏览器自报身份，不向浏览器泄露上下文或凭据。
- `preview(request)`：仅返回与该可信请求 ID、层、digest、期限绑定的纯文本预览；
  只能走用户预览通道，不将正文送入 system/agent inbox 或模型。
  UI 和适配器均限制为 1000 个 UTF-16 代码单元，不解析 HTML/Markdown/链接。
- `organizationPolicy()`：当前目标 DSH 的可信组织上限投影
  `available/revision/conversation/execution`；后两项为 `allow/ask_each/deny_all`。
  不能从浏览器、正文或缓存的用户填写值生成；组织不可用则快照失败关闭。
  若上限依来源而异，接线必须提供保守的全局上限，逐请求仍由 consent 复核。

适配器先经 consent 验本地身份，再读取组织上限和预览。默认值、名单和策略版本
均来自 consent，不在 UI 保存权限。每次决定使用最近快照的 policyRevision；
失败只允许重新读取，不自动重试批准。持续允许/屏蔽必须明确选 node 或 user。
组织收紧时禁改该层默认值，并展示组织版本；这只是 UI 提示，实际权限仍由服务复核。
撤销按 ruleId 和层精确调用；成功后重读快照，拒绝冷却和已发生副作用不受 UI 撤销影响。

## 验证边界

`test/consent-ui.test.js` 执行实际注册组件的元素树与事件处理器（轻量 hook harness），
并调用真实 consent 服务验证适配层。不是完整 React DOM、真实宿主安装或网络端到端验收。
未引入 React 测试依赖、未更改现有 ModuleLoader 声明或图标解析夹具。
