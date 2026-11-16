# Auriga 现场离线安装

包适用于 Linux amd64。现场预装 Docker Engine 和 Compose 2.29+，准备至少 4 GiB 可用内存与 20 GiB 可用磁盘。包不含 Docker 安装程序。

正式附件只在 Forgejo Release 下载。先从独立渠道核准发行引导程序 `auriga` 的 SHA-256 和发行公钥。完整附件目录用 `./auriga verify <目录> --verify-key /secure/release-public.pem` 校验签名总清单。分卷包保留同目录全部 `.part001…`、`.parts.json` 和 `.parts.json.sig`，执行 `./auriga unpack <安装包.tar.part001> --out <全新目录> --verify-key /secure/release-public.pem`，自动逐卷校验和读取合并，无须手工拼大 tar。未分卷的 tar 也支持同一命令。

已核验的发行引导程序可直接 `sudo ./auriga install --package <tar或part001> --address 192.0.2.10 --verify-key /secure/release-public.pem`。升级同样接受分卷并沿用固定公钥；其他维护命令请在解包安装目录使用。

1. 从独立受信渠道取得外层 tar 的 SHA-256，核对后解包到 root 管理的物理目录。包内 `SHA256SUMS` 覆盖全部文件。若发行方签名，额外取得独立批准的 Ed25519 SPKI PEM 公钥。
2. 在私密交互 TTY 执行 `sudo ./auriga install --address 192.0.2.10`。数据默认放 `/srv/auriga/auriga-data`。自定义父目录加 `--data /srv/auriga`。有签名时加 `--verify-key /secure/release-public.pem`。全程无下载、无构建。
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
