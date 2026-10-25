# N1 插件装配边界

`runtime.js` 将生产 `ws` 工厂、NATS supervisor、根签名公钥清单、session、耐久 host ledger/local 分配器、防入侵策略及 consent 服务组合为一个 DSH 节点运行时。所有原生会话共享连接。发送工具的宿主参数为 `to/body/thread`，内部映射到已生成的 `AgentSendToolInput.target`，身份仅取 `exec.agent.session.id`。

`plugin.js` 在 connect 宿主中可选注入插件自有的 Cordis `aurigaNetwork` provider；它**不是 DSH 原生服务**，仓库当前没有提供该 provider 的生产实现。已有 Team MCP 登录/退出保持运行；缺 provider 时 N1 不建立连接、也不注册发送工具，状态为 `unconfigured`。不能据此宣称真实双 DSH 联网已交付。

provider 接口：

- `prepare({ accessToken, signal })`：沿原生登录设备凭据通道，完成权威 node 注册/准备并返回 `runtime-types.d.ts` 的 `PreparedNetwork`。只有权威首次安装结果可设置 `initialize=true`；本地 ledger 丢失不可据此重置 local 高水位。
- `ports`：`NetworkPorts` 定义了票据、固定可信端点公钥刷新、原生 HostPort、私有控制操作、来源绑定/目录、组织政策、提交及授权 outbox 发布端口。传入闭包或已绑定的方法，不接受浏览器/模型构造的端口。
- `prepareTicket`：每次连接与轮换请求新一次性票据，runtime 按 `connect-token.schema.json` 序列化 `auth_token`，按票据 SHA-256/base32 推导本次精确 up/down/reply subject。票据或访问令牌均不落盘、不进入 UI/诊断；普通失败由 supervisor 退避，401/403/吊销终止登录连接。
- `host`：必须实现固定 DSH 版本的真实 `HostPort`，包括 history/inbox 去重、显式 cwd/preset 创建与返回 true 的耐久 flush；不能将内存夹具注册为生产 provider。普通 prompt 不支持子 agent 的限制保持失败关闭。
- `network`：现有 host adapter 的登记、CONTACT_ASSIGN、在线 EXECUTION_ADMIT 端口。runtime 在 admit 外增加第二层 consent；同意对话不赋予自动执行权。ATTEMPT_BEGIN 直接通过已验签 session 发出，响应再次验签且核对 recipient/attempt。
- `submit/reconcileSubmission`：HTTPS 提交前将同一私有请求和幂等键耐久写入独立 outbound 文件；明确拒绝立即删除记录，结果未知保留最多 10 分钟，不盲目重放。全局最多 256 条、每个来源绑定最多 16 条，满时仅对此会话背压；恢复时对同一记录最多调用 3 次 reconcile，次数先落盘。没有 reconcile 端口时同样按操作/重启惰性清理过期记录。过期、重试耗尽或旧格式记录缺失年龄时记 outcome-unknown 诊断并释放元数据，不能宣称远端操作成功或重新发送。
- `publishConsent`：耐久 outbox 的幂等发布/重新核权端口，只有确认成功后才删除事件；每批仅附事件对应的 `requestId/digest/recipientIds`，不附其他来源、预览或原始签名帧。默认/撤销也须更新平台状态，不能只修改 UI。失败后事件保留，后续 snapshot/操作会重试。

真实 IAM 目前只公开内部 gRPC，没有客户端 HTTPS register/prepare；本次没有发明路径，也没有修改 IAM、网关、契约。根清单 HTTP 分发、上述生产 provider、真实宿主 HostPort 和完整双 DSH 验收仍由主控接线。managed 模式尚未提供设备级联网凭据通道，不把 instance token 当设备令牌使用。

本地 UI 通过 `/auriga/local-login/consent/{snapshot,decide,default,revoke}` 接真实 consent。路由先经过原生 admission/同源检查，再以当前 HTTP 请求绑定用户、检查本机登录与写请求 CSRF；浏览器不能声明主体或来源。视图沿用显式字段投影，不返回 ownershipRevision、授权证明、摘要、内部决策原因或 outbox。

浏览器固定使用上述认证 HTTP 桥，不读取同名 Cordis 服务，其他浏览器插件不能替换 consent 后端。授权元数据全局最多 256 条、每个已验签来源最多 8 条、最长 24 小时；普通 delivery 只保留当前页所需的临时指针，宿主回执确认后释放耐久暂存。容量/冲突属于单条拒收，返回统一 `network.target_unavailable` 回执并留本地审计，不能关闭整个运行时。contact_prompt 仅走 consent 的拒绝事件，由发布端驱动协议终态，不伪造宿主接受回执。

退出、凭据刷新失效、明确设备吊销、provider 卸载都会立即关闭发送入口、取消连接与订阅，晚到的 prepare/READY/验签结果不能恢复旧登录。文件写入结果不确定时保持失败关闭。密钥刷新统计在 runtime diagnostics；已知版本的缓存过期/写入失败仍可受限刷新；刷新成功后从 committed 游标重扫，不跳过暂时未知密钥的消息。

工具在 READY 成功后注册，断连、票据错误或握手失败时撤销。runtime 通过 `onClosed(reason)` 在存储清理结束后通知 lifecycle；意外停机显示 unavailable，并按 500ms 至 30s 的有界退避重建。退出/吊销会使 generation 失效，不触发旧身份重建。故障诊断只含固定原因计数与最多 64 条摘要。
