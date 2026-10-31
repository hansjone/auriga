# 公网网络预算与 join 定位

插件进程与 NAS `stack.env` 使用 `AURIGA_NETWORK_OPERATION_TIMEOUT_MS`（默认 30000，范围 10000–120000 毫秒）。NAS 的 `render-collab.sh` 从已验证的原材料生成独立 `config/network-policy/` 投影，原 sealed、receipt、身份与签名根保持原样。修改后按公开 `start.sh` 重建服务；两端应采用一致预算。

- 插件 HTTP、WSS、READY、join/local_register、FETCH、RECEIPT 与 source-control 的外层操作：30 秒。单次 join 的重试共用原截止，退避不会刷新预算。
- network→collab 业务 RPC：操作预算的 2/3，默认 20 秒；gRPC 适配器继承父截止，未提供父截止时才使用 10 秒后备值。
- IAM 对账请求：预算的 1/6，默认 5 秒；每秒对账；权威临时不可用的宽限为预算的 1/2，默认 15 秒。明确 `active=false` 立即撤销，新票据在不可用期间始终失败封闭。
- Broker TLS/授权：预算的 1/3，默认 10 秒；票据兑换保留 2 秒余量，默认 8 秒。内部 Broker 客户端建连上限 10 秒。edge 的透明 WSS 拨号与 TLS 各 10 秒，无响应头截止；空闲连接池 90 秒不限制已升级的 WebSocket。
- edge 的 keys HTTP 总截止 5 秒、内部 TLS 2 秒，属于内网公钥读取；不承载 join 数据面。签名租期、票据有效期、保活间隔和重试间隔不作为 RPC 截止放宽。

JWT 校验：插件 `AURIGA_JWT_CLOCK_SKEW_SECONDS` 默认 60 秒、可设 0–120 秒，适用于 `iat`/`nbf`，插件 `exp` 严格拒绝过期。Java IAM/common 的 `auriga.jwt.clock-skew` 默认 `60s`，可设 0–120 秒，保留 Spring 时间校验并检查 `iat`。network/模型网关委托 IAM 校验设备凭据，不另设不同的设备时钟规则。

短暂断线期间，同一已认证 Host 的 join/send 工具 schema 保持注册，返回 `retryable: true`；恢复连接替换调用委托。退出登录、Host 卸载或生命周期关闭会撤销工具；旧连接的调用结果不能越过注销继续返回成功。

每个 join 生成独立 trace；其 local_register 请求 ID 包含该 trace 与独立 UUID，重试保持原请求 ID。插件、network 和 collab 使用同一 trace，认证不信任此字段。插件结构化日志记录 `stage`、累计 `elapsed_ms` 与失败点，不写输入正文。日志位于插件日志目录 `Auriga/logs/auriga-dsh-*.jsonl`，本地文件需要运维采集器导入 Loki；NAS Alloy 已采集 network/collab 容器日志。查询 `{job="auriga-nas"} |= "<trace_id>"`。

edge 对 WSS 是透明字节隧道，不解析每条 join；其连接日志只能按连接和时间窗口关联，不能冒充每个 join 的应用 span。跨桌面文件采集与现场三轮真实往返必须另外验收，单元测试不代表现场 PASS。
