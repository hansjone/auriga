# 现场 AI 部署入口

现场是 Linux amd64，预装 Docker Engine 和 Compose 2.29+。联网电脑只需浏览器，从批准版本的 **Forgejo Release** 手动下载，再拷入现场；现场无需 Git、镜像仓库或网络。

两条路径：

- **只部署**：未分卷时下载两个文件：`auriga-offline-<版本>.tar` 和 `SHA256SUMS`。
- **要改代码**：再下载同版本 `auriga-source-<版本>.tar` 与 `auriga-devkit-<版本>.tar`，按 [MAINTAIN.md](MAINTAIN.md) 操作。源码归档自带版本清单、公钥与静态 CLI，不要求 Git、Go、Maven 或 Node。

先看同页 `DOWNLOADS.txt`：列出每个附件的用途、精确大小、必下项和套件基线。附件超过站点上限或 4GiB 时会分卷；此时下载该包全部 `.part001…`、`.parts.json`、`.parts.json.sig`，并下载同页 Linux amd64 引导程序 `auriga`。只部署无需开发套件与增量包。发行附件只放 Forgejo Release。

## 三步安装

任意电脑（包括 Windows）都可用浏览器下载并拷贝附件；auriga 只在 Linux amd64 服务器或独立 Linux 构建机运行。要改代码时，先拷入 Linux 完成 verify，再编辑、验证依赖、签名构建并安装/升级；也可在其他电脑编辑后拷回 Linux。操作与构建资源检查见 [MAINTAIN.md](MAINTAIN.md)。

1. 在联网电脑用浏览器下载上述安装文件，拷入现场 `/media/auriga-release`。以下 `0.2.0` 是占位版本，替换成实际批准版本。
2. 从独立核准的 Forgejo Release 正文或组织发布记录取得发行公钥指纹（SHA256，SPKI DER），核对下载摘要并解包。包内公钥无法自行证明发行身份；仅校验包内材料时程序打印指纹和“仅完整性，未核验发行身份”，并非零退出。将下例 `$ISSUER_FP` 设置为独立核对的64位小写指纹：

   ```sh
   cd /media/auriga-release
   sha256sum --ignore-missing -c SHA256SUMS
   mkdir /srv/auriga-offline-0.2.0
   tar -xf auriga-offline-0.2.0.tar -C /srv/auriga-offline-0.2.0
   cd /srv/auriga-offline-0.2.0
   ./auriga verify . --trust-fingerprint "$ISSUER_FP"
   ```

   摘要输出须包含安装 tar 的 `OK`。分卷改用下载的引导程序：`chmod 0755 auriga`，然后 `./auriga unpack auriga-offline-0.2.0.tar.part001 --out /srv/auriga-offline-0.2.0 --trust-fingerprint "$ISSUER_FP"`，再进入该目录。程序指出缺失或损坏的具体卷，逐卷校验并读取，无须手工拼大 tar。`./auriga verify <tar或首卷或目录>` 也可直接校验。verify/unpack/install 必须核验发行身份：加 `--trust-fingerprint "$ISSUER_FP"`，或提供已独立批准的 `--verify-key /secure/release-public.pem`。缺签名/公钥默认拒绝。
3. 在私密交互 TTY 安装，打开输出的正常管理台入口实测：

   ```sh
   sudo ./auriga install --address 192.0.2.10 --trust-fingerprint "$ISSUER_FP"
   sudo ./auriga doctor
   ```

   以应急密码和 TOTP 登录、邀请成员、上传/审核/发布资源，并用固定本现场资源公钥根的桌面包安装资源。默认数据目录 `/srv/auriga/auriga-data`，自定义父目录用 `--data /srv/auriga`。域名用 `--domain example.com`，已有受信代理用 `--proxy /secure/access.json`。也可用发行引导程序直接执行 `sudo ./auriga install --package <tar或首卷> --address 192.0.2.10 --trust-fingerprint "$ISSUER_FP"`。

## 出问题先诊断

`sudo ./auriga doctor` 逐项输出 PASS/FAIL 和修复建议，可直接贴给现场 AI。健康检查不能替代正常页面登录和资源安装操作。资源公钥根与离线降级私钥导出位置由安装器输出；把资源公钥根送回可信桌面构建机，以 `--resource-trust-json` 固定后再把桌面包拷回。详细说明随安装包的 `README-OFFLINE.md`。

```sh
sudo ./auriga backup
sudo ./auriga upgrade /media/auriga-offline-new.tar
sudo ./auriga status
sudo ./auriga logs
sudo ./auriga stop
```

升级也接受首卷或 `.parts.json`，保留数据和密钥，先冷备份数据目录及已确认归属的 local named volumes，失败回滚。首次安装核准的发行公钥会复制并固定到 root 管理的 state，之后 upgrade/doctor/bundle --install 只接受该发行公钥或已登记站点构建公钥，不接受包内公钥替换。现场构建签名和登记见 MAINTAIN.md。冷备份与升级会停止栈及已确认归属的实例，备份后恢复核心栈，成员实例由成员正常打开。

## 选择开发套件

只部署无需 devkit。需要改代码时，选覆盖目标源码版本的最新完整套件：`devkit-catalog.json` 和发行清单列出基线、最新版本及每版锁摘要。同一基线内，新套件保留全部旧版本依赖和多版本工具镜像；已有精确上版套件可下载增量并用 `devkit merge` 合并。跨大版本或超过累计阈值会新开基线，下载完整套件到新目录；旧基线保留在原 Forgejo Release，供旧版源码使用。维护及自行打包路径见 [MAINTAIN.md](MAINTAIN.md)。

仓库代码提供生成与上传脚本，实际可下载版本由主控审批并发布；未发布的版本不会凭这些文档自动出现在 Forgejo。

缺签名/缺公钥仅可由操作者显式使用 `--allow-unsigned` 紧急解包/校验/安装/升级，始终打印醒目警告；它不绕过错误签名、指纹不符或未登记签名者。安装/升级把例外记入 state，doctor 持续 FAIL，不因后续健康或签名正确自动清除。未签名源码基线和 devkit 一律拒绝，不支持此例外。无签名首次安装没有可固定的发行身份，恢复受信状态须保留完整备份并由维护者做受信重装，不能把包内新公钥自动收为发行根。

资源密钥与对象目录先在同盘临时目录完整生成、设置权限，再原子改名。初始化中断后，完整材料可原样复用；发现残缺材料时保留目录并按提示从原备份恢复，禁止盲目删掉已经使用的资源密钥。升级已健康后若旧发行归档移动失败，仅警告并保留旧目录；人工归档后再进行下一次升级。

## 客户端发布（Windows 开发机）

桌面客户端由 Windows 开发机使用独立 scripts/client-release/auriga-client.cmd 构建、签名和发布，服务端 ./auriga 仍仅在 Linux 运行。Windows 输入作为同版独立签名附件，沿用发行公钥，支持无 Git 的签名源码包。流程及 Linux install 发行公钥参数见 [MAINTAIN.md](MAINTAIN.md#客户端发布windows-开发机)。
