export const resourceZh = {
  resourceIntentExpired:
    '当前创建意图已超过 24 小时，不能安全重放。请先检查正式资源列表与管理员记录，确认结果后再重新进入页面。',
  identityProviders: '身份源',
  systemSettings: '系统设置',
  governance: '资源治理',
  resourceCatalog: '资源目录',
  resourceReview: '审核队列',
  resourcePublished: '已发布资源',
  resourceDistribution: '分发策略',
  resourceTools: '工具托管',
  resourceBoundary:
    '当前可管理包元数据和查询已有版本。上传、审核、发布、下架和分发尚未开放；创建包不代表资源已上架。',
  resourceScope:
    '仅列出当前账号获授权的资源；管理员不自动获得全部私有资源的读取权。',
  resourceSearch: '搜索当前页名称或所有者 ID',
  resourceCurrentPage: '筛选仅作用于当前页；不代表全目录搜索或总量。',
  resourceAll: '全部',
  resourceLevel: '分发等级',
  resourceVisibility: '可见范围',
  resourceOwner: '所有者 ID',
  resourceEnabled: '包启用状态',
  resourceDefault: '默认版本 ID',
  resourceNoDefault: '未指定默认版本，不代表已发布',
  resourceCreate: '创建包元数据',
  resourceEdit: '修改包元数据',
  resourceNew: '新建插件包',
  resourceMetadata: '包元数据',
  resourceVersions: '版本历史',
  resourceVersion: '固定版本详情',
  resourceVersionLabel: '资源版本',
  resourceNoVersions: '暂无可读取的版本；上传与发布尚未开放。',
  resourceVersionBoundary:
    '版本状态来自服务端。变更说明、文件清单、权限声明和审核记录尚无可用查询接口；未提供不等于已通过核验。',
  resourceNoVersion: '选择一个服务端返回的固定版本查看详情。',
  resourceDigest: '内容摘要',
  resourceSize: '归档大小（字节）',
  resourceRevision: '修订号',
  resourceRelease: '发行序号',
  resourceCreated: '创建时间',
  resourceType: '类型',
  resourceNameInvalid:
    '名称需为 1–214 个 Unicode 字符，不能包含空字符或无效代理字符。',
  resourceGroupsInvalid:
    '请选择正式组列表中的至少一个分组（最多 200 个）；服务端仍会校验你的组成员资格。',
  resourceGroups: '可见分组',
  resourceGroupsRead:
    '选择新分组需要 org.groups.read；现有未变更范围可以保留。',
  resourceGroupsKeep: '保留原分组范围',
  resourceGroupsChange: '重新选择分组',
  resourceOwnerOnly:
    '只有包所有者可修改 L3/L4 元数据；L4 保持私有。等级、所有者、启用状态与默认版本由独立服务端流程管理。',
  resourceEtag: '服务端未提供有效强 ETag，无法安全修改。请重新读取详情。',
  resourceConflict:
    '数据已变化（412）。草稿已保留；请明确重新加载并放弃草稿，再比较并重新确认，不能直接覆盖。',
  resourceReload: '重新加载并放弃草稿',
  resourceReloadConfirm:
    '放弃当前元数据草稿并重新读取服务端版本？此操作不会提交修改。',
  resourceCreateConfirm:
    '确认创建插件包元数据：{changes}？这不会上传、审核、发布或安装资源；等级及归属由服务端决定。',
  resourceEditConfirm:
    '确认修改「{name}」的包元数据：{changes}？将影响该包的名称或可见范围，不发布版本、不改变已安装内容。',
  resourceChanged: '{field}：{before} → {after}',
  resourceSaved: '包元数据已保存；尚未执行上传、审核或发布。',
  resourceReadback:
    '写请求已成功，但读取最新状态失败。请重试读取，不要重复保存。',
  resourceRetryRead: '重试读取',
  resourceUncertain:
    '创建结果尚未确认。当前创建意图和幂等键保留在页面内，重试会使用同一请求；请勿刷新页面后盲目再次创建。',
  resourceIntent: '继续确认原创建请求',
  resourceStepup:
    '需要近期二次验证。请通过现有管理员登录流程完成验证后返回，再确认原操作；本页不会绕过验证。',
  resourceUnavailable:
    '资源服务尚未启用或暂不可用（503），请联系平台管理员检查服务状态。',
  resourceMissing:
    '资源不存在或当前账号不可见（404）；不能据此推断其他租户或私有资源。',
  resourceReadDenied: '没有资源读取权限，请联系管理员。',
  resourceRawUnknown: '—',
  resourceVisibilityLabels: {
    tenant: '本租户用户（含外部成员）',
    members: '正式成员',
    groups: '指定分组',
    private: '仅本人私有',
  },
  resourceStateLabels: {
    draft: '草稿',
    pending_review: '待审核',
    approved: '待发布',
    rejected: '已驳回',
    published: '已发布',
    deprecated: '已弃用',
    withdrawn: '已下架',
  },
  navOverview: '概览',
  navMembers: '成员与权限',
  navRuntime: '运行环境',
  navModels: '模型与用量',
  navSecurity: '安全与接入',
  navOps: '平台运维',
  navAudit: '审计日志',
  navConsole: '管理控制台',
  navControl: '总部控制面',
  navDevices: '现场终端',
  navHeadquarters: '总部 Agent · 系统 DSH',
  navRelay: '中转站',
  navProvidersModels: '提供商与模型',
  navAuto: 'Auto 选模',
  navUsage: '用量',
  navKeys: '模型 API Key',
  navCredentials: '插件接入凭据',
  navEntries: '访问入口',
  navEgress: '出网策略',
  navAlerts: '告警与异常',
  navUpgrades: '版本与升级',
  navBackups: '备份与恢复',
  navLifecycle: '成员离职',
  navNotifications: '通知公告',
  navNotOpen: '此流程尚未开放',
  navNotOpenDetail:
    '此入口保留原型要求的业务位置，尚未接通可操作的后端流程，不显示示例统计或模拟成功。',
  navHeadquartersDetail:
    '总部必须使用独立系统 DSH、授权入口与审计；不能使用当前管理员的个人实例代替。',
  navModelsDetail:
    '中转站配置、Auto 与外部模型凭据仍需各自正式能力接线；当前入口不发起模型调用或签发凭据。',
  navOverviewDetail:
    '待办、健康与总部入口等待正式汇总接线；当前没有可核实的汇总统计。',
};
export const resourceEn = {
  resourceIntentExpired:
    'This creation intent is older than 24 hours and cannot safely be replayed. Check the official catalog and administrator records before reopening the page.',
  identityProviders: 'Identity providers',
  systemSettings: 'System settings',
  governance: 'Resource governance',
  resourceCatalog: 'Package catalog',
  resourceReview: 'Review queue',
  resourcePublished: 'Published resources',
  resourceDistribution: 'Distribution',
  resourceTools: 'Tool hosting',
  resourceBoundary:
    'Package metadata and existing versions are available. Upload, review, publication, withdrawal and distribution are not open. Creating a package does not publish a resource.',
  resourceScope:
    'Only resources authorized for this account are listed. Administrators do not automatically receive access to every private package.',
  resourceSearch: 'Search names or owner IDs on this page',
  resourceCurrentPage:
    'Filters apply to this page only, not the full catalog or a total count.',
  resourceAll: 'All',
  resourceLevel: 'Distribution level',
  resourceVisibility: 'Visibility',
  resourceOwner: 'Owner ID',
  resourceEnabled: 'Package enabled',
  resourceDefault: 'Default version ID',
  resourceNoDefault:
    'No default version assigned; this does not indicate publication',
  resourceCreate: 'Create package metadata',
  resourceEdit: 'Edit package metadata',
  resourceNew: 'New plugin package',
  resourceMetadata: 'Package metadata',
  resourceVersions: 'Version history',
  resourceVersion: 'Fixed version detail',
  resourceVersionLabel: 'Resource version',
  resourceNoVersions:
    'No readable versions. Upload and publication are not open.',
  resourceVersionBoundary:
    'Version states come from the server. Change notes, file lists, permission declarations and review records do not yet have available read endpoints. Missing data does not mean verification passed.',
  resourceNoVersion: 'Select a server-returned fixed version to inspect.',
  resourceDigest: 'Content digest',
  resourceSize: 'Archive size (bytes)',
  resourceRevision: 'Revision',
  resourceRelease: 'Release sequence',
  resourceCreated: 'Created',
  resourceType: 'Type',
  resourceNameInvalid:
    'Use 1–214 Unicode characters without NUL or invalid surrogate characters.',
  resourceGroupsInvalid:
    'Select 1–200 groups from the official list. The server still checks your group membership.',
  resourceGroups: 'Visible groups',
  resourceGroupsRead:
    'Selecting new groups requires org.groups.read. An unchanged existing scope can be preserved.',
  resourceGroupsKeep: 'Keep existing groups',
  resourceGroupsChange: 'Select groups again',
  resourceOwnerOnly:
    'Only the owner can edit L3/L4 metadata. L4 remains private. Level, owner, enabled state and default version use separate server-controlled workflows.',
  resourceEtag:
    'The server did not provide a valid strong ETag. Reload details before editing.',
  resourceConflict:
    'The resource changed (412). Your draft is retained. Explicitly reload and discard the draft, compare and confirm again; overwriting is blocked.',
  resourceReload: 'Reload and discard draft',
  resourceReloadConfirm:
    'Discard the metadata draft and reload the server version? This does not submit a change.',
  resourceCreateConfirm:
    'Create plugin package metadata: {changes}? This does not upload, review, publish or install a resource. The server decides level and ownership.',
  resourceEditConfirm:
    'Update metadata for “{name}”: {changes}? This changes the package name or visibility, without publishing a version or changing installed content.',
  resourceChanged: '{field}: {before} → {after}',
  resourceSaved:
    'Package metadata saved. No upload, review or publication was performed.',
  resourceReadback:
    'The write succeeded, but reading the latest state failed. Retry reads instead of saving again.',
  resourceRetryRead: 'Retry reads',
  resourceUncertain:
    'The creation result is unconfirmed. This page retains the intent and idempotency key; retry uses the same request. Do not blindly create again after refreshing the page.',
  resourceIntent: 'Continue the original creation request',
  resourceStepup:
    'Recent second-factor verification is required. Complete verification through the existing administrator sign-in flow, return and confirm the original operation. Verification is never bypassed.',
  resourceUnavailable:
    'The resource service is not enabled or is temporarily unavailable (503). Ask a platform administrator to check it.',
  resourceMissing:
    'Resource missing or not visible to this account (404). This reveals no other tenant or private resource.',
  resourceReadDenied:
    'Resource read permission is required. Contact an administrator.',
  resourceRawUnknown: '—',
  resourceVisibilityLabels: {
    tenant: 'Tenant users (including external members)',
    members: 'Members',
    groups: 'Selected groups',
    private: 'Owner only',
  },
  resourceStateLabels: {
    draft: 'Draft',
    pending_review: 'Pending review',
    approved: 'Awaiting publication',
    rejected: 'Rejected',
    published: 'Published',
    deprecated: 'Deprecated',
    withdrawn: 'Withdrawn',
  },
  navOverview: 'Overview',
  navMembers: 'Members & permissions',
  navRuntime: 'Runtime',
  navModels: 'Models & usage',
  navSecurity: 'Security & access',
  navOps: 'Platform operations',
  navAudit: 'Audit log',
  navConsole: 'Management console',
  navControl: 'Headquarters control plane',
  navDevices: 'On-site devices',
  navHeadquarters: 'Headquarters Agent · System DSH',
  navRelay: 'Relay',
  navProvidersModels: 'Providers & models',
  navAuto: 'Auto selection',
  navUsage: 'Usage',
  navKeys: 'Model API keys',
  navCredentials: 'Plugin access credentials',
  navEntries: 'Access entries',
  navEgress: 'Egress policy',
  navAlerts: 'Alerts & errors',
  navUpgrades: 'Versions & upgrades',
  navBackups: 'Backup & recovery',
  navLifecycle: 'Offboarding',
  navNotifications: 'Announcements',
  navNotOpen: 'This workflow is not open yet',
  navNotOpenDetail:
    'This entry preserves the workflow in the design. No operational backend is connected; no sample statistics or simulated success are displayed.',
  navHeadquartersDetail:
    'Headquarters requires its own system DSH, authorized entry and audit trail. The administrator’s personal instance cannot substitute for it.',
  navModelsDetail:
    'Relay configuration, Auto and external model keys require their own production integrations. This entry makes no model calls and issues no keys.',
  navOverviewDetail:
    'Tasks, health and the headquarters entry await official aggregation. No verified aggregate statistics are available.',
};
