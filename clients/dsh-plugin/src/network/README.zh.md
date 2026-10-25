# N1 插件装配边界

`runtime.js` 将生产 `ws` 工厂、NATS supervisor、根签名公钥清单、session、耐久 host ledger/local 分配器、防入侵策略及 consent 服务组合为一个 DSH 节点运行时。所有原生会话共享连接。发送工具的宿主参数为 `to/body/thread`，内部映射到已生成的 `AgentSendToolInput.target`，身份仅取 `exec.agent.session.id`。

`plugin.js` 在 connect 宿主中可选注入插件自有的 Cordis `aurigaNetwork` provider；它**不是 DSH 原生服务**，仓库当前没有提供该 provider 的生产实现。已有 Team MCP 登录/退出保持运行；缺 provider 时 N1 不建立连接、也不注册发送工具，状态为 `unconfigured`。不能据此宣称真实双 DSH 联网已交付。

provider 接口：

- `prepare({ accessToken, signal })`：沿原生登录设备凭据通道，完成权威 node 注册/准备并返回 `runtime-types.d.ts` 的 `PreparedNetwork`。只有权威首次安装结果可设置 `initialize=true`；本地 ledger 丢失不可据此重置 local 高水位。
- `ports`：`NetworkPorts` 定义了票据、固定可信端点公钥刷新、原生 HostPort、私有控制操作、来源绑定/目录、组织政策、提交及授权 outbox 发布端口。传入闭包或已绑定的方法，不接受浏览器/模型构造的端口。
- `prepareTicket`：每次连接与轮换请求新一次性票据，runtime 按 `connect-token.schema.json` 序列化 `auth_token`，按票据 SHA-256/base32 推导本次精确 up/down/reply subject。票据或访问令牌均不落盘、不进入 UI/诊断；普通失败由 supervisor 退避，401/403/吊销终止登录连接。
- `host`：必须实现固定 DSH 版本的真实 `HostPort`，包括 history/inbox 去重、显式 cwd/preset 创建与返回 true 的耐久 flush；不能将内存夹具注册为生产 provider。普通 prompt 不支持子 agent 的限制保持失败关闭。
- `network`：现有 host adapter 的登记、CONTACT_ASSIGN、在线 EXECUTION_ADMIT 端口。runtime 在 admit 外增加第二层 consent；同意对话不赋予自动执行权。ATTEMPT_BEGIN 直接通过已验签 session 发出，响应再次验签且核对 recipient/attempt。
- `submit/reconcileSubmission`：HTTPS 提交前将同一私有请求和幂等键耐久写入独立 outbound 文件；响应未知时保留，不盲目重放。恢复时只调用独立 reconcile 端口，最多保留 256 个未决提交。
- `publishConsent`：耐久 outbox 的幂等发布/重新核权端口，只有确认成功后才删除事件；其元数据包含经过验签、摘要绑定的首联信息。默认/撤销也须更新平台状态，不能只修改 UI。失败后事件保留，后续 snapshot/操作会重试。

真实 IAM 目前只公开内部 gRPC，没有客户端 HTTPS register/prepare；本次没有发明路径，也没有修改 IAM、网关、契约。根清单 HTTP 分发、上述生产 provider、真实宿主 HostPort 和完整双 DSH 验收仍由主控接线。managed 模式尚未提供设备级联网凭据通道，不把 instance token 当设备令牌使用。

本地 UI 通过 `/auriga/local-login/consent/{snapshot,decide,default,revoke}` 接真实 consent。路由先经过原生 admission/同源检查，再以当前 HTTP 请求绑定用户、检查本机登录与写请求 CSRF；浏览器不能声明主体或来源。视图沿用显式字段投影，不返回 ownershipRevision、授权证明、摘要、内部决策原因或 outbox。

退出、凭据刷新失效、明确设备吊销、provider 卸载都会立即关闭发送入口、取消连接与订阅，晚到的 prepare/READY/验签结果不能恢复旧登录。文件写入结果不确定时保持失败关闭。密钥刷新统计在 runtime diagnostics；已知版本的缓存过期/写入失败仍可受限刷新；刷新成功后从 committed 游标重扫，不跳过暂时未知密钥的消息。
