export const providersZh = {
  uacHelp:
    'UAC 优先验证浏览器 SSO Cookie，无 Cookie 或验证失败时降级扫码。部署开关 auriga.iam.uac.sso-cookie.enabled 默认开启；门户必须处于 Cookie 可读域，支持单 IP、单域名部署。退出后刷新或跨标签页会抑制自动登录，须显式继续。',
  uacPolicy:
    '复验由部署配置 auriga.iam.uac.revalidation 控制：strategy 默认 token-verify，备用 department；interval-seconds 默认 7200，范围 60–86400；retry-interval-seconds 默认 30，范围 5–300；max-retries 默认 3，范围 0–10（首次以外）。均须整数。token-verify 上游故障在预算内重试，明确业务/身份失败或预算耗尽撤销会话；department 单次失败即撤销。登录后一次部门查询仅补充展示，失败不阻断登录。',
  uacTrusted:
    'baseUrl 必须为 HTTPS，无用户信息、查询或片段；主机、端口和解析地址由部署允许列表最终校验，禁止重定向。systemCode 由受信企业适配器解释。',
  uacCreateHelp:
    'UAC 新建默认关闭，只登记名称和排序；创建后在专用配置区填写 baseUrl、systemCode 并启用。编辑名称不会改写配置或启用状态。',
  uacSaveConfirm:
    '确认保存并启用此 UAC 配置？服务端将按允许列表校验，使用当前 ETag 防止覆盖。',
  uacDisableConfirm:
    '确认保存关闭状态？停用将撤销该源挑战、平台会话及关联设备授权，用户需要重新登录。',
  uacReload: '配置版本不可继续使用，请重新读取详情后确认；不会自动重放写请求。',
  uacDiagnostics: 'UAC 诊断',
  uacDiagnosticConfirm:
    '确认更改详细诊断？默认关闭，开启仅影响后续请求，15 分钟自动到期；响应结构仍须脱敏。',
  uacDetailedOn: '详细诊断已开启',
  uacDetailedOff: '详细诊断已关闭或到期',
  uacExpires: '到期时间：',
  uacOpenDetails: '开启详细诊断（15 分钟）',
  uacCloseDetails: '关闭详细诊断',
  uacSince: '最近时间窗（秒，默认 7200，最多 86400）',
  uacExportPermission: '下载还需要 audit.events.read 和 ops.alerts.read 权限。',
  uacExport: '导出诊断包',
  uacExportConfirm:
    '确认下载当前租户、所选身份源和时间窗的脱敏诊断包？缺源原因见包内 manifest。',
  uacExportInvalid:
    '请填写有效 UUID 格式的 correlationId（可选）及 1–86400 的整数秒时间窗。',
  uacPartial:
    '诊断包已下载，但来源不完整；请查看 manifest 的缺源原因，不代表完整采集成功。',
  uacDownloaded: '诊断包已下载，来源与脱敏清单见 manifest。',
  idpTitle: '身份源',
  idpInstances: '身份源实例',
  idpConnectors: '可用连接器',
  idpNew: '新建身份源',
  idpEdit: '编辑身份源',
  idpConfig: '连接器配置',
  idpConnector: '连接器类型',
  idpSource: '连接器来源',
  idpBuiltin: '内置实例 · 只读',
  idpBuiltinHelp:
    '服务端禁止修改内置实例。应急身份源不能创建、修改、关闭或删除。',
  idpMultiple: '允许多个实例',
  idpSingle: '单实例（已有则不可新建）',
  idpNoConnector: '连接器不可用；可保留已有配置，但不能编辑未知配置。',
  idpEnable: '启用',
  idpDisable: '停用（保留配置）',
  idpDisableHelp:
    '通用停用保留配置、账号和绑定，不承诺撤销已有会话；UAC 通过专用配置入口停用，并撤销该源挑战、平台会话和设备授权。',
  idpEnableConfirm:
    '确认启用身份源「{name}」？新的登录可使用该身份源，服务端仍校验实际配置。',
  idpDisableConfirm:
    '确认停用身份源「{name}」？新的登录将不可用，配置与账号绑定保留；此操作不是物理删除，也不表示已有会话已撤销。',
  idpSaveConfirm:
    '确认保存身份源「{name}」的以下变化：{changes}？配置会影响登录与回调；当前接口没有 ETag 防止多管理员覆盖。',
  idpCreateConfirm:
    '确认新建「{name}」（{type}）？只登记此身份源配置，不代表真实用户登录或绑定已验证。',
  idpSaved: '身份源配置已保存，请以最新读回状态为准。',
  idpDisabled: '身份源已停用，配置和绑定保留。',
  idpReadback: '写请求已成功，但最新状态读取失败；请仅重试读取，不要重复保存。',
  idpReadAgain: '重试读取',
  idpNameEn: '名称（英文，必填）',
  idpNameZh: '名称（中文）',
  idpButtonEn: '登录按钮文字（英文）',
  idpButtonZh: '登录按钮文字（中文）',
  idpIcon: '图标地址（仅存为元数据，不在管理源执行）',
  idpOrder: '登录排序',
  idpEnabled: '启用状态',
  idpCallback: '服务端回调地址',
  idpAdmission: '现有准入策略',
  idpAdmissionHelp:
    '本批保留现有准入规则，不重写自动开通、MFA、组映射或会话复核策略。新建未指定准入策略，使用服务端默认邀请准入和新设备 MFA。',
  idpSet: '设置或替换',
  idpKeep: '保持原值 / 不发送',
  idpSecretSet: '已设置（服务端标记）',
  idpSecretNotSet: '未返回已设置标记',
  idpSecretHelp:
    '已有机密不回显；只在勾选后发送新值，不把掩码写回。取消、读回、撤权及离开页面会清除新输入。没有通用的密钥清除操作，空值仍由实际 schema 和服务端校验。',
  idpArrayHelp: '每行一个字符串；未勾选的可选字段保持省略。',
  idpSchemaReadonly:
    '以下配置不能由当前表单安全编辑，保持原值：{paths}。复杂配置/引用/组合或含隐藏子字段的对象需要后续专用表单。',
  idpInvalid: '请检查名称、排序及配置字段：{field}。最终校验以服务端为准。',
  idpConfigChanged: '连接器配置字段：{fields}（不展示机密值）',
  idpDisplayChanged: '名称 / 登录展示 / 排序',
  idpStateChanged: '启用状态',
  idpNoChanges: '没有需要提交的变化。',
  idpUnknown: '未知',
  idpReadPermission:
    '需要 iam.providers.read 才能读取身份源；写入还需要 iam.providers.write。',
  idpUnsupported:
    '连接测试、目录搜索、连接器安装/卸载尚无当前生产端点，本页不发送虚假请求；账号恢复和绑定迁移另走正式流程。',
  idpStepup:
    '需要近期身份验证。请通过既有管理员登录流程完成验证后返回，并重新确认操作。',
  idpSecretRemoved:
    '未确认的写请求可能已经送达。新输入的机密已清空，请先读取当前状态，再决定是否重新输入；本页不会自动重放。',
  idpSearch: '筛选名称或连接器类型',
  idpCapabilities: '能力声明',
  idpSteps: '登录步骤',
};
export const providersEn = {
  uacHelp:
    'UAC checks browser SSO cookies first and falls back to QR sign-in when absent or invalid. auriga.iam.uac.sso-cookie.enabled defaults to true. The portal must be in the cookie-readable domain; single-IP and single-domain deployments are supported. Logout suppresses automatic sign-in across reloads and tabs until explicit continuation.',
  uacPolicy:
    'Deployment settings under auriga.iam.uac.revalidation: strategy defaults to token-verify (department is the fallback); interval-seconds defaults to 7200 (60–86400); retry-interval-seconds defaults to 30 (5–300); max-retries defaults to 3 (0–10 additional attempts). Values must be integers. Token verification retries upstream faults within budget; explicit business/identity failures or exhausted retries revoke sessions. Department mode revokes on any single failure. The one-time department lookup after login is informational and never blocks login.',
  uacTrusted:
    'baseUrl requires HTTPS without user information, query or fragment. Deployment allowlists validate the host, port and resolved addresses; redirects are prohibited. A trusted enterprise adapter interprets systemCode.',
  uacCreateHelp:
    'New UAC providers are disabled. Register name and order first, then configure baseUrl and systemCode in the dedicated section before enabling. Metadata edits do not change configuration or enabled state.',
  uacSaveConfirm:
    'Save and enable this UAC configuration? The server validates its allowlist and current ETag.',
  uacDisableConfirm:
    'Save the disabled state? This revokes provider challenges, platform sessions and associated device grants. Users must sign in again.',
  uacReload:
    'This configuration version cannot be reused. Reload details and confirm again; writes are never replayed automatically.',
  uacDiagnostics: 'UAC diagnostics',
  uacDiagnosticConfirm:
    'Change detailed diagnostics? It defaults to off, affects subsequent requests only and expires after 15 minutes. Response structures remain sanitized.',
  uacDetailedOn: 'Detailed diagnostics enabled',
  uacDetailedOff: 'Detailed diagnostics disabled or expired',
  uacExpires: 'Expires at:',
  uacOpenDetails: 'Enable details (15 minutes)',
  uacCloseDetails: 'Disable details',
  uacSince: 'Recent window (seconds, default 7200, maximum 86400)',
  uacExportPermission:
    'Download additionally requires audit.events.read and ops.alerts.read.',
  uacExport: 'Export diagnostic bundle',
  uacExportConfirm:
    'Download a sanitized diagnostic bundle for this tenant, provider and time window? See manifest for missing sources.',
  uacExportInvalid:
    'Enter an optional UUID correlationId and an integer window of 1–86400 seconds.',
  uacPartial:
    'Bundle downloaded with incomplete sources. See manifest for missing-source reasons; collection is incomplete.',
  uacDownloaded:
    'Diagnostic bundle downloaded. See manifest for sources and sanitization details.',
  idpTitle: 'Identity providers',
  idpInstances: 'Provider instances',
  idpConnectors: 'Available connectors',
  idpNew: 'New provider',
  idpEdit: 'Edit provider',
  idpConfig: 'Connector configuration',
  idpConnector: 'Connector type',
  idpSource: 'Connector source',
  idpBuiltin: 'Built-in instance · Read-only',
  idpBuiltinHelp:
    'The server prohibits changes to built-in instances. Emergency providers cannot be created, edited, disabled or deleted.',
  idpMultiple: 'Multiple instances allowed',
  idpSingle: 'Single instance (cannot create another)',
  idpNoConnector:
    'Connector unavailable. Existing configuration is preserved, but unknown configuration cannot be edited.',
  idpEnable: 'Enable',
  idpDisable: 'Disable (keep configuration)',
  idpDisableHelp:
    'Generic disable preserves configuration, accounts and links without promising session revocation. UAC uses its dedicated configuration endpoint and revokes provider challenges, platform sessions and device grants.',
  idpEnableConfirm:
    'Enable provider “{name}”? New sign-ins can use it; actual configuration is still checked by the server.',
  idpDisableConfirm:
    'Disable provider “{name}”? New sign-ins will be unavailable; configuration and account links remain. This is not physical deletion or revocation of existing sessions.',
  idpSaveConfirm:
    'Save these changes to provider “{name}”: {changes}? They affect sign-in and callbacks. This API has no ETag protection against concurrent administrators.',
  idpCreateConfirm:
    'Create “{name}” ({type})? This registers configuration; it does not verify real-user sign-in or account links.',
  idpSaved: 'Provider configuration saved. Check the latest readback state.',
  idpDisabled: 'Provider disabled. Configuration and links remain.',
  idpReadback:
    'The write succeeded, but reading the latest state failed. Retry reads instead of saving again.',
  idpReadAgain: 'Retry reads',
  idpNameEn: 'Name (English, required)',
  idpNameZh: 'Name (Chinese)',
  idpButtonEn: 'Sign-in button text (English)',
  idpButtonZh: 'Sign-in button text (Chinese)',
  idpIcon: 'Icon location (metadata only; not executed in the admin origin)',
  idpOrder: 'Sign-in order',
  idpEnabled: 'Enabled',
  idpCallback: 'Server callback URL',
  idpAdmission: 'Existing admission policy',
  idpAdmissionHelp:
    'Existing admission rules are preserved. Auto-provisioning, MFA, group mapping and session checks are not rewritten here. New providers use server defaults: invite-only admission and MFA on new devices.',
  idpSet: 'Set or replace',
  idpKeep: 'Keep / omit',
  idpSecretSet: 'Configured (server marker)',
  idpSecretNotSet: 'No configured marker returned',
  idpSecretHelp:
    'Existing secrets are never displayed. Only explicitly selected new values are sent; masks are never written back. Cancel, readback, revocation and leaving clear new input. There is no generic secret-clear operation; empty values remain subject to the schema and server validation.',
  idpArrayHelp: 'One string per line. Unselected optional fields stay omitted.',
  idpSchemaReadonly:
    'These settings cannot safely be edited by this form and are preserved: {paths}. Complex, referenced or composed schemas and objects with hidden children require dedicated forms.',
  idpInvalid:
    'Check name, order and configuration field: {field}. The server performs final validation.',
  idpConfigChanged: 'Connector fields: {fields} (secret values hidden)',
  idpDisplayChanged: 'Name / sign-in display / order',
  idpStateChanged: 'Enabled state',
  idpNoChanges: 'No changes to submit.',
  idpUnknown: 'Unknown',
  idpReadPermission:
    'Reading requires iam.providers.read; writing additionally requires iam.providers.write.',
  idpUnsupported:
    'Connection tests, directory search and connector installation/removal have no current production endpoints. This page sends no simulated requests. Recovery and identity migration use separate workflows.',
  idpStepup:
    'Recent verification is required. Complete the existing administrator sign-in flow, return and confirm the action again.',
  idpSecretRemoved:
    'The unconfirmed write might already have reached the server. New secret input was cleared. Read current state before deciding to enter it again; writes are not replayed automatically.',
  idpSearch: 'Filter name or connector type',
  idpCapabilities: 'Declared capabilities',
  idpSteps: 'Sign-in steps',
};
