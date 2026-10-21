# 节点代理（agent）

## 启动就绪与停止期间观测

Docker 的 `ContainerStart` 成功只代表进程启动。驱动在既有 Start 超时上下文内，
每 100 毫秒重试实例实际端点的 `GET /_auriga/health`，成功后才发布端点并返回成功。
必须是 HTTP 200 和健康契约四字段：`status=ok`、`ready=true`、`protocol=1`、非空 `pluginVersion`。
空响应、HTML、错误状态、未就绪、错误协议、额外字段或尾随 JSON 均不视为就绪。
每次请求最长 3 秒（连接及响应头各至多 2 秒），总等待受原 Start 截止和取消限制；
不读取 HTTP 代理环境变量、不跟重定向，响应最多 8 KiB。失败重试先清除旧端点，
失败不能继续通过旧端点报告 ready。周期健康探测复用相同报文校验，不记录响应体。

STOPPED 状态从 ingress 移除，但保留 `ProcessStopped` 和实例 home 路径作为闹钟文件观察目标，
不保留 HTTP 地址。停止期间文件变化仍可观察，已知截止到期后即使文件 mtime 未改也重算下一次计划，
一次性 at 会报告已消费；这仅是计划观测，不执行 DSH 业务任务。重新运行时替换为当前端点。
ABSENT、DELETING、FAILED 彻底移除目标。停止或失败的进程不发送 HTTP 探测、不采集或上报新鲜 CPU；
同时丢弃旧 CPU 基线和活动窗口，防止它们跨重启造成保活。

## Unix ingress 的组共享目录

设置 `AURIGA_AGENT_INGRESS_LOCAL_GID` 后，socket 路径必须为绝对、规范化的
`<parent>/ingress/ingress.sock`，例如 `/var/lib/auriga/ingress/ingress.sock`。
`<parent>` 必须预先存在；从它到根的各级目录都必须为真实目录（禁止符号链接），
由 agent 用户或 root 持有且不可被组或其他用户写入。root 所有的 sticky `/tmp`
只允许作为更上层祖先，不能直接作为 `<parent>`。

agent 只创建并授权专用的 `ingress` 子目录（0750）和 socket（0660），
不会对数据根、`/run`、`/tmp` 等共享目录执行 chown/chmod。
新建目录内留有 agent 所有、0600、单链接的空标记 `.auriga-ingress-v1`，
重启仅接管带此标记且由当前 agent 用户持有、无组写及其他用户权限的目录。
目录内仅允许该标记和 agent 所有的 socket；已有空目录也不会自动接管。
不符合这些条件时启动失败，错误含 `unsafe shared ingress directory`。
私有模式（未设置 gid）仍使用原来的路径和 0700/0600 权限。

新目录在父目录下以临时名 `.auriga-ingress-tmp-*` 创建，显式设为 0700 并写好 0600 标记
（不受 umask 影响）后一次 rename 成 `ingress`，所以崩溃不会留下无标记的 `ingress`；
启动时删除 agent 自己留下的临时目录。首次部署不要预建 `ingress` 目录。

升级（持久卷里已有旧版 agent 建的无标记 `ingress`）：仅当它是真实目录、属于当前 agent 用户、
且里面什么都没有或只有 agent 自己的 socket `ingress.sock` 时，agent 启动时把它整体
rename 为同一父目录下的 `.auriga-ingress-legacy-<纳秒时间戳>`（不修改它的属主和权限，
也不接管），再按上面的方式新建专用目录；备份确认无用后可手工删除。
其他情况（非 agent 所有、含别的文件、符号链接、挂载点导致 rename 失败等）照旧拒绝启动；
此时停 agent，人工检查后把旧目录移走，保留父目录再启动。不要手工伪造标记来接管目录。
edge 必须能逐级穿过父目录；**只给 edge 专属的组**配置该 gid，不要用 0 或宽泛的共享组。

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
| （run 标志） | `AURIGA_AGENT_INGRESS_LISTEN` | 多机 ingress TCP 地址，如 `192.0.2.20:7443` |
| （run 标志） | `AURIGA_AGENT_INGRESS_LOCAL` | 单机 socket/管道路径；默认 Unix `<agentDir>/ingress.sock`、Windows `auriga-agent-ingress` |
| — | `AURIGA_AGENT_FILE_TICKET_KEYS` | 文件票据公钥 JWKS；默认 `<agentDir>/file-ticket-keys.json` |
| — | `AURIGA_AGENT_IDENTITY_KEYS` | 身份头验签公钥 JWKS；默认 `<agentDir>/identity-keys.json` |
| （策略文件） | `AURIGA_AGENT_POLICY_FILE` | 默认 `<dataRoot>/agent-policy.yaml`；镜像/制品白名单与资源上限 |
| — | `AURIGA_AGENT_ARTIFACT_GC_INTERVAL` | 制品定时 GC 间隔（Go duration），默认 `6h`；`0` 关闭定时（每次准备后仍会尝试一次） |

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
- **制品**：https 白名单、大小上限、连接/总超时分开；URL 路径必须已是 `path.Clean` 形式（拒绝 `.`/`..`/空段、编码的点和分隔符、反斜杠，重定向每跳同样检查）；篡改缓存会重下；按实例 Release；无人引用满 7 天才 GC
- **制品 GC**：每次准备后尝试一次（有准备在下载时跳过），另按 `AURIGA_AGENT_ARTIFACT_GC_INTERVAL` 定时执行，退出时停止。GC 与下载互斥（下载持共享锁、GC 持独占锁）；GC 前收集所有期望规格和运行时现有实例引用的摘要，一律保留，即使 `refs.json` 丢了引用；列实例或读规格失败、或 `refs.json` 本轮刚从损坏中重建时，本轮不删任何文件
- **本地路径**：由规格/清单拼出的路径一律走 `storage.SafeJoin`：元素不能是绝对路径、不能含 `.`/`..`/冒号/NUL，`filepath.Clean` 后必须仍在根目录下，根目录以下已存在的每一级都不能是符号链接（实例可写的 `home/*` 被换成符号链接时 `Layout.Ensure` 拒绝，不 chown）；失败返回 `storage.ErrUnsafePath`。实例 id 另拒绝 Windows 设备名（CON、NUL、COM1 等）
- **容器环境变量（白名单）**：只放平台键（`AURIGA_MODE`、`AURIGA_PROTOCOL`、`AURIGA_INSTANCE_ID`、`AURIGA_OWNER_ID`、`AURIGA_TENANT_ID`、`AURIGA_PLATFORM_URL`、`AURIGA_IDENTITY_KEYS_FILE`、`AURIGA_INSTANCE_TOKEN_FILE`、`DSH_HOME`、`HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY`）和规格 `profile.env` 里声明、且在额外白名单内的键（默认 `TZ`、`LANG`、`LANGUAGE`、`LC_ALL`，`Preparing.ExtraEnvKeys` 可改）。其余键丢弃并记 warn 日志（只记键名，不记值）；键名不合法或值含换行/NUL/控制字符直接拒绝。Docker 驱动创建容器前再校验一次语法并按键排序
- **配置渲染**：`yaml.v3` 结构化生成；拒绝换行/`!!`；禁止覆盖 `AURIGA_*`/`DSH_HOME`；HPKE 解封
- **策略**：`internal/policy` 镜像/制品白名单与资源上限（本地最后一道关）
- **目录**：§8.1、严格实例 id、Lchown、软配额
- 假驱动保留给测试；真 Docker：`go test -tags docker ./internal/runtime/`

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


### A3 安全边界与资源限制

- `/i/` 在身份验签公钥为空时返回 503；有效身份头还必须满足 `aud == 实例 id`。反代共用一个 Transport，关闭入口时清理空闲连接，不读取系统 HTTP 代理环境变量。
- 文件接口拒绝符号链接、Windows 联接及其他 reparse point。Linux 通过 `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)` 打开；内核不支持时才逐级 `O_NOFOLLOW`。创建父目录使用已打开目录下的 `mkdirat`；不会在安全打开失败后退回普通路径。Windows 使用相对目录句柄的 `NtCreateFile(FILE_OPEN_REPARSE_POINT)`，检查句柄属性后才访问文件。删除使用 Go `os.Root.RemoveAll` 的受限目录操作，禁止删除实例根目录。
- 文件票据只使用一次，`jti` 必填；清洗请求路径和票据前缀后再比较。有效期最多 15 分钟，已消费的 `jti` 保留至过期并计入时钟偏差。缓存最多 100000 条，满时拒绝新票据。节点重启会清空内存缓存，上游应签发短期且唯一的票据。
- `ingress.Config.MaxUploadBytes` 默认 64 MiB；超限返回 413，未知长度的请求也受限。流式上传失败可能留下限额内的部分文件，上游需要重新签发票据后重试。
- zip 默认最多 256 MiB 原始文件内容、10000 个遍历条目，分别由 `MaxZipBytes` / `MaxZipEntries` 设置（目前为装配 API）。打包根据打开后的句柄检查文件类型，跳过链接、FIFO 和设备；先写临时文件，成功关闭 zip 后才发送响应，失败不会返回成功状态的截断 zip。
- 定时任务文件只读取普通文件，不跟随链接，单文件最多 1 MiB；解析错误不包含文件内容。cron 只接受五字段表达式；不可满足的 cron 表示无下次唤醒，不接受 `@every` 等描述符。
- 已知的下一次唤醒到期后，即使文件未修改、实例仍停止，observer 也重新计算并上报：一次性任务清空，周期任务或后续任务推进到下一次，避免平台保留过期时间。
- 最近 CPU 活动按相邻成功采样的累计 CPU 差值计算；首次采样、采集失败后的首次采样和计数器回退只建立基线。

## 测试

### Docker 实例内部平台入口

`AURIGA_AGENT_INSTANCE_PLATFORM_URL` 是实例访问 edge 的 HTTP(S) 地址，与节点的 gRPC 控制地址分开。
当 edge 与实例由同一 Docker Engine 管理时，设置 `AURIGA_AGENT_DOCKER_EDGE_CONTAINER` 为受信任的 edge 容器名或 ID。
此时平台 URL 必须使用 DNS 主机名；驱动把该主机名作为 edge 端点的网络别名，接入每个实例独立的 `internal` bridge。
实例仍只加入自己的内部网络；edge 的控制网络不会附加给实例。URL 的端口必须是 edge 容器实际监听端口。

指定的 edge 必须带 `auriga.role=edge` 标签，准备实例时必须正在运行，且不能使用 host、none 或另一个容器的网络命名空间。
驱动在连接前校验网络名称、内部隔离、bridge 驱动、实例归属标签和现有成员，连接后再检查实际 DNS 别名。
已有同名网络不符合策略时拒绝准备。删除实例会先移除实例容器，再断开该网络上的 edge 端点，最后删除网络；edge 停止时仍可移除端点。
这些配置来自节点管理员，不接受实例规格覆盖。外部管理的 edge 网络仍须由部署者提供可达的内部入口。

`deploy/compose` 的本机 Linux 测试栈采用此接线：edge 在 bridge 网络，通过 `host.docker.internal` 访问宿主上的平台和 NATS；
门户端口仅发布到 `127.0.0.1:8080`。这份测试栈不作为 NAS 现场部署配置。
Docker NAT 会把本机测试客户端显示为 bridge 地址，因此测试栈显式启用既有的应急账号远程入口配置；仍须随机安装口令和 TOTP，不能伪造回环来源。生产部署不继承此测试配置。

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

## E2：上报实例入口

NodeControl 的 Hello 现在填充 `ingress_endpoint`。`run --ingress-listen host:port`（或 `AURIGA_AGENT_INGRESS_LISTEN`）启用 TCP ingress；Hello 使用实际绑定后的端口，支持 `:0` 测试。绑定 `0.0.0.0` / `::` 时必须另设 JSON `ingressAdvertiseHost` 或环境变量 `AURIGA_AGENT_INGRESS_ADVERTISE_HOST` 为节点对 edge 可达的 DNS 名称／IP（不带端口）；不会把通配地址发布为入口。

单机继续使用 `--ingress-local` / `AURIGA_AGENT_INGRESS_LOCAL`：Unix 发布绝对 socket 路径，Windows 发布 `npipe:////./pipe/<name>`。默认值仍是 agent 数据目录内的 ingress.sock 或 auriga-agent-ingress 管道。两种本机通道都承载 TLS。入口身份加载或启动失败现在阻止控制会话启动，避免发布不存在的入口。实例运行时 Registry、实例身份执行器和 observer targets 的 A3 接线缺口仍由对应任务处理；本变更只上报已启动入口。
