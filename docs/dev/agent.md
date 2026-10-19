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

配置项前缀约定：`auriga.agent.*` / `AURIGA_AGENT_*`。

## 目录布局（`<dataRoot>`）

```
<dataRoot>/
├─ agent/          state.db、node.key、node.crt、ca.crt、sealing.key
├─ artifacts/
├─ instances/<id>/
└─ backups/
```

私钥与 sealing / CA 写入为原子 rename；Unix `0600`，Windows 用 ACL（仅 SYSTEM、Administrators、当前用户）。

## A1 范围

- 入网（`join`）、运行中按有效期（剩 1/3 或默认 7 天内）定时续期，失败记日志并退避重试
- mTLS 会话：Hello、Inventory、心跳、断线指数退避（带抖动）重连、Welcome 超时、重连后全量对账
- 本地状态（bbolt）：期望规格 + 实际状态，重启可恢复；每次加载核对 CA 指纹
- 对账状态机 + **假驱动**（内存模拟 Ensure/Start/Stop；真 Docker 留给 A2）

## 测试

```powershell
Set-Location infra\agent
go test -count=1 -timeout=10m ./...
```

集成测试用 `internal/testutil` 假 workspace（随机端口、`grpc://` + `AllowInsecure`）。CA 指纹 pin 用 `net.Pipe` 测（避免本机杀毒软件劫持 localhost TLS）。
