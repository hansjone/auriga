# NAS 离线制品的现场安装和重建

本说明供发布手册引用。平台为 Linux amd64，目标 Docker Engine 为 26.1.4。当前源码重建已在 Docker Engine 29.1.3 的断网容器中验证 Go 目标、改码后的 agent 镜像和前端三项构建。完整安装及发布组合须在目标 Engine、随包 Compose 和只允许 Forgejo 的网络中验收。

## 目录与版本

以下是同一主机上的目录示例。保持安装数据根、已验收的发行目录、运行时输入和重建输出彼此独立。每次登记精确源码提交、架构、工具链、依赖归档校验和、已审核的运行时版本及上一版本。

~~~text
/srv/auriga/releases/<release>/     服务端发布包、基镜像、工具及 SHA-256
/srv/auriga/runtime/<sequence>/    已签发的 DSH 声明、公开根、OCI manifest 与原始层
/srv/auriga/rebuild/<commit>/      锁定的源码重建输入
/srv/auriga/data/                  stack.env、持久数据、密钥和安装收据
/srv/auriga/backup/<checkpoint>/   可恢复的数据库与安装材料备份
~~~

从 Forgejo 制品索引下载实际发行单列出的文件。按 `PARTS.SHA256SUMS` 顺序复原分卷并核对整包 SHA-256，再解包到空发行目录。记录本机 `uname -m`、`docker version`、`docker compose version` 和 `docker buildx version`。Docker 26.1.4 上游打包带 Compose 2.27.1 和 Buildx 0.14.1。照发行单配置该发行已核验的 Compose 插件，使用独立 CLI 插件在目标 Engine 上验证。现场导入用 `docker image load -i`，按已核的单平台归档选择镜像。[Docker 26.1.4 说明](https://docs.docker.com/engine/release-notes/26.1/#2614) [Compose 插件安装](https://docs.docker.com/compose/install/linux/#install-the-plugin-manually)

发行单应同时列出 OCI 运输摘要和目标 Engine 的导入证据。镜像在不同 Engine 存储驱动下可能报告不同 ID，选用安装脚本在目标机校验后输出的值。运行时签名绑定的 registry locator 则按签发清单原样使用。

## 首装检查

1. 导入该发行的 PostgreSQL、NATS、registry 及其他经批准的基础镜像。在服务端包内运行已验摘要的 `load-images.sh`，保存它输出的目标 Engine 镜像 ID。
2. 依公开配方选择 domain、ip 或 proxy 配置，填写 `stack.env` 中明确的各镜像引用和数据根。新数据根只预置 `stack.env`，访问配置和独立信任输入放于数据根外。签名的 DSH 用已独立审核的公开根和 OCI 层按公开流程导入。
3. 运行 `init.sh`、`preflight.sh`、`network-preflight.sh` 和 `start.sh`。在私密终端完成首次管理员登录，核正常门户、成员邀请、托管实例启动及当前允许的业务路径。保存不含凭据的验证记录。[公开 NAS 安装说明](../../../docs/dev/nas.md#初始化启动停止) [签名运行时导入](../../../docs/dev/nas.md#消费独立审核的正式运行时)

~~~sh
release=/srv/auriga/releases/example-release
export AURIGA_DATA_ROOT=/srv/auriga/data
sh "$release/deploy-nas/scripts/load-images.sh" "$release"
# 依据输出的镜像 ID 和已审核配置编辑 stack.env 后继续。
sh "$release/deploy-nas/scripts/init.sh"
sh "$release/deploy-nas/scripts/preflight.sh"
sh "$release/deploy-nas/scripts/network-preflight.sh"
sh "$release/deploy-nas/scripts/start.sh"
~~~

## 从源码重建

从 Forgejo 检出与依赖清单同版的源码，在自己的分支记录改动。用独立输出目录，按构建说明恢复和校验工具链镜像、依赖归档、系统包。源代码编译容器只挂只读源码和依赖，以及独立输出目录，使用 `--network none --pull never`。完全禁网前预装已审核的基镜像并核对身份。

- agent/bifrost 由 Go 程序及其系统包组装。edge 同时需要 Go 与前端输出。platform 需要 Maven 产生的平台 JAR 和系统包。operator 使用锁定 Python wheel。
- 修改锁文件或上游镜像配方时，制品维护机重新采集输入并发布新的清单。只改 `.go`、前端或 Java 源码时在原锁的依赖归档上重建，记录新提交、镜像摘要和正常使用入口的验证结果。
- 装配后按公开服务端工作流重新生成归档、`images/manifest.json` 和 SHA-256，走服务端 `image-check`、`load-images.sh` 和目标机验证。生产启动使用此次经审核的整套发行。[重建配方](README.zh.md)

## 升级及恢复

1. 升级前保存上一发行、访问输入、`stack.env`、独立公开根、镜像 ID 和当前 runtime sequence。在一致检查点备份数据库、消息持久卷和数据根，其中包括实例材料、收据和私有安装文件。在隔离环境演练备份恢复。
2. 到新版本时先加载镜像并校验目标 Engine ID，再完成目标版本规定的迁移和正常入口复验。签名 DSH 的升级走发布状态机，单独核签名与递增序号。[运行时升级说明](../README.md#已有-nas-的签名-runtime-升级)
3. 晋升提交前按该版本公开的回滚入口处理未完成的运行时晋升。业务迁移已完成后，恢复以经过兼容性检查的旧版本，或旧服务与同一检查点的数据库及数据根备份为一组。保留身份根和收据。已提交的 DSH 通过有效、严格递增的签名修复版本恢复服务，延续序号高水位。[服务端升级检查点](../scripts/upgrade-admin.md)
