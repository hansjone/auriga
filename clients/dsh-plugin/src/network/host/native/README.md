# 原生 HostPort（N1）

该目录是独立接缝，尚未接入插件 provider；不注册工具、不联网、不启动宿主。
`createNativeHostPort({ctx, security})` 实现现有 `host/adapter.js` 的 HostPort。
现有核心文件未修改，主控在 #151/J4 集成后导入本目录 `index.js`。

## 原生接线

在 Node 宿主注入 `sessionController`、`sessionPersistence`、`sessions`、`agents`、
`workspaceRegistry`、`agentPresets`，等待服务就绪后创建一个 HostPort，交给
`createHostAdapter({ledger, host, network})`。同一宿主只创建一个端口实例以共享会话锁。

- `getSession`：先核归档表与 persistence.stat，再用 controller.inspect 读取原身份；
  只有真正不存在才返回 null，读取故障拒绝，不查前台、不创建、不唤醒。
- `createSession`：携带固定 sessionId/cwd/agentPreset，核对已存在会话的 cwd/preset；
  创建后核返回身份、实际 header 和耐久 provider，await sessions.flush。创建失败不重试，
  由核心按原 ID 查询。核心负责先持久化预留、CONTACT_ASSIGN 与唯一 creation_permit。
- `prompt`：只允许 queue；原生 resolveAgent 恢复普通冷会话；传原 requestId、text content
  与 AbortSignal。按 session 串行检查 inbox.nextTurn/nextStep 和 user/message.source.rpcId。
  同一 recipient 跨 thread 仍去重，不缓存“调用过就成功”。准入 CAS 仍由核心 network.admit 负责。
- `hasPromptRequest`：冷历史可以直接检查；仅匹配 user 来源的 rpcId。
  `flush` 必要时沿原生入口恢复同一普通会话，await sessions.flush 严格返回 true，
  前后核当前 provider 和 session 对象；false、磁盘错误、替换或归档不接受。
- `origin=subagent` 和原生拒绝的父拥有会话不能走普通 prompt；N2 专用 delivery 尚未实施。

## 安装隔离门禁：当前缺真实宿主工程

没有内置 `security` 实现，缺失时工厂抛 `network.host_incompatible`。接入方必须在创建
连接、恢复网络账本、发布发送工具之前完成该门禁；抛错时维持 unconfigured。
不能绕过工厂直接用不受保护的凭据/文件 ledger 开启网络，也不能将此接缝测试视为 N1 验收。

`security` 只能由独立受信宿主/broker 注入，不能从配置 JSON、模型参数、环境中的布尔值、
浏览器或未认证 IPC 组装；三个方法缺一不可，每次操作都会重新调用：

1. `inspectRuntime()`：从当前运行发行物读取版本/build/dirty 和两个实际加载源码的 SHA-256。
   `PINNED_HOST` 固定 failure-validation §1.2 的 rc.3/build/hash，任一缺失或不符即拒绝。
   `readRuntimeFingerprint` 可只读散装发行 JS；ASAR 内字节须由 Electron 宿主采集。
   不能读取另一份安装包冒充当前加载文件；只可在宿主保证代码不可变且仍是同一加载实例时缓存。
2. `assertIsolated()`：只有强制边界当前成立才正常返回，失败必须抛错；返回布尔值也拒绝。
   agent shell/文件工具与凭据及账本 broker 必须处于不同受限 OS 身份或等价沙箱边界。
   隐藏目录、同用户 ACL、同用户可解密密钥、插件闭包和指纹都不能代替该证明。
3. `assertDurableProvider(provider, sessionId)`：核给定的实际 sessionPersistence 服务对象、
   该 session 的活动 writer 与 await flush 参与者确为已核的耐久 JSONL provider；失败抛错。
   方法名存在、stat 可见、flush=true 或构造器名相同都不充分，不能用 no-op/恒真回调。

宿主工程须提供工具沙箱负向证据：文件/目录与备份不可读写凭据、source_binding、human 意图、
local 高水位/登记指纹、receipt/receive_guard/关系授权账本；工具环境和继承句柄无秘密；
调试/进程内存、日志/转储/配置导出不可绕过。IPC 必须认证对端，绑定宿主当前 session/local
和精确操作能力，拒绝工具自报 user/session/node、human 授权与 source_binding 重绑。
该目录不创建 IPC listener，也不实施 OS 账号/ACL/容器/调试权限配置；需另派宿主工程。
在这些证据到位前不得向生产注入模拟 `security` 实现。

`createNativeSourceResolver({ctx,security,lookupBinding})` 仅供**进程内真实工具执行回调**：
核 ctx.agents/ctx.sessions 的对象身份与原生 ownership，重新查询受保护 ledger 的唯一绑定，
只返回 local/bindingId/hostSessionId/cwd。它拒绝复制的执行对象、归档、子会话、错误 cwd、
非 bound/open 映射及非法 local；不释放整个账本或 token。它不是可序列化 IPC 鉴权服务。
主控仍需在 provider 工具入口先调用它，并用受信 sourceFor 校验当前授权；现有 send 工具
只读取 session.id，不能认为已有该对象身份检查。跨进程 broker 要另行设计绑定能力的传递。

## 证据范围与后续验收

源码映射依据：network/failure-validation §1.2、protocol §6–7，以及只读旧 Team 原生适配。
rc.3 prompt/inbox 历史形状另核已有发行源码核验日志。当前沙箱不能读取实际安装目录，
所以未重新计算已安装发行文件摘要；这里锁定的是设计记录中的证据基线。

所有本目录测试均为合同夹具；不导入旧 Team、不依赖其运行，也不证明实际 DSH/文件系统兼容。
尚待 J10 在被强制隔离的真实 rc.3 执行冷恢复、非默认 cwd/preset、新会话首消息竞争、
prompt/flush/ledger 三处强杀、丢回包、只读/满盘及完整双 DSH 往返。N2 不在本期支持范围。
普通 prompt 可先调度再 flush，不能声称任意副作用恰好执行一次；结果不明不得自动重做。
