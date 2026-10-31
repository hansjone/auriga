export const rolesZh = {
  activeMemberRequired: '仅可向在职且启用的成员授予角色。',
  directBinding: '直接绑定',
  inheritedBinding: '继承自分组：{group}（只读）',
  chooseMemberRole: '选择要授予的角色',
  memberRoleHint:
    '组继承的角色仅可在来源组管理；超出本人权限或已直接绑定的角色不可再次授予。最后一位所有者保护由服务端校验。',
  memberGrantConfirm:
    '确认将「{role}」授予成员「{name}」？角色权限：{permissions}。权限将立即变化。',
  memberRevokeConfirm:
    '确认撤销成员「{name}」直接绑定的「{role}」？权限将立即变化；不能撤销最后一位所有者。',
  memberRolesSaved: '成员角色已更新，并重新读取当前权限。',
  roleType: '角色类型',
  unknownPermissionMetadata: '权限目录未提供此项的敏感级别',
  roleTitle: '角色与授权',
  roleKey: '角色标识',
  roleNameEn: '英文名称',
  roleNameZh: '中文名称',
  roleDescriptionEn: '英文描述',
  roleDescriptionZh: '中文描述',
  roleBuiltin: '内置角色',
  roleCustom: '自定义角色',
  builtinReadOnly: '内置角色定义只读；绑定操作仍按授权权限单独校验。',
  createRole: '创建角色',
  editRole: '编辑角色',
  deleteRole: '删除角色',
  rolePermissions: '角色权限',
  permissionCatalog: '权限目录',
  sensitivePermission: '敏感权限',
  permissionLimit:
    '只能新增本人当前持有且目录中存在的权限。编辑时会保留已有权限，移除后不能重新选择超出本人权限的条目；服务端仍最终校验授权上限。',
  invalidRole:
    '请填写合法角色标识和非空英文名称。标识需以小写字母开头，长度 2–63，只包含小写字母、数字或下划线。',
  roleConfirm:
    '确认保存角色「{name}」及权限：{permissions}？敏感权限：{sensitive}。修改会影响已绑定主体的有效权限。',
  roleDeleteConfirm:
    '确认删除角色「{name}」？相关成员、分组和服务账号的绑定将一并删除，并影响这些主体的权限。',
  roleSaved: '角色已保存。',
  roleDeleted: '角色已删除。',
  roleBindings: '授权绑定',
  bindingSubject: '授权主体',
  subjectType: '主体类型',
  memberSubject: '成员',
  groupSubject: '分组',
  serviceSubject: '服务账号',
  serviceReadOnly: '服务账号没有本批可用的权威候选，本页仅查看已有绑定。',
  grantRole: '授予角色',
  revokeRole: '撤销授权',
  grantPermissionRequired:
    '管理绑定需要独立的角色授予权限；角色编辑权限不包含此能力。',
  candidateReadRequired: '读取这些候选需要相应的成员或分组查看权限。',
  subjectSearch: '搜索候选成员',
  loadSubjects: '读取授权候选',
  chooseSubject: '选择授权主体',
  grantConfirm:
    '确认将角色「{role}」授予「{subject}」？该主体将获得角色权限；敏感权限：{sensitive}。超出本人权限时服务端会拒绝。',
  revokeConfirm:
    '确认撤销「{subject}」的角色「{role}」绑定？主体可能立即失去通过此绑定取得的权限；最后所有者保护仍由服务端验证。',
  bindingSaved: '授权绑定已保存。',
  bindingRevoked: '授权绑定已撤销。',
  rolesReadbackFailed: '写入已提交，但正式读回失败。请重试读取，不要重复提交。',
  localizedRoleHint:
    '中文文案按当前 Org 服务的 zh 键保存；空描述会以空字符串写回，不发送 null。',
  noneSelected: '无',
  grantedBy: '授权人 ID',
  bindingCount: '绑定数',
};
export const rolesEn = {
  activeMemberRequired: 'Roles can only be granted to active, enabled members.',
  directBinding: 'Direct binding',
  inheritedBinding: 'Inherited from group: {group} (read-only)',
  chooseMemberRole: 'Select a role to grant',
  memberRoleHint:
    'Manage inherited roles in their source group. Roles exceeding your permissions or already bound directly cannot be granted again. The server enforces last-owner protection.',
  memberGrantConfirm:
    'Grant “{role}” to member “{name}”? Role permissions: {permissions}. Permissions change immediately.',
  memberRevokeConfirm:
    'Revoke the direct “{role}” binding from member “{name}”? Permissions change immediately. The last owner cannot be revoked.',
  memberRolesSaved: 'Member roles updated and current permissions reloaded.',
  roleType: 'Role type',
  unknownPermissionMetadata:
    'Sensitivity is not specified in the permission catalog',
  roleTitle: 'Roles and grants',
  roleKey: 'Role key',
  roleNameEn: 'English name',
  roleNameZh: 'Chinese name',
  roleDescriptionEn: 'English description',
  roleDescriptionZh: 'Chinese description',
  roleBuiltin: 'Built-in role',
  roleCustom: 'Custom role',
  builtinReadOnly:
    'Built-in role definitions are read-only. Binding operations require separate grant permission.',
  createRole: 'Create role',
  editRole: 'Edit role',
  deleteRole: 'Delete role',
  rolePermissions: 'Role permissions',
  permissionCatalog: 'Permission catalog',
  sensitivePermission: 'Sensitive permission',
  permissionLimit:
    'New selections must exist in the catalog and in your current permissions. Existing selections are retained; once removed, permissions outside your own cannot be reselected. The server remains authoritative.',
  invalidRole:
    'Provide a valid role key and a nonempty English name. Keys start with a lowercase letter, contain 2–63 lowercase letters, digits or underscores.',
  roleConfirm:
    'Save role “{name}” with permissions: {permissions}? Sensitive permissions: {sensitive}. Changes affect bound subjects.',
  roleDeleteConfirm:
    'Delete role “{name}”? Its member, group and service-account bindings will also be deleted, affecting their permissions.',
  roleSaved: 'Role saved.',
  roleDeleted: 'Role deleted.',
  roleBindings: 'Role bindings',
  bindingSubject: 'Subject',
  subjectType: 'Subject type',
  memberSubject: 'Member',
  groupSubject: 'Group',
  serviceSubject: 'Service account',
  serviceReadOnly:
    'No authoritative service-account candidates are available in this batch. Existing bindings are read-only.',
  grantRole: 'Grant role',
  revokeRole: 'Revoke grant',
  grantPermissionRequired:
    'Managing bindings requires separate role-grant permission; role-edit permission does not include it.',
  candidateReadRequired:
    'Reading these candidates requires the corresponding member or group read permission.',
  subjectSearch: 'Search member candidates',
  loadSubjects: 'Load subjects',
  chooseSubject: 'Select subject',
  grantConfirm:
    'Grant role “{role}” to “{subject}”? The subject gains its permissions. Sensitive permissions: {sensitive}. The server rejects grants exceeding your permissions.',
  revokeConfirm:
    'Revoke role “{role}” from “{subject}”? The subject may immediately lose permissions from this binding. The server enforces last-owner protection.',
  bindingSaved: 'Role binding saved.',
  bindingRevoked: 'Role binding revoked.',
  rolesReadbackFailed:
    'Write submitted, but authoritative readback failed. Retry reading instead of submitting again.',
  localizedRoleHint:
    'Chinese text uses the current Org service’s zh key. Empty descriptions are sent as empty strings, not null.',
  noneSelected: 'None',
  grantedBy: 'Granted by ID',
  bindingCount: 'Binding count',
};
