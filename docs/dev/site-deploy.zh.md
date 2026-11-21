# 统一现场构建与部署（设计）

状态：已通过独审（三轮，报告见主控记录）。第 7 节列出交给各实施 PR 处理的遗留项。

## 1. 目标与场景

现场和我们自己的测试环境用**同一套命令、同一份配置**完成：

| 场景 | 源码 | 依赖 | 结果 |
|---|---|---|---|
| S1 服务端，源码直接构建 | 配置里的受信 git 远端（或 root 所有的源码目录） | 本地缓存优先，缺的按配置的依赖源补 | 一键构建并安装，支持全量和增量 |
| S2 服务端，开发套件构建 | 同上 | 发行套件或现场套件，完全离线 | 同上 |
| S3 Windows 客户端 | 同上 | 缓存或套件；联网补依赖与断网构建分两段 | 产出待签目录，签名保留人工核对 |
| S4 工作流触发 | 工作流只传提交号，由 root 进程自己取源码 | 同 S1/S2 | 非交互运行，产出日志和收据，退出码可判定 |

共同要求：失败自动回滚；每次留日志和机器可读收据；依赖源可配置，默认不访问公网。

**不在范围**：数据备份策略、DSH 运行时模板升级（仍走 `runtime-upgrade.sh`）、密钥轮换、多节点（`join-node.sh` 的远端 runtime 节点，要求 agent 协议 N-1 兼容，切换和回滚只管主站）、runtime 首装（仍要求签名外层包，`deploy` 首装不含 runtime，之后用 `runtime-upgrade.sh`）。

## 2. 现状与差距

已有（`deploy/offline`、`deploy/nas/offline-build`、`scripts/client-release`）：

- `auriga bundle --build`：以下载的 NAS 发行制品为骨架（`bundle.go` 先对骨架 `image-check`），用发行索引或开发套件里**审核过的配方**覆盖源码中的 `deploy/nas/offline-build`（`build.go:50-56`），构建五个镜像和 `auriga-nas-bootstrap`、`runtime-release`、`auriga-cli`，打成签名离线包。
- `--install` 接 `upgrade`：冷备份整站，激活新发行；成功后上一版发行被移到 `auriga-backups/release-*`；失败从冷备份恢复。
- 依赖输入按生态**按锁摘要整套封闭**（build 时 `cmp source-locks`）。Go 由 `go.sum` 保证完整性，pnpm 由 lock 的 `integrity` 保证；**Maven 和 pip 没有锁哈希**，现有 `--online` 靠打印清单、`site-dependencies.json` 和人工审核兜底（`online.go` 的 `siteReportNotice`）。
- 客户端 `auriga-client build` 必须有 `--devkit`；`--online` 分两段：先联网派生现场套件，再断网构建，产出 `offline-evidence.json` 和空的 `network-denials.log` 作为证据；签名要人工 `sign --review-only` 核对。

差距：

1. 每次全量构建、整站重启，没有按服务增量。
2. 回滚靠整站冷备份，耗时长且停服。
3. 依赖源分散在环境变量里，没有统一配置和缓存优先的解析。
4. 同机构建同机安装仍要 `docker save` 再 `load`；套件模式每次 `docker load` 全部镜像。
5. 只输出到终端，要交互式 sudo，工作流无法调用。
6. 客户端不能不用发行套件构建。
7. 我们自己的 NAS 栈不是 `auriga install` 装的，没有 state。

## 3. 核心决定

### D1 部署的永远是镜像，回滚就是退回镜像

- 业务数据都在挂载目录，`deploy` 不做数据备份，回滚只把服务切回上一版镜像和上一版发行目录。
- **不可变发行目录**：每次成功部署写一份 `<data>/../auriga-releases/<序号>/`，含 `deploy-nas/`（编排与脚本）、`bin/auriga-nas-bootstrap`、`bin/runtime-release` 和本地清单 `SHA256SUMS`。`current` 指针（state 字段，切换时原子写 state）指向当前序号。保留最近 N 份（默认 3）。
- **不可变镜像标签**：每个镜像除了 `auriga/<svc>:nas`，再打 `auriga/<svc>:r<序号>`。淘汰历史前先用 `docker ps -a --format '{{.Image}}'` 确认没有容器在用。`docker image prune` 不会删掉带标签的镜像。
- 冷备份保留为独立命令 `auriga backup`。签名包 `upgrade` 的冷备份语义不变，供跨机器场景使用。

### D2 旧镜像必须能跑在新版本留下的数据上（N-1 兼容）

“只增不删”不够，改为 **N-1 兼容约定**：每个版本留下的数据库结构和数据目录格式，必须能被上一版镜像正常读写。

- **“版本”的含义**：现场按提交部署，可能跳过中间提交。所以兼容关系按提交表达：每个发行目录的清单声明 `schemaCompatFloor`，即本提交的迁移和格式变更之后，仍能正常运行的最旧提交。它由迁移脚本和格式登记的元数据生成：本提交没有破坏性变更时继承父提交的 floor，有破坏性变更时 floor 前移到本提交的父提交之后。CI 校验这个值和迁移内容一致。
- **数据库**：
  - Flyway 在全部 4 个自建入口显式配置 `ignoreMigrationPatterns("*:future")`：`CommonMigrations.migrate`、`PlatformCombinedConfiguration.migrate`、`PlatformCollabConfiguration`、`AuditStore`。它们不吃 Spring 配置，每个入口各有单测。
  - 破坏旧代码的变更（删除或改名、新增无默认值的 NOT NULL 列、新唯一或 CHECK 约束、收紧 RLS、改变语义的回填）必须分两个版本做；确需一次完成时，迁移元数据标记为破坏性，floor 随之前移。
  - **CI 真实回退测试**（PR 门禁）：用当前 main 头的镜像，跑在本 PR 迁移后的库上做冒烟。每个合入的提交都对它的父提交保证兼容，逐提交部署因此也有依据。这是硬门禁，不靠关键字检查。
  - 约定从本设计合入后的第一个版本起执行。仓库里已有含 DROP 的历史迁移不追溯；本设计之前的发行没有声明 floor，回滚到这些发行一律按“拒绝自动回滚”处理。
- **数据目录格式**：`config/materials`、collab 密钥与轮换快照、`materials-upgrades`、relay 迁移等会就地改写数据目录的流程，各自登记格式版本号（写进数据目录的 `format-versions.json`）。发行清单声明本版能读的最高格式版本。
- **收据**：逐库逐表记录迁移版本，覆盖主库各 schema 的 `flyway_schema_history` 与 `auriga_common_schema_history`、`collab-db`、audit。
- **回滚前检查**（自动和手动回滚、向更旧提交部署都适用）：目标提交必须不早于当前运行版本的 `schemaCompatFloor`，且当前数据目录格式版本不高于目标能读的上限。任一不满足，或目标没有声明 floor 和格式上限，就不自动执行，按退出码 3 交给人工。
- `--backup-db` 进入第一版：对主库、`collab-db`、audit 分别 `pg_dump`（不停服，每个库内部一致，跨库不保证同一时刻），不参与自动回滚。

### D3 同机构建同机安装，不打包；信任依据是 root 自己取的源码

- `deploy` 在服务器本机构建，镜像直接留在本机 Docker，不 `save`/`load`，不生成离线包。套件模式按 digest 判断镜像已在本机就跳过 `load`。
- **源码获取**：工作流只传提交号。`deploy` 以 root 从配置里的 git 远端 fetch 到 root 所有的暂存目录，用 `git archive <提交>` 导出快照再构建；不在运行器目录里执行 git，也不读取运行器仓库的 `.git/config`。可选：校验提交签名，或要求提交在受保护分支白名单上。人工运行时也可以用配置里 root 所有的源码目录。
- **快照摘要**：把现有 `snapshotGitSourceContext` 中“逐文件 mode、sha256、path 摘要”的部分抽成独立函数，直接对导出目录计算，得到 `SnapshotSHA256`（导出目录没有 `.git`，不能走原函数）。
  - git 远端模式：快照来自 `git archive <提交>`，`dirty=false` 由来源决定，收据记提交号和 `SnapshotSHA256`；
  - 人工源码目录模式：仍按原函数检查 git 状态，记录 dirty 标志（`sourceTreeContext` 只取 `HEAD^{tree}`，不含未提交改动，不再用作信任依据）。
- 可选的提交签名校验使用配置里的 `source.git.trustedSigners`（独立的受信签名者公钥列表），不依赖 root 的全局 keyring。
- **不再依赖 NAS 骨架**：`deploy-nas`、`auriga-nas-bootstrap`、`runtime-release` 全部从同一份源码快照构建。
- 跨机器仍用 `auriga bundle` 生成签名离线包，目标机 `upgrade`。

### D4 配方属于源码，强制约束挪进 Go CLI

- `deploy` 一律使用源码快照里的配方（`deploy/nas/offline-build`），不再用套件配方覆盖。套件只提供依赖输入、工具镜像和基础镜像。
- 原来靠审核过的配方脚本保证的约束，改由 Go CLI 强制执行，不依赖配方自律：
  - Go、Web、Java 构建容器由 CLI 启动，参数固定为 `--network none --pull never`（现状已如此）；
  - **装配不再在宿主机执行源码脚本**：CLI 按配方声明的暂存清单自己暂存产物，直接调用 `docker build --network=none --pull=false`，构建参数由 CLI 固定，配方只提供 `Dockerfile.assembly` 和暂存清单（数据文件，不是可执行脚本）。`assemble-image.sh` 保留给 `bundle` 路径；
  - 构建前在 CLI 里比较源码锁清单与所用依赖输入的 `source-locks.sha256`，不一致就拒绝；
  - 工具镜像和基础镜像必须是固定 digest。
- **强制范围如实说明**：构建容器里运行的仍是源码中的 `*-inputs.sh build` 脚本，它们在断网容器里执行，影响的是构建产物，影响不到宿主机；宿主机上以 root 执行的源码脚本只剩 `deploy-nas/scripts`（render、preflight、start 等），这正是 D12 “触发者等价 root” 的原因。
- 服务划分文件放在 `deploy/site/services.json`（不在 `offline-build` 里）。
- `bundle` 的现有行为不变（仍用审核配方覆盖），供跨机器签名包使用。

### D5 依赖：本地缓存优先，依赖源可配置，默认不连公网

- **缓存和套件同一种格式**，布局为 `inputs/<生态>/<锁摘要>/`，外加工具镜像和基础镜像。依赖输入按锁摘要整套选取，不按单个包分层查找：先在可写缓存找该锁摘要，再到配置的套件里找，都没有才去依赖源准备。
- **套件要有独立信任**：`deps.kits` 每项必须带 `trustFingerprint`，或引用 state 已钉定的发行钥。不接受套件用自带公钥验自己。
- **按生态的补依赖规则（第一版）**：

| 生态 | 完整性依据 | 能否自动补 |
|---|---|---|
| Go | `go.sum`；`allowPublic=false` 时不回落 `proxy.golang.org`，sumdb 走配置代理，取不到就失败 | 能 |
| Web（pnpm） | lock 的 `integrity`；拒绝没有 `integrity` 的条目 | 能 |
| Java（Maven） | 新增 `services/maven-deps.sha256`（坐标:版本:sha256），构建时核对 | 锁文件就绪前不能，只能 `deps import` 加人工审核 |
| Python（operator） | requirements 改为 `--require-hashes` | 改造前不能，只能 `deps import` |
| 系统包、基础镜像、工具镜像 | 固定 digest 或套件签名 | 不能，只能 `deps import` 或套件 |

- **工作流模式出现新增依赖时默认失败**（退出码 1），收据列出全部新增项。配置或参数显式 `acceptNewDeps` 才允许自动补，且仅限上表中“能”的生态。
- 新拉到的依赖写回可写缓存并记录来源和 SHA-256（沿用 `site-dependencies.json`）。离线缺依赖时失败，列出缺什么、去哪里取，用 `auriga deps import <目录或归档>` 补进缓存。
- 内部依赖源常用私有 CA：`deps.caFile` 以只读方式挂进准备依赖的容器。
- 现有 `AURIGA_*_PREPARE_*` 等环境变量保留为底层实现，由 CLI 从配置生成。

### D6 增量：输入指纹决定是否构建，产物摘要决定是否切换

- **指纹输入从配方声明生成，不手写路径**：
  - Go：`go list -deps -f '{{.Dir}} {{.EmbedFiles}}'` 得到每个程序的包目录和嵌入文件，加上 `go.mod`、`go.sum`、`go.work`、`go.work.sum`；
  - Web 和 Java：把 `web-inputs.sh`、`java-inputs.sh` 里的打包清单抽成共享常量（配方和 CLI 共用）；
  - 各镜像的装配输入：取自 `assemble-image.sh` 的声明（如 agent 的 `agent-entrypoint.sh`，bifrost 的 `config.json` 与 `third_party/bifrost/*`，operator 的 requirements 与 `inputs/system/operator`）；
  - 再加依赖输入锁摘要、工具镜像、基础镜像。
- `deploy/site/services.json` 只声明“镜像 → 产物”和“产物 → 配方输入集”的对应关系，不列路径。CI 覆盖测试：每个受跟踪文件要么属于某个输入集，要么在显式 `ignore` 里。
- **双保险**：指纹变了才构建；构建后按该镜像的**装配摘要**决定是否切换。装配摘要 = 该镜像全部产物摘要（Go 二进制已用 `-trimpath`，可复现；jar 设置 `project.build.outputTimestamp`；web 产物）+ 基础镜像 digest + 该服务的系统输入摘要（`inputs/system/<svc>`）+ `Dockerfile.assembly` 对应目标段与暂存清单摘要。任何一项与当前版本不同就切换，收据写明是哪一项触发的。这样基础镜像安全更新这类“产物不变、镜像要变”的情况不会漏掉；漏判“要构建”只会多构建，不会漏切换。
- **伪服务**：`auriga-nas-bootstrap`、`runtime-release` 也有指纹，变化时进入新发行目录。
- **provenance**：`web/admin/provenance.json`（含提交号和构建时间）改为运行时由 edge 从挂载文件读取，不打进镜像，这样不会让 edge 每次都重建，About 页面也能显示当前提交。`bundle` 跨机器路径同样改为由发行目录提供该文件，两条路径行为一致。
- `--full` 强制全部重建；`--services a,b` 手工指定，指纹检查会补上共享输入变化的服务并提示。

### D7 切换：镜像与服务的对应关系在代码里维护并与 compose 交叉测试

- **镜像 → 服务表**写在代码里，CI 解析 `docker compose config` 的 `image:` 插值来源做交叉测试。当前事实：
  - edge 镜像：`panel-prepare`、`panel-renew`、`ai-relay`、`edge`、`network-broker`、`network`、`egress`；
  - platform 镜像：platform，collab overlay 存在时还有 collab；
  - `collab-renew` 跟 `AURIGA_INSTALLER_IMAGE`（POSTGRES）走；
  - operator 不是 compose 服务，只被 preflight、admin-bootstrap、emergency 脚本使用。
- **编排摘要**覆盖 render 的全部输入：`deploy-nas` 文件（排除 `*.md`、`test/` 中非镜像输入），`stack.env` 去掉 `AURIGA_IMAGE_*` 后的规范化内容，access 文件，collab-source 回执摘要。
- **只换容器路径**（编排摘要不变时）第一版只对 edge 和 platform 开放：
  - edge：重建集合为 `edge`、`ai-relay`、`egress`、`panel-renew`、`network`；`panel-prepare` 是一次性准备任务，只换容器路径跳过它（它的输入属于编排摘要，变化时走完整启动）。network-broker（NATS 安装角色）不在只换容器路径内重建。edge 镜像变化时如果 broker 程序（`auriga-network-broker`）的产物摘要也变了，就走完整启动；否则 broker 继续用旧镜像，收据如实写明“混合版本：network-broker 仍为 r<旧序号>”。后续可把 broker 拆为独立镜像键 `AURIGA_IMAGE_NETWORK_BROKER` 再放开。
  - platform：collab overlay 存在时走 `start-collab-network.sh` 的公开路径，不直接 `up --no-deps collab`。
  - 按公开启动逻辑重启 agent，恢复实例网络端点。
- operator 镜像变化：只改 `stack.env` 并跑一次 preflight。
- 其余情况（agent、bifrost 变化，编排变化，首装）一律走完整流程 `render` → `preflight` → `start`。
- 切换后等待健康检查（`waitHealthy` 改为接受超时参数，默认 10 分钟）；冒烟用新的只读部署冒烟脚本（不用会默认报 PARTIAL 的 `smoke.sh`），可关闭。
- 写入与 `load-images.sh` 等价的发行标记和 `versions.txt`，`status.sh` 正常显示。

### D8 自动回滚

- 触发：容器未健康、部署冒烟失败、启动脚本非零退出。
- 动作：先做 D2 的格式检查；通过后把涉及服务的 `AURIGA_IMAGE_*` 写回上一条历史的值（按不可变标签解析出镜像 ID）；如果本次编排也变了，`current` 切回上一份发行目录并完整启动，否则按 D7 只换容器；然后等待健康。
- 首装失败没有上一版可回：退出码 1，保留现场，不清理数据。
- 回滚失败（包括切回旧发行目录后 render 失败）不自动重试，退出码 3，收据写明各服务实际运行的镜像。
- 手动：`auriga rollback [--to <序号>]`，可回到保留的任意一份历史，同样先做 D2 的 floor 与格式检查；跨越 floor 的回退只能人工处理。
- **2b 合入前的保护**：在 N-1 兼容门禁（§5 第 3 步）合入之前，自动回滚只在“新镜像未能启动、迁移版本和格式版本与切换前相同”（即数据未被新版本触碰）时执行；否则退出码 3 交人工。2a 就带上迁移版本和格式版本的读取，作为这条判断的依据。

### D9 统一配置文件

- 固定路径 `/etc/auriga/site.json`（Windows `%ProgramData%\Auriga\site.json`）。Linux 要求 root 所有、0600 或 0644，祖先目录不可被组或其他用户写（复用 `trustedParent`）；Windows 不能复用 `trustedParent`（它依赖 `stat -c`），单独实现 ACL 校验：所有者为 Administrators 或 SYSTEM，普通用户没有写权限。
- `deps.cache` 同样要求 root 所有、组和其他用户不可写。在 Maven 锁文件（§5 第 8 步）就绪前，可写缓存是 Java 依赖唯一的保护边界。
- **经 sudo 调用时不接受 `--config`**；人工以 root 直接运行时可以用 `--config` 指向同样满足权限要求的文件。
- 示例：

```json
{
  "schemaVersion": 1,
  "source": {
    "git": {
      "url": "https://git.internal.example/team/auriga.git", "allowedRefs": ["refs/heads/main"],
      "requireSignedCommits": false, "trustedSigners": []
    },
    "dir": null
  },
  "deps": {
    "cache": "/srv/auriga/cache",
    "kits": [{ "path": "/srv/auriga/kits/auriga-devkit-0.2.0", "trustFingerprint": "SHA256:..." }],
    "sources": {
      "go": ["https://goproxy.internal.example"],
      "npm": "https://npm.internal.example",
      "maven": "https://maven.internal.example/repository/public",
      "files": "https://files.internal.example/auriga-inputs"
    },
    "registry": "registry.internal.example",
    "caFile": "/etc/auriga/internal-ca.pem",
    "allowPublic": false,
    "acceptNewDeps": false
  },
  "server": {
    "data": "/srv/auriga/data",
    "install": {
      "address": "192.0.2.10", "domain": "auriga.example", "proxy": null,
      "desktopPublicKey": null, "desktopPublisherTenant": null, "desktopConfirmFingerprint": null
    },
    "healthTimeout": "10m",
    "smoke": true,
    "keepHistory": 3
  },
  "sign": { "keyFile": "/etc/auriga/site-sign.key" },
  "client": {
    "out": "D:\\auriga-client-out",
    "platformOrigin": "https://auriga.example",
    "verifyKey": null, "trustFingerprint": "SHA256:...",
    "releasePublicKey": "...", "resourceTrustJson": "..."
  },
  "logs": { "dir": "/var/log/auriga" }
}
```

- 规则：未知字段报错；`schemaVersion` 升级沿用套件 `requires` 能力表做法。依赖源 URL 只允许 `https://` 和 `file://`，`registry` 单独校验主机名。只有 Go 支持多个依赖源，npm、maven 各一个。密钥只写路径。客户端 `sequence` 从发布账本自动取，不写在配置里。

### D10 统一命令

```
auriga deps status|fetch|import <路径> [--client]   依赖：看缺什么、预先拉取、导入手工缓存
auriga deploy [<提交号>] [--full] [--services a,b] [--dry-run] [--backup-db]
auriga rollback [--to <序号>]
auriga status | auriga logs [服务] | auriga history
auriga adopt --stack-env <路径> --deploy-nas <目录>
auriga bundle ...                                  不变：跨机器用的签名离线包
auriga client build                                Windows 客户端，见 D13
```

- `deploy` 没有 state 时执行首次安装：复用 `install()` 的步骤（站点构建钥登记、首启 seed 翻转与二次 start 即 `finishInstall`），只把“镜像来源”换成本机构建；postgres、nats、registry 镜像来自套件或按 digest 从 `registry` 拉取。
- **首装的发行钥钉定**：没有签名包，`pinInitialTrust` 不会执行。配置了带 `trustFingerprint` 的套件时，用它钉定发行钥；没有就不钉定，state 标记未钉定。和 adopted 一样，之后第一次签名包 `upgrade` 前必须先 `trust add-*`。
- `--dry-run` 打印：会构建和切换哪些镜像及原因（哪些输入变了）、走只换容器还是完整启动、要补哪些依赖、迁移和格式版本情况。
- `adopt` 为已用 `deploy-nas` 脚本部署的站点建立 state，标记 `source=adopted`，记录当前镜像、编排摘要和数据目录，不伪造信任；之后第一次跨机器签名包 `upgrade` 前要求 `trust add-*`。支持 `AURIGA_INSTALL_MODE=host`、没有 `.oneclick` 的站点。
- **state 区分来源**：`source` 取值 `signed-package`、`local-deploy`、`adopted`。`verifyStatePackage` 对 `local-deploy` 来源核对本地清单 `SHA256SUMS`（不要求签名），并允许之后接收签名包 `upgrade`，不借用 `AllowUnsigned`。
- **签名包 `upgrade` 接入新布局**：`upgrade` 也产生一条 history，占用新序号，把包内发行写到 `auriga-releases/<序号>/`，打 `:r<序号>` 镜像标签，切换 `current`；冷备份语义保持不变（`upgrade` 仍先冷备份，失败从冷备份恢复）。之后 `rollback` 可以退回 deploy 产生的历史，同样受 floor 与格式检查约束。`activateUpgrade` 里 `s.Package` 加 `.previous` 的做法改为基于序号目录。
- **CLI 自身升级**：`/usr/local/bin/auriga` 只随签名包更新，或由 root 用受信提交手动构建安装；`deploy` 不替换自己。启动时比较 CLI 能力版本与源码快照里配方声明的能力版本，不匹配就失败并提示先升级 CLI。

### D11 日志、收据、退出码与锁

- 每次运行写 `<logs.dir>/<时间>-<命令>.log` 和同名 `.json` 收据。日志目录要求 root 所有，写入用 `O_NOFOLLOW` 并先 `noSymlinks`。
- 收据：命令和配置摘要、源码提交、`SnapshotSHA256` 与 dirty、依赖来源（缓存命中、依赖源 URL、新增依赖清单）、构建与跳过的产物及原因、切换的镜像与服务、新旧镜像 ID 和标签、混合版本说明、编排是否变化、迁移与格式版本前后、健康结果、`phase`（`build`/`switch`/`rollback`）、是否回滚、耗时。
- **脱敏**：日志不输出密钥路径以外的密钥材料；应急材料仍只走私密 TTY（`emergency.sh --first-start`），不进日志和收据。
- 退出码：`0` 成功；`1` 未切换就失败（参数、依赖、构建错误，首装失败），现场未受影响；`2` 切换失败且已自动回滚；`3` 回滚失败或格式检查拒绝回滚，需要人工。
- `--json` 让标准输出只打印收据。
- **锁**：所有改动站点的命令（`install`、`upgrade`、`backup`、`deploy`、`rollback`、`adopt`）统一改用同一把 `flock(2)` 锁文件（进程被杀时内核自动释放），替换现有 `withLock` 的 `mkdir` 锁；`deploy` 的锁范围从构建开始到收据写完。第二个任务直接失败并说明正在进行的操作。在 2a 内完成。

### D12 工作流触发与提权边界

- **边界说明**：能触发 `deploy` 的人，等价于能以 root 改这个站点的人。因为部署会以 root 执行源码里的构建和启动脚本，这一点无法通过参数限制消除，只能限制“部署哪些源码”。
- 运行器以普通用户运行，**不加入 docker 组**。sudoers 只放行 `/usr/local/bin/auriga deploy *`、`/usr/local/bin/auriga rollback *`、`status`、`history`（标准通配写法，不依赖 sudo 正则支持）。
- **参数白名单由 CLI 执行**：环境里存在 sudo 设置的 `SUDO_UID` 即判定为 sudo 调用。此时：
  - `deploy` 只接受一个 40 位十六进制提交号与 `--full`、`--services`、`--dry-run`、`--json`、`--backup-db`，其余参数一律拒绝（包括 `--config`、`--source`、`--allow-downgrade`）；
  - `rollback` 只接受 `--to <序号>` 与 `--json`；
  - 提交号格式在 CLI 里校验。
- 提交号必须能在配置的 `allowedRefs` 上找到（可选按 `trustedSigners` 校验签名），由 root 进程自己 fetch。
- **防降级**：经 sudo 的 `deploy` 只允许当前运行提交的后代（快进）。要回到更旧的提交只能用 `rollback`（受 floor 与格式检查约束），或由 root 人工运行 `deploy --allow-downgrade`（同样先做 floor 与格式检查）。
- 文档附可直接使用的 sudoers 片段与运行器安装说明。
- 签名包 `bundle` 不进工作流的 sudo 白名单：在独立构建机上运行，签名钥放构建机，或作为该构建机工作流的秘密变量传给非 sudo 进程。同机 `deploy` 不需要签名钥。
- 提供示例 `deploy/site/workflows/site-deploy.yml`（Forgejo Actions，GitHub 同理）：`sudo auriga deploy $COMMIT --json` → 上传脱敏日志和收据 → 退出码非 0 标红。附 sudoers 片段与安装说明。

### D13 客户端

- 入口保持 `auriga-client.cmd` 为主；`auriga.exe client build` 是可选薄壳，读 `site.json` 后调用同一套带摘要门禁的流程。`auriga.exe` 随发行附件分发并带 SHA-256，说明 SmartScreen 提示的处理。
- **保持两段式**：`auriga deps fetch --client` 联网把依赖写入可写缓存（形成现场客户端套件）；`build` 阶段网络守卫不变，继续产出 `offline-evidence.json` 和空 `network-denials.log`。
- “不用发行套件”指：基础输入来自现场缓存。构建用 Node 只用缓存或套件中摘要匹配 `build-node.json` 的版本，不用系统 Node。
- 构建 Node、CPython 独立包、DSH runtime、LibreOffice 闭包等文件类输入，经 `deps.sources.files` 按锁定 URL 与 SHA-256 取得。系统工具（csc.exe、tar、PowerShell）摘要允许“现场登记基线”（`auriga deps baseline --client`），写入收据；Windows 更新导致变化时提示重新登记。
- 签名仍保留人工 `sign --review-only` 核对；自动化只做到产出待签目录。

### D14 构建缓存加速（后续）

在可写缓存下持久化 Go 的 GOCACHE（按内容寻址，不影响结果正确性），以读写方式挂进构建容器。装配镜像去掉 `--no-cache`，改为基于输入摘要的确定性层缓存。只影响速度，功能闭环后再做。

## 4. 安全边界（与现状对比）

| 项 | 现状 | 本设计 |
|---|---|---|
| 依赖完整性 | 套件签名；Go `go.sum`、pnpm `integrity`；Maven、pip 无锁哈希，靠人工审核 | Go、pnpm 不变且禁止无 `integrity` 条目；Maven 补锁文件前、pip 改 `--require-hashes` 前不自动补；工作流默认拒绝新增依赖 |
| 公网访问 | `--online` 时访问 | 默认关闭，`allowPublic` 显式打开；Go 不默认回落公网 |
| 套件信任 | 签名源码时用源码钥，否则用套件自带钥 | 每个套件必须带独立信任指纹或 state 钉定钥 |
| 配方 | 套件或发行的审核配方覆盖源码配方 | `deploy` 用源码配方；构建与装配都由 CLI 以断网参数启动，锁比较、固定 digest 由 CLI 强制；宿主机上只执行 `deploy-nas/scripts`；`bundle` 不变 |
| 降级 | 无 | sudo 部署只允许快进；回退走 `rollback` 并受 `schemaCompatFloor` 与格式检查约束 |
| 同机部署信任 | 打签名包再验签 | root 自取受信远端的允许引用上的提交，记录 `SnapshotSHA256`；跨机器仍签名包 |
| 回滚 | 冷备份恢复数据 | 不可变发行目录与镜像标签；N-1 兼容门禁与格式检查；数据不动 |
| 提权 | 人工 sudo | 受限 sudoers，只收提交号；明确“触发者等价 root” |
| 配置与日志 | 命令行 | root 所有的固定配置；日志目录防符号链接；输出脱敏 |

## 5. 分步实施

每步一个 PR，按门禁合并（精确 HEAD 全 CI 绿 + 非作者独审）。

1. **配置与依赖解析**：`site.json` 解析与权限校验；`deps status|fetch|import`；缓存布局 `inputs/<生态>/<锁摘要>/`；套件独立信任；按生态的补依赖规则；`caFile`；由配置生成现有环境变量。
2. **2a 全量 deploy**：root 自取源码与目录快照摘要；源码配方、CLI 直接装配与强制约束；免打包；首装复用 `install()` 与钉定规则；不可变发行目录与镜像标签；state 来源字段、`verifyStatePackage` 与 `upgrade` 接入新布局；历史与退镜像回滚（2b 前只在数据未被触碰时自动回滚）；迁移与格式版本读取；部署冒烟；收据、退出码；统一 `flock`；sudo 入口、CLI 参数白名单与快进限制；CLI 能力版本检查。
3. **2b N-1 兼容门禁**：4 个 Flyway 入口的 `ignoreMigrationPatterns` 与单测；PR 门禁用 main 头镜像的真实回退测试；`schemaCompatFloor` 生成与 CI 校验；数据目录格式版本登记；回滚和降级前的 floor 与格式检查；`--backup-db`。
4. **2c 指纹与按产物构建**：输入集共享常量、`go list -deps`、覆盖测试、产物摘要比较、jar 可复现时间戳、provenance 改运行时挂载。
5. **2d 只换容器切换**：镜像 → 服务表与 compose 交叉测试；先开放 edge（broker 混合版本规则）与 platform（collab 走公开脚本）。
6. **2e adopt**：接入我们的 NAS 测试栈。
7. **工作流**：示例工作流、sudoers 片段、运行器安装说明、脱敏。
8. **Maven 锁文件与 pip 哈希**：完成后放开 Java、Python 的自动补依赖。
9. **客户端统一**：`deps fetch --client`、文件类依赖源、系统工具基线登记、`auriga.exe` 薄壳。
10. **构建缓存加速**（D14）。
11. **验收矩阵**（NAS 与本机 Windows）：源码配本地缓存、发行套件、现场套件；全量、增量（只改 edge、只改 platform、改编排、改 broker）；健康失败触发回滚；格式检查拒绝回滚；离线缺依赖报错与 `import`；工作流成功一次、回滚一次、并发一次、取消一次；客户端两种依赖方式各构建一次。

## 6. 首轮独审意见处理对照

| 编号 | 处理 |
|---|---|
| H1 提权 | D9、D12：固定配置不收 `--config`、sudo 只收提交号、root 自取源码、运行器不进 docker 组、明确等价 root、日志防符号链接、bundle 移出 |
| H2 配方与树摘要 | D4 源码配方 + CLI 强制约束，`services.json` 移到 `deploy/site`；D3 改用 `SnapshotSHA256` |
| H3 回滚目标 | D1 不可变发行目录与镜像标签；D10 state 来源字段与清单校验 |
| H4 迁移 | D2 改 N-1 兼容、真实回退测试、Flyway 配置、多库记录、数据目录格式版本 |
| H5 服务映射 | D7 代码内镜像 → 服务表与交叉测试、broker 规则、operator 与 collab 处理、编排摘要扩大、只换容器先限 edge 与 platform |
| H6 依赖完整性 | D5 按生态规则、Maven 锁与 pip 哈希、工作流拒绝新增依赖；§4 改写 |
| M1 | D6 指纹由配方声明生成、产物摘要双保险、provenance 运行时挂载 |
| M2 | D3 不依赖骨架；D6 伪服务；D9 补全安装参数；D10 首装复用 `install()`；§1 runtime 首装不在范围 |
| M3 | D5 套件独立信任 |
| M4 | D5 按锁摘要整套、生态范围、`caFile`；D9 `registry` 单列、单源说明 |
| M5 | D11 `flock` 与锁范围 |
| M6 | D7 部署冒烟与超时；D8 首装失败语义；D11 `phase` |
| M7 | D13 两段式、Node 摘要、文件类依赖源、系统工具基线、保留人工签名核对、客户端信任配置项 |
| M8 | §5 拆分为 2a–2e，sudo 入口在 2a 定型；D10 CLI 升级规则 |
| L1 | D7 编排摘要排除文档与测试；bootstrap 归伪服务 |
| L2 | D7 写发行标记与 `versions.txt` |
| L3 | §1 多节点不在范围，要求 N-1 协议兼容 |
| L4 | D11 脱敏与应急材料 |
| L5 | D10 adopt 不伪造信任 |
| L6 | D2 `--backup-db` 多库与一致性边界 |
| L7 | D3 套件镜像按 digest 跳过 load |
| L8 | D13 `auriga-client.cmd` 为主、`auriga.exe` 薄壳与分发说明 |

### 二审意见处理对照

| 编号 | 处理 |
|---|---|
| N1 切换判据 | D6 改按装配摘要（产物 + 基础镜像 + 系统输入 + Dockerfile 目标段与暂存清单），收据写明触发项 |
| N2 版本含义 | D2 按提交声明 `schemaCompatFloor`、PR 门禁对 main 头做回退测试；回滚与降级前检查 floor |
| N3 降级 | D12 sudo 部署只允许快进，降级走 `rollback` 或 root 人工 `--allow-downgrade` |
| N4 sudoers | D12 sudoers 用标准通配，参数白名单由 CLI 按 `SUDO_UID` 执行 |
| N5 锁 | D11 所有改动站点的命令统一 `flock`，2a 完成 |
| N6 强制范围与快照 | D4 CLI 直接 `docker build` 装配、如实写明强制范围；D3 抽出目录快照摘要函数 |
| N7 首装钉定与 upgrade | D10 首装用套件指纹钉定否则要求 `trust add-*`；`upgrade` 产生 history 并接入序号目录 |
| 二审 L1 | D2 4 个 Flyway 入口逐个配置与单测 |
| 二审 L2 | D7 明确 edge 重建集合，跳过 `panel-prepare` |
| 二审 L3 | D9 `deps.cache` 权限要求 |
| 二审 L4 | D9 Windows ACL 校验；D2 无 floor 的旧发行拒绝自动回滚 |
| 二审 L5 | D8 2b 前自动回滚只在数据未被触碰时执行；§5 2a 带版本读取 |
| 二审 L6 | D3 `trustedSigners`；D6 `bundle` 同样运行时提供 provenance |

## 7. 交给实施 PR 处理的遗留项（三审）

| 编号 | 内容 | 归属 |
|---|---|---|
| P1 | 回退测试门禁同时用父提交与 `schemaCompatFloor` 对应提交的镜像各跑一次；合并前必须对最新 main 重测（合并队列下第二个 PR 也要测到真实父提交） | 2b |
| P2 | “数据未被新版本触碰时允许自动回滚”作为长期规则保留，不只在 2b 合入前有效 | 2a |
| P3 | 判定 sudo 调用不能只看 `SUDO_UID`：`sudo -i` 的管理员也会带上它。改为同时核对调用者是否为配置里登记的运行器账户，或让人工 root 显式加 `--operator` 跳过白名单 | 2a |
| P4 | CLI 直接 `docker build` 时拒绝 Dockerfile 中的 `# syntax=` 指令，防止拉取外部前端 | 2a |
| P5 | edge 只换容器时 `network` 新镜像与 broker 旧镜像的组合要有测试；未测通前 broker 产物变化一律走完整启动 | 2d |
| P6 | 签名包（`bundle`）同样写入 `schemaCompatFloor` 与格式上限声明，否则 `upgrade` 产生的历史只能人工回滚 | 2b |
