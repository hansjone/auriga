# 统一资源包规范

本稿覆盖插件、skill和工具，skill作为首个按本规范接入的新类型。用户于2026-11-12确认D1–D11全部按推荐执行，第10节已固化为实现要求。契约草案与后续实现以此为依据。设计基线为主干 `0f7b2712329b9416260e755803dcb182a4ad1a5e`，现有实现依据为资源契约、services/resource、PR304管理台和PR308成员安装链路，客户端布局依据clients/installer。Skill原生格式与加载详见专项文档。[Skill类型专项](skill-market.zh.md)

## 1. 统一模型与存储

资源由packageId标识，kind为 `plugin | skill | tool`。`agent-preset`预留为后续类型，接入时补专属结构与权限。名称在租户和kind内唯一，packageId/versionId由服务端生成UUIDv7。owner、tenant、提交者与审核者来自可信认证上下文，清单author只描述展示作者。

每个资源拥有不可变版本，包含严格SemVer、一份UTF-8 `auriga-resource.json`清单、零至多个制品及发行声明。版本经历draft、pending_review、approved、published、deprecated/withdrawn，rejected恢复draft后重新送审。审核绑定清单摘要和版本修订，内容或清单变化清空审核。发布后字节冻结，修改任一项须创建新版本。作者不能自审或自发。

PostgreSQL保存资源、版本、清单索引、制品引用、审核证据、发行声明、分发意图和审计引用，沿用tenant隔离与RLS。制品按已验证的SHA-256内容寻址，逻辑对象键同时含tenant。物理去重可复用存储层，授权与保留引用独立计算。已知摘要不会授予下载权限。

统一关系为资源→版本→清单和制品引用→审核证据→签名发行→精确目标选择→应用回报。在线工具版本冻结端点配置，远端服务的实际内容由运营方维护。

## 2. auriga-resource.json 与签名

### 清单字段

公共字段含schemaVersion、kind、name、version、description、author.displayName、license、permissions、runtimes、artifacts、signature。name为1至64字符小写kebab-case。plugin.packageName保留npm原名，容纳scoped插件。旧插件若显示名不符合新规则，迁移时分配规范名并保存来源映射，packageId保持稳定。

permissions包含工具标识、外连主机与数据范围，供审核与授权展示。runtimes声明node/python/java版本范围，安装回执记录最终精确版本。artifacts每项含id、role、digest、sizeBytes、mediaType、os、arch。role为payload、source_snapshot、lockfile。kind专属段分别为plugin、skill、tool。tool.delivery按online、offline或git分支校验。

plugin段含packageName、dshVersionRange。skill段含entry=SKILL.md、contentProfile=text-only、dshVersionRange。offline工具含平台入口，git工具含固定仓库commit、源码/锁文件引用、packageManager、installScripts=false和入口。入口为相对工具根的path和argv数组，执行器直接启动进程，避免shell拼接。

JSON Schema草案在 `contracts/resource/package.schema.json`，根引用Manifest，定义PluginManifest、SkillManifest、OnlineManifest、OfflineManifest、GitManifest及Envelope。OpenAPI的UnifiedManifest与UnifiedEnvelope引用这一来源。

### 哈希与独立签名

清单放在被引用制品之外，防止自引用哈希。新UnifiedVersion.digest是清单原始UTF-8字节SHA-256，每个artifact.digest是制品全部原始字节SHA-256。signature字段为 `{ "mode": "detached", "algorithm": "Ed25519" }`，实际kid/value存独立发行声明。服务端保留原字节用于下载与重签，JSON键重排、文本换行和重打归档都会改变摘要。

上传使用multipart，metadata含version、expectedDigest和可选changeNotes，manifest为原始清单文件，artifacts为与清单数组顺序一致的二进制数组。在线工具提交空artifacts。全部制品核验通过后，事务登记不可变版本及引用。离线导出可把清单、发行声明和 `artifacts/<sha256-hex>` 放在同一传输容器，消费端逐项核验内部引用。

统一发行声明schemaVersion=2，statement.purpose=resource，绑定kind、name、version、manifestDigest、artifactDigests、issuedAt，外层绑定tenantId、packageId、versionId、releaseSequence、notAfter与signature。artifactDigests按去重后字典序排列并与清单精确相符。完整对象去除顶层signature后做RFC8785 JCS和Ed25519签名。

沿用独立发行/撤销/降级密钥根、高水位、UTC原时间文本和最长24小时有效期。新版本撤销以versionId+manifestDigest定位，恶意制品需查询所有引用版本逐一撤销。旧插件撤销仍匹配原归档摘要。消费者按schemaVersion与purpose选择协议，两种签名协议在迁移期共存。

### 校验和预算

清单最多256 KiB、32个制品、8条runtime需求、32个工具/域名声明。每制品最多512 MiB，单次上传总量最多1 GiB。通用归档解压最多2 GiB、20000条目、压缩比100、深度16。插件沿用原64 MiB压缩/256 MiB解压/4096条目更小上限，skill沿专项上限。接收与解压流式累计预算。

归档拒绝绝对路径、点段、链接、设备、重复条目、Windows保留名、尾点空格、大小写和Unicode规范化碰撞。二进制工具只给声明入口执行权限。JSON清单拒绝重复键、无效UTF-8和未知字段。服务端还须校验artifact id唯一、平台目标唯一、入口存在、版本一致、引用角色匹配、runtime范围可解析和实际兼容性。清单的工具/数据/网络声明须经过独立授权才转为可用权限。

## 3. 工具三种交付方式

### 在线和云端工具

delivery=online，声明HTTP/HTTPS页面或远程MCP端点、protocol为web/mcp、authentication为认证模式。客户端提供添加、打开或连接，artifacts与runtimes为空。默认推荐HTTPS，内网IP/HTTP部署沿现有访问授权与明文传输提示。凭据保存在凭据服务，清单仅含认证模式。MCP通过既有平台远程网关或公开远程能力接入，成员与会话身份来自可信上下文。

审核至少R2，核对运营主体、域名、重定向、授权范围、MCP工具清单与隐私说明。总部代访问逐次校验DNS目标与重定向，阻止metadata地址、回环和越权内网。成员内网端点依现有连接授权策略登记。页面采用独立来源，端点拒绝userinfo和带凭据查询。签名覆盖端点配置，运营期按周期复核，域名、认证域或工具清单变化触发重新审核。

### 离线预编译包

delivery=offline，每个平台发布payload和入口。客户端匹配os/arch，下载校验后装进受管目录。平台矩阵首期沿Linux/Windows、amd64/arm64，实际开放依赖构建与真实运行验证。工具可自包含，或声明受管runtime。

默认R2并审查构建来源、依赖、许可证和扫描结果。涉及特权或广泛敏感数据时升R3。进程沿成员身份及显式环境允许表运行，文件与网络另行授权。需要系统驱动或全局配置的安装动作进入专门运维授权流程。

### Git源码工具

delivery=git，repository为去凭据HTTPS地址，commit为完整40或64位十六进制对象ID。审核时平台取得精确commit，存不可变源码快照、依赖锁文件和来源证明。sourceArtifactId与lockArtifactIds解析到source_snapshot与lockfile制品。签名通过清单和制品摘要覆盖commit、快照、锁文件与runtime范围。默认拒绝浮动子模块、未解析LFS指针与浮动下载，审核前补实际字节或完整固定引用。

源码工具按R3审核，包含代码和构建行为。默认禁生命周期安装脚本。Node使用精确锁和禁脚本安装，Python优先带hash锁的预构建wheel，Java优先固定依赖和已核验构建产物。Python构建后端及Maven/Gradle插件可执行代码，需要构建时转隔离构建任务，采用已审核入口和网络允许表。构建结果登记为派生制品并签来源证明，客户端校验源码/锁文件与构建结果关系。

每工具版本独享node_modules、venv或classpath。下载缓存可共享，运行依赖不能引用另一工具的可写目录。依赖准备失败保持旧激活版本。仓库新增commit形成新版本重新送审。私有仓库凭据只授予受限抓取任务，日志脱敏。

## 4. 客户端runtime管理

`clients/installer/upstream.json`目前固定Node 24.21.0和DSH 0.2.1-rc.3，应用组件在安装根versions目录，用户数据在独立DataHome。Windows默认 `%LOCALAPPDATA%\AurigaDesktop\home`。runtime管理复用安装核心的事务、所有权标记、来源校验与崩溃恢复。

规划 `<DataHome>/auriga/runtimes/<kind>/<精确版本>/<os>-<arch>/`，每项runtime带平台签名、来源、许可证、归档摘要、文件清单和兼容性元数据。Node可导入随包已验证副本，或登记受保护安装版本引用。后者须参与应用升级/回滚清理的引用核对，避免删除正在使用的Node。

Python采用python-build-standalone，Java采用Eclipse Temurin 21 LTS，均经平台审核、签名后按需下载。Node复用Auriga Desktop自带的受管版本。Python固定精确版本、发行构建标识与平台，Temurin固定21 LTS的精确更新版本和构建号，发布记录保存来源、许可证与摘要。runtime范围解析为平台目录中允许的精确版本，下载与解包校验完成后写回执。多个工具共享只读runtime，依赖目录独立。回执同时记录安装版本、回滚保留、开发者固定引用和活进程租约。GC在锁内删前复核，下载/解包失败保留已有runtime。离线使用已缓存且仍可信的runtime和依赖。

普通模式采用平台维护的兼容版本和安全更新策略，新runtime先安装再逐工具切换，保留有效旧版回滚。开发者可显式选择自装runtime的绝对路径并查看实际版本。进程工作目录与环境允许表来自工具安装记录，平台访问令牌不传给工具。

## 5. 普通与开发者模式

受管资源状态位于 `<DataHome或DSH_HOME>/auriga/resources/<tenant>/<owner>/<client或instance>/`，内部按kind/packageId/versionId分安装记录，缓存按摘要引用。浏览器只提交标识和动作，目录根来自可信配置。

安装流程依次完成精确选择、实时授权、验签/撤销检查、摘要核验、暂存解包、语义校验、runtime和依赖准备、原子激活、宿主观察、回报。ETag与安装修订防并发覆盖，幂等键绑定请求指纹。更新锁固定版本，降级沿独立授权并保留高水位，半完成切换通过事务日志恢复。卸载仅清理归属标记匹配的文件。

普通模式面向小白，提供已审核平台签名的资源，一键添加/安装、更新、关闭和卸载。显示变化、风险、授权及实际应用结果。私有skill继续在我的资源中创建，保持“仅当前DSH”的已确认范围。

开发者模式在本机显式开启，允许本地目录/Git侧载、链接调试、日志与runtime选择。界面标“未审核”，本地目录摘要随内容变化，不能显示为平台已审核版本。链接目标限用户选择的目录，运行沿本机工具与数据授权。侧载资源只在当前DSH使用。

一键提交送审先冻结快照，预览全部文件和凭据扫描结果，用户确认后创建共享L3草稿。链接目录后续编辑不影响审核对象。Git工作树未提交变化可先冻结，但正式Git交付要固定为可追溯commit。私有内容留在当前DSH，云端文件进入本人实例备份，管理员默认只看共享内容和授权回报。

## 6. 审核、离线和审计

L0–L4表示治理控制，风险另记R1/R2/R3。R1用于纯资料技能，由至少一名非作者完整审核。R2用于命令型技能、插件、在线工具与普通离线工具，R3用于源码构建、特权与高敏操作，R2/R3均须两名非作者审核且至少一名承担安全审核。审核和发布时由服务端核对人数及职责。新通用共享路径首期只创建L3，既有插件等级通过原契约继续管理。

共享租约截止为发行声明notAfter与撤销清单expiresAt较早值，各自最长24小时。缺失、过期、回滚清单或可信时间不可用时阻止新的受管启动/加载。普通下架阻止新下载/安装/启用，已应用版本在当前租约内保留，续签按下架策略重新授权。

恶意撤销立即禁用受影响资源。插件沿工作空间处置，工具停止受管进程树并撤销凭据，在线工具关闭受管入口和代理授权，外部页面配合服务端会话失效。Skill恶意撤销或租约到期时停止受影响DSH，重新授权并禁用被撤销内容后恢复，进入新会话并保留原历史。共享Skill首期仅在具备可信启动门禁/guardian的云端和Auriga Desktop开放，其他宿主提供私有技能。启动门禁在DSH进程启动前检查信任状态，guardian持续检查有效期，执行能力独立于可卸载的Auriga插件。

审计记录资源/版本/制品摘要、固定commit、审核决策、发行/撤销修订、分发与回报，正文、令牌、私有文件和本机路径按脱敏规则处理。签名、扫描和运行隔离各自记录验证范围。

## 7. 插件映射、迁移成本与契约范围

PR304/308已实现的Package/Version、Review/Distribution、成员安装与节点下载可复用事务、对象暂存/提交、ACL、ETag、幂等、outbox、审核人分离、高水位与审计。需要改造的部位包括kind固定校验、多制品引用、新清单校验、两种摘要/签名、严格plugin客户端解码器和workspace规格。

通用契约使用 `/api/resource/v1/catalog/**` 与 `/api/resource/v1/admin/catalog/**` 的10个路径、12个操作，operationId带Unified前缀。包列举、审核队列和已发布列表支持kind过滤。单对象操作由ID解析kind，并复核版本/审核归属。保留原主干插件接口和schemas的语义。

通用草案覆盖创建/读取资源、版本上传/列举/详情、送审、审核队列、已发布、审核决定、发布和下架。UnifiedManifest与UnifiedEnvelope引用新schema。新Version.digest为清单摘要，旧Version.digest为插件归档摘要，迁移通过来源关联表对应，验证器按协议处理。

迁移依次进行以下工作。

1. 为既有插件生成通用元数据投影，关联原归档artifact和versionId。缺失权限/依赖证据的标待补齐，内容补齐后重新审核。
2. 新增清单与制品引用表和事务，核验旧归档与原审核证据后再签新声明，迁移保留旧撤销事实和安全高水位。
3. 管理侧先读通用投影，写入口切换按package串行，防止新旧流程并发生成不同版本源。客户端plugin仍用公开PluginManager，skill用技能服务/目录，tool用受管执行入口。
4. 扩workspace spec/proto、成员票据与回报、分发、重签和事件到kind/清单/制品引用，支持新协议的消费者部署后再开放相应kind发布。旧读取保留一个发布周期，回滚时关闭新写入口并保留对象和引用。

迁移的主要成本在多制品事务、摘要/签名消费和规格升级，页面与状态机可大量复用。当前为统一契约切片，正式上线前还需补成员下载票据/安装回报、制品下载、草稿恢复、审核详情/历史、runtime目录、构建来源证明、proto和密码学向量。这些接线进入分期契约线。

## 8. 管理端与成员端

管理台沿审核、已发布、分发三个现有页面组成资源中心，按kind、风险、平台、状态筛选。共用名称、版本、作者、审核、可见范围和目标结果。详情按kind展示plugin权限/依赖、skill正文/资料差异、tool端点/平台矩阵/源码commit与锁文件。审核通过、已签发、目标已应用分别显示。

分发页显示当前/待更新版本、位置、失败原因、重试、待重启、已取代、被覆盖和已过期。成员回报保留reportSource语义。成员市场/我的资源共用kind筛选，online工具显示打开/连接，offline与git显示一键安装，开发者模式增加源码、runtime和日志。Auriga插件沿DSH原生UI与主题，独立管理台沿Vue。

## 9. 分期与工作线依赖

### P1 统一规范、插件纳入与Skill接入

以下条目是合并本设计后可分别派发的任务。主控在任务文件中固定共同契约HEAD与允许路径。跨线共享文件由指定负责人串行修改，新增跨服务字段交回P1-K统一生成。每条线交付针对性测试和可部署产物，由主控串行部署到开发环境并从正常页面验证。

依赖顺序为P1-K先完成契约，随后P1-A、P1-B、P1-C和P1-D可并行。P1-D的规格/事件能力必须先于P1-A分发联调和P1-C云端安装验收就绪，P1-D无需先于P1-A目录/存储/审核开发或P1-C私有技能开发全部完成。P1-E与上述实现并行；共享安装对用户开放前，云端须通过D的可信停用验收，Desktop须通过E的可信停用验收。P1-F随可独立验收的产物串行集成，小批部署持续进行，最终全链验收等待各线产物到齐。

#### P1-K 公共契约与生成产物

- 范围为补齐当前契约切片的P1接线，包括通用审核详情/历史与风险审核人数、成员票据/制品下载、安装修订/回报、分发/续签/撤销，以及ResourceRef、实例规格和事件字段。固定kind、清单/制品摘要、精确版本、selectionRevision/generation、租约、报告来源和失败状态。补v2签名正反向向量及v1/v2互拒。
- 涉及目录为 `contracts/openapi/{resource,workspace}.yaml`、`contracts/resource/`、`contracts/proto/auriga/{resource,node,workspace}/v1/`、`contracts/events/`、`contracts/catalog/m3.json`、`contracts/test/`、`services/contracts/`，以及受影响的 `services/{resource,workspace}/api/` 公共端口声明。以新增消息/字段/接口承载通用模型。
- 依赖本稿、Skill专项、`contracts/resource/package.schema.json`、`contracts/resource/signatures.schema.json`、现行OpenAPI、resource/node/workspace proto和事件目录。P1-K须形成可定位的冻结契约提交，各线按此提交消费。
- 验收为schema全量校验、OpenAPI lint、proto lint/兼容性检查、类型生成及类型检查通过，跨租户/错kind/签名/过期/重放与代数负例通过，审核人数与下载权限有机器可校验的表达。
- 文件边界为独占上述公共契约、API端口和相关生成产物，包括 `web/shared/src/generated/`、`clients/dsh-plugin/src/generated/`、`infra/gen/`。其他线只消费这些产物。契约变化由K提交，主控通知消费线更新基线。

#### P1-A resource后端与插件投影迁移

- 范围为通用目录与kind、原字节清单/多制品存储引用、严格语义校验、R1/R2/R3审核、签名发布、分发意图、续签/撤销，以及旧插件投影与新旧写入串行化。资源事务写意图/outbox，消费workspace API完成交接。
- 涉及目录为 `services/resource/impl/` 及该目录的迁移、归档/对象存储、工作流、HTTP与测试。新增迁移保留旧数据、原始插件归档、审核证据、撤销事实与高水位。
- 依赖K冻结的resource OpenAPI、清单/签名schema、ResourceResolution与公共端口、resource事件。目录、上传、审核和投影可先开发，分发联调依赖D的workspace接线。
- 验收为真实PG测试覆盖租户/作者分离、审核人数、幂等/ETag、多制品部分失败回收、不可变发布、旧插件迁移和撤销不可复活。相同选择重试不解析latest。分发202展示已登记，目标代数观察成功后才标applied。
- 文件边界为独占resource impl，API端口变更交K。workspace、PlatformResourceWorkspace适配器和节点执行器归D，管理台归B，成员界面与宿主安装归C。

#### P1-B 管理台资源中心

- 范围为现有审核、已发布和分发三页的统一kind筛选、共享详情、审核人数/职责、Skill安全文本预览/版本差异、插件投影、精确版本分发及下架/撤销反馈。实现中英文文案和服务端冲突后的刷新。
- 涉及目录为 `web/admin/src/views/resources/`、`web/admin/src/views/ResourcesView.vue`、`web/admin/src/admin/resource*`、资源相关路由/导航配置及 `web/admin/test/`。共享路由文件由B单独修改，资源相关修改限定在本任务项。
- 依赖K冻结的resource管理OpenAPI、Problem/权限/step-up及生成类型，功能联调依赖A的管理API和D的分发回报。可先按契约做组件开发。
- 验收为组件测试覆盖kind切换、跨类型详情、R2缺第二人禁止发布、作者自审拒绝、ETag冲突、202与applied区分、撤销原因与敏感再验证。部署后正常管理入口完成上传到审核/发布/分发追踪的实际操作。
- 文件边界为独占admin资源页面和对应路由接线，生成类型交K，成员界面归C，共享组件若需改变先交主控指定唯一负责人。

#### P1-C 成员侧与插件Skill安装

- 范围为资源市场/我的资源按kind与位置展示，私有Skill创建/导入/编辑/关闭/卸载，显式冻结送审，共享Skill验签/票据下载/解包/ledger、customSkillDirs活跃视图、原子更新、同名覆盖与安装回报。插件安装沿公开PluginManager，Skill接入沿公开skills服务和目录。
- 涉及目录为 `web/panel/src/resources.ts`、panel资源/成员页面与测试，`clients/dsh-plugin/src/resources/` 及其测试、宿主原生资源页/设置/插槽接线（`src/settings/` 和 `src/index.js` 的资源部分）。插件共享注册入口由C负责资源部分接线，E通过明确的进程控制端口消费租约状态。
- 依赖K冻结的成员资源API、统一清单/v2签名向量、身份通道和回报契约。私有技能独立于A/D可先验收，共享下载依赖A，云端规格与代数依赖D；共享功能开放分别依赖D证明云端guardian能力、E证明Desktop guardian能力。
- 验收为真实DSH公开目录发现/调用、用户私有内容不上云、共享送审冻结、原始字节验签、同名私有/项目覆盖、跨身份隔离、Windows占用/崩溃恢复、并发修订与回报。云端和Desktop正常页面分别跑安装/更新/卸载与私有创建，收到撤销和离线到期分别触发D、E的停用端口。
- 文件边界为独占panel资源页面和插件资源逻辑。K独占生成代码，E独占安装器和guardian进程生命周期。C不得另写一套guardian或跨目录改节点驱动。

#### P1-D workspace规格与分发事件消费

- 范围为将已锁定kind/manifestDigest/artifact引用写入实例规格，消费资源分发意图并维护selectionRevision与generation，确定性下发、观察回报、续期对账和撤销停机传播。事件生产者由A实现，事件定义归K，D实现workspace消费与节点落地。
- 涉及目录为 `services/workspace/impl/`，`services/platform-app/src/main/java/auriga/platform/` 下的 `PlatformResourceWorkspace.java`、`PlatformResourceConfiguration.java`、`PlatformWorkspaceConfiguration.java` 及对应测试。节点侧限定 `infra/agent/internal/` 下 `reconciler/`、`runtime/`、`renderer/`、`artifacts/`、`state/`、`observer/observer.go` 与对应测试，装配限 `app/app.go` 中资源相关部分。D负责规格消费、资源下载/渲染、应用观察和云端guardian进程控制；observer的CPU/内存采集文件不属于本任务。
- 依赖K冻结的node InstanceSpec/观察消息、ResourceResolution、workspace/resource公共端口、resource事件、v2签名和撤销schema。D可用固定契约fixture验证规格与事件消费，随后接A的真实生产事件。
- 验收为重复/乱序事件不覆盖新选择、同selectionRevision重试锁定字节、generation匹配才applied、签名续期保持身份/摘要/序号、撤销失败可重试且最终停机。Linux/Windows节点分别验证清单过期和恶意撤销、断网到期/重启前门禁，云端恢复新会话。
- 文件边界为独占workspace impl、平台resource/workspace适配器与节点资源观察/执行路径。A只调用公共workspace端口，C消费回报，E负责Desktop安装器。K独占proto、事件、公共API和生成代码。D与E共用的guardian行为协议先在K冻结，两端执行实现各自归属。

#### P1-E Desktop可信启动门禁与guardian

- 范围为独立于可卸载插件的Desktop guardian、启动前信任核验、运行期租约计时、恶意撤销/到期停止受影响DSH、重启恢复门禁和新会话提示。保留独立DSH_HOME、应用升级/回滚和Node引用，不在P1实现Python/Java下载。
- 涉及目录为 `clients/installer/runtime/`、`clients/installer/windows/`、其构建脚本和测试。E负责Desktop guardian入口及进程树控制，云端执行归D。
- 依赖K冻结的租约/撤销/停用状态协议与v2向量、C提供的身份/安装记录观察端口。可与C并行做端口级验证，共享端到端验收等待C真实写入记录。
- 验收为插件关闭/卸载后仍执行已安装共享资源的有效期门禁，进程重启或时钟回拨不绕过信任检查，断网在最早租约截止停DSH，恶意撤销后阻止旧内容恢复。真实Desktop通过正常启动器和页面验证，新会话进入及原历史保留可见。
- 文件边界为独占installer，C独占插件/panel，D独占节点。影响共同信任协议时交K，部署脚本与环境修改交主控。

#### P1-F 串行集成与开发部署

- 范围为主控收取K/A/B/C/D/E产物，组合版本、构建/部署收据与正常入口验证。涉及 `e2e/` 的资源集成用例及 `docs/dev/` 资源使用说明，NAS部署走既有主控部署工具。
- 依赖所有冻结契约和实现产物。验收包括旧插件路径、统一投影、Skill私有/共享、分发/重试、guardian到期和撤销，覆盖云端/Desktop和公开IP/HTTP访问边界。
- 文件边界为各工人只交付可部署产物与证据，主控唯一执行真实PG/DSH全链和NAS部署。独立可验收改动经针对性测试可先小批部署，PR独审与完整CI沿现行合并门禁进行。

P2交付online与offline工具。端点/凭据绑定、平台payload和进程回报契约先行，后端制品/审核、客户端执行/终止、管理/成员页面并行。依赖P1的统一版本与发行模型。代理授权、独立来源页面、进程回收通过正常入口分别验收。

P3交付Git源码工具和Python/Java runtime。commit/快照/锁文件、隔离构建契约先行，平台抓取/构建、客户端runtime/依赖、开发者侧载/日志并行。依赖P2工具生命周期与P1制品签名，验收runtime共享/GC、应用升级/回滚和活进程引用。

每期测跨身份/租户、篡改、过期、撤销、重放、并发、半完成恢复与卸载归属。P3额外测commit移动、锁文件篡改、构建脚本和GC竞争。云端/Desktop通过正常入口实测，各线交主控串行小批部署。真实PG/DSH全链由主控占唯一槽。

## 10. 已决定

用户于2026-11-12确认以下11项全部按推荐执行，作为各工作线实现与验收要求。

- D1，Skill首期采用text-only，禁止附带可执行脚本。工具源码和二进制按tool类型及对应风险等级审核，技能先以原生文本格式接入。
- D2，共享Skill使用独立customSkillDirs活跃根，私有Skill使用原生user根，以便身份切换、到期停用和清理时核对文件归属。
- D3，新增通用共享资源首期为L3，现有插件等级沿原规则迁移。Skill/tool的强制等级留后续独立范围，P1按照当前允许关闭/卸载的模型验收。
- D4，共享资源采用最多24小时租约。Skill恶意撤销或租约到期时停止受影响DSH，恢复后新建会话并保留原历史。实现须明确提示可能中断该DSH其他任务，并验证停机和启动门禁。
- D5，共享Skill首期仅在具备可信guardian的云端和Auriga Desktop开放。其他DSH提供私有技能，开放共享须先具备同等可验证的启动与进程控制能力。
- D6，R1至少一名非作者完整审核，R2/R3至少两名非作者且含安全审核。服务端审核/发布均须强制核对，作者不得自审自发。
- D7，统一清单放在制品之外，使用独立发行声明，复用现有插件归档并保存迁移关联，分别验证清单和制品摘要。
- D8，Node复用Auriga Desktop自带的受管版本。Python采用python-build-standalone，Java采用Eclipse Temurin 21 LTS，两者由平台审核、签名后按需下载。多工具共享只读runtime，工具依赖目录独立，回收核对活进程与回滚引用。
- D9，Git工具锁完整commit，保存源码快照和锁文件，默认禁安装脚本，必要构建在平台隔离环境执行并记录来源证明，保证普通用户安装固定已审核字节。
- D10，开发者模式由本机显式开启，侧载标未审核且仅在当前DSH可用，送审前冻结并预览快照，由用户明确提交共享。
- D11，在线工具配置变化重新审核，远端服务周期复核，域名、认证域和工具清单变化触发复核，保留实际探测与审核证据。
