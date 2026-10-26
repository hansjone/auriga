# D7 device 回环 runtime

`auriga-ai-gateway --device-config /protected/device.json` 启动独立进程，不能同时指定
`--admin-config`、`--team-config` 或 `--network-config`。支持内存合成模式与显式启用的
OpenAI 兼容上游模式；本地假上游验证不代表真实 IAM 签票或生产模型联调验收。

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
**必须新建隔离的 D7/Beta 数据库；synthetic 会产生假账，upstream 会实际调用并可能收费，禁止混用生产账库。** 名称检查不能证明部署方
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

## upstream 模式

默认不启用真实出站。启用时将 `d7SyntheticOnly` 设为 `false`、删除 `syntheticTenant`，
并添加以下受保护配置；两种模式同时配置或缺少模式均拒绝启动。其余监听、RPC、密钥、
数据库和恢复配置沿用上例，模型、provider、凭据和 member grant 由既有管理流程准备。

```json
{
  "upstream": {
    "allowedHosts": ["models.example.com"],
    "models": [{
      "tenant": "01900000-0000-7000-8000-000000000001",
      "model": "beta-model",
      "providerId": "01900000-0000-7000-8000-000000000004",
      "upstreamModel": "provider-model",
      "contextWindow": 4096,
      "maxOutputTokens": 1024,
      "contextLimitEnforced": true,
      "trustUsage": true
    }]
  }
}
```

`models` 必须逐项显式批准 tenant/model/provider/upstreamModel，容量须与该次捕获的数据库
模型 revision 一致；未列模型拒绝调用。这里没有通用 tokenizer，也不按字符猜 token。
`contextLimitEnforced=true` 是部署方确认：该 provider 对完整模板、全部历史及输入，强制
**输入 tokens + 请求的 max_tokens ≤ contextWindow**，而非只限制最终生成量。
准入因此保守预留整个 `contextWindow - max_tokens` 输入空间，会比真实输入占用更多预算；
上游不具备此保证时不得启用本 Policy。`trustUsage=true` 表示部署方核实该 provider 的最终
usage 是可用于账务的权威计量；缺失或不可信 usage 不结成功账，保持 unknown。
模型／provider 更换时必须重新确认这两项保证。当前仅开放文本历史和 max_tokens/stream，
tools、temperature、top_p、stop 继续失败关闭。

Transport 仅向受信 target 的 `{baseURL}/chat/completions` POST，模型名使用 upstreamModel，
借用解密凭据设置 Bearer；不转发设备票据、Cookie 或客户端头。不重试、不 failover、不跟随
任何重定向、不读取代理环境变量，不保留 Authorization。stream 布尔值保持原样；SSE 增加
`stream_options.include_usage=true`，provider 必须支持该选项和最终 usage。JSON/SSE 的模型名
归一化为公开别名，只移除已知 OpenAI 元数据，其余响应仍由 Core 严格校验。4xx/重定向映射
`ai.upstream_rejected`，5xx 映射 `ai.upstream_unavailable`，408/504 映射
`ai.upstream_timeout`；上游错误正文、Location、地址和底层错误均不进入公开错误或日志。

默认只允许 HTTPS（系统证书验证），拒绝 loopback、私网、链路本地、元数据、共享地址空间、
组播／未指定地址及可嵌入 IPv4 的转换／隧道地址。解析所有结果均须通过；实际拨号重新解析并
校验，再固定单个数值 IP 拨号并检查连接远端，防止 DNS rebinding；不尝试第二个 IP。
`allowedHosts` 是部署方的**精确主机名或无方括号 IP 清单**，大小写及末尾一点规范化；
不支持通配、子域继承、端口或 URL。空清单可以访问公网 HTTPS。
清单允许指定主机访问内网并使用 HTTP，方便团队内网模型；也会允许该主机解析到其他内网／
元数据地址及其所有端口。仅列入完全可信、部署方控制的模型主机，保护配置与 DNS；
HTTP 会明文传递上游凭据和提示词，应优先部署 HTTPS。不得由设备请求控制此清单。

当前管理面 `ValidateEndpointDNS` 和 `RelayReader` 的 provider 校验仍独立拒绝私网数值 IP、
`.local`／`.internal`／localhost；管理面也拒绝注册解析到私网的域名。
本任务仅放开 Transport，**不会放开这些更早的检查**。现有公网形态域名记录在解析转入内网后，
可由显式清单允许出站；新增内网 provider 的正式管理流程仍须主控另接同一部署清单。
不要绕过管理面或直接写数据库作为部署步骤。

Core 仍负责首字节、idle、总期限与取消，连接及 TLS 握手使用 provider 的 connect timeout。
原始及归一化响应均有 1 MiB 上限，单事件沿既有协议上限；Core 对 upstream 最多 4096 个事件，
synthetic 仍为 32。仅经协议状态机验证的终结 JSON 或完整终结 SSE 可关闭现场 Stopped；
断流、超限、取消、Close 和 HTTP 错误都不证明远端模型停止，因此 unknown 可能持续占槽，
停机可能非零退出。不能将断开 HTTP 当退款依据。

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
本项不承诺跨进程、跨重启责任全覆盖，生产恢复仍需要后续设计。

## 验证

设置 `AURIGA_AI_TEST_PG_DSN` 指向隔离的测试 PG 后执行 `go test -count=1 ./...`。
root fixture 会在该测试 PG 创建专用 `auriga_d7` 库和随机非所有者角色，使用两个真实回环 gRPC listener
以及随机回环 HTTP。假 JWT 由 fixture 识别，没有真实 IAM 签名。测试保留既有 #204 跨二进制 ACL 锁。
同组恢复测试还覆盖真实账本 CAS、竞争恢复、旧 revision、COMMIT 回包不明、缺行与发送值攻击。
upstream 测试仅访问随机端口 httptest：覆盖 JSON、逐字节 SSE、错误脱敏、取消、中断、
响应限额、SSRF 清单、重定向拒绝及模拟 DNS rebinding，不访问真实模型或产生费用。
