# 上游 DSH HostPort（N1）

唯一宿主依据是 deploy/compose/real-dsh/package-lock.json 锁定的
@deepseek-ai/dsh*@0.2.0-rc.2 npm 包。本轮 npm ci --ignore-scripts 后只读核包内源码，
不以桌面发行版或其他仓库推断 API。用户在 2026-10-25 将同机隔离整体推迟；无隔离服务
不再阻止原生端口启用。诊断固定显示“未隔离（开发阶段）”，不宣称已经隔离。

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

index.js 的公开工厂不要求 security/issuer/品牌，也不调用 assertIsolated。主装配必须传入
当前 Node 宿主的 ctx，以及由宿主 loader 取得的 runtime 文件路径：manifestPath、controllerPath、
persistencePath、sessionPath、agentLoopPath。路径不得来自模型或浏览器参数。

```
const host = await createNativeHostPort({ ctx, runtime });
const sourceForExecution = await createNativeSourceResolver({ ctx, runtime, lookupBinding });
host.getDiagnostics().isolation.message; // 未隔离（开发阶段）
```

工厂每次操作仍读实际 CLI manifest.version 和四份源码 SHA-256，严格对照 PINNED_HOST；
缺文件、版本/摘要不符或 API 缺失继续 host_incompatible。复制 pin 或 no-op security 对象
不能替代实际文件。npm manifest 没有 desktop build/dirty 字段，源码校验不等于 OS 隔离证明。

隔离只在 runtime.js 的 getNativeIsolationDecision() 判定。恢复条件：用户重新要求强制隔离，
且真实宿主隔离适配及验证到位，再在此点恢复强制要求；没有环境变量/配置开关或第二处判定。
J4 读取 getDiagnostics() 展示该诊断，本任务不改现有 UI/provider 文件。

功能性耐久检查独立保留：从已校验的 persistencePath 加载实际 JSONL 类，核当前 provider 实例，
同 session 的 write handle、storage、tracker.openHandles、closing 状态和 drain/flush 能力；
未匹配 writer 时不能只因 session/flush 返回 true 就报接受。冷会话先原生恢复再核活动 writer，
不会因为冷态尚无 writer 被永久卡住。该检查无需隔离服务，也不证明同机工具隔离。

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
另测公开工厂在完全不传隔离服务时启用、创建、冷恢复和 flush：使用实际上游 Context、
SessionStore、JSONL 文件存储及 controller 包装方法，commands/Agent 装配仍为无模型夹具。
包含实际版本/文件摘要错误、错误 provider、关闭 writer 的负向检查。
这些不等于真实模型对话、JSONL 断电耐久或双 DSH 生产验收；同机隔离按用户决定整体推迟。
