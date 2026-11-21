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

备份保存在数据父目录的 `auriga-backups/`，权限0700，归档0600。升级先校验新包、加载镜像、冷备份，再更新发行和数据库。启动/健康失败会恢复升级前完整数据库、原私钥和代码，失败现场以 `.failed-时间` 保留。升级进程中断后，确认原进程已退出，处理 `.oneclick-lock` 遗留锁，再次执行相同 upgrade 会按升级记录恢复原版。不要删除备份和 `.upgrade.json`。

默认 Docker 内部网段沿用配方的 `192.0.2.0/26` 与 `192.0.2.128/26`。冲突时保留错误与已有配置，交部署维护者选择不重叠网段并走原有手工路径。安装器不改宿主路由、DNS或防火墙。

verify/unpack/install 只用包内公钥时打印指纹与“仅完整性，未核验发行身份”并非零退出。缺签名/公钥默认拒绝，只有显式 --allow-unsigned 应急例外会警告并记入安装state、doctor持续FAIL；未知签名者/错误签名不得绕过。服务器固定发行与已登记站点公钥，升级不接受包内公钥替换。站点构建必须签名，keygen site / trust add-site-key 与轮换流程见MAINTAIN.md。
