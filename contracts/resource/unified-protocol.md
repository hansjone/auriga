# P1-K 统一资源协议

本文件和 unified.schema.json 冻结 P1 公共边界。新增公开 Schema 先查询 unified-registry.json，再集中登记。旧插件 HTTP、事件及 v1 消息继续遵循原语义。新增目录协议首期接收 skill，tool 的分支保留供后续期按下述矩阵开放。

## 版本、下载与回报

UnifiedResourceIdentity 绑定可信 tenant、kind、packageId、versionId、严格 SemVer 及原始 manifestDigest。所有 ID 必须由服务端解析归属并比较。UnifiedResourceRef 同时绑定发行声明、精确制品列表与租约。manifestDownloadPath 和 downloadPath 必须是同源平台 locator，每次请求重新核验可见性和归属，摘要知识不能授予访问。

成员票据绑定认证 tenant/owner、clientId、identity、installationRevision、制品范围及截止时间。下载同时验证当前身份、客户端绑定、安装修订、票据及租约，票据不放进 URL 或事件。直接清单/制品端点按当前版本 ACL 检查，草稿仅作者和获权审核人员可读，已发布仅可见成员可读，跨租户和 plugin ID 返回404。管理员读取原始清单仍需当前审核权限。下载原始清单后先核验原字节摘要，再与已验证 envelope 比较后解析。

写操作带 If-Match 和 Idempotency-Key。幂等键绑定完整请求指纹，指纹变化返回冲突。installationRevision 是成员安装 CAS 代次，selectionRevision 是实例资源选择代次，generation 是实例规格代数。相同 selectionRevision 的重试锁定精确版本与字节，乱序选择不得覆盖新选择。续签只刷新同 identity/摘要/releaseSequence 的签名和租约，保留 selectionRevision，规格变更仍递增 generation。

202 表示接受意图。applied 要求报告 identity、installationRevision、selectionRevision 和 generation 与当前期望完全相等，observedGeneration 等于 generation，且宿主公开 catalog 已确认名称与实际来源。member 来源仅表示成员自报，node 来源只能由已认证调度节点上报，服务端从认证通道决定来源。失败状态包括 failed、pending_restart、superseded、overridden、expired、revoked。失败保留 errorCode 和原安装，禁止把未知来源/状态计为 applied。云端 C 仅观察和回报宿主来源，D 为共享内容唯一写者。

## 信任、迁移与撤销

所有签名按既有独立 release/revocation/downgrade 根验证，保留原 UTC 时间文本，JCS 去除顶层 signature 后验 Ed25519。v1 仅接受 schemaVersion=1/purpose=plugin，v2 仅接受 schemaVersion=2/purpose=resource。v2 artifactDigests 必须与原始清单集合精确一致、去重并按字典序排序。租约截止 expiresAt 必须等于 envelopeNotAfter 和 revocationsExpiresAt 的较早值，两个可信声明各自最多24小时。

UnifiedMigrationProof 包含已签名 legacyEnvelope、unifiedEnvelope、legacyArtifactId 和首个 v2 激活时间 activatedAt。消费者必须取得原 v1 归档和原始 v2 清单字节，独立验证两份签名、摘要、tenant/package/version/releaseSequence 一致，且 plugin payload 的 legacyArtifactId 对应 digest 等于 v1 归档摘要 A。v2 清单摘要为 M。activatedAt 由服务端首次 v2 签发事务写入不可回退标记，消费者须从可信迁移记录确认该时间。关联表不可替代上述密码学证明，相同映射禁止更换 M。该格式只供未来插件迁移，P1 不据此启用插件 v2。

高水位以 tenant/package 为键，跨协议共享最高 releaseSequence、versionId 和已验证摘要别名。相同序号仅接受同版本同摘要或完整证明固定的 A/M，跨租户、伪造映射、替换 M 均拒绝。低序号跨协议一律拒绝，独立降级授权须逐项绑定 tenant/instance/package/version/digest 和 from/to 序号，有效期内使用且保留最高水位。迁移证明和高水位在应用回滚、卸载后继续保留。

恶意撤销迁移版本必须同一事务登记 A、M 两条撤销项及 outbox。v1 按 A 匹配，v2 对任一条目先按 packageId/versionId 禁用整个版本，再逐项对 manifestDigest 和全部 artifactDigest 匹配可信租户撤销表。相同恶意制品被其他版本复用也拒绝，服务端仍逐一传播所有引用版本撤销。普通 withdrawn 不写恶意撤销表。

首个 v2 签发时刻 t0 以后禁止 v1 新版本。已应用版本 v1 续签 notAfter 不得超过固定 t0+24h，截止后拒绝续签。回滚不能重置 t0 或重开窗口。未实际开启插件 v2 的包继续原 v1 链路。

## 固定目录和 guardian

静态 customSkillDirs 保留现有顺序、表达式、预设和 includeDefaultRoots，并去重追加 `<DSH_HOME>/auriga/skills-active/current/`。该根下一层直接为 `<name>/SKILL.md`。登录身份保存在可信运行时记录，静态配置仅写固定扫描根。

身份缓存和回执布局固定为 `<DSH_HOME>/auriga/resources/<tenant>/<owner>/<client或instance>/skills/`，内部 `cache/<sha256-hex>/`、`staging/<transactionId>/`、`ledger.json`、`receipts/<packageId>/<versionId>.json`、`transactions/<transactionId>.json`。guardian 状态在 `<DSH_HOME>/auriga/guardian/state.json`，高水位在 `<DSH_HOME>/auriga/guardian/high-watermarks.json`。这些目录均在扫描根外，由可信进程写入并通过所有权/ACL校验。路径身份均来自已核验上下文，禁止外部输入任意目录。切换在宿主级锁和事务日志下原子完成，身份变化先清空旧活跃内容并确认，再激活新身份。

D 控制云端配置、共享内容和 guardian。E 控制 Desktop 静态配置与独立于可卸载插件的 guardian，C 控制 Desktop 下载和激活。guardian 启动时 verifying，验签、撤销及租约均有效才进入 active。租约计时结合可信时间和单调时钟持久化，时钟回拨或无法证明有效期时失败封闭，重启不能重开窗口。

撤销、到期或身份切换进入 stopping，停止受影响 DSH 整个进程树并确认后进入 isolating。随后调用 C 的可信原子停用端口清空共享视图，C 不可用时由 E 隔离并核验目录。processStopped 和 viewCleared 都为 true 才允许 private_only，newSessionRequired 必须为 true。历史保留可查看，原污染上下文不能恢复执行。清理失败进入 blocked，拒绝启动。联网重新验签授权后才恢复共享。所有未知状态拒绝启动，私有离线新会话仍须先满足上述停机与清空证明。

## 服务端授权矩阵

基础 resource.packages.upload 只授予上传基础能力。skill/text-only 还须 tenant/owner、L3共享、内容扫描及风险下限校验。plugin 继续旧专属接口与原等级规则。tool/online、tool/offline、tool/git 在 P1 全部拒绝，后续分别启用细分授权、端点/凭据与网络检查、二进制来源/平台检查、固定commit和隔离构建授权后开放。未知 kind/delivery 默认拒绝。

审核/发布每次核验当前版本修订和原始摘要。R1 至少一名非作者，R2/R3 至少两名不同非作者且一名安全资格审核者。role=security 由服务端资格验证。同一人多个角色只计一次，作者禁止审核/发布。风险只可上调，内容或风险变更递增修订并清空旧审核。分发使用 resource.distributions.manage，且当前目标实例/主人/节点归属有效。原始清单和制品必须逐次授权，票据和 locator 均无法绕过 ACL。
