# 公网网络预算与 join 定位

插件进程与 NAS `stack.env` 使用 `AURIGA_NETWORK_OPERATION_TIMEOUT_MS`（默认 30000，范围 10000–120000 毫秒）。NAS 的 `render-collab.sh` 从已验证的原材料生成独立 `config/network-policy/` 投影，原 sealed、receipt、身份与签名根保持原样。修改后按公开 `start.sh` 重建服务；两端应采用一致预算。

- 插件 HTTP、WSS、READY、join/local_register、FETCH、RECEIPT 与 source-control 的外层操作：30 秒。单次 join 的重试共用原截止，退避不会刷新预算。
- network→collab 业务 RPC：操作预算的 2/3，默认 20 秒；gRPC 适配器继承父截止，未提供父截止时才使用 10 秒后备值。
- IAM 对账请求：预算的 1/6，默认 5 秒；每秒对账；权威临时不可用的宽限为预算的 1/2，默认 15 秒。明确 `active=false` 立即撤销，新票据在不可用期间始终失败封闭。
- Broker TLS/授权：预算的 1/3，默认 10 秒；票据兑换保留 2 秒余量，默认 8 秒。内部 Broker 客户端建连上限 10 秒。edge 的透明 WSS 拨号与 TLS 各 10 秒，无响应头截止；空闲连接池 90 秒不限制已升级的 WebSocket。
- network 的 HTTPS 提交入口读头 5 秒、读写各 30 秒、空闲 60 秒，均由同一预算派生，覆盖内部 20 秒 RPC。
- edge 的 keys HTTP 总截止 5 秒、内部 TLS 2 秒，属于内网公钥读取；不承载 join 数据面。签名租期、票据有效期、保活间隔和重试间隔不作为 RPC 截止放宽。
- 本地同意回调 5 秒、动作 10 秒、统一应答窗口 10.5 秒及原生提示 30 秒属于本地用户交互边界，保持原值；e2e HTTP 外层等待为 45 秒，原生进程版本查询为 15 秒，不属于平台网络调用截止。

JWT 校验：插件 `AURIGA_JWT_CLOCK_SKEW_SECONDS` 默认 60 秒、可设 0–120 秒，适用于 `iat`/`nbf`，插件 `exp` 严格拒绝过期。Java IAM/common 的 `auriga.jwt.clock-skew` 默认 `60s`，可设 0–120 秒，保留 Spring 时间校验并检查 `iat`。network/模型网关委托 IAM 校验设备凭据，不另设不同的设备时钟规则。

短暂断线期间，同一已认证 Host 的 join/send 工具 schema 保持注册，返回 `retryable: true`；恢复连接替换调用委托。退出登录、Host 卸载或生命周期关闭会撤销工具；旧连接的调用结果不能越过注销继续返回成功。

每个 join 生成独立 trace；其 local_register 请求 ID 包含该 trace 与独立 UUID，重试保持原请求 ID。插件、network 和 collab 使用同一 trace，认证不信任此字段。插件结构化日志记录 `stage`、累计 `elapsed_ms` 与失败点，不写输入正文。日志位于插件日志目录 `Auriga/logs/auriga-dsh-*.jsonl`，本地文件需要运维采集器导入 Loki；NAS Alloy 已采集 network/collab 容器日志。查询 `{job="auriga-nas"} |= "<trace_id>"`。

edge 对 WSS 是透明字节隧道，不解析每条 join；其连接日志只能按连接和时间窗口关联，不能冒充每个 join 的应用 span。跨桌面文件采集与现场三轮真实往返必须另外验收，单元测试不代表现场 PASS。

## 跨机时钟与来源观察

平台主机须开启 NTP 时间同步。容差不是无限期授权：偏差超过 `AURIGA_JWT_CLOCK_SKEW_SECONDS`（默认 60 秒，范围 0–120 秒）时拒绝。不能只凭 NTP 进程存在就认定已同步，应核实同步状态。

来源观察同时包含平台时间和本机 native witness 时间。客户端只在比较平台 `observed_at`、`not_before` 与本机时钟时使用这项共享容差。平台内部的 `not_before <= observed_at < valid_until <= source.expires_at`、最长观察窗口、签名覆盖的时间范围保持严格。collab 在签发前已经取平台截止、IAM 决策截止、源到期和原始 witness 截止的最小值并向下取整；客户端继续严格复核这个原值上限。

发送前不再把平台 `valid_until` 直接当成本机截止。先计算平台剩余租期 `valid_until - observed_at`，以原本机 `witness.observed_at` 为起点换算，再取原本机 `witness.valid_until` 与源到期换算值的最小值。本机 witness 产生在服务端观察之前，因此网络传输只会缩短可用期。该换算不延长 witness，不修改已签名字段，不用收到响应的时刻刷新授权。通用发送工具拿到的 `expiresAt` 已属于本机时间域，继续严格检查到期。

| 比较位置 | 时间域与处理 |
|---|---|
| source-control/frames：observed_at、not_before 对本机 now | 共享可配置容差；超界拒绝 |
| device-provider：平台票据兑换/连接截止对本机的响应范围检查 | 复用共享容差；只是响应合理性检查，服务端兑换/连接授权最终过期仍严格 |
| source-control/controller：witness 观察、到期、持久化与 native fence | 本机对本机，严格；时钟倒退也拒绝 |
| durable/capability → tools/send：观察授权截止 | 一次保守换算，再按本机截止严格检查 |
| trust/store、trust/verifier：发布/密钥/封装的起始时间对本机 | 复用同一容差；签名、用途、版本、同平台签发窗口严格 |
| 签名/密钥/票据/JWT/消息的最终过期检查 | 保持原有严格过期拒绝，不额外增加存活时间 |
| collab：观察签发、最后事务 fence 与 native witness | 已取原 witness/源/平台上限的最小值；同域窗口和最终过期严格 |
| IAM：源状态、决策、身份/权限/连接到期 | 平台时间域；在线权威与到期严格 |
| network Go：source 帧窗口/签名结构、IAM 兑换/对账 | 同帧窗口或平台权威时间；不从未认证客户端接受时钟覆盖 |

`source_get` 返回 active 也不足以允许发送：必须同时通过原签名/身份/绑定/权限、原 native witness 与保守截止检查。expired/revoked/stopped 均不能续成 active。
