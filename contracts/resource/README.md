# R1 首批资源与签名边界

公开源码的通用契约适用于任何自托管部署，包括明示内网 IP:port 与 HTTP 前台。
安装环境不能改变租户、权限、签名或节点身份规则。这里固定契约，服务和 agent 消费实现留给后续任务。

## 资源流程、权限与对象存储

本节描述既有v1及原插件路径，只有 `kind=plugin/purpose=plugin`；Skill不能冒充plugin。
新增通用catalog草案见末节，P1的插件管理视图继续使用原接口，Skill/tool另走通用路径。
共享资源流程 `draft -> pending_review -> approved -> published -> deprecated/withdrawn`，
`rejected -> draft`。审核绑定 digest+versionRevision；变更草稿清空审核。
发布内容 `(tenant,package,version)` 不可覆盖；归档核验成功、审核与签名完成才能发布。
版本作者不得审核或发布自己的共享版本；审核者可与发布者相同。
L4 本人私有分支仍需平台完整签名，显示“未审核”，不能直接共享；没有隐含可信作者免审。

等级与可见范围都须通过：L0 随部署固定，资源 API 不改变门锁；L1 管理者统一，成员不能关闭/卸载；
L2 成员可关闭不可卸载；L3 审核发布后成员可安装/关闭/卸载；L4 仅本人、强制 private，
并受 WorkspacePolicy.allowPersonalPlugins 控制。`tenant` 含本租户 external，`members` 不含 external，
`groups` 只限当前组成员，`private` 仅owner。无跨租户/匿名公共发布；同摘要不授予ACL。
创建人/tenant/等级由服务端决定，不能由请求任意指定；等级管理只允许内置owner的显式权限+近期验证。
作者离职只按同租户 handoverTo 转交，无接手人先冻结，不自动公开私人包。

上传只整包 gzip，最大64 MiB，最大4096条目、解压总量256 MiB、压缩比100，
拒绝绝对路径、`..`、反斜杠/规范化逃逸、链接、设备、重复规范路径及脚本执行；
解析归档不执行 npm/install。先暂存核摘要/size/manifest，再提交不可变对象，
再在 resource 事务登记元数据/outbox。DB失败的孤儿对象保留24小时后由无引用清理器回收。
对象端口 `PutStaged(stream,maxBytes)->stagingId,digest,size; CommitIfAbsent; Open(digest); Stat; Abort;
DeleteUnreferenced`，对象key只由验证digest推导。物理去重不合并租户授权，首批不承诺S3/Range。
删除必须确认版本、分发和保留引用全部释放；下架不是立即删除对象。

创建/送审/发布/分发遵循24小时幂等，作用域与请求指纹规则见 `ai/README.md`。
ETag/If-Match 控制管理元数据和状态变更；相同幂等键异体 `common.conflict`，
版本唯一约束独立于幂等缓存。普通跨租户/不可见对象404，不泄漏名称/摘要。
审核、发布、撤回、等级、降级、重签、撤销记脱敏审计；事件不得带secret/prompt/token/任意bearer下载URL。

## D4 签名与精确 JCS 映射

`signatures.schema.json` 是JSON唯一来源；`trust.proto` 字段逐项映射为这里的lowerCamelCase。
算法 Ed25519，signature.value 为64字节签名的无填充base64url（严格解码并重编码比对）；
对**只去除顶层 signature 后的完整对象**执行 RFC8785 JCS，再以UTF-8签名。
禁止重复JSON key、无效UTF-8/孤立代理项、NaN/无穷、未知签名字段；
受签名schema的 `additionalProperties:false` 是安全规范的一部分，不能为“向前兼容”静默忽略被签名字段。
这类新字段必须新 schemaVersion，而不是跳过后继续验旧签名。

所有声明字段必须显式出现（包括空撤销数组）；字符串不做Unicode/时间规范化，按原值签名。
时间必须UTC `Z`，0–9位小数，禁止时区偏移和闰秒；验证真实日历与先后关系。
protobuf 时间用声明中的字符串，旧 `ArtifactStatement.issued_at` 按瞬间相等核对；
签名的原JSON时间字符串不能通过 Timestamp 往返重建后换精度。
整数必须[1,9007199254740991]且用JSON number，不能沿用protojson的uint64字符串。
JCS排序是UTF-16码元顺序、数组顺序不变、无多余空白；禁止默认protobuf JSON输出充当JCS。

ResourceEnvelope 绑定 tenantId/packageId/versionId/releaseSequence/notAfter 和完整statement。
同时验证旧 PluginRef.signed.statement 与新statement的 purpose/name/version/os/arch/digest/issuedAt，
再逐项比较workspace期望引用；签名不能替代ACL或容许更换租户身份。
`statement.issuedAt <= now < notAfter`，有效期最多24小时；
重签只能延长有效期，不改变归档、versionId、releaseSequence，并需重新授权与审计。
每租户/包持久化最高 releaseSequence；同序号必须同versionId/digest，拒绝换字节。
普通SemVer降级不越过高水位；显式DowngradeAuthorization绑定实例/版本/摘要、from/to安全序号、
一次性authorizationId、理由、有效期最多10分钟，from等于当前高水位且to更小。
授权使用后持久去重，保留高水位，后续启动仅允许已授权固定实例/版本，不放开其他低版本。

发行、撤销、降级三类公钥独立用途，部署经受保护的**根信任配置**提供，不能从归档自证，
也不能让被撤销的发行key自行签一份撤销清单。`ResourceTrustKey` 是公钥配置形状，
不是允许workspace通过ApplySpec随意改根；保护部署根及未来根轮换另由AR1/部署流程实现。
现有节点Join artifact_signing_keys须按既有独立可信路径持久化，不能因契约已有字段称实现完成。

## 撤销与离线最迟24小时

签名清单绑定tenantId/revision/issuedAt/expiresAt/revokedKids/revokedArtifacts，最多2MiB，
2048个kid、8192个制品项，`0 < expiresAt-issuedAt <=24h`，拒绝未来签发或过期。
每租户持久化最高revision与签名正文摘要，同revision仅接受相同正文；更低revision拒绝。
跨清单续期也必须增加revision。spec中资源选择revision、generation及撤销revision相互独立，不能互代。
ApplySpec携带新清单而不替换根。缓存命中仍检查签名、身份、有效期、ACL、撤销与高水位。

普通 withdrawn 阻止新下载/安装/启用，已运行实例保留已应用版本并通知管理者。
malicious 必须发布撤销清单、workspace显式禁用/替换，agent在收到有效新清单后立即停用受影响代码；
插件无法热卸载时必须停止实例再安全移除，不能以正常“等空闲重启”延迟恶意停用。
离线或控制面不可达时，在当前可信清单expiresAt（最长24h）必须停止含受管资源的实例，
直到拿到有效清单并重验；不得无限使用旧缓存。空/缺失清单不代表未撤销。
这保证最迟24h停用，不能宣称全网即时清除；现场断网/重启验收由AR1/Z1负责。

## 分发与节点下载

resource事务记录意图和resource.distribution_requested；workspace复核主人/策略，
以selectionRevision锁定精确版本、增加generation，再下发agent。resource不读workspace表或直接指挥节点。
只有目标generation=observedGeneration且ready才applied；202只表示已登记。
乱序旧selectionRevision不能覆盖新选择，重试同revision不重新解析latest。

下载授权绑定downloadId/nodeId/tenantId/instanceId/ownerId/packageId/versionId/digest/selectionRevision/expiry，
最多5分钟。内部URL是locator，不是bearer；每次请求和重试都认证节点mTLS URI SAN，
在线查workspace当前调度/实例/规格和resource当前可见/撤销/下架状态，拒绝任意节点或旧调度下载。
公网edge不路由 `/internal/resource/**`，浏览器下载另走已授权成员路径，禁止无鉴权blobs/digest入口。
首批不支持Range，带Range返回400 common.validation_failed；断线重新下载整包并重新鉴权。
过期授权由workspace重新解析同锁定选择续发，不能延长客户端提供的旧expiry。

## 统一资源包契约草案

统一主文档为 `docs/design/resource-package.zh.md`，skill专项为
`docs/design/skill-market.zh.md`。本轮将第1轮Skill草案改成通用catalog命名空间，
新增 `/api/resource/v1/catalog/**`、`/api/resource/v1/admin/catalog/**`，
本轮补审核详情和原始清单下载后共14个操作，kind筛选只支持skill/tool，均标记 `x-auriga-contract-stage=draft`。
plugin不在通用接口展示或写入，旧L0–L4/private使用原接口；内部只读关联投影不冒充UnifiedPackage。

`package.schema.json` 定义auriga-resource.json公共字段与kind专属段，tool覆盖
online/offline/git。清单位于制品外，制品按原字节SHA-256寻址，版本digest为清单原字节摘要。
清单signature字段声明detached/Ed25519协议，实际签名为独立Envelope v2，签名完整覆盖
租户、资源、版本、安全序号、清单及制品摘要和有效期。旧ResourceEnvelope v1与
旧Version.digest插件归档语义继续由原契约表达。迁移通过来源关联与协议选择接入，
保留撤销和高水位，原主干接口及schemas逐项保留。

插件后续真正双签迁移时，同一版本的旧归档摘要A与新清单摘要M要经原始字节、两种签名和清单payload
建立不可变关联；恶意撤销同事务写A/M两项。v2按packageId/versionId禁用整版本，并逐项核对清单及
全部制品摘要与可信租户撤销表。高水位按tenant/package共用seq/versionId和已验证A/M别名，拒绝
跨协议回滚、假关联或同序号换M。首个v2签发t0后不再用v1发新版本，旧v1续签固定截止t0+24h，
回滚不能重置截止。具体规则与P1-K密码学向量/负例见主文档第2节，不改现有v1撤销格式。

统一清单严格区分plugin、text-only skill与三种工具，运行时支持Node/Python/Java声明。
Git锁完整commit并引用源码快照/锁文件制品，installScripts=false；在线工具无本地制品。
installScripts仍为必填且仅允许布尔false。Schema以boolean加not true表达，避开Java生成器
对布尔const/单值enum生成错误字符串字面量的问题；生成模型不能替代服务端Schema校验。
共享草案先用L3，私有/开发者侧载只在当前DSH使用，冻结快照后显式送审。
跨制品引用、归档路径、平台/入口关系、runtime兼容性与时间窗口需服务端语义校验。

UnifiedRiskAssessment为服务端只读风险下限/有效等级及审核人数，命令型或有工具/外连/数据权限的
Skill至少R2，Git工具R3，审核只能上调。R1一名非作者，R2/R3两名不同非作者且含有资格的安全审核。
UnifiedReviewPage/UnifiedReview提供reviewId、作者、版本风险和决定历史；UnifiedReviewDecision
必填general/security职责，身份与资格在服务端核对。审核和发布事务均复核人数，不采信客户端自报。

UnifiedVersion.manifest仅展示，安装端从manifestDownloadPath逐次鉴权下载原始清单字节，核对digest
及已验签Envelope后解析，禁止重序列化展示对象代替原字节。通用发布/下架使用新增
resource.catalog_version_published/catalog_version_withdrawn，带kind、signatureVersion、manifestDigest
和artifactDigests；旧resource.version_*的digest仍只指插件归档。通用分发将另增事件，不重载旧类型。

共享Skill固定挂载 `<DSH_HOME>/auriga/skills-active/current/`。K冻结路径，云端D唯一写配置与共享文件，
Desktop E写配置、C切换内容，保留上游预置目录。超期离线先停DSH并隔离失效受管内容，确认清理后
允许私有Skill新会话；无法隔离则拒绝启动。历史只供查看，共享恢复必须联网重新授权与验签。

后续契约线补充密码学向量、proto/workspace引用、票据/制品下载、分发、安装回报、
重签、runtime目录与构建来源证明。审核详情/职责/人数、原始清单下载和新发布/下架事件已在本轮补入。
当前草案不代表这些服务已实现。
用户2026-11-12确认D1-D11按推荐执行。D4/D5要求Skill恶意撤销或租约到期时停止受影响DSH，
恢复使用新会话，共享Skill首期仅在具备可信guardian的云端与Desktop开放。
D8确定Node复用Desktop自带版本，Python采用python-build-standalone、Java采用Eclipse Temurin
21 LTS，经平台审核签名后按需下载。机器契约沿本轮已有定义，P1契约补齐与各实现线边界见主文档。
