# N1 agent 发送工具

入口为 `createAgentSendTool`，不注册 MCP、不创建连接。仅支持普通单播信息；
控制、首联、回复、human、组和广播均需各自独立入口。

- `inputSchema`：从 `contracts/network/send.schema.json` 生成的完整内联 schema，只有 `target/body/thread`；任务文字中的 `to` 不作为别名接受。
- `prepare(input, exec)`：返回纯 JSON `SubmitRequest`，失败抛带稳定契约 code 的错误。此方法仅供插件内部使用，异常和请求不能直接透传模型。
- `execute(input, exec, submit)`：调用一次注入的提交函数并裁剪回执；目标相关结果统一为 `{status:'request_submitted'}`，不表示落库、送达或执行成功。提交抛错返回 `network.outcome_unknown`，不自动重试。
- `mapSendReceipt(receipt)`：同样的回执裁剪，可供独立 session 接线使用。

工厂必须由可信插件代码提供：

- `ledger` 与 `localAddress`：现有宿主账本和 #137 的 `adapter.localAddress`（或同一分配器的 `addressFor`）。工具仅以真实 `exec.agent.session.id` 找唯一、已登记且开放的 session/subagent local；不新建 local。
- `sourceFor({hostSessionId,address})`：查询当前来源授权，返回 `{address,senderKind:'agent',sourceBinding,producerEpoch,sendAllowed:true,expiresAt}`，到期时间为毫秒时间戳。每次读取当前授权；精确来源、send 范围、撤销和期限均须可信控制路径核验。它不是 LOCAL_REGISTER 回执，也不能借用 human 意图凭据。准备过程中会再次核验，以防异步操作期间发生撤销或绑定变更。
- `resolveTarget({realm,node,local})`：从权威地址登记解析完整地址；session/subagent 类型不能从文本猜测。未知/不可用返回 null 或抛错，工具对外统一裁剪。不是公开会话目录，不应主动扫描或为目标创建会话。
- `rateLimit:{messages,windowMs}`：必填节点聚合滑动窗口策略，同一 ledger 的所有会话和工具实例共享，探测失败不返还额度。`limits.json` 没有速率常量，因此没有私造默认值；配置不同的同 ledger 工具会拒绝创建。此预算仅进程内预检，重启、跨进程与组织/用户聚合仍由总部权威限流。
- `now`：可选可信时钟，默认 `Date.now`；时间回退不释放额度或复活过期授权。

`submit` 必须通过共享 session/MCP 提交路径处理请求。发送前持久保存完整请求及
`idempotency`，响应丢失按原键查询/对账，不重新调用 prepare 生成另一个键。
接线层在真正提交时仍须核验当前授权、node/epoch/组织边界；总部从 `source_binding`
生成 from/sender_kind，从 to 生成 message_class，普通工具不上传这些盖章字段。
原始目标结果只留有权宿主诊断，不直接返回模型；相近时延和服务端查询防枚举由权威路径保证。

类型、校验器和编码上限均从只读契约生成，运行时无需 Ajv 或访问 contracts 目录。
生成器复用仓库锁定的 json-schema-to-typescript、Prettier 和 openapi-typescript
依赖中的 Redocly Ajv，不新增依赖；内联的 Ajv 字符串长度辅助函数保留 MIT 声明。

```powershell
node clients/dsh-plugin/test/network/tools/generate.mjs
node clients/dsh-plugin/test/network/tools/generate.mjs --check
```
