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
新记录放在 `config/collab-operations`：

- `binding.json` 绑定原 base/source 收据、固定数据根、配方路径、明确批准的平台镜像本地 ID 和可选独立 operator jar 摘要。
- `releases/<version>` 追加完整 signed/announcement、实际 PG 状态和 publication receipt 链。
- `installs/<version>` 追加配置、active/next 私钥、网关 protocol、切换前 state 快照及 install receipt。
- `requests/<version>` 保存未签请求及新业务私钥；待签内容不覆盖，不代表已发布或已安装。
- `current.json` 只选择完整提交的安装。原运行 state 由生产安装器原子更新，旧字节另外保留；原 state.lock 不删除或重建。

所有变更持同一个 NAS 文件锁。PG 发布后未安装、版本回退、同版本改字节、缺历史、
元数据/挂载漂移或半提交都阻断启动。`.pending` 与首失证据保留，不能删除后盲重试。
`rotate-collab-keys.sh stop` 是固定项目的停签逃生入口，不修 state、不改 PG 或 selector。

普通安装/render helper 不挂 Docker socket。只有显式轮换命令和受信续期任务接入本机 socket；
Java helper 仅使用批准镜像、固定主类与固定计划，数据根只读，写范围限本次暂存及必要的原 state 目录。
PG 操作先核现有数据库容器、数据挂载和内部网桥；数据库缺失不创建空库。
发布会停止该项目的 edge/network/collab，保留 platform、实例及数据卷。

## 准备请求与启动小时巡检

使用更新后的公开 NAS 包和 linux/amd64 bootstrap（0755）。`stack.env` 中的平台/安装器镜像
必须是独立核准、已存在本机的不可变引用。更换绑定的平台镜像或配方路径须另行运维批次。
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

`collab-renew` 使用现有安装器镜像、无网络、只读数据根及专用可写 operations 目录。
它每小时检查一次，复用未到期请求，清单剩余不足 6 小时输出
`keyset_expires_within_6h_operator_signature_required`；清单仍充足而任一 active 业务钥剩余不超过 6 小时时，
输出 `business_keys_expire_within_6h_operator_action_required`。失败只告警并保留证据，不延长旧钥或自动回滚。
该进程是持同一运维锁的受信只读 state 观察者；不会自行签根、发布或恢复签名服务。

## 运营者根签（每 12 小时）

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
start/render 读取已核 selector，保持其他预算/网关字段，启动前再核真实 PG、原 state 与当前有效期。
同版、同字节和同私钥重入只读复用；改变同版或回退均拒绝。

若使用普通的分阶段过期恢复，首版必须是“旧 active→retired + 新 next”，旧 kid 其他字段全部不变。
发布后没有三个有效 active，install 和 start 会明确拒绝；签名服务保持停止，小时巡检可继续。
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
