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
    "allowedHosts": [{"host": "models.example.com", "allowPlaintext": false}],
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
归一化为公开别名，接受与请求名不同的上游快照名；逐层仅保留 D2 契约白名单字段，
丢弃 annotations、obfuscation、reasoning_content 等供应商扩展，由 Core 校验保留的字段。4xx/重定向映射
`ai.upstream_rejected`，5xx 映射 `ai.upstream_unavailable`，408/504 映射
`ai.upstream_timeout`；上游错误正文、Location、地址和底层错误均不进入公开错误或日志。

兼容 MiniMax 风格 SSE：delta 缺少 `finish_reason` 时补 `null`；只含推理等扩展的 delta
按白名单投影，投影后为空仍输出空 delta。普通 chunk 中缺少 prompt/completion tokens 的占位 usage，
以及尚未结束时两者均为 0 的 usage 均丢弃；非零 usage 仍走既有严格校验。合法的零用量 finish chunk
最多暂存一个事件：若随后有最终 usage 则替换占位值，否则在 `[DONE]` 时原样输出。
已有 finish_reason 后、`[DONE]` 前，最多接受一个 `object: chat.completion`、恰好单 choice 的汇总事件，
仅投影为 D2 usage-only chunk，不重复转发 message。汇总 usage 必须是非负安全整数且
prompt+completion=total，仍受原请求及可信计量策略限制；completion_tokens 已含 reasoning_tokens，
不另加计。正文用摘要与累计流比较，不一致时以累计流为准，仅记无正文、无摘要值的内部诊断。
提前/重复汇总、汇总后继续 chunk、非法计量、多 choice、未完成工具调用、字节/事件超限均失败关闭。

默认只允许 HTTPS（系统证书验证），拒绝 loopback、私网、链路本地、元数据、共享地址空间、
组播／未指定地址及可嵌入 IPv4 的转换／隧道地址。解析所有结果均须通过；实际拨号重新解析并
校验，再依次尝试已校验的数值 IP 建立连接，复核实际远端与选定 IP 一致及地址政策，防止
DNS rebinding。仅建连失败时回退其他 IP，不重试或重放 HTTP 请求，合用一次 connect timeout。
`allowedHosts[].host` 是部署方的**精确主机名或无方括号 IP**，大小写及末尾一点规范化；
不支持通配、子域继承、端口或 URL。空清单可以访问公网 HTTPS。
清单代表部署方对该主机的信任：解析和实际拨号 IP 除回环（127/8、::1）及已知云元数据端点
（169.254.169.254、fd00:ec2::254、168.63.129.16、100.100.100.200）外均放行；IPv4-mapped
地址先还原后检查。私网、CGNAT、ULA、198.18/15 代理 fake-IP、文档段和其他链路本地地址均可用。
无效地址及带 zone 的地址仍拒绝。非清单主机保持上述严格默认过滤，清单不继承到子域。
HTTP 须每个主机额外显式设 `allowPlaintext: true`，默认 false。
清单允许该受信主机访问所有端口，只能由受保护的部署配置设置，租户、页面和设备请求均不能修改；
只列入完全可信、部署方控制的模型主机，保护配置与 DNS。
HTTP 会明文传递上游凭据和提示词，应优先部署 HTTPS。

当前管理面 `ValidateEndpointDNS` 和 `RelayReader` 的 provider 校验仍独立拒绝私网数值 IP、
`.local`／`.internal`／localhost；管理面也拒绝注册解析到私网的域名。
本任务仅放开 Transport，**不会放开这些更早的检查**。现有公网形态域名记录在解析转入内网后，
可由显式清单允许出站；新增内网 provider 的正式管理流程仍须主控另接同一部署清单。
不要绕过管理面或直接写数据库作为部署步骤。

Core 仍负责首字节、idle、总期限与取消，连接及 TLS 握手使用 provider 的 connect timeout。
设 N 为所有已批准模型 maxOutputTokens 的最大值（至多 32768，超过则拒绝启动）；共享 Core 与
Transport/Handler 的 MaxBytes=min(1 MiB+N*1024,32 MiB)、MaxEvents=min(4*N+64,131136)，留出帧、
usage 和进度注释余量。单事件沿既有协议上限，注释与 data 分帧，不增大 data 事件，每帧最多
发送一次进度注释。SSE 停止证明的结构校验逐帧执行，通过无缓冲 pipe 保持背压；不缓存或在 DONE
时重放整条 SSE 响应，避免收尾 CPU 工作占满 Core 的 idle 期限。取消或 Close 会结束校验，不自动算作停止证明。
synthetic 仍为 1 MiB/32 事件。以下三类证据可关闭现场 Stopped：未获得
可写 HTTP 连接（含 DNS、SSRF、拨号、TLS 失败）；上游错误响应在同一字节上限内完整读完并关闭；
经协议状态机验证的终结 JSON/SSE。断流、超限、取消和 Close 单独不是停止证明，unknown 可能持槽并
使停机非零。正常收完的 4xx/5xx 和未提交失败不再占槽；账务仍沿 Core 的 unknown 路径保守留置，
不从停止证明推导免费或退款。

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

## Beta edge 接线

edge 配置的 `upstreams.ai-device`（环境变量 `AURIGA_EDGE_UPSTREAM_AI_DEVICE`）指向
独立 device runtime 的固定 origin；同机可用 `http://127.0.0.1:<device-port>`，
跨主机仅接受 HTTPS origin。它与 Team 的 `upstreams.ai-gateway`、管理面的
`upstreams.ai-admin` 分开，不扩展 Team 白名单。需要已校验的 DeploymentAccess。
只在门户入口精确代理 `GET /ai/v1/models`、`POST /ai/v1/chat/completions` 和
`GET /api/ai/v1/me/usage`；不补斜杠，不接受编码路径、浏览器 Cookie/Origin 或实例转发标记。
设备 AI Bearer 原样送 runtime 逐次核验，edge 不以管理/门户会话兑换它。

资源模块另用 `upstreams.resource`（`AURIGA_EDGE_UPSTREAM_RESOURCE`）指向 platform，
共用 `iamHop` 的受保护传输。`/api/resource/v1/**` 沿现有浏览器会话、完整 Origin 和
域名/IP CSRF 规则代理；Resource 服务继续检查 read/upload、租户可见性和近期 step-up，
ETag、If-Match、Idempotency-Key 不改写。缺上游配置返回明确错误，不落入 SPA。

管理与 device 进程必须使用同一套隔离 Beta 模型数据、密钥及币种，配置真实 IAM/Org RPC。
真人验收顺序为：管理台登记 provider/凭据/模型及成员 grant → 普通成员插件读取目录 →
撤销 grant 后重新读取目录应消失。资源页新增、读取、条件修改须落到真实 Resource 服务。
生成另验：仅在部署方批准 upstream policy、可信 usage 和插件精确 tokenizer 都具备时调用，
核对真实账本；synthetic/测试 IAM 夹具不构成此证明。配置独立入口不代表这些真人验收已完成。
