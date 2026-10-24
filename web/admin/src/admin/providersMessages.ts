export const providersZh = {
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
    'DELETE 在当前服务中只停用身份源，不删除配置、账号或已绑定身份；也不承诺撤销已有会话。',
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
    'DELETE currently disables the provider. It does not delete configuration, accounts or linked identities, or promise revocation of existing sessions.',
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
