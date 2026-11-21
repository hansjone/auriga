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

`render` 重新验证原source/Node/runtime/proofs，创建 `config/shared-runtime/g-<digest>`。Platform只收到本服务签名与agent exporter输入，Edge只收到本服务identity/replay seed，Gateway只收到模型公钥，Agent只收到原完整runtime/profile/公开Host材料及svc:agent JWT只读目录；Host没有安装secret、服务JWT或签名私钥挂载。原Edge/Gateway exporter权限不变。Agent JWT由原IAM受信签发器自动续签并0600原子替换目录内文件。仅共享安装的原tenant-delegates追加既定svc:agent；原edge/gateway保留，无新增issuer或audience。新的RPC/model只发布到NAS loopback19091/18087，平台9091与共享模型18887仍有原mTLS、既定服务主体/Node UUID核验。

## 显式更换共享模型

已 committed 的共享源不能通过再次 `stage` 隐式改 `--model`（会报 `shared staging approval differs; implicit identity or policy rotation refused`）。正式更换只走显式动作，且只改模型，不轮换身份/密钥/adapter/profile/Node/CA：

```sh
# 仅 stage 新模型（可 rollback）；须提供与初装相同的独立 base 摘要
"$BOOTSTRAP" shared-runtime -action set-model \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256" -model auto
"$BOOTSTRAP" shared-runtime -action status \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source"
# 生效
"$BOOTSTRAP" shared-runtime -action commit \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256"
# 或在 commit 前回退到上一已提交代
"$BOOTSTRAP" shared-runtime -action rollback \
  -base "$DATA/config/materials" -out "$DATA/config/shared-source" \
  -expected-base-sha256 "$BASE_SHA256"
```

公开脚本一次完成 set-model、commit 与 core render：

```sh
sh deploy/nas/scripts/shared-install.sh \
  --set-model auto --expected-base-sha256 "$BASE_SHA256"
sh deploy/nas/scripts/start.sh
```

`set-model` 会重渲染 `public/host.json`、更新 `installation.receipt` 的 model/files 摘要并重置事件链；上一 committed 代暂存于 `.model-rotation`，commit 后删除，rollback 则原样恢复。同参数重入幂等；不同模型的并发/冲突 stage 拒绝。模型 ID 规则与初装相同（非空、≤128、无空白/换行）；安装器不查询中转站是否存在该 ID。commit 与 render 之后必须再跑 `start.sh`，使 Agent 加载新 host.json；一般无需清共享池，若成员会话仍绑定旧模型名再按运维窗口处理。

`start` 先停止公开Edge，沿公开 `shared-replay-prepare.sh` 生成独立临时安装NKey（root0700私有源），启动只有本阶段才含该身份的NATS配置。短命helper只连接固定项目内部网络的 `nats:4222`，没有Docker socket、任意endpoint或通用命令转发。它只可AccountInfo、固定桶INFO/CREATE；已有同名桶必须符合固定subjects、无Sources/Mirror/transform、文件存储、history1、TTL至少12小时、DiscardNew及无限聚合count/bytes，拒绝自动改桶/删桶/清墓碑。安装receipt记实际StreamInfo.Created，之后丢桶/换桶拒绝重建。

预建后强制重建NATS为无安装身份的常驻配置；核固定project/service、旧容器确已停止或移除、新容器运行，再确认临时身份收到明确Authorization拒绝、Edge身份成功读取同一桶，才开启本地readiness gate。超时/断网不能当撤权成功。常驻Edge没有CREATE/UPDATE，W仍只有原四项读取权限，五个旧password用户不加权。失败恢复常驻配置；恢复失败则停固定项目Edge/NATS，保留材料与桶。没有完成此流程时，普通Compose up/restart拒绝且不会回个人池；stop/检查仍可执行。这里只是原部署状态记录，不是新的签名信任或proof。

runtime升级pending期间，共享投影只使用原完整校验链中已提交的predecessor；初装拒pending。原commit后再投影current并重建Agent，保旧代文件和原proof/sequence。commit之前失败沿原rollback；commit之后新投影/Agent重建失败时，原事务已经禁止回滚，保留新committed pointer/sequence/proofs，停止Agent但保既有DSH进程，错误明确提示使用相同bundle/image重跑公开upgrade完成激活；不得暗降代次或手改active.json。
