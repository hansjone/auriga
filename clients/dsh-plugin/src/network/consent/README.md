# 插件本地两层授权（N1）

这是本地服务接口，不是远程协议或模型工具。只支持默认 conversation 首联及逐稳定请求的 execution 授权；不能将其默认规则直接扩展到 group_join 或 human_dialogue。

## 初始化与可信边界

调用 `openConsent(options)`，必填：

- `identity`：当前安装的 tenant/realm/node/nodeIncarnation。每个目标 DSH 独立目录，不同步其他 DSH 的个人规则。
- `store`：`createFileConsentStore({ dir })` 复用 host 原子写与单写者锁，实际使用 `dir/consent/ledger.json`，不得与 host 共用快照文件。丢库从两层 ask_each 开始，不从总部回执恢复白名单；完整备份回滚见下方限制。
- `resolveSource(source)`：可信 IAM/入站验签接线，返回当前 tenant/realm/node、稳定 userId 和 ownershipRevision；后者必须在节点归属变化时改变。不能从正文、显示名、local、模型参数自行构造证明。未知身份/故障抛错并停止处理。
- `organization(source)`：可信当前组织策略，返回 available、revision、sameOrganization，以及 conversation/execution 上限 allow/ask_each/deny_all。allow 仅是不额外收紧；ask_each 禁止自动档位但允许真实用户批准本次；deny_all/不可用禁止批准。组织查询抛错时操作整体失败关闭。
- `authorizeLocal(context)`：校验真实本机已认证用户且有权操作当前 DSH；只有严格返回 true 才能读私人设置、待决、审计或修改规则。不可用远程请求声明的 userId 冒充该上下文。

默认待决上限 100 条/1 MiB，源 node 最多 1 条、经核实源用户最多 3 条（跨两层合计）。`bytes` 是可信接线核算的原请求字节数。拒绝冷却按层同时记源 node/源用户，24 小时；目标间不共享设置。分钟级入口限流仍需总部/入站策略负责。

## UI 接口

- `listPending(localContext)`：返回未过期的元数据，重新检查当前身份与组织策略；不含正文或预览。
- `getSettings(localContext)`：defaults、rules、policyRevision；UI 两层分别显示 ask_each/same_org/allowlist/deny_all，初始均 ask_each。
- `decide(localContext, { layer, requestId, choice, sourceScope?, expectedRevision })`：choice 为 allow_once/allow_source/deny/block；持续规则 allow_source/block 必须显式选 source_node/source_user。expectedRevision 取最新待决项/设置，冲突后刷新，不自动重放旧批准。
- `revoke(localContext, { layer, source, sourceScope })`：移除持续允许或屏蔽。保留拒绝冷却；其他层不变。解除屏蔽后的同源请求可能仍在 24 小时冷却内。
- `revoke(localContext, { layer, ruleId })`：从 getSettings 返回的本地规则 ID 精确撤销，无需 IAM 在线；源节点转让或原用户已无在线节点时也能撤销旧用户规则。
- `setDefault(localContext, layer, mode)`：只改当前层；版本先耐久递增，撤销该层历史允许，后续使用时按新政策重新核实。不复活已拒绝/过期请求。
- `listAudit(localContext)`：完整本地决定/规则修改历史；含来源与本地原因，仅本机用户可读。

请求字段严格限 `layer, requestId, source, digest, createdAt, expiresAt, bytes`，有正文/额外字段就拒绝。source 只含 tenant/realm/node。requestId 为该层稳定 contact/执行 ID；同 ID 异体冲突，重投不新增提示或许可。期限为 min(expiresAt, createdAt + 24 小时)，重试不续期。真实审批预览由另一个仅用户可见通道按 requestId 获取，不调用任何模型/会话入箱 API。

## 需主控接线的宿主与状态通道

1. 入站可信元数据调用 `request(metadata)`，仅第一层允许后才能接 CONTACT_DECIDE/CONTACT_ASSIGN 和系统耐久入箱。这里的 allowed 仅是本地授权，不是平台提交、HostAccepted 或创建许可。
2. 创建和自动执行分别调用 `withAuthorization(metadata, action)`，每次均重新核 IAM/组织、期限和当前本地策略。action 在同一串行锁内启动，另核逐会话/工具 grant、可信 lineage、在线总部准入及连接世代。action 应只等待准入和宿主持久入队，不等待整个模型回合完成，且不得重入本服务。已有副作用不能撤回。第一层 allowed 不允许第二层 action；执行接线还须核其所属对话的当前许可。
3. `listOutbox(localContext)` 给可信后台同步器读取；同请求未同步旧事件被最新事件替换，审计保留全部历史。decision 事件只有请求 ID、层、决定 ID、版本、时间和 pending/allowed/denied；policy_changed 不含规则/来源。总部请求版本 expected_request_version、当前 node/epoch 和正式 CONTACT_DECIDE 序列化由接线层补齐；这些本地类型不是新增的 wire 契约。
4. 只在总部确认为该版本提交后 `acknowledgeOutbox(localContext, ids)`；旧 ID 的迟到确认不会删除新事件。收到策略修改事件应暂停受影响动作并同步较新撤销，再处理新投递；不得缓存 allowed 绕过本地门禁，也不得把旧批准改成新版本盲目重试。
5. `publicReceipt(result, canViewStatus)` 为隐私投影：无状态查看权统一 request_submitted；有权时拒绝/过期/撤销/屏蔽均 target_unavailable，等待/允许仅返回 pending/allowed，由上层按 layer 映射正式状态。不得将内部 row/localReason/rules/audit 原样发给来源。此函数不代表总部已耐久保存 submission；时延均衡与目标查找前提交由总部入口完成。

## 耐久与限制

决定、规则、审计、outbox 在一个快照原子提交；任何写入异常使当前服务不可继续使用，关闭并重开后从实际磁盘快照恢复，不能用旧内存覆盖不确定写入。Windows 使用 host store 相同的文件 fsync + NTFS rename 边界，不额外声称目录 fsync。时钟回退失败关闭。

本地请求/审计保留以防重放，默认最多 10,000 条请求；请求容量耗尽拒绝新增，但仍可撤销和处理已有请求。outbox 同请求/同层策略只保最新待同步事件；完整审计不截断，磁盘写入失败关闭。后续需主控设计带平台见证的归档清理，不可自动删库解限。此模块不检测完整安装快照回滚或恶意修改磁盘，需复用 host 恢复检查点/安装身份冲突门禁。未接真实 UI、IAM、总部和宿主前，不代表真实 DSH 首联闭环已交付。
