# Auriga 通信网络契约 v1（N0）

本目录把已合入的待实施设计 [`docs/design/auriga-network/`](../../docs/design/auriga-network/README.zh.md) 落成可校验契约，供 Go（adapter/edge）、Java（collab/iam）、Node（dsh-plugin）三端共用。**N0 只冻结契约与一致性 fixtures，未实现任何服务**；运营默认值（速率、配额、留存）仍以设计总览 §10 为准且待压测，这里只冻结影响编码与互通的硬上限。

| 文件 | 内容 |
|---|---|
| `address.schema.json` | 结构化地址、按字段角色的子集（源/目的/node/会话/系统/组）、uint64 十进制串 |
| `envelope.schema.json` | 业务消息 `Message`、来源盖章 `SourceContext`、`AuthorizationRef`、`ServerAuth`，以及 FETCH 页条目 `Delivery`/`TerminalItem`/`GapItem`/`ContactPromptItem`/`StateUpdateItem` |
| `protocol.schema.json` | 私有应用帧：READY/READY_RESULT、HEAD、FETCH/FETCH_RESULT、RECEIPT/RECEIPT_COMMITTED、ATTEMPT_BEGIN/ATTEMPT_RESULT、EXECUTION_ADMIT/ADMIT_RESULT、CONTACT_DECIDE/DECIDE_RESULT、CONTACT_ASSIGN/ASSIGN_RESULT、LOCAL_REGISTER/REGISTER_RESULT、STATUS_QUERY/STATUS、ERROR |
| `source-control.schema.json` | 由 protocol 的 Upstream/Downstream 引入的 SOURCE_REGISTER/GET/REVOKE 及各自 RESULT；native 见证搬运，见 §5.2 |
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

<a id="https-register-initialization"></a>

### HTTPS 注册初始化事实

`POST /api/iam/v1/network/register` 成功输出由 `register-result.schema.json` 定义：原 `tenant/realm/node/device/systemAddress` 身份语义不变，仅增加必需布尔 `created`。它是 HTTPS 当前调用的事务事实，不是网络署名帧或新授权令牌；不改变上述 raw 帧、1.x 消费兼容规则，也不进入 `/tickets.identity`、CONNECT token 或请求。旧设备授权、空请求、端点、错误、限额和凭据租户来源均不变。

- `true` 仅在该次调用最终提交事务实际 INSERT 当前认证设备的新 node 时返回；已有 node、幂等重试、并发落败均为 `false`。事务重试不能沿用已回滚尝试的 `true`，更不能无限重放历史首次结果。
- 客户端只在认证 HTTPS 校对当前 device/node 与完整身份、当前真实请求 generation 内一次消费实时 `true`。完整签名 READY 校验（受信根、tenant/realm/node、请求 nonce/requestId/epoch、时效）确认服务端 incarnation 后，必须原子落完整初始 ledger，才可开放业务；不能先信未验签 incarnation 或自造值。
- 迟到、缓存、跨 generation 的 `true` 不得再次初始化，也不能存为永久配置或可重用 vault 许可；缺目录、空 vault、零高水位或 keyset 均不证明首次。`false` 或旧服务缺 `created` 且无完整 ledger 时一律拒绝初始化；已有完整 ledger 按原身份和 incarnation 严格恢复。
- 响应不确定、事务提交后回包丢失、落盘前崩溃或落盘结果未知，均不承诺无损自动恢复；重试为 `false` 且无完整 ledger 时继续失败关闭。ledger 后来丢失同样拒绝，不重置 local、不补造 operation 回执。恢复沿现有真实重新登录取得新 device/new node，不能刷新同 device 冒首次，也不能继承旧 node 的本地证据。
- 前后端须联动升级：本次仅补契约，生产 handler 仍未返回 `created`；新 provider 对旧后端缺字段默认失败关闭，不能据此宣称生产首启已可验。后续 Java 事务返回值和 provider 消费/READY/落盘接线须分别实现并验证。

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

### 5.1 内部 LocalRegister 搬运（契约已定义，实现待接线）

[`NetworkMailboxService.LocalRegister`](../proto/auriga/collab/v1/network_mailbox.proto) 是新增 unary 内部搬运 RPC：
独立 Request 仅 `NetworkMailboxConnection connection=1`、`bytes frame_json=2`，Response 仅 `bytes frame_json=1`；原四 RPC 不变。
它搬运 §5 及正式 `Upstream/Downstream` 已有的 `LocalRegister/RegisterResult`，沿同一 NATS WSS up/down subject。
请求必须保留完整原 LocalRegister 的严格 UTF-8 JSON/schema 与 ≤64KiB 字节上限，uint64 保持十进制字符串；
不得拆成重复 DTO、改为 string、裁剪或重算原 op、fingerprint、帧 type、address_type、boot。

调用仅接受 `svc:ai-gateway` 平台内部 JWT、既有受信内部传输及受信非平台委托 tenant，且 tenant 与 connection 一致。
tenant/realm/node 只来自已认证 connection/NATS ingress，不来自应用帧或 NATS header；此调用不使用 `svc:collab` 的来源服务权限。
生产 collab 必须每次重核当前持久 ingress 绑定、IAM CONNECT/currentTarget、READY 关联/boot，取服务端当前 epoch 与原帧 epoch 比较；
保持 TenantContext/RLS、node health、原 op 先查、typed 不可变、revision/high-water 与耐久诊断。
登记不授 SEND、来源、首联或执行权限，不依赖尚未实现的三来源 RPC，也不开放第五授权调用。

响应只能是原字节完整已签正式 RegisterResult 或既有已签 Error；无法认证或建立签名受众时沿既有 gRPC 状态映射拒绝。
网关不得造未签成功或省字段。RegisterResult 顶层没有 epoch/boot_id/allocation_fingerprint，epoch 位于 `server_auth.audience`；
`protocol_response` 签名精确绑定 realm/node/incarnation/epoch、READY channel_nonce 与原 request_id，按既有消费者顺序验签。
只遵循现 schema，不增加 recovered_registration、上行 header/server_auth、feature、subject、权限或用户流程。

**尚未实施的 enforcement**：proto `bytes` 本身不执行 JSON/schema、大小、鉴权或签名校验。
后续 Java handler 负责上述生产重核、严格解析、LocalRegistry 调用与完整结果/错误签名；
Go ingress/codec/router/MailboxClient 负责有界严格解析、可信 connection 传递及响应原字节转发；
插件生产 provider 负责持久原操作接线、受众/用途/签名验收及原 op、fingerprint、address_type 对应核验。
本次只生成契约，未挂服务实现的方法继续 UNIMPLEMENTED/拒绝；生成与二进制往返测试不证明 PG、鉴权、NATS 或双 DSH 链路已通。

### 5.2 来源 native 见证搬运（契约已定义，实现待接线）

这是专用可信插件控制路径，不是模型工具或用户逐来源审批入口。已有真实普通会话先完成耐久 LOCAL_REGISTER/本地绑定，
插件在受信执行回调内实际调用 `createNativeSourceResolver`，核公开宿主对象、归属、未归档、普通 session、bound/open 映射，
再产生见证。远程 Java 不调用 Node 函数，也不能把两份 JSON 相等当作已执行 resolver。
控制路径不得接受模型传入的 native 对象、local、binding 或见证；确定性宿主回信也须真实受信上下文。
本节没有增加本地会话选择/登记 UI、挑战、逐来源人工批准、登录或 SEND HTTP 路由。

**通道和身份。** 六个新帧类型为 `source_register` / `source_register_result`、`source_get` / `source_get_result`、
`source_revoke` / `source_revoke_result`；沿 LOCAL_REGISTER 的同一精确 NATS up/down subject，按 request_id 关联。
网关只传完整 `frame_json` 与已认证 `NetworkMailboxConnection`；mailbox 的调用者仍仅 `svc:ai-gateway`，
collab→IAM 三 RPC 仍仅 `svc:collab`，两跳的业务 tenant/受信传输/委托白名单完全不变。
对应内部搬运方法为 `NetworkMailboxService.SourceRegister/SourceGet/SourceRevoke`；各自独立的
`NetworkMailboxServiceSource{Register,Get,Revoke}Request` 仅 `NetworkMailboxConnection connection=1` 与 `bytes frame_json=2`，
对应 Response 仅 `bytes frame_json=1`。每个方法只收本动作请求帧，回对应结果或既有 ERROR；不能跨方法路由，
不能拆重算 native/typed-local/op 字段。原五个 mailbox 方法不变；IAM 仅补原 op 未提交确认字段及撤销规则，见 iam-directory。
不增加公网 path、header、subject、CONNECT 权限或 READY 必需 feature；未实现/未知帧拒绝，不降级成 SEND/LOCAL_REGISTER。
每次重核持久 ingress、IAM CONNECT 当前授权、健康节点、严格 READY、server epoch、boot 与 connection 全七字段；
tenant/realm/node 来自认证 ingress，禁止从 JSON/header 接收自报身份。新帧顶层不带 connection、tenant/realm/node。

**公共字段与见证。** 完整生产者结构见 `source-control.schema.json`；所有列出的字段均必填，除明确的 null/空串外不得省略。
ID、摘要、时间、uint64 字符串沿 §1，所有正版本用 P64，绝不经 JS number。字段级含义如下：

- 每个请求 `app_version`=1.x、`type`=对应帧、`request_id`=本次传输关联 OpaqueId、`epoch`=当前服务端正世代、
  `boot_id`=当前 READY 进程 ID。重连可换 request_id/epoch/boot，不得据此换业务操作或改保存的 IAM connection。
- `registration` 含 `registration_id`（原登记 ID）、`allocation_op_id`（原分配 op）、`allocation_fingerprint`（原摘要）、
  `registration_revision`（原登记正版本）、`local`（正 uint64）、`address_type`（固定 `session`）。
  加入 ingress 权威 realm/node 后精确对应 IAM `NetworkAgentSourceRegistration` 全八字段。插件从已耐久保存并验签的
  原登记/分配证据取六字段；collab 必须 lookup/lock 自己的完整原见证并逐项比较，实际 IAM 请求用库中八字段。
  不能用 API projection、node 高水位、来源 revision 或 epoch 补 registration id/op/fingerprint/revision，也不能猜 type。
- `native_witness.witness_id`=插件本次实际 resolver 成功后生成的单次 OpaqueId，在 **tenant+node** 范围唯一、不得重新分配；
  collab 的见证及防重放摘要保留期不得短于来源最大时间窗，并覆盖关联 op 的完整对账生命周期。
  `binding_id`=插件本地不可变 opaque 映射 ID（不得使用原生 sessionId）；`binding_revision`=该映射的耐久单调正版本，
  关闭、归档、解绑、换 provider/映射或停发须先推进本地 fence/版本，不能复用旧见证。
  `observed_at`=完成真实核验的插件 UTC 整秒时间，`valid_until`=最多 observed_at+30 秒且不越过宿主/本地许可期限。
  witness 本身不含 token、owner、cwd、原生 sessionId/树或正文；它经同帧 registration/epoch/boot 和真实 ingress 绑定。
  collab 持久绑定 witness_id→完整见证、六字段、connection/epoch/boot 与操作，异体复用 conflict；这不是客户端自签证明。
  首次 issue 同时固化 `native_binding={binding_id,binding_revision}`，与 source_binding/整条许可链永久关联。
  每次 Get 和最终 CAS 都必须等于该签发值；不等返回 `network.conflict`，先停用并撤销，不能借新 witness_id 洗掉差异。
  后继 issue 也不得换这两个值；本协议不提供重绑或复活路径。schema 的 NativeBinding 约束结构，
  Get 校验还须将**从本地权威存储读取**的签发二字段绑定为 const（见负例 fixture），不能用客户端自报值生成约束。
- 插件负责真实采集、可信时钟和每个异步边界后的对象/映射/代际复核；collab 在接收及最终 CAS 都检查
  `observed_at <= now+60s`、`observed_at < valid_until <= observed_at+30s`、`now < valid_until`，
  再按接收时刻+30s、本地许可和 IAM 期限夹紧。60 秒只容忍客户端快钟，不延长 valid_until。
  未知时钟/缺 resolver/见证过期或本地版本改变均失败关闭；重试当前使用必须重新真实核验并产生新 witness_id。
  宿主变化先本地停发并持久撤销 outbox，不能等待网络回包后才停用。远程观察有有界竞争窗口，不声称跨机零撤销窗口。

**请求与响应。** 所有下行都带 `app_version/type/request_id/server_auth`；`purpose=protocol_response`，
完整原对象签名绑定当前 realm/node/incarnation/epoch、READY channel_nonce 与本次 request_id。
按 §4 先验签/用途/受众/期限再处理，网关原字节转发。重新封装历史业务回执时，只更新传输签名/关联，不改原业务结果。

- `source_register`：`operation_id`=先持久再发的 Register 稳定 OpaqueId；`scope` 固定 `durable_unicast_send`；
  `predecessor_binding` 首代为空串、`expected_revision`="0"，后继为原 EXPIRED head ID/正 CAS 版本；不能从非 head 分叉。
  `mode=issue` 必带当前 `native_witness`；`mode=reconcile` 必为 null，仅查已保存操作，不产生 native 使用许可。
  无见证的 reconcile 不得盲重放可能新建来源的 Register：原 C 仍可能有效、又无已提交回执时保持 outcome_unknown；
  同 op issue 只有补齐新鲜见证且当前连接/epoch/boot 仍匹配冻结请求才可走潜在新建路径，原 IAM 业务体不变。
  `registration/scope/predecessor_binding/expected_revision` 是该 op 的不可变客户端业务体；持久键为 (tenant, 方法, operation_id)，
  认证 node/原归属固定关联该记录，换 node/归属不能借同 op 读取。
  collab 首次 issue 将真实当前 IAM connection 及库中八字段固化成完整原 IAM 请求，记录摘要，之后不改。
  request_id、当前 epoch/boot、mode 和新鲜 native_witness 是搬运/见证字段，不改变原 IAM 请求摘要。
  同 op 异业务体 conflict；未持久操作的 reconcile 按下述确定失败规则处理，不能按当前连接补造原请求或默默新建。
  已提交 op 的 issue 重试也先读取原操作，不能先按现在的 CAS/过期判断后另建。未提交的新建仍须完整当前见证/权限检查。
- `source_register_result.operation_id` 回原 op；`result=registered` 时 `failure_evidence=null`，
  `source` 为下述原创建事实，revision="1"、state="active"。
  这是可能已经到期/撤销的历史原回执；不能直接激活、授 SEND、报 send_ready 或当作 Get 结果。
  对账恢复 binding 后须当前 native 见证→Get→最终 CAS，签发前仍须第五 RPC。
- `source_get`：`source_binding` 指精确代际，`registration` 是当前完整 typed-local 比对输入，`native_witness` 必须新鲜。
  native 的 binding_id/binding_revision 必须等于 collab 保存的该代签发值（与首代相同），非“任意当前映射”即可。
  读取终态不要求被查询来源仍有 SEND 许可；native 见证证明当前真实宿主/映射，不能用该来源的历史 expires_at 截断见证，导致自然到期后无法 Get。
  collab 用本次真实当前 connection 调 Get，不替换 IAM 的 original_connection；同 device/backing-session 重连按 IAM 规则。
  Get 没有 mutation operation_id；每次是新在线观察，不从旧缓存/原回执产生当前 allow。
- `source_get_result`：`source`=当前事实；`policy_revision`=IAM 当前正版本，`observed_at`=IAM 观察时间；
  active 时 `valid_until`=IAM/native/本地期限最小值，必须为尚有效的时间；active 观察期限已过仍按 `network.expired` 要求刷新。
  **有效查询的终态正常返回 source_get_result**，`state=expired/revoked/stopped`、当前命名代的正 revision，完全省略 `valid_until`（不能是 null）。
  不因来源终态返回 ERROR：来源业务查询只有 binding 不存在、跨 tenant 或身份不符才拒绝；非法帧、见证/连接校验及依赖故障仍沿原协议处理，不伪造事实。
  已知状态投影顺序：IAM 命名代 REVOKED→revoked；否则 IAM 链停权或 collab 已持久停发→stopped；否则 IAM EXPIRED→expired；
  只有明确 IAM ACTIVE 且未停才是 active，未知枚举/缺字段失败关闭。
  IAM Get 的 `lineage_stopped` 必须显式存在（包括 false），缺字段失败关闭，不能把未知停链状态当自然到期。
  stopped 只是不可使用状态，不等于全局撤销确认；revision 仍为 IAM 命名代当前值，不用本地停发版本替换。
  只有当前 head 自然 expired 且无停权/停发，才可直接用所返回的 revision 构造后继 expected_revision，禁止猜固定值；revoked/stopped 不可续代。
  `witness_id` 回显本次见证，插件须匹配原待决请求及当前本地 binding/revision/epoch/boot。
  collab 还须比较 IAM connection 回显与当前精确元组，但不把内部兑换字段发给插件。
- `source` 受限投影固定含 `source_binding`、`registration`（上述六字段）、`scope`、来源正 `revision`、
  `state`、`not_before/expires_at`（IAM 不可变原时间窗，前者小于后者）、`predecessor_binding`、`native_binding`（签发时二字段）。
  Register 成功仍仅历史 active/revision=1；Get 可为 active/expired/revoked/stopped。终态的原 not_before/expires_at 仅是历史窗口，不能当有效许可；不授 SEND 或任何新权。
  不返回内部 owner/original_connection/device/backing-session。该 projection 只用于回执/本地比对，不是权威 native 输入。
- `source_revoke`：仅 `source_binding`=被点名的已登记原代、`operation_id`=独立稳定 Revoke op，加请求公共字段。
  **不要求 native_witness 或精确 revision**；插件不先 Get，不因到期推进 revision 而卡住撤销。
  插件先耐久停发，再发送；collab 核保存的原连接/归属锚点，用 IAM Revoke 撤销整条链。
  “线兼容”仅指 **IAM proto RevokeAgentSourceRequest.expected_revision 的字段号 3** 保留且废弃；JSON source_revoke 不定义这个字段，也不能将它解释成 CAS 前置。
  **落地顺序：先改造 IAM Revoke 接受无 revision/字段 3=0 并原子撤整条许可链，再接 collab 撤销。**
  现有 IAM 若仍校正 revision，collab 不能提前启用或靠假确认绕过；本轮不改 IAM 实现。
  当前 mailbox 请求仍须有权且关联当前 READY；连接不可用时由已耐久的 collab 撤销 outbox 在后台继续，不能要求旧连接重上线。
- `source_revoke_result` 仅 `operation_id/source_binding/revision/state/lineage_stopped` 加下行公共字段；
  state 只可 revoked/expired，`lineage_stopped=true` 必须表示 IAM 已提交整个链停权（含撤销可能存在的 active 后继）。
  expired 前代仍 expired；重放回原终态/revision，不假装刷新当前快照。仅本地停发/outbox 入队不能发此成功结果。

**Register 操作终态（与来源本身的 state 分开）。** 失败结果仍是受签 source_register_result，
必含原 operation_id/result/source/failure_evidence；不使用新错误码。

- `registered`：IAM 原创建回执已提交；只恢复原成功结果，必须再 Get，不能把回执 active 当许可。
- `not_accepted`：只由 collab 在 **pending 持久化之前**判定，包括校验、业务鉴权、大小和限流拒绝；
  前提是可信 tenant/node 已建立，能安全识别合法 op 及原业务体。collab 在同一 op 键锁下确认从未接受并先持久失败墓碑，之后才响应。failure_evidence 固定
  `{authority:"collab",reason:"rejected_before_pending",request_digest:Digest}`，digest 是 JCS 的不可变客户端业务体摘要。
  可执行判定：读主库，在所有 pending/outbox/终态写者共用的 (tenant,Register,op) 键锁下，查无任何操作记录和 outbox，
  且该 tenant+node 的耐久 `source_operation_recovery_gap` 标记为 false，才能同事务 INSERT 失败墓碑并提交。
  这依赖“不删除操作历史、任何 IAM 调用都必须晚于 pending+outbox 提交”的不变量；同键迟到 issue 必须读到墓碑，不能再创建 pending。
  备份恢复、历史丢失或完整性不明必须先置 recovery_gap，不经核实不得清除；不能因表空自行判为 false，也不能用副本读缺行作证据。
  reconcile 未命中也采用上述键锁/缺口检查/墓碑提交流程；发现既有 pending/outbox 就只原 op 对账，有缺口或提交不确定就 outcome_unknown。
- `not_committed`：由 IAM 判定、collab 验证和持久转存。collab 保留原 IAM 请求 C/R/op，重放原 Register 作原键查询；
  IAM 核可信服务/tenant 与原兑换归属锚点，在原 op 锁下确认没有成功来源提交，并原子保存“不允许该 op 再提交”的确定业务回执：
  原 C 失效时原因为 original_connection_invalid_uncommitted；C 仍有效但 CAS 不符、前代非 head、许可链已停时，
  分别为 revision_mismatch/predecessor_not_head/lineage_stopped。拒绝回执本身必须已提交，不能从异常文本或旧错误码推断。
  与并发/迟到 Register 用同一锁串行化；已提交必须回原成功，未能判定在途事务必须返回不可判定，不能从 SELECT 不存在推断未提交。
  IAM 通过 Register 响应的 `definitively_uncommitted=true/operation_id/request_digest/rejection_reason` 确认，source 必须缺失；
  collab 逐项核原 op、完整冻结 IAM 原请求的 JCS digest，保存完整 IAM 原响应后投影
  `{authority:"iam",reason:<上述四种 rejection_reason>,request_digest:Digest}`，source=null。
  插件不掌握完整内部连接：**此 request_digest 对插件是不透明证据，只保存、不复核**；原 op/body/digest 的校验由 collab 负责。
  只有已提交的类型化回执可转为 not_committed；普通 forbidden/unauthorized/conflict/超时/空响应/UNIMPLEMENTED 都不能证明未提交。
- 两个失败结果都 `source=null`；同 op 同体永远返回原失败结果，不得同 op 再 issue。插件持久验签后的确定失败，
  才能另存新 op，用当前新鲜见证、全量身份/健康/typed-local 检查重新申请；停发/链撤销仍禁止申请。
  未知结果保持 pending，只能同 op 对账；同 op issue 重放只查原操作，不以 C2 补建缺失原请求。
  失败墓碑、摘要、原归属及完整原响应与成功操作采用同等永久防重绑/防复用留存，不按消息去重期删除。

**Register 拒绝/错误分界。** 下表 code 是既有错误分类；not_accepted 本身是正常结果，不同时发送 ERROR。
先查 op 的耐久状态，再分类；已保存的成功或失败始终重放原响应。pending 后的处理/RPC 失败一律未知，
只有原 op 的 IAM **已提交业务回执**（不是 RPC 失败）才能确定成功或 not_committed。

| 阶段/失败 | 响应与操作规则 |
|---|---|
| pending 前：可安全识别 op 的字段/范围/大小校验（protocol_error） | 原子失败墓碑后 not_accepted；允许新 op |
| pending 前：已认证 tenant/node 内的业务鉴权拒绝（unauthorized） | 同上；不改变原归属限制 |
| pending 前：epoch/boot 旧（stale_epoch）、native/本地许可过期（expired） | 同上；新 op 必须换当前连接/新鲜见证并全量检查 |
| pending 前：缺 typed-local（local_registration_required）、登记/映射不符（conflict）、已停发（revoked） | 同上；新 op 不绕登记、映射不变或停发约束 |
| pending 前：大小/配额/限流/背压（protocol_error/quota_exceeded/overloaded） | 同上；仍受节点/租户预算及退避约束 |
| pending 前：查无记录但存在恢复缺口，或失败墓碑提交不确定 | outcome_unknown；只能原 op 对账，不发 not_accepted |
| pending 后：上述任何校验/鉴权/大小/限流错误，或 IAM 普通错误、断线、超时、存储结果不明 | outcome_unknown，保留原体/outbox；不得自判失败或换 op |
| pending 后：IAM 原 op 已提交成功/四种确定拒绝回执 | registered/not_committed；先转存完整原响应再发结果，确定失败才允许新 op |
| 入口尚不能认证 tenant/node，或原始超限/非法 JSON 无法安全识别 op | 既有传输鉴权拒绝/有界 ERROR，不能替未知 op 写墓碑；插件保留原 op，用合法 reconcile 获得确定结果 |

not_accepted 的 digest 是插件可复核的客户端业务体 JCS 摘要；not_committed 的 digest 则仅由 collab 复核。

**幂等、恢复和 collab 事务义务。** 插件在自己的同一耐久账本保存 op、不可变业务体、原 typed-local/映射版本、
pending/停发版本，成功原回执及绑定也真提交后才可对外使用；未知落盘/回包保持 pending。
每 node 最多 2 个在途请求：Register/Get 共用 1 个常规名额，Revoke 独占 1 个保留名额，不能排在登记后；
撤销队列优先，停发本地立即生效。每次请求从受理起最多 30 秒，超时即释放名额，mutation 保留 pending/原 op，
不把超时当失败终态；晚到结果可耐久对账，但不能激活已超时的请求/旧 generation。撤销超时仍停发并保留 outbox。
持久队列有界并沿现有限流/公平预算，不得以换 local/op 绕过背压；上限取 `limits.json#sourceControl`。

collab 在同 tenant 短事务保存 pending/op、规范原 IAM 请求/摘要、完整 typed-local、native 见证、
connection/epoch/boot、许可/停发版本、前代与 CAS 版本、动作 outbox。禁止持 DB 事务/行锁等 IAM。
事务外 Register/Get，返回后短事务 lock/CAS 重核 pending/停发版本、健康、当前完整 connection/epoch/boot、
typed-local 全八字段、native 有效期及已持久的宿主许可 fence，变动拒绝激活；再校回包完整元组/前代/新 binding 与原 op。
跨服务 Get ACTIVE 只是观察。发件可复用**最近一次 source_get 完成且最终 CAS 已通过**的服务端观察，
同时满足 `observed_at <= now < valid_until` 且 `now-observed_at <=60s`；保留原 observed_at/valid_until，不在命中时续期。
每次发件仍重核同 binding/签发 native_binding、同连接/epoch/boot、typed-local、健康、停发/许可 fence；
缺缓存或任一条件不满足，先由插件重新推送 source_get 见证，完成 IAM Get/最终 CAS 后才能继续，未刷新失败关闭。
不向 SEND JSON 增加见证字段；每次签前仍调用第五 RPC，最终短事务还锁业务关系/线程/recipient 版本。

Register 结果未知只以原 op、保存的原 IAM 请求（包括旧 connection）对账；不得用新连接重写原请求或换 op 绕过。
collab 从自己的持久 pending 找到原体，经 IAM 留存的真实原归属锚点核验取回完整原响应，即使原 transport 已失效；
无 native 的历史对账调用独立 `ReadAgentSourceReceipt`，wrapper 的 `original_request` 为完整冻结的
原 Register 请求（原 C，不是当前 C2），返回原 `RegisterAgentSourceResponse` 成功或已提交拒绝回执。
查询不调用新建 Register，也不写来源/操作/墓碑/代际或改期限、状态、revision；缺记录/锚点、依赖故障
按 `UNAVAILABLE`/`FORBIDDEN` 关闭，异体仍冲突，不能从未命中推断 not_committed。
本节 reconcile 仍须当前有权 mailbox 身份且同 tenant/node，并核持久原操作归属，不能靠知道 op 跨归属读取。
重连的当前 envelope 只运回原回执；完整账本能证明从未接受时按 not_accepted 围栏处理，
账本丢失/锚点缺失则保持 outcome_unknown，不能下载 IAM 事实重建插件/本地登记，也不能伪造失败终态。
原 op 对账完成后，自然 EXPIRED 且链未停权才可另存新 op/前代 CAS，经新鲜全量检查申请独立新代；旧回执/旧期限永不改。

撤销先短事务持久停发与 Revoke 原体/op/outbox，再事务外 IAM，最后短事务确认终态与保存原响应。
迟到 Register/Get 无权重置停发；换连接、到期、换 op 都不得自动申请/激活后继。撤销未知只重放原 Revoke；
撤销无需靠 Get 取得当前 revision；IAM 在本库链/来源锁下原子停链并返回终态，不因调用者旧版本拒绝。
原归属已失效仍沿 IAM 受限停权路径；新操作不得越过未知旧操作，内部原请求字段冻结后仍原封对账。
IAM 未确认只报待确认，继续本地停发；显式停权永久压过代际恢复。

**大小与错误。** 六帧和 mailbox bytes 各自均受原始 UTF-8 ≤64KiB 及 §1 全部解析上限约束；单请求一 local/一 binding，
不加批量/任意 native JSON。schema 只证明结构，时间比较、真实对象、精确连接、签名、op/CAS/库锁必须由实现执行。
全部复用 `errors.json`，不新增错误码：结构/大小/未知 scope/type→`network.protocol_error`；
自报或错归属/权限→`network.unauthorized`（不泄露 binding 是否存在）；错 epoch/boot→`network.stale_epoch`；
缺 typed-local→`network.local_registration_required`；见证过期→`network.expired`；
同 op/见证异体、typed-local 不符、版本 CAS→`network.conflict`；非 Get 的本地停发/链撤销→`network.revoked`；
结果未知（含不能证明“从未受理”的原 pending 缺失）→`network.outcome_unknown`；依赖不可用→`network.overloaded`。
Register 的操作阶段分界优先于常规错误映射，不能把 pending 后普通 IAM 错误当确定拒绝。其他操作的
IAM common.validation_failed/conflict/forbidden/unavailable 分别映射 protocol_error/conflict/unauthorized/overloaded；
认证失败沿既有内部 gRPC 状态映射。ERROR 仍为原受签结构、request scope、原 request_id 和 catalog retryable；
无法建立签名受众时用既有 gRPC 拒绝。outcome_unknown 不自动发新 op；overloaded 退避也只重放原 mutation。

**生成与验收。** Go 沿 `cd infra; go run generate.go`；Java 沿根目录
`./mvnw.cmd -B -pl services/contracts -am generate-sources`（生成至忽略的 target，不提交 Java 生成目录）。
TS 使用已锁定的 json-schema-to-typescript：根目录 `node contracts/network/generate-source-control.mjs`，
`node contracts/network/generate-source-control.mjs --check` 检查漂移。生成器沿插件既有方式仅展开所需跨文件引用并用项目 Prettier 格式化；
生成的 source-control-types.generated.json 固定 schema/生成器/锁文件输入 hash 与 TS 输出 hash；--check 只用 Node 内置库验证二者，
让只安装契约 Ajv 工具的门禁同样能失败关闭，不为此新增依赖或改 CI。
TS 声明只表达结构，freshness/条件分支仍由 schema 及语义校验执行。连续重生比较字节/摘要检查漂移，Go 生成物和 TS 声明提交。
schema 正负矩阵 `node --test contracts/network/fixtures/source-control.test.mjs` 与 TS --check 均已接入 check-schemas；它们不证明服务、PG、NATS 或宿主链已实现。
后续插件须覆盖真实 resolver/耐久账本/代际 fence/签名与重连原 op；collab 须覆盖真实 PG outbox、原体对账、
无锁远程调用与最终 CAS、停发压过迟到回包、过期前代撤销 active 后继及跨 tenant/归属负向。

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
`RegisterAgentSource`、`ReadAgentSourceReceipt`、`GetAgentSource`、`RevokeAgentSource`；
读取方法使用独立 wrapper Request 并复用 Register Response，其余各用独立 Request/Response；
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
该只读方法不要求 native，历史 ACTIVE 也不是当前许可；不能替代有写入的原连接失效未提交围栏，
不能据此声称所有 pending 均可收敛。用户/模型/网关无此入口。

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
