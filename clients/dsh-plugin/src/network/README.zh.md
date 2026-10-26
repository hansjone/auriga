# N1 插件装配边界

## 首次 READY 内部原语（尚未接线）

`session/bootstrap-ready.js#bootstrapReady({registration,transport,trust,signal})` 仅供受信内部调用；plugin、lifecycle、provider 默认入口均未调用。它不接令牌、不发 register/tickets、不建/关闭 transport、不读写 ledger/host、不发 FETCH/RECEIPT/LOCAL_REGISTER/SEND，也不设置 session.ready 或 runtime.connected。现完整注入 provider 仍走已知 incarnation 的原严格 `session.start`；runtime 顺序、allports、consent 和 SEND 未改变。本原语的测试通过不表示生产首启或真实双 DSH 已通。

调用方必须先经固定 IAM HTTPS register 校验本次 `{tenant,realm,node,device,created:true}`：tenant/device 等于当前真实设备 JWT，realm 等于受批准安装配置；transport 属于该设备新票建立的当前连接，trust 由安装根批准。helper 无法从这个普通对象证明来源，也不会从下行帧取得 created/tenant/device。信封没有 tenant/device，签名只直接证明 realm/node/incarnation/application epoch；tenant/device 的绑定依赖上述可信注册、票据和 transport 链。

每个 transport 同时只允许一次尝试；本次随机生成 requestId/channel_nonce/boot_id，捕获 transport.epoch 与 trust.version，恢复 hint 固定为空初值。候选 incarnation/正 uint64 epoch 先只作未可信验签参数，完整原对象（含未知可选扩展）交原严格 verifier 验签，固定 protocol_response、有效根批准业务键、realm/node、requestId/nonce/audience；再核正式 READY_RESULT 必需字段、恢复字段及 required_features，且 node_identity 与签名 audience 一致。应用 epoch 由签名接受，与客户端连接 epoch 无关。仅 ready 返回深冻结 `{identity:{tenant,realm,node,nodeIncarnation},readyResult}`；其他状态/ERROR/非 READY 均失败。超时默认 5 秒，取消、连接代际变化、异常、成功均退出自己的消息/abort 监听；刷新后及返回前重核当前尝试。

输出不赋业务权，也不是可持久保存或重复消费的初始化许可。后续首启接线须在同一单写者锁内，确认 ledger 缺失、本次 created 尚未消费且无写入未知，才可通过现有 `openLedger(...initialize:true)` 原子提交完整身份；完整落盘后才开放业务。若先获得 helper 结果再申请锁，锁内必须重查，只接受一致的已有完整账本，绝不覆盖。原 transport/trust 的交接或关闭也须单独评审。

created=false 且缺 ledger、首次响应丢失、落盘前崩溃/写入未知、后来丢 ledger 始终失败关闭，沿现有真实重新登录取得新 device/new node 恢复；不能缓存 true、刷新旧设备或重用 helper 输出恢复首次资格。现有 ledger 的重连仍从账本取得已知 incarnation，不用 bootstrap 覆盖。

### 官方设备 provider 首启接线前置

现服务端 `NetworkMailboxWireService.prepareReady` 将同一 ingress 绑定到第一次完整 READY；仅相同连接身份、完整请求及应用 epoch 的原样重放可通过。helper 返回后若在该连接换新 requestId、channel_nonce 或 boot_id，服务端返回 `network.conflict`。helper 也不返回可接管的 nonce/bootId/session，因此不能直接把其结果置为正常会话 ready。

“首次 helper → 同锁完整账本原子提交 → 同连接新上下文正常 READY”的装配还需要服务端明确支持该交接，或另行批准落账后持原锁、换新票和真实新连接再严格 READY。不能通过放松现有重放检查、伪造另一连接、回填 helper 上下文或提前显示 connected 绕过。该前置解决前，官方设备 provider 尚未接入默认入口，静态安装 pin 仍不代表已联网；本说明不声称完成数据库/WSS/真实 IAM 的整链验收。

2026-10-25 用户决定：同机隔离（含云端）整体推迟。`isolation.js` 是 runtime 与 HostPort adapter 共用的唯一隔离判定点，策略写在代码内，不读取工具参数、provider 配置或环境变量。local 与 managed 在登录、票据、信任根和真实宿主适配等条件满足后均可联网，无需先证明同机隔离。状态及 diagnostics 的 `isolation` 明确返回 `required:false / isolated:false / state:unisolated-development`；运行时首次启动记录一次“未隔离（开发阶段）”警告，重连不重复记录。

恢复隔离门禁前，须完成 local/managed 的进程、凭据、ledger 和 IPC 隔离验收并接入可信能力探测，再修改这一个代码判定点。profile 目录只用于耐久状态分区，不作为隔离证明；host 的去重、真实 flush、登录鉴权、授权及验签要求仍有效。本项策略变更不补齐下述生产 provider/managed 设备凭据缺口，也不代表真实双 DSH 验收通过。

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

真实 IAM 已提供设备 Bearer 专用的 `/api/iam/v1/network/register` 与 `/api/iam/v1/network/tickets`；固定根清单与公告端点分别为 `/api/collab/v1/network/keys`、`/api/collab/v1/network/keys/announcement`。这些正式控制面契约与首次 READY 原语已存在，但默认入口的官方设备 provider、公告持久门槛及同锁首建尚未接通。真实宿主 HostPort 和完整双 DSH 验收仍由后续任务接线。managed 模式尚未提供设备级联网凭据通道，不把 instance token 当设备令牌使用。

本地 UI 通过 `/auriga/local-login/consent/{snapshot,decide,default,revoke}` 接真实 consent。路由先经过原生 admission/同源检查，再以当前 HTTP 请求绑定用户、检查本机登录与写请求 CSRF；浏览器不能声明主体或来源。视图沿用显式字段投影，不返回 ownershipRevision、授权证明、摘要、内部决策原因或 outbox。

浏览器固定使用上述认证 HTTP 桥，不读取同名 Cordis 服务，其他浏览器插件不能替换 consent 后端。授权元数据全局最多 256 条、每个已验签来源最多 8 条、最长 24 小时；普通 delivery 只保留当前页所需的临时指针，宿主回执确认后释放耐久暂存。容量/冲突属于单条拒收，返回统一 `network.target_unavailable` 回执并留本地审计，不能关闭整个运行时。contact_prompt 仅走 consent 的拒绝事件，由发布端驱动协议终态，不伪造宿主接受回执。

退出、凭据刷新失效、明确设备吊销、provider 卸载都会立即关闭发送入口、取消连接与订阅，晚到的 prepare/READY/验签结果不能恢复旧登录。文件写入结果不确定时保持失败关闭。密钥刷新统计在 runtime diagnostics；已知版本的缓存过期/写入失败仍可受限刷新；刷新成功后从 committed 游标重扫，不跳过暂时未知密钥的消息。

工具在 READY 成功后注册，断连、票据错误或握手失败时撤销。runtime 通过 `onClosed(reason)` 在存储清理结束后通知 lifecycle；意外停机显示 unavailable，并按 500ms 至 30s 的有界退避重建。退出/吊销会使 generation 失效，不触发旧身份重建。故障诊断只含固定原因计数与最多 64 条摘要。
