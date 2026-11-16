# 现场维护

成员、邀请、角色、配额、身份源、模型供应商、凭据、入口和运维策略先通过管理台配置。Agent 行为优先使用资源市场的插件、技能和受控配置。超出这些能力后再改源码。禁止直接修改运行容器或部署服务器上的程序文件。

## 浏览器下载后离线开发

联网电脑只用浏览器，在批准标签的 Forgejo Release 下载 `DOWNLOADS.txt`、`SHA256SUMS`、源码归档 `auriga-source-<版本>.tar`、覆盖该版本的完整 `auriga-devkit-<版本>.tar`。分卷时下载该前缀全部分卷及清单/签名，再下载同页 `auriga` 引导程序。后续步骤可在无 Git、无外网、无 Forgejo 连接的现场完成。

开发机是 Linux amd64，预装 Docker Engine 和 Compose 2.29+。不要求本地语言工具链。使用另附的 auriga-source 归档，其中有 `auriga-source.json`（标签、上游提交、源码文件摘要、锁摘要）、公钥、签名与 `bin/auriga-cli`。Forgejo 自动生成的 zip/tar 不含这些元数据与 CLI，因此应选另附源码包。

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

每版同时生成安装包、带元数据及 CLI 的源码、累积完整 devkit、相对上版增量、引导程序、公钥、下载说明、发行清单、总摘要及签名。超实际站点/代理上限或 4GiB-1 自动分卷，`--attachment-limit` 填实际最小限制。锁与批准输入不变时复用签名单版依赖缓存，再累积到新版；当前 NAS/可选发行文件另行刷新，避免复用旧部署代码。换签名密钥用新缓存，现有套件换根须重新批准完整套件。

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
