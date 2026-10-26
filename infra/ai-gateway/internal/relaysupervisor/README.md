# 现场恢复 Supervisor

`New(ledger, capacity, timeout, backoff)` 创建一组固定 cell 和单 worker。
`Reserve()` 返回 `finish func(*Pending), ok bool`，必须在 Execute 前同步调用。
finish 恰好生效一次：nil 释放，否则复制原 Ref/StopProof 到原 cell，不做 I/O，不二次排队。
预留而尚未交接的 cell 不得被 worker 读取。`CloseAdmission` 只拒新预留，不拒旧 token 交接。

worker 仅轮转现场已交接 cell，按完整 Ref 调 GetRecoveryCandidate，用最新 revision 调原 Recover；
CAS 竞争和任何 COMMIT 不明都下轮重读。不存在 ListRecoveryCandidates、租户扫描或执行权重授予。
reserved 仅凭该调用已返回的路径与原 Core CAS 协议取消；dispatching 必须 unknown，不能按 reserved 退费。
unknown slot_held 只凭现场 channel 真实关闭释放；发送值不证明停止。无 proof 则一直保留责任和耐久槽。
空 Ref、缺行、DB 故障、尚不可见都保留 cell；终态或 unknown 无槽经重读才完成。不会重 Admit 或重派。

`Stats()` 只给占用、已交接、故障数量和关闭状态，不输出引用/凭据。
`Drain(ctx)` 包括尚未交接的预留，超时停止 worker 但保留 cell 并报错，不能解释成执行已停或责任已清。
调用方须先关新准入、取消/排空 HTTP，允许旧调用 handoff，再 drain，最后关 Store/RPC。
独立 PG 操作期限与固定 worker 限制 goroutine 和工作集合；backoff 避免持续故障忙转。

这是易失现场责任，不是耐久队列或跨进程恢复协议。崩溃/重启不能重造 StopProof，旧 durable unknown/slot
继续留置；无租户发现或启动盲扫。真正缺行也不证明未提交，耗尽容量后明确失败关闭。
