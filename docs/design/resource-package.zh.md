# 统一资源包规范草案

本稿覆盖插件、skill和工具，skill作为首个按本规范接入的新类型。状态为设计待用户审阅，依据主干 `0f7b2712329b9416260e755803dcb182a4ad1a5e` 和第1轮 `d6bde342`。现有实现依据为资源契约、services/resource、PR304管理台和PR308成员安装链路，客户端布局依据clients/installer。Skill原生格式与加载详见专项文档。[Skill类型专项](skill-market.zh.md)

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

Python/Java按需下载平台审核的runtime。版本范围解析为允许的精确版本，下载与解包校验完成后写回执。多个工具共享只读runtime，依赖目录独立。回执同时记录安装版本、回滚保留、开发者固定引用和活进程租约。GC在锁内删前复核，下载/解包失败保留已有runtime。离线使用已缓存且仍可信的runtime和依赖。

普通模式采用平台维护的兼容版本和安全更新策略，新runtime先安装再逐工具切换，保留有效旧版回滚。开发者可显式选择自装runtime的绝对路径并查看实际版本。进程工作目录与环境允许表来自工具安装记录，平台访问令牌不传给工具。

## 5. 普通与开发者模式

受管资源状态位于 `<DataHome或DSH_HOME>/auriga/resources/<tenant>/<owner>/<client或instance>/`，内部按kind/packageId/versionId分安装记录，缓存按摘要引用。浏览器只提交标识和动作，目录根来自可信配置。

安装流程依次完成精确选择、实时授权、验签/撤销检查、摘要核验、暂存解包、语义校验、runtime和依赖准备、原子激活、宿主观察、回报。ETag与安装修订防并发覆盖，幂等键绑定请求指纹。更新锁固定版本，降级沿独立授权并保留高水位，半完成切换通过事务日志恢复。卸载仅清理归属标记匹配的文件。

普通模式面向小白，提供已审核平台签名的资源，一键添加/安装、更新、关闭和卸载。显示变化、风险、授权及实际应用结果。私有skill继续在我的资源中创建，保持“仅当前DSH”的已确认范围。

开发者模式在本机显式开启，允许本地目录/Git侧载、链接调试、日志与runtime选择。界面标“未审核”，本地目录摘要随内容变化，不能显示为平台已审核版本。链接目标限用户选择的目录，运行沿本机工具与数据授权。侧载资源只在当前DSH使用。

一键提交送审先冻结快照，预览全部文件和凭据扫描结果，用户确认后创建共享L3草稿。链接目录后续编辑不影响审核对象。Git工作树未提交变化可先冻结，但正式Git交付要固定为可追溯commit。私有内容留在当前DSH，云端文件进入本人实例备份，管理员默认只看共享内容和授权回报。

## 6. 审核、离线和审计

L0–L4表示治理控制，风险另记R1/R2/R3。建议R1用于纯资料技能，由至少一名非作者审核。R2用于命令型技能、插件、在线工具与普通离线工具，R3用于源码构建、特权与高敏操作，R2/R3建议两名非作者且含安全审核。新通用共享路径首期只创建L3，既有插件等级通过原契约继续管理。

共享租约截止为发行声明notAfter与撤销清单expiresAt较早值，各自最长24小时。缺失、过期、回滚清单或可信时间不可用时阻止新的受管启动/加载。普通下架阻止新下载/安装/启用，已应用版本在当前租约内保留，续签按下架策略重新授权。

恶意撤销立即禁用受影响资源。插件沿工作空间处置，工具停止受管进程树并撤销凭据，在线工具关闭受管入口和代理授权，外部页面配合服务端会话失效。Skill已有正文处置见专项。可信启动门禁/guardian负责撤销与离线期限，Skill整DSH停止行为作为D4/D5待审阅建议。

审计记录资源/版本/制品摘要、固定commit、审核决策、发行/撤销修订、分发与回报，正文、令牌、私有文件和本机路径按脱敏规则处理。签名、扫描和运行隔离各自记录验证范围。

## 7. 插件映射、迁移成本与契约范围

PR304/308已实现的Package/Version、Review/Distribution、成员安装与节点下载可复用事务、对象暂存/提交、ACL、ETag、幂等、outbox、审核人分离、高水位与审计。需要改造的部位包括kind固定校验、多制品引用、新清单校验、两种摘要/签名、严格plugin客户端解码器和workspace规格。

本轮把第1轮10个Skill草案路径改为 `/api/resource/v1/catalog/**` 与 `/api/resource/v1/admin/catalog/**`，12个操作使用Unified前缀。包列举、审核队列和已发布列表支持kind过滤。单对象操作由ID解析kind，并复核版本/审核归属。保留原主干插件接口和schemas的语义。

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

P1交付统一规范、插件纳入和skill接入。契约线先定清单/发行schema、向量、API、事件和workspace引用。后端存储/工作流、DSH适配/安装器随后并行，管理/成员页面基于同一契约并行。集成依赖签名验证、ACL、撤销传播和宿主能力，先做通用只读投影与私有skill小批，再开放共享安装。

P2交付online与offline工具。端点/凭据绑定、平台payload和进程回报契约先行，后端制品/审核、客户端执行/终止、管理/成员页面并行。依赖P1的统一版本与发行模型。代理授权、独立来源页面、进程回收通过正常入口分别验收。

P3交付Git源码工具和Python/Java runtime。commit/快照/锁文件、隔离构建契约先行，平台抓取/构建、客户端runtime/依赖、开发者侧载/日志并行。依赖P2工具生命周期与P1制品签名，验收runtime共享/GC、应用升级/回滚和活进程引用。

每期测跨身份/租户、篡改、过期、撤销、重放、并发、半完成恢复与卸载归属。P3额外测commit移动、锁文件篡改、构建脚本和GC竞争。云端/Desktop通过正常入口实测，各线交主控串行小批部署。真实PG/DSH全链由主控占唯一槽。

## 10. 待用户决定

- D1建议skill首期text-only，工具源码/二进制按各自风险审核。备选同期开放skill脚本，需要新增运行授权和隔离设计。
- D2建议skill共享用独立customSkillDirs活跃根，私有用原生user根，便于切换和到期处置。备选合放user根，接管清理更易影响用户文件。
- D3建议通用共享首期L3，旧插件等级沿原规则迁移。备选同期给skill/tool强制等级，需解决项目覆盖与进程处置。
- D4建议最多24小时租约，Skill恶意撤销/超期时停止受影响DSH，恢复后新建会话。代价是中断其他任务。备选仅阻断新加载，需要明确上下文残留与到期边界。
- D5建议共享Skill先在具备可信guardian的云端/Desktop开放。备选外部DSH同期开放，须明确可验证的启动与进程控制方式。
- D6建议R1一名非作者，R2/R3两名且含安全审核。备选全部双人，运营成本更高。
- D7建议清单外置与独立发行声明，复用既有插件归档。备选重封装全部插件，增加下载和迁移验证成本。
- D8建议复用受管Node，Python/Java按需下载平台审核runtime，独立工具依赖。Python发行版、Java发行商/LTS版本需用户结合体积和许可证选择。备选只用用户自装runtime，一键体验受限。
- D9建议Git锁commit、存快照/锁文件，默认禁脚本，必要构建在平台隔离执行。备选本机构建保留开发者入口，需逐次明确代码/网络授权。
- D10建议开发者模式本机显式开启，侧载显示未审核，冻结后显式送审。备选团队共享调试资源，需要补跨成员未审核代码授权。
- D11建议在线配置变更重审，远端服务周期复核并在域名/认证域/工具清单变化时复核。备选仅首审URL，后续变化证据较少。

上述推荐待用户审阅后固化为实现要求。
