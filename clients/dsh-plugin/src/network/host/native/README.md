# 上游 DSH HostPort（N1）

适配基线是用户控制的 `@deepseek-ai/dsh@0.2.0-rc.2`，与
`deploy/compose/real-dsh/package-lock.json` 一致。版本仅用于核验测试范围；运行时不检查
版本号、构建号、包位置或源码哈希，也不自动升级 DSH。升级由用户交主控评估。

## 公开 API 与接线

主装配仅传当前 Node 插件宿主的 `ctx`，不传宿主文件路径、隔离 issuer 或安全品牌：

```js
const host = await createNativeHostPort({ ctx });
const sourceForExecution = await createNativeSourceResolver({
  ctx,
  lookupBinding,
});
host.getDiagnostics().isolation.message; // 未隔离（开发阶段）
host.getDiagnostics().capabilities.create;
sourceForExecution.getDiagnostics().capabilities.source;
```

最小接口在 `types.d.ts`。下列均通过 npm 包的公开入口导出并在基线公开声明中核对：

- `@deepseek-ai/dsh-api-session-controller`：`ctx.sessionController.inspect`、
  `resolveAgent`、`create`、`prompt`、`projections`。冷读不激活 Agent；创建显式指定
  sessionId/cwd/agentPreset；prompt 保留 requestId 并使用 queue。
- `@deepseek-ai/dsh-agent`：公开 `Agent`、`Inbox.nextTurn/nextStep`、
  `ctx.agents.get/isOwnedBy`。不读取 loop 驱动实现。
- `@deepseek-ai/dsh-session`：`ctx.sessions.get/flush`、Session.id/header。
  历史只用 controller.inspect 的公开结果，不调用已弃用的同步 Session 事件读取器。
- `@deepseek-ai/dsh-session-persistence`：服务 identity、stat、flush、open 与只读句柄。
  不限定 JSONL 或任何具体后端，不读取 writer/tracker，不解包 Cordis 对象。
- `@deepseek-ai/dsh-session-projection`、`@deepseek-ai/dsh-agent-preset-registry`、
  `@deepseek-ai/dsh-workspace`：stateOf、resolve、archivedSessionIds。

## 能力探测与诊断

runtime.js 在启动时无副作用地检查公开服务方法、归档列表和持久化 identity 类型；不创建
探针会话、不调用模型、不试发消息。没有目标会话时不能证明创建/恢复/flush 的行为，
因此在真正操作时继续校验返回形状、原生对象身份、cwd/preset 与 awaited flush 结果。

每次操作及关键异步返回后重新探测服务，避免宿主卸载后沿用旧结论。
`getDiagnostics().capabilities` 分为 read、requests、durability、create、prompt、source。
`available` 只表示依赖 API 当前齐备；`missing` 列出缺少的公开接口；`lastFailure` 是最近一次
行为失败的固定原因码，不含宿主异常、路径或正文。瞬时错误可重试，不永久锁死端口。
缺少某项时受影响操作失败封闭，其余能力仍可使用；工厂本身可返回诊断，不拖垮插件。
J4 应读取并展示诊断，不把结构探测通过当作真实对话验收。

隔离只在 `getNativeIsolationDecision()` 判定。用户 2026-10-25 整体推迟同机隔离，
固定显示“未隔离（开发阶段）”。恢复需要用户新的决定和经过验证的宿主隔离实现，
随后只在该判定点恢复要求；没有环境变量或配置绕过。

## 会话与耐久边界

- getSession 读归档/stat/inspect，不使用前台会话、不重建、不把读取失败当不存在。
- 新建和已有 adoption 均过同一耐久屏障；冷态通过公开 resolveAgent 原身份恢复。
  preset 读取公开投影，新建时解析默认 preset，已有会话的 null preset 不改变其配置。
- hasPromptRequest 检查公开 live Inbox 和 inspect 的 user/message.source.rpcId。
  冷态用公开 projections 的 inbox 值，不激活 Agent；缺少/损坏队列不是请求不存在。
- flush 先恢复需要的会话，再核公开 provider/stat，await sessions.flush(session) 严格 true，
  随后 await sessionPersistence.flush() 严格 void，并重新核 provider identity、session 和归档。
  用公开 open/read/close 检查屏障前事件前缀确实可读，防止无匹配 writer 的空成功。
  后者是公开的后端整体屏障，会等待该服务所有活跃写句柄，可能受到其他会话背压。
  依赖宿主遵守公开持久化契约，不证明后端实现、写句柄私有状态或断电耐久。
- prompt 按会话串行，其他会话可并行；30 秒上限 abort 并释放锁。公开 prompt 的 signal
  只保证入场前取消，不声称取消进行中的 admission。最多保留 1,024 项不确定请求且不重做，
  重启依靠核心 ledger。正文与 requestId 一一对应由核心 recipient/messageDigest 保证。
- 子 Agent 用 origin 与公开 runtime ownership 判断；只有 parentSession 的普通 fork 可用。
  N2 专用 delivery 未实施。来源解析只在真实工具回调中核活对象及唯一 bound/open local；
  它不是 IPC 能力或当前执行授权，J4 仍须检查工具授权并绑定可信 IPC peer。
- prompt 可能先调度后 flush；不承诺副作用恰好一次。历史扫描仍为 O(n)。

## 验证边界

默认 native 测试只使用最小公开宿主接口夹具，不依赖 DSH 安装目录。
显式基线核验在已有 real-dsh lockfile 安装后，从插件目录执行：

```sh
node --test test/network/host/native/upstream-check.mjs
```

该测试仅通过公开 npm 导出解析模块，编译 `baseline-contract.ts` 核对所用 API 的公开类型，
并用真实 Context/SessionStore/JSONL 验证公开屏障、无监听者、失败传播及建会话/冷恢复。
控制器生命周期的装配使用明确的最小接口夹具，不伪造控制器私有成员或执行源码片段。
不启动 DSH/模型/网络服务。这些不等于完整 DSH 对话、双 DSH 或断电/强杀验收。
