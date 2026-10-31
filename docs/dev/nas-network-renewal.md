# NAS network 密钥到期恢复与续期方案

本文说明现有实现边界和待实现的 NAS 运维接线，不是已完成的 NAS 轮换入口。
权威规则见 [密钥运维契约](../../contracts/network/key-operations.md)，
现有 Compose 轮换与 expired-old 流程见 [M2 运维说明](m2.md)。

## 两种有效期

根签 keyset 的 `valid_until - issued_at` 最多 86400 秒，
由生产 `RootKeyPublication.checkPayload` 和共用安装输入读取器共同拒绝超限。
业务钥的 `not_after` 独立于清单有效期；不能靠重签清单延长同一 kid 的期限。
一次性 `generate-test-approval.mjs` 把初始三用途钥也设为 24 小时，
适合明确授权的一次性测试，不适合持续运行。该工具生成新根，不能用于已有网络续期。

## 现有 NAS 边界

`activate-collab.sh` 只负责首次激活。已存在的 source 会核原收据、
安装输入摘要和运行副本，输入变化会被拒绝；`start.sh` 不执行密钥轮换。
安装输入只接受三个 active 用途，不接受带历史的 retired/next 清单。
`collab-materials.sh` 也不会覆盖与封存源不同的运行文件。
因此不能把新签文件覆盖进原审批目录/source/runtime，再执行 start 冒充合法轮换。

N1 的 `rotate-collab-keys.sh` 入口固定绑定 `deploy/compose/var`、
封存 project、recipe、jar 和挂载关系，不能直接用于 NAS 的 data root。
NAS 需要专门的适配器，复用生产 `NetworkKeyOperator` 的 PG 权威校验；
不能通过拼造 Compose var、手工 SQL、删除 receipt/state.lock 或修改原 source 来接入。

## 已过期旧钥的恢复

先只读核对原 root 指纹、tenant/realm、PG 高版本、完整 first_published、
原物理 state/lock、当前配方和镜像。离线保留原历史，批准更高版本：
旧 active 明确转为 retired，三把新钥只进入 next，旧 kid 的其他字段全部不变。
仅离线签名/验签不产生 PG 首发时间，也不表示已安装或已恢复。

NAS 适配器应先执行生产预检，再在部署锁内按固定 project 停止签名服务、
调用生产 publish，并追加绑定原 base/source/root/版本和实际 PG 返回的 publication receipt。
等待期不安装 next-only 运行栈，不宣称公钥服务、业务签名或 fanout 已恢复。
真实 PG 首发至少满 86400 秒且向上取整到整秒后，另签更高版本 next→active；
旧项保持 retired。再 publish/install，同版加载三用途私钥及 gateway protocol，最后公开 start。
新 active 未成熟前不得提前激活；已过期旧 active 不能在激活同一版中才 retired 来绕过重叠检查。

追加式 NAS 安装记录应与原 collab-source 分开：保原 state 物理路径和锁，
新配置/私钥放版本目录，install receipt 绑定 PG 发布回执和 state 摘要，活动选择器只指向已提交版本。
render/start 必须先验证该分支的完整归档、PG 高水位、实际挂载及当前 active 期限，
避免再用初装投影覆盖新状态。pending 或半提交要阻断启动，并保留固定 stop/running 逃生入口。

## 自动续期方案（待实现）

采用“本机离线批准发布 + NAS 消费已签包”的分工。根私钥仍留本机私有目录，
不放 NAS、镜像、在线服务或 panel-renew sidecar。面板续签 sidecar 的定时/退避机制可参考，
但其持钥在线续签方式不能直接照搬为 network 根签名器。

建议离线作业每 12 小时批准新的 24 小时清单并原子交付完整 signed/announcement 对，
每次提高版本，保留全部历史及 PG first_published。仅重复下载同版不会续期。
每次发布都会 fence 旧 payload，因此消费端必须按同版完整安装 state/密钥映射并恢复服务，
不能只替换公钥 JSON。失败保留 pending 和首失，禁止自动回退 PG 或重置历史。

业务钥建议采用独立的 7 天寿命：提前至少 48 小时发布 next，
成熟且旧 active 仍覆盖激活时刻后 360 秒时才切换，保留 retired 历史。
必须同时监控清单剩余寿命、active/next 期限、PG 成熟时间、交付积压及实际签名状态；
清单不足 6 小时或轮换窗口不足时告警。离线作业不可用时维持硬截止，不能自动延长旧钥。

NAS 消费调度应复用公开安装器、固定 project 与部署锁，先预检再停机；
同版同字节/同绑定重入只读，外来 project/容器/挂载、回退、改同版、缺历史、
过早激活、原 state 锁占用和半提交均拒绝。NAS lock 与本机部署锁的跨执行机互斥需同时设计。

验收至少覆盖真实 PG 的正常轮换和 expired-old 分阶段恢复、亚秒首发向上取整、
86400/360 秒边界、原 root/receipt/state.lock 不变、同版重入/故障断点、
不同实例挂载拒绝、容器安装模式的非 root 运维以及轮换后的真实 READY。
上述适配器与调度尚未实现，不能以这份方案或离线候选包代替现场通过证据。
