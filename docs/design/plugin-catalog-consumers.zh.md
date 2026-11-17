# 统一插件目录消费与第三期清理

本文件配合资源包规范第7节。第一期统一目录仅开放 L3 共享 plugin/skill，旧插件 ID 不映射到新目录；tool 的写入继续拒绝。管理台和客户端切换必须消费同一接口，不能给 UnifiedPackage 填入旧 L0–L4 对象。L0 部署来源、L1/L2 强制集合和 L4 私有发行尚须主控决定并补齐契约，补齐前保留旧治理入口。

## 第二期接口清单

所有下列通用接口接受或返回 kind=plugin；按 kind 筛选与单对象归属校验使用服务端真实类型。非上传写请求仍须遵循各操作的 If-Match、Idempotency-Key、权限和近期验证要求。

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/resource/v1/catalog/packages` | `UnifiedListResourcePackages` |
| POST | `/api/resource/v1/catalog/packages` | `UnifiedCreateResourcePackage` |
| GET | `/api/resource/v1/catalog/packages/{packageId}` | `UnifiedGetResourcePackage` |
| GET | `/api/resource/v1/catalog/packages/{packageId}/versions` | `UnifiedListResourceVersions` |
| POST | `/api/resource/v1/catalog/packages/{packageId}/versions` | `UnifiedUploadResourceVersion` |
| GET | `/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}` | `UnifiedGetResourceVersion` |
| POST | `/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/submissions` | `UnifiedSubmitResourceVersion` |
| GET | `/api/resource/v1/admin/catalog/review` | `UnifiedListMarketAdminReview` |
| GET | `/api/resource/v1/admin/catalog/published` | `UnifiedListMarketAdminPublished` |
| POST | `/api/resource/v1/admin/catalog/reviews/{reviewId}/decisions` | `UnifiedDecideResourceReview` |
| POST | `/api/resource/v1/admin/catalog/packages/{packageId}/versions/{versionId}/publications` | `UnifiedPublishResourceVersion` |
| POST | `/api/resource/v1/admin/catalog/packages/{packageId}/versions/{versionId}/withdrawals` | `UnifiedWithdrawResourceVersion` |
| GET | `/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/manifest` | `UnifiedDownloadResourceManifest` |
| GET | `/api/resource/v1/admin/catalog/reviews/{reviewId}` | `UnifiedGetResourceReview` |
| POST | `/api/resource/v1/catalog/member/download-tickets` | `UnifiedCreateDownloadTicket` |
| POST | `/api/resource/v1/catalog/member/downloads` | `UnifiedDownloadArtifact` |
| POST | `/api/resource/v1/catalog/member/installations` | `UnifiedSelectInstallation` |
| GET | `/api/resource/v1/catalog/member/installations` | `UnifiedListInstallations` |
| POST | `/api/resource/v1/catalog/member/installation-reports` | `UnifiedReportInstallation` |
| POST | `/api/resource/v1/catalog/member/renewals` | `UnifiedRenewInstallation` |
| POST | `/api/resource/v1/admin/catalog/distributions` | `UnifiedCreateDistribution` |
| GET | `/api/resource/v1/admin/catalog/distributions` | `UnifiedListDistributions` |
| GET | `/api/resource/v1/admin/catalog/distributions/{distributionId}` | `UnifiedGetDistribution` |
| GET | `/api/resource/v1/catalog/packages/{packageId}/versions/{versionId}/artifacts/{artifactId}` | `UnifiedDownloadVersionArtifact` |
| GET | `/internal/resource/v1/catalog/downloads/{downloadId}/manifest` | `UnifiedDownloadNodeManifest` |
| GET | `/internal/resource/v1/catalog/downloads/{downloadId}/artifacts/{artifactId}` | `UnifiedDownloadNodeArtifact` |

管理台：创建包、multipart 上传、送审、审核队列/详情、general/security 决定、发布、已发布列表、下架/恶意撤销、分发及结果追踪共用一个来源。插件最低 R2，两名不同非作者且含安全审核，发布者不得是作者；治理 level 与风险 risk 分别展示。

客户端：先选择精确 identity，再按安装修订取得票据、下载原始清单和 payload，核对 v2 签名、原始字节摘要、撤销及租约。清单 plugin.packageName 是 npm 原名，目录 name 只是资源名，不能互换。下载票据不进 URL/事件，locator 不授予权限。调用 DSH 公开 PluginManager / dsh plugin add <tgz>；不要把插件按 SKILL.md 解包或扫描。保持安装 CAS、selectionRevision、generation、报告来源，202/applying 只表示意图，真实宿主观察后才能报 applied。

云端：resource 产生 UnifiedResourceRef 与 resource.catalog_distribution_requested，workspace 规格规则接受 plugin；现有 kind 为 string，无需增加 proto 字段。本期不实现节点/DSH 的插件执行分支，第二期必须接好公开安装执行、观察、撤销停用和 guardian，并经正常页面验收后再开放用户入口。

第二期必须将 `clients/dsh-plugin/src/resources/schema-data.js` 改为引用 `../generated/resource-schema-data.js`，或同步该旧快照；本期禁止修改前者，因此现有插件快照一致性测试会失败。新快照由 `node contracts/resource/generate-runtime-schemas.mjs` 生成，`--check` 检查漂移。

生成来源：OpenAPI 与 contracts/resource/{package,unified}.schema.json；web/shared/src/generated/{resource,shell-auth,workspace}.ts，clients/dsh-plugin/src/generated/resource.d.ts，infra/gen/{resourceapi,shellauthapi}。shell-business.schema.json 由 contracts/plugin/generate-shell-business.mjs 生成，禁止消费者手改投影。

## 第三期待清理清单

- 旧 /api/resource/v1/packages/**、旧 admin/review、admin/published、admin/packages/**、旧 member 安装/票据/下载/续签/回报与 internal 插件下载路径；具体以旧 OpenAPI operationId 和路由为准。
- services/resource 的旧 ResourceWorkflow/ResourceMembers/ResourceDistributions/ResourceCommands/ResourceQueries 业务和控制器。CatalogWorkflow/CatalogDelivery 当前仍复用旧类里的事务、幂等、审计和撤销能力，须先抽出共用能力，不能直接整类删除。
- resource_package/resource_version 及旧审核、分发、成员安装/票据、下载授权表；catalog_plugin_projection、catalog_migration_proof 等未使用迁移结构。保留统一目录引用的对象与共用撤销事实、租约、高水位、幂等和审计能力，逐项核对引用后以新增迁移删除。
- resource.version_* 与旧 distribution 事件、旧 PluginRef/v1 签名发行分支、旧 OpenAPI/schema/proto 和所有受影响生成代码；统一 v2 与共用撤销签名不能误删。
- UnifiedMigrationProof/Marker 与跨协议摘要别名、v1 续签截止等已取消迁移机制的 schema/向量/测试助手。第一期不生产这些记录，清理时同步全部消费者和生成器。
- 管理台旧插件数据源、页面拼接和分流；DSH/云端旧下载与安装调用；旧插件种子发布路径。统一 dsh-memwiki 种子见 seed-resources/README.md。

删除前必须验收原治理能力在统一入口的替代实现、旧调用方全部切换、开发数据重灌、plugin/skill 完整安装与撤销停用。部署及真实 DSH/页面验收由主控串行执行。
