# 现场 AI 部署入口

当前支持 Linux amd64，现场只需预装 Docker Engine 和 Compose 2.29+。联网电脑使用 Linux/WSL，源码打包入口需要 Go 1.27.3，或用 `AURIGA_GO_IMAGE` 指定已批准的 Forgejo Go 工具镜像完整 digest 交给 Docker Desktop 构建入口。离线包自带静态安装器。

## 三步安装

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

仓库当前没有发布可直接使用的默认索引。维护者先按 `deploy/offline/release.example.json` 登记受信的 Forgejo NAS tar 及外层摘要、PostgreSQL/NATS/Registry 的 linux/amd64 镜像 digest。已批准且预加载的本机镜像可显式写 `sha256:<不可变ID>`，此类条目直接导出。NAS 制品须来自包含本安装器配方及 `image-extra-resolve` 的提交，使用 `.github/workflows/nas-bundle.yml` 产出目录再 tar 打包。联网 bundle 不接受未固定的基础镜像标签，也不自行推送镜像或发布制品。打包机 Docker Engine 需要支持 `docker save --platform`（API 1.48+）。私有发行页可通过环境变量 `AURIGA_BUNDLE_TOKEN` 提供 Bearer，仅发送给与 `AURIGA_BUNDLE_AUTH_HOST` 完全匹配的主机及端口。镜像仓库沿用打包机的 Docker 登录，凭据均不入包。

`--build` 复用 `FORGEJO-SELF-HOSTED-DEPS` 的源码配方。索引 `build.recipes` 指向该分支批准快照的 git archive tar，`build.inputs` 指向已核摘要的 Go/Web/Java/system 依赖目录归档，`tools`/`bases` 固定 Forgejo 镜像 digest。脚本下载并校验输入，按当前源码 HEAD 生成快照，以 `--network none` 执行原 go-inputs/web-inputs/java-inputs，最后调用原 assemble-image 装配。必须先提交源码改动。此路径需要能运行完整构建的维护机内存，不在16GB开发机做冷建。

升级与备份使用以下命令。升级前核对新包外层摘要，签名安装会继续使用已固定公钥验证新包。

```sh
sudo ./auriga backup
sudo ./auriga upgrade /media/auriga-offline-new.tar
sudo ./auriga status
sudo ./auriga logs
sudo ./auriga stop
```

冷备份和升级会停止栈与已确认归属的实例。备份后恢复核心栈，成员实例由成员正常打开。升级失败会回滚完整数据库和私钥。中断恢复及保管要求见随包 README。
