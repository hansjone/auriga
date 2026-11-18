# 现场维护

成员、邀请、角色、配额、身份源、模型供应商、凭据、入口和运维策略先通过管理台配置。Agent 行为优先使用资源市场的插件、技能和受控配置。超出这些能力后再改源码。禁止直接修改运行容器或部署服务器上的程序文件。

## 浏览器下载后离线开发

联网电脑只用浏览器，在批准标签的 Forgejo Release 下载 `DOWNLOADS.txt`、`SHA256SUMS`、源码归档 `auriga-source-<版本>.tar`、覆盖该版本的完整 `auriga-devkit-<版本>.tar`。分卷时下载该前缀全部分卷及清单/签名，再下载同页 `auriga` 引导程序。后续步骤可在无 Git、无外网、无 Forgejo 连接的现场完成。

服务端命令只在 Linux amd64 运行，构建机预装 Docker Engine/Compose 2.29+，不要求本地语言工具链。任意电脑（包括 Windows）可用于浏览器下载、编辑和拷贝文件，Windows 不运行 auriga。另附 auriga-source 归档含标签/提交/逐文件与锁摘要的 auriga-source.json、公钥、签名和 Linux 静态 bin/auriga-cli。Forgejo自动源码zip/tar不含这些发行元数据与CLI，应选另附源码包。

```sh
cd /media/auriga-release
sha256sum --ignore-missing -c SHA256SUMS
mkdir /opt/auriga-source-0.2.0
tar -xf auriga-source-0.2.0.tar -C /opt/auriga-source-0.2.0
cd /opt/auriga-source-0.2.0
./auriga verify . --trust-fingerprint "$ISSUER_FP"
./auriga unpack /media/auriga-release/auriga-devkit-0.2.0.tar --out /opt/auriga-devkit --trust-fingerprint "$ISSUER_FP"
./auriga devkit verify /opt/auriga-devkit
```

分卷时把 tar 输入替换为首卷；源码本身分卷时先用发行引导程序 `unpack`。归档无 `.git`，现场命令读取发行清单，不调用 Git。发行公钥指纹从独立批准的 Release 正文/组织文档核对，设置 `$ISSUER_FP`。verify/unpack/install 必须传 `--trust-fingerprint` 或 `--verify-key /secure/release-public.pem`；只用包内公钥打印指纹和“仅完整性，未核验发行身份”并非零退出。源码整体 `verify` 在修改前执行，修改后用补丁、快照和 `devkit verify` 检查。

`devkit verify` 在源码根写 `.auriga-devkit.json` 绑定套件路径。移动套件后重新运行。指定版本检查覆盖：`./auriga devkit verify /opt/auriga-devkit --version 0.2.0`；当前源码版本还校验每个依赖锁和上游提交。锁不匹配列出新增、缺失或不同文件及应下载版本；缺依赖绝不静默联网。

## 修改、导出与离线构建

### 拷到 Linux 后验证、改代码和构建

在任意电脑上下载附件，把完整源码和套件拷到现场 Linux 服务器或独立 Linux 构建机。先按上面的指纹校验、解包并绑定 devkit，再改代码、按模块规范测试。也可在其他电脑编辑后拷回 Linux；changes export/apply、密钥生成、构建和安装/升级均在 Linux 执行。建议开发机至少 16GiB 内存、SSD，以及容纳完整套件、展开文件和产物的磁盘空间。

```sh
cd /opt/auriga-source
# 拷贝若使外层 auriga 变 CRLF 或丢执行位，直接使用 Linux ELF 入口：
chmod 0755 bin/auriga-cli
./bin/auriga-cli devkit verify /opt/auriga-kit
./bin/auriga-cli doctor --build
./bin/auriga-cli keygen site --out /secure/site-build-key
# 把该公钥与独立核对的指纹送到服务器登记，具体见后面的信任流程。
./bin/auriga-cli bundle --build --offline --out /exports/auriga-site.tar --sign-key /secure/site-build-key/site-private.pem
```

构建快照对已知 UTF-8 文本规范化 CRLF→LF、去除 shell BOM，并按签名清单恢复执行位；新增 .sh 按 0755。比较摘要不受纯换行变化影响；原始源码、签名清单不被改写，真实锁改动仍拒绝。UTF-16、单独 CR、大小写冲突、保留名、尾点/空格明确拒绝并指出文件。通用 .cmd/.bat/.ps1 文本副本保留其 CRLF，不代表支持在 Windows 运行 auriga。

`devkit verify` 仅检查签名、依赖和源码匹配，不要求 Docker 可用或内存足够；`doctor --build` 单独检查 Linux Docker/Compose、cgroup 配额及内存。实际构建保留强制门禁：默认每个工具容器 4096MiB/2CPU，并预留 1024MiB 给运行服务，实际 MemAvailable 不足 5120MiB 时拒绝；用 `--memory-mib`/`--cpus` 显式调整。镜像层装配复用 Engine 的 BuildKit，不等于整个 Engine 的总 cgroup 隔离，严格总资源要求请用独立 Linux 构建机。

Linux 服务器允许低峰构建。显式 `sudo ./bin/auriga-cli bundle --build --offline --out /exports/auriga-site.tar --sign-key /srv/auriga/auriga-data.oneclick/site-build-key/site-private.pem --install --data /srv/auriga` 会在构建成功后调用原 upgrade：先冷备份数据/密钥/卷，再升级，失败回滚；仍只接受 state 固定的公钥。

离线 Linux 前置由 IT 使用受信介质预装：匹配发行版/架构的 Docker Engine、containerd、Docker CLI、buildx、Compose 插件和系统依赖闭包，不能只带客户端。默认 devkit 不再分发操作系统或 Docker 安装器；实际体积与许可证通知以 IT 介质清单为准。

无需 Git 的默认路径：修改源码，按模块规范测试，再构建。支持新增、修改和删除；对比归档基线摘要判断现场改动。内置忽略规则排除 `.env`、`.pem/.key`、部署数据、`node_modules/target/dist` 等缓存、根 `bin/` 及标准安装包输出；不解释全部 `.gitignore` 规则，其他普通文件按源码收录。凭据和额外产物放源码之外，导出前人工检查准备交付的代码。

```sh
./auriga changes export --out /exports/auriga-changes-0.2.0-site.3.tar
./auriga bundle --build --offline --version 0.2.0-site.3 --out /exports/auriga-offline-0.2.0-site.3.tar --sign-key /secure/site-build-key/site-private.pem
```

补丁包含基线版本、提交、改动前后摘要和新文件内容，是现场代码备份，不是官方签名发行。省略 `--version` 自动加基于源码摘要的 `-site.<摘要>`；上游版本、提交、是否有改动、源码摘要和时间写入包，doctor 与管理台“关于”可见。无 Git 分支显示 `site/archive`，提交号保留上游提交，现场差异由摘要标识。已知文件保留发行时执行模式；需要改变执行方式时改脚本内容或配方，而非仅 chmod。

`--offline` 只读本地套件。工具容器 `--network none`、`--pull never`，镜像装配禁止拉取，缺依赖保留工具具体报错。修改锁或 Dockerfile 需维护者补齐新依赖、重出套件。保留套件根摘要、签名、`devkit-catalog.json` 和 `blobs/` 原字节。

将产物及摘要送入服务器，在安装目录执行 `sudo ./auriga upgrade /media/auriga-offline-0.2.0-site.3.tar`，然后 doctor、正常页面登录与资源安装实测。升级先冷备份数据和已确认归属的 local named volumes；保留上一安装包、私钥备份和现场补丁。

## 跟进上游：不用 Git

1. 在旧源码目录 `changes export`，补丁放源码外并备份。
2. 浏览器下载新源码与对应完整 devkit，或同基线逐版增量，拷入现场。新源码解到全新目录，先 `verify .`。
3. 在新源码目录应用补丁、绑定依赖、测试、构建：

```sh
cd /opt/auriga-source-0.3.0
./auriga changes apply /media/auriga-changes-0.2.0-site.3.tar
./auriga devkit verify /opt/auriga-devkit
./auriga bundle --build --offline --version 0.3.0-site.1 --out /exports/auriga-offline-0.3.0-site.1.tar --sign-key /secure/site-build-key/site-private.pem
```

`changes apply` 预检全部路径和改前摘要；上游与现场改了同一文件则列冲突并在写入前拒绝，不猜测合并。保留旧目录，对照旧代码在新源码人工解决，再导出补丁。重复应用相同补丁无害。基线元数据、Git 内部文件、凭据路径和符号链接不能通过补丁覆盖。

## 可选：本地 Git 管理

有 Git 的现场可在已验证源码目录离线初始化，不设远端：

```sh
git init
git add .
git commit -m 'import approved upstream 0.2.0'
git tag upstream-0.2.0
git switch -c site/local
# 修改、测试后 git add 和 git commit 保存现场改动
```

Git 仅用于回退和审查；构建仍以 `auriga-source.json` 为上游基线，对当前文件比摘要，已提交改动同样标记 `siteChanges=true`。不修改或重建官方基线。跟进上游可用上述 `changes export/apply`，也可把新归档导入本地上游分支形成普通提交，再在现场分支 `git merge upstream-0.3.0`，解决冲突后使用新归档原始元数据及 CLI，重新绑定套件。导入须完整替换上游源码、正确处理删除、保留现场 `.git`，不提交依赖绑定、凭据或缓存。全部历史仅在本地，不需 git clone/fetch 访问 Forgejo。

## 累积套件和增量

完整 devkit 从基线到当前保存各发行版锁清单、依赖、工具和基础镜像。相同字节按 SHA-256 共用 `blobs/<摘要>`，不同版本并存；源码归档单独下载。构建临时展开当前版本依赖，用完清理。发行清单与 `devkit-catalog.json` 列支持版本及各版锁摘要，不能用“最新”推断跨基线兼容性。

已有精确上版套件时：

```sh
./auriga devkit merge /media/auriga-devkit-0.2.1-delta.tar --kit /opt/auriga-devkit
./auriga devkit verify /opt/auriga-devkit
```

增量支持首卷。合并固定旧套件公钥，校验精确前置清单、全部新增块和目标完整清单，在同盘临时目录准备后替换；重复执行无害。合并与构建串行进行。缺前置时逐版合并或下载最新完整套件。若进程在目录替换时中断，保留同盘 `.auriga-devkit-merge-*` 中的 `previous` 与 `.merge-lock`，确认进程退出后由维护者恢复，不删除唯一有效副本。

默认累计阈值为 64GiB（唯一依赖总字节）。跨大版本、超阈值或显式收拢时开新基线，下载新完整套件到新目录。每次新开基线只发布完整套件，同基线后续版本才有增量。单版超过阈值时须调整阈值。旧基线仍可从原 Forgejo Release 下载。

## 维护者：准备和发布附件

### 1. 固定标签与输入

主控在批准的 Windows x64 与 Linux amd64 维护机上使用同一干净、独审通过的标签。先同步精确标签到 Forgejo。取得目标提交对应的 NAS tar、Forgejo 构建输入、当前配方和 digest 固定的镜像。索引格式见 `deploy/offline/release.example.json`。把实际索引、Ed25519 PKCS8 PEM 发行私钥及制品放在源码外。`build.recipes` / `build.inputs` 沿用 `deploy/nas/offline-build` 的配方及锁定的语言输入。

DSH 源码使用 `deploy/offline/dsh-source-pin.json` 固定的官方标签提交归档。先将原始归档放入批准的 Forgejo 制品区，Linux 从该区下载到本地，或将该 HTTPS 附件 URL 直接交给 `--dsh-source`。当前归档可用，发布其原字节并附带原始 LICENSE 和来源清单。更新 DSH 时同步复核 npm 锁、官方标签提交与归档 SHA256。若上游无法提供对应提交归档，先审批新材料方案，保留 npm 锁对应闭包的原始 tarball、许可证与完整性证据。

### 2. Windows 先准备、签名并转运

联网 Windows x64 维护机在标签根目录使用 Node 22.19.0 准备输入。以下 `$kitName` 取 `prepare.mjs` 输出的目录名。通过受控密钥介质在 Windows 签名，使其公钥与 Linux 发版公钥相同。传输材料只有 tar 和独立核对的整包 SHA256。

```powershell
$kitParent = 'D:\exports\client-kits'
New-Item -ItemType Directory -Force -Path $kitParent
node scripts/client-release/prepare.mjs $kitParent
$kitName = 'auriga-client-windows-<prepare 输出的锁 SHA256>'
$kit = Join-Path $kitParent $kitName
node scripts/client-release/sign-inputs.mjs --devkit $kit --key-file C:\secure\issuer\release-private.pem --version 0.2.0
$archive = "$kit.tar"
tar.exe -cf $archive -C $kitParent $kitName
(Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
```

通过批准的 Forgejo 大文件转运把该 tar 放入 Linux 本地制品目录，保留文件名，核对接收回执中的长度与 SHA256。签发版本随 Windows 输入保留。若复用旧套件，当前标签的 Windows 锁与旧套件签名公钥均须匹配。

### 3. Linux 生成发行目录

在新标签 checkout 中使用 Go 1.27.3，或设置 `AURIGA_GO_IMAGE` 为已批准的 Forgejo Go 工具镜像完整 digest。复用 checkout 时先重编 `bin/auriga-cli`，避免加载上一版的缓存二进制。Docker 需支持 `save --platform`（API 1.48+）。门禁需 Linux root、`dockerd`、`unshare` 及 `nsenter`，以及私有网络、mount 和 PID 命名空间权限。

```sh
git status --short
git rev-parse HEAD 'refs/tags/0.2.0^{commit}'
TMPDIR=/build/auriga-tmp sh deploy/offline/prepare-release.sh \
  --tag 0.2.0 --release /secure/release.json --outdir /exports/0.2.0 \
  --cache-dir /exports/devkit-cache --sign-key /secure/release-private.pem \
  --windows-client "$WINDOWS_TAR" --windows-client-sha256 "$WINDOWS_SHA256" \
  --dsh-source "$DSH_SOURCE" --attachment-limit 2147483648 --new-baseline --build
```

先创建命令所用 `TMPDIR`，设定 `WINDOWS_TAR` 为转运到本地的归档路径，`WINDOWS_SHA256` 为 Windows 回执值，`DSH_SOURCE` 为绝对本地路径或上述 HTTPS 附件地址。私有输入按批准主机设置 `AURIGA_BUNDLE_AUTH_HOST`，在进程环境提供 `AURIGA_BUNDLE_TOKEN`。镜像仓库使用维护机 Docker 登录。

Linux 在构建前流式核验 Windows tar、内部发行签名、依赖锁及每个输入，核对 DSH 提交归档与 npm 锁。发行命令一次生成 Linux 安装包、签名源码和 devkit、Windows 套件、DSH 源码及元数据，并用同一发行钥签全套摘要。超过站点限制或 4GiB-1 自动分卷。后续同基线发版，将 `--new-baseline` 换成 `--previous-devkit /exports/unpacked-devkit-0.2.0`。锁与批准输入未变可复用签名单版缓存。NAS 与可选材料按版刷新。

`unshare --net` 门禁使用空存储的独立 Docker 引擎，既有引擎的镜像缓存无法参与。门禁执行真正的 `bundle --build --offline`，工具容器使用 `--network none --pull never`。`release-manifest.json.offlineBuildGate` 记录精确提交、锁、源码/完整套件/清单/构建输出摘要及起止时间。`--build` 发行门禁构建出的安装包，省略该参数则发行批准的预建镜像包并仍执行完整门禁。

- QNAP 若在容器启动时报 `fchmodat2 AT_EMPTY_PATH`，先在本次维护环境验证受信且兼容宿主内核的 runc，或改用受支持的 Linux 维护机。R1 在私有 Docker 工具目录使用冻结 DinD 镜像的兼容 runc。保留工具摘要，NAS 主引擎继续使用其自身配置。
- 若 GNU tar 报 `Bad address`，先做目录提取与 `cp -a` 探针。R1 使用冻结的 Debian Node 基底中 GNU tar 1.34、cp、loader 和库，构建工具镜像重新核定 digest 后进入签名 devkit。兼容层制作见 [Forgejo 冷建指南](docs/dev/forgejo-only-build.zh.md)。
- 独立引擎就绪限 30 秒，单次探针限 2 秒。Docker 元数据读取限 2 分钟，重新导出核 config 与层摘要限 3 分钟，完整断网门禁限 2 小时。VFS 在慢盘容易超时。用足够空间的快盘作为 `TMPDIR`，预留展开套件、镜像和编译空间，先确认最大工具镜像导入和重新导出耗时。
- 门禁运行状态在短路径 `/run/auriga-gate-*`，大数据留在 `TMPDIR`。若仍报告 Unix socket 路径过长，检查兼容运行时是否另用长目录，以及独立容器各层是否都能看到对应绝对路径。
- Ctrl+C 与 SIGTERM 触发进程、容器和私有挂载的有界清理。若报清理失败，按错误中的目录查 daemon 日志，核实本次进程与挂载是否退出再处理。最终发行目录在门禁和清理全部结束后才出现。

### 4. Forgejo 草稿、核验与发布

默认使用公开 HTTPS，私有 CA 可通过 Linux 发布进程的 `SSL_CERT_FILE` 使用独立批准的 CA 文件。若 NAS 无法通过公开 HTTPS 路由到同一 Forgejo，将 `FORGEJO_INTERNAL_URL` 设置为批准的内网 HTTP 地址（私有或回环 IP，带端口），再加 `--internal-http "$FORGEJO_INTERNAL_URL"`。此选项仅用于受控直连网络，HTTP 上的令牌以明文传输，发布程序会绕开代理。两种路径均使用公开 `--server` 标识站点，附件回读拒绝跨站跳转。

```sh
publish_release() {
  sh deploy/offline/publish-forgejo.sh \
    --server https://forgejo.example.com --repo owner/auriga \
    --dir /exports/0.2.0 --verify-key /secure/release-public.pem "$@"
}
# HTTPS 预演只检查本地产物
publish_release
# 主控从批准的密钥渠道将 FORGEJO_TOKEN 注入进程环境后，上传草稿
publish_release --execute
```

令牌只由进程环境读取。维护终端关闭命令跟踪，通过密钥管理器或受控的标准输入读入环境，避免把令牌写进命令行、容器配置、文件或日志。内网方式在每次执行追加相同的 `--internal-http` 参数。上传前核签名、附件、断网门禁及 Forgejo 精确标签，重跑复用同名同字节附件，遇冲突停止。草稿回读校验全部附件后再执行以下发布命令。

```sh
publish_release --execute --publish
unset FORGEJO_TOKEN
```

发布命令在 Forgejo 回读同一个发行 ID、同一标签且确认已退出草稿后返回成功。写请求遇到重定向即报错。遇到写入失败或回读失败，先在 Forgejo 核对该标签的实际发行状态，再用同一发行目录和参数重试。若服务端已经发布，重试只逐字节核验现有附件。

从正常 Forgejo Release 页面核对版本、附件、发行公钥指纹及 Windows 引导摘要，并下载后按 `DEPLOY.md` 安装。Windows 与 DSH 如有分卷，按 `.parts.json` 顺序以二进制拼接并核对 `DOWNLOADS.txt` 所列完整归档摘要，再按各自用途使用。安装后的正常页面登录、邀请、资源上传/发布和桌面安装由主控实测。

## 发行信任与站点构建签名

发行源码、devkit（包括 `.auriga-devkit.json` 指向的目录）必须保留完整签名和公钥；剥掉签名不再降级为通过。devkit 的发行公钥必须与已验证的源码发行公钥匹配。移动套件只修改绑定路径不能改变信任根。发行换根须独立批准新版源码/完整套件。

首次 install 用 `--verify-key` 或从独立渠道核对的 `--trust-fingerprint` 固定发行公钥；公钥复制到 `<数据目录>.oneclick/trusted-keys/`，state 保存发行和站点公钥集合。upgrade、doctor、bundle --install 强制使用该集合，不能通过新包里的 release-public.pem 换根。发行流程将 SPKI DER SHA256 指纹同时写入 Forgejo Release 正文和 DOWNLOADS.txt；必须核对正文/组织记录，不能把待验证包自己显示的指纹当作独立批准。

首次安装还生成本服务器专用站点构建钥：`<数据目录>.oneclick/site-build-key/{site-private.pem,site-public.pem}`，目录0700、私钥0600，仅root持有（不挂给业务服务），公钥自动登记。本机服务器构建使用该私钥并 `--install`，仍先冷备份。所有 `bundle --build --offline` 必须传 `--sign-key`；这也适用于发行断网构建门禁（由发行私钥签名）。

独立 Linux 构建机生成自己的站点钥，私钥只留构建机，不复制服务器私钥：

```sh
./auriga keygen site --out /secure/site-build-key
# 独立核对命令打印的 SHA256(SPKI) 指纹，仅将 site-public.pem 拷到服务器。
```

在服务器私密终端登记，用构建机独立核对的 64 位小写指纹替换 `$SITE_FP`（需要 root）：

```sh
sudo ./auriga trust add-site-key /media/site-public.pem --confirm-fingerprint "$SITE_FP" --data /srv/auriga
```

回到 Linux 源码目录验证依赖并签名构建：

```sh
./auriga devkit verify /opt/auriga-kit
./auriga bundle --build --offline --sign-key /secure/site-build-key/site-private.pem --out /exports/auriga-site.tar
```

将产物拷入服务器后 `sudo ./auriga upgrade /media/auriga-site.tar --data /srv/auriga`。未登记站点钥、替换的自签发行钥或错误指纹均拒绝。任意电脑编辑的文件须拷到 Linux 后完成上述校验、构建和升级。

轮换：生成新目录的新站点钥 → 独立核对新指纹并add-site-key → 新钥签名构建并升级 → `sudo ./auriga trust remove-site-key <旧指纹> --data /srv/auriga`。旧钥仍签着当前安装包时拒绝移除，固定发行公钥不能用该命令移除；私钥受控离线备份后按组织流程销毁，不交给普通业务服务。

`--allow-unsigned` 仅用于 verify/unpack/install/upgrade 的显式应急缺签名例外，打印警告；不是未知签名者/错误签名的绕过。安装和升级记录永久告警，doctor持续FAIL；无签名首次安装不能自行接纳后来的包内发行根。源码基线和devkit不支持无签名例外。资源初始化残缺时保留原材料，从备份恢复；确认从未使用才由维护者人工隔离整个残缺目录重试。健康升级后的旧发行归档失败只警告，旧目录必须人工归档后才能再次升级。

## 客户端发布（Windows 开发机）

桌面客户端使用独立的 `scripts/client-release/auriga-client.cmd`，服务端 `./auriga` 仍只在 Linux 运行。当前客户端目标为 Windows x64；平台配方、引导程序布局和安装器命名集中于 `scripts/client-release/platform.mjs`，以后扩展 Linux 客户端时沿用来源与签名协议。

浏览器下载同版签名源码包和 Windows 输入独立附件 `auriga-client-windows-<锁SHA256>.tar`。Windows 套件包含 Node/npm/pnpm、store、Windows DSH 及其依赖，签名链沿用 #373 发行公钥。分卷先拼成完整归档。从独立批准的发行说明取两份完整归档与引导 `node.exe` 的 SHA-256。用 `Get-FileHash` 核对两份归档后解包到本地固定磁盘，再核对解包出来的 `node.exe`，通过后执行客户端命令。

```powershell
$sourceArchive = 'D:\media\auriga-source-0.2.0.tar'
$kitArchive = 'D:\media\auriga-client-windows-<锁SHA256>.tar'
$sourceSHA = '<独立批准的源码归档 SHA256>'
$kitSHA = '<独立批准的 Windows 套件归档 SHA256>'
$nodeSHA = '<独立批准的 node.exe SHA256>'
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $sourceArchive).Hash -ne $sourceSHA) { throw '源码归档摘要不符' }
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $kitArchive).Hash -ne $kitSHA) { throw 'Windows 套件归档摘要不符' }
$sourceRoot = 'D:\auriga-source-0.2.0'
$kitParent = 'D:\client-kits'
New-Item -ItemType Directory -Path $sourceRoot, $kitParent -ErrorAction Stop
tar.exe -xf $sourceArchive -C $sourceRoot
if ($LASTEXITCODE -ne 0) { throw '源码解包失败' }
tar.exe -xf $kitArchive -C $kitParent
if ($LASTEXITCODE -ne 0) { throw 'Windows 套件解包失败' }
$kitRoot = Join-Path $kitParent ([IO.Path]::GetFileNameWithoutExtension($kitArchive))
$nodeFile = Join-Path $kitRoot 'build-node\node.exe'
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $nodeFile).Hash -ne $nodeSHA) { throw '引导 Node 摘要不符' }
$env:AURIGA_CLIENT_DEVKIT = $kitRoot
$env:AURIGA_CLIENT_NODE_SHA256 = $nodeSHA.ToLowerInvariant()
Set-Location -LiteralPath $sourceRoot
.\scripts\client-release\auriga-client.cmd --help
```

薄壳先用 PowerShell5.1 核引导 Node 摘要，所有命令使用附件自带 Node，无需预装 Git/Node/pnpm。输入清单、Windows 系统工具与维护者封装见[客户端发行说明](scripts/client-release/README.md)。

2026-11-17决定：Windows x64客户端随包内置CPython 3.12.14和13个固定office/data wheel，成员无需另装Python，离线可用。`clients/installer/locks/python-win-x64.json`固定python-build-standalone release `20260901` install_only及全部wheel的URL/长度/SHA256/许可证；此锁进入Windows附件锁键，变化必须重出并审核附件。prepare只联网获取固定字节，现场离线构建只校验/解包，不执行pip、sdist、构建后端或安装脚本。运行时清单和许可证随包，`versions/<id>/runtime/python`随完整包验签、升级和回滚，启动器仅为DSH及其子进程前置PATH。自动更新当前传完整exe，无二进制差分，发行时应计入完整Python体积；Java及其他Python版本的按需下载仍属P3。固定解释器预编译unchecked-hash pyc并纳入校验。安装时用当前用户SID创建显式保护DACL，复制校验后拒绝普通文件访问的内容写入和新建目录项，保留删除/WriteAttributes支持清理。运行时无pip/ensurepip，不支持安装额外依赖。提供python/python3，系统py按原PATH解析。运行时额外文件触发desktop.python_runtime_modified并提示重新运行同版本安装包修复。发布前按发行说明离屏执行python-smoke，并核对包内13个精确版本。

### 现场个人 Python 加包

客户端在 `<DataHome>\python-user\cp312` 创建个人 venv，继承客户端内置包。pip 26.3 wheel 与解释器共同锁定，附件包括新增的 `python/pip/` 输入，共15项 Python 输入，锁变化后重新准备并审核 devkit。pip 首次准备时离线解包到个人 venv。普通安装进入个人目录，内置包优先并有精确版本约束，不兼容依赖会失败。只安装适用于 CPython 3.12 / Windows x64 的 wheel 或兼容纯 Python wheel。

个人环境 marker 记录 cp312 ABI，客户端遇到不同 ABI 会保留旧个人目录并回退内置解释器。个人目录内的 `.pth` 路径排在内置包之后。`.pth` 的 import 行按 venv 机制在 sitecustomize 前执行个人代码，用户须承担所安装包的代码执行风险。

现场 Forgejo 管理员启用包仓库功能，确定包归属用户或组织 `owner`，给上传账号该归属的包写权限。PyPI 包仓库随首次上传建立，可在归属主页“软件包”中查看，也可关联代码仓库。联网准备机用独立上传环境安装 twine，将所需包及其传递依赖的 wheel 全部带入现场。上传时由交互提示或受控凭据环境提供认证，令牌不写入仓库和索引 URL。Forgejo 的上传路径与下载索引路径如下。[Forgejo PyPI 文档](https://forgejo.org/docs/latest/user/packages/pypi/)

```powershell
python -m twine upload --repository-url https://forgejo.example.com/api/packages/owner/pypi D:\wheels\*.whl
```

下载索引为 `https://forgejo.example.com/api/packages/owner/pypi/simple`。当前本机索引配置使用无凭据 URL，私有仓库认证通过 pip 支持的现场凭据机制单独设置，禁止把 token 放进下述命令或配置文件。需私有认证时在现场验证凭据可用，pip 已关闭交互输入。HTTP 索引也支持，显式配置后只为该主机生成 trusted-host。

HTTP 索引存在中间人窃听与替换包内容的风险。现场建议使用 HTTPS 索引并验证证书。

从托盘退出 Auriga 后，用已安装客户端的 Node 和 CLI 保存索引，再从正常入口启动 Auriga。以下安装根目录仅为示例。

```powershell
$installRoot = 'C:\Apps\AurigaDesktop'
$packageId = (Get-Content -LiteralPath "$installRoot\current.txt").Trim()
$clientVersion = Join-Path $installRoot "versions\$packageId"
& "$clientVersion\upstream\node\node.exe" "$clientVersion\auriga\runtime\cli.mjs" python configure --root $installRoot --index https://forgejo.example.com/api/packages/owner/pypi/simple
```

设置保存在 `<DataHome>\auriga\python.json`，个人 `<DataHome>\python-user\pip.conf` 在准备时生成，DSH 子进程通过 `PIP_CONFIG_FILE` 使用它。环境清理继承的 PYTHON/PIP 设置。索引变化在重启后生效。无索引时包名安装直接失败，可用同一命令 `--index none` 恢复离线模式。离线 wheel 使用完整路径安装，所有依赖也须预置在 wheel 目录。

在 DSH 会话中执行以下命令，或让会话中的助手执行。

```text
python -m pip --version
python -m pip install package-name==1.0.0
python -m pip install --no-index --find-links D:\wheels package-name==1.0.0
python -m pip check
```

同名个人包以内置版本优先。需要更新内置13包时更新客户端锁并发行。个人环境会随升级/回滚保留，版本引用随当前客户端刷新。个人环境故障时使用托盘“设置 → 重置个人 Python 环境”，确认后停止运行中的 DSH、删除并离线重建环境，个人包需要重新安装，有效索引设置保留。也可在退出客户端后用上述 CLI 执行 `python reset --root $installRoot`。内置运行时损坏仍按启动器提示重装同版本客户端。

重置同时删除 pip 缓存，索引设置损坏时恢复离线模式。重置前运行中的 DSH 会在完成后恢复运行，原先停止的 DSH 保持停止。日志中的 `python_user.*` 固定原因码可用于排查失败。

### 现场桌面发行签名

首次在受信离线签名机生成独立现场桌面发行钥；父目录须存在、输出目录须全新。私钥及目录仅当前用户可读写，签名前复核权限，备份到受控离线介质。

```powershell
.\scripts\client-release\auriga-client.cmd keygen --out C:\secure\site-desktop
```

只把 `desktop-public.pem` 送往 Linux 平台。首次 install 或沿原参数重跑 install 时，加 `--desktop-public-key /media/desktop-public.pem --desktop-publisher-tenant <实际发行租户UUID> --desktop-confirm-fingerprint <独立核对的SPKI-SHA256>`，三项必须一并提供。指纹应从 Windows keygen 输出的 publicKeyFingerprint 独立核对（64位小写十六进制）；不符拒绝登记，成功打印已登记指纹。平台在原 state 登记公钥指纹和租户，配置 AURIGA_RESOURCE_DESKTOP_PUBLIC_KEY_FILE 与桌面卷；重复安装省略三项时复核 state 固定值，显式提供公钥仍须确认指纹，拒绝自动换钥/换租户。取得平台输出的 `resource-keys/resource-roots.json` 公钥根拷至 Windows。桌面私钥、资源私钥不进平台附件/源码/客户端。

修改客户端、插件、认证或界面代码并按模块测试，保留原 auriga-source.json、SHA256SUMS、SHA256SUMS.sig 和 release-public.pem。无 Git 时优先验签源码清单，记录上游提交、签名基线和当前源码摘要、发行者指纹及 siteChanges；普通源码支持增删改，已知文本 CRLF 不算现场改动。清单存在时，即使另建 .git 也不替换基线；篡改/缺签名拒绝。仅无清单的 Git checkout 沿原 Git 流程，两种来源都没有则拒绝。

```powershell
.\scripts\client-release\auriga-client.cmd build --offline --devkit $env:AURIGA_CLIENT_DEVKIT --verify-key D:\approved\release-public.pem --version 1.0.2-site.1 --sequence 102 --minimum-version 1.0.0 --channel stable --release-public-key C:\secure\site-desktop\desktop-public.pem --resource-trust-json D:\site\resource-roots.json --out D:\exports\desktop-102
```

--verify-key 为独立批准的 #373 套件发行公钥，也可用 --trust-fingerprint <独立批准SPKI-SHA256>，同时核源码和 Windows 附件。它与现场桌面发行钥不同。输出须新目录；UNC、设备路径、网络映射盘、junction、缺失输入、摘要/依赖锁变化会拒绝。可用 --platform-origin 预置成员入口，IP/HTTP 沿 installer 既有显式确认参数。

每次从全新源码目录构建；源码树中预置 node_modules、dist、.pnpm-store 或未列入基线的 clients/dsh-plugin/ui 会在启动构建前拒绝，并提示 PowerShell 清理命令。构建用 --offline --frozen-lockfile --ignore-scripts 恢复独立依赖，Node 子进程继承网络拒绝守卫；生产依赖只复制本地输入。输出 exe、诊断 zip、构建记录、未签清单、SHA-256 和网络证据。把目录带到离线签名机，审阅来源/现场改动、公钥/资源根、包摘要、版本/序号/最低版本/渠道等；Git 来源会显示未通过发行者签名清单认证的醒目警告，须独立核对。再人工确认摘要：

```powershell
.\scripts\client-release\auriga-client.cmd sign --dir D:\exports\desktop-102 --review-only
.\scripts\client-release\auriga-client.cmd sign --dir D:\exports\desktop-102 --key-file C:\secure\site-desktop\desktop-private.pem --confirm-sha256 <人工审阅后的reviewSha256>
```

制品或记录变化须重新审阅；错钥、宽松权限、重复覆盖均拒绝。签名后的 desktop-release.json 与 exe 配对发布，zip 不上传。默认有效期30天；渠道、--rollout-percentage 0..100、--minimum-version 和显式 --rollback-from 在构建时设定并签名，发布时不能修改。序号必须超过平台全部历史（含其他渠道及下架版本），日常发布不要随意抬高最低版本。

### 管理台发布与核对

管理员从“运维 → 客户端发布与更新”进入 `/ops/desktop`，使用实际发行租户及 `resource.versions.publish` 权限。读取台账、渠道当前版本、登记公钥与清单预检均无需近期身份验证。发布和下架仍要求最近 10 分钟内的身份验证，过期时沿管理台续验流程完成后继续。

1. 先看正式/测试渠道当前生效版本、sequence、最低版本、灰度比例及有效期，再核对发行台账中的下架记录。加载失败会显示权限、身份验证或服务错误，点击刷新重试。
2. 选择已离线签名的 `desktop-release.json` 与对应 `.exe`，点击“校验文件”。浏览器分块计算 exe SHA256，核对大小、全平台 sequence 高水位、渠道策略与登记公钥 SPKI-SHA256。公钥与指纹来自平台登记配置。
3. 页面在上传 exe 前将清单原文提交只读预检。平台以登记 Ed25519 公钥验证签名及当前发布策略，返回实际验签公钥指纹。该步骤支持纯 HTTP 管理入口，不需要浏览器 WebCrypto。清单未内嵌签名者指纹，页面通过登记公钥验签成功确认签名者一致。私钥始终保存在离线签名机。
4. 核对预览中的版本、sequence、渠道、最低版本、灰度、rollbackFrom、大小、摘要和公钥指纹，勾选确认，再点击“发布”。选择其他文件或刷新台账后需重新校验确认。服务端提交时会再次完整校验并在账本锁内检查策略。
5. 页面分别显示发布结果与同渠道 `latest` 完整清单自检。发布成功而自检或刷新失败时，先核对台账和渠道状态，再处理。管理台自检覆盖清单回读，完整下载验证可用下方 CLI。客户端真实自动更新仍需按后文演练。
6. 下架保留历史记录与序号高水位，渠道回到上一份可下载清单。客户端最低版本、灰度及显式回滚策略由离线签名清单决定。

设备版本分布来自当前租户未吊销桌面设备的最近认证上报，查看还需 `iam.network.manage` 权限。桌面登录完成后，以及更新下载失败、安装成功或失败回退后，使用现有 shell broker 的设备 Bearer 调用 `POST /api/iam/v1/me/desktop-report`。平台仅更新该凭据本人的设备记录，每设备每五分钟最多 30 次，保存当前安装版本、渠道及最近尝试的目标版本、结果、固定失败阶段与 reason code，详细日志留在本机。上报异步执行，网络异常后台重试；退出时尚未送达的事实保存在本机，下次登录继续发送。上报序号防止旧重试覆盖新状态，平台记录接收时间。

页面按设备上报渠道核对当前版本，显示已更新、尚未更新、渠道版本未知和未上报台数。最近尝试失败数包含回退，可与尚未更新数量重叠。无上报时显示“暂无上报”。旧客户端仅有登记 `clientVersion` 时仍显示版本分布，其更新状态计入未上报。云端实例尚无桌面安装器版本字段。

也可将正常会话的 {cookie,csrf} JSON 仅注入当前进程 AURIGA_CLIENT_ADMIN_AUTH（不写命令历史/日志/仓库），用同一接口发布并回读：

```powershell
.\scripts\client-release\auriga-client.cmd publish --dir D:\exports\desktop-102 --public-key C:\secure\site-desktop\desktop-public.pem --admin-origin https://admin.example.com --portal-origin https://portal.example.com
Remove-Item Env:AURIGA_CLIENT_ADMIN_AUTH
```

发布目录须同时保留 desktop-build.json；CLI 在任何网络请求前核 --public-key 的 SPKI 指纹与构建记录 publicKeyFingerprint 一致，再验签、核 exe 摘要及账本序号，发布后从门户同渠道 latest 验签并完整下载核长度/SHA256，拒绝跳转。自检失败可能已发布，先核管理台，不自动重发或撤回；没有近期会话就走管理台，不另造令牌。

从门户下载，独立核首次安装件摘要。让固定同一现场发行公钥的旧客户端走托盘“检查更新→立即更新→重启以完成更新”，核目标版本、配置/会话保留与资源验签；下载及安装确认仍沿成员设置。CLI 回读不替代真实自动更新验收。

**更换发行钥当前需要成员通过独立可信渠道重新安装**，没有自动取新根或双根过渡；平台不自动轮换。资源根也须统筹平台和客户端切换，保留更新高水位及成员数据，不能清状态绕过防回退。
