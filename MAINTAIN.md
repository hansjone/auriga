# 现场维护

成员、邀请、角色、配额、身份源、模型供应商、凭据、入口和运维策略先通过管理台配置。Agent 行为优先使用资源市场的插件、技能和受控配置。超出这些能力后再改源码。禁止直接修改运行容器或部署服务器上的程序文件。

## 浏览器下载后离线开发

联网电脑只用浏览器，在批准标签的 Forgejo Release 下载 `DOWNLOADS.txt`、`SHA256SUMS`、源码归档 `auriga-source-<版本>.tar`、覆盖该版本的完整 `auriga-devkit-<版本>.tar`。分卷时下载该前缀全部分卷及清单/签名，再下载同页 `auriga` 引导程序。后续步骤可在无 Git、无外网、无 Forgejo 连接的现场完成。

开发机可为Windows或Linux amd64，Linux预装Docker Engine/Compose 2.29+，Windows见后面的双后端准备。不要求本地语言工具链。另附auriga-source归档含标签/提交/逐文件与锁摘要的auriga-source.json、公钥、签名、Linux版bin/auriga-cli及Windows版bin/auriga-cli.exe。Forgejo自动源码zip/tar不含这些发行元数据与CLI，应选另附源码包。

```sh
cd /media/auriga-release
sha256sum --ignore-missing -c SHA256SUMS
mkdir /opt/auriga-source-0.2.0
tar -xf auriga-source-0.2.0.tar -C /opt/auriga-source-0.2.0
cd /opt/auriga-source-0.2.0
./auriga verify .
./auriga unpack /media/auriga-release/auriga-devkit-0.2.0.tar --out /opt/auriga-devkit
./auriga devkit verify /opt/auriga-devkit
```

分卷时把 tar 输入替换为首卷；源码本身分卷时先用发行引导程序 `unpack`。归档无 `.git`，现场命令读取发行清单，不调用 Git。默认公钥随包；独立批准公钥可用 `--verify-key /secure/release-public.pem`。源码整体 `verify` 在修改前执行，修改后用补丁、快照和 `devkit verify` 检查。

`devkit verify` 在源码根写 `.auriga-devkit.json` 绑定套件路径。移动套件后重新运行。指定版本检查覆盖：`./auriga devkit verify /opt/auriga-devkit --version 0.2.0`；当前源码版本还校验每个依赖锁和上游提交。锁不匹配列出新增、缺失或不同文件及应下载版本；缺依赖绝不静默联网。

## 修改、导出与离线构建

### Windows PowerShell 5.1

建议至少16GiB物理内存、SSD和足够存放套件、展开文件及产物的磁盘。Windows本机只运行入口/编辑器，语言构建在Linux容器内。后端二选一：

- Docker Desktop：启用WSL2后端与Linux容器并启动Engine。入口从签名devkit加载`tools.runner`镜像（Docker CLI、Compose、buildx、sh、tar），将源码/依赖传入临时Docker卷，避免从NTFS逐文件bind mount；构建后取回安装包并清理。缺runner的旧套件可用WSL后端。
- WSL2 + 发行版内Docker Engine：无需Desktop。入口自动以root调用选定发行版，通过tar流把源码及当前版依赖放进`/var/tmp`，构建后清理。不需进入WSL交互终端，不自动安装或重启WSL/Engine。

源码建议解到短目录`C:\auriga-src`。首次可用Windows内置tar解源码；也可下载同一Forgejo Release的`auriga.exe`，用`Get-FileHash`与发行SHA256SUMS核对后，运行`auriga.exe unpack`解包，支持分卷和长子路径。源码内自带Linux/Windows静态CLI，无需本机Go。

```powershell
Set-Location C:\auriga-src
.\auriga.ps1 verify .
.\auriga.ps1 unpack C:\media\auriga-devkit-0.2.0.tar --out C:\auriga-kit
.\auriga.ps1 devkit verify C:\auriga-kit
.\auriga.ps1 doctor --build
# 修改并按模块规范测试后：
.\auriga.ps1 changes export --out C:\exports\site-changes.tar
.\auriga.ps1 bundle --build --offline --out C:\exports\auriga-site.tar
```

默认有`docker.exe`选Docker，否则选WSL。发行版内Engine给verify/doctor/bundle加`--backend wsl`，可加`--wsl-distro Ubuntu`；也可设置进程环境变量`AURIGA_WSL_DISTRO`。只允许本机socket/命名管道，拒绝远程TCP/SSH context。若脚本策略阻止入口，用`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\auriga.ps1 ...`仅为本进程执行，或直接`.\bin\auriga-cli.exe ...`；工具不修改系统策略。

完整性verify在编辑前执行。构建快照自动将已知UTF-8文本的CRLF转LF，去shell BOM，恢复清单执行位，新增.sh标0755；原始源码/签名清单不改写，实际锁改动仍拒绝。UTF-16、单独CR、大小写冲突、保留名、尾点/尾空格明确拒绝并报文件。Go文件操作处理超过260字符的子路径；根目录仍建议短路径。被Defender/编辑器占用时有限重试后提示，不关闭防护。

doctor与devkit verify检查Docker/Compose、Linux后端和内存。工具容器默认4096MiB/2CPU，并留1024MiB给运行服务；Linux实际MemAvailable不足5120MiB则拒绝。Windows Docker先查VM总内存，Linux侧再查实际可用。WSL默认内存为物理内存的一半，可由用户在`%UserProfile%\.wslconfig`设置：

```ini
[wsl2]
memory=8GB
processors=2
```

设置需等全部WSL任务安全停止后，由用户安排重启才生效；入口不执行shutdown/terminate。见[Microsoft配置说明](https://learn.microsoft.com/en-us/windows/wsl/wsl-config)。生产全量构建资源须实测，小夹具不代表其内存需求。

### 离线安装前置（现场IT准备）

devkit默认不附OS/虚拟化安装器、不自动安装。联网电脑用浏览器准备独立批准介质，记录版本、大小、SHA256和签名验证结果再拷入现场：

- 两路线均需匹配Windows/amd64的WSL MSI，以及本机缺失时的匹配Windows可选组件离线源、虚拟化前置。IT按[Microsoft离线安装说明](https://learn.microsoft.com/en-us/windows/wsl/install#offline-install)装MSI、启用VirtualMachinePlatform；旧系统另备WSL组件/内核包并安排必要重启。
- Desktop路线另备批准的`Docker Desktop Installer.exe`及供应商校验记录，按[Docker安装说明](https://docs.docker.com/desktop/setup/install/windows-install/)选择WSL2/Linux容器。订阅适用性由IT核对[官方许可](https://docs.docker.com/subscription-billing/desktop-license/)；工具不代接受许可或再分发安装器。
- WSL内Engine路线另备发行版.wsl或可导入的rootfs tar；以及匹配发行版/架构的containerd.io、docker-ce、docker-ce-cli、docker-buildx-plugin、docker-compose-plugin及系统依赖闭包。也可由IT提供已装Engine/Compose的批准WSL导出tar，保留第三方许可与通知。按[Docker离线包说明](https://docs.docker.com/engine/install/ubuntu/#install-from-a-package)准备，不能只带docker客户端。

以下在PowerShell导入IT准备的完整rootfs，不需进入Linux终端（Windows系统组件由管理员预先处理）：

```powershell
wsl.exe --import AurigaBuild C:\WSL\AurigaBuild C:\media\auriga-build-rootfs.tar --version 2
# IT已确认发行版内Engine运行后：
.\auriga.ps1 doctor --build --backend wsl --wsl-distro AurigaBuild
```

前置与Auriga依赖分开管理，体积以实际介质清单为准；避免每版devkit重复携带整个发行版/WSL/Desktop，也避免未核准许可就再发行Desktop。现场不能安装WSL时用下一条文件搬运路径。

### Windows编辑、现场Linux构建

Windows编辑源码或changes export无需WSL/Docker。拷完整源码目录到Linux；或在Linux解新版本官方源码，再changes apply补丁。Linux构建机只需Docker/Compose，不需本地语言工具链：

```sh
cd /opt/auriga-source
# Windows拷贝若使外层auriga变CRLF或丢执行位，直接使用ELF入口：
chmod 0755 bin/auriga-cli
./bin/auriga-cli devkit verify /opt/auriga-kit
./bin/auriga-cli bundle --build --offline --out /exports/auriga-site.tar
```

快照规范化换行及执行位。产物给服务器upgrade。允许在部署服务器低峰构建，优先建议独立Linux构建机。默认检查可用内存并为语言工具容器设4GiB/2CPU；`--memory-mib`/`--cpus`可显式调整，保留1GiB余量。最终镜像层装配复用Engine的BuildKit，不是整个宿主/Engine的cgroup隔离；严格总资源要求请用独立构建机。

Linux服务器可显式`sudo ./bin/auriga-cli bundle --build --offline --out /exports/auriga-site.tar --install --data /srv/auriga`：成功后调用原upgrade，先备份数据/密钥/卷再升级，失败回滚。已有固定公钥时新包仍须受信密钥签名，--install不放宽校验。Windows入口不执行服务器安装。

无需 Git 的默认路径：修改源码，按模块规范测试，再构建。支持新增、修改和删除；对比归档基线摘要判断现场改动。内置忽略规则排除 `.env`、`.pem/.key`、部署数据、`node_modules/target/dist` 等缓存、根 `bin/` 及标准安装包输出；不解释全部 `.gitignore` 规则，其他普通文件按源码收录。凭据和额外产物放源码之外，导出前人工检查准备交付的代码。

```sh
./auriga changes export --out /exports/auriga-changes-0.2.0-site.3.tar
./auriga bundle --build --offline --version 0.2.0-site.3 --out /exports/auriga-offline-0.2.0-site.3.tar
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
./auriga bundle --build --offline --version 0.3.0-site.1 --out /exports/auriga-offline-0.3.0-site.1.tar
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

默认累计阈值 64GiB（唯一依赖总字节），维护者可调整。跨大版本、超阈值或显式收拢时开新基线，增量标记 reset，拒绝合入旧基线；下载新完整套件到新目录。单版已超阈值则失败，需调整阈值。旧基线留在原 Forgejo Release 供旧版本使用，不删除。

## 维护者：准备和发布附件

以下仅由主控在批准的 Linux/WSL 维护机或 NAS 执行，不是现场默认路径，不在 CI 完整冷建。本16GB开发机只运行小夹具。源码 checkout 引导打包需要 Go 1.27.3，或 `AURIGA_GO_IMAGE` 指定批准的 Forgejo Go 工具镜像完整 digest；归档已有静态 CLI。Docker 需支持 `save --platform`（API 1.48+）。发版门禁另需本机 `dockerd`、root 和 `unshare --net` 权限；仅连接 Docker Desktop 的客户端不足以启动门禁的独立引擎。

取得独审批准标签、对应 NAS tar、Forgejo 依赖输入与固定 digest 的镜像。索引格式见 `deploy/offline/release.example.json`；仓库无真实默认发行索引。索引和 Ed25519 PKCS8 PEM 私钥放源码外。NAS 制品提交须等于目标提交。`build.recipes` / `build.inputs` 复用已合 main 的 `deploy/nas/offline-build` 配方及 Go proxy、Maven、pnpm/npm/Corepack/system 输入，不重写生产规则。

```sh
git status --short
git rev-parse HEAD 'refs/tags/0.2.0^{commit}'
sh deploy/offline/prepare-release.sh --tag 0.2.0 --release /secure/release.json --outdir /exports/0.2.0 --cache-dir /exports/devkit-cache --sign-key /secure/release-private.pem --attachment-limit 2147483648 --new-baseline --build
# 后续同基线版本，把 --new-baseline 换为：
# --previous-devkit /exports/unpacked-devkit-0.2.0
```

手工导出可用 `./auriga devkit --release /secure/release.json --out /exports/devkit.tar --sign-key /secure/release-private.pem --new-baseline`，后续传 `--previous-devkit`。手工导出不等于门禁通过。

每版同时生成安装包、带元数据及双平台CLI的源码、累积完整devkit、相对上版增量、Linux/Windows引导程序、公钥、下载说明、发行清单、总摘要及签名。新索引必须有固定digest的build.tools.runner（Docker CLI/Compose/buildx/sh/tar），发行时断网探测这些工具，再用批准Go镜像交叉编译Windows CLI，宿主不需要Windows编译器。Desktop的runner需要访问本机Docker socket，不自动放宽组织策略。超站点上限或4GiB-1自动分卷。锁与批准输入未变则复用签名单版依赖缓存，再累积到新版；NAS/可选发行文件另刷新。换签名密钥用新缓存，换根须重新批准完整套件。

不可跳过的发版门禁：最新累积套件绑定本次源码归档，在 `unshare --net` 中启动空存储的临时独立 Docker 引擎，实际运行 `bundle --build --offline`；引擎与构建进程均断网，容器另加断网和禁止拉镜像。只允许从本次套件加载镜像，宿主已有镜像无法掩盖缺依赖。不修改宿主原引擎，结束后清理临时引擎与数据；需为完整套件、展开依赖、临时镜像存储和产物预留磁盘，必要时设置 `TMPDIR` 到足够空间的本地磁盘。核对产物版本、提交、干净快照后才生成可上传目录。缺依赖或缺权限均失败，不在线回退。`release-manifest.json.offlineBuildGate` 记录提交/锁、源码/完整套件/清单/构建输出摘要、断网方式、起止时间。默认安装包用批准预建镜像，仍须门禁；`--build` 直接发行门禁产物。

小夹具不替代生产配方完整离线演练。主控在 NAS 隔离构建区以真实索引执行上述命令，再将浏览器下载附件送到干净 Linux，按 DEPLOY.md 安装；正常页面登录、邀请、资源上传/发布和桌面安装全通过才宣称可验收。

主控检查生成目录及门禁后，先预演、上传草稿、核验后发布：

```sh
sh deploy/offline/publish-forgejo.sh --server https://forgejo.example.com --repo owner/auriga --dir /exports/0.2.0 --verify-key /secure/release-public.pem
# 仅主控执行，令牌通过 FORGEJO_TOKEN 环境变量提供，先同步精确标签：
sh deploy/offline/publish-forgejo.sh --server https://forgejo.example.com --repo owner/auriga --dir /exports/0.2.0 --verify-key /secure/release-public.pem --execute
sh deploy/offline/publish-forgejo.sh --server https://forgejo.example.com --repo owner/auriga --dir /exports/0.2.0 --verify-key /secure/release-public.pem --execute --publish
```

预演不访问网络；上传先校验签名、附件和断网门禁，再验证 Forgejo 与远端精确标签。只走同源 HTTPS；相同附件续传跳过，冲突不覆盖，全部重新下载核验后才发布。私有输入可用 `AURIGA_BUNDLE_TOKEN` / `AURIGA_BUNDLE_AUTH_HOST`，镜像仓库沿用维护机 Docker 登录，凭据不入包。新基线发布不清理旧发行附件。
