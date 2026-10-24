# 管理台资源治理（ADMINR1）

## 设计与本批闭环

界面依据管理台 HTML 原型的 `resourcesPage`、`resourceDetail`，以及需求完整性复核 F01/F12：紧凑工具条与表格、右侧 600px 详情抽屉，详情中先展示版本历史及固定版本，再展示包元数据。八组主导航保留概览、资源治理、成员与权限、运行环境、模型与用量、安全与接入、平台运维、审计日志；已有成员、邀请、分组、角色、配额、实例、节点深链保持可用。

资源目录只查询当前账号获授权返回的包，没有管理员私有资源旁路。包启用状态、L0–L4 等级、可见范围、版本审核/发布状态分别呈现，不以包创建成功冒充版本发布。列表搜索、等级与范围筛选仅作用于当前页，页面明确标注；服务端只收到实际支持的 limit/cursor，游标原样编码，不推导总量。

真实闭环：包列表 → 包详情抽屉 → 版本列表 → 固定 versionId 详情；创建或修改包元数据 → 确认 → 写前重读权限 → 正式写入 → GET 读回 → 更新列表/详情。版本不存在时显示空态，不伪造变更、文件、权限声明或审核记录。

## 接口与边界

- `GET /api/resource/v1/packages?limit=50&cursor=...`
- `GET /api/resource/v1/packages/{packageId}`
- `GET /api/resource/v1/packages/{packageId}/versions?limit=50&cursor=...`
- `GET /api/resource/v1/packages/{packageId}/versions/{versionId}`
- `POST /api/resource/v1/packages`：JSON，正式 PackageInput（name、kind=plugin、visibility、groupIds）。
- `PATCH /api/resource/v1/packages/{packageId}`：JSON，正式 PackagePatch，仅发送变化字段，携带详情 GET 原始强 ETag 的 If-Match。不能猜 revision 代替响应头，也不使用 merge-patch 内容类型。

类型直接引用仓库现有 generated resource 类型（仅类型导入；shared 当前未从入口导出它们），没有复制协议 DTO、修改生成物或依赖配置。管理台 API 内提供本页专用 GET 响应头读取：每次调用独立读客户端，沿用既有同源、cookie、语言与 401 登录跳转，互不串 ETag；写入仍由既有 apiClient 执行 CSRF。未修改共享客户端或公共授权 helper。

读权限为 `resource.packages.read`；元数据写权限为 `resource.packages.upload`。写前查询 `/api/org/v1/me`，前端仅向包所有者提供 L3/L4 修改入口，服务端继续执行所有者、等级、租户、ACL、近期二次验证和个人插件策略。L4 范围固定 private；不开放等级、owner、tenant、enabled、默认版本编辑。没有近期验证时显示明确提示，要求完成既有管理员登录验证后返回，不自动重试或绕过门锁。

新分组范围只能从正式组列表选择且需要 `org.groups.read`，不能输入任意 UUID。原有组范围未修改时可随名称修改保留而不查询组目录；服务端仍复核当前组成员资格。

创建意图的幂等键及请求体仅存组件内存；创建结果不确定时，表单锁定为原请求，显式重试仍使用同一 key/body。关闭表单后可继续原意图；超过 24 小时阻止重放并要求先核对目录和记录。离开页面、重新加载会丢失此内存意图，不能承诺跨页面恢复或引导盲目再次创建。

412 保留修改草稿并禁用直接保存，只能显式确认“重新加载并放弃草稿”后重新比较、重新确认；不悄悄获取新 ETag 覆盖。成功写入后的读取失败只重试 GET，包抽屉内也提供重试入口，不重放 PATCH/POST。

## 失败与未开放流程

加载、空目录、缺版本、ACL/租户 404、服务未启用或不可用 503、403、401、撤权、会话主体变化、取消、防重复和晚到响应有明确处理。读权丢失清除列表、抽屉、版本与编辑草稿；卸载使未完成响应失效。错误不回显任意服务端 detail。

本批不是完整的资源发布交付：归档上传、摘要与 manifest 核验、送审/驳回、固定版本审核、签名发布、下架/恶意撤销、分发目标与节点应用反馈仍待后端流程接线。相应原型入口明确标注未开放，不发起不存在的请求、不用示例数据模拟成功。文件清单/变更说明/权限声明/审核记录同样列为查询能力缺口。

现场终端、独立总部系统 DSH、模型中转站/Auto/模型 API Key、通知/备份/审计等原型核心入口保留，但本批没有实现它们的业务流程；总部入口不能转到管理员的个人实例。Skills/工具托管没有被当作已有 plugin 安装能力。系统设置任务 ADMIN6 继续暂停。
