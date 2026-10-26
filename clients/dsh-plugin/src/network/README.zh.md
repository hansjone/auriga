# N1 插件装配边界

## 官方设备基础连接

connect 安装具备完整 `network` pin 且原生设备登录成功后，`plugin.js → createConnectRuntime → createDeviceProvider → lifecycle → openNetworkRuntime(kind: device-connection) → openConnectionRuntime` 自动装配官方 provider。根只取安装 realm/SPKI；固定平台源上的 register、tickets、完整 keys、announcement 均使用现 `getPlatformAccess` 与已验证当前设备身份闭包，拒绝 cookie、重定向、响应超预算及迟到的失效代际。不增加可见登录或审批。票据严格核完整设备身份与随机票派生的 ingress、含 shard 的 up、down/reply，兑换截止与最长连接截止分开处理；每次新连接均取新票。无安装 pin 且无完整注入 provider 的行为仍为 unconfigured。

基础分支不创建 consent，不注册发送工具，不 FETCH/ACK/SEND 或发布授权；consent 入口明确不可用。`connected` 仅代表基础认证连接，不能据此声称双 DSH 收发或业务能力已完成。完整可信 Cordis provider 可暂时替代官方默认项，撤销注入后恢复默认；替换/退出先失效旧状态，等旧实例停止、在途提交结束并释放锁后才允许下一实例开账本。完整业务模式的 allports 检查与原 session 路径保留。

## 第二能力阶段：显式登记当前会话

只有基础连接通过严格 READY，且公开 Host 的读取、活对象核验、归属和耐久能力齐备后，才注册零参数 `auriga_network_join`。参数必须是空对象；调用会话只取真实 `exec.agent.session`，并核对公开 registry、resolveAgent、inspect/stat、cwd、归档与子 agent 归属。缺能力只关闭登记入口，基础 connected 保持独立。登出、断线、换 epoch、密钥公告刷新及宿主卸载立即注销工具，在途调用逐边界检查原代际。

登记沿基础连接持有的同一个 writer handle 和账本进行，不重开、不另建账本。实际等待 `hasDurableProvider`、`sessions.flush`、provider flush 和公开 read-back，再将稳定原操作、宿主映射摘要和 local 预留耐久保存，沿既有 LOCAL_REGISTER 等待完整签名 REGISTER_RESULT。回包校验用途、受众、READY epoch/nonce、requestId 和原 op/type/revision；原 fingerprint 由已保存请求与 requestId 绑定，回包不含 fingerprint 字段。凭据及不可变 binding 完整提交后才返回 `{status:"registered",address:"aur1:…"}`；失败仅返回 `{status:"unavailable"}`。不会返回宿主 id/cwd、token 或来源信息。

同会话并发与重试使用同一原操作，不重复分配。未知结果保留队首，重连首先按原操作对账；READY 只核对本地已有 pending 能解释的计数差异，不下载服务端事实或提升本地高水位。已登记但未绑定时须重新验证当前原生对象和原映射后恢复。新 binding 使用 receive_none/inbox_only，不建立消息关系、不授来源、SEND 或执行权。发送工具不会隐式登记，未登记时提示“请先调用 auriga_network_join”；完整发送能力仍受原有独立门禁约束。

测试分列公开 npm API 兼容、真实 NATS WSS 接缝和 Java/PG 服务端证据。夹具签名往返不等于全链路 Java/PG 验收，更不代表双 DSH 收发已通。

插件目录下运行 `node --test test/network/session-registration.test.js test/network/connection-runtime.test.js`。按 `deploy/compose/real-dsh/package-lock.json` 安装隔离基线后，运行 `node --test test/network/host/native/upstream-check.mjs test/network/session-registration-upstream.mjs`，核公开类型、真实工具 registry/SessionStore/JSONL/WSS 与文件账本。测试只使用临时目录和随机端口，controller 为公开 API 形状的最小适配，不调用模型。`node test/network/session-registration-mutations.mjs` 在独立临时副本中分别去掉 awaited flush 与旧代 fence，要求坏实现均通过 tsc、随后被特定断言检出；原生产文件保持原样。

`session/bootstrap-ready.js#bootstrapReady({registration,transport,trust,signal})` 由基础首建路径显式调用。helper 本身不接令牌、不发 register/tickets、不建/关闭 transport、不读写 ledger/host、不发 FETCH/RECEIPT/LOCAL_REGISTER/SEND，也不设置 session.ready 或 runtime.connected。原语测试或控制面夹具不替代真实 IAM/org 的跨服务验收。

调用方必须先经固定 IAM HTTPS register 校验本次 `{tenant,realm,node,device,created:true}`：tenant/device 等于当前真实设备 JWT，realm 等于受批准安装配置；transport 属于该设备新票建立的当前连接，trust 由安装根批准。helper 无法从这个普通对象证明来源，也不会从下行帧取得 created/tenant/device。信封没有 tenant/device，签名只直接证明 realm/node/incarnation/application epoch；tenant/device 的绑定依赖上述可信注册、票据和 transport 链。

每个 transport 同时只允许一次尝试；本次随机生成 requestId/channel_nonce/boot_id，捕获 transport.epoch 与 trust.version，恢复 hint 固定为空初值。候选 incarnation/正 uint64 epoch 先只作未可信验签参数，完整原对象（含未知可选扩展）交原严格 verifier 验签，固定 protocol_response、有效根批准业务键、realm/node、requestId/nonce/audience；再核正式 READY_RESULT 必需字段、恢复字段及 required_features，且 node_identity 与签名 audience 一致。应用 epoch 由签名接受，与客户端连接 epoch 无关。仅 ready 返回深冻结 `{identity:{tenant,realm,node,nodeIncarnation},readyResult}`；其他状态/ERROR/非 READY 均失败。超时默认 5 秒，取消、连接代际变化、异常、成功均退出自己的消息/abort 监听；刷新后及返回前重核当前尝试。

输出不赋业务权，也不是可持久保存或重复消费的初始化许可。基础 runtime 先取得原 host store 单写者 handle，再注册并在锁内 load 一次；已有完整账本先核结构、原身份及权威 tenant/realm/node，从原账本取 incarnation。缺账本仅当前真实 created=true 可以用 helper 验签结果，经原 emptyState/handle.commit 原子首建。写入未知消费本次许可并关闭，不能重复 initialize。

created=false 且缺 ledger、首次响应丢失、落盘前崩溃/写入未知、后来丢 ledger 始终失败关闭。普通重新登录不保证获得新 device/new node，不能承诺因此自动恢复。若保有完整原账本，只能在原身份与所有恢复证据校验通过后继续；若账本确实缺失，当前没有已验收的恢复入口，需主控另行完成平台侧设备退役/新设备登记恢复流程的验收。只有权威注册实际返回新 node 的 created=true 才能重新首建。插件不提供自动修复或重置入口，缺账本状态保持 unavailable。不能删除账本、缓存 true、刷新旧设备或重用 helper 输出恢复首次资格。现有完整 ledger 的重连仍取原 incarnation；C2 失败后重试可继续该账本，不用 bootstrap 覆盖。

官方基础连接只在 HTTP 401 或明确的 network.revoked 凭据吊销时终止本机登录。网络功能关闭、平台租户、未兑换票配额等普通 403 仅关闭网络，状态为 unavailable / reason=forbidden，保留模型登录和 DSH 会话。票据两个期限的本地合理性检查统一容忍 ±60 秒时钟差；服务器仍决定是否兑换成功。消息桥顺序处理，公告刷新期间最多排队 64 帧，超限才以 message_backpressure 关闭。健康连接的暂时取票网络错误/超时/429/503 交原 transport 做有上限的指数退避，原连接继续可用；凭据吊销、普通 403、非法票据和密钥硬截止仍失败关闭。

### 同锁 C1 → 原子落账 → 新票 C2

现服务端 `NetworkMailboxWireService.prepareReady` 将同一 ingress 绑定到第一次完整 READY；仅相同连接身份、完整请求及应用 epoch 的原样重放可通过。helper 返回后若在该连接换新 requestId、channel_nonce 或 boot_id，服务端返回 `network.conflict`。helper 也不返回可接管的 nonce/bootId/session，因此不能直接把其结果置为正常会话 ready。

因此首建在 C1 helper 成功后，仍持原 handle 原子提交完整账本；随后失效 C1 尝试、取消监听并真实关闭其 transport/socket，再申请新票 T2、建立新 ingress 的 C2，以原账本完整身份和全新 requestId/nonce/bootId 正常严格 READY。只有第二次验签、身份与恢复字段全核过才 connected。两阶段都持同一锁，不复用 T1 或伪装第二连接；transport.epoch 与服务端应用 epoch 分别验证。C2 失败保留完整账本，后续 created=false 只严格加载并走正常新票，不再 bootstrap。

完整 keys 与公告按安装根验证原始 JSON/JCS，公告本身无 realm 字段，其 realm 归属由安装根及匹配的完整清单绑定。更高有效公告立即阻断旧 keys，版本/摘要/硬截止匹配且完整清单持久后才解除；写入未知不降高水位或恢复撤销键。连接/重连与有界密钥刷新复核固定端点，不添加定时轮询；硬截止关闭基础连接。

旧业务账本的恢复仍受已合服务边界限制：ledger 没有独立持久 resume_cursor，服务端 receiptReady 当前不对非零 checkpoint/revision/evidence 直接 ready。含 recipient 证据而无可证明 cursor 的账本保持不可用；其它账本提交原 revision/高水位/pending/摘要，reconciling/recovery_conflict/identity_rollback 均不变成 connected。不下载服务端 cursor、不伪造 revision0、不清除 pending/receipt 求 READY；本项只承诺基础首建和可证明 ready 的恢复。

2026-10-25 用户决定：同机隔离（含云端）整体推迟。`isolation.js` 是 runtime 与 HostPort adapter 共用的唯一隔离判定点，策略写在代码内，不读取工具参数、provider 配置或环境变量。local 与 managed 在登录、票据、信任根和真实宿主适配等条件满足后均可联网，无需先证明同机隔离。状态及 diagnostics 的 `isolation` 明确返回 `required:false / isolated:false / state:unisolated-development`；运行时首次启动记录一次“未隔离（开发阶段）”警告，重连不重复记录。

恢复隔离门禁前，须完成 local/managed 的进程、凭据、ledger 和 IPC 隔离验收并接入可信能力探测，再修改这一个代码判定点。profile 目录只用于耐久状态分区，不作为隔离证明；host 的去重、真实 flush、登录鉴权、授权及验签要求仍有效。本项策略变更不补齐下述生产 provider/managed 设备凭据缺口，也不代表真实双 DSH 验收通过。

## 可信注入的完整业务模式

`runtime.js` 的完整分支将生产 `ws` 工厂、NATS supervisor、根签名公钥清单、session、耐久 host ledger/local 分配器、防入侵策略及 consent 服务组合为一个 DSH 节点运行时。所有原生会话共享连接。发送工具的宿主参数为 `to/body/thread`，内部映射到已生成的 `AgentSendToolInput.target`，身份仅取 `exec.agent.session.id`。

`plugin.js` 在 connect 宿主中可选注入插件自有的 Cordis `aurigaNetwork` 完整 provider；它**不是 DSH 原生服务**，其业务/Host 端口仍由受信调用方提供。已有 Team MCP 登录/退出保持运行；基础官方 provider 不能补造这些业务端口。

provider 接口：

- `prepare({ accessToken, signal })`：沿原生登录设备凭据通道，完成权威 node 注册/准备并返回 `runtime-types.d.ts` 的 `PreparedNetwork`。只有权威首次安装结果可设置 `initialize=true`；本地 ledger 丢失不可据此重置 local 高水位。
- `ports`：`NetworkPorts` 定义了票据、固定可信端点公钥刷新、原生 HostPort、私有控制操作、来源绑定/目录、组织政策、提交及授权 outbox 发布端口。传入闭包或已绑定的方法，不接受浏览器/模型构造的端口。
- `prepareTicket`：每次连接与轮换请求新一次性票据，runtime 按 `connect-token.schema.json` 序列化 `auth_token`，按票据 SHA-256/base32 推导本次精确 up/down/reply subject。票据或访问令牌均不落盘、不进入 UI/诊断；普通失败由 supervisor 退避，401/403/吊销终止登录连接。
- `host`：必须实现固定 DSH 版本的真实 `HostPort`，包括 history/inbox 去重、显式 cwd/preset 创建与返回 true 的耐久 flush；不能将内存夹具注册为生产 provider。普通 prompt 不支持子 agent 的限制保持失败关闭。
- `network`：现有 host adapter 的登记、CONTACT_ASSIGN、在线 EXECUTION_ADMIT 端口。runtime 在 admit 外增加第二层 consent；同意对话不赋予自动执行权。ATTEMPT_BEGIN 直接通过已验签 session 发出，响应再次验签且核对 recipient/attempt。
- `submit/reconcileSubmission`：HTTPS 提交前将同一私有请求和幂等键耐久写入独立 outbound 文件；明确拒绝立即删除记录，结果未知保留最多 10 分钟，不盲目重放。全局最多 256 条、每个来源绑定最多 16 条，满时仅对此会话背压；恢复时对同一记录最多调用 3 次 reconcile，次数先落盘。没有 reconcile 端口时同样按操作/重启惰性清理过期记录。过期、重试耗尽或旧格式记录缺失年龄时记 outcome-unknown 诊断并释放元数据，不能宣称远端操作成功或重新发送。
- `publishConsent`：耐久 outbox 的幂等发布/重新核权端口，只有确认成功后才删除事件；每批仅附事件对应的 `requestId/digest/recipientIds`，不附其他来源、预览或原始签名帧。默认/撤销也须更新平台状态，不能只修改 UI。失败后事件保留，后续 snapshot/操作会重试。

真实 IAM 已提供设备 Bearer 专用的 `/api/iam/v1/network/register` 与 `/api/iam/v1/network/tickets`；固定根清单与公告端点分别为 `/api/collab/v1/network/keys`、`/api/collab/v1/network/keys/announcement`，官方基础 provider 使用这些正式端点。普通会话耐久登记沿上述第二阶段显式触发；完整双 DSH 收发仍由后续任务接线。managed 模式尚未提供设备级联网凭据通道，不把 instance token 当设备令牌使用。

本地 UI 通过 `/auriga/local-login/consent/{snapshot,decide,default,revoke}` 接真实 consent。路由先经过原生 admission/同源检查，再以当前 HTTP 请求绑定用户、检查本机登录与写请求 CSRF；浏览器不能声明主体或来源。视图沿用显式字段投影，不返回 ownershipRevision、授权证明、摘要、内部决策原因或 outbox。

浏览器固定使用上述认证 HTTP 桥，不读取同名 Cordis 服务，其他浏览器插件不能替换 consent 后端。授权元数据全局最多 256 条、每个已验签来源最多 8 条、最长 24 小时；普通 delivery 只保留当前页所需的临时指针，宿主回执确认后释放耐久暂存。容量/冲突属于单条拒收，返回统一 `network.target_unavailable` 回执并留本地审计，不能关闭整个运行时。contact_prompt 仅走 consent 的拒绝事件，由发布端驱动协议终态，不伪造宿主接受回执。

退出、凭据刷新失效、明确设备吊销、provider 卸载都会立即关闭发送入口、取消连接与订阅，晚到的 prepare/READY/验签结果不能恢复旧登录。文件写入结果不确定时保持失败关闭。密钥刷新统计在 runtime diagnostics；已知版本的缓存过期/写入失败仍可受限刷新；刷新成功后从 committed 游标重扫，不跳过暂时未知密钥的消息。

工具在 READY 成功后注册，断连、票据错误或握手失败时撤销。runtime 通过 `onClosed(reason)` 在存储清理结束后通知 lifecycle；意外停机显示 unavailable，并按 500ms 至 30s 的有界退避重建。退出/吊销会使 generation 失效，不触发旧身份重建。故障诊断只含固定原因计数与最多 64 条摘要。
