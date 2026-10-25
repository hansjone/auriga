# modelprotocol（D2 假 Transport 边界）

只处理正式 `contracts/ai/README.md` D2 和 `infra/gen/aiapi` 的文本/工具 JSON/SSE。
这是可直接组合的协议模块；没有 HTTP runtime、身份、ACL、预算、DB、真实出站或重试。
不声明兼容任意真实供应商的字段、usage、分词器或扩展事件。

## 给后续 relay handler

1. 从服务内可信模型配置构造 `Policy`，其 `Model` 必须与已授权别名完全相同。
   上下文、最大输出、工具和各采样能力不能取自请求或猜测模型名；能力缺省为拒绝。
   `MeasureInput(ctx, generatedRequest)` 必须覆盖实际完整输入（历史、工具 schema、模板开销），
   返回精确 token 或可证明安全的上界；没有算法或证明就不能配置该模型。
   `DecodeRequest` 有 1 MiB 硬界限；`ValidateRequest` 为已有生成类型提供相同验证入口。
   `Value()` 返回独立副本；开放工具 schema 的数值保留为 `json.Number`。
   `InputBound()+max_tokens <= ContextWindow`，该结果仅供后续可信预算准入使用，不授予权限。
2. 完成在线鉴权、ACL、固定快照和耐久预算 dispatching 后，由 handler **提交一次**假 Transport；
   将本次 body 交给 `Complete` 或 `Stream`。模块没有 Transport 回调，无法重放或故障切换。
   两者均取得 body 所有权，成功、错误、取消后都关闭。
3. `Limits` 三个值都必须为正；`MaxEventBytes <= 256 KiB`，包含 SSE 字段和事件分隔符。
   `MaxBytes` 分别约束输入和规范化输出，`MaxEvents` 分别约束输入事件帧和输出帧，包含 DONE/错误。
   JSON 也检查规范化编码后的字节数。空帧/注释计入输入帧上限。
   额外硬界限：JSON 深度 64、id 1024 字符、模型 128 字符、累计文本 1 Mi 字符，
   工具数 128、工具名 64 ASCII 字符、工具 id 128 字符、每工具参数 262144 字符。
   已知字段的枚举/范围以正式 schema 为准；这些资源界限可进一步拒绝过大响应。
4. SSE 的事件交付槽固定为 1，没有等待队列，同步读取并交付一个事件；读缓冲最多预读 4096 字节。
   只保留有界文本长度计数、工具拼接状态和单事件；调用者不得另接无界队列。
   `Sink(ctx, bytes)` 返回真实写入字节数，短写/错误即停止，不重试写入。
   sink 必须遵守 ctx，阻塞写必须设 deadline；body 的 Close 必须及时中断并发 Read。
   不能靠额外 goroutine 强制抢占不遵守取消的任意 I/O，否则会泄漏 goroutine。
5. handler 提供带总期限的 ctx，并把票到期、撤销、客户端断开和首字节/空闲超时转为取消。
   本模块在取消时关闭 body、等待关闭回调退出；不另建流处理 goroutine。处理返回后 handler 也须取消本次 Transport context。
   请求 Reader 的所有权及请求读取超时属于 handler。可信计量/usage 回调也须及时响应 ctx。

## usage、完成与错误

- `VerifyUsage(ctx, usage)` 是服务侧对**该次假 Transport 最终 usage 来源**的信任判断，
  不能相信客户端计数。字段完整、非负、安全整数、加和一致、未超输入预占/输出上限仍不足以证明来源可信。
  未配置或返回 false 时，SSE 剥除该 usage；`Usage=nil, UsageKnown=false`，不能记成 0 或退未知预占。
- SSE 完成内容和工具、收到完整 DONE 且成功写出后，`Completed=true`；无可信 usage 仍可完成内容，
  但计费保持 unknown。JSON 正式 schema **要求 usage**，缺失/不可信则返回错误和空结果，
  不伪造 0、不输出契约外的 nullable/缺省 usage。
- 只有最终 usage 通过来源判断和全部校验才设 `UsageKnown=true`。
  SSE 若在此后写失败/断流，可以同时 `Completed=false, UsageKnown=true`；handler 仍须按可信最终证据和账本规则结算。
  已报告可信 usage 后不再接受替换；未证实最终 usage 的中断始终为 unknown。
- `Complete` 不写公共响应，`Started=false`；成功时 handler 仅编码 `Result.Completion`。
  `Stream.Started` 表示 sink 已报告至少一字节（包含短写）。handler 必须另跟踪自己预先写入的头/字节，
  不能在预先发送响应头后把本字段 false 当作可修改 HTTP 状态。
- `*Error` 只含已有 catalog code 和固定脱敏信息，没有原始错误/body/URL/key/prompt。
  `Envelope(localRequestID)` 生成正式 AiError。流未开始则只返回错误；开始后在剩余限额内尽力写同型 AiError，
  然后关闭，错误绝不发成功 DONE。写失败或取消时不继续写错误。
  上游 AiError 仅保留允许的网关错误码，替换 message/requestId；不透传其身份错误或敏感信息。
  HTTP 状态及是否还能发送错误由后续 handler 决定。

## 测试模型的含义

`synthetic` 夹具**定义**输入 token 为规范化完整 JSON 字节数加 7 个模板 token，仅对该合成模型成立。
它不是任何真实供应商的 tokenizer，也不能作为真实模型的安全上界；部署真实模型仍须另行证明计量、能力及 usage 来源。
工具增量与已合插件 Adapter 同为 D2 的 index/id/function.name/function.arguments，名称和参数追加拼接。
完整工具必须有唯一 id、已声明函数名和 JSON 对象参数；工具历史必须完整回答一次后才能继续。
