# N1 网络集成测试骨架

当前可运行的是 **Host adapter × 两个插件 NatsTransport × 真实 NATS/WSS 接缝**。
完整网关/collab PostgreSQL 往返仍有具名 `skip`；本目录不能作为 N1 已验收的依据。
测试不调用模型，不连接真实部署，不需要 Docker，也不运行常驻服务。

## 运行

从仓库根目录执行（Node ≥22，插件依赖已按仓库锁文件准备）：

```powershell
# 指向现有、可信的仓库锁定版本 nats-server 可执行文件；不会自动下载。
$env:AURIGA_NATS_SERVER = '<absolute-path-to-nats-server>'
node --test "e2e/network/*.test.mjs"
```

也支持现有插件辅助的 `AURIGA_TEST_CACHE` 查找规则；详见
`clients/dsh-plugin/test/network/transport/support/nats-server.js`。
Broker 版本取自 `services/test-support/.../NatsServer.java` 的 `VERSION`，当前为 2.15.1。
运行前检查实际 `nats-server -v`；不存在、版本不匹配、启动或连接失败均令测试失败，不自动跳过。
现有 Go 接入 spike 的内嵌版本为 2.12.6，本骨架采用 JS/Java 测试辅助锁定的可执行版本。

Linux CI 使用同一条 Node 命令，先准备对应平台的可信 Broker 制品和插件依赖即可。
建议主控把该命令加入现有 CI；本任务没有修改工作流。报告中的通过数与 skip 数必须分开统计。
当前预计 **3 pass / 4 skip**，后续接线完成应替换对应 skip 的测试体并删除跳过原因。

## 装配与信任边界

每个测试创建一个随机回环端口 Broker、随机 CA/WSS 证书、临时 JWT 账户、随机 subject 前缀，
两个不同 node 的生产 `NatsTransport` 和 `createHostAdapter`，以及两份独立文件账本。
复用 #127 的假宿主和 NetworkPort fixture，仅用于 local 登记和执行准入。
每个节点绑定一个非默认 cwd 的冷普通会话，并保留一个不应收到消息的其他会话。

驱动通过源端 `NatsTransport.send` 发送**宿主 adapter Page fixture**，测试侧无状态 relay
经真实 Broker 将其转到对端的 down subject。目标 adapter 持久接受后，经生产 `encodeReceipt`
生成 RECEIPT 并从目标 WSS 发回，测试侧观察其 recipient、delivery、mail_seq、摘要及接受方式。
Page 是宿主接缝输入，不是经过网关的合法签名 FETCH_RESULT；`verified: true` 只表示 fixture
自身指定信任。JWT 使用隔离测试账户，未测试 IAM callout 或生产 subject ACL。

relay 没有邮箱、数据库、回执提交、游标、READY、HEAD 或 FETCH 实现。
不调用 fixture 的 `pump` / `commitReceipts`，不发送伪造的 RECEIPT_COMMITTED，不调用
`adapter.markCommitted`；因此观察到回执后 `pendingReceipts()` 仍须保留原项。
所有连接、子进程、账本句柄和临时目录均由测试清理；依赖启动失败也会执行已注册清理。

## 已运行覆盖

- `unicast fixtures travel both ways...`：A→B、B→A 各一个 fixture，两个插件真实 WSS 链路，
  两份 RECEIPT，指定会话各 prompt 一次，其他会话零输入，不创建新会话。
- `lost RECEIPT...`：在宿主接受后、目标 transport 发送回执前丢弃一次；同一 delivery /
  recipient / 摘要重投，Host 历史仅一个 requestId，准入仅一次，重发回执仍待权威提交。
- `crash after durable host acceptance...`：在产品已有 `after_host_accepted` 故障接缝抛错，
  关闭旧 WSS、放弃旧 ledger 句柄、假宿主丢易失状态，重新从文件打开 ledger 并建新 WSS；
  验证 binding/cwd、账本修订与待发回执恢复，再重投仍仅一次 prompt。
  这是进程内模拟插件崩溃，不证明 OS kill、断电、真实宿主持久性或持久协议游标。

## 必须等待的验收

- 完整单播往返：#138 网关→IAM、#139/#140 两端签名/验签、collab↔IAM，
  以及网关 Mailbox→collab PG 的 RPC 装配。当前 `Mailbox` 仅接口与内存实现，
  `EncodeFetchResult` 输出不含插件所需完整 message/source_context/attempt_id。
- 持久游标重启与 READY 自动对账：`NetworkSession.resumeCursor` 只在内存，
  宿主 ledger 无协议 checkpoint。须补正式持久接线，不能写测试侧游标替代。
- 已提交回执响应丢失：须用真实 PG 回执事务、权威 committed_cursor 和 READY 对账，
  不能以 NATS 收到回执冒充事务提交。
- 真实双 DSH、非默认 cwd、冷恢复、首联并发映射及同机强制隔离：须固定真实宿主 build /
  hash、两个隔离 profile 和真实宿主 adapter 接线。fixture 的普通会话语义不能替代兼容验收。
- 目录/跨租户/授权/首联源预算、回滚/克隆、解析护栏、签名轮换/撤销、组播广播与部分回执、
  N2 子 agent、容量/HA/chaos 等故障矩阵项均未覆盖，不据此宣称通过。
