# 节点代理（agent）

`infra/agent` 是装在运行 DSH 实例的主机上的 Go 程序。进程名 `auriga-agent`。

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
| `drivers` | `AURIGA_AGENT_DRIVERS` | 逗号分隔；`docker`（默认，含 Podman API）或 `fake`（测试） |
| `maxConcurrentPrepare` | `AURIGA_AGENT_MAX_CONCURRENT_PREPARE` | 同时准备/启动上限，默认 4 |
| `controlEndpoint` | `AURIGA_AGENT_CONTROL_ENDPOINT` | 覆盖入网返回的 NodeControl 地址 |
| `agentVersion` | `AURIGA_AGENT_VERSION` | Hello / Join 上报的版本 |
| `allowInsecure` | `AURIGA_AGENT_ALLOW_INSECURE` | 允许明文端点（仅本地开发） |
| （策略文件） | `AURIGA_AGENT_POLICY_FILE` | 默认 `<dataRoot>/agent-policy.yaml`；镜像/制品白名单与资源上限 |

配置项前缀约定：`auriga.agent.*` / `AURIGA_AGENT_*`。

未设置 `drivers` / `AURIGA_AGENT_DRIVERS` 且未提供 JSON 配置时，`run` 使用 **Docker** 驱动。本地/单测可设 `AURIGA_AGENT_DRIVERS=fake`。

Docker Engine 地址遵循 `DOCKER_HOST`（`unix:///var/run/docker.sock`、`npipe:////./pipe/docker_engine`、`tcp://127.0.0.1:2375`）。节点代理通过 Engine HTTP API 操作，不调用 `docker` CLI。

本地策略示例（`agent-policy.yaml`）：

```yaml
allowedArtifactURLPrefixes:
  - https://artifacts.example.com/
allowedImageRepos:
  - registry.example.com/auriga/
maxCPUMillis: 8000
maxMemoryMB: 16384
maxPIDs: 2048
maxArtifactBytes: 2147483648
```

## 目录布局（`<dataRoot>`）

```
<dataRoot>/
├─ agent/          state.db、证书、sealing.key、instance-uids.json、agent-policy.yaml（可选）
├─ artifacts/
│  └─ sha256/<hex> 内容寻址制品（只读）；refs.json 按实例 holder 引用
├─ instances/<id>/   # id: ^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$
│  ├─ home/        绑进容器 /home/dsh（rw）
│  ├─ meta/        绑进容器 /run/auriga（ro）；令牌 0400
│  └─ trash/
└─ backups/
```

各凭据文件先写临时文件并同步，再原子 rename；顺序为 `node.key`、`sealing.key`、`ca.crt`、`node.crt`，防止私钥写入失败时发布新证书并搭配旧私钥。这不是多文件事务：强制重新入网中断仍可能留下新私钥配旧证书，需要重新入网恢复。Unix `0600`；Windows 从当前进程令牌取得用户 SID，设置受保护的 DACL，仅授予 SYSTEM、Administrators、当前用户完整权限，不保留继承项。

`instance.token` / `ai-gateway.token` 仅写在 `meta/`，权限收紧（0400 / DACL），内容不进日志；环境变量里是容器路径 `/run/auriga/…`。

运行时由单个续期协程立即检查一次，此后默认每小时检查；重连不另行发起续期。失败以连接配置的最小退避（默认 1 秒）起步，指数增长到 30 分钟并加抖动，达到上限后仍持续退避；成功后恢复检查间隔。续期成功写盘，下次建立会话时重新加载新证书。退出前等待续期协程结束，再关闭状态存储。

## A2 范围

- **Docker / Podman 驱动**：Engine API；持久 uid（`instance-uids.json`）、只读根、`cap-drop ALL`、`no-new-privileges`、资源取 min(规格, 策略)；Mounts（home + meta:ro）；拉镜像解析错误流并核对 digest
- **制品**：https 白名单、大小上限、连接/总超时分开；篡改缓存会重下；按实例 Release；7 天 GC
- **配置渲染**：`yaml.v3` 结构化生成；拒绝换行/`!!`；禁止覆盖 `AURIGA_*`/`DSH_HOME`；HPKE 解封
- **策略**：`internal/policy` 镜像/制品白名单与资源上限（本地最后一道关）
- **目录**：§8.1、严格实例 id、Lchown、软配额
- 假驱动保留给测试；真 Docker：`go test -tags docker ./internal/runtime/`

## 测试

```powershell
Set-Location infra\agent
go vet ./...
go test -count=1 -timeout=10m ./...
go test -race ./...   # 需要 cgo 和可用的 C 编译器
go test -tags docker -count=1 ./internal/runtime/   # 需本机 Docker，默认 CI 不跑
```

集成测试用 `internal/testutil` 假 workspace（随机端口、`grpc://` + `AllowInsecure`）。CA 指纹 pin 用 `net.Pipe` 测（避免本机杀毒软件劫持 localhost TLS）。

Docker 单元测试用 `httptest` 假 Engine，断言创建容器请求含加固参数。制品测试覆盖哈希不符与下载中断。

续期测试将检查间隔缩短至 10 毫秒，覆盖存活会话中的重复换证、重连凭据加载、失败退避重试，以及缺失/畸形证书、公钥不符、节点身份变化、错误 CA、过期证书均不替换原证书。另覆盖 CA 文件篡改、主机名与 IP SAN 不符、重复入网不修改凭据、保存各阶段失败和临时文件清理。Windows 专用测试读回目录与全部凭据文件的 DACL，逐项核对 SID、权限及禁止继承，包含覆盖保存场景。
