# P1-K 统一资源协议

本文件和 unified.schema.json 冻结 P1 公共边界。新增公开 Schema 先查询 unified-registry.json，再集中登记。旧插件 HTTP、事件及 v1 消息继续遵循原语义。新增目录协议首期接收 skill，tool 的分支保留供后续期按下述矩阵开放。

## 版本、下载与回报

UnifiedResourceIdentity 绑定可信 tenant、kind、packageId、versionId、严格 SemVer 及原始 manifestDigest。所有 ID 必须由服务端解析归属并比较。UnifiedResourceRef 同时绑定发行声明、精确制品列表与租约。manifestDownloadPath 和 downloadPath 必须是同源平台 locator，每次请求重新核验可见性和归属，摘要知识不能授予访问。

成员票据绑定认证 tenant/owner、clientId、identity、installationRevision、制品范围及截止时间。下载同时验证当前身份、客户端绑定、安装修订、票据及租约，票据不放进 URL 或事件。直接清单/制品端点按当前版本 ACL 检查，草稿仅作者和获权审核人员可读，已发布仅可见成员可读，跨租户和 plugin ID 返回404。管理员读取原始清单仍需当前审核权限。下载原始清单后先核验原字节摘要，再与已验证 envelope 比较后解析。

写操作带 If-Match 和 Idempotency-Key。幂等键绑定完整请求指纹，指纹变化返回冲突。installationRevision 是成员安装 CAS 代次，selectionRevision 是实例资源选择代次，generation 是实例规格代数。相同 selectionRevision 的重试锁定精确版本与字节，乱序选择不得覆盖新选择。续签只刷新同 identity/摘要/releaseSequence 的签名和租约，保留 selectionRevision，规格变更仍递增 generation。

202 表示接受意图。applied 要求报告 identity、installationRevision、selectionRevision 和 generation 与当前期望完全相等，observedGeneration 等于 generation，且宿主公开 catalog 已确认名称与实际来源。member 来源仅表示成员自报，node 来源只能由已认证调度节点上报，服务端从认证通道决定来源。失败状态包括 failed、pending_restart、superseded、overridden、expired、revoked。失败保留 errorCode 和原安装，禁止把未知来源/状态计为 applied。云端 C 仅观察和回报宿主来源，D 为共享内容唯一写者。

## 信任、迁移与撤销

所有签名按既有独立 release/revocation/downgrade 根验证，保留原 UTC 时间文本，JCS 去除顶层 signature 后验 Ed25519。v1 仅接受 schemaVersion=1/purpose=plugin，v2 仅接受 schemaVersion=2/purpose=resource。v2 artifactDigests 必须与原始清单集合精确一致、去重并按字典序排序。租约截止 expiresAt 必须等于 envelopeNotAfter 和 revocationsExpiresAt 的较早值，两个可信声明各自最多24小时。

UnifiedMigrationProof 包含已签名 legacyEnvelope、unifiedEnvelope、legacyArtifactId 和首个 v2 激活时间 activatedAt。消费者必须取得原 v1 归档和原始 v2 清单字节，独立验证两份签名、摘要、tenant/package/version/releaseSequence 一致，且 plugin payload 的 legacyArtifactId 对应 digest 等于 v1 归档摘要 A。v2 清单摘要为 M。marker 是独立 release 根签名的 UnifiedMigrationMarker，覆盖 tenantId、packageId、首个 versionId/releaseSequence 和 activatedAt。proof.activatedAt 必须等于 marker.activatedAt；marker 首次签发与首个 v2 在同一事务持久化，消费者验签并持久化最早 t0，拒绝回滚或推迟 t0。关联表不可替代上述密码学证明，相同映射禁止更换 M。该格式只供未来插件迁移，P1 不据此启用插件 v2。

高水位以 tenant/package 为键，跨协议共享最高 releaseSequence、versionId 和已验证摘要别名。相同序号仅接受同版本同摘要或完整证明固定的 A/M，跨租户、伪造映射、替换 M 均拒绝。低序号跨协议一律拒绝，独立降级授权须逐项绑定 tenant/instance/package/version/digest 和 from/to 序号，有效期内使用且保留最高水位。迁移证明和高水位在应用回滚、卸载后继续保留。

恶意撤销迁移版本必须同一事务登记 A、M 两条撤销项及 outbox。v1 按 A 匹配，v2 对任一条目先按 packageId/versionId 禁用整个版本，再逐项对 manifestDigest 和全部 artifactDigest 匹配可信租户撤销表。相同恶意制品被其他版本复用也拒绝，服务端仍逐一传播所有引用版本撤销。普通 withdrawn 不写恶意撤销表。

首个 v2 签发时刻 t0 以后禁止 v1 新版本。已应用版本 v1 续签 notAfter 不得超过固定 t0+24h，截止后拒绝续签。回滚不能重置 t0 或重开窗口。未实际开启插件 v2 的包继续原 v1 链路。

## 固定目录和 guardian

静态 customSkillDirs 保留上游顺序、表达式、预设和 includeDefaultRoots，并去重追加由可信绝对 DSH_HOME 推导的 `auriga/skills-active/current/`。该根下一层直接为 `<name>/SKILL.md`。登录身份只在可信运行期记录中保存。真实 YAML 输入、上游预置行和期望输出由 cordis-upgrade-fixture.json 索引。D/E 必须逐项消费同一夹具，解析失败或缺少可信预置时保持旧文件并拒绝共享。

身份根为 `<DSH_HOME>/auriga/resources/<tenant>/<owner>/<client或instance>/skills/`，内部包含 `cache/<sha256-hex>/`、`staging/<transactionId>/`、`ledger.json`、`receipts/<packageId>/<versionId>.json`、`transactions/<transactionId>.json`。五类文件都有必填 schemaVersion=1，拒绝未知版本、未知字段、符号链接和身份不匹配。

- ledger.json 使用 UnifiedLedgerFile，C 唯一写入，E 只读。revision 是文件 CAS，选择代次与规格代数独立保存。
- 安装回执使用 UnifiedReceiptFile 的 installation 分支，C 唯一写入，E 只读。E 的停用回执使用同一 Schema 的 disable 分支，位于 `<DSH_HOME>/auriga/guardian/disable-receipts/<receiptId>.json`，E 唯一写入，C 只读；不得覆盖 C 的安装回执。
- transactions/<transactionId>.json 使用 UnifiedTransactionFile，C 唯一写入，E 只读。C 激活前写 prepared，切换后写 switched，ledger/回执均持久化才写 committed。E 的隔离恢复日志放在自己的 state.json.pendingIsolation。
- `<DSH_HOME>/auriga/guardian/state.json` 使用 UnifiedGuardianStateFile，E 唯一读写，C 可只读观察。
- `<DSH_HOME>/auriga/guardian/high-watermarks.json` 使用 UnifiedHighWatermarksFile，E 唯一读写，C 只读。包含跨协议高水位、已验签迁移标记和已使用降级授权。C 暂存待激活数据，E 独立验签并原子持久化高水位后，C 重新获取锁核验 E 的 active 状态和完全相同的身份/代次/字节才切换 current；C 不直接写高水位，也不在等待 E 时占锁。

所有 C/E 文件和 current 切换共用宿主级排他锁 `<DSH_HOME>/auriga/guardian/host.lock`。通过 OS 文件句柄锁实现，POSIX flock(LOCK_EX)、Windows LockFileEx 排他锁整文件；C 的适配器与 E 必须使用此文件的同一内核锁。锁文件存在不等于占锁，不能按 PID/时间猜测并删锁。进程退出由内核释放。获取失败允许退避重试，超时保持原状态且拒绝激活。若平台或 C 无法提供相同锁语义，阻止共享开放。

首次激活时，C 先持锁将精确 ResourceRef 写入 ledger 的 applying 安装项，并持久化 prepared 事务及 staging 字节，然后释放锁。E 从这些文件独立验证完整待激活集合，持久化高水位后将 guardian 写为对应 selectionRevision/generation 的 active，表示该集合已获可信激活许可。C 再持锁复核许可、ledger 修订、完整资源集合和 staging 字节，才切换 current 并写 applied 回执；许可本身不能当作安装成功。E 不等待 C 的 applied 回执才授权，避免双方互等。失败或修订变化均重新验证，不能沿用旧许可。

锁内重新读取并核验文件 revision、可信身份、待停用回执、guardian 状态和高水位，禁止依据加锁前快照写入。文件更新写同目录临时文件，flush/fsync 后原子替换并持久化父目录。Windows 使用等价的持久化替换 API。读者持同一锁，原子替换失败或状态未知时失败封闭。固定根和隔离目录禁止链接与跨文件系统移动。

Desktop 停用完全由 E 独立完成。E 先持锁将 guardian 状态设 stopping 并记录 pendingIsolation，释放锁停止整个 DSH 进程树，确认退出后重新持锁进入 isolating。C 此时已停止运行。E 重新核验归属，将 `skills-active/current/` 原子改名为 `skills-active/quarantine/<transactionId>/`，不原地遍历删除；确认 current 缺失或为空且隔离目录归属匹配后，先持久化 disable 回执，再把 state 写为 private_only。若 current 原已为空，也写同样回执。rename 后崩溃时按 pendingIsolation 恢复并补齐回执。隔离失败、进程未停或无法确认清空则 blocked，拒绝启动。

C 下次启动先持锁读取未处理 disable 回执，验证 tenant/owner/host 和路径归属，用 cleanup 事务清理隔离目录并标记 ledger.processedDisableReceiptIds。回执不得删除，高水位不得回退。清理失败保留回执和原状态。C 不得从旧安装记录自行恢复共享目录，必须等 E 联网重验并授权新激活。历史会话可查看，private_only 必须新建会话，原上下文不能恢复执行。

guardian 全部转移、守卫和动作见 guardian-transitions.json。未列转移默认拒绝。验证失败且进程仍运行则 stopping，已停止则 isolating；blocked 可重试清理，private_only 恢复共享须在线重验并先停进程。active 的每次 tick 都要求租约、身份和时钟仍有效，任一失效立即停机。

防回拨字段固定在 UnifiedTrustedClock，持久化已见最大可信时间、最后可信时间及来源、bootId、最后单调毫秒数和当前租约的单调截止。相同 bootId 下单调计数不得降低，墙钟小于已见最大可信时间即拒绝共享，容差为0；按转移文件中公式同时核对 UTC 与单调截止。不同 bootId 的计数不可比较，离线重启不重开窗口，必须先停机隔离才能以私有新会话启动，恢复共享需要新的在线可信时间。D 在云端执行等价行为，C/E 的 Desktop 文件所有权约定由 D 映射到自己的可信执行器。

## 服务端授权矩阵

基础 resource.packages.upload 只授予上传基础能力。skill/text-only 还须 tenant/owner、L3共享、内容扫描及风险下限校验。plugin 继续旧专属接口与原等级规则。tool/online、tool/offline、tool/git 在 P1 全部拒绝，后续分别启用细分授权、端点/凭据与网络检查、二进制来源/平台检查、固定commit和隔离构建授权后开放。未知 kind/delivery 默认拒绝。

审核/发布每次核验当前版本修订和原始摘要。R1 至少一名非作者，R2/R3 至少两名不同非作者且一名安全资格审核者。role=security 由服务端资格验证。同一人多个角色只计一次，作者禁止审核/发布。风险只可上调，内容或风险变更递增修订并清空旧审核。分发使用 resource.distributions.manage，且当前目标实例/主人/节点归属有效。原始清单和制品必须逐次授权，票据和 locator 均无法绕过 ACL。

## HTTP 条件写入与分页

所有 Unified 写操作必填 If-Match 和 Idempotency-Key，成功响应提供 ETag。初次创建使用 `"0"`，已有对象使用服务端当前修订的 ETag。缺失前置条件返回 common.validation_failed，旧 ETag 返回412 common.precondition_failed。票据/下载 POST 的 ETag 绑定当前安装修订，不能绕过撤销或实时授权。成员列表必填 clientId，可带 kind、limit、cursor；分发列表支持 instanceId、kind、limit、cursor。游标绑定认证租户、过滤条件和快照，拒绝换过滤条件复用。limit 默认50，范围1到200。

所有 Unified 下载 locator 必须满足 Schema 同源相对路径，客户端拒绝绝对URL、`//`、编码路径穿越、query、fragment及重定向，不向其他来源携带凭据。新增管理员分发创建、列表、详情都要求 resource.distributions.manage 和600秒内 step-up。
