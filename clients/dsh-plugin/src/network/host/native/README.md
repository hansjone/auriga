# 上游 DSH HostPort（N1）

唯一宿主依据是 deploy/compose/real-dsh/package-lock.json 锁定的
@deepseek-ai/dsh*@0.2.0-rc.2 npm 包。本轮 npm ci --ignore-scripts 后只读核包内源码，
不以桌面发行版或其他仓库推断 API。安装隔离另有任务，本目录保持失败封闭，不定隔离方案。

## 源码基线与可复核证据

以下包版本均为 0.2.0-rc.2，路径相对各 npm 包；源码 SHA-256 固定在 runtime.js：

- @deepseek-ai/dsh-api-session-controller/lib/index.js:125–131：subagent ownership 是
  origin=subagent，或 parentSession 对应的 live parent 实际拥有 child；仅有 fork lineage 不拒绝。
- 同文件 :208–244、:377–410：resolveAgent 原身份冷恢复，调用 sessionQuery 后 agents.resume；
  :412–456：显式身份创建/采用及 cwd/preset 冲突；:686–714：create 的 sessionId/cwd/agentPreset。
- 同文件 :339–340：当前 preset 读 sessionProjections.stateOf(session,'agentPreset')；
  :358–369：省略 preset 使用注册表默认值。本适配创建时明确解析默认 preset，已有会话省略时不改 preset。
- 同文件 :850–902：prompt 校验内容，写 user source.rpcId，queue 走 followup；
  :1063–1076：去重查 inbox.nextTurn/nextStep 与 session.snapshotEvents 的 user/message。
- @deepseek-ai/dsh-agent-loop/lib/index.js:70–85：真实 ReactLoopInbox 两个 getter；
  :177–179：来自 inbox 投影；:806–814：followup 写 next-turn，steer/inject 写 next-step。
- controller :2930–2951：resolveAgent 返回 {agent}|{error}，inspect 附着/冷态都返回 meta/events；
  :3096–3099：signal 仅在 prompt 入口 throwIfAborted，然后交 commands.prompt。
- @deepseek-ai/dsh-session/lib/index.js:1376–1381：snapshotEvents；:1832–1852：
  sessions.flush(session) 等所有监听者，任一失败抛错，无参与者 false，有参与者 true；:1861–1862：get。
- @deepseek-ai/dsh-session-persistence-jsonl/lib/index.js:413–420：匹配 writer 后 drainLive+flush；
  :2536–2562：stat 可见 pending header，不能当耐久完成；:3178、:3204：实际文件 sync 路径。
  有 session/flush listener 但无匹配 writer 仍可能得到 true，因此仍必须核真实 durable provider。
- @deepseek-ai/dsh-agent/lib/index.js:605–607：isOwnedBy 比较真实 owner 对象；
  @deepseek-ai/dsh-workspace/lib/index.js:504–505：archivedSessionIds；
  @deepseek-ai/dsh-agent-preset-registry/lib/index.js:603–615：resolve(id?) 返回解析后的 id/broken。

## 适配行为

index.js 的两个公开原生工厂先过来源/隔离门禁；普通对象、复制、Proxy 和 no-op 均不能放行。
本任务不提供隔离服务发行者，私有来源注册表仍为空，J4 保持 unconfigured，不建连、不注册工具。
readRuntimeFingerprint 只读 CLI manifest.version 与四份包源码摘要；npm manifest 没有 desktop
build/dirty 字段。诊断指纹匹配不证明当前加载实例，也不替代隔离或已加载文件来源证明。

host-port.js 的 createHostPortOperations 和 source-context.js 的 createSourceResolver 是底层
组合算法，不从 index.js 导出，不授予联网权。单元测试验证算法，不能冒充生产隔离验收。

- getSession 原身份读归档/stat/inspect，不查前台、不重建、不把读错误当不存在。
- 新建和已有 adoption 均过同一 durable + 严格 awaited flush；冷态沿原生 resume，核 cwd 与
  实际 preset 投影，false/异常/provider 或 session 更换/归档均不成功。丢响应只查原持久 ID。
- hasPromptRequest 按已核源码直接检查两个真实 pending 队列和 user/message.source.rpcId；
  字段缺失或无效拒绝，不把缺证据当不存在；恢复为 live 后再查一次。
- prompt 只允许 queue，按 session 串行。30 秒上限发 abort 并释放锁，但上游仅保证入场前检查
  signal，不能宣称中途 admission 被取消。不确定请求最多 1,024 项且不重做，重启依靠核心 ledger。
- requestId 与正文摘要一一对应由核心 recipient/messageDigest 保证；不得同 ID 换正文。
- 子 agent 按真实 origin/runtime ownership 拒普通入口；普通 fork 不再因 parentSession 被误拒。
  N2 专用 delivery 未实施。来源投影核真实对象与唯一 bound/open local，不证明本次工具执行权，
  不可直接当 IPC 能力；J4 必须只在真实工具回调中使用并另核当前授权。
- 错误保持 host_incompatible 和原身份对账；J4 不得永久缓存瞬时失败。历史扫描暂为 O(n)。
  prompt 可先调度后 flush，不承诺副作用恰好一次，结果不明不自动重做。

## 验证边界

默认插件测试不依赖 DSH 安装目录。另提供显式上游核验（不启动 DSH、不调用模型、不监听端口）：

```
# 先在 deploy/compose/real-dsh 按 lockfile npm ci --ignore-scripts
# 在 clients/dsh-plugin 执行
node --test test/network/host/native/upstream-check.mjs
```

它核四份真实源码摘要，直接加载上游 SessionController/SessionStore；inbox 的非导出实现按
已固定摘要的原始源码片段执行并与适配器比对，不替换宿主模块或放宽原生参数。
这些是包接口/内存行为核验，真实创建/恢复模型、JSONL 断电耐久、双 DSH 与隔离仍待后续验收。
