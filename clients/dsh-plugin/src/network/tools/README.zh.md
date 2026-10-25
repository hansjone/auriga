# N1 agent 发送工具

入口为 `createAgentSendTool`，不注册 MCP、不创建连接。支持普通单播信息；系统目标默认拒绝，只有可信宿主核实独立 first_contact 授权后才生成首联请求。
控制、回复、human、组和广播均不经此入口。

- `inputSchema`：从 `contracts/network/send.schema.json` 生成的完整内联 schema，只有 `target/body/thread`；任务文字中的 `to` 不作为别名接受。
- `prepare(input, exec)`：返回纯 JSON `SubmitRequest`，失败抛带稳定契约 code 的错误。此方法仅供插件内部使用，异常和请求不能直接透传模型。
- `execute(input, exec, submit)`：调用一次注入的提交函数并裁剪回执；目标相关结果统一为 `{status:'request_submitted'}`，不表示落库、送达或执行成功。提交抛错返回 `network.outcome_unknown`，不自动重试。
- `mapSendReceipt(receipt)`：同样的回执裁剪，可供独立 session 接线使用。

工厂必须由可信插件代码提供：

- `ledger` 与 `localAddress`：现有宿主账本和 #137 的 `adapter.localAddress`（或同一分配器的 `addressFor`）。工具仅以真实 `exec.agent.session.id` 找唯一、已登记且开放的 session/subagent local；不新建 local。
- `sourceFor({hostSessionId,address})`：查询当前来源授权，返回 `{address,senderKind:'agent',sourceBinding,producerEpoch,sendAllowed:true,expiresAt,firstContactAllowed?}`，到期时间为毫秒时间戳。每次读取当前授权；精确来源、send 范围、撤销和期限均须可信控制路径核验。它不是 LOCAL_REGISTER 回执，也不能借用 human 意图凭据。准备过程中会再次核验，以防异步操作期间发生撤销或绑定变更。仅此可信端口两次均返回 `firstContactAllowed===true` 时允许 local=0 目标，固定 `kind=first_contact/contact_scope=conversation/intent=request`；普通 session/subagent 目标仍为 message，不从模型参数或父会话继承首联权限。
- `resolveTarget({realm,node,local})`：从权威地址登记解析完整地址；session/subagent 类型不能从文本猜测。未知/不可用返回 null 或抛错，工具对外统一裁剪。不是公开会话目录，不应主动扫描或为目标创建会话。
- `rateLimit:{messages,windowMs,localMessages?}`：必填节点聚合滑动窗口策略，同一 ledger 的所有会话和工具实例共享；localMessages 可另设每个精确 local 的窗口额度（正整数且不大于节点额度，省略时等于节点额度）。无权调用不预占合法额度；通过来源授权后、查询目标前原子预占 local 与节点预算，授权后的探测失败不返还。local 超额不消耗其它 local 的节点余量，建议主控明确配置更窄 local 额度。`limits.json` 没有速率常量，因此没有私造默认值；配置不同的同 ledger 工具会拒绝创建。此预算仅进程内预检，重启、跨进程与组织/用户聚合仍由总部权威限流。
- `releaseResult(startedAt)`：必填可信结果释放端口。execute 的成功、目标不可用及失败结果均在返回模型前走此端口，只传开始时间，不传目标、回执或原因。宿主必须接入有界共享响应队列，按统一时间窗/负载规则释放；队列失败只给 outcome_unknown。本模块不新建定时器或无界等待队列；服务端仍须统一查目标的时序，不能把相同字段或空释放函数当作时延防枚举已验证。
- `now`：可选可信时钟，默认 `Date.now`；时间回退不释放额度或复活过期授权。

`submit` 必须通过共享 session/MCP 提交路径处理请求。发送前持久保存完整请求及
`idempotency`，响应丢失按原键查询/对账，不重新调用 prepare 生成另一个键。
接线层在真正提交时仍须核验当前授权、node/epoch/组织边界；总部从 `source_binding`
生成 from/sender_kind，从 to 生成 message_class，普通工具不上传这些盖章字段。
原始目标结果只留有权宿主诊断，不直接返回模型；相近时延和服务端查询防枚举由权威路径保证。

类型输出到 `src/generated/network/*.d.ts`，运行时校验器及编码上限输出到本目录的 `generated/`。
生成器位于 scripts，使用显式精确钉版的 Ajv 开发依赖；内联的 Ajv 字符串长度辅助函数保留 MIT 声明，运行时无需 Ajv 或访问 contracts 目录。
生成 JS 沿仓库既有 `**/generated/**` ESLint 忽略，不含文件级 eslint-disable；Prettier 正常检查。
`generated-fresh.test.js` 每次重新生成全部产物到内存并逐字节比较版本化文件，契约/生成器变化未重生时测试失败。

```powershell
cd clients/dsh-plugin
npm run gen:network-tools
npm run gen:network-tools:check
```
