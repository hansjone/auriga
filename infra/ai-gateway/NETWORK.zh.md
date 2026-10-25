# N1 网关联网鉴权运行方式

启动：`auriga-ai-gateway --network-config ./network.json`。`--version` 不初始化服务；
未指定运行配置会失败。Team MCP 继续用 `--team-config`，两个入口各用一个进程。
本入口只启动 callout 和已知连接吊销检查，不启动 Broker，也不代替应用 READY/epoch/逐操作授权。

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

每轮对账只取一次完整、按客户端账户过滤的 CONNZ，并调用一次批量 IAM active；
按 tenant/realm/node 去重，响应缺项、重复或包含额外身份均视为查询失败。批量 RPC 继续携带
原服务 JWT 和一个业务租户的委托元数据，精确主体检查不变；其中的受信身份列表只能来自
此前兑换成功的网关记录。IAM 将列表按租户分组，每组进入独立 FORCE RLS 作用域，用集合 SQL
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
同 Broker 世代 CID 不复用，因此无需为每次 KICK 再发 CONNZ；截断/错误快照不据此删记录或踢人。
新连接仍有一个 auth timeout 的建立宽限期；正常断连由快照回收。
5,000 连接（50 租户）的规模单测模拟一次 IAM 100ms、每个 KICK 10ms，核对一次 CONNZ、
一次批量 IAM、全 5,000 次 KICK 成功并回收绑定，整轮小于 4 秒，加最大轮询抖动 600ms 仍在 5 秒内。
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
