# Auriga NAS isolated test pack

Public deployment templates for a separate NAS test stack (`auriga-nas-test`).

普通 NAS 共享路径可显式设置 `AURIGA_INSTALL_MODE=container`，以预先审核并加载的不可变
`AURIGA_INSTALLER_IMAGE` 初始化新数据目录，无须宿主 sudo 或更改共享上级权限。
数据根、包、access 和独立 runtime 输入必须先解析为规范路径，数据根与输入互不包含。
helper 只挂一个数据根为 RW，其它输入 RO，无网络或 Docker socket；原 bootstrap 的
UID、祖先、receipt 和硬链接检查在容器内照常执行。宿主源目录由 operator 管理，
此模式不防御拥有宿主存储写权限的人。完整步骤见[操作指南](../../docs/dev/nas.md#普通共享路径的容器初始化)。

- Operator guide: [`docs/dev/nas.md`](../../docs/dev/nas.md)
- Runtime release guide: [`docs/dev/runtime-release.md`](../../docs/dev/runtime-release.md)

`preflight.sh`在初始化完成后还会执行只读的`mount-access.sh`：以各服务实际UID/GID和必要补充组，检查实际挂载目录遍历、公开材料读取及必要秘密文件打开权限。它复用已pin且本地存在的platform镜像提供`sh/cat`，所有源路径只读、无网络、无额外capability、无Docker socket，不拉镜像、不修改材料或ACL。可单独运行`AURIGA_DATA_ROOT=/srv/container/auriga-test sh deploy-nas/scripts/mount-access.sh`。0755不代表继承ACL下有效可读；出现`service`/`uid:gid`/`path-category`错误时，按[公开排障指南](../../docs/dev/nas.md#挂载访问与继承-acl-排障)核查自己专用目录的有效权限。节点在线但模板无法调度时，另按[节点label指南](../../docs/dev/nas.md#节点-label-与模板匹配)经正式管理台/API核对`template=real-dsh`，禁止直接改库绕过。

PostgreSQL仅对只读探针可确认完全为空的PGDATA放行官方entrypoint准备阶段，支持`umask 077`初始化产生的`root:root/0700`空目录。非空（含隐藏项、悬空链接）或无法确认空时，仍按999:999检查数据目录；SQL始终按999:999检查。探针不执行chown/chmod。

LiteLLM 原秘密保留在 `config/materials/litellm/`，归安装器 UID10001、文件0600；render 生成 `config/litellm/` 运行时副本（文件0640，master/salt组10003、数据库口令组999、团队Key组10001）。compose secrets 仅挂副本，LiteLLM 与 init 无额外 capability。备份原件、回执和数据库并保留 UID/GID/模式；恢复后由 render 校验或重建副本，已有内容或元数据漂移会拒绝。详见[中转站部署与备份](../../docs/dev/relay-litellm.md)。

- Do not put real domains, LAN IPs, or credentials in this tree
- Build images via `.github/workflows/nas-bundle.yml` (`workflow_dispatch` + exact commit)
- Never deploy from a worker session; main controller runs site instantiation from private notes

## 下载工件后的准备

GitHub Actions 工件不会保留文件执行位（见 [Permission Loss](https://github.com/actions/upload-artifact#permission-loss)）。
在解包后的工件根目录，先确认 `images/manifest.json` 的 `commit`、`source_tree` 对应独立核对的精确源码提交，
并核对 `bootstrap`、`runtime_release` 的包内相对路径及 SHA-256（后者目标为 `linux/amd64`）。
再验证工件根 `SHA256SUMS` 中的两个二进制、manifest、versions 摘要，以及 `images/SHA256SUMS` 中的镜像归档摘要，恢复两个工具的执行位：

```sh
sha256sum -c SHA256SUMS
(cd images && sha256sum -c SHA256SUMS)
chmod 0755 deploy-nas/bin/auriga-nas-bootstrap deploy-nas/bin/runtime-release
sh deploy-nas/scripts/load-images.sh "$PWD"
```

`load-images.sh` 是公开包的应用镜像加载入口。它先用已核对的静态 bootstrap 验证根和镜像校验和、
固定三份 tar 的 tag、原 config 摘要 `config_id`、唯一 `linux/amd64` 与实际层字节的有序 diffID 链，
再只加载 `platform.tar`、`edge.tar`、`agent.tar`。加载后从目标 Engine 结构化 inspect 得到精确本地 Id，
只按该不可变 Id 导出并再次核对原 config 字节及层链；不会再次用可变 tag 选择导出对象。
三项全部成功后才复制原 `images/manifest.json` 为 release marker，输出三条 `AURIGA_IMAGE_*` 建议。
将这些建议手工填入 `stack.env`；脚本不会改写 `stack.env`／`STACK_ENV`，不会创建或认领数据根。
默认 marker 位于包根；`AURIGA_RELEASE_DIR` 可指向已存在的独立目录，同时接收原 `versions.txt`。
失败会保留已加载镜像和原有 tag，不自动删除、修复或回退。

另外，operator 须从可信发布者独立批准并固定 PostgreSQL、NATS、registry 三个基础镜像的精确
digest，逐个执行 `docker pull --platform linux/amd64 'name@sha256:<已批准摘要>'`，再用
`docker image inspect --format '{{json .RepoDigests}} {{.Os}}/{{.Architecture}} {{.Id}}' 'name@sha256:<已批准摘要>'`
核对摘要与 `linux/amd64` 平台，并把完整引用写入 `stack.env`。
完整三镜像在线示例见 [操作指南](../../docs/dev/nas.md#初始化启动停止)。
全零占位不允许启动；预检只读核验七项镜像（包括 digest 引用）都在本机，启动只执行一次
`up -d --no-build --pull never`，失败保留非零退出码，不隐式拉取或重试放宽策略。

离线运输的 `docker save` / `docker load` 不保证 RepoDigests 保留。加载后应独立核对运输关联、
平台和目标 Engine 本地不可变身份，将已核对的本地 `sha256:...` Id 写入配置。
不能只沿用源机 Id，也不能将 config ID 当成 containerd store 的通用 Id。

`images.*.id` 保留构建时 CI Engine 的原身份；不同 Docker store 的实际本地 Id 可能是 config、manifest
或 index 摘要，不能直接把 CI 值或 `config_id` 当成目标 Engine 值。新增 `config_id`、`os`、`arch` 来自真实
docker-save 归档；这些是运输关联元数据，不是正式 runtime 签名、官方可信根或独立发布授权。
缺少新元数据或任一校验和的旧包会明确失败，应重新获取新 workflow 生成的完整包。

bootstrap 的 `image-metadata IMAGES_DIR`、`image-check BUNDLE_DIR`、`image-resolve BUNDLE_DIR` 三个入口均只读，
在安装参数校验之前分派，不需要 access／PKI／identity／数据根，也不需要 NAS 安装 Python 或 Go。
元数据入口供 CI 使用；check 无成功输出，resolve 仅在三镜像核验完后输出本地 Id 建议，错误走 stderr。
解析不解包到文件系统，拒绝重复 JSON／tar 条目、链接、不可信路径、截断和多可运行候选；允许有序重复层，
OCI attestation 必须绑定唯一的可运行 manifest。每份 tar 原字节上限 8 GiB、展开字节总量上限 16 GiB、
条目上限 32768，单 JSON 上限 2 MiB、保留元数据总量上限 32 MiB；单次命令总时限 10 分钟，
inspect 各 30 秒、每次不可变 Id 导出各 3 分钟，失败取消并回收 Docker 子进程。

两个工具均由 workflow 从同一精确 checkout 使用 Go 1.27.3、`CGO_ENABLED=0` 预构建为 Linux/amd64 二进制。
源代码 checkout 的对应路径为 `deploy/nas/bin/auriga-nas-bootstrap` 和 `deploy/nas/bin/runtime-release`。
操作脚本使用 `sh deploy-nas/scripts/<操作>.sh`，脚本不要求执行位。bootstrap 准备好后再按操作指南配置数据根并初始化。
在 NAS 上可直接使用 `deploy-nas/bin/runtime-release`，按 runtime release 指南运行 `verify`、`stage-layout` 等命令，无须现场安装 Go 或编译。

本工件是准备包，**不是完整离线安装包**：它不提供官方可信根、已签名的真实 DSH 发行材料、DSH 全部镜像层或第三方基础镜像。
工具不会自动生成或信任发布根；operator 必须从可信渠道独立审核并固定包外公开根、正式材料和精确允许镜像。
仅本机匿名 HTTP import profile 的运输流程、经 operator 独立审核的 ORAS 工具及其使用路径仍需另行准备，
遵照 runtime release 指南按原始 manifest 摘要运输与核验，并按对应精确版本的显式命令执行导入；
工具不自动启动 registry、不管理 registry 认证，也不自动执行现场部署。
完成摘要检查、材料验证或基础栈初始化不代表已能登录、真实实例已就绪或现场验收通过。

模型接入、独立 UI Origin、成员预算和外部 Key 操作见 [LiteLLM 部署指南](../../docs/dev/relay-litellm.md)。

## 可选集中日志

NAS 也支持 `observability` profile，使用与 Compose 开发栈相同版本的
Loki 3.5.5、Alloy 1.10.2。在可信渠道独立批准这两个版本的 linux/amd64 摘要，
显式拉取后核对 `RepoDigests`、平台和本地 Id；在数据根的 `stack.env` 添加：

```dotenv
COMPOSE_PROFILES=observability
AURIGA_IMAGE_LOKI=grafana/loki:3.5.5@sha256:<已批准摘要>
AURIGA_IMAGE_ALLOY=grafana/alloy:v1.10.2@sha256:<已批准摘要>
```

然后重新执行 `scripts/start.sh`。预检增加两项精确镜像检查；仍不自动拉取、
构建或改变部署根。关闭 profile 不会删除日志卷，也不会自动停止已经运行的采集器；
如需停采集，用同一 project 的 Compose 显式停止 `alloy loki`，保留业务服务。
新增服务仅接内部日志网络，不发布端口；日志保存七天，资源上限约 448 MiB。
Alloy 仅筛选固定 `auriga-nas-test` project 的容器。一次性的 `alloy-prepare` 无网络、
只持有 CHOWN，固定其状态卷顶层为 UID/GID 0；运行时 Alloy 以 UID 0 访问 Docker socket，
仍丢弃全部 capability，不能依赖 DAC_OVERRIDE 写入上游镜像拥有的状态目录。
公开日志配置须能由 Loki 的 UID10001 和 Alloy 的 UID0 读取；通过 SMB 单文件更新时，
需恢复这些公开文件的发布权限与所有权，并重新创建消费容器，不能只检查宿主的 mode 位。
不要将此权限操作应用于签名密钥、凭据或其他部署材料。
Docker socket 即使只读挂载仍有
宿主 root 等效权限；只在受控测试 NAS 启用，审阅日志后才分享。
此 profile 收集服务容器日志，独立桌面插件日志仍需另行按原日志指南收集。

不必对公网暴露无鉴权 Loki。在已授权的 NAS 操作通道运行
`scripts/observability-query.sh`，stdin 传一个换行结尾的
`http://127.0.0.1:3100/loki/api/v1/query_range?...` URL；stdout 返回原始 Loki JSON。
脚本只接受这个固定回环查询端点，通过 `compose exec -T loki` 查询，拒绝其他地址或多行输入。
它使用相同 `AURIGA_DATA_ROOT`、安装模式和项目，不读取业务凭据。
统一验收的 `observability.queryCommand` 可配置受信任的 argv 数组（例如既有 SSH
操作通道调用此脚本），`lokiUrl` 固定为 `http://127.0.0.1:3100`。
命令通过 stdin 接收 URL，stdout 只能输出 Loki JSON；操作通道的提示信息写 stderr。
`streamSelector` 使用 `{job="auriga-nas"}`，`expectedServices` 填真实结构化日志的
服务字段（如 `edge`、`iam`）。验收仍要求同一失败 trace 覆盖所有指定服务，不能用健康状态代替。

锁定镜像的真实状态卷回归可在本机 Unix Docker 上运行：
`AURIGA_IMAGE_ALLOY=sha256:<已核对本地Id> sh deploy/nas/test/observability-volume.sh`。
它只创建带唯一标签的临时卷，先复现 capless UID0 对镜像所属目录的写拒绝，再运行正式
prepare 命令并验证可持久写入；结束时仅清理标签匹配的测试卷，不接业务网络或 Docker socket。
