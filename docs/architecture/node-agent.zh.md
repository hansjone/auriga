# 节点代理（agent）详细设计（v0.1，2026-10-19）

> 总体架构见 [platform.zh.md](platform.zh.md)。"平台"即 Auriga。

---

## 一、定位

节点代理是装在每台运行 DSH 实例的主机上的一个 Go 程序，**平台"控制 DSH"的手和脚**：

- workspace 服务决定"每个实例应该是什么样"（实例规格），节点代理负责在本机把它变成那个样子，并持续上报实际状态。
- 它是平台里唯一有宿主机特权的组件，所以要**小、只执行、不决策、不信任任何输入**。

| 节点代理做 | 节点代理不做 |
|---|---|
| 安装 DSH 运行时和插件包、渲染实例配置、建实例目录和账号 | 判断谁能用、配额多少、什么时候该停（这些是 workspace 的事） |
| 启动、停止、重启、删除实例；探活；资源统计 | 保存任何业务数据（本地状态只是缓存，丢了可以从平台重建） |
| 目录迁移、配额、文件读写、日志、备份和恢复 | 对外提供接口（只对 edge 开一个 mTLS 入口） |
| 读实例里的定时任务文件，算出下次唤醒时间 | 解析或修改 DSH 会话内容 |

它取代原型里的三样东西：`docker-proxy`、实例镜像里的脚本（`init-profile`、`profile-models`、卷迁移）、插件里的运行时准备（`plugin-sync`、心跳里的 cron 解析、文件接口）。

---

## 二、内部模块

```
                      ┌──────────────── agent ────────────────┐
 workspace ◀─gRPC 流─▶│ conn      连接管理：入网、mTLS、重连       │
                      │   │                                      │
                      │ reconciler 对账器：规格 vs 实际，按实例串行 │
                      │   ├─ artifacts   制品：DSH 运行时、插件包   │
                      │   ├─ renderer    配置渲染                  │
                      │   ├─ storage     目录、配额、迁移、备份     │
                      │   └─ runtime     驱动接口                  │
                      │        ├ docker（含 Podman）               │
                      │        ├ linux-process（systemd）          │
                      │        └ windows-process（账号+Job Object）│
                      │ observer  观测：探活、资源、日志、定时任务  │
                      │ ingress   实例入口：反代、文件、日志（mTLS）│◀── edge
                      │ policy    本地策略：规格校验的最后一道关    │
                      │ state     本地状态（bbolt）                │
                      │ updater   自升级                           │
                      └──────────────────────────────────────────┘
```

---

## 三、和平台的连接

### 3.1 入网

1. 管理员在管理台"节点"页生成一次性入网令牌（默认 1 小时有效）。
2. 在新主机上执行 `auriga-agent join --server https://平台地址 --token <令牌>`。
3. 节点代理在本机生成密钥对，用令牌换取 workspace 内置 CA 签发的**节点证书**；之后一律用证书做 mTLS，令牌作废。
4. 证书 30 天有效，到期前 7 天自动续期；管理员可以在管理台吊销某个节点。

单机安装包在安装时自动完成入网，用户无感。

### 3.2 会话协议

节点代理**主动**连 workspace（单机走本机 socket / 命名管道，多机走 mTLS 的 gRPC），工作节点不用开入站端口，NAT 后面也能用。

```proto
service NodeControl {
  rpc Session(stream AgentMessage) returns (stream ControlMessage);
}

message AgentMessage {
  oneof body {
    Hello          hello      = 1;  // 节点 id、版本、OS/架构、可用驱动、容量
    Inventory      inventory  = 2;  // 本机所有实例的实际状态（连上和重连时全量发一次）
    InstanceStatus status     = 3;  // 单个实例状态变化：phase、observedGeneration、ready、error
    NodeUsage      usage      = 4;  // 节点和各实例的 CPU、内存、磁盘、网络
    WakeSchedule   wake       = 5;  // 实例下次定时任务触发时间
    CommandResult  result     = 6;
    LogChunk       log        = 7;  // 按需的日志尾部
  }
}

message ControlMessage {
  oneof body {
    ApplySpec   apply   = 1;  // instance_id + generation + 规格
    Delete      delete  = 2;
    Command     command = 3;  // restart / snapshot / restore / archive / export / tail-log / drain
    PolicyHint  policy  = 4;  // 平台侧限制（节点本地策略仍然优先）
    UpdateAgent update  = 5;  // 新版本地址、sha256、签名
  }
}
```

- **代数（generation）**：每次规格变化代数加一；节点代理上报 `observedGeneration`。两者相等且 ready，才算这次变更完成。重复下发同一代数是安全的（幂等）。
- **心跳**：每 15 秒一次 `NodeUsage`；workspace 60 秒收不到就把节点标为失联，但**不会**据此认定实例已停。
- **重连**：指数退避（1 秒起、最长 30 秒）；重连后先发全量 `Inventory`，由 workspace 对账。

---

## 四、实例规格与对账

### 4.1 规格（由 workspace 生成）

```yaml
instance: inst_01J...           # 实例 id（不是用户 id，一个人以后可以有多个实例）
tenant: t_default
owner: u_9413
generation: 42
runtime: docker                  # docker | linux-process | windows-process
dsh:
  runtime: dsh-0.2.0-rc.2+node22.19   # 制品 id
  digest: sha256:…
resources: { cpus: 2, memoryMb: 2048, diskGb: 10, pids: 512 }
plugins:
  - { name: auriga-dsh, version: 1.0.0, level: L0, digest: sha256:…, signature: … }
  - { name: memwiki,    version: 0.2.1, level: L1, digest: sha256:…, signature: … }
profile:
  listenPort: 7860
  patches: [ … ]                 # 渲染成 cordis.patch.yml 的条目
  mcpServers:
    team: { url: "https://edge.internal/ai/mcp", tokenRef: instanceToken }
  modelProviders:
    team: { baseUrl: "https://edge.internal/ai/v1", tokenRef: instanceToken, models: [ … ] }
network:
  egressProxy: http://egress.internal:3128
  allow: [ edge.internal:443, egress.internal:3128 ]
storage: { schemaVersion: 3 }
secrets:                         # 用节点公钥加密，只有本节点能解开
  instanceToken: sealed:…
  identityPubKeys:               # 用来校验 edge 签名身份头；当前 + 下一把，按 kid 选用
    - { kid: k2026-10, key: "ed25519:…" }
    - { kid: k2026-11, key: "ed25519:…" }
desired: running                 # running | stopped
```

### 4.2 状态机

```
            ApplySpec
 Absent ───────────────▶ Preparing ──▶ Migrating ──▶ Starting ──▶ Running
   ▲                     (下载制品、建  (目录版本     (驱动启动、   (ready=健康检查
   │                      账号和目录、   低于规格时    等健康检查)    通过 + 插件就绪)
   │                      渲染配置)      依次迁移)                      │
   │                                                                   │ 规格变了且需要重启
   │                                                                   ▼
   │         Delete          ┌──────────── Stopped ◀── Stopping ◀── PendingRestart
   └──── Deleting ◀──────────┘    (desired=stopped)                  （等 workspace 发 restart）
                    任一步出错 → Failed（带原因，按退避重试；连续 5 次失败后停止重试并上报）
```

- 每个实例一个对账协程，同一实例的操作串行，不同实例并行（默认同时最多 4 个实例在准备或启动，避免冷启动风暴）。
- **需要重启的变化**（DSH 版本、插件清单、配置补丁、资源上限）只把实例标成 `PendingRestart`，**由 workspace 决定什么时候重启**（通常是实例空闲时），节点代理不会自己打断用户。
- **不需要重启的变化**（出网白名单、令牌轮换）直接生效。

---

## 五、运行时驱动

### 5.1 统一接口

```go
type Driver interface {
    Name() string
    Capabilities() Caps                       // 是否支持网络隔离、磁盘配额、只读根目录等
    Ensure(ctx context.Context, s *Spec, r *Rendered) error  // 创建或更新，但不启动
    Start(ctx context.Context, id string) error
    Stop(ctx context.Context, id string, grace time.Duration) error
    Remove(ctx context.Context, id string) error
    Inspect(ctx context.Context, id string) (Status, error)
    Stats(ctx context.Context, id string) (Usage, error)
    Endpoint(id string) (Endpoint, error)     // 实例的 HTTP 地址，供 ingress 反代
    List(ctx context.Context) ([]Status, error) // 靠标签 / 命名规则发现本节点托管的实例
}
```

以后加 Kubernetes 驱动，只需要再实现这个接口。

### 5.2 Docker / Podman 驱动（Linux 推荐）

| 项 | 做法 |
|---|---|
| 镜像 | 平台构建的实例镜像，按 digest 固定；只能用本地策略白名单里的仓库 |
| 数据 | 绑定挂载 `<data>/instances/<id>/home` → `/home/dsh`，不再用匿名卷 |
| 网络 | 每个实例一个 internal 网络，只连 edge 内部入口和 egress，实例之间互不可达 |
| 用户 | 每个实例分配一个独立的宿主机 uid（从 200000 起），容器以 `--user <uid>` 运行。这样绑定挂载的实例目录在宿主机上属于各自的 uid，节点代理做文件操作时切换到这个 uid，和原生进程驱动的做法一致 |
| 加固 | `cap-drop ALL`；`no-new-privileges`；根目录只读 + `/tmp` 用 tmpfs；内存、CPU、进程数上限；不允许特权、不允许挂宿主机其他路径 |
| 识别 | 容器标签 `auriga.instance=<id>`、`auriga.generation=<n>` |
| Podman | 用它兼容 Docker 的 API；可选 rootless 运行 |

### 5.3 Linux 原生进程驱动

适合不想装 Docker 的 Linux 服务器。要求 systemd ≥ 245、cgroup v2（CentOS 7 不满足，只能用 Docker 驱动）。

| 项 | 做法 |
|---|---|
| 账号 | 每个实例一个系统用户 `auriga-<短 id>`，没有登录 shell，主目录就是实例目录（权限 0700） |
| 启动 | `systemd-run` 创建临时服务单元 `auriga-inst-<id>.service`，进程以实例用户运行；节点代理重启不影响实例 |
| 资源 | `MemoryMax`、`CPUQuota`、`TasksMax` |
| 网络 | `IPAddressDeny=any` + `IPAddressAllow=<edge 内部入口>,<egress>`（systemd 基于 eBPF 的按服务防火墙）；`HTTPS_PROXY` 指向 egress |
| 文件系统 | `ProtectSystem=strict`、`ProtectHome=yes`、`ReadWritePaths=<实例目录>`、`PrivateTmp=yes`、`NoNewPrivileges=yes` |
| 运行时 | DSH 运行时制品（自带 Node）解压到只读的共享目录 |

### 5.4 Windows 原生进程驱动

Windows 服务器上的默认方式。支持 Windows Server 2019 / 2022 / 2025、Windows 10 / 11。

| 项 | 做法 |
|---|---|
| 账号 | 每个实例一个本地账号 `auriga_<短 id>`，口令随机 32 字节，用 DPAPI（本机级）加密存在本地状态里；禁止交互登录和远程桌面登录；只属于专用组 `auriga-instances` |
| 进程托管 | 每个实例一个**垫片进程（shim）**，类似 containerd-shim：由节点代理以实例账号拉起（`LogonUser` + `CreateProcessAsUser`），持有该实例的 Job Object 和日志管道。节点代理重启或升级时实例不受影响，新的节点代理按名字重新接管垫片 |
| 资源 | Job Object：内存上限、CPU 硬上限（CPU rate control）、进程数上限；实例里 shell 派生的所有子进程都在同一个 Job 里，停止实例就是结束整个 Job |
| 文件系统 | 实例目录关闭继承，只给实例账号、SYSTEM 和节点代理权限；数据根目录去掉 Users 组的读权限（否则所有实例账号都能读 `ProgramData` 下的东西）；平台自己的数据（数据库、密钥）只给对应服务账号 |
| 磁盘配额 | 每个实例账号不同，直接用 NTFS 按用户配额 |
| 网络 | 通过 WFP（Windows 筛选平台）按实例账号的 SID 下发出站规则：只允许访问 egress 和 edge 内部入口，其他一律拒绝，包括访问其他实例的本机端口 |
| 监听 | 实例只听 `127.0.0.1:<分配的端口>`，端口从 41000–45000 里分配并记在本地状态 |
| 运行时 | DSH 运行时制品（自带 Node）解压到只读的共享目录；插件目录用目录联接（junction）挂进实例，不需要管理员权限的符号链接 |

**更强的备选：AppContainer**。Windows 自带的应用沙箱，默认没有网络、没有文件访问，权限逐项授予，隔离强度接近容器。风险是 Node 和 DSH 在 AppContainer 里能否正常运行（监听端口、子进程、shell）没验证过。M4 先做一次验证，可行就作为"强隔离"选项。

**隔离强度对比**：

| | 容器 | Linux 原生 | Windows 原生 | Windows + AppContainer |
|---|---|---|---|---|
| 实例之间看不到对方文件 | ✓ | ✓（账号 + 0700） | ✓（账号 + ACL） | ✓ |
| 实例看不到宿主机文件 | ✓ | 大部分（ProtectSystem） | ✗ 能读系统公共目录 | ✓ |
| 网络只能出到白名单 | ✓ | ✓（eBPF） | ✓（WFP） | ✓ |
| 资源上限 | ✓ | ✓ | ✓ | ✓ |
| 内核级隔离 | 命名空间 | 无 | 无 | 部分 |

---

## 六、制品管理

- **内容寻址存储**：`<data>/artifacts/sha256/<摘要>`，同一个文件只存一份。
- **DSH 运行时制品**：由平台的构建流水线打成"Node + 原版 `@deepseek-ai/dsh` 指定版本 + 依赖"的签名压缩包，按 OS / 架构各一份。节点上**不在运行时执行 `npm install`**：可离线、可复现、不受 npm 源影响。Docker 驱动下对应实例镜像（按 digest 拉取，或从离线包导入）。
- **插件包**：从 resource 服务下载，校验 sha256 和平台签名（Ed25519），解压到 `<data>/artifacts/plugins/<名字>@<版本>-<摘要前缀>/`，设为只读；实例通过符号链接或目录联接引用，实例改不动共享的插件文件。
- **预热**：workspace 可以提前让节点下载新版本（灰度升级前），升级时只需切换引用。
- **清理**：没有实例引用的制品保留 7 天后删除；磁盘紧张时提前清理并上报。

---

## 七、配置渲染

每次准备实例时，按规格生成以下文件（都在实例目录的 `meta/` 和 DSH 的 profile 目录下）：

| 生成物 | 内容 |
|---|---|
| profile 的 `package.json` | `dsh.profile.bundles`：L0–L4 插件清单 |
| `cordis.patch.yml` | 监听地址和端口、团队 MCP、团队模型提供商、插件开关、禁用原生插件安装 |
| 环境变量文件 | `DSH_HOME`、插件模式（managed）、平台地址、`HTTPS_PROXY` |
| `identity-keys.json` | 校验 edge 签名身份头的公钥（当前 + 下一把）；换钥匙不需要重启实例，插件发现文件变化就重新读取 |
| `instance.token` | 实例令牌，只有实例账号能读（0600 / ACL） |

- **确定性**：同样的规格永远渲染出同样的文件；渲染结果的摘要存进本地状态，摘要变了才需要重启。
- **只管平台管的部分**：用户自己的设置、会话、工作区、个人模型一概不碰。团队模型写进用户设置文档时，只改带平台标记的那一条（沿用原型的做法）。

---

## 八、存储

### 8.1 目录布局

```
<data>/
├─ agent/                 节点代理自己的：state.db、证书、日志
├─ artifacts/             制品（只读共享）
├─ instances/<id>/
│  ├─ home/               实例主目录：.dsh  .memwiki  .auriga  workspace（用户看到的"我的文件"）
│  ├─ meta/               渲染生成物、令牌、目录版本号
│  └─ trash/              文件接口删除的东西，保留 7 天
└─ backups/
```

Linux 默认 `<data>` = `/var/lib/auriga`，Windows 默认 `%ProgramData%\auriga`，安装时可改。

### 8.2 配额

| 环境 | 做法 |
|---|---|
| Linux（XFS / ext4 支持项目配额） | 每个实例目录一个项目配额，内核级硬限制 |
| Linux（不支持项目配额） | 定期统计用量；超出后文件接口拒绝写入、通知本人和管理员（软限制） |
| Windows | NTFS 按用户配额（每个实例账号单独限制） |

### 8.3 目录迁移

- `meta/schema-version` 记录目录版本；低于规格里的 `storage.schemaVersion` 时，启动前依次执行迁移。
- 迁移写在节点代理代码里（Go），每一步必须幂等；执行前对受影响的目录做快照，失败就回滚并上报。
- 取代原型镜像里的卷迁移脚本。

### 8.4 备份、归档、恢复

- `snapshot`：实例目录打包（tar + zstd），停机或在线（在线时先让插件把内存里的状态刷盘）。
- `archive`：长期不用的实例，打包后移到冷存储（对象存储），删除本地目录；本人下次登录时 `restore`。
- `export`：离职或用户申请时导出给本人。

---

## 九、实例入口（ingress）

节点代理只对 edge 开一个入口（mTLS，只接受 edge 的证书）。单机时走本机 socket / 命名管道，多机时听节点的内网地址。

| 路径 | 用途 |
|---|---|
| `/i/<实例 id>/…` | 反代到实例的 HTTP / WebSocket。edge 已经加了签名身份头，节点代理原样转发 |
| `/files/<实例 id>/…` | 文件上传、下载、打包、删除。要求 workspace 签发的短期票据（限定实例、路径前缀、操作、有效期），所以大文件不用经过 Java 服务中转 |
| `/logs/<实例 id>` | 日志尾部（管理员排障用） |

文件操作的安全要求：
- 以实例账号的身份执行（Linux 用 `setfsuid`，Windows 用模拟），不以 root / SYSTEM 直接写；
- 路径解析不能逃出实例目录：Linux 用 `openat2` + `RESOLVE_BENEATH`，Windows 检查并拒绝指向目录外的联接和符号链接；拒绝 `..`、NUL、点开头的系统目录；
- 打包 zip 时设置 UTF-8 文件名标志（否则中文文件名在部分解压工具里乱码）；上传时自动创建上级目录。

---

## 十、观测

| 项 | 来源 | 上报 |
|---|---|---|
| 健康 | 实例的 `/_health`（由插件提供）+ 进程或容器状态 | `InstanceStatus.ready` |
| 活动 | 插件上报"执行中的轮次 / 终端 / IM 机器人"计数（直接报给平台）；节点代理补充最近 5 分钟的 CPU 使用，用来发现插件看不到的后台进程 | workspace 合并后判断闲置 |
| 定时任务 | 监听实例里的 `ops-cron/store.json`、`storages/schedule.json` 文件变化，解析 cron（含时区）算出下次触发时间 | `WakeSchedule` |
| 资源 | cgroup（Linux / 容器）、Job Object 统计（Windows） | `NodeUsage`，同时在 ingress 上提供 Prometheus 指标 |
| 日志 | 实例标准输出和错误输出，按实例滚动保存（默认每实例 50 MB） | 按需拉取；可选推到 Loki |

---

## 十一、安全

1. **本地策略是最后一道关**。节点上的 `agent-policy.yaml` 由安装时生成、只有管理员能改，规定：允许的镜像仓库和 digest、允许的驱动、单实例资源上限、数据根目录、禁止特权和挂载其他路径。哪怕平台被攻破、下发了恶意规格，节点代理也会拒绝执行并上报。
2. **只听 workspace 的**。所有指令都来自 mTLS 认证过的会话；ingress 只接受 edge 的证书。
3. **机密只在本节点解密**。规格里的令牌等机密用节点公钥加密，传输和日志里都是密文；写到磁盘时只给实例账号读权限。
4. **最小写权限**。节点代理以 root / SYSTEM 运行，但对实例目录的文件操作一律切换成实例账号身份。
5. **本地审计**。每条执行过的指令追加写入本地审计日志，同时上报平台。
6. **限速**。对同一实例的重启、删除类指令限速，防止被利用来做拒绝服务。

---

## 十二、平台不可达时

- 已经在运行的实例**照常运行**，不会因为连不上平台而停。
- 闲置回收暂停（不知道是否真的闲置时宁可不停）。
- **闹钟唤醒照常**：节点代理缓存了每个实例最近一次的规格和下次唤醒时间，到点自己拉起实例，事后上报。
- 文件、日志入口照常（票据在有效期内仍可校验）。
- 重连后全量上报，由 workspace 对账。

---

## 十三、自升级

1. workspace 下发 `UpdateAgent`（版本、地址、sha256、签名）。
2. 节点代理下载、校验签名，放到 `agent/releases/<版本>/`。
3. 切换后通过服务管理器重启自己（systemd / Windows 服务恢复策略）。
4. 新版本 60 秒内连回平台就算成功；否则服务管理器拉起旧版本（保留最近 2 个版本）。
5. 实例在整个过程中不受影响：容器独立于节点代理；Linux 原生实例是独立的 systemd 单元；Windows 原生实例由垫片进程托管。

---

## 十四、本地状态

用 bbolt（纯 Go 的嵌入式键值库）保存：节点身份和证书、每个实例最近应用的规格和代数、渲染摘要、端口分配、实例账号映射（Windows 口令用 DPAPI 加密）、制品引用计数。

**它不是权威数据**：全部丢失时，节点代理按命名规则和标签重新发现本机实例，再由 workspace 重新下发规格即可恢复。

---

## 十五、配置文件

```yaml
# /etc/auriga/agent.yaml 或 %ProgramData%\auriga\agent.yaml
server: https://platform.example.com      # 单机安装时是本机 socket
dataRoot: /var/lib/auriga
drivers: [docker]                         # docker | linux-process | windows-process
ingress:
  listen: 192.0.2.20:7443                 # 单机时不填，走本机 socket
portRange: 41000-45000                    # 原生进程驱动用
maxConcurrentPrepare: 4
policyFile: /etc/auriga/agent-policy.yaml
```

---

## 十六、测试与验收

| 层 | 内容 |
|---|---|
| 单元测试 | 对账状态机（用假驱动）、配置渲染的确定性、cron 计算（含时区、夏令时）、路径安全、策略校验 |
| 驱动测试 | 每个驱动跑同一套用例：创建、启动、健康、资源上限生效、网络只能出白名单、实例之间互相访问不到、停止后无残留进程 |
| 故障注入 | 平台断连（实例不停、闹钟照常）、节点代理被杀（实例不受影响、重启后接管）、迁移失败回滚、磁盘满 |
| 端到端 | 平台 + 节点代理 + 真实 DSH：登录 → 冷启动 → 对话 → 闲置回收 → 闹钟唤醒 → 灰度升级 → 回滚 |
| 平台矩阵 | Ubuntu 22.04 / 24.04、Rocky 9、麒麟 V10（Docker 和原生）；Windows Server 2022 / 2025（原生） |

---

## 十七、分步实现

| 步骤 | 内容 | 所属里程碑 |
|---|---|---|
| A1 | 骨架：入网、gRPC 会话、本地状态、对账器、假驱动 | M2 |
| A2 | Docker 驱动、制品管理、配置渲染、目录迁移；替换 docker-proxy | M2 |
| A3 | ingress（反代、文件、日志）、观测（活动、定时任务、资源） | M2–M3 |
| A4 | Windows 原生驱动（账号、垫片、Job Object、ACL、WFP、NTFS 配额）；AppContainer 验证 | M4 |
| A5 | Linux 原生驱动（systemd-run、eBPF 网络限制） | M4 |
| A6 | 备份、归档、恢复、导出；自升级；平台不可达时的自治 | M5 |
