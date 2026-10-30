# Device HTTP（D7）

`New(Config) (*Handler, error)` 返回可直接安装的 `http.Handler`，只处理：

- `GET /ai/v1/models` → `Gate.Catalog`。
- `POST /ai/v1/chat/completions` → 一次 `Core.Execute`，JSON 只编码成功 Completion；SSE 使用核心 sink 和落账后 DONE。
- `GET /api/ai/v1/me/usage` → 本次 `Authority.Check` 的 Identity → `Ledger.ListUsage`。

三个操作只接受原 device AI Bearer；IAM 专用在线核验负责验签、audience、device/backing 状态，
Authority 继续核当前成员、权限及配额。没有 idle touch、refresh 或外层重试。
不接受浏览器 Origin/Cookie/Referer、浏览器站点标记和 Auriga-Forwarded-*。
Node 原生 fetch 同样发送的 `Sec-Fetch-Mode` 仅为元数据，不授予任何身份。
目录按正式四字段返回，用量保留正式 unknown/null 与 owner cursor。
数据面错误使用 AiError；本人用量仍为 Problem，依赖故障映射该操作的 common.unavailable。

## 受信 runtime 的安装责任

- Core/Gate/Authority/Ledger 必须来自同一套受信装配；本 factory 不读取请求提供的配置、端点、计量或能力。
  每进程共享 Core；Policy 缺真实可证明计量仍失败关闭。D7 只注入 fake Transport。
- 直接按原始 RequestURI 分派给本 handler；前层不得清理路径、补斜杠、解码路径、自动 HEAD/OPTIONS、
  添加 CORS 或把浏览器、托管、Team、管理请求送入此通道。main 的 --device-config 已接入隔离 D7 回环 runtime；不提供公网/edge 路由。
- `ReadTimeout` 是完整 body 读截止；读取最多 1MiB+1（判超限），不是信任 Content-Length。
  `WriteTimeout` 是每次真实 Write/Flush 的截止，取 sink ctx 截止最小值；取消立即压到当前 socket 截止。
  callback 在 I/O 返回前被撤销或等待完成，没有脱离请求的写 goroutine/队列。
  ResponseWriter 中间件须保留 ResponseController 的能力或提供 `Unwrap()`；不支持读写期限时直接中止请求。
- runtime 的 `http.Server` 仍须配置有限 `ReadHeaderTimeout`、`IdleTimeout`、`MaxHeaderBytes` 和入口连接/并发上限。
  handler 只在 headers 已解析后开始工作。`QueryTimeout` 限定目录/用量操作，`MaxResponseBytes` 限定响应字节。
  核心另有协议总量/事件限制、第一字节/idle/总时限及原短票期限；本包不重复预算、计量或结算。
- SSE 第一片准备就绪才提交 200；header 提交状态独立于写入 n，零字节失败不能改状态。
  开始后只尽力发送一次 AiError 并结束，失败不伪造 JSON 成功或 DONE。
- `ReserveRecovery() (finish func(*Pending), ok bool)` 是强制同步预留协议；Handler 在 Execute 前占固定容量，满/关闭按 ai.budget_unavailable 拒绝，Admit/Transport 均不运行。finish(nil) 释放，finish(Pending) 原位交接；回调不做 I/O、扩容或二次排队，请求取消不取消责任。
  Pending 只交付受信 `budget.Ref` 与现场 `StopProof`，从不写到 HTTP。
  回调只取 Core 的专用 `Output.PendingRef`；Admit INSERT/回调成功但事务返回错误时，
  精确定位仅在此内部端口交接，公开 `Output.Ref` 保持空，不写入 header、JSON/SSE、错误或日志。
  原成功请求的公开关联 ID 和幂等错误语义不变；客户取消后仍同步交接。
  正常返回空 PendingRef 表示 Admit 尚未提交：INSERT 成功才设置 result，事务提交在其后，之后失败必带 Ref。
  此时 finish(nil) 归还容量，响应仍失败关闭；不能从请求头/体、参数、幂等键或旧 ID 补造 Ref。
  panic 不适用该推理，独立 Faulted 标记保留并计故障，原 panic 继续传播。
  非空 Ref 仅表示本次行可能存在，不证明提交、准入或行存在；先在 RLS 下重读真实状态/revision，
  再按真实证据 CAS 调用 `Ledger.Recover`，绝不重派。首次 not_found 不证明最终回滚或责任已清。
  缺停止证明继续保留槽，unknown 金额/token 继续冻结；StopProof 必须是现场全部执行停止的
  closed channel，重启、时间、发送值或缺行均不能替代。reserved 取消 CAS 可 fence 旧提交。
  此定位不是耐久 Pending 队列，进程崩溃仍可能丢失现场交接，不承诺跨重启全覆盖。
  本进程现场后台恢复由 relaysupervisor 接管；跨重启扫描及真实 Transport 属于后续任务。

## 验证

`go test -count=1 ./internal/relayhttp` 的无 PG 测试使用随机回环真实 socket，检查慢读、慢写、取消、
超 body 与重复 header；没有替换 Core 执行接口的 mock。
PG 测试须设置 `AURIGA_AI_TEST_PG_DSN` 指向独立测试库，组合实际 Store/Reader/Cipher/Gate/Ledger/Core/协议。
控制面是明确 fake gRPC，模型 Transport 是明确合成 fake；不是真实 IAM 签票或付费模型闭环。
无 DSN 具名 skip，CI 无 DSN 失败。用量/outbox/冻结/槽与成功 JSON/DONE 的写入顺序均有断言。

`e2e/relay-device/run-pg.ps1 -PgBin <已有PG-bin> -All -DshAnchor <实际安装dsh/package.json>`
创建新 PG 数据目录、随机端口并在 finally 停止，只保留本工位 evidence。
可选 SDK 测试用公开 profile resolver、PluginPackages 和实际 rc.2 LlmRuntime，
以真实 fetch 调本 handler 的 JSON/SSE；缺实际 SDK anchor 时具名 skip，不以 fake Runtime 替代。
不读取用户 profile、不启动用户 DSH、不调用真实模型。
