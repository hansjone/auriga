# Auriga 公开 LLM 适配层

入口为 `index.js` 的 `createAurigaAdapter(options)` 与
`registerAurigaAdapter(llm, options)`。这是独立模块；尚未接到 `plugin.js`、
`client.js`、真实设备 token broker 或生产模型链路。

组合层从 **`@deepseek-ai/dsh-llm` 公开包根**加载模块，将其作为 `hostApi`
注入，再将公开服务 `ctx.llm` 传给注册入口。工厂继承实际 `LlmAdapter`，
探测其公开能力，不检查版本号或源码哈希。缺少能力时抛出脱敏的 `NO_ADAPTER`。
返回 `{ adapter, dispose }`，组合层须将 `dispose` 绑定到插件卸载/登出生命周期；
它幂等注销路由并中止在途操作。直接创建的 adapter 通过 `adapter.dispose()` 终止。
宿主仅自动释放路由不能替代此清理入口；后续装配必须挂接它。

注入端口见 `types.d.ts`：

- `apiBase`：受信部署 origin，支持 HTTP/HTTPS；不得含路径、查询、片段或用户信息。
  模块仅构造同源 `/ai/v1/models` 与 `/ai/v1/chat/completions`。
- `fetch`：受控实现，必须遵守 `signal`、`redirect: error`、`credentials: omit`；
  无全局 fetch 回退，无 Cookie、动态 URL、自动重放或刷新凭据持久化。
- `getAccessToken(signal)`：异步内存 broker，返回 `{ token, expiresAt, signal }` 或 null；
  `expiresAt` 是 Unix 毫秒绝对时刻，返回的 signal 用于撤销/登出失效。
  每次 GET/POST 仅获取一次令牌，到期或撤销关闭在途 body；轮换只作用于新请求。
- `modelLimits`：受信的精确 model id → 已知容量/能力，创建时复制快照。
  `maxOutputTokens` 必须已知；每次必须有正整数 maxTokens 或受信 defaultMaxTokens。
  tools/temperature/stop 只有显式 true 才发送，未知能力失败关闭。
- `countInputTokens`：受信 tokenizer 的准确输入 token 计数；已知 contextWindow 时必需。
  字符数/字节数仅用于传输大小限制，不作为 token 数。未知上下文、价格和额度不编造。
- `responseMode`：固定 `sse`（默认）或 `json`，每次 stream 调用只发一次 POST。

`listModels('auriga')` 每次从受鉴权模型目录获取 id，不缓存权限；
`resolveModel` 仅返回已知元数据，并不代表在线授权已成功。真正请求仍须取得令牌，
最终 ACL/预算由网关在线验证。注册路由固定为 `auriga`，无可选 URL/供应商透传。
宿主重试策略明确为 normal/maxRetries=0。

请求仅投影文本、普通 function 工具及对应历史结果。未知选项、思考/媒体/文件块、
developer/动态工具更新、deferLoading、tool strict 等均拒绝，不静默丢弃语义。
源身份与会话 metadata 不进入模型正文。D2 不允许 `n` 字段，故省略它，
并只接受 index=0 的单个 choice；不为任务中的“固定 n=1”扩大契约。

JSON 响应上限 1 MiB；SSE 每事件上限 256 KiB、整条流累计输入上限 8 MiB、
可见文本累计上限 4 MiB，工具数量/参数遵守 D2；没有无限事件队列。
取消、撤销、到期、30 秒获取/首字节/空闲超时、300 秒总超时均关闭 body。
只有完整 JSON 或 `[DONE]`、finish 与完整 JSON 对象工具参数全部校验成功后才结束块，
发送已确认 usage 和成功 finish。失败只发 error/aborted finish；不推断未计费，
缺失 usage 不发送 usage，不将未知值写成 0。错误不包含上游正文、令牌或提示。

`contract.d.ts` 由 `node test/ai/generate-contract.js` 从实际 OpenAPI 数据面 schema
依赖闭包生成，标准测试校验它未漂移。`types.d.ts` 的宿主类型来自公开包根，
宿主为可选运行时 peer；当前任务禁止更改依赖/锁文件，普通 workspace 无宿主包。
`public-api.test.js` 会对可读的真实公开包执行类型检查与实际 Cordis/LlmRuntime
注册、调用和注销。默认不可解析时明确 skip，不把传输 mock 算作宿主兼容证据。
可将 `AURIGA_DSH_LLM_PACKAGE` 指向受许可只读的公开包 `package.json` 再运行：

```powershell
node --test "test/ai/*.test.js"
```

本机源码树构建入口的通过结果不证明系统安装包、完整 GUI 或生产装配兼容；
未实现 refresh 交换，不调用付费模型，不依赖上游内部运行文件。
