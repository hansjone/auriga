export const quotaZh = {
  unsafeQuota:
    '读回数值超出可安全编辑的整数精度，已禁止保存本层配置。请通过支持精确整数的工具处理：{fields}。',
  unsafeEffective: '数值超出可精确显示范围',
  quotaTitle: '配额',
  quotaScope: '配置层',
  defaultQuota: '默认配额',
  groupQuota: '分组覆盖',
  memberQuota: '成员覆盖',
  quotaTarget: '选择目标',
  quotaSearch: '搜索候选成员',
  loadCandidates: '读取候选',
  candidatePermission: '读取这些候选需要相应的分组或成员查看权限。',
  currentLayer: '当前层配置',
  layerAbsent: '本层未配置',
  layerUnset: '本层未设置',
  quotaValue: '具体值',
  unlimited: '不限',
  allow: '允许',
  deny: '禁止',
  fields: {
    cpuMillis: 'CPU',
    memoryMb: '内存',
    diskGb: '磁盘',
    maxInstances: '实例数量上限',
    alwaysOnAllowed: '允许常驻',
  },
  units: {
    mCPU: '毫核',
    MB: 'MB',
    GB: 'GB',
    count: '个',
  },
  numericHint: '整数，最小 {min}；最大可精确表示整数为 9007199254740991。',
  invalidQuota: '「{field}」输入无效，请检查整数范围或最小值。',
  quotaSemantics:
    '未设置表示由下一层决定；只有明确标注“不限”的字段才是不限。实例数为 0 表示禁止实例，与未设置不同。',
  effectiveQuota: '成员有效配额',
  effectiveUnknown: '未设置',
  effectiveOrder:
    '按字段：成员覆盖 → 所在多个组中最宽松的设置 → 默认配额。下方值来自正式有效配额接口；接口不提供具体来源组。',
  quotaImpact:
    '配额保存不代表已有实例立即变更；仍以服务端后续准入与执行结果为准。',
  replaceQuota:
    '确认替换「{target}」的整份本层配置？本次将清除：{fields}。未设置的字段将按继承规则处理。',
  noneCleared: '无',
  saveQuota: '保存本层配额',
  quotaSaved: '本层配额已保存，已重新读取配置。',
  deleteQuota: '删除本层覆盖',
  deleteQuotaConfirm:
    '确认删除「{target}」的整份配额覆盖？删除后重新按继承规则计算；已有实例与预占不会因此立即重置。',
  quotaDeleted: '本层覆盖已删除。',
  quotaRefreshFailed: '写入已提交，但重新读取失败。请重试读取，不要重复写入。',
};
export const quotaEn = {
  unsafeQuota:
    'Returned values exceed safe integer precision. Saving this layer is disabled. Use a tool supporting exact integers to update: {fields}.',
  unsafeEffective: 'Value exceeds exact display range',
  quotaTitle: 'Quotas',
  quotaScope: 'Configuration layer',
  defaultQuota: 'Default quota',
  groupQuota: 'Group override',
  memberQuota: 'Member override',
  quotaTarget: 'Select target',
  quotaSearch: 'Search member candidates',
  loadCandidates: 'Load candidates',
  candidatePermission:
    'Reading these candidates requires the corresponding group or member read permission.',
  currentLayer: 'Current layer',
  layerAbsent: 'No configuration at this layer',
  layerUnset: 'Unset at this layer',
  quotaValue: 'Specific value',
  unlimited: 'Unlimited',
  allow: 'Allow',
  deny: 'Deny',
  fields: {
    cpuMillis: 'CPU',
    memoryMb: 'Memory',
    diskGb: 'Disk',
    maxInstances: 'Instance limit',
    alwaysOnAllowed: 'Allow always-on',
  },
  units: {
    mCPU: 'millicores',
    MB: 'MB',
    GB: 'GB',
    count: 'instances',
  },
  numericHint:
    'Integer, minimum {min}; maximum exact integer 9007199254740991.',
  invalidQuota: 'Invalid “{field}”. Check integer range or minimum.',
  quotaSemantics:
    'Unset inherits from the next layer; only an explicit Unlimited value is unlimited. Zero instances prohibits instances and differs from unset.',
  effectiveQuota: 'Effective member quota',
  effectiveUnknown: 'Unset',
  effectiveOrder:
    'Per field: member override → loosest settings across member groups → default. Values below come from the effective-quota endpoint; it does not identify a source group.',
  quotaImpact:
    'Saving does not immediately change existing instances. Subsequent server admission and execution remain authoritative.',
  replaceQuota:
    'Replace the entire current layer for “{target}”? Fields to clear: {fields}. Unset fields follow inheritance rules.',
  noneCleared: 'None',
  saveQuota: 'Save current layer',
  quotaSaved: 'Quota saved and configuration reloaded.',
  deleteQuota: 'Delete layer override',
  deleteQuotaConfirm:
    'Delete the entire quota override for “{target}”? Inheritance will be recalculated; existing instances and reservations are not immediately reset.',
  quotaDeleted: 'Layer override deleted.',
  quotaRefreshFailed:
    'Write submitted, but reload failed. Retry reading instead of writing again.',
};
