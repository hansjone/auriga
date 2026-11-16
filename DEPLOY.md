# 现场 AI 部署入口

现场是 Linux amd64，预装 Docker Engine 和 Compose 2.29+。联网电脑只需浏览器，从批准版本的 **Forgejo Release** 手动下载，再拷入现场；现场无需 Git、镜像仓库或网络。

两条路径：

- **只部署**：未分卷时下载两个文件：`auriga-offline-<版本>.tar` 和 `SHA256SUMS`。
- **要改代码**：再下载同版本 `auriga-source-<版本>.tar` 与 `auriga-devkit-<版本>.tar`，按 [MAINTAIN.md](MAINTAIN.md) 操作。源码归档自带版本清单、公钥与静态 CLI，不要求 Git、Go、Maven 或 Node。

先看同页 `DOWNLOADS.txt`：列出每个附件的用途、精确大小、必下项和套件基线。附件超过站点上限或 4GiB 时会分卷；此时下载该包全部 `.part001…`、`.parts.json`、`.parts.json.sig`，并下载同页 Linux amd64 引导程序 `auriga`。只部署无需开发套件与增量包。发行附件只放 Forgejo Release。

## 三步安装

Windows改代码可用PowerShell 5.1的`.\auriga.ps1`，Docker Desktop（WSL2/Linux容器）或WSL2发行版内Engine二选一，入口自动搬运并构建，不需进入WSL敲命令。也可Windows编辑后拷到现场Linux构建。离线安装文件清单、资源检查及操作见[MAINTAIN.md](MAINTAIN.md)。只部署仍按下面Linux服务器步骤。

1. 在联网电脑用浏览器下载上述安装文件，拷入现场 `/media/auriga-release`。以下 `0.2.0` 是占位版本，替换成实际批准版本。
2. 核对下载摘要，解包，并用包内公钥全离线校验：

   ```sh
   cd /media/auriga-release
   sha256sum --ignore-missing -c SHA256SUMS
   mkdir /srv/auriga-offline-0.2.0
   tar -xf auriga-offline-0.2.0.tar -C /srv/auriga-offline-0.2.0
   cd /srv/auriga-offline-0.2.0
   ./auriga verify .
   ```

   摘要输出须包含安装 tar 的 `OK`。分卷改用下载的引导程序：`chmod 0755 auriga`，然后 `./auriga unpack auriga-offline-0.2.0.tar.part001 --out /srv/auriga-offline-0.2.0`，再进入该目录。程序指出缺失或损坏的具体卷，逐卷校验并读取，无须手工拼大 tar。`./auriga verify <tar或首卷或目录>` 也可直接校验。默认验证包内公钥及签名；需独立核准发行身份时，用已批准公钥加 `--verify-key /secure/release-public.pem`。包内公钥用于随包完整性检查，独立公钥可进一步固定发行身份。
3. 在私密交互 TTY 安装，打开输出的正常管理台入口实测：

   ```sh
   sudo ./auriga install --address 192.0.2.10
   sudo ./auriga doctor
   ```

   以应急密码和 TOTP 登录、邀请成员、上传/审核/发布资源，并用固定本现场资源公钥根的桌面包安装资源。默认数据目录 `/srv/auriga/auriga-data`，自定义父目录用 `--data /srv/auriga`。域名用 `--domain example.com`，已有受信代理用 `--proxy /secure/access.json`。也可用发行引导程序直接执行 `sudo ./auriga install --package <tar或首卷> --address 192.0.2.10`。

## 出问题先诊断

`sudo ./auriga doctor` 逐项输出 PASS/FAIL 和修复建议，可直接贴给现场 AI。健康检查不能替代正常页面登录和资源安装操作。资源公钥根与离线降级私钥导出位置由安装器输出；把资源公钥根送回可信桌面构建机，以 `--resource-trust-json` 固定后再把桌面包拷回。详细说明随安装包的 `README-OFFLINE.md`。

```sh
sudo ./auriga backup
sudo ./auriga upgrade /media/auriga-offline-new.tar
sudo ./auriga status
sudo ./auriga logs
sudo ./auriga stop
```

升级也接受首卷或 `.parts.json`，保留数据和密钥，先冷备份数据目录及已确认归属的 local named volumes，失败回滚。首次显式固定的发行公钥会沿用到升级。冷备份与升级会停止栈及已确认归属的实例，备份后恢复核心栈，成员实例由成员正常打开。

## 选择开发套件

只部署无需 devkit。需要改代码时，选覆盖目标源码版本的最新完整套件：`devkit-catalog.json` 和发行清单列出基线、最新版本及每版锁摘要。同一基线内，新套件保留全部旧版本依赖和多版本工具镜像；已有精确上版套件可下载增量并用 `devkit merge` 合并。跨大版本或超过累计阈值会新开基线，下载完整套件到新目录；旧基线保留在原 Forgejo Release，供旧版源码使用。维护及自行打包路径见 [MAINTAIN.md](MAINTAIN.md)。

仓库代码提供生成与上传脚本，实际可下载版本由主控审批并发布；未发布的版本不会凭这些文档自动出现在 Forgejo。
