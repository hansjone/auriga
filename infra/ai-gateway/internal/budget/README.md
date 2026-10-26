# 耐久预算账本（M2）

本包仅供 gateway 内部调用。没有 HTTP 鉴权、模型出站、tokenizer、定时退款或 NATS 发布器。
复用 `store.Transaction`；运行角色必须与迁移所有者分离。业务表均强制 tenant RLS，
主体读写固定 `tenant_id + subject`，恢复候选扫描显式固定单个 `tenant_id`。`budget_deployment` 仅保存全库单币种，
没有租户信息；首次有效准入固定币种，不允许运行时改变币种。

## 接口与 M3 接线

- `New(store, currency, policy)`：部署配置；policy 为 nil 时默认 2 个在途、25%。
  配置来自部署端，不能从请求合并覆盖；保留指针副本，返回的快照不能改变配置。
- `Admit(ctx, TrustedSnapshot, Request)`：原子领取主体槽、UTC 日 token、UTC 月金额和幂等执行权。
  返回服务端 UUIDv7、固定的生成类型 `BudgetAdmission` 与 revision=1。
- `MarkDispatching(ctx, Ref, revision)`：reserved 的 CAS。**只有收到该调用提交成功的调用者**
  可以提交一次上游请求；不重试上游、不故障切换。提交结果不确定时不能自行执行。
- `Settle(ctx, Ref, revision, Finish)`：只接受 dispatching；可信最终 usage 且执行停止后结算一次。
  failed/cancelled 同样不能推断免费。缺 usage 一律 unknown；无可信停止证据保留并发槽。
- `Recover(ctx, Ref, revision, RecoveryEvidence)`：只释放可证明未提交的 reserved；
  dispatching 总是转 unknown；unknown 确认停止后只能释放槽。没有按租约退款路径。
- `GetReservation(ctx, Ref)`：供可信监督器在重启/提交结果不明后读取持久状态与 CAS revision。
  读取 dispatching 不授予执行权；后续监督器须从耐久请求表发现待恢复请求，本包不运行扫描服务。
- `ListUsage(ctx, tenant, owner, limit, cursor)`：按指定主人读取已终结用量，limit 1–200；
  持久 `(recorded_at,request_id)` 升序游标绑定 tenant/owner，不承诺跨分页事务的全库快照。

### 内部恢复观察（不执行恢复）

`Admit` 只有实际 INSERT 一行并成功完成事务回调后，若 Transaction/Commit 返回错误，
才通过专用内部错误携带本次服务端生成的完整 tenant/account/request 定位。
`AdmissionPendingRef(err)` 仅供可信内部调用方提取；错误常规格式化隐藏定位与原因，
`Unwrap` 保留内部 `errors.Is/As`。验证、预算拒绝、回调/INSERT 失败、仅生成 UUID，
以及幂等命中的旧 `OriginalRequestID` 都没有本次 hint。
事务端口仍要求真实 Store 的 RLS、回调成功后 Commit 语义；受控测试包装仅注入该边界的失败。
hint 只表示本次行可能存在，不证明提交/存在，也不授予 Reservation、派发、结算或退款权。
失败仍返回 nil Reservation，不重试 Admit；后续须在 RLS 下重读真实状态/revision 再 CAS，绝不重派。
即使 `GetRecoveryCandidate` 返回 not_found，也不能断言最终回滚或责任已清。
它不是耐久 Pending 队列，不提供跨重启全覆盖；后台扫描/恢复与租户发现仍未实现。

`ListRecoveryCandidates(ctx, tenant, limit, cursor) (*RecoveryPage, error)` 仅供可信 runtime 内部调用；
显式提供一个经 UUID 验证的 tenant，不能从未验证的客户端参数直接调用。它不发现全局租户，
没有 HTTP、新权限或外部 API。完整部署租户集合及对所有新准入的集合约束由后续任务负责。
返回 reserved、dispatching 和仍 `slot_held=true` 的 unknown，排除已释放槽 unknown 和其它终态。
`RecoveryCandidate` 仅含 `Ref/State/Revision/SlotHeld/CreatedAt`，没有 admission、price、key hash、
fingerprint、secret、正文或数据库原始 JSON。`RecoveryPage.Items` 空时也非 nil，末页 `NextCursor=nil`。

limit 严格 1–200；SQL 最多读取 limit+1 行，使用完整 `(request_id,subject)` keyset 升序，
不假设 requestId 在租户内跨 owner 唯一。游标最多 512 字节，为规范无填充 base64url 编码的
`recovery-v1|tenant|request_id|subject`，绑定租户及恢复 scope，拒坏编码/坏键/跨租户/Usage 游标。
游标仅用于定位，不是授权凭据。`0011_recovery_scan.sql` 只增加与候选条件相同的
`(tenant_id,request_id,subject)` 部分索引，不修改旧表、RLS 或账务数据。

`GetRecoveryCandidate(ctx, Ref) (*RecoveryCandidate, error)` 按完整 Ref 读取同一最小投影，
包括终态与已释放槽 unknown，供已知 Pending 重读最新 revision/槽状态；缺行返回 `common.not_found`，
不能制造 allow。两个端口都在现有 `Store.Transaction`/FORCE RLS 内仅 SELECT，无行锁或账务写入。
分页之间并发新准入或状态变化可能需要下一扫描周期从头发现，单次分页不构成全库原子快照。

候选只是瞬时观察，不是恢复、退款、重新提交或执行停止证据。重启/租约到期不能证明停止；
unknown 继续冻结预占，丢失停止证明时继续留槽。禁止盲扫取消活跃 reserved 或改写活跃 dispatching，
它们会破坏正常提交/结算。reserved 后续仅处理已知现场 Pending 或覆盖全部执行进程的可信独占窗口；
独占及 Supervisor 不在本端口实现内。现有 reserved 取消的 revision CAS 可阻断旧 MarkDispatching，
不因此引入 owner/lease/generation 框架。扫描不调用 Recover/MarkDispatching/Transport，也无后台服务。

M3 必须从已验证 JWT 和在线 modelgate/组织配额端口取得 tid/sub、有效成员/ACL、
实例或设备来源、固定 model/provider/credential、价格和限额；不能让客户端选择账号或扩大限额。
`TrustedSnapshot.ValidUntil` 及 token 到期由数据库时钟在取得锁后核验；有效期应足够短，
最终派发前仍须检查权限/撤销和到期。该输入不是外部 DTO，不得从请求 JSON 反序列化生成。
需要可信 tokenizer 或模型上下文保守上界提供输入 token 正上限，以及明确的最大输出 token；
不能拿字符数当精确 token。本包不会编造 tokenizer 或在线权限结果。

`Request.Fingerprint` 是规范化、严格验证后请求（含模型及生成参数）的 SHA-256；
只持久 digest 与 key 的 SHA-256，不保存 key/prompt/正文。相同 key 的指纹不符为 common.conflict；
相同在途为 ai.request_in_progress；所有终态为 ai.replay_unavailable，带 originalRequestId/outcome。
同 key 的耐久记录无自动过期/删除，终态永不重新执行。无 key 每次都是新请求，仍受主体预算限制。
适配器只映射 `Error` 的契约码，其他存储错误映射为预算不可用，不能把原始 SQL 错误直接回传用户。

## 金额、限额与异常

金额为 `ceil((input × inputRate + output × outputRate) / 1,000,000)` micros。
中间乘加使用任意精度，落库/跨接口数值限制在 0–9007199254740991；token 合计、累计占用也检查溢出。
限额 nil 表示不限、0 表示禁止（包括零价格模型），负数或缺安全上界失败关闭。
在途上限按有限周期配额的百分比向下取整，再与配置的正数绝对上限取小；
不限周期必须配置对应正数绝对上限。总周期核验是已结算 + 所有未释放预占 + 本次预占。
主体并发槽或在途 token/金额上限返回 `common.rate_limited`；周期 0 或周期总额不足返回
`ai.budget_exceeded`。拒绝准入不改变既有占用或创建请求执行权。
主体在途金额/token 包含旧日月留下的 unknown，不因换周期消失。

固定准入的日/月周期与价格，不按结算时刻改写。可信 final usage 释放全部在途预占并向原周期入账；
reserved 证明未提交时记录 confirmed 零用量。unknown 的三个用量字段显式为 null，
最坏 token/金额继续冻结；可信停止证据只允许释放并发槽。
负数、超预占或价格/币种冲突会同事务写 unknown、`invalid_final_usage` 对账原因和 outbox，
然后返回 ai.budget_unavailable；返回的 Reservation 标识已持久异常。
unknown 是终态，本轮没有自动重新定价/补账/退款接口，后续人工对账必须另行设计并审查。

锁序为主体行 → 请求 → 固定周期。等待后采用新的语句读取；更高数据库隔离级别的序列化失败
只会拒绝本次操作，不授予执行权。结算、幂等终态、Usage 和完整 usage.recorded CloudEvent
在同一事务提交，outbox 不依赖 NATS 可用性。本轮仅写耐久 outbox，不包含投递/重试/删除器。
生产角色使用现有安装流程授予业务表 DML、budget_deployment SELECT/INSERT；没有新角色创建逻辑。

## 验证

设置 `AURIGA_AI_TEST_PG_DSN` 为隔离测试库的迁移角色 URL，运行 `go test -count=1 ./internal/budget`。
沿用 testpg 创建独立的非所有者/非 BYPASSRLS 运行角色；CI 缺 DSN 仍直接失败。
真实 PG 覆盖多连接池、6 个子进程、同 key 与不同 key 竞争、CAS、重复结算、0/不限/上限、
旧周期结算、unknown 不退款、租户隔离、分页及 outbox 失败时全事务回滚。
所有模型和用量均是假数据，没有真实模型调用。
