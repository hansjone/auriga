# 维护者：准备和发布附件

> 本页只给主控 / 发版维护者。现场开发、构建、签名和安装见根目录 [MAINTAIN.md](../../MAINTAIN.md)，现场部署见 [DEPLOY.md](../../DEPLOY.md)。

## 1. 固定标签与输入

主控在批准的 Windows x64 与 Linux amd64 维护机上使用同一干净、独审通过的标签。先同步精确标签到 Forgejo。取得目标提交对应的 NAS tar、Forgejo 构建输入、当前配方和 digest 固定的镜像。索引格式见 `deploy/offline/release.example.json`。把实际索引、Ed25519 PKCS8 PEM 发行私钥及制品放在源码外。`build.recipes` / `build.inputs` 沿用 `deploy/nas/offline-build` 的配方及锁定的语言输入。

DSH 源码使用 `deploy/offline/dsh-source-pin.json` 固定的官方标签提交归档。先将原始归档放入批准的 Forgejo 制品区，Linux 从该区下载到本地，或将该 HTTPS 附件 URL 直接交给 `--dsh-source`。当前归档可用，发布其原字节并附带原始 LICENSE 和来源清单。更新 DSH 时同步复核 npm 锁、官方标签提交与归档 SHA256。若上游无法提供对应提交归档，先审批新材料方案，保留 npm 锁对应闭包的原始 tarball、许可证与完整性证据。

## 2. Windows 先准备、签名并转运

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

## 3. Linux 生成发行目录

在新标签 checkout 中使用 Go 1.27.3，或设置 `AURIGA_GO_IMAGE` 为已批准的 Forgejo Go 工具镜像完整 digest。复用 checkout 时先重编 `bin/auriga-cli`，避免加载上一版的缓存二进制。Docker 需支持 `save --platform`（API 1.48+）。门禁需 Linux root、`dockerd`、`unshare` 及 `nsenter`，以及私有网络、mount 和 PID 命名空间权限。

```sh
git status --short
git rev-parse HEAD 'refs/tags/0.2.0^{commit}'
TMPDIR=/build/auriga-tmp sh deploy/offline/prepare-release.sh \
  --tag 0.2.0 --release /secure/release.json --outdir /exports/0.2.0 \
  --cache-dir /exports/devkit-cache --sign-key /secure/release-private.pem \
  --windows-client "$WINDOWS_TAR" --windows-client-sha256 "$WINDOWS_SHA256" \
  --dsh-source "$DSH_SOURCE" --new-baseline --source-only
```

先创建命令所用 `TMPDIR`，设定 `WINDOWS_TAR` 为转运到本地的归档路径，`WINDOWS_SHA256` 为 Windows 回执值，`DSH_SOURCE` 为绝对本地路径或上述 HTTPS 附件地址。私有输入按批准主机设置 `AURIGA_BUNDLE_AUTH_HOST`，在进程环境提供 `AURIGA_BUNDLE_TOKEN`。镜像仓库使用维护机 Docker 登录。

Linux 在构建前流式核验 Windows 套件的归档、签名、依赖锁及各项输入，并核对 DSH 提交归档与 npm 锁。`--source-only` 生成签名源码、Linux 和 Windows 开发套件、DSH 源码和发行元数据。现场用这些材料自己构建并签发服务端和桌面版本。Forgejo 默认发布完整归档。需要经 FAT32 等受限介质转运时，显式指定正数 `--attachment-limit`（字节），分卷上限为 4 GiB 减 1 字节。后续同基线发版，将 `--new-baseline` 换成 `--previous-devkit /exports/unpacked-devkit-0.2.0`。锁与批准输入未变可复用签名单版缓存。NAS 与可选材料按版刷新。

若旧累积套件包含成品桌面安装程序，第一次发行源码和套件时用 `--new-baseline`，确保旧安装程序不会被重新打包。

`unshare --net` 门禁使用空存储的独立 Docker 引擎，既有引擎的镜像缓存无法参与。门禁执行 `bundle --build --offline`，工具容器使用 `--network none --pull never`。`release-manifest.json.offlineBuildGate` 记录精确提交、锁、源码、完整套件、清单、构建输出摘要及起止时间。源码发行的验证包留在临时目录并随验证清理。历史安装包发行命令仍支持 `--build` 选择门禁构建的安装包。

- QNAP 若在容器启动时报 `fchmodat2 AT_EMPTY_PATH`，先在本次维护环境验证受信且兼容宿主内核的 runc，或改用受支持的 Linux 维护机。R1 在私有 Docker 工具目录使用冻结 DinD 镜像的兼容 runc。保留工具摘要，NAS 主引擎继续使用其自身配置。
- 若 GNU tar 报 `Bad address`，先做目录提取与 `cp -a` 探针。R1 使用冻结的 Debian Node 基底中 GNU tar 1.34、cp、loader 和库，构建工具镜像重新核定 digest 后进入签名 devkit。兼容层制作见 [Forgejo 冷建指南](../dev/forgejo-only-build.zh.md)。
- 独立引擎就绪限 30 秒，单次探针限 2 秒。Docker 元数据读取限 2 分钟，重新导出核 config 与层摘要限 3 分钟，完整断网门禁限 2 小时。VFS 在慢盘容易超时。用足够空间的快盘作为 `TMPDIR`，预留展开套件、镜像和编译空间，先确认最大工具镜像导入和重新导出耗时。
- 门禁运行状态在短路径 `/run/auriga-gate-*`，大数据留在 `TMPDIR`。若仍报告 Unix socket 路径过长，检查兼容运行时是否另用长目录，以及独立容器各层是否都能看到对应绝对路径。
- Ctrl+C 与 SIGTERM 触发进程、容器和私有挂载的有界清理。若报清理失败，按错误中的目录查 daemon 日志，核实本次进程与挂载是否退出再处理。最终发行目录在门禁和清理全部结束后才出现。

### 套件清单格式兼容

一份套件会覆盖之后一段时间的源码，因此会出现“旧 auriga 读新套件”。`devkit.json`、`devkit-catalog.json`、`delta.json` 按以下规则演进（实现见 `deploy/offline/devkit_compat.go`）：

- 每个字段都要在 `devkitFieldFeature` 登记，`TestDevkitFieldsClassified` 会拦下漏登记的字段。登记分三类：本机制之前已有的基线字段；之后新增、旧 auriga 可以忽略的说明性字段（`devkitInfo`）；会改变校验或构建行为的字段，登记一个能力名。能力字段的零值必须等于旧行为。非基线字段必须带 `omitempty`，结构体用指针，保证零值时整个键不出现在清单里（门禁会检查）。
- 清单里出现说明性新字段或能力字段时，写出时才在顶层带 `requires`（列出用到的能力，可以为空数组）。不认识其中能力的 auriga 会拒绝，并提示改用套件发行页附带的 `auriga`。没用到新字段的清单与旧格式逐字节相同，旧 auriga 照常读取。
- 本机制之前的 auriga 读带 `requires` 的套件会报 `unknown field "requires"`。新增能力字段前，要确认目标现场使用的发行页 `auriga` 已包含本机制。
- 格式只增不删：删字段或改变字段含义，必须换新基线（`--new-baseline`），出整套新套件。
- 维护机生成套件用的 auriga 不能比上一版套件旧，否则重写清单时会丢掉不认识的说明性字段。
- 发行页始终附带与套件同版本的 `auriga` 引导程序，现场的套件命令以它为准。

## 4. Forgejo 草稿、核验与发布

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

从正常 Forgejo Release 页面核对版本、附件、发行公钥指纹及 Windows 引导摘要。下载后按 [MAINTAIN.md](../../MAINTAIN.md) 做现场构建、签发、安装和升级。Windows 与 DSH 如有分卷，按 `.parts.json` 顺序以二进制拼接并核对 `DOWNLOADS.txt` 所列完整归档摘要。现场构建后的登录、资源发布、安装和更新从正常入口验证。
