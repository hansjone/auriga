# N1 网关联网鉴权运行方式

启动：`auriga-ai-gateway --network-config ./network.json`。`--version` 不初始化服务；
未指定运行配置会失败。Team MCP 继续用 `--team-config`，两个入口各用一个进程。
本入口启动 callout、已知连接吊销检查及 collab 邮箱 gRPC 转发，不启动 Broker。
READY/epoch/逐操作授权与耐久游标由 collab 权威处理；collab 服务端仍须另行实现新契约。

示例配置（所有秘密只引用文件）：

```json
{
  "brokerConfigFile": "./nats.conf",
  "serverAudience": "broker-a",
  "clientAccount": "CLIENTS",
  "issuerSeedFile": "./secrets/callout-account.seed",
  "xkeySeedFile": "./secrets/callout-curve.seed",
  "iam": {
    "Target": "127.0.0.1:9090",
    "ServiceTokenFile": "./secrets/ai-gateway.jwt"
  },
  "collab": {
    "Target": "127.0.0.1:9091",
    "ServiceTokenFile": "./secrets/ai-gateway.jwt"
  },
  "data": {
    "url": "nats://127.0.0.1:4222",
    "username": "network-data",
    "passwordFile": "./secrets/network-data.password"
  },
  "protocolSigning": {
    "privateKeyFile": "./secrets/network-protocol.pem",
    "keyId": "r1-protocol-1",
    "keysetVersion": "1",
    "notBefore": "2026-10-25T00:00:00Z",
    "notAfter": "2027-10-25T00:00:00Z"
  },
  "auth": {
    "url": "nats://127.0.0.1:4222",
    "username": "network-auth",
    "passwordFile": "./secrets/network-auth.password"
  },
  "ops": {
    "url": "nats://127.0.0.1:4222",
    "username": "network-ops",
    "passwordFile": "./secrets/network-ops.password"
  },
  "redeemTimeoutMillis": 500,
  "activeIntervalMillis": 500,
  "requestTimeoutMillis": 250,
  "maxKickAttempts": 3,
  "maxConnections": 5000
}
```

`brokerConfigFile` 必须是 Broker 启动时使用的同一个受信配置文件（保持不可变，变更时一起重启）。
其中必须显式设置 `server_name`、`system_account`、`authorization.timeout`、
`authorization.auth_callout.{issuer,account,auth_users,xkey}`。网关直接读取该 timeout，
校验 redeem timeout 严格更短；WebSocket 若另设鉴权 timeout，必须与其相等。
issuer/XKey 必须分别匹配两个 seed 文件。缺失、错钥、错账户、不同 Broker 都拒绝启动。

客户端账户必须明确配置 `limits { max_payload: 65536, max_subscriptions: 8 }`
（可收紧 payload，上限 64KiB）。v2.12.6 config-mode 的 User JWT 限额不代替账户限额；
网关同时把该账户配置中的限额写入 JWT。账户导入/导出沿既有精确 up/down/reply subject 配置。
`auth` 用户必须属于 callout account；独立 `ops` 用户必须属于 system account；两者均列入
`auth_users`。ops 只授予 `$SYS.REQ.SERVER.*.CONNZ`、`$SYS.REQ.SERVER.*.KICK`
发布权及其私有请求响应订阅权，不授予外部 DSH。

IAM 沿用 Team 的 `RPCConfig` 与 `DialRPC`：服务 JWT 文件每次 RPC 重新读取，支持外部轮换；
仅接收 `aud=auriga.internal`、`ptyp=internal_service`、`sub=svc:ai-gateway`。
IAM 配置 `auriga.grpc.server.tenant-delegates` 包含该主体，
`auriga.iam.network.server-audiences` 包含本例的 `broker-a`，并启用原 gRPC 监听器。
业务租户经 `auriga-tenant-id` 委托，不使用设备令牌代替服务身份。
非数字回环 IAM 地址必须配置 `CertFile/KeyFile/CAFile/ServerName`，延续现有 mTLS 检查。
非回环 NATS URL 必须为 `tls://`，同时配置 `certFile/keyFile/caFile`。
外部客户端只允许 WebSocket；其公网入口仍须按整体设计提供 WSS/TLS。

NATS CONNECT 的 `auth_token` 是 `contracts/network/connect-token.schema.json` 定义的 JSON：
`tenant/node/device/ticket` 仅用于路由，IAM 重新核验全部绑定；audience 来自受信配置，
Broker ID 来自已认证控制连接的 INFO，并须匹配签名 callout 请求。不得记录 CONNECT 凭据。
此任务未新增 HTTPS register/prepare 入口，准备端返回 IAM 的一次性票据后应按该契约组装 token。
私有 ingress 为 ASCII 原始 ticket 的 SHA-256 前 16 字节的小写 RFC4648 base32（无 padding），
准备端可提前返回精确 up/down/reply subject；客户端不需要读取 Broker 内部的 User JWT。
该值来自总部随机票据，不从用户/node 身份推导，也不代替 IAM 鉴权。
IAM 成功授权前提交 `network_ingress`，保存 server generation/CID/user key 与私有 ingress；
原始票据及访问 token 不落该表。票据先独立提交作废，设备核验异常、连接绑定冲突也不能复活。

每轮对账只取一次完整、按客户端账户过滤的 CONNZ，网关先按租户分组、再按 realm/node 去重，
每租户调用一次 IAM active，最多 64 个租户 RPC 并发；每组在取得 worker 后才开始独立的
requestTimeoutMillis（≤250ms）预算，不把排队时间算成该租户的 IAM 故障。
每组的成功、失败与退避隔离；一组错误只给本组连接累计失败，其他组继续采用真实结果。
响应缺项、重复或包含额外身份均视为该组查询失败。每个批量 RPC 携带原服务 JWT 和该组自己的
委托租户元数据，不再借第一个节点的租户代查其他租户；身份列表只能来自此前兑换成功的记录。
IAM 的每组查询进入独立 FORCE RLS 作用域，用集合 SQL
核 node/设备/账户/portal 会话及期限，不逐节点调用 credential，也不扩大外部用户权限。
SQL 时限不超过 RPC 剩余期限，取消后停止处理后续租户；升级时先部署含批量接口的 IAM，再更新网关。

明确 active=false 立即锁存吊销；查询失败不会单次即踢。连续失败达到 3 次或首次失败经过
1.5 秒宽限后才锁存，查询恢复即清失败计数；新连接兑换及每条业务操作仍失败关闭。
查询/轮询有 ±20% 抖动和有界指数退避；故障驱动的首次 KICK 另打散 0–250ms，
明确失效不受该延迟影响。KICK 失败保留吊销状态，最多 1–5 次、退避重试并报警。

检查间隔须为 1–500ms，单次请求时限为 1–250ms，Broker 鉴权/建立宽限最多 2 秒，
最多跟踪 10000 条连接，满额在兑换前拒绝。
KICK 阶段最多 128 并发、每轮总预算 1.5 秒，未开始的条目保留到下轮，优先处理尚未尝试者。
全量 CONNZ 是本轮 KICK 的归属证明：逐个核 server generation/CID/account/ingress；
同 Broker 世代 CID 不复用，因此无需为每次 KICK 再发 CONNZ。截断/错误快照不据此删记录；
持续失败达到 1.5 秒即告警，之后对已锁存吊销者按受信 callout 的 server generation/CID 降级 KICK。
最长轮询抖动 600ms、一次 CONNZ 和 IAM 各 250ms、前轮 KICK 1.5s 时，从首次快照失败到启动
降级的保守上界为 1.5+0.6+0.25+ceil(租户组数/64)×0.25+1.5 秒（调度余量另计；≤64 组时 4.1 秒）。
因此 5000 连接/50 租户的单测不能代表上万独立慢租户的撤销 SLO；生产容量须实测并按租户数扩容。
吊销锁存后的所有新操作立即被拒绝，FETCH 在 RPC 完成发布前再次核锁存，因此不依赖快照恢复。
新连接仍有一个 auth timeout 的建立宽限期；正常断连由快照回收。
5,000 连接（50 租户）的规模单测模拟每租户 IAM 100ms、每个 KICK 10ms，核对一次 CONNZ、
50 次租户批量 IAM、全 5,000 次 KICK 成功并回收绑定，整轮小于 4 秒，加最大轮询抖动 600ms 仍在 5 秒内。
这是网关预算与算法的验证；真实 IAM 数据库/Broker/主机须满足相同响应预算，真实容量压测交 E2E 线。
控制面不可用时无法承诺物理断连时限，仍由 JWT（最长一小时且不超过 IAM 授权）到期兜底。

IAM 在签发绑定前重新推导并核对 ticket 的 ingress；Java API 只保留必须带绑定的 redeem。
V7 增加 (tenant_id,expires_at,id) 清理索引及只含租户 ID 的维护目录，迁移时事务内回填旧租户，
提交前恢复 FORCE RLS；后台每分钟扫描最多 32 个租户、每租户清理最多 256 条过期绑定，
使用独立租户事务、SKIP LOCKED 和 2 秒 SQL 时限，永久离线节点不依赖重连触发清理。
当前已知连接注册表属于网关进程；进程崩溃或重启不会从 IAM 历史表重建在线连接，
旧连接依赖原 JWT 到期及应用层逐操作授权。控制连接不自动重连，断开即退出并要求重新启动，
防止把新 Broker 世代当作旧 server ID；多 Broker/跨进程恢复仍需后续接线。

Go 通过 `infra/gen/auriga/iam/v1` 的生成客户端与消息类型调用 IAM，测试服务也使用生成的
服务注册函数。在 `infra` 目录执行 `go run generate.go` 同步生成物；Java 类型继续由
原 Maven contracts 模块生成。

## 邮箱生产接线

新增必填 `collab/data/protocolSigning`；缺少时启动失败。`collab` 复用 IAM 的 DialRPC、
轮换服务 JWT 和 mTLS 限制；调用身份仍为 `svc:ai-gateway`。collab 必须只允许该服务代业务租户，
逐次核 `connection.tenant_id == auriga-tenant-id`，并与 IAM 持久 ingress 记录核对 server/CID/account/
realm/node/ingress 及当前授权。上行 JSON 和 NATS header 内自报的身份永不填入 connection。

`data` 是独立 SVC 账户身份，不能使用 AUTH、SYS 或 CLIENTS 用户，须列入 Broker 的 auth_users。
仅需 SUB `auriga.net.up.*.*`、`auriga.internal.network.head.*`，PUB `auriga.net.down.*`；
跨账户按对应 subject 单向导入/导出，不授 DSH 内部提示发布权。data 与 auth/ops 必须连接同 Broker 世代，
所有控制/数据连接关闭即通知运行监督器退出，不自动重连。
SVC 账户/data 连接的订阅配额应容纳 maxConnections+1（每个认证 ingress 一条 up，另加内部 HEAD）。

任务要求的精确上行为 `auriga.net.up.<shard>.<ingress>`。32 桶 shard 为
`abcdefghijklmnopqrstuvwxyz234567[SHA256(ASCII(ingress))[0] & 31]` 的单个字符；仅为路由提示。
准备端/客户端要一起采用新精确 subject，旧无 shard 的 subject 不接受。down/reply 命名不变。
网关不使用客户端 Reply/header 选择返回目标，所有结果始终写 `auriga.net.down.<认证 ingress>`。

READY/FETCH/RECEIPT 经生成的 NetworkMailboxService 转发完整原始 JSON（包括 continuation、
收执证据和未来扩展），由 collab 负责完整 schema/业务校验。成功签名帧原字节转发，不走内存骨架编码器。
只有 collab 返回的 READY 结果建立 incarnation/epoch/nonce，不能由 FETCH 自报建立。
内部提示 `auriga.internal.network.head.<ingress>` 的 payload 不参与寻址；网关调用 Head 得到权威签名提示。
没有私造客户端 HEAD 请求，也没有用逐连接短 poll 取邮箱；collab 的批量修复扫描/提示合并由后续任务接线。

客户端 READY/FETCH/RECEIPT 按 tenant/realm/node 最多 1 个 RPC 在途（续接 ingress 共享），
每租户最多 16 个、全局 128 个；RPC 时限复用 requestTimeoutMillis（≤250ms）。每节点请求
64/s、burst=8，每租户 256/s、burst=32，限额先在轻量回调检查，不在回调解析 JSON 或签名。
每个已认证 ingress 独立精确订阅，pending ≤8 帧且 ≤128KiB，随连接授权失败/断开回收；
订阅总数受 maxConnections 限制，不随 DSH 内的逻辑会话增长。
拒绝响应另限每节点 1/s、burst=4，每租户最多 2 个、全局 16 个异步签名/发布 worker。
达到在途上限尽力返回可重试 network.overloaded；超请求/错误预算或 Core pending 溢出可丢请求，
调用方沿原幂等键有界重试，单节点灌包不占用其他节点的接收队列或全部租户在途预算。
HEAD 提示不占客户端位，按 node 只保留一个最新待处理标记；FETCH/RECEIPT 完成后补跑一次权威 Head。
HEAD 自有全局 16/每租户 2 的 RPC 预算，未 READY 或被吊销时静默丢弃，本地失败不向客户端发 ERROR。
大响应发布只持该 ingress 的发布/吊销互斥锁，不在 socket 写入期间持全局身份锁；同 ingress 已在途
写入可能先完成，全局身份状态访问不再等这次 socket 写入；共用数据连接的底层 socket 背压仍存在。
无法连接/超时/未知服务错误映射 network.overloaded；鉴权错误映射 network.unauthorized，
已知公开 mailbox 错误只允许精确白名单，后台消息/堆栈/凭据不转发。

本地 ERROR 用独立 `protocol_response` Ed25519 PKCS8 PEM 私钥真实签名，必须先把对应公钥/KID/版本/
有效期配置进各 realm 的受信根签名 keyset；不能使用业务签名或 callout seed。ERROR 寿命最多 60 秒且
不超过密钥与连接期限，配置有效期届满即停止签名并告警，换钥需滚动重启。签名仅覆盖固定 ASCII ERROR
对象（其 JSON sorted-key 编码等同 JCS），不尝试重签 collab 业务帧。客户端仍独立验完整下行签名。
首次 READY 尚未拿到权威 incarnation 时，本地失败 ERROR 使用 epoch=0、incarnation=unbound 和本次
READY nonce；该拒绝只作诊断，不能建立身份或推动游标。若客户端只接受已登记 incarnation 的 ERROR，
应继续按 READY 超时有界重试；需要协同约定该初始拒绝的展示方式，不放宽成功/正文帧的受众检查。
### 来源控制搬运

source_register/source_get/source_revoke 复用同一精确 up/down subject，分别调用正式生成的
SourceRegister/SourceGet/SourceRevoke RPC；connection 全七字段只取认证 ingress，委托 tenant 取同一 connection。
网关按正式 1.x 消费边界严格检查原 UTF-8 JSON、已知字段、关联和签名结构，保留未知可选字段；
完整请求和响应均原字节转发，不重编码、不重签业务结果。boot 必须对应该 ingress 最近成功的 READY，
RPC 前后都检查当前连接、epoch、nonce 和 boot；断线取消在途来源请求，旧连接回包不发给新连接。

每个 tenant/realm/node 的 Register/Get 共用一个普通来源槽，Revoke 独占一个保留槽，均无等待队列。
普通来源与原 mailbox 共用全局 128/每租户 16 的普通池及原请求限速；撤销独立保留全局 16/每租户 2，
并独立限速（每 node 64/s、burst=8；每 tenant 256/s、burst=32），普通池或普通限速耗尽不阻塞撤销。
加上原 HEAD 16 和 ERROR 16，所有池总硬上限为 176；请求拒绝仍走原有界错误预算，超预算可静默丢弃。
NATS 每 ingress 的 8 帧/128KiB pending 上限不变；回调只作有界 type 提取，完整来源校验在 worker 执行。

三来源请求从受理起最多 30 秒，专用 mailbox 拨号器仅对这三种生成方法解除原公共 10 秒拦截器截短，
仍使用相同配置的 mTLS/轮换服务 JWT；其余方法的 requestTimeoutMillis 和 busy 语义不变。
超时/取消释放本次槽并返回可重试诊断，不能据此声明 mutation 未提交；插件保留原 op/outbox 对账。
迟到响应不会发布或释放后来请求的槽。未实现 RPC、坏响应、错受众及超限均失败关闭。

来源响应校验只检查关联和结构，权威验签仍由插件对完整原对象执行，不增加来源信任根。
历史 Register 的 active/revision=1 只是创建回执；Get 的 expired/revoked/stopped 正常搬运且必须省略
valid_until，active 则必须有时间字段；任何状态均不授 SEND，也不绕过插件当前观察、验签和最终 CAS。
来源集成测试使用明确的 collab/IAM 协议夹具与真实 gRPC/NATS WSS，不代表真实 IAM、PG 或双 DSH 整链验收。
