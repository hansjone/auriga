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

节点范围的允许绑定当前 userId/ownershipRevision，归属变化后重新核实；节点范围的屏蔽只绑定 tenant/realm/node，归属变化不会解除屏蔽。用户范围始终绑定经核实的 tenant/userId。

返工新增 `limits`：全局 maxRecords=10000，单 node/user 的 maxSourceRecords/maxUserRecords=32/64，终态 maxSourceTerminal/maxUserTerminal=16/32；不写入超出该来源预算的新行，不影响其他用户剩余额度。新请求还按 node/user 聚合 maxRequestsPerMinute=5，重投已有稳定 ID 不计新请求；总部整体入口预算仍须保留。所有限额为可信宿主配置，不由来源提供。

回调默认 callbackTimeoutMs=5000、actionTimeoutMs=10000；源回执固定 responseDelayMs=10500 的窗口，在阻塞/待问/允许等状态下均相同。窗口到点时未持久完成或失败统一报 intake unavailable，不伪报已保存；排队过期不能启动动作。主控可按端到端超时预算调整窗口，不能根据目标状态动态调整。运行时调度抖动不构成精确时间保证。

## UI 接口

- `listPending(localContext)`：返回未过期的元数据，重新检查当前身份与组织策略；不含正文或预览。
- `getRequest(localContext, {layer, requestId})`：新增的鉴权私有查询，返回内部行或 null；只供本机 UI/可信接线读取决定 ID、策略版本、本地原因与 usedAt 等，绝不返回给远程来源。
- `getSettings(localContext)`：defaults、rules、policyRevision；UI 两层分别显示 ask_each/same_org/allowlist/deny_all，初始均 ask_each。
- `decide(localContext, { layer, requestId, choice, sourceScope?, expectedRevision })`：choice 为 allow_once/allow_source/deny/block；持续规则 allow_source/block 必须显式选 source_node/source_user。expectedRevision 取最新待决项/设置，冲突后刷新，不自动重放旧批准。
- 冷却期内尝试允许会明确抛 `cooldown active`，不保存允许规则；UI 提示用户等待剩余冷却，不静默将允许变成拒绝。已用请求不能再次批准。
- `revoke(localContext, { layer, source, sourceScope })`：移除持续允许或屏蔽。保留拒绝冷却；其他层不变。解除屏蔽后的同源请求可能仍在 24 小时冷却内。
- `revoke(localContext, { layer, ruleId })`：从 getSettings 返回的本地规则 ID 精确撤销，无需 IAM 在线；源节点转让或原用户已无在线节点时也能撤销旧用户规则。
- `setDefault(localContext, layer, mode)`：只改当前层；版本先耐久递增，撤销该层历史允许，后续使用时按新政策重新核实。不复活已拒绝/过期请求。
- `listAudit(localContext)`：最近 maxAudit=256 条决定/修改历史；含来源与本地原因，仅本机用户可读。`getAuditSummary(localContext)` 返回淘汰数量和 SHA-256 连续摘要。需要完整长期审计时，主控应定期导出活动窗口与摘要；摘要不能还原已淘汰详情。

请求字段严格限 `layer, requestId, source, digest, createdAt, expiresAt, bytes`，有正文/额外字段就拒绝。source 只含 tenant/realm/node。requestId 为该层稳定 contact/执行 ID；同 ID 异体冲突，重投不新增提示或许可。首次入站记录本机 receivedAt，期限为 min(可信总部 expiresAt, receivedAt + 24 小时)，不会仅因对端 createdAt 晚于本机时钟而拒绝；createdAt 只参与元数据一致性校验。过期或超过本机 24 小时窗口的 expiresAt 不新建行；总部接线必须提供经过准入、最多 24 小时的权威期限，不采用对端自报 TTL。真实审批预览只给用户，不调用模型/会话入箱 API。

## 需主控接线的宿主与状态通道

1. `request(metadata)` **返回值已改变**：所有状态只返回 `{status:'request_submitted'}`，不返回内部行；INBOXUI 原来直接读返回行的调用须改为 getRequest/listPending。可信接线从私有查询取得第一层 allowed 后再接 CONTACT_DECIDE/CONTACT_ASSIGN/系统耐久入箱，本地 allowed 不是平台提交、HostAccepted 或创建许可。
2. `withAuthorization(metadata, action)` **不再返回 action 结果**，成功只给相同的匿名回执，拒绝统一 authorization required。action 现在接收 AbortSignal，每次调用前重新核 IAM/组织、期限和当前策略；先原子写 usedAt 消耗该稳定请求许可（包括 allow_once），再在串行锁内启动，重启也不能重复执行。另核对话/会话/工具 grant、lineage、在线总部准入和世代；action 只等待准入/宿主持久入队，不等整个模型回合，不重入服务，并在每次外部副作用前检查 signal。超时会 abort 并释放队列，未合作的外部回调不能物理强停；超时/不确定结果不重发原许可，需走宿主对账。第一层许可不隐含第二层许可。
3. `listOutbox(localContext)` 给可信后台同步器读取；同请求未同步旧事件被最新事件替换。decision 只有请求 ID、层、决定 ID、版本、时间和 pending/allowed/denied；policy_changed 不含规则/来源。总部请求版本 expected_request_version、当前 node/epoch 和正式契约映射由接线层补齐。
4. 只在总部确认为该版本提交后 `acknowledgeOutbox(localContext, ids)`；旧 ID 的迟到确认不会删除新事件。收到策略修改事件应暂停受影响动作并同步较新撤销，再处理新投递；不得缓存 allowed 绕过本地门禁，也不得把旧批准改成新版本盲目重试。
5. request/withAuthorization 内置匿名回执与固定窗口，不要求调用方记得 publicReceipt。保留的 publicReceipt 只给经过独立状态查看权检查的上层映射使用；不得通过它扩大普通来源的查询权。该本地视图不替代总部耐久 submission；总部入口仍须在目标查找前提交并保证查询外观一致。

## 耐久与限制

决定、规则、审计、outbox 在一个快照原子提交；任何写入异常使当前服务不可继续使用，关闭并重开后从实际磁盘快照恢复，不能用旧内存覆盖不确定写入。Windows 使用 host store 相同的文件 fsync + NTFS rename 边界，不额外声称目录 fsync。时钟回退失败关闭。

终态包括拒绝、过期、撤销、已消费许可，按 terminalRetentionMs（默认 24 小时）保留且不早于本地 deadline 清理，同时移除相应过期状态 outbox。合法新请求的 deadline 等于权威 expiresAt；旧快照中更长的原期限不延长本地终态保留。每次事务维护过期/回收，无独立服务或周期轮询；总部权威 TTL 到期不依赖本地 outbox。单源限额和有界审计防止换 requestId 永久耗尽目标空间；全局多来源过载仍受总预算限制。快照版本 1 原子迁移到版本 2，保留规则/决定并补接收时间、限速桶和审计摘要。完整安装快照回滚/恶意改盘仍需 host 恢复检查点门禁；未接 UI/IAM/总部/宿主前不代表真实首联闭环交付。
