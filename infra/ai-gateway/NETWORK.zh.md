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

吊销检查最多 16 个并行任务；每次 IAM/系统请求有界，KICK 最多 1–5 次，失败报警且不恢复授权。
检查间隔和单次请求时限均须在 1–1000ms，最多跟踪 10000 条连接；满额在兑换前拒绝。
新连接保留一个 auth timeout 的建立宽限期；断连经 CONNZ 回收。KICK 前再次核对
server ID/CID/account/ingress，不按客户端自报 CID 踢连接。
单连接小负载检查界约为宽限期 + 扫描间隔 + IAM/CONNZ/KICK 请求时限；
大量连接还须计入最多 16 并发的整轮扫描时间，未做容量/SLO 压测。
IAM 故障也失败关闭；KICK 控制面失效时 JWT（最长一小时且不超过 IAM 授权）到期兜底。
当前已知连接注册表属于网关进程；进程崩溃或重启不会从 IAM 历史表重建在线连接，
旧连接依赖原 JWT 到期及应用层逐操作授权。控制连接不自动重连，断开即退出并要求重新启动，
防止把新 Broker 世代当作旧 server ID；多 Broker/跨进程恢复仍需后续接线。

Go 通过 `infra/gen/auriga/iam/v1` 的生成客户端与消息类型调用 IAM，测试服务也使用生成的
服务注册函数。在 `infra` 目录执行 `go run generate.go` 同步生成物；Java 类型继续由
原 Maven contracts 模块生成。
