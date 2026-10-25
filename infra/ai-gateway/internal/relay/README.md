# Device 执行核心（D7）

仅供后续 HTTP runtime 内部装配；没有监听器、真实模型出站实现或生产模型计量规则。
`New(Config)` 直接接现有 `*modelgate.Gate`、`*admin.RelayReader`、`*admin.Cipher`、
`*budget.Ledger`，只另注入受信 `Policy` 与单次 `Transport`。
`Core.Execute(ctx, rawDeviceTicket, body, idempotencyKey, streamSink)` 执行 chat；
只有 `err == nil` 才可把 `Output.Result.Completion` 编码为 JSON 成功响应。
SSE 由核心写 sink，成功 DONE 延迟到可信 usage 落账之后。

## 接线责任

- Policy 以在线核准的 tenant/model/revisions 查受信配置，容量必须与快照一致。
  必须定义完整有效输入（含历史、工具、模板）的可证明 token 上界及最终 usage 来源。
  不猜 tokenizer，不使用字符计数；未配置模型拒绝。测试的固定 32-token 规则仅属于合成模型。
- Transport 收到 `Submission`：固定 Target、已把 Model 改为 upstream 的独立生成请求、
  PublicModel 和耐久 RequestID。响应必须归一化为 PublicModel 所用 D2 JSON/SSE。
  只借用 callback 期间的 secret，返回后不得保留；不得重试、重定向重放或故障切换。
  Transport 负责 connect timeout、及时响应 ctx 取消、可中断的 Body.Close，及真实停止证明。
  后续真实 Transport 还必须完成 DNS/SSRF/重定向约束；本包没有真实 HTTP 出站能力。
- 核心负责首次字节（从派发开始）、idle、总超时（从 Execute 开始计）、原始短票截止的最小值，
  派发前在线复核、在途周期复核（间隔及单次核验各至多 1 秒）、取消及 body 所有权。
  `Stopped` 只允许在全部上游执行真实停止时关闭；发送一个值不能充当停止证明。
- provider MaxConcurrency 是每个 Core 的本机调度上限；每进程复用同一个 Core。
  跨 gateway 的主体槽、token/金额预算始终由实际 PG Ledger 原子裁决。
- 协议总字节、事件及单事件界限来自必填 Limits；同步 sink 没有事件队列。
  HTTP runtime 必须给请求 body 读取和 sink 写入设置截止，并保证 sink 在 ctx 取消时及时返回。
  收尾错误只尽力写 100ms 同型 AiError；没有成功 DONE 或工具副作用重演。

## 耐久收尾与后续 runtime

保留原快照的 identity、版本、price 和 origin，实际 BudgetAdmission 使用生成类型中已有的
身份、provider/credential ID、价格版本、计量上界和原票截止字段；revision 由 Gate/Reader 固定复核。
账务使用独立且至多 5 秒的 ctx；它从不传给上游，也不重试派发/结算。
无可信 usage、断流、取消、未证明停止均冻结为 unknown（三个数值为 null）。
只有证实未提交的 reserved 才回收为零；dispatching 永不退款。

`Output.Pending` 表示冻结 unknown 或未确认账务，必须移交 `Ref` / Reservation / StopProof。
账务失败可能仍是 dispatching，也可能 COMMIT 已成功；用既有 `Ledger.GetReservation` 查最新 revision，
再按可信证据调用 `Ledger.Recover`，不能由错误、租约或 ctx 推定未提交/已停止。
StopTimeout 后保留 StopProof 供现场 supervisor 等待；丢失现场证据或重启不能制造停止证明。
本机 provider 槽只在关闭的现场证明可用后由下次准入惰性回收；没有证明则保守保留。
这不会替代 PG Recover；全量扫描、跨重启对账和常驻 recovery 是另任务。
models 用既有 `Gate.Catalog`；me usage 先由 Authority.Check 派生本人身份，再接 `Ledger.ListUsage`。

集成测试使用 `AURIGA_AI_TEST_PG_DSN` 指向隔离测试 PG，实际 Store/Reader/Cipher/Gate/Ledger/协议，
IAM/org 是随机端口 gRPC 夹具，绝非真实 IAM 签票端到端。未给 DSN 时具名 skip；CI 缺 DSN 失败。
