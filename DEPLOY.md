# 现场 AI 部署入口

当前支持 Linux amd64，现场只需预装 Docker Engine 和 Compose 2.29+。联网电脑使用 Linux/WSL，源码打包入口需要 Go 1.27.3，或用 `AURIGA_GO_IMAGE` 指定已批准的 Forgejo Go 工具镜像完整 digest 交给 Docker Desktop 构建入口。离线包自带静态安装器。

## 三步安装（下载发行附件）

维护者在 **Forgejo Release** 提供每个服务端标签的安装包与开发套件；GitHub 不存放发行附件。现场不需要自行打包。Linux amd64 引导程序 `auriga`、`release-manifest.json`、`SHA256SUMS`、`SHA256SUMS.sig` 同页下载。超过 Forgejo 附件上限或 4GiB 的 tar 自动变成 `.part001…`、`.parts.json` 和 `.parts.json.sig`；须下载该包的全部分卷和清单。清单记录每卷及合并后总摘要。

1. 在联网电脑下载批准标签的全部附件到单独目录，拷入现场；从独立受信渠道取得发行公钥和引导程序 `auriga` 的 SHA-256。开发套件只供需要修改源码的开发机使用。完整附件目录可统一核验：

   ```sh
   cd /media/auriga-release
   printf '%s  auriga\n' '<独立批准的auriga引导程序SHA256>' | sha256sum -c -
   chmod 0755 auriga
   ./auriga verify . --verify-key /secure/release-public.pem
   ```

   `verify` 要求目录内附件与签名清单完全一致。公钥、临时下载文件和其他文件放在目录之外。只转移安装附件的场景，也可在联网电脑完成全目录校验后转移引导程序及安装包，下一步仍强制验证包内签名。

2. 自动校验、解包；对于分卷将输入改成 `auriga-offline-0.2.0.tar.part001` 或其 `.parts.json`。程序逐卷读取合并，校验每卷和整体摘要，无需生成另一个巨大的 tar。

   ```sh
   ./auriga unpack auriga-offline-0.2.0.tar --out /srv/auriga-offline-0.2.0 --verify-key /secure/release-public.pem
   cd /srv/auriga-offline-0.2.0
   ```

3. 在私密交互 TTY 安装，再从输出的正常管理台入口实际登录、邀请成员、发布并安装资源：

   ```sh
   sudo ./auriga install --address 192.0.2.10 --verify-key /secure/release-public.pem
   ```

   或直接一条命令读附件并安装：`sudo ./auriga install --package /media/auriga-release/auriga-offline-0.2.0.tar.part001 --address 192.0.2.10 --verify-key /secure/release-public.pem`。这时使用已核验的发行引导程序。默认数据目录 `/srv/auriga/auriga-data`；自定父目录加 `--data /srv/auriga`。域名模式用 `--domain example.com`，已有代理用 `--proxy /secure/access.json`。

## 自行打包的备用路径

1. 联网机拿到维护者提供的批准发行索引 `auriga-release.json`，在源码根执行一条命令。默认使用索引指定的 Forgejo NAS 发行制品，以及精确 digest 固定的全部基础镜像。

   ```sh
   ./auriga bundle --out auriga-offline.tar
   ```

   其他索引用 `--release /secure/release.json`。有离线发行签名私钥时加 `--sign-key /secure/release-private.pem`。私钥不会进包。

2. 把 `auriga-offline.tar` 拷入现场。从独立受信渠道取得联网机输出的 SHA-256，先核对外层 tar，再解包。发行签名公钥也须独立批准。

   ```sh
   printf '%s  %s\n' '<独立批准的SHA256>' auriga-offline.tar | sha256sum -c -
   mkdir auriga-offline
   tar -xf auriga-offline.tar -C auriga-offline
   cd auriga-offline
   ```

3. 在私密交互 TTY 执行安装，打开输出的管理台地址，以应急密码和 TOTP 登录并邀请成员。

   ```sh
   sudo ./auriga install --address 192.0.2.10
   ```

   有签名时加 `--verify-key /secure/release-public.pem`。默认数据目录为 `/srv/auriga/auriga-data`，`--data /srv/auriga` 指定数据父目录。域名用 `--domain example.com`，预先设置 portal/admin/实例泛域名的 DNS。已有受信代理用 `--proxy /secure/access.json`。详细说明随包的 `README-OFFLINE.md`。

## 出问题先诊断

```sh
sudo ./auriga doctor
```

doctor 输出逐项 PASS/FAIL 和修复建议，可直接贴给 AI。完成安装还须从正常管理台实际登录、邀请成员、上传/审核/发布资源，并用固定同批资源公钥根的桌面包安装资源。服务健康检查不能覆盖这些页面操作。

资源公钥根和离线降级私钥导出位置会在安装后输出。先把公钥根送回桌面构建机，使用 `--resource-trust-json` 固定到桌面包，再拷回现场。可选桌面发行与资源种子包随包保留，按其发行说明通过管理台导入。

## 维护者准备发行

发行流程在维护机或有足够内存的 NAS **本地执行**，不新增 CI 完整冷建。先取得独审批准的源码标签及对应 NAS 制品、Forgejo 依赖输入；下列仅为主控执行命令，不会在文档编写或测试时上传。`0.2.0` 为占位版本，替换成实际已批准标签；私钥和索引放源码目录之外。源码必须干净且 HEAD 正好等于标签，NAS 镜像清单提交也必须相同。

```sh
git status --short
git rev-parse HEAD 'refs/tags/0.2.0^{commit}'
sh deploy/offline/prepare-release.sh --tag 0.2.0 --release /secure/auriga-release.json --outdir /exports/0.2.0 --cache-dir /exports/devkit-cache --sign-key /secure/release-private.pem --attachment-limit 2147483648
```

附件限制单位为字节，取 Forgejo `repository.release`/`attachment` 配置及反向代理中最小的实际上传限制；程序再以 `4GiB-1` 封顶。默认上限为 `4GiB-1`。上面的 2GiB 只是示例。未提供足够小的实际站点上限将导致上传失败；草稿与已上传附件保留，可重跑，不自动删除或覆盖冲突。安装包默认用批准的预建制品，需从标签源码重建时另加 `--build`（仅维护机执行）。

每版输出 `auriga-offline-<标签>.tar`、`auriga-devkit-<标签>.tar` 或其分卷，外加引导程序、发行清单和签名总摘要。缓存保存 `auriga-devkit-<锁SHA256>.tar`，文件摘要及缓存记录受同一发行私钥签名；锁与批准输入未变时复用同一套件字节，各版本附件仍有同版本友好名。`release-manifest.json` 记录当前标签提交、锁文件明细/总摘要，以及复用套件的原始标签、提交与摘要。更换签名密钥使用独立缓存，不混用旧签名。Forgejo 是否在服务端去重取决于其存储实现，本工具不承诺服务端物理去重。

主控在上传前核对生成目录，先预演，再从受控环境提供 `FORGEJO_TOKEN`（只读环境变量，不写命令参数、日志或仓库），创建/续传草稿，核验后发布：

```sh
sh deploy/offline/publish-forgejo.sh --server https://forgejo.example.com --repo owner/auriga --dir /exports/0.2.0 --verify-key /secure/release-public.pem
# 以下两步只由主控执行，先将精确标签同步到 Forgejo：
sh deploy/offline/publish-forgejo.sh --server https://forgejo.example.com --repo owner/auriga --dir /exports/0.2.0 --verify-key /secure/release-public.pem --execute
sh deploy/offline/publish-forgejo.sh --server https://forgejo.example.com --repo owner/auriga --dir /exports/0.2.0 --verify-key /secure/release-public.pem --execute --publish
```

预演不访问网络；实际执行先验证 Forgejo 专有版本接口和远端标签提交。只使用同站点 HTTPS；令牌需该仓库发布权限。已有附件须大小/字节摘要一致才跳过，冲突不覆盖。全部附件重新下载校验后才将草稿发布。API 用法依据 [Forgejo 官方文档](https://forgejo.org/docs/latest/user/api/usage/) 与目标站点 `/swagger.v1.json`；版本识别依据[官方版本接口](https://forgejo.org/docs/latest/user/api/versions/)。若站点把附件重定向到独立对象存储域名，脚本会拒绝，主控应配置同源附件下载入口，不放宽令牌边界。

仓库当前没有发布可直接使用的默认索引。维护者先按 `deploy/offline/release.example.json` 登记受信的 Forgejo NAS tar 及外层摘要、PostgreSQL/NATS/Registry 的 linux/amd64 镜像 digest。已批准且预加载的本机镜像可显式写 `sha256:<不可变ID>`，此类条目直接导出。NAS 制品须来自包含本安装器配方及 `image-extra-resolve` 的提交，使用 `.github/workflows/nas-bundle.yml` 产出目录再 tar 打包。联网 bundle 不接受未固定的基础镜像标签，也不自行推送镜像或发布制品。打包机 Docker Engine 需要支持 `docker save --platform`（API 1.48+）。私有发行页可通过环境变量 `AURIGA_BUNDLE_TOKEN` 提供 Bearer，仅发送给与 `AURIGA_BUNDLE_AUTH_HOST` 完全匹配的主机及端口。镜像仓库沿用打包机的 Docker 登录，凭据均不入包。

`--build` 复用 `FORGEJO-SELF-HOSTED-DEPS` 的源码配方。索引 `build.recipes` 指向该分支批准快照的 git archive tar，`build.inputs` 指向已核摘要的 Go/Web/Java/system 依赖目录归档，`tools`/`bases` 固定 Forgejo 镜像 digest。脚本下载并校验输入，保存当前源码快照，包含提交、暂存/未暂存修改及未被 Git 忽略的新文件，以 `--network none` 执行原 go-inputs/web-inputs/java-inputs，最后调用原 assemble-image 装配。包内记录完整版本来源。此路径需要能运行完整构建的维护机内存，不在16GB开发机做冷建。

需要在内网修改源码时，从 Forgejo 下载源码标签与同版本 devkit，验证解包后执行 `./auriga devkit verify`，再运行 `./auriga bundle --build --offline`。详细的无本地 Go 工具链准备、复用旧套件和新源码绑定、现场分支及跟进上游步骤见根目录 `MAINTAIN.md`。维护者仍可用 `./auriga devkit --release /secure/release.json --out auriga-devkit.tar` 手工导出套件。

升级与备份使用以下命令。升级前核对新包外层摘要，签名安装会继续使用已固定公钥验证新包。

```sh
sudo ./auriga backup
sudo ./auriga upgrade /media/auriga-offline-new.tar
sudo ./auriga status
sudo ./auriga logs
sudo ./auriga stop
```

升级同样支持 `.tar.part001`/`.parts.json`，自动校验并逐卷读取；沿用首次安装固定的发行公钥验证新包与分卷清单。

冷备份和升级会停止栈与已确认归属的实例。备份后恢复核心栈，成员实例由成员正常打开。升级失败会回滚完整数据库和私钥。中断恢复及保管要求见随包 README。
