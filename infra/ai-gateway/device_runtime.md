# D7 device 回环 runtime

`auriga-ai-gateway --device-config /protected/device.json` 启动独立进程，不能同时指定
`--admin-config`、`--team-config` 或 `--network-config`。这只是本进程现场恢复和内存合成模型的
真实 HTTP / 控制面 RPC 协议接线，不表示正式模型中转已通，也不是真实 IAM 签票端到端验收。

配置为最多 1 MiB 的单个 JSON 对象，拒绝未知字段、尾随数据、越界数值；错误不回显配置值。
以下字段是部署方受保护的配置，不能取自调用者请求：

```json
{
  "listen": "127.0.0.1:0",
  "d7SyntheticOnly": true,
  "syntheticTenant": "01900000-0000-7000-8000-000000000001",
  "masterKeyFile": "/protected/master.key",
  "store": {"dsn": "", "migrationDsn": "", "maxConnections": 8},
  "iam": {"target": "127.0.0.1:19001", "serviceTokenFile": "/protected/iam.jwt"},
  "org": {"target": "127.0.0.1:19002", "serviceTokenFile": "/protected/org.jwt"},
  "capacity": 64,
  "maxConnections": 64,
  "maxInFlight": 32,
  "headerMillis": 5000,
  "readMillis": 15000,
  "writeMillis": 5000,
  "idleMillis": 30000,
  "shutdownMillis": 10000
}
```

监听只接受数值 loopback IP；端口 0 表示随机端口，启动日志给出实际地址。无公网、edge 或 TLS
监听扩展。IAM 和 Org 必须分别配置，可同址；RPC 完整沿用 `team.DialRPC` 的每次读取 service JWT
和非回环 mTLS 校验。远端 mTLS 另配置 `certFile`、`keyFile`、`caFile`、`serverName`。
身份仍由 DeviceAccess 与 Org 在线核验，不能用 admin 登录或证书代替设备身份。

DSN 使用 `AURIGA_AI_PG_DSN`、`AURIGA_AI_MIGRATION_PG_DSN` 覆盖，master key 使用
`AURIGA_AI_MASTER_KEY_FILE` 覆盖；key 必须是按现有 Cipher 规则保护的 32 原始字节普通文件。
两个 DSN 的数据库名均强制为 `auriga_d7`，运行角色禁止所有者/特权角色并受 FORCE RLS 约束。
**必须新建隔离的 D7 测试数据库；会产生假账，禁止连接生产收费库。** 名称检查不能证明部署方
确实隔离了数据库。runtime 不创建数据库、不 seed 模型，也不修改正常模型配置。

省略数值时采用上面的默认值。capacity 为 1–4096，连接为 1–1024，在途为 1–连接上限，
毫秒期限为 10–60000；store.maxConnections 为 0–64（0 沿 Store 默认 8）。
头部 32 KiB，header/body/idle 有服务端期限；Handler 对每次真实 Write/Flush 设置期限，
Server.WriteTimeout 显式为 0，避免统一写期限误斩正常 SSE。HTTP 直接挂原 Handler，不经 ServeMux；
保留 raw URI、原 bearer、浏览器与转发标记拒绝规则。额外连接立即关闭，在途满按现错误目录拒绝。

## 精确合成边界

部署方必须通过受保护的既有管理/配置流程准备同一租户下的以下配置及 member grants：

- 模型 `auriga-d7-synthetic`，上游名 `auriga-d7-memory-v1`；model revision=1，context=64，maxOutput=2。
- provider `01900000-0000-7000-8000-000000000004`，kind=`openai_compatible`，revision=1，
  URL=`https://synthetic.invalid/v1`，activeCredential 指向下项；provider 超时和并发仍遵循既有校验。
- credential `01900000-0000-7000-8000-000000000005`，active，revision=1，cipherVersion=1，
  使用该 tenant/provider/credential/version 绑定加密的测试用非生产秘密。
- 价格 USD，version=`d7-fake-v1`，effectiveAt=`2026-01-01T00:00:00Z`，
  input=1000000、output=2000000 micros/million。Policy 每次核精确版本、能力、价格和目标。

假协议将每个完整有效的消息历史定义成一个固定 32-token 输入符号，生成固定 `OK`，固定 2-token 输出。
这不是 tokenizer，也不是实模型的上界估计。max_tokens 必须恰为 2；工具、temperature、top_p、stop
均不支持。普通 JSON 和 SSE 均验证 32/2/34 usage；内存响应生成实际结束后才关闭 Stopped。
Transport 无 HTTP client、DNS、上游连接、重试或收费调用；真实/未知模型与非精确配置均无 Policy。

## 责任与停机

每次 Execute 前先同步预留固定 cell，满或关闭时 Admit/Transport 为 0。Execute 完成后恰好一次
finish(nil) 释放，或 finish(Pending) 将原 PendingRef/StopProof 原位交接；交接不访问 PG、扩容或二次排队。
worker 独立于请求取消，单 worker 轮转这些现场 cell，短 context 重读完整 Ref 和最新 revision 后做 CAS。
无租户发现、启动/周期盲扫、重派或猜退款。带 Ref 的缺行/提交不明/PG 故障保留 cell，容量可能耗尽。
正常 Execute 返回空 PendingRef 时按未提交释放：Admit 只在 INSERT 成功后赋 result，COMMIT 位于其后；
只要 result 已赋值，事务失败就带完整 PendingRef。因此正常返回的空 Ref 可证明本事务未提交耐久行，
Handler 调 finish(nil)，错误响应仍失败关闭。该推理不适用于 panic；panic 可能发生在派发后，
按独立 Faulted 标记保留 cell 并计故障，不因丢失返回值而虚构未提交或停止证明。
reserved 取消 fence 旧 dispatch；dispatching 转 unknown；只有实际关闭的现场 channel 才释放 unknown 槽，
发送值、超时、取消和 Close 均不算停止证明，金额/token 保守留置，usage/outbox 不重复。

已确认 unknown 持槽且无闭合证明时先本地检查 channel，未关闭不查 PG；首次读取仍须区分可取消的 reserved。
故障按 cell 指数退避（上限 5 秒）；每轮 PG 预算最多 min(操作期限,50ms)、最多 16 次操作，跨轮轮转，
新交接以一个合并调度提示优先处理，责任始终在原 cell，不增加责任队列。StopProof 只允许 close，不能 send。

停机先关闭 Handler/Supervisor 新准入，以 shutdownMillis 给 HTTP 优雅完成宽限；之后才取消剩余 HTTP，
超时则 Close，再以独立 shutdownMillis drain。已预留调用仍能交接。HTTP/恢复均正常完成才关 RPC/Store。
任一窗口超时都返回非成功，仅输出未决/故障计数，不暴露 Ref；不在错误路径同步调用 pgxpool.Close
或等待不响应取消的 worker。未决路径的主动等待上界是两个窗口之和（最多120秒，另计调度开销），
不会再附加等待借出连接归还。CLI 非零退出由进程回收剩余资源；嵌入式调用者须待旧工作退出后收尾，不能复用该 runtime。
进程退出或崩溃会丢失内存 cell/proof；重启不构造 proof、不重新接管旧责任，durable unknown/slot 仍保守留置。
本项不承诺跨进程、跨重启责任全覆盖，生产恢复和真实 Transport 需要后续设计。

## 验证

设置 `AURIGA_AI_TEST_PG_DSN` 指向隔离的测试 PG 后执行 `go test -count=1 ./...`。
root fixture 会在该测试 PG 创建专用 `auriga_d7` 库和随机非所有者角色，使用两个真实回环 gRPC listener
以及随机回环 HTTP。假 JWT 由 fixture 识别，没有真实 IAM 签名。测试保留既有 #204 跨二进制 ACL 锁。
同组恢复测试还覆盖真实账本 CAS、竞争恢复、旧 revision、COMMIT 回包不明、缺行与发送值攻击。
