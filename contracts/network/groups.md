# 独立组/广播应用端口（NETN1-GROUPS）

本增补定义 collab 内部 Java 应用接缝，不新增公网路由/模型工具，不改现有线上帧。
入口为 `auriga.collab.api.network.group.NetworkGroups`；实现仅消费已认证的租户上下文。
整数地址段使用规范十进制 uint64，展示地址仍按现有契约转十六进制。
普通组 local 从 2 单调分配，广播保留 local=1；组地址不得作为来源。

## 信任与事务

`NetworkGroupAuthority.current` 只能从专用认证入口取得真实人或宿主绑定，禁止从正文、
模型参数或自报信封恢复身份。HUMAN 只能本 node/sys；AGENT 只能精确 session/subagent。
`humanIntent` 必须由独立真人 UI 用户认证入口调用，产生 60 秒一次性意图；
绑定操作者、源绑定、规范目标、文本摘要、线程、幂等键、期限和派发模式。
消费与 send 同事务；相同键只返回原消息，不重新取成员名单。
humanUnicastIntent/sendHumanUnicast 提供 sys 来源的独立人身份单播路径，绑定完整目标地址，
与组消息/非耐久消息使用不同操作摘要。humanUnicast 事务端口保留预分配 ID，必须复用
直接关系或共享 FIRST_CONTACT/human_dialogue；无关系不冒成已投递，不创建会话/启动模型。
`Send` 仅接纳文本 information/request，不接受 from/class/sender_kind、控制 schema、flags 或授权引用。
后续 HTTP/MCP 适配必须拒绝未知顶层字段，不能先反序列化丢字段后放行。

全部权威改动使用相同 collab 数据源和 TenantTransactionManager，并持短事务租户栅栏。
Authority 只能消费已验证证据/事务一致投影；不得持数据库锁等待 HTTP/NATS/宿主。
IAM 不可用失败关闭。跨服务入口、生产授权投影和签名由 J1/J2 集成，缺适配时不注册 Bean，
没有默认允许的 Authority 或 Mailbox 实现。

## 成员生命周期

create 默认 32 成员/8 node、每用户 10 活跃组，创建者精确地址作为首成员。
capacity 必须总部管理授权，最多 1,024 成员/256 node；不自动增员。
发言默认 members；admins_only 同时核精确成员实例和组内管理员。
delegate 仅真人 owner，可委派现有成员管理权，不授广播权。
invite 必须走共享 FIRST_CONTACT/group_join：sys 或已有直接关系 local、共享源用户/node/local
预算、目标隐私、拒后冷却和屏蔽。reserveInvitation 返回耐久 contact 引用，绝不另造免审通道。
join 要求邀请未过期、邀请/组版本一致、邀请者仍有权，目标插件 approveJoin 验证当前获准
contact、真实用户同意及已耐久保存的本地组许可。重试不加第二成员；退出重入用新 membership_id。
组内成员不获得私聊/历史/模型执行许可。leave/delete/发言权变化终结尚未接受的受影响快照。

## 发送与派发

send 固定不可变接收快照并预分配逐 local recipient_id，按 node 合并。
同一用户两个 DSH 的 sys 分别计数；后加入/新注册 node 不补旧消息。
小批最多 32 node/256 locals，任一预检失败全回滚；超出必须显式 PAGED。
普通组最多 1,024 成员/256 node，广播最多 10,000 sys。
每 node 最多 256 locals；Mailbox.preflight 必须按最终签名 FETCH 编码检查 64KiB。
Mailbox.store 是数据库事务端口，引用固定 messageId/recipientId，不能逐页调用旧 store 生成新消息。
页面最多 32 node，邮箱写入、逐项 dispatched 与预算进度同事务；失败整页回滚。
同线程/组内按事务分配的数据库 position 排序，fetch 按 local 阻挡更早待决项；
一个 local 未决不会挡住同 node 其他 local 已可接收的消息。
HEAD 只能在提交后发。源/目标权限、成员实例、冻结和广播 grant 每页及 fetch 重核。
正文只保留在固定 messageId 对应的消息记录，不能每 local 复制；适配器应引用计划正文，
不得把每页当新消息。所有 recipient 状态闭合前不允许回收共享正文。
状态返回 pending/complete 仅表示邮箱派发，不表示用户阅读或模型执行。

## 广播与管理

广播默认无人拥有，管理员角色也不能代替显式 grant。
USER 仅本人 HUMAN；NODE 还绑定实际用户及 node；AGENT 独立绑定 user/node/local/source_binding。
期限最长 24 小时，不自动续；权限/规模只能收紧组织上限。
授权来源每用户 1 次/分钟（突发 1），tenant 5 次/分钟；换 local/node/组不重置用户预算。
普通组源 node 60 次/分钟（突发 10），每组 300 次/分钟。
计划/快照另受有界 tenant 待决数、正文传输字节预留限制。拒绝事件独立事务留审计，
默认每用户最多 10 待决计划、10,000 目标 node 预留、64MiB 传输正文预留，
tenant 最多 100 待决计划、100,000 recipient、1GiB 传输正文预留；checkSend 再取组织上限。
不保存正文、human 意图句柄或原始异常。
revoke/freeze/recall 必须独立管理认证、幂等键、预期版本及审计理由；不是业务 control 消息。
freeze 阻止新发件并撤销匹配 grant，解冻不恢复旧终态或旧 grant。
recall 递增消息状态版本；未接受项统一 rejected，已接受仍 accepted，另标 recalled。
按原 node 快照持久写独立 state_update，已 ACK 的 node 也要同步。
冻结/解冻先提交 scope 的权威版本并阻止后续发送，历史 node 通知每次最多展开 32 项；
scope 状态更新带 subject/frozen，版本属于该 scope，不冒充消息撤回版本。
pendingUpdates/updateCommitted 仅内部 worker 使用；生产适配需经专用签名状态用途和可靠邮箱
提交后确认，客户端先耐久 tombstone 再隐藏正文，离线重连先同步状态再展示缓存。

## 收件、回应与统计

所有 Item.inboxOnly 固定 true，execution_status=not_applicable；不提供执行准入。
fetch 按当前目标精确 local 裁剪；历史 accepted 保留，不因发送 grant 到期改成失败。
receipt 必须由目标身份和耐久证据验证端口核验，逐 recipient 幂等，不允许整 node ACK。
status 只供原认证发送者，返回互斥收件/回应计数，分母始终为原 totalMembers，不泄露逐地址名单。
respond 先经 verifyResponse 校验已提交的窄私信/显式拒绝证据，再记录业务回应状态，
与收件 ACK 分开；每原 recipient 一次最终回应、最多 3 次修订，
截止不超过原 TTL/24 小时。实际私信应由 J2 原消息/原 recipient 的窄回复端口生成，
不能把回应状态更新冒作已发私信。reply_all 必须显式新 send 并重新核当前成员/广播权。

prepareEphemeral 仅允许 progress/online、information、普通组和最长 30 秒 TTL；
校验当前成员/发言权/目标政策并沿用源与组预算，返回短时快照，不保存正文、消息或回执。
真人进度意图用独立 humanEphemeralIntent，绑定事件种类/版本，不能挪用耐久消息意图。
Core adapter 发布前仍须逐 node 复核权威成员/策略，只用内部精确 subject，不授 DSH 共享组订阅。
丢失/重复/断线均不补传、不冒 platform_stored，不触发模型。

本端口是服务端模块接缝，尚不代表插件 UI、真实双 DSH、Core adapter、容量/HA 已验收。
