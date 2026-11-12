# Skill 市场契约与设计草案

状态：待用户审阅；本轮不实现、不部署。依据基线 `0f7b2712329b9416260e755803dcb182a4ad1a5e`，核对日期 2026-11-12。下文“应”“必须”约束后续实现，不表示当前入口已支持 Skill。

## 1. 边界和依据

保留一个 Auriga 插件，成员在“资源市场 / 我的资源”水平页签使用技能；共享内容由总部审核发布，个人私有内容仅在当前 DSH 使用。云端实例与本地 DSH 是两个安装位置，不隐式同步。总部管理台只治理共享资源，不默认浏览个人文件。

本仓库依据：

- `contracts/resource/README.md`、`contracts/openapi/resource.yaml`、`contracts/resource/signatures.schema.json`：审核绑定、签名、ACL、版本高水位、撤销与下载授权。
- `services/resource/README.md` 及 `ResourceWorkflow`、`ResourceMembers`、归档校验器：已实现插件流程；PR304 的管理侧落在 `web/admin/src/views/resources/ResourceGovernanceView.vue`，PR308 的成员链路落在 `web/panel/src/resources.ts`、`clients/dsh-plugin/src/resources/{engine,host,member-api}.js`。这些入口和验证器目前严格接受 plugin。
- `docs/design/auriga-admin/requirements-review.md` 的已确认范围 2、3 和 F01；`docs/adr/0004-platform-controls-dsh.md`、`0015-contracts-first.md`、`0019-dsh-runtime-artifact.md`、`0021-m3-contract-boundaries.md`。
- `clients/installer/upstream.json` 当前为 DSH `0.2.1-rc.3`；`runtime/core.mjs` 的 `launchEnv` 使用独立 `DSH_HOME`。旧共同规则里的 rc.2 不覆盖这里的现行锁定输入。

上游公开依据（仅使用公开格式、公开配置和服务）：

- [filesystem provider README](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/skill/skill-filesystem/README.md)：根目录和 frontmatter 格式。另核对 [同版本 npm 包](https://www.npmjs.com/package/@deepseek-ai/dsh-skill-filesystem/v/0.2.1-rc.3) 发布归档内 README，integrity 为 `sha512-IMGONDTeLvQanAAfI/2cLm1/Ppl/msriFxjOON/TOJAEnulou4inRAJ1DaJhLwDbhuX7zMUbapLQE7kwHN+k8g==`。
- [公开技能子系统契约](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/skills.md)：provider 注册、列举、加载、作用域及失效通知。master 会变化；实现阶段必须针对受控运行版本做能力探测和真实宿主验收，不按私有文件哈希绑死上游。
- [工具消费契约](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/skill/tool-skill/README.md)：内容进入会话历史后，修改磁盘不能抹除历史。这也是撤销不能只删文件的原因。

旧 `oclaw/dsh-team-hub/lib/resources/{index,package}.js` 保留 H3 及后续资源中心交互：目录、我的资源、上传、版本、送审、驳回理由、发布。只借鉴这些流程；不沿用旧 `/dsh-team/api/skills`、SQLite 表、签名或宿主接线。

## 2. 包规范（推荐首期）

一个归档只含一个技能，采用严格 ustar + gzip。归档根为 `SKILL.md`、`manifest.json`，可选 `references/`、`assets/` 下的资料；没有 npm 的 `package/` 外层。安装器将原始包放到校验区，再安装为 `<加载根>/<name>/SKILL.md`，不能在加载根下再套包 ID / 版本目录而期待上游递归发现。

`SKILL.md` 为 UTF-8 Markdown，开头是 YAML frontmatter，必填 `name`、`description`；可选 `whenToUse`、`metadata`、`disable-model-invocation`、`user-invocable`。Auriga 首期只接受真正 YAML 布尔值，拒绝重复键、标签、别名、过深结构。名称为小写 kebab-case，1–64 字符，Windows 保留设备名禁止；目录名、manifest.name 和 frontmatter.name 一致。description 1–1024 字符，whenToUse 最长 2048，metadata JSON 等价序列化不超过 8 KiB。调用开关仅影响技能曝光，不构成权限隔离。

`manifest.json` 对应新增 `SkillManifest`：schemaVersion=1、name、严格 SemVer version（≤128 字符）、description、dshVersionRange、contentProfile=text-only、license、outboundHosts、requiredTools 必填，sourceUrl 可选。出网域名最多 32 个，不含通配符、路径、端口和凭据；工具标识最多 32 个。license 用 SPDX 标识或明确许可证引用，第三方资料须有授权。name/description 与 frontmatter 精确相等；上传元数据的 version/manifest 与归档逐项一致。未知 manifest 字段拒绝。tenant、作者、ACL、等级、审核记录来自服务端，不接受归档自声明。工具和出网声明供审核展示，不自动开权限。

大小上限：压缩 5 MiB、解压总量 20 MiB、条目总数 512（含目录）、普通单文件 2 MiB、SKILL.md 256 KiB、manifest 32 KiB、路径 240 个 UTF-8 字节、最多 8 层、压缩比 100。流式计数，任一越限终止；拒绝多 gzip 成员、尾随载荷及解压炸弹。继承现有路径检查并增加 Windows 大小写折叠碰撞、保留名、尾点/空格与 Unicode 规范化碰撞拒绝；不悄悄重命名。禁止绝对路径、反斜杠、点段、链接、设备、硬链接、重复规范路径、setuid 和执行位。

首期允许文件仅 UTF-8 `.md/.txt/.json/.yaml/.yml`，文件内容也校验，禁止伪装二进制；`assets/` 首期仅文本资料。拒绝 `.sh/.ps1/.bat/.cmd/.py/.js/.mjs/.exe/.dll/.so` 等以及 `scripts/`、嵌套压缩包、HTML/SVG 主动内容。安装和审核从不执行包内容、npm lifecycle 或远程拉取依赖。Markdown 示例命令仍可能诱导模型执行，必须人工审正文；“纯文本”不等于无执行风险。

推荐先禁附带脚本，而不是把“更高等级审核”当运行隔离。后续确需脚本，应单独引入 code profile、双人审核、逐工具与网络权限、无平台凭据的沙箱和输出限制，再扩 schemaVersion；目前 DSH 原生技能加载本身不提供此保证，不能宣传“已隔离运行”。本机用户手工技能不通过市场上传规则强制改写，其风险由宿主工具授权约束。

身份规则：packageId/versionId 由服务端生成 UUIDv7，packageId 与名称、摘要无推导关系；`(tenantId, kind, name)` 唯一，Skill 与同名插件可并存。共享安装后的模型名称仍为 manifest.name，同一 DSH 不允许两个包占同名。`(tenant,package,version)` 不可覆盖，重打包即使文本相同也作为不同字节拒绝覆盖。

`digest=sha256:<64位小写hex>` 计算**原始 gzip 全部字节**，作为归档寻址与发行绑定。`manifestDigest` 是归档内 manifest.json **原始 UTF-8 字节**的 SHA-256，禁止先 parse/stringify 重建。文件清单逐个记路径/字节数/摘要，供审核和安装后核验；不以文件清单拼接摘要代替归档 digest。建议打包器固定排序、mtime/uid/gid=0、普通权限0644/目录0755、gzip时间戳，便于重建，但服务端仍以接收字节为准。包内不含自签信任根。

## 3. resource 扩展与契约覆盖范围

复用资源服务的对象存储、暂存/提交/孤儿回收、租户 RLS、ACL、幂等（24h）、ETag、审核/签发职责分离及 outbox。状态继续 `draft -> pending_review -> approved -> published -> deprecated/withdrawn`，驳回恢复草稿后重新送审；审核绑定 versionRevision+digest，manifest 在 digest 内。作者不得自审或自发；不能只因作者可信免审。

等级 L0–L4 表示管理控制，不是安全审核分数。首期共享技能只支持 L3，可安装/关闭/卸载；本地私有为本地类型，不把它伪装成已签 L4。强制 L1/L2 技能会受项目同名覆盖和会话残留影响，不能照搬插件“禁止关闭”承诺，留待用户明确要求后设计。独立审核风险分为：R1 纯流程资料、R2 涉命令/工具/外部域名/敏感数据操作、R3 附带可执行内容。首期 R1 一名非作者完整审核，R2 两名非作者（含安全审核）均通过后发布，R3 禁止。风险由服务端扫描辅助、审核者确认，作者不能自行降低。

仅枚举扩展不足：Package/PackageInput 的 kind、旧 Statement.purpose、归档 manifest、PluginRef、客户端 schema 和应用状态均锁 plugin。旧 ResourceEnvelope v1 是严格 additionalProperties=false 的签名格式，不能偷加字段。草案新增 `SkillStatement`、`SkillEnvelope`（schemaVersion=2，purpose=skill，os/arch=any，含 manifestDigest），继续完整对象去顶层 signature 后 RFC8785 JCS/Ed25519。信任根、发行/撤销/降级分钥、字符串时间精度规则复用。旧验证器必须拒绝 v2。

本轮 OpenAPI **只增加** Skill 前缀 schemas、operationId，独立 `/api/resource/v1/skills/**` 和 `/api/resource/v1/admin/skills/**` 草案路径，并标 `x-auriga-contract-stage=draft`。旧路径、schema 和插件枚举原样保留，避免旧成员严格解码器收到 skill。

草案覆盖包列举/创建/读取、版本上传/列举/详情、送审、管理员审核队列/已发布、审核决策、发布、下架。复用原 ReviewDecision/Withdrawal 等通用请求及权限、错误码、事件声明；管理员路径保留近期再验证、作者分离和精确 ETag。上传仍为受限 multipart。不得据此启用路由。

这是首轮契约切片，**尚非可实现全链的最终契约**：草稿编辑/恢复、审核详情与历史、Skill 安装回报/票据、重签、分发、workspace spec/proto、策略、签名 JSON Schema/向量和事件 kind 扩展列入下一阶段契约线。`SkillSelection` 预留精确引用形状，不接到旧插件安装接口；不把 SkillEnvelope 塞入旧 PluginRef。RevocationList 现有租户/版本/摘要项能表达技能撤销，但所有消费者须先支持 skill，再复用清单；不能因格式可用认定执行器已支持。

未来下载：云端复用节点 mTLS + 当前调度/owner/ACL/selectionRevision 校验，公网不开放 internal；本地复用成员60秒单次正文票据及已认证整包下载（禁止 URL bearer、Range、跨成员 clientId）。新增 Skill 类型票据接口前不调用旧插件接口。分发 resource 记录意图，workspace 锁精确选择并增加 generation；202 只是登记。applied 要有目标 generation、安装与宿主可发现性验证；成员回报仍明确 reportSource=member，不冒节点证明。

## 4. 私有和共享

个人在当前 DSH 的“我的资源 → 新建私有技能”输入名称/说明/正文，先预览和本地校验，再写 `<DSH_HOME>/skills/<name>/SKILL.md`。手工创建的原生技能也可本机列举，读取范围限公开技能根；不扫描任意文件系统。打开/编辑用 DSH 原生文件能力，不在总部造私有编辑后台。关闭时移动我方管理的技能到非扫描区；用户手工目录需明确接管，不能默默挪走。

私有内容不调用 resource 上传、签名、分发或审核接口，也不传内容摘要/文件名清单到管理台；只在当前 DSH 可见。云端私有文件留本人实例卷，备份按工作空间备份策略处理，默认不跨实例同步。管理员默认不可浏览内容；主机维护人员理论上可能通过存储权限接触卷，产品不能承诺端到端加密。离职冻结/转交共享包不自动公开私有目录。

“提交共享”显式展示将上传的全部文件、风险声明和目标范围，用户确认后快照为独立共享 L3 草稿；不上传无关兄弟目录或自动替换本地私有版本。审核发布后共享和本地私有副本各自演进，不暗中建立同步。发布范围复用 tenant/members/groups，租户成员变化实时重核，无匿名/跨租户公共市场。跨租户市场需要独立产品决定。

## 5. 位置和安装生命周期

本地私有原生目录统一 `<DSH_HOME>/skills`；Auriga Desktop 默认 `%LOCALAPPDATA%\AurigaDesktop\home\skills`，自定义 DataHome 时跟随真实值，不能硬编码 `~/.dsh` 或写到 `versions/`。普通 DSH 若使用默认 home，则是 `~/.dsh/skills`。云端同样以节点分配的实例 `DSH_HOME` 为根，宿主实例卷持久化；不写共享只读 DSH 运行时或镜像层，不猜固定 NAS 物理路径。

受管共享推荐分离：原始对象、ledger、暂存/隔离区在 `<DSH_HOME>/auriga/resources/skills/<tenant>/<owner>/<client-or-instance>/`；启用加载视图在 `<DSH_HOME>/auriga/skills-active/<tenant>/<owner>/<client-or-instance>/<name>/SKILL.md`。后者作为 DSH filesystem provider 的公开 customSkillDirs 根（rank 300），身份切换关闭旧视图，不能把多个身份根同时加入。启动时用公开配置挂接，不写 Loader 私有状态。只将启用且验证成功的包放入加载视图；版本缓存绝不位于扫描根内。更改 customSkillDirs 如需重启，明确显示“待重启”，不能改目录就报告已应用。

上游默认还扫描项目 `.dsh/skills`、`.agents/skills` 及用户根；项目来源优先于 custom，custom 优先于用户根，且作用域也可能覆盖全局。安装前通过公开 skills catalog 能力按当前 cwd 检查有效来源，检查所有已知根的同名项，并拒绝本地已有同名私有技能；以后新建项目覆盖时显示冲突/被覆盖，不删用户文件，不靠改 rank 强制压过项目。不同目录但同 frontmatter.name 也算冲突。用户可改名后重新打包重审；不能安装时改签名包内名称。首期仅 L3 能接受“项目可覆盖”的宿主边界。

安装顺序：锁 identity+package → 获取精确版本及在线授权 → 验证根/签名/租户/ACL/时间/撤销/高水位 → 下载到非加载暂存区并验 digest → 安全解包核 manifestDigest/文件清单 → 检查同名 → 原子提交版本缓存/ledger → 原子切换加载目录 → 读取宿主 catalog 并验证实际名称/来源 → 回报。调用链每阶段复核身份未换人。宿主能力不存在或观察不完整则失败封闭，保留可诊断状态；不因 watcher 没报错判成功。

更新显式选择固定版本，展示变更/风险/权限声明，不每次启动追 latest；关闭期间准备新缓存，切换失败恢复仍有效且未撤销的旧版本。Windows 目录占用允许回报待重启，重启前保持旧版本状态，不能混合两版文件。事务日志记录切换步骤，崩溃后启动恢复并先验证有效期，避免半安装暴露。降级必须沿独立授权，卸载不清除发行/撤销高水位。

关闭移除加载视图而保留缓存；卸载只删除 ledger 标记且文件摘要一致的我方文件，不递归清空用户技能根，检测手改则隔离冲突并提示。删除目录只阻止后续加载，旧会话可能已保留正文；普通关闭/更新提示新会话生效，可保留当前会话，不能保证正在进行的模型步骤立即切换。

## 6. 到期、下架、撤销与离线

Skill 无独立运行时进程，不走 PluginManager.installBundle 或“停技能进程”。普通 withdrawn 阻止新下载/安装/启用；已应用版本可在仍有效的租约内继续，展示已下架，后续是否继续重签须在线复核授权与下架策略，不自动无限续命。deprecated 展示替代版本，仍按固定选择处理。

共享 skill 的加载租约截止 `min(envelope.notAfter, revocations.expiresAt)`，两者各最长24h；缺少/过期/回滚清单不视作安全。到期即移出受管加载视图、阻止新加载，恢复联网后先拿新清单、重新授权/续签相同版本再恢复。缓存不绕过 ACL、撤销或安全序号，时钟回拨/无法建立可信时间则失败封闭。本地自建私有 skill 无平台租约，离线仍按原生 DSH 使用，不能将本地私有缓存当作共享的到期替代物。

紧急 malicious 撤销要先持久化更高 revision 的签名清单；收到后立即关闭共享加载视图并隔离缓存，阻断后续加载。**仅移出目录不足以处理已注入正文**：云端由 workspace/agent 停止受影响实例，重验并禁用撤销 skill 后才能再启动；本地由受管启动器停止该 DSH 进程、提示重新打开新会话，不自动恢复含恶意正文的历史会话。不能静默重写/删除历史。

普通插件安装模式没有可信进程控制时，只能阻止后续加载并显示“需停止 DSH / 新建会话”，无法承诺清除当前上下文。因此首期共享技能应只在具备可信 guardian/启动门禁的云端和 Auriga Desktop 开放；其他宿主只支持私有技能或显式提示共享能力不可用。guardian 必须独立于可卸载的 Auriga 插件，并在进程启动前校验租约。离线最迟租约到期停止受影响 DSH，不能宣称全网即时清除；若无此能力，不降低要求来宣称验收通过。

## 7. 页面与交互

管理台复用 `/resources/review`、`/resources/published`、`/resources/distribution`，加“全部/插件/技能”过滤；后端未就绪先不暴露可操作按钮。审核详情加 SKILL.md 安全纯文本预览、资料清单、版本 diff、manifest/摘要校验、工具/域名声明、R1/R2风险及审核人；禁止渲染原始 HTML、自动访问链接。审核通过、签发发布、分发应用分开显示。R2 缺第二人审核时禁止发布，作者不可自审自发。

已发布详情展示固定版本、安全序号、适配能力、下架/撤销区别及影响位置。分发页目标按成员/组/云端实例显示，区分待安装、待重启、已应用、被覆盖、失败、已取代和租约过期；私有技能不入表，本地结果标成员回报。未补齐契约的状态属于计划，不偷偷塞进既有 enum。

成员资源市场加类型过滤与位置“当前 DSH（云端/本地）”；安装明确当前目标，不用勾选暗示同步两处。技能卡展示描述/审核情况/风险/版本/更新时间，不暴露内部路径或签名术语。详情展示文件、工具与外连声明、更新差异和卸载；有冲突先指出同名来源。

“我的资源”同页列出已装共享技能与本机私有技能，可筛类型；私有提供创建/编辑/导入/删除/提交共享，共享提供更新/关闭/卸载。标明“仅当前 DSH”“待重启”“新会话生效”“已撤销/已过期”，给可执行的恢复操作。沿 DSH 原生组件/主题/图标和统一设置，不另造登录或 Vue 插件页；中英文文案齐备。

## 8. 安全和审计

提示注入是技能的核心风险：正文可伪装更高优先级要求、索要凭据、诱导执行或外传；description 也进入模型目录，须同正文一起审。对敏感文件读取、终端、网络仍按宿主工具权限与 Auriga 远程网关授权，不因签名/审核给额外权力。静态扫描只能辅助，不提供“无恶意”证明。

相较插件，text-only skill 没有 Node 顶层执行或 install hook，但可持续影响工具选择且留存于会话，撤销后上下文处置更难。插件的操作系统隔离不能自动套给技能，技能也不能突破成员/会话边界创建平台身份。

供应链验证覆盖原始归档、manifest、完整文件清单、受保护根及原子加载；签名只证明来源和字节，不证明内容安全。拒绝下载重定向到任意来源、路径穿越、符号链接/TOCTOU、跨租户缓存复用授权、低序号重放、旧撤销清单及未知签名字段。文件系统所有者可修改技能，完整性检查发现变更即停用，不包装成对本机管理员的防护边界。

云端内容按实例用户与卷权限隔离；Desktop 同机多身份 ledger/加载根分离，退出时关闭受管共享视图和旧请求；不把平台令牌/环境变量写入技能。公开 API 从可信认证上下文取 tenant/owner/client/instance，不从模型参数取身份。审计只记录包/版本/摘要、操作人/目标、风险审核决策、发行与撤销修订、安装结果和原因码；不记录 prompt、私有正文、令牌、绝对本机路径或 bearer URL。本地私有编辑不上传审计内容；共享读取/预览也要 ACL。

## 9. 分阶段实施与验收依赖

1. 用户确认本稿取舍后，契约线先补最终签名 JSON Schema/向量、proto/spec、分发与票据/安装回报、策略与风险审核状态、目录清单及事件；补跨租户、未知字段、过期、重放、同名冲突负例。当前草案路径不能直接当完整上线规格。
2. 契约稳定后后端线与宿主适配线可并行。后端扩 kind、归档 profile、R2审核计数、发布与重签/撤销、下载；宿主线在受控 DSH 验证公开 customSkillDirs/catalog、原子切换与占用恢复、作用域/项目覆盖、有效期与会话残留。后者不调用 PluginManager 插件安装路径。
3. 管理侧可按最终契约并行做类型过滤、技能审核预览/差异和分发跟踪；成员侧可并行做市场/我的资源交互、私有创建与位置表达，最后接宿主端口。禁止用 mock 壳称全链完成。
4. 安装器/节点线与后端并行，实现独立 guardian、DSH_HOME 路径与账户隔离、到期停用、恶意停机及启动门禁。依赖最终租约/撤销契约，是共享技能发布给用户前的硬门槛。各线向主控提供可部署产物，由主控串行做小批 NAS 开发部署。
5. 私有技能可作为独立小批先验收；共享技能在契约、后端、适配器和 guardian 接通后再部署。正常入口分别测云端和 Desktop 的安装/调用/更新/卸载，测试同名项目覆盖、跨身份、篡改、断网超期、撤销时已有会话、崩溃恢复。真实PG/DSH全链由主控串行占槽，本工位不启动。

## 10. 待用户决定

- D1 附带脚本：推荐首期 text-only 并禁可执行附件；备选开放 code profile，但必须先交付沙箱和双人安全审核。理由：审核不能代替运行时隔离。
- D2 受管加载目录：推荐 customSkillDirs 的独立活跃根，私有仍用原生 user root；备选都写 user root，但共享/私有冲突和到期清理更难。两者都不宣称压过项目技能。
- D3 首期等级：推荐共享只 L3、私有本地不上传；备选加入 L1/L2，需额外解决项目覆盖和可信门禁，不能机械复制插件策略。
- D4 离线与紧急处置：推荐延续最多24h租约并在撤销/超期停止受影响 DSH、恢复时新建会话；代价是可能中断其他任务。备选仅阻止新加载，不能满足当前资源最迟停用承诺，需要用户明确批准另立契约，不能静默采用。
- D5 开放宿主：推荐首期共享仅云端与具备 guardian 的 Auriga Desktop；任意外部 DSH 先支持私有。备选开放普通插件宿主共享安装，须先具备同等进程/启动控制能力。
- D6 审核风险：推荐 R1 一名非作者、R2 两名非作者（含安全）、R3拒绝；备选全部双人审更稳但吞吐更低。任何方案都保持作者不能自审自发。

个人内容不自动云同步、总部管理员默认不可浏览、共享由总部审核发布属于已确认范围，不在以上选项中重新开放。
