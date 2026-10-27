# 普通会话最小耐久单播

本增量只开放同一 tenant、双方在线的 session→session、kind=message、durable、atomic 单播。
首联、human、group/subagent、离线目标首次提交和模型自动回复不在本轮入口范围。

## 提交与身份

插件向 gateway 的私有设备 HTTPS 路径 `POST /api/ai/v1/network/submit` 提交完整
`send.schema.json#/$defs/SubmitRequest`，成功为 HTTP 200 和完整 `SendResult`。
使用现有 device bearer；gateway 必须从设备的已登记、当前有效连接中选出唯一来源
`NetworkMailboxConnection`，不得接受 body/header 自报 tenant、owner、node、from 或连接 tuple。
JSON 中 `source.source_binding` 只是引用：collab 核它属于受验证来源连接，并只用已保存的
SOURCE_GET 有效观察构造身份、策略版本及 SourceContext。未知可选 Message 字段原样进入
冻结 Message 与 JCS 摘要，禁止客户端声明 origin/correlation_id/causation_id 或署名身份。

gateway 调 `NetworkMailboxService.Submit(connection,submit_json)`；connection 是来源连接。
collab 由目标 typed-local 与当前 READY 状态解析目标连接，事务外调
`AuthorizeNetworkDelivery`，该第五 RPC 的 connection **始终是目标连接**。
现有 svc:ai-gateway、服务 JWT、业务 tenant 委托、可信内部传输和 RLS 边界不变。
gateway/插件接线由后续工作线实施，本契约不宣称 HTTPS 入口已经上线。

## 发行、幂等与恢复

已登记设备的第一次 mailbox READY 为其 node 发行服务端 `producer_epoch`，在签名
READY_RESULT 中返回；数据库随 node 保存，后续 READY/连接 takeover/进程重启不重发。
插件须在第一次 SEND 前将其与原 SubmitRequest、producer key 一起耐久保存。
重连不能使用 connection_epoch 或新随机数替换 producer_epoch。

唯一键为 `(tenant, source_binding, producer_epoch, idempotency.key)`。
同键请求必须与冻结原请求 JCS 完全相同；变更正文、目标或任一字段返回 `network.conflict`。
同键重提同时承担查询：已提交时返回保存的**完整原 SendResult 字节**，不重新分配标识、
刷新时间、修改状态或盖章；即使目标后来断线、来源后来停发，当前已验证的同 node 设备仍可对账。
跨 node/tenant 不得读取原结果。幂等 pending、完整原结果及 continuation 至少保留 7 天；
当前实现不自动清理，所以不会提前删除，也不能把过期查询 miss 当作安全重发依据。

短事务冻结原请求、完整 Message/SourceContext、稳定 message/delivery/recipient/thread 标识
及来源/接收快照；事务外核第五 RPC；最终短事务按固定顺序锁 node、来源与 typed-local，
重核 stop/permission fence、观察内容与期限、连接代际/boot、目标登记和 receive_version。
最终同事务保存正文、SourceContext、线程序号、邮箱序号、recipient、幂等终态和原 SendResult；
只有事务提交成功才返回 platform_stored。任何远程 IAM 调用都不持 PG 事务、同步器或连接资源。

第五 RPC 拒绝或最终 CAS 失败可以留下原 pending，但正文、邮箱项及 recipient 均为零。
pending 重试复用原 Message/身份与期限；不得借新观察、新连接或新政策延长原许可。
确认丢失/超时/断线均保留原 key；绝不自动换 key 重发。冻结事实发生变化的 pending 保持
失败关闭，需要上层明确处理，不能把它当作 platform_stored 或擅自改写原请求。

## 显式接收与最小收件包装

新登记 session 默认 receive_none（`receive_enabled=false`，receive_version=1）。会话所属成员
在插件设置/会话控制里开启“接收消息”后，插件经现有鉴权 mailbox 控制链发送
`receive_set`：当前 epoch/boot、完整八字段 typed-local 见证、enabled 和 expected_version。
gateway 转 `ReceiveSet`。collab 在当前连接和登记锁内 CAS，返回签名
`receive_set_result(enabled,state_version)`；同版本同值重试不重复增加版本。
关闭或关闭后重开均改变版本，已在途的旧发送不能越过这个 fence。
规则以当前同 tenant 接收许可为准，不另建好友/首联关系；不得直接改插件 ledger 或数据库开通。

`AttemptBegin` 搬运现有 attempt_begin，按 recipient/attempt_id 幂等计数；
`ExecutionAdmit` 搬运现有 execution_admit，稳定 host_request_id=recipient_id，首次准入获许可，
重试只返回 already_admitted。仅开放普通 session/process；两者均核当前 READY、boot、目标
typed-local、接收许可和 recipient。每次第五 RPC 在事务外完成，最终重核快照后才签名。
这些入口不自行调用宿主或模型，不证明宿主持久接受。

FETCH 返回的 continuation 是服务端保存的随机不透明位置，绑定 tenant/node/epoch、原始
resume_cursor、scan_after 和 snapshot_head；使用时不得篡改、跨节点或跨 epoch 搬用。
同一分页链排除快照之后的新邮件。held/paused/state_barrier 仍失败关闭，不能静默忽略。
FETCH delivery.recipients 的 `state_version` 为服务端 recipient.state_version；
RECEIPT 的 expected_state_version 必须取该值，不能取插件 ledgerRevision。
ADMIT_RESULT 同样输出服务端版本；RECEIPT 原有 CAS 与证明验证保持生效。

## 验证与交接

真实 PG 用例位于 `NetworkSourceControlPostgresTest#send*`，覆盖同键并发、回复丢失/重启重连
对账、第五拒绝零正文/邮箱、来源 stop/permission/观察/连接/接收/登记竞争，以及 FETCH
分页、attempt 幂等、准入版本。主控从精确 HEAD 使用本模块测试资源内
`network-durable-send/verify-durable-send.ps1 -ExpectedHead <40位HEAD> -Run`，保留 Control
与 NoDedup 编译突变的独立 archive、哈希、命令、退出码和 Surefire 断言证据。
所有合并仍须非作者独审与精确 HEAD CI；本增量触及 contracts，标【待审查】。
