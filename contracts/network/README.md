# Auriga 通信网络契约 v1（N0）

本目录把已合入的待实施设计 [`docs/design/auriga-network/`](../../docs/design/auriga-network/README.zh.md) 落成可校验契约，供 Go（adapter/edge）、Java（collab/iam）、Node（dsh-plugin）三端共用。**N0 只冻结契约与一致性 fixtures，未实现任何服务**；运营默认值（速率、配额、留存）仍以设计总览 §10 为准且待压测，这里只冻结影响编码与互通的硬上限。

| 文件 | 内容 |
|---|---|
| `address.schema.json` | 结构化地址、按字段角色的子集（源/目的/node/会话/系统/组）、uint64 十进制串 |
| `envelope.schema.json` | 业务消息 `Message`、来源盖章 `SourceContext`、`AuthorizationRef`、`ServerAuth`，以及 FETCH 页条目 `Delivery`/`TerminalItem`/`GapItem`/`ContactPromptItem`/`StateUpdateItem` |
| `protocol.schema.json` | 私有应用帧：READY/READY_RESULT、HEAD、FETCH/FETCH_RESULT、RECEIPT/RECEIPT_COMMITTED、ATTEMPT_BEGIN/ATTEMPT_RESULT、EXECUTION_ADMIT/ADMIT_RESULT、CONTACT_DECIDE/DECIDE_RESULT、CONTACT_ASSIGN/ASSIGN_RESULT、LOCAL_REGISTER/REGISTER_RESULT、STATUS_QUERY/STATUS、ERROR |
| `send.schema.json` | 模型工具参数、插件→总部私有发送请求、发送结果 |
| `errors.json` | `network.*` 错误码唯一定义（可重试、可见性、掩码、中英文案） |
| `limits.json` | 协议硬上限 |
| `fixtures/` | 已签名合法帧、签名已知答案向量、地址/帧/原始字节/错误呈现用例 |

参考检查实现与测试在 `contracts/test/network-lib.mjs`、`network.test.mjs`；`network-gen.mjs` 重新生成 `fixtures/frames.json` 与 `signature-vectors.json`（确定性，测试会核对重签字节不变）。

## 1. 通用约定

- **字段命名例外**：网络帧沿用设计的 snake_case（`message_id`、`server_auth`），不用 REST/事件的 lowerCamelCase；它们是 NATS payload，不经 OpenAPI。目录类 JSON（`errors.json`、`limits.json`）沿用 `catalog/` 的 lowerCamelCase。
- **uint64** 一律无前导零十进制字符串（`"18446744073709551615"`），不经 JSON number。JSON number 只用于小整数（`version`、`ttl`、`max_items` 等），且必须是安全整数的整数写法。
- **时间**：UTC 整秒 RFC 3339（`2026-10-25T00:00:00Z`），唯一表示，不带小数秒。
- **ID**：`OpaqueId` = `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`，不可解析；平台生成的建议 UUIDv7。摘要统一 `sha256:<64 位小写 hex>`。
- **严格解析**（任何下行页在 JCS/验签/正文解析前，任何上行帧在处理前）：原始字节 ≤64KiB；拒绝重复键、孤立代理项、字符串内原始控制字符、非整数/超安全整数/指数写法的数字、NaN、尾随数据；嵌套深度 ≤16、单对象成员 ≤128、单数组 ≤256、单串 ≤16KiB、全帧值 ≤8192。全部返回 `network.protocol_error`。
- **生产方/消费方**：schema 里的 `additionalProperties:false` 只给生产方自测；消费方放宽为允许额外字段（与 `contracts/README.md` 约定一致）。**与 resource/runtime 严格受签对象不同**：网络信封允许未知可选字段，存储/转发方原样保留，签名与摘要都覆盖它们；验签必须对“收到并严格解析的原始对象”做 JCS，禁止先映射成类型化结构丢字段再验签。未知可选字段不能覆盖已知字段（重复键已在解析时拒绝）、不能改变授权/期限/默认动作。未知 `kind`、未知控制动作、未知地址主版本、未知 required feature 一律拒绝，不“忽略后执行”。

## 2. 地址

文本 `aur1:[realm.]node.local`：各段无前导零小写十六进制、1–16 位；只解析 2 段或 3 段。JSON 始终完整：

```json
{"version":1,"realm":"2","type":"session","node":"63","local":"418"}
```

| type | node | local | 角色 |
|---|---|---|---|
| system | ≥1 | 0 | 源、目的、recipient |
| session / subagent | ≥1 | ≥1 | 源、目的、recipient |
| group | 0 | ≥1（1=组织广播，普通组从 2 起） | 只作目的 `to` |

文本解析顺序（参考 `parseAddressText`）：

1. 前缀 `aur<数字>:` 且数字≠1 → `network.unsupported_address_version`；不是 `aur1:` → `network.invalid_address`。
2. 段数非 2/3、任一段不符合 `0|[1-9a-f][0-9a-f]{0,15}`（大写、补零、符号、`0x`、空段、子层级 `/`、空白、17 位以上）→ `invalid_address`。
3. 两段只在已认证本域上下文补 realm，无上下文 → `invalid_address`；realm=0 → `invalid_address`；realm≠本域 → `network.realm_not_supported`（不查外部目录、不建外连）。
4. node=0 且 local=0 → `invalid_address`；node=0 为 group，local=0 为 system；其他非零 local 的 session/subagent 由该 node 的登记决定，文本本身不携带。

本域 realm=2 时 `aur1:3f.1a2`、`aur1:2.3f.1a2` 与上面的 JSON 等价；入库、摘要、幂等与关系键一律先规范成完整 JSON。规范文本输出总是三段（`aur1:2.3f.1a2`）；省略 realm 的短形式只用于展示/输入。UI 短别名（各段前 4 位）不是地址，不得用于 API/去重/授权。

JSON 地址检查（`checkAddressJson`，按字段角色）：`version` 为大于 1 的整数 → `unsupported_address_version`，其他非 1 值 → `invalid_address`；realm/node/local 不是规范 uint64 串、type 未知或与 node/local 矛盾（如把系统地址改 type 成组）→ `invalid_address`；外域 → `realm_not_supported`；group 出现在 from/reply_to → `network.sender_mismatch`；group 出现在 recipient、回执目标、单播 node 地址 → `network.target_mismatch`。

## 3. 应用信封

### 3.1 字段来源

| 字段 | 生成者 | 说明 |
|---|---|---|
| `message_id`、`thread_id`、`thread_sequence`、`created_at`、`expires_at` | 总部 | 重投不变；`expires_at` ≤ `created_at+ttl`，平台按类型夹紧，不能按接收时间重置 |
| `from`、`reply_to`、`sender_kind`、`authorization_ref` | 总部按已核验来源授权（source_binding / human_send_intent）生成 | v1 `reply_to` 必须等于 `from`；human 固定本 node/sys，agent 固定会话/子会话 |
| `message_class` | 总部按 `to` 生成 | 普通组 multicast、`node=0,local=1` broadcast、其余 unicast |
| `to`、`kind`、`contact_scope`、`reply_mode`、`in_reply_to`、`intent`、`durability`、`dispatch_mode`、`ttl`、`body`、`flags`、`ext`、`response_deadline` | 插件私有请求（`send.schema.json#SubmitRequest`），总部核权 | 模型工具只给 target/body/thread |
| `origin`、`correlation_id`、`causation_id` | 总部从权威上下文固化 | 客户端禁止提交，提交即拒绝；组消息回复的 origin 从原 recipient 推导 |
| `source_context` | 总部从认证连接与权威存储重建 | 客户端同名字段删除；`message_digest = SHA-256(JCS(message))` |

### 3.2 kind / intent / class 规则（schema 与 `checkMessage` 双重执行）

- `kind=control` ⇔ `intent=control` ⇔ `body.subtype=control`；只能 unicast、durable，动作枚举 wake/cancel/queue/steer/start，未知动作失败关闭。控制意图落在组播/广播 → `network.dangerous_multicast_forbidden`；三者不一致 → `protocol_error`。
- `first_contact`：unicast、`intent=request`、durable、`in_reply_to=null`、必带 `contact_scope`。conversation/human_dialogue 目标必须 system、body 为 text；human_dialogue 只允许 human 来源；group_join 的 body 必须是 `group_invitation`（目标为 sys 或已有直接关系的 local，关系由总部/插件核）。其他 kind 不得带 `contact_scope` 或组邀请 body。违反目标规则 → `network.target_mismatch`。
- `reply`：必带 `in_reply_to` 与 `reply_mode`；private → unicast，group → multicast（不能回复广播全体）。违反 → `target_mismatch`。
- 组播/广播只 `information`/`request`；broadcast 必须带非空 `authorization_ref.broadcast_grant_id`（缺 → `network.broadcast_forbidden`），其他类别该字段为 null。
- `durability=ephemeral` 只允许 `kind=message`、`intent=information`、body 为 progress/status、非 broadcast；最终结果/决定/控制必须 durable。
- `response_deadline` 只用于 `intent=request`。
- 消息类别与目的矛盾 → `network.class_mismatch`；from 不是认证 node、`reply_to≠from`、sender_kind 与 from 类型矛盾、源为组 → `network.sender_mismatch`；缺 `to` → `network.target_required`。
- body：`subtype` 必填（text/progress/status/artifact_ref/conversation_opened/execution_waiting/group_invitation/control）。整个 body 的 JCS UTF-8 字节 ≤16KiB（`ext` 另计入 64KiB 帧上限）；conversation_opened/execution_waiting 必须 `provenance=host`。
- `flags`：v1 生产方恒为 `[]`；未知非关键 flag 消费方保留忽略。`ext`：命名空间键（含点号，如 `com.example.trace`）最多 32 个；`ext.e2e_signature` v1 恒为 null，非空也只按未知扩展保留、无信任效力，不替代 `server_auth`。

### 3.3 node 邮箱项与页条目

`Delivery` = 一条 node 邮箱项：`epoch`、`delivery_id`、`mail_seq`、`target_node`、1–256 个 `recipients`（每个稳定 `recipient_id`）、`source_context`、`message`、`message_state_version`、`server_auth(purpose=business_delivery)`。单播恰 1 个 recipient；组播/广播必带 `snapshot_id` 与每成员 `membership_id`；广播 recipient 只能 system；首联正文下发必带 `contact`（状态、请求版本、`assigned_address` 或 null）。同 node 多个 local 共用一个 delivery/mail_seq，不拆多项。

其他页条目：`terminal`（不含正文的受权终态，供游标越过）、`gap`（held/paused/quarantined/awaiting_authorization/out_of_window 空洞说明）、`contact_prompt`（contact_pending 期间仅审批 UI 的 ≤280 字预览，不是正文交付，不能形成 host_accepted）、`state_update`（撤回/冻结/解冻固定动作，`purpose=management_state`，无正文、无可执行动作）。

## 4. 总部服务器签名

签名输入：`UTF-8("Auriga.ServerEnvelope.v1") || 0x00 || UTF-8(JCS(对象，仅删除该对象最外层 server_auth.signature))`；Ed25519，签名为 64 字节的规范无填充 base64url（86 字符，解码再编码必须一致）。JCS 按 RFC 8785：键按 UTF-16 码元排序、无空白、字符串不做 Unicode 归一化。

- **覆盖**：每个下行对象自身的全部字段（含 server_auth 其余字段、正文、未知可选扩展、目标/recipients/成员实例、序号、状态与错误）。`message_digest` 也在签名内。
- **用途与密钥**：`purpose` 按对象 type 固定——`delivery`/`contact_prompt` = business_delivery，`state_update` = management_state，其余下行帧与 `terminal`/`gap`/`fetch_page` = protocol_response；三种用途使用不同 kid，用错用途的密钥即拒绝。
- **FETCH_RESULT 布局**：外层 `{app_version,type:"fetch_result",page,items}` 是不签名的传输容器；`page`（type=fetch_page）受签，含 head/cursor/scan_after/snapshot_head/next_continuation 与 `manifest`；`manifest[i] = {item_type, mail_seq|null, digest: SHA-256(JCS(items[i] 完整对象含其签名))}`，条目数与顺序必须与 `items` 一致。每个条目各自受签、可独立验证；删项、换项、改页头都使验签失败。条目的 `server_auth.request_id` 必须等于页的 request_id。
- **接收方检查顺序**（`checkDownstream`）：app_version major=1（否则 `unsupported_app_version`）→ 有 server_auth（否则 `signature_required`）→ version/alg=Ed25519 → kid 在已信任密钥集内（否则 `key_unavailable`）→ 验签 → 用途 → issuer_realm 与受众 realm/node/incarnation/epoch → channel_nonce → request_id（响应必须等于当前待决请求；HEAD 必须为 null）→ 封装寿命 ≤300 秒且 `issued_at−60s ≤ now ≤ not_after`（容差不延长 not_after）→ 投递项 `not_after ≤ message.expires_at`、target_node 是本 node → 消息语义（§3.2）→ `source_context` 的 realm/node/sender_kind 与 message 一致且 digest 匹配（否则 `sender_mismatch`）→ 完整 schema（消费方放宽）。上述签名/受众/时效失败统一 `network.signature_invalid`，与 `signature_required`/`key_unavailable` 一样仅本地诊断：不写回执、不推进 cursor、不给声称的 recipient 造终态、不计入被冒充源的处罚。
- **epoch=0**：只允许出现在 READY 被拒时（尚无 connection_epoch）的 ERROR 受众里。
- **已知答案**：`fixtures/signature-vectors.json` 给出三把测试密钥（种子 = SHA-256(公开标签)，**仅限测试**）、规范串与签名；各语言实现须逐字节复现。

## 5. 网络消息

上行（DSH→平台，经本连接 up subject，不签名；tenant/realm/node 只从 ingress 记录取，READY 等帧带这些字段即生产方 schema 错误）：

| 帧 | 要点 |
|---|---|
| `ready` | channel_nonce、boot_id、features、resume_cursor、local_high_water、registration_revision、receipt_checkpoint、keyset_version、队首未决 `pending_registration`、≤128 项对账证据 |
| `fetch` | resume_cursor、continuation（服务端防篡改、只读）、max_items 1–128、max_bytes 4KiB–64KiB、held_ids ≤256、paused（thread+完整目标）≤128、广播 state_barrier ≤128。不锁定、不改状态、不建租约 |
| `receipt` | 一个邮箱项的 1–256 个逐 recipient 结果；status = host_accepted / rejected / quarantined / retryable / outcome_unknown。host_accepted 必带 accepted_by、acceptance_mode、evidence_digest；inbox_only 必带 `execution_status=not_applicable`；accepted_by=system 只能用于 sys 目标；rejected 的 code 恒为 `network.target_unavailable`；重复 recipient 或目标不属本 node 整次拒绝 |
| `attempt_begin` | 外层验签后、语义解析/入箱前登记；purpose=process 或仅审批预览的 preview |
| `execution_admit` | admit(recipient_id, target, connection_epoch, host_request_id, attempt_id, boot_id)；target 为 sys 时（仅首联）必带 CONTACT_ASSIGN 固化的 `assigned_address` |
| `contact_decide` | decision_scope = conversation / human_dialogue / group_join / execution；conversation 带 conversation_allowed 与 execution_allowed（null=未决），human_dialogue 的 execution_allowed 恒 null，group_join 只带 join（join_allowed、选定既有地址、membership 实例），execution 只带 authorization_request_id 与布尔 execution_allowed |
| `contact_assign` | contact_id、已登记的会话 `assigned_address`、registration_id、creation_attempt_id、decision_version |
| `local_register` | candidate_local ≥1、address_type（session / subagent）、allocation_op_id、allocation_fingerprint、expected_registration_revision |
| `status_query` | message_id 或 submission_id 二选一 |

下行（全部 `server_auth`；FETCH_RESULT 见 §4）：

| 帧 | 要点 |
|---|---|
| `ready_result` | status = ready / reconciling / recovery_conflict / identity_rollback（后两者只开放诊断）；epoch、node_identity、head_seq、committed_cursor、retention_low_seq、local_high_water、registration_revision、seen_checkpoint、required_features（含插件不认识的值即 `unsupported_app_version`，v1 必需 server_auth_v1/source_attestation_v1/inbox_only_v1，广播另需 broadcast_grants_v1）、可选根签名密钥清单公告 |
| `head` | epoch、head_seq；验签失败最多触发重新同步 |
| `receipt_committed` | 逐 recipient transport_status 与版本、`mail_item_closed`、committed_cursor |
| `attempt_result` | entered（attempt_count ≤5）或 quarantined；max_attempts 恒 5 |
| `admit_result` | admitted / already_admitted（只返回既有状态，不是第二次许可）、admission_id、admitted_epoch |
| `decide_result` | contact_state / join_state / execution_permission 三层分开 |
| `assign_result` | assigned / existing；`creation_permit` 仅首次为 true，existing 恒 false |
| `register_result` | registered / existing、address_type（原登记类别）、registration_revision、local_high_water |
| `status` | full：dispatch_status、total_members/nodes、七个互斥收件桶（accepted+rejected+expired+canceled+skipped+unresolved+pending = total_members）、quarantined ⊆ pending、请求回应五桶（同分母）；restricted：只有 `request_submitted`、submission_id、wait_until，禁止出现 message_id/计数 |
| `error` | code ∈ `errors.json` 且不是 local_only；scope、retryable、retry_after、related |

状态集合（与设计一致）：传输 `pending/quarantined/host_accepted/expired/canceled/revoked/skipped/unresolved`；执行 `not_applicable/not_started/admitted/running/completed/failed/canceled/outcome_unknown`；首联 `contact_pending/contact_allowed/contact_denied/contact_expired`；入组 `pending/allowed/joined/denied/expired/revoked`；第二层 `pending/allowed/denied`。

三类消息硬上限（`limits.json#dispatch`）：小批原子 ≤32 node 且 ≤256 local；单 node 项 ≤256 local、编码 ≤64KiB；普通组 ≤1,024 成员/256 node；广播 ≤10,000 node；超原子上限必须 `dispatch_mode=paged`（否则 `network.requires_paged_dispatch`），分页每页 ≤32 node。

非零 local 的类型由所属已认证 node 首次登记时固定，独立字段 `address_type` 仅允许 session/subagent，与帧 `type` 分开。类别同 tenant/realm/node/local、registration_id、allocation_op_id 和原指纹一起成为不可变见证，不增加权限，不上传宿主 ID、父子树或内容。同原操作同类别重试先查原登记，再核新分配 revision；换类别冲突，墓碑不可复用。

READY 的非空 pending_registration 必须带原类别；仅完整匹配的原操作可通过原 REGISTER_RESULT 签名响应复原，READY 不做第二次分配或签发创建许可。address_type 纳入既有服务器签名，接收方确认登记前须比对原持久操作的类别，缺失/未知/不符均不能借响应确认为成功。混合版本缺字段或未知值沿严格 schema/既有操作错误失败关闭；不增加全局登录、版本协商或 READY 必需能力。旧数据库 NULL 不从新声明、活会话或 CONTACT_ASSIGN 补写：类型依赖操作拒绝，保留证据/高水位，按已有诊断和新 local 流程处理；丢失旧类型事实不能凭空恢复。

## 6. 错误码与呈现

`errors.json` 是唯一定义。`visibility`：

- `public`：可返回调用方；`maskIfTargetRelated` 的码（quota_exceeded）在目标相关且调用方无状态查看权时改呈现为统一 `request_submitted`。
- `status_viewer`：contact_pending/denied/expired，仅有请求状态查看权者可见，否则 `request_submitted`。
- `diagnostic`：host_incompatible → 无诊断权者见 `target_unavailable`；group_publish_forbidden → `group_not_available`。
- `own_node`：stale_epoch、resume_unavailable、local_registration_required、local_allocation_conflict、recovery_conflict、identity_rollback，只给本 node 有权操作者。
- `local_only`：signature_required/signature_invalid/key_unavailable，仅插件本地诊断，平台不得作为 ERROR 下发。

不存在/归档/关闭/删除/无关系/接收模式不允许/被屏蔽对外一律 `network.target_unavailable`（同码、同字段、同不可自动重试语义），细因只进本地审计。参考 `presentError`。

## 7. 发送入口

- `AgentSendToolInput`：只有 `target`（文本地址）、`body`（text/progress/status/artifact_ref，不得带 provenance）、可选 `thread`。`AgentReplyToolInput` 只有 body，in_reply_to 与原 recipient 由可信当前消息上下文绑定。`AgentFirstContactToolInput`：sys 目标 + text，另需独立 first_contact 能力。from/sender_kind/message_class/reply_to/server_auth/source_context/授权引用等额外字段 schema 直接拒绝。
- `SubmitRequest`（插件经 HTTPS 私有接口）：`source` 二选一 source_binding / human_intent_id，`idempotency` = producer_epoch + key；同样不接受任何总部盖章字段。
- `SendResult`：platform_stored / contact_pending 带实际完整 from/to、message_id 等；`request_submitted` 只带 submission_id 与 wait_until，不返回真实 message/delivery/mail_seq。

### 7.1 agent 来源内部权威（契约已定义，实现待接线）

独立 [`NetworkSourceAuthorityService`](../proto/auriga/iam/v1/network_source.proto) 定义
`RegisterAgentSource`、`GetAgentSource`、`RevokeAgentSource`，各用独立 Request/Response；
只供 `svc:collab` + 非平台业务 tenant + 既有可信内部传输，不给用户/模型或旧网关增加入口。
IAM 从现行兑换/设备权威推导 owner/device/backing-session，保存最小 opaque 来源事实；collab 在真实 native
resolver、健康/epoch、typed-local lookup/lock 后见证登记，并保留 local type 与线程/recipient/关系最终权威。
只覆盖普通 agent/session 的已有关系 durable 单播 send；human、subagent 发送、组播/广播、控制、首联等继续拒绝。
源许可不等于任何消息、收件或执行许可，也不新增人工批准/挑战/登录。

不可变元组、永久不重绑、同 op 同体原回执/异体冲突、revision CAS、期限最小值、撤销锁序、
collab 无锁 RPC/outbox/最终 CAS、既有第五 RPC 字段映射及后续真实 PG 负向要求，统一见
[`iam-directory.md` 的来源权威规范](iam-directory.md#agent-source-authorityagent-来源权威)。
登记回执不是当前授权状态；每次使用须 Get，签前第五 RPC 仍须查真实来源行/版本、完整来源政策和目标当前读取身份，
collab 再短事务重核 typed-local/业务关系。未实现服务保持拒绝，本节不表示来源链已启用。

自然到期不复活旧来源：同健康连接/同登记经全量当前核验，可用 Register 的 `predecessor_binding` 与
`expected_revision` CAS 取得新 binding/独立有界期限，同代并发仅一个后继；持久停发或显式撤销不能靠新 op/连接绕过。
已提交 Register 的原回包丢失时，svc:collab 可持原 op/原体经 IAM 留存归属锚点核验读取原响应，即使原 transport
已失效；这不授当前权，取得原 binding 后仍须当前连接 Get 与本地 CAS。未知/未提交操作不享该回执读取例外。

## 8. Fixtures 与校验

```
cd services/contracts && npm run check-schemas    # 编译全部 *.schema.json（含 network）
node --test contracts/test/network.test.mjs       # 需要 services/contracts 的 ajv 依赖
node contracts/test/network-gen.mjs               # 修改合法帧后重新生成并签名
```

- `fixtures/frames.json`：39 个合法帧（`def` 指向 schema 定义，`receiver` 为接收方上下文）；下行帧均已用测试密钥签名。
- `fixtures/frame-cases.json`：在合法帧上按 ops 改写后的期望码；`mode` = ingress（总部入口，认证源 node=63）/ downstream（插件接收）/ receipt（总部收回执）/ schema（生产方自测）。
- `fixtures/address-cases.json`、`raw-cases.json`、`error-presentation.json`：地址解析与规范化、原始字节、错误呈现。

Go/Java 实现应加载同一批 fixtures、复现 `signature-vectors.json` 字节，并对每个用例给出相同错误码；参考实现之间不一致时以本 README 与 schema 为准。

## 9. 公钥发布与 Java/Node 联合样本（N1 返工1）

`keyset.schema.json#/$defs/KeysetPayload` 是 Java `NetworkPublicKeys.publicKeysJson`
返回的公钥元数据格式。字段沿网络协议使用 snake_case；状态明确区分 active、next、retired、revoked。
next 只预发布，retired 只能在有效窗口验旧封装，revoked 立即拒绝；清单有效不延长单钥或消息期限。
可信配置端点必须按 `SignedKeyset` 用安装预置的离线根封装，不能把未签公钥 JSON 当信任根。
根签名的输入域为 `Auriga.NetworkKeyset.v1`、一个零字节和 `JCS(keyset)`；业务签名密钥不兼任根。

生产 Java 注册表使用 `NetworkSigningKeys(clock, initialVersion, metadataFile)`；Spring 配置项为
`auriga.collab.network.key-state-file`。不配置时不发布默认的 version=1 公钥集合，邮箱签发仍失败关闭。
元数据文件只含公钥、状态、版本和发布时间，使用原子替换及独占 publisher 锁；私钥另从受保护配置/DER 文件加载。
重启读取已持久化版本/吊销状态，配置重绑同 kid 不能换公钥或复活 retired/revoked；发布新钥、激活、撤回都提升版本。
同版本下载不刷新硬截止；过期清单重新发布时先提升并持久化版本。多实例 HA 需外部受控密钥设施，不共享多个无协调写者。

`NetworkMailboxService.fetchPage` 输出协议 `FetchResult`，其中 `page`（type=fetch_page）及每个条目分别签名；
manifest 绑定条目完整对象的摘要（含签名）。整帧按调用者 max_bytes（4KiB–64KiB）限长和校验深度/值数量。
过期项写 Expired 终态，无权或不可编码项只下发无正文 gap，pending 首联只下发 contact_prompt。
当前内部分页入口接受上一受签页的 scan_after；next_continuation 为 null，HTTP/NATS adapter 不能将 scan_after 当授权或确认前缀。

共享边界 `fixtures/raw-boundaries.json` 覆盖 UTF-8 解码后串/键字节数、根深度从 1 起算的空容器/标量边界和总值数 8192。
valueTree 用 128 个对象各 63 个空对象成员构造；lastMembers 控制最后对象成员数，属性名和结束标记不计入值数。
`fixtures/java-signed-samples.json` 是 Java 真实 signer 生成的 TEST ONLY 样本（公钥、4 个既有向量及完整 FETCH 页），不含私钥。
Java 测试默认只比较已提交样本，再启动 Node 运行 `fixtures/verify-java.mjs`，核真实签名、manifest、篡改负例及共享边界。
插件任务可复用样本接自己的接收链路；本联合检查使用契约参考实现，不代表真实插件/网络端到端已验收。

在仓库根目录显式重生（其他测试不写跟踪文件）：

```powershell
.\mvnw.cmd -B -pl services/collab/api,services/collab/impl -am test '-Dtest=NetworkJavaInteropTest' '-Dsurefire.failIfNoSpecifiedTests=false' '-Dauriga.network.export-fixture=true'
node contracts/network/fixtures/verify-java.mjs
```

契约参考检查 `checkKeyset` 强制 valid_until ≤ issued_at+24h、每个 not_before < not_after、清单内 kid 唯一；标准 JSON Schema 不表达这些跨字段约束。`checkSignedKeyset` 使用独立预置根验签后执行同样检查。跨版本单调性、kid 不复用和状态不可复活仍需消费端持久历史检查。

`fixtures/reply-request.json` 是携带 in_reply_to 的合法组回复提交正例（回复原组、沿用 thread）；原消息可见性、精确 recipient 和服务端 origin 固化仍由 collab 核验，此 fixture 不替代 PG 端到端验证。

Contracts 的 check-schemas 同时执行 verify-java.mjs（含 12 个 raw-boundaries）与 network-keyset.test.mjs。根签名向量 `fixtures/keyset-signature-vector.json` 只含测试公钥、规范输入和签名；普通测试现生成临时密钥验证签名及负例，不写 fixtures。显式重生命令：

```powershell
$env:UPDATE_NETWORK_KEYSET_VECTORS = '1'
node --test contracts/test/network-keyset.test.mjs
Remove-Item Env:UPDATE_NETWORK_KEYSET_VECTORS
```

此处仅提供根签名契约参考检查及测试向量，生产配置端点的根签名接线仍由后续任务完成。
