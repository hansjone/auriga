# NAS network 密钥轮换与续期

权威规则见 [密钥运维契约](../../contracts/network/key-operations.md)。NAS 通过
`rotate-collab-keys.sh` 复用生产 `NetworkKeyOperator`，不修改网络根、PG 首发时间或客户端账本。
操作前备份原数据、PG 和审批材料。安装者拥有本机 Docker 权限；所有输入使用规范绝对路径。

## 有效期与恢复边界

清单 `valid_until - issued_at` 最多 86400 秒；业务钥 `not_after` 独立。
一次性 `generate-test-approval.mjs` 将初装业务钥也设为 24 小时，且会生成新根，不能用于续期。
相同 kid 的材料、用途、原有效期不可改变。新业务钥先 next，真实 PG 首发满 86400 秒才可 active；
激活时原 active 仍须覆盖至少 360 秒。完整历史不能删除，根私钥不能进入 NAS 或在线进程。

客户端按原根验签、版本及不可变材料/状态历史接收完整清单；24 小时首发成熟检查在服务端 PG。
独立离线恢复仅在旧清单和全部历史钥均过期满 360 秒、PG 与应用时钟相差不超过 60 秒时可用。
恢复保留所有旧 kid、材料、用途和原有效期，旧 revoked 不复活，其他旧钥保持 retired/revoked；
新增三个独立用途 active，寿命不超过 7 天。原根签名和明确人工审批仍必需，PG 首发时间取真实当前时间。
恢复审批必须确认客户端重新拉取完整 keyset；不清空客户端账本或改变已安装根。
普通 publish 的 next/24h 门禁保持不变，服务 HTTP 没有 force 或恢复入口。

## 版本化记录与运维边界

初装 `config/materials`、`config/collab-source` 和旧 activation receipt 保持原样。
新记录放在 `config/collab-operations`（root 0700、文件 0600）：

- `binding.json` 绑定原 base/source 收据、固定数据根、配方路径、最初批准的平台镜像本地 ID 和可选独立 operator jar 摘要（历史记录不重写）。
- `releases/<version>` 追加完整 signed/announcement、实际 PG 状态和 publication receipt 链。
- `installs/<version>` 追加配置、active/next 私钥、网关 protocol、切换前 state 快照及 install receipt。
- `requests/<version>` 保存未签请求及新业务私钥；待签内容不覆盖，不代表已发布或已安装。
- `batches/<firstVersion>` 保存公开批次计划，`queues/<firstVersion>.json` 保存通过根签与审批校验的队列及各文件摘要。
- `current.json` 只选择完整提交的安装。原运行 state 由生产安装器原子更新，旧字节另外保留；原 state.lock 不删除或重建。

所有变更持同一个 NAS 文件锁，竞争时最多等 5 分钟，超时明确失败。PG 发布后未安装、清单/业务钥过期或轮换半提交只使消息网络降级；公开 start 仍启动登录、管理、中转站、模型和 agent，并输出 `message_network=degraded`。版本回退、同版本改字节、缺历史或材料篡改仍拒绝签名。`.pending` 与首失证据保留，不能删除后盲重试。只读 status/path 允许存在 pending，status 使用真实受保护 state 摘要查询 PG；stop 不依赖最新发布已经安装。
`rotate-collab-keys.sh stop` 是固定项目的停签逃生入口，不修 state、不改 PG 或 selector。

消息配置渲染失败时，核心 Compose 单独保留 `managed-identity-compose.yml`：只有初装
activation receipt 校验通过、静态 `managed-network.json` 的属主/权限及源文件字节均匹配时，
才继续启用原托管身份/模型凭据签发流程；这不挂载失败的消息配置或启动消息消费者。
消息连接失败不影响持有效托管凭据的实例就绪和普通模型请求；凭据过期、撤销或验签失败仍
使整个实例不可用。独立安装投影缺失或改变时拒绝使用，不能拿门户/心跳 200 代替实例认证。

普通安装/render helper 不挂 Docker socket。只有显式轮换命令和受信续期任务接入本机 socket；
Java helper 仅使用批准镜像、固定主类与固定计划，数据根只读，写范围限本次暂存及必要的原 state 目录。
PG 操作先核现有数据库容器、数据挂载和内部网桥；数据库缺失不创建空库，已停的 postgres 不由巡检或轮换命令拉起。
发布只停止该项目的 network/collab；保留 platform、edge、agent、实例及数据卷。

## 准备请求与启动小时巡检

使用更新后的公开 NAS 包和 linux/amd64 bootstrap（0755）。`stack.env` 中的平台/安装器镜像
必须是独立核准、已存在本机的不可变引用。绑定独立 operator jar 后，平台正常升级只需按公开 load/start 更新 `AURIGA_IMAGE_PLATFORM`，固定 jar 在新批准镜像的 JRE 中执行；历史 binding、输入与 receipts 保留原 image 字段，不需要重签或修改历史。未绑定 jar 时仍固定原操作器镜像；配方路径、数据根或 jar 字节改变属于独立迁移，不能改历史收据绕过。
过期恢复还须从公开源码构建新的 platform executable jar，部署到固定
`deploy-nas/bin/collab-key-operator.jar`（root:root、不可组写、非链接），并在 `stack.env` 配置
`AURIGA_COLLAB_OPERATOR_JAR_SHA256=<独立核准的小写SHA256>`。固定 helper 在批准镜像的 JRE
中执行这个只读 jar；不会替换现有平台或 collab 服务镜像。绑定后不能替换 jar 字节。
先在已验证的源码工位执行 `./mvnw -pl services/platform-app -am -DskipTests package`，
核对 `services/platform-app/target/platform-app-0.1.0-SNAPSHOT-exec.jar` 摘要后交付到上述固定位置。

```sh
export AURIGA_DATA_ROOT=/srv/container/auriga-test
sh deploy-nas/scripts/rotate-collab-keys.sh prepare
```

该命令返回私有 requestDirectory，不输出私钥。请求包括 `payload.json`、`trust.json`、
公开发布用 `input.json`、安装用 `install-input.json` 和 active/next 所需的 `keys/`。
新业务钥寿命 7 天，剩余不超过 48 小时时自动准备 next；到达真实 PG 成熟边界后准备激活请求。
`trust.json` 只是便于运营者核对的安装元数据，不能替代独立确认的根指纹。

在第一次 publish **之前**，启用小时巡检：在 `stack.env` 设置
`AURIGA_COLLAB_RENEW_ENABLED=true`，然后使用公开配方只启动该任务：

```sh
sh deploy-nas/scripts/schedule-collab-renewal.sh
```

`collab-renew` 使用现有安装器镜像、无网络、只读数据根，专用 operations 与原 state 目录可写。它每小时检查一次。已导入预签队列时，到点复用生产 Java publish/install，按原容器定义只重建 collab/network，使新配置挂载生效，不启动 PG 或其余控制服务。业务私钥只在 NAS，原根只在离线设施。默认业务钥 7 天，到期前 48h 准备 next；新 next 最早在计划首发 36h 后转 active，给小时调度留 1h 余量，真正首发成熟仍由 PG 强制。停机超出计划余量时，不跳过不成熟门禁，告警并由运营者重新排队。

没有队列时巡检只准备单份请求，并在清单或业务钥不足 6h 时告警。单份请求只在剩余至少 18h 时复用，publish 要求至少剩余 12h；过旧请求需要重新 prepare/根签，不会把旧签名当作续期。`stop.sh` 先暂停调度，`start.sh` 正常启动后恢复；`start.sh --network-offline` 明确只启动核心服务并暂停消息调度。也可用 `rotate-collab-keys.sh pause|resume` 控制；不删除历史或改变有效期。

## 每周一次离线批签（默认 14 份）

```sh
sh deploy-nas/scripts/rotate-collab-keys.sh queue-prepare
sh deploy-nas/scripts/rotate-collab-keys.sh queue-export > /private/public-queue.tar
```

默认 14 份，每份 24h、签发时间相隔 12h，连续覆盖约 7.5 天。首份对齐 UTC 00:00/12:00，准备时必须留出 6–12h。固定 12h 网格并非每天所有时刻都有 6–12h 后的点，因此每周固定在 UTC 00:00–06:00 或 12:00–18:00 准备；其余时刻明确拒绝并给出下一次准备时间，不缩短窗口。首份的前驱由当前 PG 的精确 signed 摘要出发，沿已经批准的队列推到接管前最后一份，期间不发布或改写历史。

导出只含 `batch.json`、`base/{signed.json,announcement.json,root.der}`（已签前驱及公开根），与各版本的 payload/trust/input 三个公开 JSON。导出不含 install-input 或 keys，不取回 NAS 业务私钥。在离线运营者专用目录解包，独立核 tenant/realm/base/source、原根 kid/指纹与新生产 operator jar 的摘要。签名机要求首份位于真实时间前 5 分钟至后 24h 内且对齐网格，超过窗口重新准备更高版本。

```sh
python3 deploy-nas/scripts/sign-collab-queue.py \
  --request /private/public-queue --output /private/approved-week \
  --review-state /private/local-weekly-review \
  --jar /private/platform-exec.jar --jar-sha256 APPROVED_SHA256 \
  --expected-nas-jar-sha256 HISTORICALLY_APPROVED_NAS_SHA256 \
  --root-kid original-root-kid --root-fingerprint sha256:PINNED_ROOT_SHA256 \
  --root-private /offline/root-private.der --approved-by alice
```

脚本先用固定生产 `OfflineKeySigning verify-archived` 核验前驱的双根签与独立根指纹，不读根私钥、不安装状态。再逐份比较：旧 kid/材料/用途/期限不变，不遗漏历史；仅允许同状态、next→active、active→retired 或→revoked；新增只能是 Ed25519 next、符合 nas-purpose-hex 命名，每用途最多一把，原 active 剩余不超过 48h 且没有现有 next，寿命不超过 7 天。Java 在签名及当前/历史验签时另设 8 天业务钥上限，生产 PG 仍强制真实首发成熟期。

签名机 `--jar-sha256` 与 `--expected-nas-jar-sha256` 分别独立核准，允许不同。前者核本机发布版 jar 字节，用于历史验签与新签名；后者核每份 trust 中 NAS 历史固定的 jar，只作 NAS 运行时绑定核对。审阅输出展示两个摘要，审批 reason 记录二者。选择拆开校验是因为根签格式不变，NAS 旧生产 jar 仍可验签/发布；不迁移或重写旧 binding，不上传签名机新 jar。NAS 的 8 天纵深检查仍需另行升级才能生效。

全部核对通过后打印新增 kid、SPKI 指纹、生效区间和 batch SHA256。**必须先执行同一命令加 `--review-only`，逐项审阅完整输出，再以其 batch SHA256 执行 `--confirm-batch-sha256 REVIEWED_SHA256`**；不能只对 batch 文件计算摘要就视作审阅。脚本将最近一次完整审阅写入 `--review-state` 的 `latest.json`，该运营者专用目录必须位于本机，独立于请求与输出目录，不随批次传输。签名时强制核对机器/用户、批次、新钥摘要、请求与输出位置、两个 JAR pin、原根及批准人，审阅超过一小时、未来时间或任一不符均拒绝，要求重新只审阅；交互确认同样不能绕过。该记录约束操作顺序，不抵御能改写签名机文件的同用户攻击者。没有通用跳过确认开关。旧签名 jar 不支持公开历史验签会拒绝，应在签名机使用经验证的新发布 jar，不能直接替换 NAS 历史 binding 中固定的 jar。

签名 JAR 的核准摘要只从独立核准的 Auriga 发布构建（CI 构建记录或本机完整验证的发行构建）取得，保留源码 commit、构建日志及制品 SHA256 对照；不得从 NAS 导出文件取签名 JAR 的信任 pin。审批 reason 中的精确 JAR 摘要用于关联这些发行证据，不把 NAS 的 trust.json 当来源证明。

升级含 8 天限制的 operator/collab 镜像之前，执行公开只读命令 `sh deploy-nas/scripts/rotate-collab-keys.sh history-check`。它离线核不可变归档的原根双签名，再对清单全部历史逐把核 `0 < not_after - not_before ≤ 8 天`（包括 retired/revoked、三种用途），输出版本、已检查钥数及清单摘要；bootstrap 本身不调用 Engine 或 PG，容器安装模式仍通过已有 Docker 启动无网络 helper。PG 已运行且就绪后，可用 `history-check-pg` 通过生产 status 核对 PG 当前 pair 与同一归档；该只读命令对已停或尚未就绪 PG 明确拒绝，不启动或等待 PG。新 collab 镜像在 `start.sh` 的消息启动段（或下述消息恢复命令）执行 Compose up 时生效，该处强制执行离线历史与 PG 当前 pair 门禁。`runtime-upgrade.sh` 负责实例 DSH runtime 发行，不启动新 collab 镜像，不再因无关消息历史检查失败或 PG 未就绪而拒绝 stage；其原有签名、序列、服务就绪和回滚要求仍有效。固定 operator JAR 仍不可直接替换。升级新 JAR 时，运营者**必须另用经独立核准的新 JAR 的只读 `verify-archived` 对同一对 signed/announcement 和原根指纹复核**，保留新 JAR 摘要及输出，避免 Go/Java 规则将来不同步；任一超限或拒绝均停止该消息规则升级，不删历史。

普通 `preflight.sh` 只检查主机、材料与镜像前提，不运行消息历史或 PG 观察器。`start.sh` 先启动核心服务，再调用 `start-collab-network.sh`。消息段 `startup-check` 对续期锁最多等待 30 秒，对本次核心启动已拉起且正在运行的 PG 最多等待 120 秒健康，再核当前 pair；观察器始终不创建或启动已停的 PG。失败输出 `message_network=degraded` 和恢复命令，仅停止消息组件，核心服务保持可用。消息路径读取、history-check、check、pause/resume 仍遇锁立即拒绝；发布和续期事务保持原有完整互斥锁。生成配置拿不到轮换快照时，保留已有动态签名投影并继续生成静态配置、网络预算与令牌目录；消息检查成功后重新生成完整投影。Compose 保留消息服务定义供 stop/down 使用，缺少已核快照时仅拒绝消息消费者的 up，不阻止核心配置、核心启动或停止操作。首次无轮换归档的安装仍通过原始材料验签与已运行 PG 健康检查，再启动消息组件。

小时任务发现消息消费者缺失、已停、不健康或续期失败时，会在 JSON `warning` 和 `messageRecoveryCommand` 中给出带实际数据根、配方路径的可执行命令。修复 PG、历史或锁条件后执行，例如：

```sh
AURIGA_DATA_ROOT='/share/Container/auriga-test' sh '/absolute/deploy-nas/scripts/start-collab-network.sh'
```

该命令重新执行同一有界门禁、刷新投影并启动消息组件，无须再跑整站 `start.sh`；核心和 PG 须已运行。采用明确恢复命令方案，小时控制器不增加 Docker Compose CLI 或重建服务权限。运营者主动 pause 时不提示恢复；`AURIGA_MESSAGE_NETWORK_OFFLINE=1` 仍保持消息离线。

`start.sh` 的核心配置生成不运行消息投影；消息配置在单独的非致命边界内生成，任一步骤失败都记录降级，核心启动省略失败的消息挂载。拿不到轮换快照且已有网络策略时，跳过网络预算投影，原文件保持不变；没有策略时才使用封存初始材料生成。消息启动仍须通过原有门禁，不能以核心启动成功代替消息 READY。

当前 `status.sh` 不主动验证消息网络 READY，也不会推送消息降级告警。运维须查看 start/恢复命令的 `message_network` 输出，以及 `collab-renew` 的小时 JSON 日志和消息容器健康状态；出现降级时按日志给出的命令恢复，不能只凭核心服务正常判断消息可用。此限制仍待后续独立改进。

普通 `start.sh` 和消息恢复命令在非离线模式下会恢复续期调度，清除此前手动设置的 pause；单独 pause 不会跨下一次普通 start 保持。需要持续离线维护时使用 `sh deploy-nas/scripts/start.sh --network-offline`，并在需要跨后续调用保持离线时设置 `AURIGA_MESSAGE_NETWORK_OFFLINE=1`，恢复前明确清除该开关。

覆盖不足 48h 的小时任务告警会附当前或下一个可准备时段的准确 UTC 起止：UTC 00:00–06:00、12:00–18:00（北京时间 08:00–14:00、20:00–次日02:00，截止点可用）。正在窗口内则立即准备，错过后不能调钟或缩短离线窗口。

Windows 输出目录的父目录也必须是运营者专用私有目录，不得授予其他用户 DELETE/FILE_DELETE_CHILD；子目录 DACL 不能防止非属主从父目录整体替换它。合并前检查清单必须包含下述 Windows 生产 jar 集成测试；Linux CI 不能替代其 ACL 验证。

原根私钥路径仅交给本机 Java，不复制、不回显。输出必须全新目录，失败保留部分产物，不覆盖已签同版本。未来 issued_at 仅用于预签；生产 Java 仍拒绝提前发布。把整个 approved-week 目录交回 NAS 外部 root 0700/文件 0600 的批准输入目录（不含任何私钥），导入前核摘要：

Windows 上，批签工具用系统安全描述符在创建输出目录的同时设置当前用户专属、禁止继承的 DACL，子文件只继承该用户权限。不能用先创建再放宽/修补 ACL 的方式签名，也不改变原根文件或生产 Java 的私密性校验。Python 3.13+ 的 `mkdir(mode=0700)` 会生成 Java 无法按具体属主识别的 `OWNER RIGHTS` ACE，因此不能替代这里的显式 DACL。构建上述生产 operator jar 后，执行 `python deploy/nas/test/collab_queue_windows_integration_test.py -v`，测试会临时生成独立测试根，真实调用生产 Java 签名和验签，并检查暴露根拒绝与禁止覆盖；缺 jar 在 Windows 上算失败。

```sh
sh deploy-nas/scripts/rotate-collab-keys.sh queue-import /private/approved-week
sh deploy-nas/scripts/rotate-collab-keys.sh renew
sh deploy-nas/scripts/schedule-collab-renewal.sh
```

导入先验证全部根签、announcement、审批、原始请求、前驱快照和计划摘要，再核新批次的前驱是否仍等于当前 PG 或沿有效旧队列可达的接管前驱。旧队列在离线签名期间推进后，如果新前驱已经脱节，明确拒绝，要求重新 prepare/export/sign/import，保留旧队列。验证全部通过才追加同字节材料；重复导入只允许逐字节相同且仍可衔接。

小时任务优先使用可衔接的最新批次，未来前驱未发布时由旧队列继续推进，未到签发时刻不发布。新批次元数据、前驱或材料失效时退回仍有效的旧队列，并输出 `queue_invalidated_fallback`、真实 coverageUntil 和重新准备/审批的动作；无可用旧队列则输出 `queue_renewal_failed`，不回滚 PG。剩余覆盖 ≤48h 继续明确告警“预签队列即将用完”。首次导入后执行 renew 只核验等待状态，到点由小时任务发布。

紧急吊销使用现有人工签发流程：版本必须 **高于全部已排队版本**，保留完整历史，根签并 publish/install/start。PG 或本地最新发布一旦跳出队列的 base/成员版本，旧队列全部拒绝使用，不回退、不补发。必须根据紧急批次的新真实 PG 状态重新 queue-prepare 并离线审批。不要更改系统时钟来加速预签验收。

本地 receipt 链只防误改，不能抵御 NAS root 重写；同宿主 PG/JVM 时钟差检查也不能抵御宿主调钟。调快时钟最多提前消耗已获原根批准的队列，不能签出新材料，但可能使真实时间下的客户端拒收或提前到期。请求私钥与 activation 备份只追加，保留策略和 sidecar docker.sock 权限收敛另行评估，不自动删除现场历史。

## 中断恢复与审计

```sh
sh deploy-nas/scripts/rotate-collab-keys.sh status
sh deploy-nas/scripts/rotate-collab-keys.sh recover-pending
```

恢复命令在同一有界锁内核 intent、原 state 摘要、准确 PG pair 与审批输入。PG/原 state 未变则把 pending 移入 `failures`，允许原输入重试；PG 已准确提交候选则补本地 publication receipt，随后显式 install；install 已推进 state 则用原 before-state 快照经生产 loader 重入，或核完整安装 receipt/文件集后补 selector。失败现场从不退 PG 或回写旧 state。恢复后正常 start；仅配置信息切换失败时也可公开 start 重新创建固定服务，不需改容器挂载。

`.preparing` 只含未签请求，下一次准备会自动归档前次未完成暂存。`.pending` 缺失/不可信 intent、PG 是第三个版本、state 元数据异常或 before-state 快照缺失时，恢复明确拒绝；运营者保留现场并排查真实 PG/文件属主，不删除 pending/state.lock，不伪造 firstPublished。旧版本没有 intent 的 pending 也需人工审查，不能自动采纳。此时公开 start 仍保证核心服务可用，消息网络维持降级。重建签名容器中断时保留 root 私有 `activation-<service>-<id>.json` 作为原定义证据，按公开 start 恢复。

新 publication receipt 归档原始 approval/payload 与 `requireClientRefresh`，兼容旧三文件 receipt。receipt 链只防误改和半提交，不能防已取得 NAS root 的人篡改；PG 与客户端独立反回退仍是最终门禁。同宿主 PG/应用时钟比较只发现配置错误，不能抵抗修改宿主时钟。revoked 历史钥也必须全部过期后才允许独立过期恢复，保留这个保守条件。

业务私钥不自动清理：先确认不再是 active/next、无排队/回滚审计引用，再由运营者按批准的保留策略离线加密归档；不在此流程删除 NAS 数据。续期 sidecar 的 root + docker.sock 权限风险与长期新鲜度钥设计另列主控 backlog，本轮不宣称已经隔离。插件模型状态每次 GET 的探测/内网 HTTP 边界及 bidi 显示名处理另行评估；自有 cookie jar 使用 fetch credentials=omit，避免同时混入宿主 cookie，保持现有行为。

## 临时单份根签

没有使用周队列时，运营者仍至少每 12h 完成一次新鲜请求签名。以下为既有手工流程，不能替代周队列覆盖告警。

运营者至少每 12 小时处理一次新请求，完成根签、明确审计记录、发布/安装和公开 start。
清单上限仍是 24 小时，小时巡检不会自动延长它；仅重复同版下载不会续期。
每次 PG 新版都会 fence 旧 payload，不能只换公钥 JSON 而不安装同版 state。
业务 next 的真实首发满 24 小时后，还须在旧 active 截止前至少 360 秒完成激活根签与安装；
按 `earliestNextActivation` 安排额外操作，不能只等下一个固定 12 小时班次。

只把请求中的 `payload.json` 和 `trust.json` 交给本机离线根设施。
先对照原独立受信记录核 tenant、realm、root kid/指纹、base/source 和批准的平台镜像；
NAS 上的业务私钥不必为根签而取回。运营者使用已独立核摘要的可执行 jar 和原根私钥：

```sh
java -Dloader.main=auriga.collab.network.keys.OfflineKeySigning \
  -Dloader.config.location=file:/private/empty-loader.properties \
  -cp /private/platform-exec.jar org.springframework.boot.loader.launch.PropertiesLauncher \
  sign /private/request/payload.json original-root-kid /offline/root-private.der \
  /private/new-release/signed.json /private/new-release/announcement.json
```

输出必须是新文件，父目录仅运营者可访问。不得把根私钥放入 NAS、镜像、shell 参数值或日志。
在同一私有操作中创建 `approval.json`，字段为 `approvedBy`、`reason`、`payloadSha256`、
`signedSha256`、`announcementSha256`，摘要针对上述三个文件的准确原始字节（小写 SHA-256 hex）。
这是明确的人工审计记录，根签仍是密码学发布权威。将 signed、announcement、approval 三个新文件
交回准确的 NAS requestDirectory，保留请求原文件和私钥；不要覆盖已有已签候选或审批记录。

## 发布、安装与正常恢复

```sh
sh deploy-nas/scripts/rotate-collab-keys.sh publish /private/approved-request
sh deploy-nas/scripts/rotate-collab-keys.sh status
sh deploy-nas/scripts/rotate-collab-keys.sh install /private/approved-request
sh deploy-nas/scripts/start.sh
```

`publish` 使用 input.json（不得含私钥映射）；`install` 使用同目录 install-input.json，
必须精确匹配最新已发布 pair 及全部 active/next 私钥。生产预检在停机前完成；正式提交前
再停受信签名进程，确认没有其他活跃写者占用原 state。失败不写成功 receipt。
start/render 读取已核 selector，保持其他预算/网关字段；只有消息网络在启动前核真实 PG、原 state 与当前有效期。
同版、同字节和同私钥重入只读复用；改变同版或回退均拒绝。

若使用普通的分阶段过期恢复，首版必须是“旧 active→retired + 新 next”，旧 kid 其他字段全部不变。
发布后没有三个有效 active，install 会拒绝，start 只让消息网络降级；核心服务继续，小时巡检可继续。
从 status 的新 kid `firstPublished` 最大值加 86400 秒，并向上取整到整秒，才是最早激活时间。
本机生成时间、签名时间和文件时间不能替代真实 PG 首发。

成熟后由巡检/prepare 生成更高版“旧保持 retired + next→active”，运营者再次根签并
publish/install/start。等待期若需持续清单有效，仍按 12 小时周期根签更高版同一 retired/next 状态。
恢复后分别验证实际根签 keys/announcement、三用途签发、网关 protocol、真实设备 READY；
服务健康或公钥 GET 成功不能代替这些证据。自动巡检和离线候选也不能代替现场验收。

## 经批准的独立过期恢复

仅在上述全部截止与时钟条件成立时执行一次 `prepare-recovery`，并核对请求 `publicationMode`
为 `expired_recovery`。沿相同人工根签步骤签发，审批增加 `requireClientRefresh: true`，
原因写明到期恢复、批准人及客户端刷新安排。恢复不会由小时巡检自动触发。

```sh
sh deploy-nas/scripts/rotate-collab-keys.sh prepare-recovery
# 离线核对并根签新请求，交回 signed/announcement/approval 三个新文件
sh deploy-nas/scripts/rotate-collab-keys.sh recover-expired /private/approved-recovery-request
sh deploy-nas/scripts/rotate-collab-keys.sh status
sh deploy-nas/scripts/rotate-collab-keys.sh install /private/approved-recovery-request
sh deploy-nas/scripts/start.sh
```

`preflight-recovery` 使用真实 PG 事务校验后回滚，正式 `recover-expired` 在停签与独占锁后提交。
核对恢复 receipt、实际 PG 新版及真实 firstPublished，安排所有客户端重新获取原根签名的完整 keyset，
再验证真实设备签发与 READY。未刷新客户端继续拒绝旧清单，不通过删除缓存或回退版本恢复。
