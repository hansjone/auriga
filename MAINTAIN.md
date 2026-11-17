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

默认累计阈值 64GiB（唯一依赖总字节），维护者可调整。跨大版本、超阈值或显式收拢时开新基线，增量标记 reset，拒绝合入旧基线；下载新完整套件到新目录。单版已超阈值则失败，需调整阈值。旧基线留在原 Forgejo Release 供旧版本使用，不删除。

## 维护者：准备和发布附件

以下仅由主控在批准的 Linux 维护机或 NAS 执行，不是现场默认路径，不在 CI 完整冷建。本16GB开发机只运行小夹具。源码 checkout 引导打包需要 Go 1.27.3，或 `AURIGA_GO_IMAGE` 指定批准的 Forgejo Go 工具镜像完整 digest；归档已有静态 CLI。Docker 需支持 `save --platform`（API 1.48+）。发版门禁另需本机 `dockerd`、root 和 `unshare --net` 权限。

取得独审批准标签、对应 NAS tar、Forgejo 依赖输入与固定 digest 的镜像。索引格式见 `deploy/offline/release.example.json`；仓库无真实默认发行索引。索引和 Ed25519 PKCS8 PEM 私钥放源码外。NAS 制品提交须等于目标提交。`build.recipes` / `build.inputs` 复用已合 main 的 `deploy/nas/offline-build` 配方及 Go proxy、Maven、pnpm/npm/Corepack/system 输入，不重写生产规则。

```sh
git status --short
git rev-parse HEAD 'refs/tags/0.2.0^{commit}'
sh deploy/offline/prepare-release.sh --tag 0.2.0 --release /secure/release.json --outdir /exports/0.2.0 --cache-dir /exports/devkit-cache --sign-key /secure/release-private.pem --attachment-limit 2147483648 --new-baseline --build
# 后续同基线版本，把 --new-baseline 换为：
# --previous-devkit /exports/unpacked-devkit-0.2.0
```

手工导出可用 `./auriga devkit --release /secure/release.json --out /exports/devkit.tar --sign-key /secure/release-private.pem --new-baseline`，后续传 `--previous-devkit`。手工导出不等于门禁通过。

每版同时生成安装包、带元数据及 Linux 静态 CLI 的源码、累积完整 devkit、相对上版增量、Linux 引导程序、公钥、下载说明、发行清单、总摘要及签名。构建工具镜像固定 digest；发行不生成或探测 Windows 制品。超站点上限或 4GiB-1 自动分卷。锁与批准输入未变则复用签名单版依赖缓存，再累积到新版；NAS/可选发行文件另刷新。换签名密钥用新缓存，换根须重新批准完整套件。

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

浏览器下载同版签名源码包和 Windows 输入独立附件 `auriga-client-windows-<锁SHA256>.tar`。Windows 部分与 Linux 累积 devkit 分开，依赖未变可复用；包含 Node/npm/pnpm、store、Windows DSH 及其依赖，签名链沿用 #373 发行公钥。先用独立批准的发行说明核源码归档和引导 node.exe 的 SHA-256，再解包到本地固定磁盘。不能用未核验包内公钥或临时计算摘要冒充批准值。

```powershell
Get-FileHash -Algorithm SHA256 D:\media\auriga-source-0.2.0.tar
Get-FileHash -Algorithm SHA256 D:\client-kit\build-node\node.exe
$env:AURIGA_CLIENT_DEVKIT = 'D:\client-kit'
$env:AURIGA_CLIENT_NODE_SHA256 = '<独立批准的node.exe SHA256>'
.\scripts\client-release\auriga-client.cmd --help
```

薄壳先用 PowerShell5.1 核引导 Node 摘要，所有命令使用附件自带 Node，无需预装 Git/Node/pnpm。输入清单、Windows 系统工具与维护者封装见[客户端发行说明](scripts/client-release/README.md)。

2026-11-17决定：Windows x64客户端随包内置CPython 3.12.14和13个固定office/data wheel，成员无需另装Python，离线可用。`clients/installer/locks/python-win-x64.json`固定python-build-standalone release `20260901` install_only及全部wheel的URL/长度/SHA256/许可证；此锁进入Windows附件锁键，变化必须重出并审核附件。prepare只联网获取固定字节，现场离线构建只校验/解包，不执行pip、sdist、构建后端或安装脚本。运行时清单和许可证随包，`versions/<id>/runtime/python`随完整包验签、升级和回滚，启动器仅为DSH及其子进程前置PATH。自动更新当前传完整exe，无二进制差分，发行时应计入完整Python体积；Java及其他Python版本的按需下载仍属P3。发布前按发行说明离屏执行python-smoke，并核对包内13个精确版本。

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

管理员在正常管理台 `/ops/desktop` 完成近期身份验证，选择已签 JSON 和 exe 后发布，需实际发行租户及 resource.versions.publish 权限。也可将正常会话的 {cookie,csrf} JSON 仅注入当前进程 AURIGA_CLIENT_ADMIN_AUTH（不写命令历史/日志/仓库），用同一接口发布并回读：

```powershell
.\scripts\client-release\auriga-client.cmd publish --dir D:\exports\desktop-102 --public-key C:\secure\site-desktop\desktop-public.pem --admin-origin https://admin.example.com --portal-origin https://portal.example.com
Remove-Item Env:AURIGA_CLIENT_ADMIN_AUTH
```

发布目录须同时保留 desktop-build.json；CLI 在任何网络请求前核 --public-key 的 SPKI 指纹与构建记录 publicKeyFingerprint 一致，再验签、核 exe 摘要及账本序号，发布后从门户同渠道 latest 验签并完整下载核长度/SHA256，拒绝跳转。自检失败可能已发布，先核管理台，不自动重发或撤回；没有近期会话就走管理台，不另造令牌。

从门户下载，独立核首次安装件摘要。让固定同一现场发行公钥的旧客户端走托盘“检查更新→立即更新→重启以完成更新”，核目标版本、配置/会话保留与资源验签；下载及安装确认仍沿成员设置。CLI 回读不替代真实自动更新验收。

**更换发行钥当前需要成员通过独立可信渠道重新安装**，没有自动取新根或双根过渡；平台不自动轮换。资源根也须统筹平台和客户端切换，保留更新高水位及成员数据，不能清状态绕过防回退。
