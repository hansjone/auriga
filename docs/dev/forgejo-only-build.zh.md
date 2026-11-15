# 只从 Forgejo 获取构建输入

本入口采用 Forgejo Generic Packages 运输已经冻结的依赖闭包。镜像归档、Go 文件代理、Maven 本地仓库、npm/pnpm 缓存和系统包均先下载校验，再在无网络容器重建。公共 Dockerfile 的默认联网行为保持原样。现场真实域名、所有者和凭据配置放在被忽略的 `deploy/forgejo/config.local.json`。

当前是部分闭包：Maven 全 reactor 尚未冻结，五应用镜像、bootstrap/runtime-release 的同版本整体断网验收尚未完成。不能直接把本入口当作完整正式离线发行。已有源码重建配方复用于 `deploy/nas/offline-build/`，其前置条件和锁校验见该目录说明。

## 清单与配置

`node deploy/forgejo/inventory.mjs` 从 Git 跟踪文件生成 `forgejo-deps-inventory.json`。记录 npm 所有 lock、pnpm packages、Go 模块与校验、Maven 依赖/插件/扩展/工具、Docker FROM、Compose 镜像、脚本和工作流的下载引用，以及输入文件 SHA-256。`download-reference` 是待逐条解析的引用，不代表一个已锁定的包。缺失版本/摘要使用 null；Maven 的继承和传递依赖需要完成 reactor 后生成有效闭包。Go sum 包含历史和其他平台条目。

`deploy/forgejo/artifacts.json` 单独记录已有 Forgejo 构建依赖包的版本、分片路径、长度与 SHA-256。路径中的 `{owner}` 由私有配置提供。`api-listed` 仅表示 API 列出该分片；下载器回读成功才表示内容已验证。不能由归档名称推断其中每个包都覆盖当前锁文件。源码清单与归档内容的对应关系须通过离线配方的 source-locks 校验。

复制 `config.example.json` 至 `config.local.json`，设置 Forgejo HTTPS 根地址、包所有者和只读 token 的环境变量名。凭据只通过环境注入，不写配置。下载器拒绝跳转、跨源路径、路径穿越、摘要不符、重复目标和覆盖已有输入；无外部回退地址。

```sh
node deploy/forgejo/download.mjs deploy/forgejo/config.local.json \
  deploy/forgejo/operator-inputs.json /data/auriga/operator-downloads
sh deploy/forgejo/operator-verify.sh /data/auriga/operator-downloads /data/auriga/operator-work
```

使用 Node 24；临时文件与归档均放有空间的数据盘。为其他闭包下载时，从 `artifacts.json` 选出所需包版本的全部分片组成同格式的 manifest。分片按编号连接后，还必须核发布方记录的完整归档 SHA-256；不得只校验分片后跳过完整归档校验。下载命令不执行归档里的脚本。

## 从依赖构建

解包后核各目录 `SHA256SUMS`，从基镜像归档加载镜像，核 Linux amd64 与镜像 config 摘要。具体装配步骤见 [离线重建](../../deploy/nas/offline-build/README.zh.md)。Go 配方设置 `GOPROXY=file:///inputs/download`，禁止外网回退；Maven 使用恢复的本地仓库与 `-o`；Web 恢复 pnpm、供应链元数据和 store 后离线安装。保留原有源码锁、依赖完整性、签名和策略检查。

先以 `--network none --pull never`、空构建缓存运行编译容器，再使用 `assemble-image.sh` 的 `--network=none --pull=false --no-cache` 装配。Operator 使用本地 wheel 的 `--no-index`，不访问 PyPI。基础镜像需提前加载；Docker build 的网络参数不会隔离 Docker daemon 自身。

## 正式验收

由现场维护者提供只允许 Forgejo 的独立 VM/网络环境，限制 IPv4、IPv6、DNS 和 Docker daemon 出站。不要修改共享开发网络来做隔离。从空 Git/语言/Docker 缓存开始，用 Forgejo clone 指定完整源码 SHA，下载同版本归档，保存访问日志与每步退出码。生产发行清单还需包含部署的第三方镜像、Docker/Compose/ORAS、DSH 上游闭包和已签名运行材料。

串行编译十个 Go 程序、前端与 Java，装配五应用镜像；按 [NAS 安装说明](../../deploy/nas/README.md) 起栈并从正常入口测试登录和健康。真实 DSH 按原运行时签名/导入流程验收。当前单服务测试不能替代此流程。Maven 采集缓存由原任务独占；接续前先由主控安排资源和交接，禁止两个容器同时写同一缓存。
