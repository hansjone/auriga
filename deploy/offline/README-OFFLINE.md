# Auriga 现场离线安装

包适用于 Linux amd64。现场预装 Docker Engine 和 Compose 2.29+，准备至少 4 GiB 可用内存与 20 GiB 可用磁盘。包不含 Docker 安装程序。

正式附件只在 Forgejo Release 用浏览器手动下载。未分卷时只部署需安装 tar 和 SHA256SUMS；改代码再下源码归档与累积 devkit，见根 DEPLOY.md / MAINTAIN.md。包自带静态 CLI 和公钥，`./auriga verify <文件或目录>` 全离线校验摘要与签名；必须使用独立批准公钥，例如 `--verify-key /secure/release-public.pem` 固定。分卷保留全部 `.part001…`、`.parts.json`、`.parts.json.sig`，用同页引导程序 `./auriga unpack <安装包.tar.part001> --out <全新目录>` 逐卷校验和读取，无须拼大 tar。未分卷 tar 也支持同一命令。

新基线发行提供完整 devkit。同一基线的后续版本提供精确上版可用的 delta，同页仍可下载完整套件。Windows 客户端开发需要签名源码和独立 Windows 输入套件。DSH 上游源码归档、许可证及来源清单供只读参考。Windows 与 DSH 的附件选择、整包摘要及分卷说明见同页 `DOWNLOADS.txt`。

包含 dedicated 运行配方的新 Linux devkit 还必须携带 `build.runtimeTooling` 指定的原始 pnpm 11.28.5 TGZ，存入 `cache/runtime-tooling.tgz` 或对应累积 blob。它复用 installer 的同一固定版本/SHA512，并另以发行索引 SHA256、套件签名和依赖锁键登记；缺失或篡改明确失败。现场仍先运行 `./auriga devkit verify`，real-dsh checked context 会从对应版本自动定位此输入，按固定配方离线安装至只读 `/opt/auriga-tools/pnpm`。维护者输入准备、原始 TGZ 校验与镜像命令见 [real-dsh 配方](../compose/real-dsh/README.md)。旧已签套件不修改，使用与新源码锁匹配的新套件。

完整 DSH 冷构建还使用 `build.runtimeDependencies` 的四项固定 artifact（manifest/corepack/webCache/npmCache）及 `build.tools.runtime` 固定 Node 工具镜像。它们沿既有 devkit 签名、依赖 identity 和累积 blob 收录；缓存缺失或源码锁不符时不在线补包。DSH 权威入口仍是公开 `build-real-dsh.mjs`，默认从匹配已签套件离线构建，项目 broker 按当前源码重新生成；具体准备和使用见上述配方。原 source-only 的签名、版本选择和服务端五镜像装配流程不变。

已核验的发行引导程序可直接 `sudo ./auriga install --package <tar或part001> --address 192.0.2.10 --verify-key /secure/release-public.pem`。升级同样接受分卷并沿用固定公钥；其他维护命令请在解包安装目录使用。

> **⚠ 安装完成后立即按源码归档里的 `docs/site/keep-list.zh.md`（必备参数与保管清单）导出并离线保管：应急口令和 TOTP、资源公钥根、离线降级私钥、站点构建私钥和备份。** 该文档也列出安装前必须自己准备的输入。常见报错见 `docs/site/troubleshooting.zh.md`。

首次安装已有签名 Docker DSH 运行时，给同一命令同时加 `--runtime-bundle /media/runtime/release-1 --runtime-root /secure/operator-approved/root.pb --runtime-image '127.0.0.1:15000/auriga/dsh@sha256:<原manifest摘要>'`。三个参数完整提供，根由维护者独立批准，镜像先按[原运行时流程](../../docs/dev/runtime-release.md)导入精确 registry。安装器在已核外层发行信任后使用原 `runtime-release verify`，固定公开快照到 `<数据目录>.oneclick/runtime-inputs` 并将同一输入传给原 `init`；原资源密钥和对象目录继续自动装配。运行时路径要求已签名外层包，不能用 `--allow-unsigned` 绕过。

中断重跑可以省略三个参数复用固定快照；显式重传必须是相同路径、image 和字节。不同请求或固定配置漂移拒绝，不换根或清现场。原 `init` 因残缺目录拒绝时保留诊断，不覆盖材料。旧无 runtime 首装记录的安装不能通过此参数补根；升级仍用原受保护流程。此安装接线不代表镜像已导入、Node 已入网、共享 DSH 或资源市场已通过正常入口验收。

1. 从独立受信渠道取得外层 tar 的 SHA-256，核对后解包到 root 管理的物理目录。包内 `SHA256SUMS` 覆盖全部文件。必须独立核准发行方 Ed25519 SPKI PEM 公钥或 SHA256(SPKI DER) 指纹。
2. 在私密交互 TTY 执行 `sudo ./auriga install --address 192.0.2.10 --verify-key /secure/release-public.pem`。数据默认放 `/srv/auriga/auriga-data`。自定义父目录加 `--data /srv/auriga`。必须加 `--verify-key /secure/release-public.pem`，或从独立批准的Release正文核对的 `--trust-fingerprint <64位指纹>`。全程无下载、无构建。
3. 打开输出的管理台地址，导入 TOTP，用应急账号登录后邀请成员。应急凭据仅向 TTY 显示，重定向时按提示到私密 TTY 查看。

IP 模式使用 HTTP 8080/8084，沿用明确允许的可信团队弱隔离配置。域名模式用 `--domain example.com`，事先准备 `portal.example.com`、`admin.example.com` 与 `*.i.example.com` 到现场服务器的 DNS，默认同样使用 HTTP 8080/8084。
已有受信 HTTPS/mTLS 代理时用 `--proxy /secure/access.json` 提供批准的 proxy access 配置。代理证书、受信 peer 和浏览器 HTTPS 入口由现场代理管理者配置。doctor 的自动登录探针当前支持 direct HTTP，代理模式会明确报告待浏览器复核。

出现问题执行 `sudo ./auriga doctor`，把纯文本结果交给现场 AI。`status`、`logs`、`stop` 使用相同 `--data`。doctor 会执行真实密码/TOTP登录，可能消耗当前30秒TOTP窗口，人工登录请等待下一个验证码。

资源上传/发行使用独立的 Ed25519 发行、撤销私钥和 UID10001 独占对象目录。公钥根在 `<数据目录>/resource-keys/resource-roots.json`。把此文件原字节送回可信桌面构建机，使用 `node clients/installer/scripts/build.mjs --resource-trust-json <文件> ...`，或填 Installer workflow 的 `resource_trust_json`，核对 `resourceTrustSha256` 后再把安装包拷回现场。每个现场的根独立，通用桌面包需要按现场根重建后才能验证该现场资源。

`resource-keys/offline/downgrade.pk8` 由 root 独占，导出到受控离线介质保存，未挂载到在线服务。完整备份也包含私钥。可选 `optional/desktop.tar`、`optional/seeds.tar` 仅保存已核摘要的发行材料，按其发行说明经正常管理台导入。

维护命令如下，冷备份和升级会停止栈及已核实归属的实例。

```sh
sudo ./auriga backup
sudo ./auriga upgrade /media/auriga-offline-new.tar
sudo ./auriga doctor
```

备份保存在数据父目录的 `auriga-backups/`，权限0700，归档0600。升级先校验新包、加载镜像、冷备份，再更新发行和数据库。启动/健康失败会恢复升级前完整数据库、原私钥和代码，失败现场以 `.failed-时间` 保留。升级进程中断后，再次执行相同 upgrade 会按升级记录恢复原版。install、upgrade、backup、stop、deploy、rollback 共用一把 `flock` 站点锁（`<数据目录>.oneclick-lock`，进程退出或被杀时自动释放），同时只能有一个在跑，第二个直接失败并说明正在进行的操作；旧版本留下的同名锁目录需确认没有 auriga 进程后手工删除。不要删除备份和 `.upgrade.json`。

默认 Docker 内部网段沿用配方的 `192.0.2.0/26` 与 `192.0.2.128/26`。冲突时保留错误与已有配置，交部署维护者选择不重叠网段并走原有手工路径。安装器不改宿主路由、DNS或防火墙。

verify/unpack/install 只用包内公钥时打印指纹与“仅完整性，未核验发行身份”并非零退出。缺签名/公钥默认拒绝，只有显式 --allow-unsigned 应急例外会警告并记入安装state、doctor持续FAIL；未知签名者/错误签名不得绕过。服务器固定发行与已登记站点公钥，升级不接受包内公钥替换。站点构建必须签名，keygen site / trust add-site-key 与轮换流程见MAINTAIN.md。

## 站点配置与依赖缓存

统一现场部署（设计见源码 `docs/dev/site-deploy.zh.md`）的站点配置和依赖缓存，`deploy`、`rollback`、`history` 见下一节。

- **站点配置** 固定读 `/etc/auriga/site.json`，字段按设计 D9，`schemaVersion` 为 1，未知字段、重复键直接报错。文件必须 root 所有、权限 0600 或 0644，祖先目录不可被组或其他用户写。经 sudo 调用时不接受 `--config` 与 `--source`；root 直接运行时可用 `--config` 指向同样满足权限要求的文件。依赖源只允许 `https://` 与 `file://`，不能内嵌凭据，不能带查询、片段，也不能含 `,` 或 `|`（GOPROXY 列表分隔符，多个 Go 源写成数组元素）；只有 `deps.sources.go` 是数组，`npm`、`maven`、`files` 各一个地址；`deps.registry` 只写主机名[:端口]。
- **依赖缓存** `deps.cache` 必须 root 所有、组和其他用户不可写。布局为 `inputs/<生态>/<锁摘要>/`（生态 go、web、java、system；锁摘要是目录内 `source-locks.sha256` 的 SHA-256）和 `images/<用途>/<digest>/image.json`（可带 `image.tar`）。依赖输入按锁摘要整套选取：可写缓存 → `deps.kits` → 依赖源。
- **套件** 有两种布局，都直接可用、不需要转换：
  - 解包后的**发行开发套件**（单版本 `devkit.json`，或累积 `devkit-catalog.json`）。CLI 按当前源码各生态的锁摘要，在套件各版本的 `inputs.tar` 里找 `<生态>/source-locks.sha256` 匹配的输入；system 的锁清单由该版本 `recipes.tar` 里的 `system-inputs.sh` 和版本锁清单记录的 `admin-bootstrap-requirements.txt` 摘要合成。工具镜像、基础镜像取最新版本的镜像记录。
  - 与缓存相同的布局，根目录另有覆盖全部文件的 `SHA256SUMS` 与 `SHA256SUMS.sig`。
  - 每项必须恰好带一种独立信任：`trustFingerprint`（独立核对过的发行公钥指纹，`SHA256:<64 位十六进制>`），或 `"trustState": true`（只用已安装站点 state 里钉定的发行/站点公钥验签，需要配置 `server.data`）。两者都没有、或两者都写，配置校验时就拒绝。套件自带的 `release-public.pem` 只是公钥载体，不能自证。
  - `trustState` 是本 CLI 新增的字段，设计 D9 只写了“或引用 state 已钉定的发行钥”。
  - 套件目录与 `deps.cache` 同为保护边界：必须 root 所有、组和其他用户不可写，祖先目录同样受信，否则该套件不可用。
  - 清单文件和用到的数据文件都必须受已验签的 `SHA256SUMS` 覆盖。签名清单、公钥和套件清单只读一次到内存，核验与解析用同一份字节；`inputs.tar`、`recipes.tar` 整档核对摘要后才取锁清单（所以打开发行开发套件会顺序读完各版本的 `inputs.tar`）；`deps fetch` 用套件里的镜像归档时先复制到缓存暂存目录，在副本上核对后再 `docker load`。
- **导入发行开发套件**：`deps import <套件目录或归档> --trust-fingerprint …`（或 `--trust-state`）。发行开发套件必须带独立信任，不接受人工未签名导入。CLI 按当前源码（`source.dir`，或 root 直接运行时的 `--source`）的锁摘要取出各生态输入，读的同时核对归档摘要，写进 `inputs/<生态>/<锁摘要>/`，再导入最新版本的镜像（`image.tar` 加镜像 ID）。每个条目的 `auriga-origin.json` 记录套件路径、版本和签名指纹。导入源（目录或归档文件）同样必须 root 所有、组和其他用户不可写；U 盘等介质请先复制到这样的目录再导入。
- **补依赖规则**：只有 Go、Web 能用 `sudo ./auriga deps fetch` 从配置的依赖源自动准备。`allowPublic=false` 时 Go 不回落 `proxy.golang.org`，校验库只经配置的代理，取不到就失败；Web 拒绝没有 `integrity` 的 lock 条目。Java、系统包、Python、基础镜像与工具镜像只能 `sudo ./auriga deps import <目录或归档>` 或套件提供。`acceptNewDeps=false`（且未加 `--accept-new-deps`）时 `deps fetch` 只打印新增依赖清单并以退出码 1 结束。新拉的依赖写回缓存，条目内 `site-dependencies.json` 列出新增项及 SHA-256，`auriga-origin.json` 记录来源。`deps.caFile` 必须 root 所有、组和其他用户不可写，只读挂进准备依赖的容器。

```sh
sudo ./auriga deps status            # 逐生态列出命中位置（缓存/哪个套件）或缺失及补法；--json 输出机器可读结果
sudo ./auriga deps fetch             # 按规则准备缺失且允许自动补的生态
sudo ./auriga deps import /media/auriga-deps.tar --trust-fingerprint SHA256:<64位指纹>
sudo ./auriga deps import /srv/auriga/kits/auriga-devkit-0.2.0 --trust-state   # 直接导入解包后的发行开发套件
```

## 本机部署（deploy、rollback、history）

当前版本只支持 **root 直接运行**（经 sudo 调用的入口、参数白名单与快进限制在下一版提供，现在经 sudo 调用会被拒绝）。站点配置需要 `server.data`；首次安装还需要 `server.install`（`address`、`domain`、`proxy` 三选一）。

```sh
./auriga deploy --dry-run                 # 只打印计划：构建哪些镜像、切换方式、缺的依赖、镜像来源、迁移与格式版本
./auriga deploy <40 位提交号>              # 从 source.git 取该提交构建并部署
./auriga deploy                           # 用 source.dir（或 source.git 第一个 allowedRefs 的最新提交）
./auriga deploy --services edge,platform  # 只构建这两个镜像，其余沿用当前；仍完整启动
./auriga history                          # 部署历史，* 为当前
./auriga rollback [--to <序号>]
```

- **源码**：给了提交号时，root 把 `source.git.url` 的 `allowedRefs` fetch 到数据目录同级的裸仓库 `auriga-source.git`，提交必须在某个 `allowedRefs` 上（是其祖先或本身），否则拒绝；再用 `git archive` 导出提交的树，不在任何运行器目录里执行 git，也不读 root 的系统/全局 git 配置和继承的 `GIT_*` 环境变量。没有提交号时用 `source.dir`，root 直接运行时也可用 `--source <目录>`（两者都须 root 所有、组和其他用户不可写），此时记录工作区是否有未提交改动。收据记录提交号和快照摘要 `SnapshotSHA256`。
- **签名提交**：提交签名校验在下一版实现。现在只要 `source.git` 配置了 `requireSignedCommits: true` 或非空的 `trustedSigners`，deploy 就直接拒绝（退出码 1），不会在不校验签名的情况下部署。
- **配方**：一律使用源码快照里的 `deploy/nas/offline-build`。`deploy-recipe.json` 声明各镜像的暂存清单、工具镜像与基础镜像（以及 postgres、nats、registry）的固定 digest、CLI 能力版本；能力版本与本 CLI 不符时失败并提示先升级 CLI，deploy 不替换自身。CLI 自己按清单暂存产物，`docker build --network=none --pull=false` 装配，构建参数由 CLI 固定；Dockerfile 含 `syntax`/`escape` 解析器指令时拒绝（任意位置，包括 BOM、`//` 前缀、任意空白与 JSON 形式等变体），`FROM` 只能是固定 digest 的镜像、配方的 `${<服务>_BASE}`（只能用在同名阶段）或前面声明的阶段，拒绝 `ADD <URL>`、引用外部镜像的 `COPY --from`/`RUN --mount from=`、`ONBUILD` 与 heredoc；构建前逐字节比较源码锁清单与依赖输入的 `source-locks.sha256`。`bundle` 与 `assemble-image.sh` 不变。
- **依赖与镜像**：依赖输入沿用 `deps` 的解析（缓存 → 套件），使用前完整校验；缺失时只有 Go、Web 且 `acceptNewDeps`（或 `--accept-new-deps`）才自动补，否则退出码 1 并列出缺什么。镜像按配方 digest 依次找：缓存或套件记录（本机已有就不 load）→ 本机 Docker → `deps.registry` 按 digest 拉取。
- **发行目录与标签**：每次部署写 `<数据目录的父目录>/auriga-releases/<序号>/`（`deploy-nas/`、`bin/auriga-nas-bootstrap`、`bin/runtime-release`、`release.json`、本地 `SHA256SUMS`），五个装配镜像加打 `auriga/<服务>:r<序号>`；发行目录 `SHA256SUMS` 的摘要记进 state，回滚前核对。state 的 `current` 原子切换。保留 `server.keepHistory` 份成功的发行（默认 3，失败记录不占名额），淘汰前确认没有容器在用对应镜像。装配摘要（产物、系统输入、基础镜像、Dockerfile、暂存清单）与当前一致的镜像不重建、不切换。每次都完整启动（render → preflight → start）。
- **首次安装**：没有 state 时复用 install 的步骤，只是镜像来自本机构建。配置了带 `trustFingerprint` 的套件时用其发行公钥钉定，否则 state 标记未钉定（`auriga status` 会提示），之后第一次签名包 upgrade 前须先 `trust add-*`。state 记录 `source: local-deploy`。
- **回滚**：切换失败时，先停掉新版本里会写库或改写数据目录的服务（platform、collab、collab-renew、edge 系列、agent、bifrost）并确认已停止，再读迁移版本（各库各 schema 的 `flyway_schema_history`、`auriga_common_schema_history`）与数据目录 `format-versions.json`；与切换前完全相同、且上一份发行是成功记录时才自动回滚（退出码 2）；停不下来、读不到或有变化时不回滚（退出码 3，会写数据的新容器保持停止）；回滚本身失败不重试（退出码 3）。首装失败退出码 1，保留现场。
- **手动 rollback（2b 之前的规则与风险）**：`rollback` 只在当前迁移与格式版本与目标发行运行时记录的完全相同才执行，否则退出码 3；跨越迁移的回滚需要 `schemaCompatFloor` 与格式上限声明（2b 生成），现在一律交人工。注意：迁移版本没变不等于数据没变——新版本运行期间可能已写入旧代码不认识的行数据或取值，回滚后旧代码可能读错或报错；新版本运行越久风险越大。执行前请先备份，并确认这段时间的业务改动。2b 上线后改为按 floor 与格式上限判断。
- **日志与收据**：每次 deploy/rollback 写 `<logs.dir>/<时间>-<命令>.log` 与同名 `.json` 收据（默认 `/var/log/auriga`，必须 root 所有、组和其他用户不可写），内容脱敏。`--json` 让标准输出只打印收据。退出码：0 成功；1 未切换就失败；2 切换失败已自动回滚；3 需要人工。
- 已用签名包 `install` 的站点暂不能 `deploy`（接入新布局在下一版），deploy 安装的站点也暂不能用签名包 `upgrade`。
