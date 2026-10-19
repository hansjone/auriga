# 节点代理（agent）

`infra/agent` 是装在运行 DSH 实例的主机上的 Go 程序（任务 A1 骨架）。进程名 `auriga-agent`。

详细设计见 `docs/architecture/node-agent.zh.md`；契约见 `contracts/proto/auriga/node/v1/`。

## 运行

```powershell
. D:\project\chatgpt\.agents\auriga-env.ps1   # 若在派活环境
Set-Location infra\agent
go run . --version
go run ./cmd/agent --version
go run . join --server https://platform.example.com:7443 --token-file .\token.txt --ca-sha256 <指纹> --data-root D:\tmp\auriga-data
# 令牌也可用环境变量 AURIGA_AGENT_JOIN_TOKEN；勿把令牌写进进程参数（可被本机他用户看到）
go run . run --data-root D:\tmp\auriga-data
```

入网令牌与 CA 指纹由管理台「节点」页生成。指纹是平台 CA 证书 SubjectPublicKeyInfo（DER）的 SHA-256，小写十六进制。Join 要求 `https://` / `grpcs://`；明文 `grpc://` / `http://` 仅在 `--allow-insecure` 或 `AURIGA_AGENT_ALLOW_INSECURE=true` 时允许（本地开发）。服务端下发的 control endpoint 同样禁止降级为明文。

日志为 JSON slog，字段含 `service=agent`。默认**不监听公网**（只主动连平台）。

## 配置

JSON 文件 + 环境变量覆盖。公开仓示例用 `example.com` / `192.0.2.x`。

| 字段 | 环境变量 | 说明 |
|---|---|---|
| `server` | `AURIGA_AGENT_SERVER` | 入网用的平台 URL（生产须 https/grpcs） |
| `dataRoot` | `AURIGA_AGENT_DATA_ROOT` | 数据根；Linux 默认 `/var/lib/auriga`，Windows 默认 `%ProgramData%\auriga` |
| `drivers` | `AURIGA_AGENT_DRIVERS` | 逗号分隔；A1 运行时用假驱动 `fake` |
| `maxConcurrentPrepare` | `AURIGA_AGENT_MAX_CONCURRENT_PREPARE` | 同时准备/启动上限，默认 4 |
| `controlEndpoint` | `AURIGA_AGENT_CONTROL_ENDPOINT` | 覆盖入网返回的 NodeControl 地址 |
| `agentVersion` | `AURIGA_AGENT_VERSION` | Hello / Join 上报的版本 |
| `allowInsecure` | `AURIGA_AGENT_ALLOW_INSECURE` | 允许明文端点（仅本地开发） |
| （run 标志） | `AURIGA_AGENT_INGRESS_LISTEN` | 多机 ingress TCP 地址，如 `192.0.2.20:7443` |
| （run 标志） | `AURIGA_AGENT_INGRESS_LOCAL` | 单机 socket/管道路径；默认 Unix `<agentDir>/ingress.sock`、Windows `auriga-agent-ingress` |
| — | `AURIGA_AGENT_FILE_TICKET_KEYS` | 文件票据公钥 JWKS；默认 `<agentDir>/file-ticket-keys.json` |
| — | `AURIGA_AGENT_IDENTITY_KEYS` | 身份头验签公钥 JWKS；默认 `<agentDir>/identity-keys.json` |

配置项前缀约定：`auriga.agent.*` / `AURIGA_AGENT_*`。

## 目录布局（`<dataRoot>`）

```
<dataRoot>/
├─ agent/          state.db、node.key、node.crt、ca.crt、sealing.key
├─ artifacts/
├─ instances/<id>/
└─ backups/
```

各凭据文件先写临时文件并同步，再原子 rename；顺序为 `node.key`、`sealing.key`、`ca.crt`、`node.crt`，防止私钥写入失败时发布新证书并搭配旧私钥。这不是多文件事务：强制重新入网中断仍可能留下新私钥配旧证书，需要重新入网恢复。Unix `0600`；Windows 从当前进程令牌取得用户 SID，设置受保护的 DACL，仅授予 SYSTEM、Administrators、当前用户完整权限，不保留继承项。

运行时由单个续期协程立即检查一次，此后默认每小时检查；重连不另行发起续期。失败以连接配置的最小退避（默认 1 秒）起步，指数增长到 30 分钟并加抖动，达到上限后仍持续退避；成功后恢复检查间隔。续期成功写盘，下次建立会话时重新加载新证书。退出前等待续期协程结束，再关闭状态存储。

## A1 范围

- 入网（`join`）、运行中按有效期（剩 1/3 或默认 7 天内）定时续期，失败记日志并退避重试
- mTLS 会话：Hello、Inventory、心跳、断线指数退避（带抖动）重连、Welcome 超时、重连后全量对账
- 本地状态（bbolt）：期望规格 + 实际状态，重启可恢复；每次加载核对 CA 指纹
- 对账状态机 + **假驱动**（内存模拟 Ensure/Start/Stop；真 Docker 留给 A2）

## A3 实例入口与观测

`run` 在入网凭据可用时启动 ingress + observer：

| 项 | 说明 |
|---|---|
| 入口 | 只接受 URI SAN 为 `auriga://edge` 的客户端证书；多机 `--ingress-listen` / `AURIGA_AGENT_INGRESS_LISTEN`；单机 Unix socket 或 Windows 命名管道（`AURIGA_AGENT_INGRESS_LOCAL`） |
| `/i/<id>/…` | 反代到实例；验签后转发 `Auriga-Identity`，伪造头剥掉 |
| `/files/<id>/…` | workspace 签发的 Ed25519 文件票据（`file-ticket-keys.json` / `AURIGA_AGENT_FILE_TICKET_KEYS`）；路径防逃逸；zip UTF-8 标志 |
| `/logs/<id>` | 实例日志尾部；滚动默认 50 MB |
| `/metrics` | Prometheus 文本指标 |
| 观测 | 健康探活、5 分钟 CPU 活动、ops-cron/schedule 下次唤醒（含时区）、Linux cgroup；上报经 `observer.Reporter`（当前记日志，接 Session 待主控接线） |

实例注册表与观测目标列表目前为空，需 runtime/reconciler 提供上游地址与根目录后由主控接线。

## 测试

```powershell
Set-Location infra\agent
go vet ./...
go test -count=1 -timeout=10m ./...
go test -race ./...   # 需要 cgo 和可用的 C 编译器
```

集成测试用 `internal/testutil` 假 workspace（随机端口、`grpc://` + `AllowInsecure`）。CA 指纹 pin 用 `net.Pipe` 测（避免本机杀毒软件劫持 localhost TLS）。

续期测试将检查间隔缩短至 10 毫秒，覆盖存活会话中的重复换证、重连凭据加载、失败退避重试，以及缺失/畸形证书、公钥不符、节点身份变化、错误 CA、过期证书均不替换原证书。另覆盖 CA 文件篡改、主机名与 IP SAN 不符、重复入网不修改凭据、保存各阶段失败和临时文件清理。Windows 专用测试读回目录与全部凭据文件的 DACL，逐项核对 SID、权限及禁止继承，包含覆盖保存场景。
