# 默认共享 DSH 的独立材料源

`shared-runtime` CLI 负责安装材料源和批准事件，`project` 将受信材料装配为各服务独立的不可变投影。标准 render/start/runtime-upgrade 持久选择已提交的共享配置。CLI 或文件投影成功仍不表示正常门户已验收；实际部署须完成下面的 NATS 预建、撤权和正常页面检查。

新现场和旧 sealed base 都复用原安装的 `config/materials`、原入网 Node 身份以及已提交 runtime 快照。先按原公开流程初始化、入网、安装批准的 runtime，再执行以下同一流程。不要向 sealed base 添加文件、重新入网、删除状态库或重建发行分区。

`BOOTSTRAP` 指向从当前源码构建的可信 NAS bootstrap；`DATA` 是原数据根的规范绝对路径。`BASE_SHA256` 必须来自独立核对的原 bootstrap receipt。现有私有 adapter 的全部原字节与公司身份映射必须保留，摘要由操作者独立批准；可以直接指向原 `config/materials/uac/adapter.json`（保原10001:10001/0640），也可提供独立root:root/0600私有文件。原固定adapter存在时两者必须逐字相同。存在有效 `sharedOutbound` 才派生批准的主机/身份头；仅缺失此段则公开策略为空白名单，IAM沿原missing-node路径拒企业出站，不影响普通team模型/受控工具及有真实凭据时的本人raw注入。公开 profile 必须为批准的完整共享 profile，root:root/0644，并由Agent原生产profile验证器提前核验。

```sh
"$BOOTSTRAP" shared-runtime -action stage \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256" \
  -adapter-file "$APPROVED_ADAPTER" -adapter-sha256 "$ADAPTER_SHA256" \
  -profile-file "$APPROVED_PROFILE" -profile-sha256 "$PROFILE_SHA256" \
  -model gpt-6.1-sol
"$BOOTSTRAP" shared-runtime -action status \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source"
```

只有明确没有公司adapter或其他UAC身份配置的新安装，可把上面两个adapter参数替换为 `-team-only`。安装器生成私有 `{}`，公开策略明确 `outboundAllowedHosts: []` 且没有伪造headers，raw本人策略仍true；当前没有公司凭据不会制造公司工号/token或把team token当公司token。此选项与adapter参数互斥，既有adapter/uac.properties/members存在时拒绝使用；公司登录源不会因为NAS的team优先被清空。模式和完整原件摘要绑定receipt，旧committed源不能暗切模式。

安装器产生固定且独立的Agent installation secret/hash、五用途Ed25519密钥、公钥JWKS/SPKI、独立NKey user seed及公开Host配置，不接受操作者手写JWT或签名证明。Host公开策略仅派生原批准的白名单主机和身份头，raw本人任务凭据策略为true；Cookie、token和私有地址pin不会进入公开Host配置。IAM仍消费完整原私有origin/IP pin策略，不以公开Host白名单替代。

新材料在root:root/0700的 `config/shared-source/v-00000000000000000001`；源叶固定0600，仅原Host renderer产生的 `public/host.json` 为0444。原base、Edge/Gateway秘密与输出目录不变。源receipt绑定全部叶、tenant、原Node UUID/CA、公有模型以及独立批准摘要。`.pending` 保留中断证据，发现它时拒绝自动覆盖、补生成或悄悄换钥；应在维护窗口核对备份和失败原因后按独立审定的恢复操作处理。

部署前可提交材料批准；提交会重新验证当前Node证书和已提交runtime：

```sh
"$BOOTSTRAP" shared-runtime -action commit \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256"
"$BOOTSTRAP" shared-runtime -action check \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source"
"$BOOTSTRAP" shared-runtime -action path \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source"
```

`check/path` 只接受committed，staged/rolled-back不会开启入口。每次检查沿原runtime-upgrade校验器核对完整源及proof链，拒绝pending候选，保留完整InstanceSpec的签名信封、资源、代次与资源修订；转换不是一次新的批准或签名。不读取Node私钥；Node证书更新只要仍是同一UUID/原CA并在有效期内即可。

尚未commit的stage可以用同一base批准摘要执行 `-action rollback`；重复rollback幂等。再次用原stage参数重试会沿追加receipt链恢复staged，复用全部原密钥。commit之后禁止材料rollback或隐式轮换。已部署后的故障维护应先通过原标准命令停止公开Edge入口并保留现有Agent/池/配额home，修复配置后重新核验；不得通过删除本源、池账本、执行分区或高水位伪装新安装。此CLI本身不启动、停止或重建任何服务。

消息侧另需既有批准msgnet材料、原collab IAM通道/服务票据、真实standalone shared-message Bean和公开root投影；本批Host尚未投影messageTrust，不称消息功能可用。普通team用户的模型和受控普通工具不应依赖公司UAC登录或消息可用性。

## 公开安装与持久启动

统一入口同时支持原host模式和container安装helper；输入为独立批准的原件与摘要，不要求现场手写密钥或服务JWT：

```sh
sh deploy/nas/scripts/shared-install.sh \
  --expected-base-sha256 "$BASE_SHA256" \
  --profile-file "$APPROVED_PROFILE" --profile-sha256 "$PROFILE_SHA256" \
  --adapter-file "$APPROVED_ADAPTER" --adapter-sha256 "$ADAPTER_SHA256" \
  --model gpt-6.1-sol
sh deploy/nas/scripts/start.sh
```

明确无任何公司身份配置的新装才把两个adapter选项替换为 `--team-only`。NAS团队登录优先继续来自原部署配置；共享装配保留原Edge登录设置及私有UAC字节，不修改产品SSO默认策略。

`render` 重新验证原source/Node/runtime/proofs，创建 `config/shared-runtime/g-<digest>`。Platform只收到本服务签名与agent exporter输入，Edge只收到本服务identity/replay seed，Gateway只收到模型公钥及独立的 `gateway/ai-relay.env`（自动从既有 Bifrost PG 运行/迁移密码投影 `AURIGA_AI_PG_DSN` 与 `AURIGA_AI_MIGRATION_PG_DSN`，host 与 container 模式均自动注入 `ai-relay`，无需现场手拼 DSN），Agent只收到原完整runtime/profile/公开Host材料及svc:agent JWT只读目录；Host没有安装secret、服务JWT或签名私钥挂载。原Edge/Gateway exporter权限不变。Agent JWT由原IAM受信签发器自动续签并0600原子替换目录内文件。仅共享安装的原tenant-delegates追加既定svc:agent；原edge/gateway保留，无新增issuer或audience。新的RPC/model只发布到NAS loopback19091/18087，平台9091与共享模型18887仍有原mTLS、既定服务主体/Node UUID核验。

## 输入获取与 XFS 存储后端前置

1. **`BASE_SHA256`**：取自原 `config/materials/iam-installation/bootstrap.receipt` 文件字节的 SHA-256（可用 `auriga-nas-bootstrap collab-base "$DATA/config/materials"` 交叉核对）。
2. **`profile.yml`**：使用仓库冻结的 [shared-runtime-profile.example.yml](shared-runtime-profile.example.yml)（须通过 `ValidateSharedProfile` 校验，不可随意删改行；渲染器会自动替换监听占位符）。
3. **存储后端选择（默认 `ext4-image`，可选 `--backend xfs`）**：
   - 在不支持 ext4 `prjquota` 的内核（例如 CentOS 7 `3.10.x`）上，可使用 XFS 项目配额后端：
     ```sh
     sh deploy/nas/scripts/shared-install.sh \
       --expected-base-sha256 "$BASE_SHA256" \
       --profile-file "$APPROVED_PROFILE" --profile-sha256 "$PROFILE_SHA256" \
       --model auto --team-only \
       --backend xfs \
       --quota-mount "$AURIGA_DATA_ROOT/data/agent/pools" \
       --quota-binary /usr/sbin/xfs_quota \
       --project-first 10000 --project-last 199999
     ```
   - **`--quota-mount` 路径规则**：必须是已启用 `prjquota`（`Accounting: ON / Enforcement: ON`）的独立 XFS 挂载点（可为裸设备或写入 `/etc/fstab` 的 loop XFS 镜像文件），且必须位于 `$AURIGA_DATA_ROOT/data/agent/pools`（Agent 容器内 `DataRoot` 为 `$AURIGA_DATA_ROOT/data/agent`，池根为 `data/agent/pools`）。
   - **Agent 镜像要求**：`xfs` 后端要求 `agent` 容器内存在可执行 `xfs_quota`（官方 `Dockerfile.agent` 与离线装配 `system-inputs.sh` / `Dockerfile.assembly` 已纳入 `xfsprogs`；离线构建变更后须同步刷新 `system/agent` 离线 deb 输入包）。`shared-install.sh`、`preflight.sh` 与 `auriga doctor` 均会以无网络只读容器探针校验 `xfs_quota` 可执行性。
   - **启动与观测**：提交并 render 后必须执行一次 `sh deploy/nas/scripts/start.sh` 完成 NATS replay 桶预建与临时安装身份撤权。可通过 `sh deploy/nas/scripts/status.sh`、`sh deploy/nas/scripts/preflight.sh` 与 `auriga doctor` 查看 `shared_runtime_state`、`backend`、`quota_mount`、`shared_replay_state` 及 XFS 探针状态（不含任何凭据）。

## 显式更换共享模型或共享 profile

已 committed 的共享源不能通过再次 `stage` 隐式改 `--model` 或 `--profile-*`（会报 `shared staging approval differs; implicit identity or policy rotation refused`）。正式更换只走显式动作；同一时刻只允许一种轮换，暂存目录为 `.rotation`（含 `rotation.json` 记录 `model` 或 `profile`）。

```sh
# 更换模型（只重渲染 public/host.json；不改 profile/身份/密钥）
"$BOOTSTRAP" shared-runtime -action set-model \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256" -model auto
"$BOOTSTRAP" shared-runtime -action commit \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256"

# 更换共享 profile（只替换 public/profile.yml；不改 model/身份/密钥/adapter/dedicated）
"$BOOTSTRAP" shared-runtime -action set-profile \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256" \
  -profile-file "$NEW_PROFILE" -profile-sha256 "$NEW_PROFILE_SHA"
"$BOOTSTRAP" shared-runtime -action commit \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256"

# commit 前可用 rollback 恢复上一 committed 代
"$BOOTSTRAP" shared-runtime -action rollback \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256"
```

公开脚本（各自一次完成 stage+commit+core render；不要在同一调用里混用）：

```sh
sh deploy/nas/scripts/shared-install.sh \
  --set-model auriga --expected-base-sha256 "$BASE_SHA256"
sh deploy/nas/scripts/shared-install.sh \
  --set-profile "$NEW_PROFILE" --profile-sha256 "$NEW_PROFILE_SHA" \
  --expected-base-sha256 "$BASE_SHA256"
sh deploy/nas/scripts/start.sh
```

一次维护窗口若两者都要做，推荐顺序：先 `set-profile` 新 profile 并 commit，再 `set-model auriga` 并 commit，最后 `render`/`start.sh`。新版生产策略不接受旧 profile，`set-profile` 起始时只对即将被替换的旧 profile 免检生产策略（回执、摘要、事件、权限照常校验），而 `set-model` 读当前代时要求 profile 已符合新策略，所以必须先换 profile。二者不能交叠（一种仍 staged 时另一种会拒绝）。同参数重入幂等；不同参数冲突拒绝；profile 须字节与独立 SHA 一致且通过生产 `ValidateSharedProfile`（`agent-default-model` 固定为 `auriga`）。commit 与 render 之后必须再跑 `start.sh`；一般无需清共享池。

## 显式更换已安装的公司 UAC adapter

在维护窗口，使用待升级版本的 `auriga-nas-bootstrap` 与 NAS 脚本。对照现场批准记录，分别核对原 sealed base receipt、当前共享源的 `platform/uac-adapter.json` 和新私有 adapter 的 SHA-256。新文件使用数据根之外的规范绝对路径，属主 root:root、权限 0600。原 sealed base 内的 adapter 保持初装字节和权限。

```sh
sh deploy/nas/scripts/shared-install.sh \
  --set-adapter "$NEW_ADAPTER" \
  --expected-adapter-sha256 "$CURRENT_ADAPTER_SHA256" \
  --adapter-sha256 "$NEW_ADAPTER_SHA256" \
  --expected-base-sha256 "$BASE_SHA256"
```

脚本在 host 和 container 模式均执行暂存、核对双摘要并提交、更新 replay 收据绑定及重新渲染。共享安装身份、密钥、profile、runtime、XFS 配额配置、replay 桶和池账本沿用原值。`--set-adapter` 只用于已有非空公司 adapter 的共享安装，与模型或 profile 轮换分开执行。仅当 token-verify 服务会拒绝错误的工号与令牌配对时，现场才可在新私有 adapter 顶层设 `"proofSubjectPolicy": "either"`。

需要分步核验时，可用 `shared-runtime -action set-adapter`，传入固定 `-base`、`-out`、`-expected-base-sha256`、`-adapter-file`、`-expected-adapter-sha256` 和 `-adapter-sha256`，并在提交前用 `-action rollback` 恢复原共享源。提交或渲染中断后，用同一份新文件和三项摘要重跑公开脚本。提交后若需恢复旧 adapter，可用同一入口反向轮换并重新独立核对双摘要。

随后按计划完成正式部署或运行 `start.sh` 让 Platform 载入新投影，再用正常桌面入口测试自动关联，并从 `status.sh` 确认共享模式与 replay 状态。Adapter 的私有内容仅投影给 Platform，Host 仍只收到允许公开的出站参数。

## 启动与 replay 核验

`start` 先停止公开Edge，沿公开 `shared-replay-prepare.sh` 生成独立临时安装NKey（root0700私有源），启动只有本阶段才含该身份的NATS配置。短命helper只连接固定项目内部网络的 `nats:4222`，没有Docker socket、任意endpoint或通用命令转发。它只可AccountInfo、固定桶INFO/CREATE；已有同名桶必须符合固定subjects、无Sources/Mirror/transform、文件存储、history1、TTL至少12小时、DiscardNew及无限聚合count/bytes，拒绝自动改桶/删桶/清墓碑。安装receipt记实际StreamInfo.Created，之后丢桶/换桶拒绝重建。

预建后强制重建NATS为无安装身份的常驻配置；核固定project/service、旧容器确已停止或移除、新容器运行，再确认临时身份收到明确Authorization拒绝、Edge身份成功读取同一桶，才开启本地readiness gate。超时/断网不能当撤权成功。常驻Edge没有CREATE/UPDATE，W仍只有原四项读取权限，五个旧password用户不加权。失败恢复常驻配置；恢复失败则停固定项目Edge/NATS，保留材料与桶。没有完成此流程时，普通Compose up/restart拒绝且不会回个人池；stop/检查仍可执行。这里只是原部署状态记录，不是新的签名信任或proof。

runtime升级pending期间，共享投影只使用原完整校验链中已提交的predecessor；初装拒pending。原commit后再投影current并重建Agent，保旧代文件和原proof/sequence。commit之前失败沿原rollback；commit之后新投影/Agent重建失败时，原事务已经禁止回滚，保留新committed pointer/sequence/proofs，停止Agent但保既有DSH进程，错误明确提示使用相同bundle/image重跑公开upgrade完成激活；不得暗降代次或手改active.json。
