# 默认共享 DSH 的独立材料源

当前 `shared-runtime` CLI 负责安装材料源和批准事件，尚未自动装配服务；持久 Compose、标准 start/render/runtime-upgrade 的接线另批完成。在该接线完成前，CLI 成功不表示门户已启用 shared。

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

## 固定 replay 桶安装小批

在原shared-source已commit、标准render已使用当前NATS模板后，执行 `sh deploy/nas/scripts/shared-replay-prepare.sh`。host与container模式均支持；两个真实网络操作必须使用原本地immutable installer镜像，只连固定auriga-nas-test_internal网络的nats服务，没有socket/任意endpoint/通用argv。脚本停止Edge，产生root0700独立临时NKey，只在prepare配置出现。受控helper只可AccountInfo、固定桶INFO/CREATE；已有同名桶只核固定subjects/无Sources与Mirror/文件存储/history1/TTL至少12h/DiscardNew/无限聚合容量，拒绝改删或丢失receipt后的重建。

随后强制重建固定project/service的NATS为无installer配置，确认旧进程停止/移除与新进程运行；installer明确Authorization拒绝且Edge成功读取同一Created的桶后才开本地gate。断网/超时不算撤权；失败恢复常驻配置，无法恢复则停固定Edge/NATS。五个旧password用户不放权，W原四读不增加，Edge常驻无CREATE/UPDATE。此小批成功后仍保持Edge停止；完整shared role/start/render/upgrade接线属于下一批，不能将本命令成功称门户已启用。公开复核入口为 `shared-replay-prepare -base 固定materials -action check`；stage/prepare-path/final-path均为离线材料操作，provision/verify-final只能通过固定网络helper。
