# 网络协议、授权与宿主恢复

状态：待评审协议提案。不是现有契约。本篇与 [总览](README.zh.md)、[验收](failure-validation.zh.md) 配套；后续必须先生成正式 schema/protobuf 和一致性测试，禁止各端私加同名字段。

## 1. 稳定身份与临时标识

| 标识 | 分配者、范围与含义 |
|---|---|
| tenant_id / realm / owner_user_id | IAM/org 决定 tenant 与节点归属用户；realm 是独立顺序 uint64 组织/部署域 ID，由总部验证其与认证 tenant 的绑定，不能把 tenant UUID 当 realm。首发仅本 realm。owner_user_id 仅归属属性，不进入地址，不能合并同用户的多个 node。 |
| node_id | 总部在 realm 内从 1 顺序分配的 uint64，永不回收复用、0 专留总部 group 命名空间而不分配给 DSH；本地/云端/总部 DSH 各占一个，初期 endpoint 映射需显式迁移。 |
| local_id | DSH 插件持久计数器从 1 递增分配的 uint64，在该 node 内永不回收复用；0 是 system 收件箱，其他值映射本地 session/subagent。总部只做不透明值比较和计数。 |
| address_version / address_type | 地址结构版本；类型 system/session/subagent/group（group 由总部管理）。不是额外路由段或授权；同一 node/local 不得通过换类型变成第二身份。 |
| node_incarnation | 平台批准的实例注册世代；普通原实例重启不清零 local，重装/克隆必须新注册 node，不能借新世代继承旧地址。 |
| connection_epoch（连接世代） | PG 接管连接时原子递增 uint64，关联 ingress；不改变稳定地址、delivery 或执行准入记录。 |
| binding_id / host_session_id | 插件本地持久映射 local→binding→真实宿主 session；总部不建立宿主 session 外键，不解析其内部 ID。 |
| grant_id / policy_revision / lineage | 会话 grant、父子/root 和递归预算由插件依据可信宿主维护；总部保 node 策略版本和不透明对话关系的撤销版本。 |
| channel_id | 插件逻辑通道，关联本地 binding 与完整地址；不等于 NATS socket/consumer。 |
| thread_id / thread_sequence | 平台持久对话及提交序号，参与者为完整不透明地址；不是宿主会话树。 |
| message_id / delivery_id / recipient_id | 一份不可变正文 / 每目标 node 一条邮箱项 / 邮箱项内每个 local 的稳定接收项；重试均不变，宿主 requestId、尝试次数与执行准入以 recipient_id 去重。 |
| sender_kind / message_class / intent | 总部按已核验来源授权生成 human/agent，按目的地址生成 unicast/multicast/broadcast；intent 区分 information/request 与仅单播的执行控制，不由自报类别授予权限。 |
| broadcast_grant_id / server_auth | 总部独立高危广播授予引用及版本 / 必需的服务器下行签名封装；均不是客户端可申请的任意信封字段。 |
| contact_id | kind=first_contact 的原 message_id；不是另一个无地址暂存对象，目标固定为系统地址。 |
| request_id | 单次协议请求关联，不提供业务幂等。 |
| producer_epoch + idempotency_key | 源操作幂等域 (tenant, source_node, source_local, operation, producer_epoch, key)，同键同体返回原结果，异体拒绝。 |
| mail_seq / resume_cursor | node 邮箱序号与连续确认前缀；scan_after 另作分页位置，均不是执行进度。 |

**地址编码**：v1 文本为 `aur1:[realm.]node.local`，各段无前导零小写十六进制、最多 16 位、uint64 精确解析；realm≥1，真实 DSH node≥1，系统 local=0，普通会话/子会话 local≥1。本域 realm=2 时 `aur1:3f.1a2` 与 `aur1:2.3f.1a2` 等价，系统为 `aur1:2.3f.0`。JSON 始终完整：`{"version":1,"realm":"2","type":"session","node":"63","local":"418"}`，数字用无前导零十进制字符串，不经 JS number。两段仅在已认证本域上下文补 realm；入库、摘要、幂等与关系键先规范成完整三段，重复两种写法只得同一结果。拒绝大写、补零、符号、空段、溢出及一段/四段以上地址；更多结构须新版本 aur2，v1 不猜测解析。子会话仍独立 local，父子只在插件内部。

路由先核 realm→是否外域，再 node→DSH，最后插件 local→会话。首发只准本 realm 或省略形式，其他 realm 返回 network.realm_not_supported，不查外部目录、不建外连；未来联邦须独立授权/协议，完整三段并不代表已支持。realm 由受信域注册机构从 1 顺序持久分配、永不复用，恢复/耗尽规则与 node 相同；无域注册依据不能由客户端自定。`type=group,node=0,local=group_id≥1` 为总部保留组空间，组也在本 realm；普通组如 `aur1:2.0.17`，隐式组织广播固定 `aur1:2.0.1`，普通 group_id 从 2 顺序分配且不复用。node=0/local=0 无效，group 不能作 from/reply_to，不能用修改 type 将系统或会话变成组。

**顺序分配与崩溃**：总部 node 计数器、插件 local 计数器均从 1 单调递增，先提交可恢复的事务日志/分配高水位，再把 ID 暴露给消息、宿主创建或外部调用。并发分配由唯一约束和原子计数保证；已预留但创建失败的 ID 也烧掉，不因回滚业务操作而回收。进程断电可留跳号但不能回退、重发旧号；到 uint64 最大值后拒绝新分配，不回绕。总部恢复备份须有独立耐久高水位证明，不能从旧备份继续发可能已用过的 node。插件恢复计数日志不完整则停止旧身份发件/分配并按重装流程注册新 node，不能扫描“当前还活着的会话”推算下一个 local。真实 DSH 的 local=0 始终是系统入口，不进入普通会话分配池；node=0 的组空间由总部独立管理。

**重装、克隆与墓碑**：重装和克隆一律获新 node，即使复制了本地库也不得带旧 node 凭据/关系继续联网；新 node 可从 local=1 开始，完整地址仍不同。原 node 若仍在原安装中使用不受新 node 注册影响；弃用的旧 node 和其地址永久保留不复用语义，新身份不继承旧 pending delivery、会话关系或执行准入。普通原安装进程重启保原 node、计数器和 ledger，重放原幂等操作；这不叫重装。意外复制凭据造成双连仍按已有 fencing/conflict 防重处理，是故障场景而非允许克隆接管的迁移流程。非零 local 对归档/关闭/删除留墓碑；重开仅可恢复原会话身份，不能指向另一个新会话，见 §1.3。

顺序编号可以猜，观察高号可推断 realm 内大致累计 DSH 分配规模；预留跳号和历史弃用使其不等于存活/在线数量。这是采用短顺序地址的明确取舍，地址不是秘密也不是凭据，保密依赖授权与统一回执。UI 短别名最多先取各段前 4 位，不足 4 位原样展示、冲突加长到完整原串；不能用于 API/去重/授权。local 层级只存在于插件内部 session/child 映射，不传 parent address 给总部或对端。

### 1.1 系统目录、对话关系与目标解析

对应[总览 §6.1](README.zh.md#61-dsh-内多-agent-的寻址与发现)。第 7 轮已替换 binding 通讯录；§1.2 保留首联用户授权，§1.3 增加插件独立防护，以下均为待契约化语义。

**发现与信任**：目录只返回有权发现的 DSH 系统地址、display_name、owner_user_id、online_state/status_observed_at；不提供会话搜索、精确 local 存在性查询、会话数量或冷热能力索引。来源插件以可信宿主上下文/真实用户入口取得 §1.5 的来源授权句柄；总部从认证连接重建 realm/node，从授权记录取 local/type/sender_kind，不能以请求自报 from 替代。human 固定 sys，agent 固定当前会话。插件保证 local→宿主隔离；总部不因此能独立证明已被攻陷宿主内部的真实操作者。这是职责下沉后的明确信任边界，不改变跨 node/tenant 鉴权。

**地址不是凭据**：总部每次操作核 IAM/node→node 策略及已存的完整地址关系，目标插件先核 §1.3 的本地关系、生命周期和接收策略，再核 receive/read/wake/control 等 grant；即使总部错误放行也不能省略。非系统单播目的必须有该目标通过已认证对话给当前来源的关系；组消息则须 §1.4 的显式本地入组授权，不能从普通成员名单推导许可。单播还要求该关系有效，且该 thread 的参与范围仍有效；知道地址、thread_id、归属用户或复制他人的信封均不建立关系。首发关系限原线程，不隐式授权新线程或全部历史；新线程需已有对话内双方确认的邀请。关系撤销与提交/准入按状态版本核验；插件本地撤销立即阻止新执行，再可靠上报总部，传输滞后不能声称全局零窗口。

| 发送意图 | 地址与关系规则 | 不得发生 |
|---|---|---|
| first_contact | to 为目标 node 的 system/local=0，from 为源插件已授权 agent 会话或经用户意图校验的 human/sys；总部核组织首联策略，有效目标原子保存线程、消息、系统 delivery、幂等和邮箱序号；contact_pending 留总部待用户授权，对外回执按 §1.2 裁剪 | 使用缺省前台、通过首联绕到任意非零 local、自动启动模型 |
| 已有对话 message | 单播 to 只能一个完整非 group 地址；组播/广播须显式组地址并按 §1.4 固化快照，thread 不是隐式多目标广播入口 | 仅凭猜到地址创建参与者、将同 node 两 local 合并 |
| reply | thread_id、in_reply_to 引用已存消息；默认 reply_mode=private，to=原已核验 reply_to（v1 等于 from）；发件人须是原接收者或首联唯一新地址。reply_mode=group 仅用于原组对话且按 §1.4 重新核当前成员/发言权 | 第三方 reply_to 重定向、跨线程引用、默认回复全组 |
| control | 只允许单播，同样核关系，另需节点策略与目标插件控制 grant；动作写入受支持 body schema | 靠 kind/flags 提权或绕过限额 |

普通精确发送缺完整目标返回 network.target_required；地址、type、reply 引用或目标集合矛盾返回 network.target_mismatch。没有关系/权限或猜测失效地址统一 network.target_unavailable，不泄露 local 是否存在。不存在、归档/关闭/删除、无权、被屏蔽的具体接收原因对外统一 network.target_unavailable，即使曾有关系也不暴露生命周期；本地诊断才有细分原因。通过关系与生命周期检查后的适配不兼容才可向有诊断权者报告 host_incompatible。平台尚未知本地拒绝时可先保存后异步给统一拒绝，不编造实时会话状态。成功响应列出实际完整 from/to，合法关系内选错联系人不能自动纠正。

**参与者与回执**：thread_participant UNIQUE(tenant,realm,thread,node,local)；delivery/node_mail_item UNIQUE(tenant,message,target_realm,target_node)；recipient_status UNIQUE(tenant,delivery_id,target_local)，每个成员有稳定 recipient_id。含 tenant 的外键和 realm 绑定保留，不建立总部 host_session 外键。同消息到同 node 两个 local（本轮须通过同一组快照）只产生一个 delivery_id/mail_seq、两个 recipient_id，各自核权、尝试、接受与执行；传输按 node 计量，执行按 local 计量。delivery 引正文，recipient 固化目标地址、关系/成员版本；HEAD 合并不丢接收状态。系统首联仍是一条 node 邮箱项内一个 sys recipient，原 to=sys 不改，assigned_address 另存。初始参与者为来源和 sys；首回复加入唯一 assigned_address 与来源关系，后续默认精确回复不把 sys 当群聊成员，不授其其他线程权。

human 从 A/sys 发起首联仍需 B 用户批准；B 后续回信到 A/sys 时仅进入人的系统收件箱，reply 不当作新的 first_contact，不自动创建 A 会话。A 插件按原 contact 保存待回复许可；human 与 agent 均不能靠 sys 地址绕过首联或普通消息关系。确定性的宿主地址介绍仍属已绑定会话来源 sender_kind=agent、provenance=host，不冒 human 或暗示已运行模型。

**系统首联与幂等建会话**：有效目标的 contact_id=首联 message_id，正常 message/delivery/mailbox/outbox/dedup 同事务保存后形成内部 platform_stored；面向发起方的可见状态遵守 §1.2，不将受限响应当成对目标存在性的证明。授权前状态为 contact_pending，消息正文与权威请求留在总部目标系统收件箱，未进入本地 agent inbox。插件只向已认证目标用户展示批准提示/限长预览；提示已读、预览读取及默认策略评估都不是 Host receipt，不能推进邮箱确认前缀，不能启动模型。离线时同样留存，期限不延长。

CONTACT_DECIDE 允许建立对话提交后，才进入 awaiting_session 并开放正常正文 FETCH/本地系统收件；本地耐久后可回 host_accepted 且必须带 accepted_by=system，仍非会话/模型接受。session_accepted 与 execution_completed 另报，四级 ACK 本义不变。授权前拒绝/到期生成无正文终态供游标跨过，不造 host_accepted；分页仍可越过未决位置服务其他 local，沿 §8 的空洞与节点配额约束。

本地用户允许建立对话（或本地该层默认档位允许）且总部 CONTACT_DECIDE 已提交后，插件以 (tenant,node,contact_id) 单写者锁持久预留唯一非零 local、host_session_id、cwd/preset 与创建状态，核本地建立对话/创建 grant；此阶段不要求或隐含自动执行授权。先向总部提交私有 CONTACT_ASSIGN CAS，固定该 contact 的 assigned_address 与 creation_attempt_id，只允许当前目标 node/epoch、contact_allowed 及有效决定版本、当前组织策略、未撤销关系和原期限；总部只存不透明分配结果，不解释宿主结构，也不向目录或发起方提前公开。重复同内容返回既有状态，改 local 冲突。创建许可沿 §5 的一次准入原则，记录获准 epoch/boot_id；仅第一次成功响应且本地 creation_started 标记已持久化的原进程可调用一次 create。回包丢失/崩溃/意外凭据克隆接管的故障后只查原映射、原生 ID 和原持久存储，不能凭查询结果重新获得创建许可。确认原会话存在才继续后续阶段，无法证明则 outcome_unknown 并停止自动创建，允许零次创建，不换随机 ID；不能用单台宿主的幂等能力推断两个独立克隆共享幂等。READY 接管与分配 CAS 串行，防两克隆给同 contact 各建一次；分配永不因连接续期清零。

native create 并持久确认后，在本地关联原首联 delivery→assigned_address；原消息仅能面向此新会话，绝不自动投给已有会话。允许创建只得到可通信的空会话和地址；没有第二层自动执行授权时不得将正文送入会自动调度模型的 queue/prompt，也不能以原生 create 自带 prompt 绕过。若宿主不支持无模型创建，则停在已允许待适配/host_incompatible，不擅自启动模型。真正 queue/prompt 前须另获本地模型/工具对应授权，再按 §5 在线 EXECUTION_ADMIT，并核尚有效的本地 grant。首联即使已有 system 收件回执，仍可凭单独的有效 contact 执行状态申请唯一准入；system receipt 只释放传输项，不删除 contact 正文、分配/执行状态或创建预算。待处理 contact 的到期和取消继续生效。首条回复用原新会话 from/reply_to、to=原首联 from、in_reply_to=contact_id；collab 只允许该 assigned_address 经原目标 node 认证，在一次回复事务中建立该对话关系并提交回复，不能由任意发送者自报地址建立关系。只有第一层允许且本地已预留对应地址给出关系时，插件可在新会话已持久创建后发送 body 为 conversation_opened / execution_waiting、provenance=host 的确定性 reply，用该已绑定会话 from/reply_to 交换地址；不是模型生成内容、不计模型完成，不能夹带工具动作。模型实际答复仍需第二层授权。后续添加参与者须受邀地址经其 node 的有权对话确认，不接受仅列 ID 的 add-member。

首条回复进入插件持久 outbox，按稳定 reply 幂等键发送；PG 提交回包丢失查同键，A 接收回执丢失则同 delivery 补投。A 重发首联同键只查原 contact/回复，不再创建。使用新键是新联系，仍受首联速率/积压/创建预算约束，不承诺识别正文相同的不同业务意图。未决 contact/分配/结果至少保留到终态及重试窗口结束；system receipt 后不能提前按普通已接受正文 24 小时清除，待执行期限默认 24 小时且不超过原 expires_at，幂等元数据至少 30 天。过期只禁止新创建/准入，不能撤回已执行动作。

**首联防洪泛与容量**：总部按源 node→目标 node、源 IAM 用户→目标 node、目标 node 及 tenant 共同限制频率、待决条数/字节，插件限制提示频率与创建/执行并发。拒绝后根据请求状态计算同来源冷却；本地屏蔽名单不上传。规则和建议值见 §1.2 与总览 §10。等待期间全由总部计入待授权积压，不能因提示已读释放；获准后转入有界待创建/交接预算并预留本地空间，拒绝/过期写终态才释放待决额度。system receipt 只确认获准后的本地耐久收件，不清未决创建/执行证据。不因人工未处理停止整个 node，也不使用本地 ACK 把待批准正文从总部提前删除。

**冷会话与子会话**：只有仍开放且本地接收策略允许的冷 local 才可恢复原 native session；允许 receive 不自动允许 wake/执行，没有 wake 时 queued_cold。归档/关闭/删除拒收所有新入站，不执行自动恢复；所有来源一律统一 target_unavailable，归属用户显式重开原会话后才重新核权。撤销拒收和既有回执对账分开，已准入副作用边界仍按 §5。子 local 内部映射保存可信 lineage 和 durable parent address，N2 验专用子 agent adapter 前返回 host_incompatible，不回退到父普通 prompt。冷会话、子树授权及映射恢复在插件完成，平台只分页返回该 node 的不透明对话关系/未决项，不保存全量会话树。

### 1.2 首次联系授权、状态可见性与撤销

**决策权与作用域**：first_contact 表达“请求与该 DSH 建立对话”。目标 DSH 归属用户在其本机插件操作 allow_once（允许本次）、allow_source（总是允许该发起方）、deny（拒绝）、block（屏蔽）；总部系统 DSH 由有权管理员设置的本地策略决定。允许本次绑定 contact_id 和源身份，建立的对话后续仍需当前个人策略和逐会话 grant；不等于只允许一条回复，也不永久豁免后续策略。总是允许明确选择 source_node 或 source_user，用户身份由 IAM 核当前归属，不能用 payload 自报的 owner_user_id/显示名匹配。按用户的规则涵盖其多个源 node；源 node 归属变化须重新评估，目标用户其他 DSH 不自动同步授权。

**两层默认档位**：conversation_policy 管建立对话/创建普通会话/交换地址；execution_policy 管自动唤醒/启动模型，具体工具还受工具 grant。两层各自可选 ask_each（每次询问，推荐默认）、same_org（同组织自动允许）、allowlist（仅白名单自动允许，未命中保持待问）、deny_all（全部拒绝）。第一层每个 contact 决策，第二层每个稳定执行请求决策，同请求重投不重复提示/授权。工具权限不得从“允许自动模型”推出，白名单记录必须带层级、范围和策略版本。没有跨层默认继承；只允许第一层时可创建并介绍地址，模型调用数与工具执行数必须为 0。

优先级是总部组织禁止/身份失效 → 本地屏蔽或该层全部拒绝 → 当前显式允许/匹配白名单/同组织档位 → 等待人工决定。任何个人允许都不能扩大总部组织上限；same_org 以最新 IAM/org 判定，不凭本地用户填写组织名。同组织但被部门互联策略禁止时直接停止，不弹一个能覆盖组织禁令的允许按钮。IAM/org 不可用时停止新决策提交、创建和执行，不能降级接受缓存允许。

**权威存放与修改入口**：个人决定、默认档位、白名单、屏蔽名单、策略版本及其完整审计在目标插件本地事务库，由真实本地用户界面校验身份后修改；不向模型暴露批准/改设置工具，不接受远程 first_contact/control 或总部普通 UI 更改这些规则。总部只保组织政策、请求/对话处理状态及节点提交的最小决定凭据（decision_id、request_version、opaque policy_revision、结果、时间），不持有个人规则列表/未来默认授权。它不能离线推演“这个用户之前总是允许”，也不能把请求状态当新请求授权。平台已有 node 身份、关系、回执与配额仍按前节保留。

**提交与审计**：插件先在本地持久记录决定及状态 outbox，再经当前认证 node/epoch 发私有 CONTACT_DECIDE。包括 contact_id、decision_id、expected_request_version、opaque policy_revision、conversation_allowed / execution_allowed（独立结果）与层级；详细规则和屏蔽原因留本地。总部核决定来自原目标 node、当前组织策略、身份/期限与请求版本后 CAS 请求状态；首联决定及后续消息的执行授权分别绑定自己的稳定请求/消息 ID，任何执行允许不能复用到另一条消息，同 ID 同体重试返回原结果，异体拒绝。崩溃后补交 outbox，不把未提交的本地允许当平台准入。对 block/deny 总部只记录该请求 denied；屏蔽后新请求仍按通用配额暂存，插件上线时按本地名单静默拒绝，不提示用户、不向总部复制屏蔽名单。审计按 contact/decision ID 可对账，本地库丢失时不从总部结果自动重建“总是允许”，默认回到每次询问。

| 权威请求状态 | 有请求状态查看权的发起方 | 后续约束 |
|---|---|---|
| contact_pending | 等待对方授权；不是发送失败或送达 | 正文留总部，提示不 ACK，不 create/prompt |
| contact_allowed | 对话已允许；可能仍等待创建或执行授权 | 不隐含 execution_allowed；创建仍需 CONTACT_ASSIGN |
| contact_denied | 已拒绝；不透露屏蔽/拒绝细节 | 终结本次未交接投递、进入冷却，不自动重试 |
| contact_expired | 已过期 | 不可迟到批准复活；如需新联系用新请求且仍受限额 |

上表 contact 状态针对第一层；第二层另存 execution_permission=pending/allowed/denied，拒绝自动执行不撤销已允许建立的对话，也不把 conversation 状态改成 denied。对已建对话需要重新询问时，沿同一机制登记该消息的有界授权请求，scope 标明 conversation 或 execution；对外返回状态码时带 scope 与关联原消息，不复用已终结的旧 contact 来重建会话。

错误/状态码提案为 contact_pending、contact_denied、contact_expired；错误对象的 code 对应 network.contact_pending / network.contact_denied / network.contact_expired。pending 是可查询的业务等待态，不当传输失败或 SDK 自动重发依据；denied/expired 为此请求的终态，非成功 ACK。请求允许后，创建/执行生命周期按 §1.1/§4 独立记录。撤销已允许关系可记录 revoked；已创建会话身份不删除/复用，既有执行不假报撤回。

**两类回执避免探测**：状态查看权独立于“能提交请求”，由总部的目标发现/状态可见策略或已建立对话授权决定，后续查询重新核权。有权者显示上表状态，但 pending 不附目标在线/已读时点。未获该权者对存在/不存在、组织禁止、个人拒绝/屏蔽、在线/离线目标均只见 request_submitted“已提交请求”；不返回真实 target/message/mail_seq、是否排队、拒绝原因或可变的目标相关 retry_after，也不暴露 contact_denied/contact_expired 的时间差。通过收件同意后才能获得对话地址；发起人自己设定的截止可显示为本次等待窗口结束，不能据此推断远端处理。

为使“已提交请求”真实且不因不存在目标分叉，入口先在源身份配额内耐久保存不透明 submission_id/幂等摘要/截止；对目标校验和真实系统收件箱 materialize 异步处理并关联同一 submission，重试不能重复产生 contact。只有合法可路由目标才有正式系统 message/delivery/mail_seq；无效目标的占位提交不生成虚假投递。外部响应在目标查找前形成，格式、长度和查询外观一致；目标相关限流/冷却/屏蔽不泄漏到该受限回执。源自身鉴权、语法、全局入口配额或数据库失败仍明确报错，不能假回已保存。占位记录同样有界、到期且防批量枚举；它的提交不是四级 ACK 的 platform_stored，也不保证对方会看到。组织允许的真实请求最终留在总部系统收件箱，隐私外观不绕过组织拦截。

**离线、期限和提示**：目标离线时任何个人自动档位均不在总部执行，尚无明确决定的请求只保 contact_pending；上线以 READY/HEAD 和分页取提示，插件本地读取策略后决定。审批 UI 的限长预览仅给目标已认证用户，不进入模型上下文/会话 inbox；预览缓存可丢、非权威。待授权期限=min(原 expires_at,提交+24 小时)，到期任务无需目标在线就写 contact_expired 与未交接 delivery 的 Expired tombstone；迟到 CONTACT_DECIDE/CONTACT_ASSIGN 拒绝，上线不再提示过期项。相同 state_version 提示在本地去重，重连不重复骚扰，不新增周期性短轮询。需要重新询问的已有会话消息同样由总部有界保留待授权；若在撤销同步前已取到本地，只可隔离等待，不产生新的接受回执/执行，也不能删除总部未确认副本或靠旧缓存先执行。已经持久交接的历史项保留事实但新动作按新策略核验。

**撤销与竞态**：本地策略修改先持久增加 revision，重算受影响已建对话、会话收件/执行和待决请求。个人规则不因对话建成而固定：收紧为每次询问时，受影响后续请求待重新批准；deny_all/屏蔽或撤销许可时拒绝新动作。原 allow_once 只在其未被撤销且当前上限仍允许的线程内有效。插件通过同一私有状态通道把受影响请求/关系标为暂停或撤销，只传处理状态，不上传名单；HEAD 通知源查询有权状态。重连先对账本地未提交的撤销，再处理新投递，旧本地允许或平台已知 allowed 都不能跳过本地核验。

CONTACT_DECIDE 的 expected_request_version 拒绝乱序更新；撤销/收紧先保留更高版本，旧允许回包不得继续使用，恢复须按本地最新 revision 与平台版本重新对账，不无条件把冲突旧允许改成新版本重试。

CONTACT_DECIDE、组织撤销、到期、CONTACT_ASSIGN、EXECUTION_ADMIT 在总部按请求/执行版本与既定锁序串行；较旧决定不能覆盖新拒绝/撤销，拒绝/过期终态不改回 allowed。插件本地在 create/prompt/工具调用前核当前 revision/该层许可，撤销与开始动作在本地锁内排序；平台已准入但本地撤销先发生则不调用且保留原准入待对账，不重发许可。组织政策检查与 IAM 不构成跨服务原子事务，已有在途动作不可物理收回；离线撤销尚未同步时总部可能有界存后续消息，但目标不会使用旧允许执行。

**防骚扰默认**：源 node→目标最多 1 个待决，源用户→目标最多 3 个；两者各每分钟 5 次/突发 5，目标每分钟 30 次/突发 10、待决 100 条或 1MiB，另受 tenant/全局入口额度。相同幂等键只占一项，换 local/换源 node 仍受用户聚合；源 node 与源用户任一对目标的拒绝冷却为 24 小时，按总部已存拒绝状态计算，不延长原请求 TTL。屏蔽在本地持续到用户解除，优先于总是允许；被屏蔽请求不产生新提示。目标相关超限对有状态查看权者给清楚的 quota_exceeded/冷却提示，对无权者保持统一提交外观，但内部不越配额，不能靠无限隐藏排队实现隐私。

### 1.3 插件本地关系、防枚举与内容边界

本节检查必须在进入会话/模型前由插件完成，不能把总部的准入结果、对话参与者或远端文字当成本地授权。总部误路由、漏做一次关系检查或安全报告暂时不可用时，本地仍拒绝不合规则的消息；但来源身份的真实性仍依赖可信总部认证与受保护传输，不声称在总部身份权威被攻陷后仍能证明真正的远端用户。

**来源盖章与入站顺序**：总部从源认证连接取得 node/realm，并从 IAM 核当前归属用户，源 local 来自可信源插件宿主上下文。平台生成不可由客户端覆盖的 source_context，绑定规范 from、目标 to、message_id/thread_id、消息摘要、身份版本/观测时间；转发时从权威存储重建，删除/拒绝源 payload 自报的同名元数据。目标只接受当前受信平台 FETCH/响应通道中与请求/delivery 对应且通过 §3.2 server_auth 验签的 source_context；先限长并验签、目标/epoch/期限/重放，再核 from/摘要/tenant/类别一致和本地规则，未验签不显示为可信消息。普通正文、ext、任意 PUB、仿造“总部签名”文字或自报 user_id 不可信，字段矛盾或无可信上下文则失败关闭。按用户授权须核当前 node 归属；权威不可用/证明过期不能以旧用户标签匹配白名单。

第 11 轮将盖章加强为必需的总部服务器数字签名，独立于 NATS ACL；它证明总部签发了这份信封，不证明正文指令安全，也不是发送者端到端签名。复制 JSON 仍须通过目标/epoch/请求关联、时效、状态版本和去重，不能取得新操作许可。总部仍无法证明受损源插件内部哪一个 local 真在调用；因此本地默认关系尽量限制完整源地址与 thread，不把给某用户一个地址扩大成其所有消息皆可信。

**关系表与首次回信**：本地关系按 (target_local, peer_scope, peer_node 或 peer_user, peer_address, thread_id) 保存，含该 local 的地址介绍/授权发出记录、原 contact/message ID、允许 kind、接收模式、期限、revision 和撤销状态。默认 peer_address 精确到完整源地址；用户显式允许按 node/用户放宽时仍需匹配对应线程与类型，不扩大到其他目标 local。来源只使用上述盖章，绝不以 payload 自报用户匹配关系。非系统单播目标若找不到“此 local 曾在有权对话中给过该来源”的有效记录（组播改核 §1.4 的本地组授权），拒收并丢弃会话正文、写安全审计；有界拒绝账本保 delivery/recipient/digest 供终态上报和去重，不把消息静默吞成 host_accepted，也不无限重投。插件以稳定操作 ID 提交 RECEIPT(status=rejected, code=network.target_unavailable)，平台核当前目标 node/epoch、原 delivery/recipient/目标/摘要及版本后，将尚未交接项记入既有 Revoked 策略拒收 tombstone 并推进合法确认前缀；此处只终结该 recipient，不终结同邮箱项其他 local，也不撤销整个 node。详细拒收原因留本地。已接受/已开始项只能补对账或更新执行状态，不被迟到拒绝倒写成从未接受；上报失败保留有界拒绝元数据重试，不再把正文交给模型。

发出地址介绍时先持久预留关系/原出站请求，再发送；只有收到平台提交结果或按同幂等键核实已提交，才激活该记录，回包丢失可对账，不能从总部普通参与者列表自动补一条永久许可。初始 A1 向 B/sys 给出自己的 from 时，A 插件持久记录一条仅此 contact 的待回复关系，允许认证 node B 的唯一 assigned_address 介绍回信；必须匹配 in_reply_to/contact、原 thread、期限、B 的可信源身份与平台固化分配结果，首次成功固定精确 B1。它不授权 B 的任意 local 主动投 A1/A2。B 插件经用户同意给新 B1 分配后，按首联来源建立 B1→A1 地址给出记录；原 sys 消息只沿固化分配内部交接。重放首回复只对账原 delivery，不创建第二关系/会话。接收方本地没有原 contact/给出记录时失败关闭，不因“首次回复”字样临时开门。

**生命周期与会话接收策略**：归档、关闭、已删除的 local 对 message/reply/control/唤醒一律默认拒收，禁止通过入站消息、总部提示、父会话或历史 grant 自动恢复。墓碑持久且不回收号码；归属用户显式重开原会话时重核 native ID、关系、接收模式和权限，旧已拒消息不自动复活。真正删除且原数据不可恢复时只能新建新 local，不能“重开”成不同会话。同一原会话重开不是把号码再分配；恢复墓碑不得丢弃旧拒绝记录。状态变更与入站/准入在本地版本锁内排序，已持久接受的历史回执仅可对账，不能触发新执行。

每会话接收模式独立于首联默认档位：replies_only（仅线程内回复，建议默认）要求来源关系有效、kind=reply、in_reply_to 指向该 local 发出的有效请求且回复者匹配；thread_messages（允许已授权线程消息）要求本地逐参与者/线程关系；receive_none 拒绝一切新入站。回复去重、可回复次数/有效期有界，不能持一个旧请求无限发送。control 还必须有单独动作 grant，并且未被生命周期/receive_none 禁止；replies_only 不因 kind=control 自动放行。用户可立即撤销某来源 node/用户/完整地址关系，本地先写 revoked revision，再可靠上报请求/关系拒绝状态；不能靠平台未收到撤销继续投递。

**统一错误及相近时延**：非系统目标不存在、归档/关闭/删除、无关系/接收模式不允许、已屏蔽，统一 code=network.target_unavailable、同一外部文案“目标不可接收”、相同响应字段和无原因细分的不可自动重试语义。平台前置拒绝和插件拒绝的对外接口统一裁剪，且服务端查询规范地址不得通过不同验证顺序泄露存在性；不能从状态查询、trace、quota、重试间隔、receipt reason、目标是否产生 host_request_id 或归档专用错误绕开。详细 reason 只进目标用户有权查看的本地审计；已获首联状态查看权仍按 §1.2 看 contact 状态，但 contact_denied 不细分“用户拒绝/屏蔽”。

四类本地拒绝使用同一有界处理与响应队列，在不额外查宿主内容的路径判定；建议统一在入站后 100–200ms 时间窗内抖动发出拒绝，按负载测试调整；总部前置拒绝同样使用统一延迟规则而非按原因快速返回。不能为每个探测分配无界计时器或线程，超载统一节流并保留审计预算；相同延迟规则也适用于已知被屏蔽源，不能快速返回形成旁路。公网 RTT/分区不保证恒定时延，因此这是可测的相近分布目标而非数学恒时承诺。测试须比较四类响应大小、字段及延迟分布，不仅检查错误字符串；正文永不进入模型。

**扫描检测与隔离**：插件按可信源 node 聚合无效目标命中，辅以当前 IAM 用户聚合，避免换 local 绕过；只对身份已验证请求计入归责。建议滚动 60 秒内 ≥20 次无效命中或 ≥10 个不同无效 local 即原子写本地自动屏蔽，直到归属用户显式解除；无效包括不存在/墓碑/未给出关系，不因宽松会话 grant 免计。每个稳定 recipient_id 只计一次，拒绝去重至少覆盖重试窗口，防正常补投误封；计数窗口/屏蔽跨重启持久，时间异常不清零屏蔽。伪造/无认证来源不能冒充受害用户增加其计数，只记受信连接异常并限流。源用户级扩大屏蔽须用户设置或经核实的组织策略，不因报文声明连坐。计数表/去重账本/屏蔽表均有硬容量，达到上限停止接纳新来源并告警，不能逐出有效屏蔽来腾位置或分配无界内存；仅按已覆盖重试窗口的规则回收去重证据。

超阈值立即拦截该来源的新请求、合并提示本地用户，并经独立有界安全状态 outbox 上报 tenant、可信源 node、时间窗、次数/去重摘要、检测规则版本及报告 node；不带正文、local 清单或完整个人屏蔽名单。日志/上报队列满时合并计数并记录丢弃统计，屏蔽依然生效，恢复后补报，无需总部批准才能本地封禁。总部结合自己的认证/路由审计与多个 DSH 摘要识别跨 DSH 低速扫描，依组织策略或管理员决定隔离该 node；单个目标恶意报告不能直接全组织封禁。隔离按现有 IAM/node 拒绝新业务及 KICK 流程，解除总部隔离不自动清本地屏蔽；总部不可达时本地关系、墓碑、屏蔽和接收策略继续执行拒绝，新执行仍按在线准入失败关闭。

**外部内容与工具**：所有入站正文/附件引用/链接/审批预览都标注“外部消息”、可信 from、thread/message，正文仅作为数据或受限用户消息载入；保留来源标记，不允许伪 system/developer 角色、指令文本或转发内容改变本地策略。内容写“用户已批准”“忽略检查”“请运行命令”没有授权效力；不在解析/预览时执行脚本、自动打开外部链接或把内容当工具参数执行。来源已获联系权、甚至未来签名有效，也不授工具能力。远程触发的危险工具继续走本地逐动作审批/权限，不能由另一条消息模拟批准；control 单独核控制对象、动作范围、期限和本地用户授权，未支持动作拒绝。自动模型许可不是免审危险工具许可，LLM 判断也不是安全边界。

本地审计记录时间、可信来源/归属版本、目标地址摘要、thread/message/delivery、关系/生命周期/策略版本、拒绝原因码、自动屏蔽及解除操作者。日志有字节/留存上限、受用户权限控制，避免正文/凭据与高基数指标；拒收正文丢弃不等于删除审计，磁盘/审计故障须失败关闭新交付并告警，不能无限堆日志拖垮宿主。

### 1.4 组成员、广播、分页派发与非耐久组播

**管理与授权**：总部保存 group、owner/admin、发言策略、版本及成员完整地址，成员只能是本 realm 的 session/subagent/system，不嵌套组。publish_policy 默认 members，当前有效成员即可发；所有者可切换 admins_only，发言者须同时是该组有效成员和组管理员/所有者，非成员不能发。人按 sys 成员身份、agent 按精确 local 成员身份核验，用户加入不让其全部 agent 入组。创建政策 group_create_requires_approval 可由总部配置；建议默认普通用户可建 ≤32 成员/8 node 小组、每用户 ≤10 个活跃自建组，扩容需组织允许且不超过 §1.4 普通组硬上限。agent 发起创建仅能用独立委派的建组能力，计入同用户/node 配额，不借创建获得广播权。owner/admin 可邀请和管理，但目标插件必须由真实本地用户确认加入并先耐久保存 (group_address,target_local,membership_id,allowed_kinds,receive_mode,revision) 授权，再提交激活；自动入组也须目标用户事先明确设置的本地许可。远端把地址加入名单不能代替同意。一次退组后重加使用新 membership_id，历史授权不复活。加入仅允许该组上下文内当前有发言权者的消息，不建立成员间任意私信/历史读取/自动模型/工具许可；replies_only 仍不接受普通组 message，须显式为该组启用 thread_messages，receive_none/归档/本地屏蔽始终优先。插件核可信来源、组与目标、成员实例/版本及本地授权，外部正文仍不可信；组播/广播仅 information/request，control/wake/cancel 等执行控制一律拒绝，单独 control grant 也不能放开组播。入箱不自动模型/工具，receive_mode 仅决定收件，不决定执行。总部组织禁止高于所有组/个人允许。

**快照与退出**：send 在有权组版本下固化完整接收快照（与成员变更按同组版本锁排序；广播取事务一致的组织节点快照，不能跨页混读成员版本），按完整地址去重、按 node 聚合 locals；每个快照成员含稳定 recipient_id、membership_id，保留原总人数。后加入不看旧消息，不能因重试重新取最新名单扩大接收集合。退出/屏蔽先在本地持久生效，停止新接收/未开始执行，可靠上报总部后取消该成员未交接项；总部每次派发、FETCH/签发还核当前成员实例、来源身份/发言权及组织政策；组播/广播原消息禁止 EXECUTION_ADMIT。退组重入不接旧快照，管理员删组/撤销发言权也终结尚未交接的受影响项。已接受事实不倒写失败，但未开始动作按新授权停止；已在途副作用不能物理撤回。离线本地退出尚未同步时总部可暂存有界项，插件仍拒收；“即停收”指拒绝新的本地交接/执行，不声称能召回已读正文或全局零同步窗口。普通 group 只向有权参与者展示，不成为公开会话通讯录。

**广播**：本 realm 的所有已注册、组织策略允许的 DSH 系统地址构成隐式组 aur1:2.0.1（2 为合成 realm），发送时固化 node 快照，后注册 node 不补旧广播。只有 §1.5 明确、未过期/撤销的 broadcast.send 授予可发，默认无人拥有，管理员角色和普通组发言权均不能替代；隐式成员资格来自组织注册政策，不通过群邀请批准任意自动执行。插件保本地广播接收/屏蔽策略，默认仅系统收件箱耐久入箱，不创建会话、不启动模型，accepted_by=system。静音仅关闭通知、不伪造失败；明确屏蔽/拒收则逐 sys 返回统一拒绝。转入会话及模型执行须本地用户显式新建处理操作，原广播不能自动执行；reply_all 仍要求独立 broadcast 权并显式提交新广播，普通接收者只可依下述窄关系私下回复。同一用户多个 node 各一个 sys recipient、各自 ACK/配额；各界面按 (realm,message_id) 去重展示，跨设备合并摘要只能经用户有权状态查询，不能把一台 ACK 扩大为其他台已收。

**回复**：默认 reply_mode=private、to=原 reply_to=from、in_reply_to 指向原消息。源插件在组发送前持久预留本次出站的窄待回复许可，平台提交确认后绑定原成员快照/线程/期限；只接受已核验原 recipient 的回复，不能让该组任意新成员或转发者私信。目标本地需核这条待回复记录，回复上限/期限与普通回复相同；它不建立永久直接关系。reply_mode=group 由插件根据人或 agent 明确选择原组目标的发送动作和可信原消息上下文生成，普通 agent send 工具不暴露该信封字段；这才是显式“回复全组”，to=原 group，核当前成员与发言权，按本次发送新快照分发；退出者不收，新成员可收这条新回复，但 in_reply_to 只给不透明引用，不能顺带读旧正文/附件，逐历史项仍核原快照资格和当前许可。

**原子与分页**：小批原子上限为 ≤32 个目标 node 且 ≤256 个 local；每 node 最多 256 locals 且完整邮箱条目编码 ≤64KiB（含正文、元数据）。普通组最多 1,024 成员/256 node；广播最多 10,000 node，超过须管理上拆域/改批准配置，不能静默截断。客户端提交超过小批上限必须显式 dispatch_mode=paged，否则 network.requires_paged_dispatch；每 node 本身超限返回 quota_exceeded，不能偷偷拆成同消息同 node 多邮箱项。发送时先做所有已知授权/格式/预算检查，小批任一失败整笔回滚；后续本地拒收仍独立，并非原子全体执行。

大批在一个有界 PG 事务提交一份 message、完整且不可变的成员/node 快照、源/组织目标 node 费用预留、幂等和 dispatch_plan（包括 TTL、进度、原始总数），才报 platform_stored、dispatch_status=pending；**不表示所有邮箱已创建/所有成员已收到**。每次最多 32 node，按稳定 node 次序在短事务中创建 node 邮箱项、recipient 状态、分配 mail_seq 并推进页进度，提交后 HEAD；唯一约束和进度同事务防崩溃重复，多个 worker 用短行锁/SKIP LOCKED 协调，不设逐投递租约，不持锁等网络/宿主。未能创建邮箱的快照成员仍必须有可查询状态，不能从总数分母消失。recipient_id 在快照中预分配；建邮箱后关联 delivery_id，未派发状态以快照项为权威，关联后由 recipient_status 更新同一逻辑项，禁止双算或双终结。

派发计划可恢复重试到原 expires_at；目标队列满保留 pending/backpressure 并限制退避，IAM 故障失败关闭重试，明确退组/撤销写 recipient 终态；到期把未派发项标 expired。已知格式/成员硬上限在首次保存前拒绝，不能保存一个永远不能装入 FETCH 页的条目。取消阻止未派发页并终结未交接项，已准入动作如实报告。后续成员变化/目标限额导致 partial 状态合法，不宣称跨页全成全退。同 thread/目标 local 的派发须检查更早 sequence 的未决计划；较新项不能因先建邮箱而先入对应 local 收件箱。可继续扫描其他线程/成员，取消/过期的较早项按明确终态越过，不用全组屏障拖住在线 node。源端计费按快照不同目标 node 数预留，每 node 成功建邮箱仅结算一次；终态未派发部分退回预留，有界扫描防泄漏，重试不重复扣费。另计总字节、成员状态/本地执行预算，按 node 计费不等于本地百会话免费或一个 node 能无限列表。

**频率与回收**：默认普通组每源 node 60 次/分钟、突发 10，每组 300 次/分钟；广播每授权源 1 次/分钟、突发 1，组织共 5 次/分钟。叠加源用户/tenant 的目标 node 数、字节、计划数量和未派发快照配额，不能换 local/组绕限流。冻结源费用不预占全部目标邮箱锁；分页按 tenant/node 公平处理，控制/回执有预留预算。正文仅一份，直到全部派发项和每 recipient 交接/终态、首联未决执行及留存条件满足才可回收；不能在第一台 DSH 成功后删除共享正文。

**ephemeral 路径**：仅进度/在线状态可选 durability=ephemeral；经源认证、当前组成员/发言权、组织限额后，用 NATS Core 内部精确组 subject 原生 fanout 到持有在线目标路由的受信 adapter，adapter 再按当前成员和 node 策略发各自 down。内部订阅不能用全局 queue group 让唯一副本吞掉其他目标；同目标路由归属去重、允许重复/丢弃。DSH 不获共享组 subject 的 PUB/SUB 权限，避免退组后旧 Broker ACL 仍可读正文；组主题为平台生成，不接受任意 subject。插件仍核本地当前成员、墓碑/屏蔽与不可信内容。平台不存该正文/邮箱/receipt，membership 权威存储照常使用；仅在线投递、无离线补发/成功交接保证，不触发新会话/模型/工具。携带短 TTL、事件 ID/revision，丢弃重复/旧进度，队列满可丢并计数；权限不可用丢弃而非放行。最终结果、决定、control 必 durable，durable 永远走 PG 一正文与 node 邮箱路径，不能用 Core PUB 成功冒 platform_stored。内外带宽仍按实际目标 node/字节限额。

### 1.5 三类消息、人/agent 来源及总部管理权限

**类别由目的决定**：message_class=unicast 的 to 只能一个真实 node 的完整 system/session/subagent 地址；普通 group（node=0、local≥2）为 multicast；保留隐式组 node=0/local=1 为 broadcast。三类均可由有权 human 或 agent 发，判定与认证发生在总部入口，不能通过声称 multicast 把广播降级。地址解析器针对字段角色拒绝任何 group/广播作 from/reply_to；from.realm/node 必须等于当前认证域和 node，本 node 系统 local=0 或会话 local≥1 再与来源授权匹配。他 node 或组作源一律拒收并审计，不“纠正后发出”。单播批量工具如将来增加，必须拆成明确独立请求，不能用 thread 隐含多个目标；本轮多目标都用显式组。

**human 发送**：人从插件受信 UI 操作，from=本 node/sys、sender_kind=human。DSH 连接凭据只证明 node，不证明此操作由人发起；总部另校验 human_send_intent：通过真实用户认证且与 agent/MCP 隔离的管理入口签发一次性意图记录，绑定用户、realm/node、规范目标、正文摘要、线程、操作/幂等键与期限（建议 60 秒）。插件在人的私有发送路径持有其句柄，不把用户 token/句柄交模型、工具、正文或日志。消费与发送幂等事务绑定，同键重试返回原结果，不生成新发送；换正文/目标/操作者失败。只有系统地址或自报 human 而无此凭据不够，后台定时器不能伪造人点击。本人可以显式提交 agent 起草的内容，但这是经过 UI 确认的新 human 操作，不是 agent 自动借权。

**agent 发送**：受信插件将宿主执行上下文绑定到总部可核验的 opaque source_binding 记录：realm/node/local/type= session 或 subagent、principal_kind=agent、版本/到期与 send 范围；总部只存不透明地址/授权引用，不新增宿主会话树。注册/使用来自专用插件控制路径，模型无权注册来源或挑其他 source_binding。普通确定性宿主回信也沿相同会话记录，provenance=host 不改变 agent 类别。总部以该记录而不是消息自报字段生成 sender_kind/from；用户拥有 sys/广播权不会扩展这条记录。受损 DSH 或窃取用户独立认证材料仍可能冒充本机操作者，签名不能证明物理键盘或修复已失守的信任端；正常工具隔离与授权检查须实际验收。

**工具面**：普通 agent 发送工具参数仅 target、body 和可选 thread；from、sender_kind、message_class、reply_to、server_auth、source_context、授权引用、flags 等不是模型字段，额外顶层字段直接 schema 拒绝，不先接收再丢弃。插件用真实执行上下文组装私有请求；回复动作由可信当前消息上下文绑定 in_reply_to/原 recipient，普通发件工具不能以自造引用攫取回复许可。body 只接收受支持信息/请求 schema；正文内出现“from=总部”只是数据，不递归当信封解析。执行控制使用独立有专权的单播入口，不能藏进普通 send 的 flags/ext。总部再次检查来源和目的权限：插件预拒只是体验优化，无广播权会话即使直接调私有接口也返回 broadcast_forbidden。

**盖章与危险类别**：总部据上述授权生成 sender_kind 与真实用户、from，据 to 生成 message_class，再核 kind/intent/durability 组合；保留字段自报与来源矛盾拒绝 sender_mismatch，类别矛盾拒绝 class_mismatch，自报授权引用不续权。information/request 为组播/广播唯一业务意图（进度/在线状态是 information 子类型），reply 表达回应关系而不产生控制权。control/wake/cancel/queue/steer/执行启动等只允许 unicast 且专门授权；未知执行 schema 失败关闭。总部与插件都强制 multicast/broadcast 及其请求的单播回应 inbox_only，不调 prompt/自动恢复/工具/设置，不允许自动模型档位覆盖。用户之后显式处理须新本地 operation_id、独立批准并再次核状态，原 recipient.execution_status=not_applicable，不给它 EXECUTION_ADMIT；一份组消息不能自动变成 N 个模型任务。

**广播高危授予**：broadcast.send 与普通 send、组 owner/admin、总部管理角色分离，默认无授予。只有总部有权管理主体可在管理入口显式创建：grant_id、realm/tenant、subject_type=user/node/agent、具体 subject_id、允许 sender_kind、可选更窄 node 范围、not_before/expires_at、频率/目标 node/字节上限、revision、active/revoked、授予人/理由。user 授予仅本人 human 路径、node 授予仅该 DSH 上已通过 human 意图验证的有权用户；两者均不授本 node 的 agent。agent 必须独立 subject_type=agent 授予并绑定精确 (realm,node,local) 及 source_binding，禁止 local 通配、父/子/fork 或用户角色继承。建议默认 1 小时、最长 24 小时且不自动续，部署可收紧；grant 范围只能缩小组织硬上限。

广播发送必须由总部按当前 grant、用户/node 活性、source_binding、冻结和组织策略核验，不信缓存允许或签名内旧引用。授予/撤销与 send 在同一权威版本锁/CAS 顺序提交，IAM 依赖失败关闭；分页每页、FETCH/重新签发也重核。撤销/到期先于 send 则零正文/计划；send 先提交则已有 platform_stored 事实保留，后续未交接成员转 Revoked/Expired，已接受不倒写。插件收到已在途签名页还须核最新广播状态屏障（§3.2），并按本地撤销版本拒绝；不能承诺跨网络零时间窗。发件 grant 到期/撤销或冻结不等于撤回已接受广播：未交接项不能继续派发/签发；已接受项的历史展示只核目标当前读取权及未撤回状态，不要求源发件 grant 永久有效。历史回执对账和管理状态更新也只核目标当前身份/原证据，不受源 grant 已失效阻断，不能因此无法收尾。grant 审计包括授予、变更、使用消息/快照、拒绝、撤销/过期和真实操作者，不记凭据/正文。

**名称与可信展示**：普通用户、node、会话和组显示名禁止包含保留词“总部”“管理员”“系统”及受控别名列表；写入时用统一 Unicode 兼容规范化、去不可见控制字符、大小写折叠后校验，原始名称另作安全转义，不改变地址/签名规范化。旧不合规名称显示中性占位并提示改名，不因此删除旧消息。总部专用角色使用不可编辑角色徽标，不以开放显示名表达管理身份；普通名称禁保留词的规则不因用户是管理员而失效。签名 source_context 绑定当时核验的 user ID、node、地址、sender_kind 和安全显示标签；UI 固定来源栏展示这些字段，正文自称角色、HTML/双向文本不能覆盖。相似字形无法仅靠词表穷尽，地址/真实身份和徽标才是识别依据。

**撤回与冻结**：总部专用管理 API 支持 recall_broadcast(message_id,expected_state_version,reason) 与 freeze_sender(scope,subject,revision,reason)，须管理员当前管理权限、幂等键、审计；不是普通业务 kind=control，也不授 agent 一个广播控制工具。freeze 可针对具体用户/node/agent，用户冻结覆盖其发件来源、node 冻结覆盖该 node、agent 冻结仅该 local；对新发送/未派发/重新签发阻断，执行控制还受已有准入边界。解冻不恢复已撤销 grant/终态消息，须明确重新授权。冻结防继续发件，若需隐藏旧广播另执行撤回，不能混为一事。

撤回事务持久递增 message_state_version、标 recalled，停止未派发页与正文读取/再签发；未交接 recipient 写 Revoked，已接受仅保原 ACK 并追加 recalled 展示状态。以原 node 快照写可靠的逐 node 状态更新计划，复用 mailbox 的有界恢复路径、幂等 revision 和控制预留容量；不复制原正文，也不借用户组播通道执行任意动作。已完成原 mailbox 的 node 仍有独立更新序号，已接受不等于以后不再同步撤回。状态更新只能表示固定 recall/freeze/版本通知，受专用管理来源与签名用途校验，无可执行脚本/工具动作。

插件验签并核原 message_id、realm、目标和单调版本后，先耐久记撤回 tombstone，再隐藏正文/通知/检索与自动载入入口，清可回收展示缓存，保审计/去重证据；撤回先到、正文后到仍隐藏，重启不复活。HEAD 丢失靠 PG 更新恢复，离线设备 READY 后先同步相关撤回/冻结状态再展示缓存；不能在线证明当前状态时，广播旧正文保持“待同步”隐藏，新页也不先展示。状态 tombstone 至少覆盖原消息/客户端最长重放窗口（默认 30 天），越保留窗口必须状态重同步而非信旧缓存。在线签名页与撤回同时在途仍有已读窗口；服务器不能抹除截图、导出、用户已知内容或已发生副作用，撤回的展示/执行效果应单独审计而非伪造从未收到。

## 2. 原生 NATS 接入，不另造传输协议

一 DSH 默认一条官方 nats.js `wsconnect` 连接，Broker 使用 nats-server 原生 WSS。`@nats-io/nats-core` 的 WebSocket transport 与 `transport-node` 的原生 TCP connect 不能混用；须按真实宿主 Node 版本验证。NATS CONNECT/INFO/PING/PONG/PUB/SUB/UNSUB、WebSocket 二进制分帧、服务器发现与慢消费检测全部复用官方实现，不另定义 HELLO/REAUTH/CREDIT/GOAWAY 来替换它们。官方来源：[nats.js core](https://github.com/nats-io/nats.js/tree/main/core)、[WSS](https://docs.nats.io/learn/websocket/)。

官方 SDK 在目标运行时的隔离连接证据及其限制见[验收 §1](failure-validation.zh.md#1-已有证据与明确未验证项)，不等于完整插件验收。

### 2.1 三层准入与连接生命周期

1. **HTTPS 连接准备**：Node 用当前真实设备/实例凭据调用私有准备接口，IAM/org核有效身份、node归属和接收权限，生成短期一次性网络连接票据（提案：30秒可用、持久存摘要）。返回随机 `ingress_id`、精确上下行 subject（路由主题）、允许协议版本。票据仅是现有登录的内部派生，不增加用户可见第二登录，不包含主密钥。
2. **Broker CONNECT auth callout（外部鉴权回调）**：Node将该票据作为NATS CONNECT认证数据经WSS提交。可信callout在线核原凭据/组织状态，并原子兑换票据；返回签名User JWT，绑定服务器提出的user_nkey与server audience，指定客户端 account、精确 pub/sub 权限、max payload/订阅数，以及不超过 IAM 会话/设备网络授权截止的 exp（默认上限一小时，见 §2.3）。官方v2.12.6源码已核这些能力，但本项目映射仍须安全spike。失败/超时拒绝，不降级静态allow-all。
3. **Auriga 应用 READY**：Node先SUB本次唯一down subject，再发送READY（应用版本、adapter能力、原node cursor）。adapter从已授权up subject查询可信ingress记录，不信payload的node/tenant；核身份和能力后事务接管node epoch、记录route、返回READY_RESULT。只有此时才允许 FETCH/发送该 node 的新正文。旧 ingress 标 fenced，新业务拒绝，迟到持久回执仅按 §5 对账；新正文只发新 down subject。READY_RESULT 带 head_seq、平台确认前缀与可恢复窗口。

票据兑换后而READY前断线：未形成可执行连接，按新票重试；旧票不可重放。SUB与READY可能乱序时，由NATS同连接顺序及READY结果确认订阅已建立，仍以PG恢复兜底。READY响应丢失：以同ingress查询当前epoch/重试READY；不要无条件增加世代。重复重连冲突在短时阈值后暂停，不让两个克隆无限互踢。

callout只负责CONNECT身份及subject边界；**每条应用操作**依然由adapter→collab核网络授权所关联的当前 IAM 会话/设备状态、node 策略、tenant、完整地址的对话关系与 epoch；会话 binding/grant 在源和目标插件核验，禁止把ACL当每操作IAM。网络ticket/subject不能让模型指定session，`exec.agent.session.id`等仍来自可信宿主；外部通过网络收到的他人消息也不获得本地工具能力。

### 2.2 精确 subject 隔离

示意命名 `auriga.net.up.<ingress>` 与 `auriga.net.down.<ingress>`，其中 ingress 为服务端随机不透明值。默认 Node 只可 PUB 自己 up、SUB 自己 down；经下述验证后才允许私有 reply 前缀订阅，不允许 DSH 直发其他 DSH。Broker 只有固定少量订阅/方向约定，不按逻辑会话造 Broker consumer。完整不透明地址/thread 在信封中，collab 核节点与对话关系，DSH 才解析 local 映射。

不授予客户端 `$JS.API.>`、`$SYS.>`、内部服务 subject、全局 `_INBOX.>` 或跨 ingress 通配订阅。默认应用响应经精确 down subject 以 request_id 关联。官方 nats.js v3.4.0 的 [ConnectionOptions.inboxPrefix](https://github.com/nats-io/nats.js/blob/v3.4.0/core/src/core.ts) 支持私有响应前缀，因此 request() 不是一律禁用；可配置服务端分配的 `auriga.net.reply.<ingress>`，仅授权该前缀下 SDK 所需的窄订阅。

N0 须验证实际生成的 mux 订阅、noMux 分支、连接迁移清理与超时取消，以及 adapter 只向当前 ingress 私有前缀内合法 reply 发响应。拒绝任意 reply subject，不使用宽泛动态响应权限绕过 ACL。验证前保持固定 down 关联方式，两种方式共享 FETCH/RECEIPT 语义。服务端始终以收到的 up subject 提取 ingress，不相信 payload/header 自报的 tenant/node。

客户端 account 与内部 service/system account 独立。共享 broker 的 account 隔离只隔离权限，不提供 CPU/磁盘故障域隔离；达到容量/威胁边界时部署独立接入 broker，通过精确 subject 导入导出接内部 adapter，不开放双向全通配。敏感 auth callout 请求使用其 XKey 加密能力、内部 TLS 与独立服务身份；不记录 CONNECT 凭据。

### 2.3 刷新、吊销、接管

NATS 在 CONNECT 时鉴权；不用自定义 REAUTH 延长 Broker 权限。区分三种期限：HTTPS 访问 token 按 IAM 原规则刷新；一次性连接票据只负责短时兑换；Broker User JWT 来自独立的网络连接授权，建议最长一小时且不超过 IAM 会话、设备授权或实例授权剩余期限。它不自动延长原 IAM 凭据，也不准用一个过期访问 token 继续调用业务 API。每次应用操作以 ingress 绑定的权威授权记录核当前身份，访问 token 的轮换不要求健康 WSS 跟着断开。

Node 在网络 JWT 真正到期前的随机安全窗口内获取新票并重建连接；每次意外断线也重新兑换，不能自动复用旧票。SDK 同步认证/重连 callback 不执行异步 HTTP、不 throw；插件监督器完成刷新及重建。稳态一条连接，迁移短暂最多两条，只有当前 epoch 可发新操作。以一小时有效期、平均提前 90 秒计算，大档 5,000 DSH 约 5,000/3,510=1.42 次握手/秒；若 IAM 授权剩余期更短，按实际周期重算。

吊销主路径是权威记录撤销后拒绝新操作和新 FETCH，并停止向旧 ingress 发正文；随后由隔离的系统运维身份按 server_id/CID 关闭连接。锁定 v2.12.6 的 [events.go:62–63](https://github.com/nats-io/nats-server/blob/v2.12.6/server/events.go#L62) 定义 KICK/LDM subject；[kickClient:3203](https://github.com/nats-io/nats-server/blob/v2.12.6/server/events.go#L3203) 解析 CID 并调用 DisconnectClientByID，[ldmClient:3221](https://github.com/nats-io/nats-server/blob/v2.12.6/server/events.go#L3221) 调用 LDMClientByID。KICK 用于撤销关闭；LDM 是迁移提示机制，不能代替撤销。

只读源码证明接口存在，不证明 Auriga 接线通过。N0 必核 auth callout 请求的 client_info 到 CID 的提取（[auth_callout.go:376/456](https://github.com/nats-io/nats-server/blob/v2.12.6/server/auth_callout.go#L456) 填入 c.cid），及受信 server_id、account、user_nkey 与 ingress 的原子关联。CID 是服务器本地标识，须带服务器运行世代防重启后复用误踢；用连接事件或受权查询核当前归属，不能只信客户端上报。验证多 Broker、重启、快速重连、旧 KICK 延迟和系统账号最小权限。外部 DSH 永不获 `$SYS` 权限。

KICK 失败不恢复授权；有界重试并告警，JWT 到期是最终连接关闭兜底。运维控制面可用时撤销到断连目标 ≤5 秒，跨服务在途正文和已开始副作用仍不可收回，不承诺从 IAM 提交瞬间全网线性撤销。原生 expiry 由 [setExpiration](https://github.com/nats-io/nats-server/blob/v2.12.6/server/auth_callout.go#L322) 设置。若正式网络授权与在线逐次校验未实现，不可直接把旧短 token 的 exp 拉长。

IAM/org 故障时拒绝新票、READY、发件、FETCH、回执状态写入和执行准入；已落盘 receipt 暂存待权威恢复后对账。退出清本机执行许可并断连，不复活被撤销子 grant，不撤回已发生副作用。缺可信接管依据的丢失设备/克隆实例重新注册 node。

### 2.4 TLS、Origin与身份边界

拟由edge精确路径 `/ai/network/v1` 反代Broker WSS listener；不是标准MCP端点。TLS终止后跨机链路继续TLS，不能信外部身份头。Node专属edge入口拒绝任何Origin（含null）、Cookie和浏览器凭据混用；Broker同时配置Origin边界，但Origin不是认证，非浏览器可省略它。浏览器UI继续沿ADR0017/0020和DSH本机requestRejection，不能访问网络凭据。

WSS CONNECT内的凭据不是HTTP Authorization头；HTTP MCP仍使用原Bearer流程。外部首发WSS+短期凭据，内部mTLS。后续机器密钥/mTLS可降低票据盗用，但不能替代会话授权。托管INSTANCE及总部SYSTEM必须正式签发，`ait_`活动令牌、portal/admin cookie、模型key不可用于网络登录。HTTP-only弱安全安装保留显式受信回环/隔离链路选择，不自动从WSS降级，不宣称符合公网机密性目标。

时间由 DB 判 route 有效期/expiry，客户端monotonic计时；明显时钟偏差告警并刷新，不能放宽JWT exp。证书/签名钥采用有限重叠和失效拒绝；有界重连处理TLS轮换，不禁用校验。NATS服务发现不能泄露内部管理地址给外部客户端，公布的服务器地址须通过允许的外部入口访问。

## 3. Auriga应用信封：只补Broker不了解的业务语义

NATS payload首发UTF-8 JSON，最大64KiB（含应用头），正文继续现有16KiB上限；WS上是NATS原生二进制wire，不是“一条WS text frame一个Auriga JSON”。配置Broker max_payload、pending bytes、连接/订阅限额与SDK缓冲，应用在反序列化前再次限长。大文件经resource对象上传，只发授权引用/摘要/大小。应用不做fragment；NATS/WS分帧由官方实现。关闭压缩，具体代理/SDK读写超时通过接入spike定值。

**SDK 缓冲硬门禁**：pendingLimit 是 flush 阈值，不是硬上限；WebSocket send 也不自动提供字节硬 cap，源码证据见验收 §1。N0/N1 必须证明应用队列、SDK 与 socket 缓冲的总上界。断线即停止向 SDK publish；超限明确失败并关闭连接。不能私改 SDK 内部字段或用 Go 客户端行为推断 JS。

JSON便于沿现有Go/Java/Node契约生成，不能声称性能更高。只有实测解析/带宽成瓶颈才协商Protobuf payload；NATS协议保持不变。`app_version`在READY协商，major不兼容拒绝，minor 的未知可选字段须按下述规则保留并忽略；必要安全能力不降级。N/N-1滚更用相同应用schema互通测试。服务器签名/source_attestation/inbox_only 是本轮 required_features（server_auth_v1、source_attestation_v1、inbox_only_v1），不支持的旧插件必须拒绝 READY/新交付，不能作为未知可选字段忽略；启用广播另需 broadcast_grants_v1。安装信任根及取有效密钥集在 READY 验证前完成，不能用尚未可信的 READY 响应自带公钥建立信任。

示意（合成地址 from=aur1:3f.1a2、to=aur1:40.0；十六进制 3f/40/1a2 对应十进制 63/64/418；realm=2 为合成组织域，不含真实身份）：

```json
{
  "app_version": "1.0",
  "type": "delivery",
  "epoch": "42",
  "delivery_id": "server-delivery-id",
  "mail_seq": "1205",
  "target_node": {
    "realm": "2",
    "node": "64"
  },
  "recipients": [
    {
      "recipient_id": "server-recipient-id",
      "local": "0",
      "type": "system"
    }
  ],
  "source_context": {
    "realm": "2",
    "node": "63",
    "user": "source-user-fixture",
    "message_digest": "server-canonical-digest",
    "sender_kind": "agent",
    "display_label": "协作会话"
  },
  "message": {
    "message_id": "server-message-id",
    "from": {
      "version": 1,
      "realm": "2",
      "type": "session",
      "node": "63",
      "local": "418"
    },
    "to": {
      "version": 1,
      "realm": "2",
      "type": "system",
      "node": "64",
      "local": "0"
    },
    "reply_to": {
      "version": 1,
      "realm": "2",
      "type": "session",
      "node": "63",
      "local": "418"
    },
    "thread_id": "server-thread-id",
    "thread_sequence": "1",
    "in_reply_to": null,
    "kind": "first_contact",
    "flags": [],
    "ttl": 86400,
    "ext": {
      "e2e_signature": null
    },
    "body": {
      "text": "首次联系示例"
    },
    "sender_kind": "agent",
    "message_class": "unicast",
    "intent": "request",
    "authorization_ref": {
      "source_binding": "source-binding-fixture",
      "source_revision": "3",
      "policy_revision": "8",
      "broadcast_grant_id": null
    },
    "created_at": "2026-10-25T00:00:00Z",
    "expires_at": "2026-10-26T00:00:00Z"
  },
  "message_state_version": "1",
  "server_auth": {
    "version": 1,
    "alg": "Ed25519",
    "kid": "realm-2-key-fixture",
    "issuer_realm": "2",
    "issued_at": "2026-10-25T00:00:00Z",
    "not_after": "2026-10-25T00:05:00Z",
    "audience": {
      "realm": "2",
      "node": "64",
      "incarnation": "node-incarnation-fixture",
      "epoch": "42"
    },
    "request_id": "fetch-request-fixture",
    "channel_nonce": "ready-nonce-fixture",
    "keyset_version": "5",
    "signature": "EXAMPLE_ONLY_NOT_A_VALID_SIGNATURE",
    "purpose": "business_delivery"
  }
}
```

示例 source_context 是平台根据认证连接和权威消息生成的元数据，不是发送模型可设置字段；接收方仍须按 §1.3 核对消息关联及本地关系。

from/to/reply_to、sender_kind、message_class、authorization_ref 是总部生成并纳入服务器签名的业务字段；以下示例省略真实密码材料，server_auth.signature 为不可验的说明占位，绝不能用于运行时。from/to/reply_to 是结构化完整地址，thread_id/in_reply_to 是平台 ID；v1 reply_to 必须等于 from。kind 取 first_contact/message/reply/control；first_contact 仅请求目标用户授权，不携带可生效的个人 allow/默认设置，模型在 flags/ext/body 声称“已同意”也无效；进度、状态、唤醒、产物引用由相应 body 子类型及 durability 表达，不新增隐式绕权限的路由入口。first_contact 只允许 message_class=unicast、to.type=system 且 local=0，真实 node 的非系统 local 不得用 0；group 必须 node=0、local≥1，按 §1.4 解析。其他 local 的 type 由其所属节点在对话介绍时固定，不能改 type 获得新身份。

ttl 为正整数秒，受消息类型默认值与平台最大留存夹紧；总部初次提交计算不可延长的 expires_at，重投沿用同值，不能按接收时间重置。flags 首发为空，未来可选标记不得改变授权/执行语义；关键能力通过 READY 协商及明确 required_features，未知必需能力、地址主版本、kind 或执行控制子类型明确拒绝，不“忽略后执行”。

**端到端签名扩展预留**：message.ext.e2e_signature 首发为 null/省略，仅为未来发送者到接收者的端到端完整性预留，不能替代本轮必需的 server_auth 总部服务器签名；未来可能含 alg、key_id、covered_fields、digest、value，但算法、密钥分发/撤销、规范化覆盖与防重放须另立版本化契约。首发不签发/不校验 e2e_signature 本身，也不把该非空扩展当来源证明或权限；按未知可选扩展保留并忽略行为。若对端另要求端到端签名校验为必需能力，则拒绝协商，不能静默降级。将来签名有效也不绕本地关系、接收策略、归档墓碑或工具审批。

**向前兼容**：未知可选字段（含 ext 的命名空间键、未知非关键 flag）由存储/转发方在限长和 JSON 语法检查后保留，不解释、不投影丢弃；接收方忽略其行为，转发或重试仍保留原数据。不是无条件忽略版本/安全错误：已知字段类型错误、重复 JSON key、地址冲突均拒绝；未知字段不能覆盖 from/to、授权、期限或改变默认动作。总 64KiB、正文 16KiB 上限包含扩展，解析深度/字段数须在契约定有限上限。幂等摘要采用契约固定的 JSON 规范化，覆盖包括未知字段的完整业务信封，排除传输 epoch/mail_seq 等封装；不能由旧客户端丢字段后使用同键改写已存消息。N/N-1 往返必须验证扩展保留、地址精度和未知关键语义拒绝。

最小应用消息为 READY/READY_RESULT、HEAD、FETCH/FETCH_RESULT、RECEIPT/RECEIPT_COMMITTED、ATTEMPT_BEGIN/ATTEMPT_RESULT、CONTACT_DECIDE/DECIDE_RESULT、CONTACT_ASSIGN/ASSIGN_RESULT、EXECUTION_ADMIT/ADMIT_RESULT、STATUS/ERROR。上述处理尝试、用户授权决定、首联唯一分配和执行准入均为私有应用操作，经同一 WSS 上行调用 collab；不开放为模型工具，也不是 Broker 控制帧。DELIVERY 只是 FETCH_RESULT 中的条目，不是另一套重传帧。不透明参与关系恢复单独分页，binding/grant/lineage 在插件本地恢复；NATS 订阅与心跳仍交官方实现。

FETCH 携带 request_id、resume_cursor、受保护的 continuation（含 scan_after/snapshot_head）及本次 max_items/max_bytes。返回条目、受权终态或不暴露正文的空洞说明、当前 head、平台确认前缀和下一页位置。整页含编码头不超过 64KiB，每项（包括 recipients）须预留页封装空间，发送前用实际编码检查可装入单项页；最多 128 个 node 邮箱项只是数量上限；剩余窗口不足则缩页。Node 默认 single-flight，重复请求/响应不改变额度。FETCH 不锁定 delivery、不修改其状态、不建立租约；Core 请求超时可同位置重试。额度耗尽暂停，收到回执确认/本地空间释放才继续。无待取页时不按秒发空 FETCH。

FETCH 可携带最多 256 个 recipient_id 的 held_ids（本地已持有排除集），以及最多 128 个已授权 thread/完整目标地址 的暂停标记；它们只在本次请求内抑制正文重发，不确认投递、不推进游标、不写持久终态。服务端验证归属并按整包 64KiB 限长，跳过已知隔离/暂停线程的后续正文。客户端只能排除已写入自身 ledger 的项，重启从 ledger 重建；丢失或过期的排除提示只导致安全重发。允许按 continuation 跨过这些项查其他线程，但仍保留空洞。

kind/body 按上述枚举承载普通消息、请求/响应、执行控制、进度/状态、唤醒与 artifact_ref；message_class 与 kind 不同，组播/广播只 information/request，所有危险执行控制限定有专权单播；附 `correlation_id/causation_id` 与 `durability`。最终结果/决定必durable，progress可合并丢弃并带revision。控制消息也需专门grant；模型不能因为type叫control就获取高优先级或绕过授权。

ACK_RECEIVED若需要只作为可选应用诊断，不清PG状态；NATS `flush()`/PONG、Core publish成功、JS PubAck、JS consumer ACK均不能映射为host_accepted。Host receipt 以 node 邮箱项分组、逐 local 包含 delivery_id/recipient_id/完整目标地址/host_request_id/digest 与持久接受证据、accepted_by（system/session）；系统首联额外携带已固定的 assigned_address（如已有）及执行状态。host_session_id 留本地，仅按授权诊断导出；只有 collab 事务结果能形成 RECEIPT_COMMITTED。RECEIPT 必须带 status：rejected 表示 §1.3 的本地策略拒收，由平台提交统一终态，不能冒接受；host_accepted 才是持久接受；quarantined/retryable/outcome_unknown 仅报告处理状态，不冒成功 ACK。每种状态更新带幂等操作 ID 和状态版本，旧版本不得把终态恢复成可执行项。

首联状态和隐私裁剪按 §1.2，不能把 contact_pending 转为自动发送重试。未获状态查看权时以下目标相关错误不得穿透统一回执。错误族建议`network.contact_pending|contact_denied|contact_expired|unauthorized|revoked|stale_epoch|conflict|quota_exceeded|overloaded|expired|resume_unavailable|host_incompatible|outcome_unknown|protocol_error`，待契约统一。返回scope、关联request、retryable/retry_after，不泄漏堆栈/凭据。发送超时为outcome_unknown，查同幂等键；源端认证/全局入口限流错误不转换成功；目标相关拒绝对无状态查看权者仅保留 §1.2 的提交回执，不能显示为消息送达。传输重连期间禁止SDK盲目缓存并重发任意业务写；持久发送必须依赖应用幂等键和权威结果。
### 3.1 组信封与逐成员回执

组消息的 message.to 保留组地址，不篡改成某个 local；target_node/recipients 是平台固化的派发元数据，绑定消息摘要、snapshot_id 和成员实例，客户端不能伪造/删减后声称全收。单 node 的正文只出现一次，如下是 100 成员/10 node 快照中的一台（仅展示该台两个成员，完整快照其余成员另存）：

```json
{
  "app_version": "1.0",
  "type": "delivery",
  "epoch": "42",
  "delivery_id": "node-item-64",
  "mail_seq": "1206",
  "snapshot_id": "snapshot-17-v8",
  "target_node": {
    "realm": "2",
    "node": "64"
  },
  "recipients": [
    {
      "recipient_id": "recipient-a",
      "local": "2",
      "type": "session",
      "membership_id": "join-a-v3"
    },
    {
      "recipient_id": "recipient-b",
      "local": "3",
      "type": "session",
      "membership_id": "join-b-v1"
    }
  ],
  "source_context": {
    "realm": "2",
    "node": "63",
    "user": "source-user-fixture",
    "message_digest": "server-canonical-digest",
    "sender_kind": "agent",
    "display_label": "协作会话"
  },
  "message": {
    "message_id": "group-message-1",
    "durability": "durable",
    "dispatch_mode": "paged",
    "from": {
      "version": 1,
      "realm": "2",
      "type": "session",
      "node": "63",
      "local": "418"
    },
    "to": {
      "version": 1,
      "realm": "2",
      "type": "group",
      "node": "0",
      "local": "23"
    },
    "reply_to": {
      "version": 1,
      "realm": "2",
      "type": "session",
      "node": "63",
      "local": "418"
    },
    "thread_id": "group-thread-17",
    "thread_sequence": "9",
    "in_reply_to": null,
    "kind": "message",
    "flags": [],
    "ttl": 86400,
    "ext": {
      "e2e_signature": null
    },
    "body": {
      "text": "组通知正文仅此一份"
    },
    "sender_kind": "agent",
    "message_class": "multicast",
    "intent": "information",
    "authorization_ref": {
      "source_binding": "source-binding-fixture",
      "source_revision": "3",
      "policy_revision": "8",
      "broadcast_grant_id": null
    },
    "created_at": "2026-10-25T00:00:00Z",
    "expires_at": "2026-10-26T00:00:00Z"
  },
  "message_state_version": "1",
  "server_auth": {
    "version": 1,
    "alg": "Ed25519",
    "kid": "realm-2-key-fixture",
    "issuer_realm": "2",
    "issued_at": "2026-10-25T00:00:00Z",
    "not_after": "2026-10-25T00:05:00Z",
    "audience": {
      "realm": "2",
      "node": "64",
      "incarnation": "node-incarnation-fixture",
      "epoch": "42"
    },
    "request_id": "fetch-request-fixture",
    "channel_nonce": "ready-nonce-fixture",
    "keyset_version": "5",
    "signature": "EXAMPLE_ONLY_NOT_A_VALID_SIGNATURE",
    "purpose": "business_delivery"
  }
}
```

组播/广播 local 收件用 acceptance_mode=inbox_only、accepted_by=session 或 system，证据来自绑定该 local 的持久收件箱，禁止为了生成 host_accepted 调 prompt；execution_status=not_applicable。单播执行回执保持既有语义。

10 node/100 local 在上限内也可选 atomic；此例显式 paged 仅演示封装。FETCH 只给当前可读 locals，撤销项给受权终态而不给正文；只要该 node 尚有合法成员可读，正文传该 node 一次，再由受信插件按 local 分派，不能防已攻陷的目标 node 窃取同机内容。held_ids 只能排除对应 recipient，不能一个 local 已持有就隐藏同项其他 local；若所有待交付 recipient 均已持有，才抑制整项正文。服务端保完整快照/总数，不以客户端返回列表长度推断完成。

RECEIPT 可在一个操作中列多个结果（每个含 recipient_id、地址、digest、host_request_id、status、版本和证据），如 a=host_accepted、b=rejected/target_unavailable；缺席的成员保持 pending，重送相同子结果幂等，某项格式/归属非法则整次请求拒绝并要求修正，不能静默遗漏。平台逐项提交状态，只有该 node 的全部 recipients 均有接受或合法终态，才关闭一个 mail_seq 并推进连续前缀；quarantined/retryable 不能跨过该项。已接受的 a 不因 b 的重投再 prompt，b 的 attempt/准入不占用 a 的记录。

STATUS 分开给 dispatch_status（pending/dispatched/partial/closed）、原快照 total_members/total_nodes，以及 accepted、rejected、expired、canceled、skipped、unresolved、pending 的互斥接收计数（quarantined 是 pending 子集），总和等于 total_members，未创建邮箱也包含在 pending/终态中。执行统计另列，不与收件相加。只有 accepted=total_members 才叫全部接受；99 接受+1 拒绝显示部分接受，全部终态也可能零成功。空快照拒绝发送，不作 0/0 成功。发送方有权看总数/通用原因，逐地址详情须原快照与当前查看权，不暴露拒收生命周期/名单；无查看权仅通用状态。组 sys/广播 accepted_by=system 不能显示为会话或模型接受。

**请求回应聚合**：intent=request 时原消息另存 response_deadline（默认 ≤24 小时且不超 expires_at）和按快照 recipient 的 response_status。默认 reply_mode=private、to=原 reply_to/from，回复必须带平台固化的 origin_message_id/origin_recipient_id；人的 sys 回人 UI，agent local 回该 local 的信息收件箱，不自动 prompt。平台验证原接收身份、当前关系、期限/撤回状态；组播成员回复还须当前有效成员，广播收件者不需 broadcast.send 才能私下回应。回复是新的单播 message，独立四级 ACK；接收回执不等于已回应，填写回应也不等于已完成请求。

每原 recipient 最多一个最终回应记录，允许最多 3 次显式修订、单调 response_revision；重发同键幂等，不自动另发“收到/感谢”回复。源/目标 node 请求与字节限额叠加、满时明确 backpressure，不因高优先级绕预算。聚合按原成员互斥计 pending/responded/declined/timed_out/unavailable，总和为原 total_members；同成员多次修订不增人数，无权查看者不见细分地址。撤回原请求使未回应项 unavailable，迟到回应拒绝且已有回应只留审计/撤回标记。当前成员 reply_all 必须显式操作并重新通过组/广播发言权，自动回应管线永不 reply_all。广播同用户多 node 的应答按原 sys recipient 分开计，UI 可另显示去重用户视图，但不能改写真实分母或把一台的回应算给其他台。

新增错误：network.invalid_address（段数/编码）、unsupported_address_version、realm_not_supported、requires_paged_dispatch、group_not_available（不存在/无组查看权同外观）、group_publish_forbidden（仅有权组可诊断）、quota_exceeded；成员退出/屏蔽/归档对外统一 target_unavailable，分页可能 pending/backpressure/partial，不转成发送失败后换键重发。未知结果仍查原幂等键。另加 network.broadcast_forbidden（缺权/到期/撤销/冻结统一，无细分授予泄露）、sender_mismatch、class_mismatch、dangerous_multicast_forbidden、reserved_display_name、message_recalled；验签本地错误 signature_required/signature_invalid/key_unavailable 只作受控诊断，不能对不可信消息 ID 上报成功/终态。

human 发请求类广播的下行示意（总部确认 user 授予后生成，不能从本例拷字段取得权限；签名仍为不可验占位）：

```json
{
  "app_version": "1.0",
  "type": "delivery",
  "epoch": "42",
  "delivery_id": "broadcast-node-item-64",
  "mail_seq": "1207",
  "snapshot_id": "broadcast-snapshot-fixture",
  "target_node": {
    "realm": "2",
    "node": "64"
  },
  "recipients": [
    {
      "recipient_id": "broadcast-sys-recipient",
      "local": "0",
      "type": "system"
    }
  ],
  "source_context": {
    "realm": "2",
    "node": "63",
    "user": "source-user-fixture",
    "message_digest": "server-canonical-digest",
    "sender_kind": "human",
    "display_label": "通知发送者"
  },
  "message": {
    "message_id": "broadcast-request-fixture",
    "durability": "durable",
    "dispatch_mode": "paged",
    "from": {
      "version": 1,
      "realm": "2",
      "type": "system",
      "node": "63",
      "local": "0"
    },
    "to": {
      "version": 1,
      "realm": "2",
      "type": "group",
      "node": "0",
      "local": "1"
    },
    "reply_to": {
      "version": 1,
      "realm": "2",
      "type": "system",
      "node": "63",
      "local": "0"
    },
    "thread_id": "broadcast-thread-fixture",
    "thread_sequence": "1",
    "in_reply_to": null,
    "kind": "message",
    "flags": [],
    "ttl": 86400,
    "ext": {
      "e2e_signature": null
    },
    "body": {
      "text": "请在方便时确认信息，回复仅回发送者"
    },
    "sender_kind": "human",
    "message_class": "broadcast",
    "intent": "request",
    "authorization_ref": {
      "human_intent_id": "consumed-intent-fixture",
      "broadcast_grant_id": "broadcast-grant-fixture",
      "grant_revision": "4",
      "subject_type": "user",
      "policy_revision": "8"
    },
    "created_at": "2026-10-25T00:00:00Z",
    "expires_at": "2026-10-26T00:00:00Z",
    "response_deadline": "2026-10-26T00:00:00Z"
  },
  "message_state_version": "1",
  "server_auth": {
    "version": 1,
    "alg": "Ed25519",
    "kid": "realm-2-key-fixture",
    "issuer_realm": "2",
    "issued_at": "2026-10-25T00:00:00Z",
    "not_after": "2026-10-25T00:05:00Z",
    "audience": {
      "realm": "2",
      "node": "64",
      "incarnation": "node-incarnation-fixture",
      "epoch": "42"
    },
    "request_id": "fetch-request-fixture",
    "channel_nonce": "ready-nonce-fixture",
    "keyset_version": "5",
    "signature": "EXAMPLE_ONLY_NOT_A_VALID_SIGNATURE",
    "purpose": "business_delivery"
  }
}
```

### 3.2 总部服务器签名、密钥生命周期与撤回重放

**安全目标与事实依据**：首发下行采用 Ed25519 服务器签名；算法及测试向量见 [RFC 8032](https://www.rfc-editor.org/rfc/rfc8032)，JSON 规范化采用 [RFC 8785 JCS](https://www.rfc-editor.org/rfc/rfc8785)。本协议字段、时限和密钥运维是 Auriga 待审设计，不是 RFC 或现有代码已经提供的功能。固定算法白名单、核 issuer/audience 和不同用途隔离借鉴 [RFC 8725](https://www.rfc-editor.org/rfc/rfc8725) 的验证原则；此处不是把业务信封当登录 JWT，也不复用 Broker JWT 密钥。总部签名防无私钥者通过错误 ACL 注入/改写信封，不防丢包/阻断、授权错误、签名权威失守或泄密；签名不是加密，ACL 泄露正文的风险仍存在。

**签发边界**：签名私钥只在总部受控签名组件/密钥设施，按 realm 与用途隔离，不放 NATS、客户端、agent、日志或普通配置。签名组件只接受已认证内部业务服务的结构化请求，核权威已提交消息/允许的状态和实时授权结果；不提供“给任意客户端 JSON 签名”的 oracle。durable 先 PG 保存，再对当前有权目标/世代签发；签名失败不回退无签名发送，已存消息留 PG 重取，发送方响应丢失按原幂等键查结果。ephemeral 无持久正文，但须同样在线鉴权、生成可信头并对每目标 node/当前 audience 签发，不能直接把内部 Core 到达当可信。

**覆盖范围**：业务 DELIVERY、状态/撤回更新以及会影响客户端状态的 READY_RESULT/FETCH_RESULT 页头/RECEIPT_COMMITTED/ADMIT_RESULT/STATUS/ERROR 均须服务器签名；HEAD 也签名，验证失败最多触发受限重新同步提示，不改 head/cursor/权限。node 邮箱条目可独立验签，分页封装另签其 head/cursor/continuation/条目清单，禁止替换或截断结果列表后保留一个有效成员签名。实际契约须定义唯一封装布局，两份 JSON 是单条 node delivery 的字段示意，不能当真实签名测试向量。

server_auth 含 version、purpose（business_delivery/protocol_response/management_state，按信封 type 固定并用不同用途密钥）、固定 alg=Ed25519、kid、issuer_realm、issued_at/not_after、audience=(realm,node,node_incarnation,connection_epoch)、request_id、channel_nonce、keyset_version、signature。签名输入为明确 UTF-8 域分隔串 Auriga.ServerEnvelope.v1、一个零字节和 JCS(完整下行对象但仅删除最外层 server_auth.signature)。JCS 对象含 server_auth 其余字段及 from/reply_to/to、message_class、sender_kind、authorization_ref、source_context（真实用户和安全显示标签）、message_id/thread/序号/kind/intent、created_at/expires_at、正文与未知可选扩展、message_state_version、目标 node/recipients/成员实例、delivery_id/mail_seq/snapshot、状态与错误等；任何语义值改变验签失败（JCS 允许对象键顺序/空白变化）。message_digest 采用 SHA-256(JCS(message)) 并纳入签名，不能只签头而把正文留作可替换字段。目标列表或消息摘要不由客户端定义可选签名覆盖集。

JSON 先按字节/深度/字段数限长，拒重复 key、非法 Unicode、非有限数值，uint64 始终十进制字符串；不得把 JCS 的字符串偷偷做 Unicode 归一化，名称审核的规范化是签发前的业务步骤。签名固定 64 字节（线上用无填充 base64url），kid 只作已信任密钥集内索引；拒 alg=none/算法替换、重复或缺失关键字段、错误 issuer/用途/受众。使用经验证的密码库与标准向量，不手写曲线或凭本例签名字串冒验证。校验失败前不显示“总部已确认”，不写成功回执、不推进 cursor、不执行、不将未验签 ID 计到被冒充发送者的扫描/处罚记录；仅受控连接级审计/限流。失败报错限流避免攻击者用签名校验耗尽 CPU/日志。

**时效与防重放**：封装签名默认 ≤5 分钟，not_after 不晚于业务 expires_at、签名密钥有效期及本次所需授权截止；它是短下行证明，不是永久权限。管理撤回/冻结状态更新以独立管理权限与自身 update 有效期签发，不能受原广播已撤销 grant 或原消息过期阻止；仅隐藏/状态同步，不借此重发正文。READY 随机 channel_nonce 由插件本次连接准备产生并经认证握手绑定；结果还绑定原 request_id/请求 nonce，响应只能匹配当前待决请求，HEAD/ephemeral 等主动提示匹配当前 channel_nonce/epoch。接管、重连和重试按新有效上下文重新核权/签发，message_id/recipient_id/digest 不变，不改变业务 TTL。插件核时间（建议容差 ≤60 秒，不能据此延长业务授权/到期）、接收对象、epoch/nonce、请求关联和单调状态版本；跨 node、跨请求、旧世代的真签名也拒绝。本地去重仍按 recipient，不以新签名/新 kid 当新消息。签名不能阻止同一有效上下文内曾合法的旧消息被重放，故 durable 状态/回执、版本与当前授权检查不能删除。

特别是广播显示/从缓存恢复前，插件经已认证请求查询该消息当前 state_version、grant/freeze/recall 状态，签名状态响应绑定新的请求 nonce，作为本批展示屏障；撤回同步与显示在本地同一版本锁排序。该查询可批量并入 FETCH/READY 恢复，不能变成每消息短轮询。新的签名页只证明核验时刻有效，不是未来 5 分钟免撤销；无当前状态证明时不展示广播旧正文，签名有效也不足以放行。屏障查询后立即发生撤回仍可能产生在途显示，在线即时更新与重连先同步缩小窗口，不承诺全球零窗口。

**信任引导与轮换**：安装/已认证组织接入预置总部信任根（离线根公钥/受控指纹及官方 HTTPS 端点），不得从消息附带公钥、kid URL、jku/x5u 或 NATS 临时 PUB 建立信任。插件通过固定已验证 TLS 的配置端点取根签名的 realm 密钥清单，含单调 keyset_version、issued_at/valid_until、kid、公钥、用途、有效期和吊销状态；持久防版本回退，缓存新鲜度上限建议 5 分钟，按 min(valid_until,issued_at+5 分钟) 判定，重新下载旧清单不能重置新鲜度。未知 kid 只允许一次合并、限频的受信刷新，失败等待/拒绝，不下载任意地址。首连/离线恢复先拉当前清单；清单过期、时钟不可信或吊销状态不可确认时停止新业务处理，保有界待验元数据/PG pending，不以过期缓存放行。

常规轮换先发布新公钥并等待至少一个密钥集刷新窗口，再切签名；旧公钥继续验证其有效期内、未吊销的旧封装，至少覆盖最大签名 TTL+时钟容差，不因换 kid 重执行。已离线超过 5 分钟的客户端重新 FETCH 当前签名，不要求旧封装撑满 7 天离线期；旧公钥与签发审计可保留到消息/审计窗口结束，仅用于历史证据。私钥泄露时立刻停签、吊销 kid、发布更高版本清单并发送签名状态提示；使用可信 HTTPS/根签名清单核实，不能由已泄露业务密钥自证解除吊销。新清单得知吊销后旧签名一律不作新操作依据，待交接原 PG 消息可由新密钥重签，已接受事实不倒退。推送丢失时暴露窗口受密钥集新鲜度约束（目标 ≤5 分钟，非零），离线无新交付；信任根更换须可信配置/软件分发的显式轮换，不用任意消息自举。

验签失败或密钥设施不可用是可观测安全/可用性事件：无自动降级、无伪 host_accepted；无权或坏签 frame 不给其声称 recipient 生成 Revoked tombstone，避免伪造包把真消息删掉。正式实现必须验证 Go/Node 的规范化、签名向量和跨版本未知字段一致性，并测签名/刷新/撤回队列成本。message.ext.e2e_signature 仍仅预留，不参加信任选择、不替代 server_auth；保留其字节时它作为普通扩展被服务器签名覆盖。

## 4. 两个状态机，不混淆投递和执行

以下状态机、attempt 与执行准入均针对 **recipient_id**；delivery_id 只标识包含多个 recipient 的 node 邮箱项。

```mermaid
stateDiagram-v2
  [*] --> Pending: PG邮箱事务提交
  Pending --> Pending: FETCH或重复提示 / 无状态领取
  Pending --> HostAccepted: 持久receipt与PG回执事务
  Pending --> Expired: 到期禁止新准入 / 留存未知标记
  Pending --> Canceled: 取消先于执行准入
  Pending --> Revoked: 权威授权撤销
  Quarantined --> Skipped: 有权终态跳过
  Quarantined --> Expired: 原截止或隔离截止到达
  Quarantined --> Revoked: 权威授权撤销
  Pending --> Unresolved: 持久未知结果与停止自动重试
  Quarantined --> Unresolved: 持久未知结果与停止自动重试
  Pending --> Quarantined: 毒消息或宿主不兼容
  Quarantined --> Pending: 有权修复 / 同recipient
  Quarantined --> HostAccepted: 经核实的既有持久receipt
```

Expired/Canceled/Revoked/Skipped 是可审计的传输终态，不是执行成功。隔离不延长原 expires_at：平台保存 quarantine_deadline = min(expires_at, 首次隔离时间 + 24 小时)，服务级到期任务按索引处理 Pending 和 Quarantined，即使 node 离线也提交终态。扫描周期目标 60 秒；到期时先禁止新处理/准入，不等待扫描才能拒绝过期请求。撤销及授权跳过同样可结束隔离，记录操作者、原因和策略版本。

outcome_unknown 是执行结果未知，**单凭本地报告不能越过游标**。平台核实原准入/尝试与身份后，持久提交 Unresolved 传输 tombstone（终态记录）及 execution_status.outcome_unknown，禁止该 recipient 自动再准入；这时才允许确认前缀越过。未知事项仍在独立待对账清单内，可查询原请求/结果，绝不显示 execution_completed。迟到结果可补充原执行记录，不撤回 tombstone、不自动重做。Quarantined 本身不能越过前缀；HostAccepted 或上述已提交终态才可以。

授权状态与传输/执行状态分别记录：contact_pending 时系统 recipient 仍 Pending，提示不产生 HostAccepted；第一层拒绝/过期写 Revoked/Expired 传输终态（业务显示 contact_denied/contact_expired），已交接后的撤销只更新关系/执行状态。

上述 `HostAccepted` 结束传输交接，不结束执行生命周期。首联系统已接受后，contact 另保 awaiting_session / creating / session_ready / closed 及创建未知状态；到期、取消、撤销、隔离和未知只更新 contact/执行状态，不将已确认的传输倒退为 Pending 或再推进一次游标。平台到期扫描必须同时覆盖未决 contact（包括已有 system receipt 的项）。本地系统收件和会话收件分别记录，不能因为存在 system receipt 就跳过尚未准入的会话处理，也不能将 session 阶段再次调用误认为系统重投。组播/广播及其信息回应的执行状态为 not_applicable；显式后续本地操作另记 ID，不回写成原消息自动执行。其余有权单播另记录 `not_started → admitted → running → completed|failed|canceled|outcome_unknown`，每次变化都是有版本的事件/结果，不修改原 message 内容。`admitted/running` 需关联同 recipient 和原生 turn/request 身份，不能由 LLM 文本“收到/完成”触发。

平台 ACK 四级：

1. `platform_stored`：小批正文/全部 node 邮箱事务提交；大批正文/完整快照/预算/可恢复派发计划提交，尚有未派发页须显示 pending。数据库 durability 档和失效假设必须随部署披露。
2. `transport_received`：对端已读帧，不耐久。
3. `host_accepted`：宿主持久接收可恢复；平台 ACK 事务已提交才能展示。非系统投递必须有正确 local 证据；inbox_only 的组播/广播及其请求回应只证明绑定 local 的插件耐久收件箱接受，不声称已进入模型 prompt，必须展示 acceptance_mode=inbox_only；首联获准后本地系统耐久入箱标 accepted_by=system、awaiting_session；授权前总部保存/本地提示不算此 ACK，之后新会话持久接受另标 accepted_by=session/assigned_address，绝不把系统入箱显示成 agent 接收。平台只存证据摘要和不透明地址，原生 session 映射留插件。
4. `execution_completed`：宿主可信完成事件和结果记录，且语义满足本请求的完成契约。turn/end 可能代表失败/取消，不能无条件视为成功。

至少一次只保证有效期、保留期和故障模型内的已接受消息可恢复投递。网络不保证模型一定会响应，不保证工具副作用天然幂等。执行第三方写入应使用其业务幂等键或事务收件表；外部动作成功而结果落盘前崩溃时，无法查询结果则标 outcome_unknown，禁止自动重复不可幂等操作。连接 epoch 只能 fence 后续网络准入，不能杀死已开始的外部 HTTP 请求。

## 5. PostgreSQL 权威状态与事务

沿现有 collab 表新增迁移，不修改已合 migration；tenant_id、RLS、含 tenant 的 FK 保留。拟增加：

- source_binding / human_send_intent / broadcast_grant：来源类别与不透明 local 绑定、短时用户发送意图、总部广播高危授予/版本和审计；不把本地会话树上传。
- message_state / sender_freeze / state_update_plan / response_status：广播撤回/冻结版本、逐 node 状态更新与原 recipient 的业务回应；与收件/执行事实分开。
- connection_route：当前 epoch/ingress、网络授权引用、server_id/CID/服务器运行世代与有效期。新 FETCH/写操作核当前路由。
- contact_request / contact_decision：总部系统收件箱的请求状态、两层决定结果/不透明版本、截止和审计关联；个人规则/名单及默认档位只在插件。隐私入口的 submission 记录与正式 contact 关联，不生成虚假投递。
- node_policy / conversation_relation / contact_assignment：总部保节点策略、完整不透明地址的线程关系、关系来源消息/撤销版本与首联唯一分配/创建准入；binding_grant/session_lineage、宿主 session 映射和 root/祖先预算下沉插件本地耐久库，不再作为总部会话结构表。
- node_mailbox：head_seq、committed_cursor、保留窗口下界；以邮箱行锁分配序号，事务回滚同时回退。未确认数/字节与 delivery 在同事务更新，用索引找在线积压，不引入 dirty_version、dispatch_ready 或 owner generation。
- message / dispatch_plan / recipient_snapshot：唯一正文和有界派发计划/快照、成员实例、费用预留、页进度及未派发项状态，见 §1.4；已有 dedup/outbox 同事务保留。
- delivery/node_mail_item：message 引用、target_realm/node、mail_seq、expires_at 与聚合状态，同消息同 node 唯一；不复制正文。recipient_status：每 local 的稳定 recipient_id、完整目标/成员实例、quarantine_deadline、原因、attempt_count/active_attempt_id、交接/执行版本。每 node 邮箱序号唯一，不增加 lease_epoch/delivery_token。
- host_receipt：稳定 recipient_id 及所属 delivery_id、digest、宿主 request、目标地址、持久证据摘要及接受状态；以 (tenant, recipient_id, accepted_by) 区分 system/session 阶段，同阶段异体拒绝。非系统项只允许 session；组内 sys/广播只允许 system 且不自动创建会话，首联 session 阶段还核唯一 assigned_address。首次持久接受只关闭该 recipient；全部成员已交接/终态才推进邮箱稀疏回执/连续前缀和释放该 node 传输额度，后续阶段只追加证据、不重复释放。
- execution_status / operation_dedup：以 (tenant, recipient_id) 唯一记录执行准入；含 admission_id、获准 epoch/进程 boot ID、host_request_id、状态版本和结果。epoch 变化不删除这条唯一记录；另保留源操作幂等 tombstone。不是业务任务调度表。

小批 send 在 ≤32 node 且 ≤256 local 内，按稳定次序锁邮箱、thread 和配额，全成或全退；大批按 §1.4 先存计划再每页事务派发，不声称跨页原子。FETCH 逐项核权，contact_pending 仅给目标审批 UI 提示/受限预览，不进入正文交付接口；获准后才正文交接。只读有界页面，不持 DB 锁等宿主/网络。RECEIPT 事务核节点身份、固化目标地址/关系、digest 与原 request，写幂等回执、更新稀疏已确认集合并推进无空洞前缀。已 host_accepted 的同 receipt 返回原结果，不再次扣/释放配额。PG 错误不返回成功。

允许旧连接迟到 RECEIPT 的范围很窄：连接所属网络授权仍有效、同 tenant/node/incarnation 与原完整目标地址、digest/request 身份一致，且只能补录接受事实或返回已有结果。它不能改路由、准入执行、取消或重映射会话。因吊销而失权的旧连接拒绝；新有效连接或授权对账流程可提交本地持久证据。若过期/取消先发生，记录 receipt 事实与冲突结果，不覆盖已存在的准入终态，不自动重新 prompt。旧 epoch FETCH 和迟到页面仍拒绝。归档/关闭/删除后的历史 receipt 只作元数据对账，不属于新入站正文，不可因此自动重开会话。

**处理尝试的持久计数**：进入可能崩溃的宿主 deliver 前，网络插件先生成 attempt_id 并写本地 ledger，再在线调用 ATTEMPT_BEGIN。collab 在短事务中核当前 epoch/授权/期限，按 recipient 原子增加 attempt_count 并登记本次 attempt_id；同 ID 重发不重复计数，FETCH 本身不计数。平台计数是权威，本地仅镜像尝试次数和 entered 标记。只有本次有效成功响应且本地 entered 已持久化，才可调用一次 deliver；重复响应、重启或复制 ledger 不允许重新进入同一尝试。响应丢失可查状态，但查询结果不产生新的调用许可。

崩溃后未完成尝试不退款：无执行准入记录时，当前连接可用新 attempt_id 申请下一次，旧尝试同时失效；已有执行准入则先按下述规则对账，不再自动调用 prompt。最多自动 5 次；第 5 次失败/重启发现未完成，或第 6 次申请时，由平台持久转 Quarantined。首联从系统处理到会话处理共用该 recipient 的累计次数；已 system-accepted 时转 contact 的隔离/关闭状态，不倒退传输，总部等待授权、本地审批提示与策略评估均不消耗宿主尝试。CONTACT_ASSIGN 额外记录一次不可重领的创建许可，attempt 增加不能越过已发出的创建/执行许可。明确毒消息可提前隔离，经 RECEIPT(status=quarantined) 上报；即使插件崩溃到无法上报，平台仍能依据计数和下一次请求隔离。无人重连时由原 expires_at 收尾。管理员修复后若批准额外尝试，必须显式审计额度，重启/换 epoch/反复取页均不得清零。

**执行前的在线准入**：组播/广播和其 inbox_only 请求回应在平台与插件都禁止本接口；普通控制必须是有专权单播。其他允许执行的单播，宿主在本地 session 锁内完成历史去重、即将 queue/prompt 前，必须调用 EXECUTION_ADMIT，即 admit(recipient_id, target_address, connection_epoch, host_request_id, attempt_id, boot_id)。插件在同一会话锁内先核 §1.3 可信来源、本地单播给出关系、会话仍开放及接收模式，再核当前个人两层授权（包括已有对话的新策略）、工具范围、grant/lineage/映射与策略版本，收到准入响应后、调用宿主前再次检查；collab 核 recipient 固化的完整 to 与请求地址一致、当前 route epoch、IAM/组织互联/node 策略及单播对话关系、仅首联所需的 contact 决定版本及本次执行允许状态、有效尝试、期限及取消版本（首联还核 CONTACT_ASSIGN 固化的执行地址；target 仍为 sys，assigned_address 单列，不能任意替换），事务 CAS 把该 recipient 从未准入变为 admitted，并持久记录 admission_id。READY 接管、取消与 admit 按同一 node/执行记录锁序串行化；只有当前世代第一次成功准入的调用可继续。断线、超时、IAM 故障或旧世代均停在 RECEIVED，不能仅凭旧页面或本地 ledger 执行。

执行准入不是可到期重领的租约：同 recipient 的重复请求只返回已有状态，不另发执行许可。epoch 续接、意外克隆 B 接管的故障、超时或未收到 Host receipt 都不能清除 admitted 记录。准入成功响应丢失，或响应成功后 prompt 前崩溃，可能牺牲自动完成：恢复只查原宿主 request/日志；无法证明结果时提交 Unresolved，不把同请求再次交给另一个进程执行。获准进程把 admission_id 与 invocation_started 持久化后只调用一次；重复响应不重复调用。

如果 A 的 admit 先于 B 接管提交，B 不会得到第二次许可；A 已获准的在途动作无法被网络世代物理收回。A 一旦已知断线/世代失效，或收到迟到的旧世代准入响应，就不得开始 prompt，原准入保留待对账。若 B 接管先提交，A 的旧世代 admit 必须失败。此保证覆盖遵守协议的宿主与意外克隆；不能阻止已持有凭据的恶意宿主绕过插件直接做外部动作。在线 IAM 与 collab 也不是跨服务原子事务，不承诺准入提交后零时间窗口的全局撤销。

同线程按提交 sequence 入宿主；不同线程并发。同一宿主 session 的 prompt 准入由插件串行，即使消息来自多个 thread，也不能并发穿过 hasPromptRequest 检查。顺序只保证入队，不保证多个 agent 的执行完成顺序。阻塞只限对应有序 thread/逻辑 recipient，不能锁住整个 DSH；毒消息先隔离，只有已提交接受或有权的 skip/过期/撤销/Unresolved 终态才能越过对应传输空洞；业务上是否继续该线程执行另由其准入策略决定。

线程内取消请求不被前一条执行消息的队头阻塞：取消进入独立受控控制队列，并以目标 operation_id 与状态版本 CAS。若取消先于 execution admission，结果 canceled，不再执行；若执行已开始，返回 cancel_requested/already_started，待宿主确认。连接断开、工具 timeout 和 channel close 都不隐式取消业务。过期也不撤销已经发生的副作用；已接受但 ACK 迟到的 receipt 进入 reconciliation，不以新请求身份重做。

## 6. 宿主持久 inbox 与崩溃窗口

宿主 adapter 通过 `deliver(page) → receipts` 与网络层解耦；fixture 传输可独立调用。输入含已通过服务器签名及当前状态检查的平台完整地址/epoch/attempt，以及插件本地核验的 binding/grant 上下文，输出逐项接受证据或 retry/quarantined/outcome_unknown。网络层提供私有 admit 回调供宿主 adapter 在 prompt 前调用；fixture 可以替换该依赖验证时序，但不能用本地恒真检查替代生产在线准入。插件本地正文按 message_id/digest 只存一份，node 邮箱 ledger 保存 delivery_id/mail_seq 及完整 recipient 集；另维护逐 recipient 持久 inbox/receipt ledger（收件与回执账本），至少存 `(tenant,node_incarnation,recipient_id,message_digest,target_address,assigned_address,binding_id,host_session_id,host_request_id,state)`；单写者锁、原子事务、真实 fsync/可恢复日志、容量和回收策略必须验证。可以选择兼容当前 Node 平台的嵌入式事务存储，不能仅以普通配置 JSON 的“已保存”作为断电耐久证明。不要把网络凭据与消息正文混写到公开日志。

组消息的有效目标取平台 recipients 内完整地址（realm/node 来自 target_node、local/type 来自该 recipient），message.to 保留 group，不能拿 node=0 去查本地 session；组成员校验替代单播地址给出关系，但不替代生命周期/接收模式/执行授权。组内 sys/广播只做本地耐久收件与 system receipt；组内普通 local 也只做逐 local 耐久 inbox 与 session receipt（acceptance_mode=inbox_only），不走 CONTACT_ASSIGN/create/prompt/EXECUTION_ADMIT。其请求回应默认同样只入箱；本地用户后续显式处理另建操作和审批，不把旧自动执行档位当许可。

inbox_only 路径在验签/本地授权后，用可恢复事务原子保存共享正文、逐 local inbox 关联与 receipt ledger，真实持久化后才回 ACK；只入箱不调用可能自动调度模型的宿主 API。崩溃后按 recipient 去重补回执，缺耐久 provider/磁盘错误不接受。以下步骤仅描述需要进入宿主调度的有权单播会话交付；system 首联按 §1.2 留总部待授权，获准后按 §1.1 做无模型的本地耐久入箱与分配，再对 assigned_address 执行相同的会话准入/持久接受步骤。ledger 区分 system_received、session_received、session_accepted，检查“已接受”时必须匹配阶段。顺序为：

1. 网络层限长并按 §3.2 验服务器签名/目标/时效/重放，再按 §1.3 核受信来源元数据、本地关系、生命周期/接收策略和扫描屏蔽，失败正文不进会话并留拒绝审计；再核完整 to 与插件本地 local→binding→host_session_id 映射、幂等体及 ledger 一致；错配拒绝，不查询当前前台。持久记录 RECEIVED，尚不能报 host_accepted。调用 §5 ATTEMPT_BEGIN 并持久记录 entered 后才进入宿主 deliver；所有重启共用平台按 recipient 保存的次数上限。
2. 非系统 recipient 必须已有本地 local 映射，仅当原会话仍开放才用原 native session 恢复；归档/关闭/删除拒收，不重复 create 来“接收”。系统 first_contact 按 §1.2 总部待授权/CONTACT_DECIDE，再按 §1.1 本地耐久入箱/预留/CONTACT_ASSIGN/原生创建，始终沿用原系统 recipient 及其唯一 assigned_address，不再生成改投前台的 delivery。创建或分配响应丢失查询同身份，不另建随机会话。system receipt 与 session 执行各自持久记录，系统已接受不等于模型已执行。
3. 在同 session 锁内查 ledger 和宿主历史/inbox 的 requestId=recipient_id。已 session-accepted 项只重建回执；未执行项先完成 §5 在线 EXECUTION_ADMIT CAS，成功并持久写 invocation_started 后，普通会话才可 queue/prompt，子 agent 才可进入专用调用。准入不成功只保留 RECEIVED，不执行。
4. prompt accepted 后核对正确 session 及持久 provider，执行 awaited flush；成功后写 ledger HOST_ACCEPTED 并 fsync，随后发 RECEIPT。缺 provider、flush=false、异常、磁盘满均不发成功 ACK。
5. 平台按 §5 核节点/完整地址关系/digest 后提交 host_receipt/recipient 状态及 node 邮箱聚合，回 RECEIPT_COMMITTED。插件只在此后把平台游标推进至无空洞前缀；结果事件走同样可重放 outbox。

| 崩溃点 | 恢复动作 |
|---|---|
| ledger RECEIVED 前 | 平台重投，原 requestId 不变。 |
| RECEIVED 后、尚未申请执行准入 | 保留原 recipient/request。旧 epoch 已收项只可在当前有效连接下重新核权并申请在线 admit；旧世代或离线不得执行。处理尝试次数不清零。 |
| 准入已提交但响应丢失，或获准后 prompt 前崩溃 | 不自动重新准入/调用；查询原 admission 和宿主证据，无法确认则 Unresolved。换 epoch 或另一台克隆不能重获执行许可。 |
| deliver 进入后崩溃、尚无执行准入 | 本次平台 attempt 已消耗；重启申请下一次，最多 5 次后隔离。不能复用旧 attempt 无限调用。 |
| prompt 已入内存、宿主 flush 前 | 宿主可能已开始执行；恢复核宿主日志/外部幂等结果。不能用“没 ACK”推断未执行；无法判定则 outcome_unknown。 |
| 宿主 flush 后、ledger HOST_ACCEPTED 前 | 从同 session inbox/历史核同 requestId；重建 receipt，不重新生成 requestId。历史不足时进入人工/受控 reconciliation。 |
| ledger HOST_ACCEPTED 后、平台 ACK 前 | 在有效连接上提交同 receipt，无需领取 token；不得再次 prompt。 |
| 平台 ACK 成功、响应丢失 | 查询/重送同 receipt，返回现有结果；游标不足只会重复恢复，不丢消息。 |
| 执行完成、结果事件丢失 | 宿主 durable outbox 按同结果 ID 重放；若结果未落盘且副作用不可查询，标 outcome_unknown。 |

**执行前耐久屏障的限制**：目前普通 prompt 会让宿主调度执行，随后 flush 不是“执行前必然持久”的证明。对于需要强恢复保证的执行控制，N1/N2 必须有真实宿主 checkpoint/准入适配证明，或保持为可恢复消息入箱并等待受控执行；不能以插件先写一条 RECEIVED 就宣称任意执行不会重复。系统禁止自动重放结果不明的非幂等动作；这会牺牲该项自动完成，换来不重复危险副作用的准确边界。

## 7. 宿主适配边界

当前锁定宿主的 API、build/hash 与发行源码位置集中见[验收证据 §1.2](failure-validation.zh.md#12-宿主发行源码与接缝证据)。N1 独立验证普通既有/冷会话、首次明确 cwd/preset 的创建，以及 open-thread 返回前已到达消息的映射竞争。先持久预留请求与会话映射，回包和接收路径在同一绑定锁下合并；不能靠重新 create 随机会话消除冲突。

N2 再核专用子 agent delivery、parent address、父不在线和归档。先按 §1.3 检查目标生命周期/关系，失败统一 target_unavailable；通过后普通入口拒绝子 agent 时才返回 host_incompatible，不绕过、不默认继承父能力。fork、历史、等待、中断和产物按宿主原生能力分别验收，保留 Codex 原生协作目标。宿主 adapter 的 fixture 测试可先进行，只有真实宿主证据才能确认兼容；本轮均不执行。

## 8. 游标、恢复与清理

`mail_seq` 在目标 node 邮箱行锁内递增，提交顺序与可见 head 一致。`resume_cursor` 仅表示此前每个 node 邮箱项内所有 recipient 均已有平台提交的 host receipt 或显式终态 tombstone；不是已见最大值。暂时无权或仅本地报告未知的项不能伪造为成功；明确撤销可生成不含正文的 revoked 终态，未知结果只有按 §4 提交 Unresolved 才能越过。授权依赖故障则失败关闭。

分页位置 `scan_after` 与确认前缀不同：FETCH 从平台前缀之后扫描，以快照 head 和有界 continuation 继续。continuation 由服务端签名或等价防篡改，绑定 node/incarnation、扫描位置、快照边界与期限；它只允许读取，不授予新权限，每页重查授权。一次扫到 head 后，后续 HEAD、READY 或已知空洞的退避重试开始新一轮，从平台前缀重新查未确认索引。无未决项时不启动轮询。

例：101 是 thread A 毒消息，102 是 thread B 正常消息。101 持久隔离，scan_after 可推进到 102；102 单独提交 receipt，确认前缀仍停 100。FETCH 排除已接受 102，并跳过 A 被暂停的后续正文，其他线程继续。101 最迟在原到期时间或隔离 24 小时截止转 Expired；提前撤销/授权跳过也可终结。只有平台持久接受或终态提交后，前缀才可越过 101。

空洞限额作用于 (thread, target_node, target_local)，默认其首个未决项之后最多积累 1,024 个本线程投递位置，或 256 个稀疏确认区间；到顶只拒绝该逻辑收件人的新消息，已有消息仍可回执/到期，其他线程继续。这里的跨度只数该线程位置，不以全 node 的 mail_seq 差值计算，否则其他线程流量会替毒消息耗尽预算。每 node 稀疏确认区间默认上限 4,096，另预留 256 个在途回执的最坏增量；总量或磁盘真正到顶才实施 node 级准入背压。

已确认连续后缀在 PG 合并成精确区间；单个 101 空洞后，其他线程连续接受的 102…N 只占一个覆盖区间，不要求把每个历史 receipt 永远当稀疏项保存。区间只能覆盖平台已有接受/终态证明的位置，不能跨未决项；原始回执和执行审计仍按留存期保存，区间本身必须留到前缀推进。无法合并的多空洞按上述配额限入，预留回执/终态空间，不能通过丢证据释放配额。

客户端游标回退时返回平台确认前缀并对账本地 ledger，不重新执行已接受项；客户端超前时纠正到 PG 前缀并保留全部未确认项。不能因客户端上报更大游标而删除数据。越出保留窗口返回 resume_unavailable 与可见 gap 范围，重新核对持久证据，绝不当作全收。分页重叠、响应丢失与乱序只产生重复页面；delivery_id/message_digest 去重正文、recipient_id/digest 去重交付且不按响应到达顺序推进确认。旧 epoch 页丢弃，其持久 receipt 可由新连接幂等补交。

node 邮箱顺序只用于同步，业务入队顺序按 thread/逻辑收件人检查。取消走单独受限控制类别，可按 FETCH 的受权类别优先取，不受正文 thread 队头阻塞；它仍占 node 总预算，控制类别的分页位置也不能推进全局确认前缀。单 session 的 prompt 准入继续串行。

幂等与保留需要闭合：producer_epoch 有服务器截止，建议最长 7 天；过期世代只可查询旧结果，不能产生新写。去重/终态 metadata 至少保留 30 天且覆盖最大离线/重试窗口；清理后仍拒绝旧 producer_epoch，防止旧键重新执行。续期发行新世代不能改变未决操作的原身份。自动 purge 不得先删 dedup 再让旧消息重试成功；有空洞时，其后已确认位置的覆盖证明（原回执或已提交的精确区间）必须保留到前缀推进，不能只因合并就删除尚无区间覆盖的证明；原始回执/执行审计仍遵守各自留存期。配额不足按 §8 的局部/总量范围限入，不能丢证据。本地 receipt 只在平台已确认且窗口结束后回收，磁盘满时明确拒收而非删掉未 ACK 数据。

## 9. 背压、公平性与资源隔离

所有默认数值以[总览 §10](README.zh.md#10-可调默认值设计提案未批准或压测)为集中入口，以下解释实施约束。

每连接未确认投递默认最多 256 个 node 邮箱项 / 1MiB，另限制实际本地 recipient 待处理总数（默认 1,024），每逻辑通道最多 16 条 / 64KiB。adapter 输出队列硬上限 2MiB，回执与恢复控制另保留 64KiB 和 100 条/秒预算。单 DSH 最多 1,000 个活跃通道、5,000 项应用订阅；执行准入并发最多 4，同 session 为 1。超限在分配前拒绝，不把历史会话全部装入内存。

接入 Broker 建议显式设置 max_pending=2MiB、write_deadline=10s。它限制每个客户端的服务端待发送缓冲，不是客户端 SDK 上限，也不限制所有连接的总和。该配置作用于 Broker 进程，若与内部事件共用实例须一并验其影响，不能假称只有外部 account 受影响；必要时拆接入 Broker。原生慢消费断连后仍靠 PG 恢复。HEAD 在 adapter 端每 node 只保最新值，避免慢消费者积累逐消息提示；具体合并预算见总览 §7.1。

窗口由客户端按已取页未回执、本地持久未处理项与执行预算计算；每次 FETCH 给绝对条数/字节上限，并受服务端 node/tenant 限流约束。重复页不重复占用逻辑容量，迟到回执不重复释放预算；换 epoch 先对账 ledger 再继续。没有服务端逐消息在途 lease，也不新增 RECEIVE_WINDOW 协议状态。慢消费或丢 Core 响应按同位置重新 FETCH，不把 SDK 缓存当持久队列。

服务端首发按 tenant/node 请求限流与有界并发处理 FETCH，插件按 root/channel 轮转并保留控制预算；不引入有 owner 的 DRR dispatcher。连续热点、超大批次与控制类别同样受限，防一个 node 耗尽请求池。只有 N3 压测证明需要才增加按字节的公平请求调度。fanout 原子上限 ≤32 个目标 node 且 ≤256 个 local；超限必须 §1.4 的显式分页计划，组/广播按每 node 一个邮箱项、每 recipient 独立回执。广播受总部显式高危授予和独立低频限额，不开放任意全网发布。

插件本地根子树在授权时预留预算、消费时统一计量；总部独立按不透明 local、node/tenant 限额，不声称能从 local 推导子树。父子额度相加不得超过 root，再受 node/tenant 上限约束。默认最大深度 8，每 root 最多 256 个活跃后代；多开子会话不能绕过配额。跨 DSH 委派同时计入源授权树和目标执行预算。

慢消费者先暂停受影响的通道，node 总窗口耗尽才暂停该 node。写超时由 Broker 配置和 SDK 适配实现，并须实测；待投消息留在 PG，adapter 不假定自己能直接关闭客户端 socket。处理尝试按 §5 由平台持久计数，最多自动 5 次后隔离，并遵守隔离截止和局部限额。组播共享正文仍受总本地 pending recipient、逐通道和逐 node 字节预算限制，不能只看邮箱条数就一次调度 256 个 prompt；已取得页面可逐 local 暂停、回执，保留其余成员状态。实例忙、模型不可用是执行状态，不能让 adapter 无限持有正文。临时进度可只保最新版本，耐久消息不得静默丢弃。

磁盘到 70% 告警，80% 限制新 fanout/大正文，90% 拒绝新 durable write，并预留 ACK/终态/回收空间；阈值须按实际磁盘和 WAL 余量设定，不能等 ENOSPC 才处理。过期数据写可查询终态后按策略清理；保留审计摘要与去重 tombstone，异常进入受限隔离区。存储发生实际错误时不再承诺成功 ACK。
