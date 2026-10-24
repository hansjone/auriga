export const settingsZh = {
  settingsTeam: '团队资料',
  settingsWorkspace: '工作空间策略',
  settingsName: '团队显示名',
  settingsLocale: '团队语言',
  settingsHosts: '主机映射（只读）',
  settingsCreated: '创建时间',
  settingsTeamScope:
    '仅修改本团队显示名和语言。空字符串会原样保存；无版本校验，多个管理员可能覆盖彼此修改。',
  settingsWorkspaceScope:
    '替换本团队整层 workspace 策略；省略字段不写入默认值，未知字段保留。策略配置不代表资源安装或已有实例变更已执行。版本冲突时保留草稿，请重新读取后再确认。',
  settingsMissing: '尚无 workspace 策略；本页不创建缺失策略。',
  settingsEtag: '缺失或非法强 ETag，禁止保存；请重新读取。',
  settingsUnsafe: '服务端值包含非法字段类型或无法安全往返的数值，禁止保存。',
  settingsInvalid:
    '请填写有效值：闲置分钟至少 5，同时冷启动数至少 1；整数必须可安全表示。显示名最多 100 字符。',
  settingsReload: '重新读取（丢弃本区草稿）',
  settingsReadback: '写入已提交，但正式读回失败。请仅重试读取，不要重复提交。',
  settingsReadRetry: '只重试读取',
  settingsConflict:
    '版本已变化（412）。草稿已保留，禁止再次提交；请明确重新读取后检查并重新确认。',
  settingsUnset: '省略（本层未设置）',
  settingsPresent: '本层设置',
  settingsChanges: '确认以下实际变化：{changes}。{scope}',
  settingsRaw: '服务端原始策略（包含保留的未知字段）',
  idleMinutes: '闲置回收分钟',
  wakeOnSchedule: '定时任务到点唤醒',
  alwaysOnRequiresApproval: '常驻运行需批准',
  maxConcurrentStarts: '同时冷启动上限',
  defaultDshVersion: '默认 DSH 版本',
  allowPersonalPlugins: '允许个人插件',
};
export const settingsEn = {
  settingsTeam: 'Team profile',
  settingsWorkspace: 'Workspace policy',
  settingsName: 'Team display name',
  settingsLocale: 'Team locale',
  settingsHosts: 'Hosts (read only)',
  settingsCreated: 'Created at',
  settingsTeamScope:
    'Changes only this team’s display name and locale. Empty strings are stored as supplied. There is no version check; administrators may overwrite each other’s changes.',
  settingsWorkspaceScope:
    'Replaces this team’s entire workspace policy. Omitted fields receive no client defaults; unknown fields are preserved. Saving does not confirm resource installation or changes to existing instances. A version conflict preserves your draft; reload and confirm again.',
  settingsMissing:
    'No workspace policy exists. This page cannot create a missing policy.',
  settingsEtag:
    'Missing or invalid strong ETag. Saving is blocked; reload first.',
  settingsUnsafe:
    'The server value contains invalid field types or numbers that cannot safely round trip. Saving is blocked.',
  settingsInvalid:
    'Enter valid values: idle minutes ≥ 5, concurrent starts ≥ 1, safe integers only, and display name ≤ 100 characters.',
  settingsReload: 'Reload (discard this section’s draft)',
  settingsReadback:
    'The write was submitted but the official read failed. Retry reading only; do not submit again.',
  settingsReadRetry: 'Retry reading only',
  settingsConflict:
    'Version changed (412). Your draft is retained and resubmission is blocked. Explicitly reload, review and confirm again.',
  settingsUnset: 'Omitted (not set at this layer)',
  settingsPresent: 'Set at this layer',
  settingsChanges: 'Confirm actual changes: {changes}. {scope}',
  settingsRaw: 'Original server policy (including preserved unknown fields)',
  idleMinutes: 'Idle minutes',
  wakeOnSchedule: 'Wake on schedule',
  alwaysOnRequiresApproval: 'Always-on requires approval',
  maxConcurrentStarts: 'Maximum concurrent starts',
  defaultDshVersion: 'Default DSH version',
  allowPersonalPlugins: 'Allow personal plugins',
};
