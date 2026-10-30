export interface paths {
  '/me': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getMyMembership'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/quota': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getMyEffectiveQuota'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/tenant': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getTenant'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/tenant': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch: operations['updateTenant'];
    trace?: never;
  };
  '/admin/members': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listMembers'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/members/{accountId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get: operations['getMember'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch: operations['updateMember'];
    trace?: never;
  };
  '/admin/members/{accountId}/disable': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 发 user.disabled。不能停用自己，不能停用最后一个超管（409 org.last_owner）。 */
    post: operations['disableMember'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/members/{accountId}/enable': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['enableMember'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/members/{accountId}/offboard': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 离职。发 user.offboarded，后续的导出、归档、转交由各服务按事件处理。 */
    post: operations['offboardMember'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/members/{accountId}/role-bindings': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    /** @description 成员直接获得和经组获得的角色绑定。 */
    get: operations['listMemberRoleBindings'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/groups': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listGroups'];
    put?: never;
    post: operations['createGroup'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/groups/{groupId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    get: operations['getGroup'];
    put?: never;
    post?: never;
    /** @description 有子组时 409 org.group_not_empty。成员移出、该组的角色绑定和配额一并删除。 */
    delete: operations['deleteGroup'];
    options?: never;
    head?: never;
    patch: operations['updateGroup'];
    trace?: never;
  };
  '/admin/groups/{groupId}/members': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    get: operations['listGroupMembers'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/groups/{groupId}/members/{accountId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get?: never;
    put: operations['addGroupMember'];
    post?: never;
    delete: operations['removeGroupMember'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/permissions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listPermissions'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/roles': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listRoles'];
    put?: never;
    post: operations['createRole'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/roles/{roleId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        roleId: components['parameters']['RoleId'];
      };
      cookie?: never;
    };
    get: operations['getRole'];
    put?: never;
    post?: never;
    delete: operations['deleteRole'];
    options?: never;
    head?: never;
    /** @description 内置角色不能改（409 org.role_builtin）。 */
    patch: operations['updateRole'];
    trace?: never;
  };
  '/admin/role-bindings': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listRoleBindings'];
    put?: never;
    post: operations['createRoleBinding'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/role-bindings/{bindingId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** @description 不能撤销最后一个超管（409 org.last_owner）。 */
    delete: operations['deleteRoleBinding'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/policies/{key}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        key: components['schemas']['PolicyKey'];
      };
      cookie?: never;
    };
    get: operations['getPolicy'];
    put: operations['putPolicy'];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/quotas/default': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getDefaultQuota'];
    put: operations['putDefaultQuota'];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/groups/{groupId}/quota': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    get: operations['getGroupQuota'];
    put: operations['putGroupQuota'];
    post?: never;
    delete: operations['deleteGroupQuota'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/members/{accountId}/quota': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get: operations['getMemberQuota'];
    put: operations['putMemberQuota'];
    post?: never;
    delete: operations['deleteMemberQuota'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/members/{accountId}/quota/effective': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    /** @description 成员覆盖 → 所在组 → 租户默认，逐项合并：某层没设置（字段不出现）就看下一层；多个组时取最宽松的值，"不限"最宽松。本人查自己的用 /me/quota。 */
    get: operations['getEffectiveQuota'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/invitations': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listInvitations'];
    put?: never;
    post: operations['createInvitation'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/invitations/{invitationId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['revokeInvitation'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/invitations/preview/{code}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** 邀请页用：不需要登录 */
    get: operations['previewInvitation'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
}
export type webhooks = Record<string, never>;
export interface components {
  schemas: {
    Tenant: {
      id: components['schemas']['Uuid'];
      slug: string;
      displayName: string;
      locale: string;
      hosts: string[];
      /** Format: date-time */
      createdAt: string;
    };
    /** @enum {string} */
    MemberType: 'member' | 'external';
    /** @enum {string} */
    MemberStatus: 'active' | 'disabled' | 'offboarded';
    Member: {
      id: components['schemas']['Uuid'];
      displayName: string;
      email?: string;
      memberType: components['schemas']['MemberType'];
      status: components['schemas']['MemberStatus'];
      groupIds: components['schemas']['Uuid'][];
      roleKeys: string[];
      /** Format: date-time */
      createdAt: string;
      /** Format: date-time */
      disabledAt?: string;
      /** Format: date-time */
      offboardedAt?: string;
    };
    GroupInput: {
      name: string;
      description?: string;
      parentId?: components['schemas']['Uuid'];
    };
    Group: {
      id: components['schemas']['Uuid'];
      name: string;
      description?: string;
      parentId?: components['schemas']['Uuid'];
      memberCount: number;
      /**
       * @description mapped 表示由身份源属性映射维护。
       * @enum {string}
       */
      source: 'manual' | 'mapped';
      /** Format: date-time */
      createdAt: string;
    };
    Permission: {
      /** @description `<服务>.<资源>.<动作>`，例如 `org.members.write`。 */
      key: string;
      service: string;
      description: components['schemas']['LocalizedText'];
      /** @description 高危权限，授予时界面要二次确认。 */
      sensitive: boolean;
    };
    RoleInput: {
      /** @description 创建后不能改。 */
      key: string;
      name: components['schemas']['LocalizedText'];
      description?: components['schemas']['LocalizedText'];
      permissions: string[];
    };
    /** @description 内置角色 owner（超管）、admin、member、external 不能修改或删除。 */
    Role: {
      id: components['schemas']['Uuid'];
      key: string;
      name: components['schemas']['LocalizedText'];
      description?: components['schemas']['LocalizedText'];
      builtin: boolean;
      permissions: string[];
      bindingCount: number;
    };
    BindingSubject: {
      /** @enum {string} */
      type: 'member' | 'group' | 'service_account';
      id: components['schemas']['Uuid'];
    };
    RoleBinding: {
      id: components['schemas']['Uuid'];
      roleId: components['schemas']['Uuid'];
      roleKey: string;
      subject: components['schemas']['BindingSubject'];
      /**
       * @description 在成员视图里，经组获得的绑定标 group。
       * @enum {string}
       */
      via?: 'direct' | 'group';
      grantedBy?: components['schemas']['Uuid'];
      /** Format: date-time */
      createdAt: string;
    };
    /** @enum {string} */
    PolicyKey: 'workspace' | 'egress' | 'login';
    /** @description 策略键 workspace。 */
    WorkspacePolicy: {
      /**
       * @description 四个闲置条件都满足多久后回收。
       * @default 30
       */
      idleMinutes: number;
      /**
       * @description 有定时任务时到点唤醒。
       * @default true
       */
      wakeOnSchedule: boolean;
      /** @default true */
      alwaysOnRequiresApproval: boolean;
      /**
       * @description 准入：同时冷启动的上限，超出返回 429。
       * @default 4
       */
      maxConcurrentStarts: number;
      defaultDshVersion?: string;
      /**
       * @description 是否允许 L4 个人插件。
       * @default true
       */
      allowPersonalPlugins: boolean;
    };
    /** @description 策略键 egress。解析到内网地址的目标一律拒绝，不能通过策略放开。 */
    EgressPolicy: {
      /** @description 允许的分类（分类清单由 egress 维护，例如 package_registries、code_hosting、model_providers、search）。 */
      categories?: string[];
      /** @description 额外允许的域名，支持 `*.example.com`。 */
      allowDomains?: string[];
      denyDomains?: string[];
    };
    /** @description 策略键 login（全租户的登录要求，身份源实例的准入策略在 iam）。 */
    LoginPolicy: {
      /** @default false */
      requireMfa: boolean;
      /** @default true */
      adminRequireMfa: boolean;
      /** @default 20 */
      maxSessionsPerAccount: number;
    };
    PolicyValue:
      | components['schemas']['WorkspacePolicy']
      | components['schemas']['EgressPolicy']
      | components['schemas']['LoginPolicy'];
    Policy: {
      key: components['schemas']['PolicyKey'];
      version: number;
      value: components['schemas']['PolicyValue'];
      /** Format: date-time */
      updatedAt: string;
      updatedBy?: components['schemas']['Uuid'];
    };
    /** @description 各字段都可省略；省略表示"这一层不设置"。值为 null 表示"不限"。合并时"不限"优先于任何具体数值。 */
    Quota: {
      instance?: {
        cpuMillis?: number;
        memoryMb?: number;
        diskGb?: number;
        /** @description 0 表示不允许有实例；null 表示不限。 */
        maxInstances?: number | null;
        alwaysOnAllowed?: boolean;
      };
    };
    InvitationInput: {
      /** @description 限定受邀人邮箱（可选）。 */
      email?: string;
      displayName?: string;
      memberType: components['schemas']['MemberType'];
      roleIds?: components['schemas']['Uuid'][];
      groupIds?: components['schemas']['Uuid'][];
      /** @description 允许用哪些身份源兑换，空表示不限。 */
      providerIds?: components['schemas']['Uuid'][];
      /** Format: date-time */
      expiresAt?: string;
      /** @default 1 */
      maxUses: number;
    };
    Invitation: {
      id: components['schemas']['Uuid'];
      /** @enum {string} */
      status: 'active' | 'used' | 'expired' | 'revoked';
      email?: string;
      displayName?: string;
      memberType: components['schemas']['MemberType'];
      roleIds: components['schemas']['Uuid'][];
      groupIds: components['schemas']['Uuid'][];
      providerIds: components['schemas']['Uuid'][];
      maxUses: number;
      uses: number;
      /** Format: date-time */
      expiresAt: string;
      createdBy: components['schemas']['Uuid'];
      /** Format: date-time */
      createdAt: string;
    };
    /** Format: uuid */
    Uuid: string;
    FieldError: {
      /** @description JSON Pointer，例如 `/email`。 */
      field: string;
      code: string;
      message?: string;
    };
    /** @description RFC 9457 问题详情。 */
    Problem: {
      /**
       * Format: uri-reference
       * @default about:blank
       */
      type: string;
      /** @description 英文短标题。 */
      title: string;
      status: number;
      /** @description 按 Accept-Language 返回的说明。 */
      detail?: string;
      /** Format: uri-reference */
      instance?: string;
      /**
       * @description 机器可读的错误码，形如 `<域>.<snake_case>`。
       * @example iam.login_tx_expired
       */
      code: string;
      /** @description 错误文案里的参数。 */
      params?: Record<string, never>;
      /** @description 字段级错误（validation_failed 时）。 */
      errors?: components['schemas']['FieldError'][];
      traceId?: string;
    };
    /**
     * @description 按语言给出的文案，键是 BCP 47 语言标签。至少要有 `en`；前端按当前语言选，没有就用 `en`。
     * @example {
     *       "en": "Password",
     *       "zh-CN": "口令"
     *     }
     */
    LocalizedText: {
      en: string;
    } & {
      [key: string]: string;
    };
  };
  responses: {
    /** @description 资源不存在（common.not_found）。 */
    NotFound: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
    /** @description If-Match 不匹配（common.precondition_failed）。 */
    PreconditionFailed: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
    /** @description 状态冲突（common.conflict 等）。 */
    Conflict: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
    /** @description 没有权限（common.forbidden 或更具体的码）。 */
    Forbidden: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
    /** @description 请求无效（common.validation_failed 等）。 */
    BadRequest: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
    /** @description 太频繁（common.rate_limited）。 */
    RateLimited: {
      headers: {
        'Retry-After': components['headers']['RetryAfter'];
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
  };
  parameters: {
    AccountId: components['schemas']['Uuid'];
    GroupId: components['schemas']['Uuid'];
    RoleId: components['schemas']['Uuid'];
    Limit: number;
    /** @description 上一页响应里的 nextCursor。 */
    Cursor: string;
    /** @description 资源的 ETag；不匹配返回 412。 */
    IfMatch: string;
    IdempotencyKey: string;
  };
  requestBodies: never;
  headers: {
    /** @description 资源版本。 */
    ETag: string;
    /** @description 多少秒后重试。 */
    RetryAfter: number;
  };
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  getMyMembership: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 我的成员资料和有效权限（前端据此显示菜单；权限检查仍在服务端） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            member: components['schemas']['Member'];
            tenant: components['schemas']['Tenant'];
            permissions: string[];
          };
        };
      };
    };
  };
  getMyEffectiveQuota: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 我的有效配额（合并后） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
    };
  };
  getTenant: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 当前租户 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Tenant'];
        };
      };
    };
  };
  updateTenant: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': {
          displayName?: string;
          locale?: string;
          hosts?: string[];
        };
      };
    };
    responses: {
      /** @description 已更新 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Tenant'];
        };
      };
    };
  };
  listMembers: {
    parameters: {
      query?: {
        /** @description 按显示名、邮箱搜索。 */
        q?: string;
        status?: components['schemas']['MemberStatus'];
        memberType?: components['schemas']['MemberType'];
        groupId?: components['schemas']['Uuid'];
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成员列表 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Member'][];
            nextCursor?: string;
          };
        };
      };
    };
  };
  getMember: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成员 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Member'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  updateMember: {
    parameters: {
      query?: never;
      header?: {
        /** @description 资源的 ETag；不匹配返回 412。 */
        'If-Match'?: components['parameters']['IfMatch'];
      };
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': {
          displayName?: string;
          email?: string;
          memberType?: components['schemas']['MemberType'];
        };
      };
    };
    responses: {
      /** @description 已更新（发 user.updated） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Member'];
        };
      };
      412: components['responses']['PreconditionFailed'];
    };
  };
  disableMember: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: {
      content: {
        'application/json': {
          reason?: string;
        };
      };
    };
    responses: {
      /** @description 已停用 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Member'];
        };
      };
      409: components['responses']['Conflict'];
    };
  };
  enableMember: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已启用（发 user.enabled） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Member'];
        };
      };
    };
  };
  offboardMember: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          handoverTo?: components['schemas']['Uuid'];
          reason?: string;
        };
      };
    };
    responses: {
      /** @description 已离职 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Member'];
        };
      };
      409: components['responses']['Conflict'];
    };
  };
  listMemberRoleBindings: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 角色绑定 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['RoleBinding'][];
          };
        };
      };
    };
  };
  listGroups: {
    parameters: {
      query?: {
        q?: string;
        parentId?: components['schemas']['Uuid'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 组 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Group'][];
          };
        };
      };
    };
  };
  createGroup: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['GroupInput'];
      };
    };
    responses: {
      /** @description 已创建 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Group'];
        };
      };
    };
  };
  getGroup: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 组 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Group'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  deleteGroup: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已删除 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      409: components['responses']['Conflict'];
    };
  };
  updateGroup: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': components['schemas']['GroupInput'];
      };
    };
    responses: {
      /** @description 已更新 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Group'];
        };
      };
    };
  };
  listGroupMembers: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 组成员 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Member'][];
            nextCursor?: string;
          };
        };
      };
    };
  };
  addGroupMember: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已加入（发 user.updated，有效权限变化时另发 user.permissions_changed） */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description 组的有效权限超出操作者自己的权限（org.grant_exceeds_own） */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  removeGroupMember: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已移出 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  listPermissions: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 权限目录（各服务注册） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Permission'][];
          };
        };
      };
    };
  };
  listRoles: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 角色（内置 + 自定义） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Role'][];
          };
        };
      };
    };
  };
  createRole: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['RoleInput'];
      };
    };
    responses: {
      /** @description 已创建 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Role'];
        };
      };
      403: components['responses']['Forbidden'];
    };
  };
  getRole: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        roleId: components['parameters']['RoleId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 角色 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Role'];
        };
      };
    };
  };
  deleteRole: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        roleId: components['parameters']['RoleId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已删除，相关绑定一并删除 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      409: components['responses']['Conflict'];
    };
  };
  updateRole: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        roleId: components['parameters']['RoleId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': components['schemas']['RoleInput'];
      };
    };
    responses: {
      /** @description 已更新（受影响成员发 user.permissions_changed） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Role'];
        };
      };
      403: components['responses']['Forbidden'];
      409: components['responses']['Conflict'];
    };
  };
  listRoleBindings: {
    parameters: {
      query?: {
        roleId?: components['schemas']['Uuid'];
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 角色绑定 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['RoleBinding'][];
            nextCursor?: string;
          };
        };
      };
    };
  };
  createRoleBinding: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          roleId: components['schemas']['Uuid'];
          subject: components['schemas']['BindingSubject'];
        };
      };
    };
    responses: {
      /** @description 已授予（发 user.permissions_changed） */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['RoleBinding'];
        };
      };
      403: components['responses']['Forbidden'];
    };
  };
  deleteRoleBinding: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        bindingId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已撤销 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      409: components['responses']['Conflict'];
    };
  };
  getPolicy: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        key: components['schemas']['PolicyKey'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 策略 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Policy'];
        };
      };
    };
  };
  putPolicy: {
    parameters: {
      query?: never;
      header?: {
        /** @description 资源的 ETag；不匹配返回 412。 */
        'If-Match'?: components['parameters']['IfMatch'];
      };
      path: {
        key: components['schemas']['PolicyKey'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          value: components['schemas']['PolicyValue'];
        };
      };
    };
    responses: {
      /** @description 已保存（version 加一） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Policy'];
        };
      };
      400: components['responses']['BadRequest'];
      412: components['responses']['PreconditionFailed'];
    };
  };
  getDefaultQuota: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 租户默认配额 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
    };
  };
  putDefaultQuota: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Quota'];
      };
    };
    responses: {
      /** @description 已保存 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
    };
  };
  getGroupQuota: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 组配额（只含设置了的字段） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  putGroupQuota: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Quota'];
      };
    };
    responses: {
      /** @description 已保存 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
    };
  };
  deleteGroupQuota: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        groupId: components['parameters']['GroupId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已删除 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getMemberQuota: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成员覆盖（只含设置了的字段） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  putMemberQuota: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Quota'];
      };
    };
    responses: {
      /** @description 已保存 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
    };
  };
  deleteMemberQuota: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已删除 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getEffectiveQuota: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 合并后的配额 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Quota'];
        };
      };
    };
  };
  listInvitations: {
    parameters: {
      query?: {
        status?: 'active' | 'used' | 'expired' | 'revoked';
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 邀请 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Invitation'][];
            nextCursor?: string;
          };
        };
      };
    };
  };
  createInvitation: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['InvitationInput'];
      };
    };
    responses: {
      /** @description 已创建。code 和 url 只在创建时返回 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Invitation'] & {
            code: string;
            /** Format: uri */
            url: string;
          };
        };
      };
      403: components['responses']['Forbidden'];
    };
  };
  revokeInvitation: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        invitationId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已撤销 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  previewInvitation: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        code: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 邀请概要（不含受邀人信息以外的内部数据） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            valid: boolean;
            /**
             * @description valid=false 时的原因。
             * @enum {string}
             */
            reason?: 'expired' | 'used' | 'revoked' | 'not_found';
            tenantName?: string;
            inviterName?: string;
            memberType?: components['schemas']['MemberType'];
            /** @description 限定的受邀邮箱（部分遮挡）。 */
            email?: string;
            providerIds?: components['schemas']['Uuid'][];
            /** Format: date-time */
            expiresAt?: string;
          };
        };
      };
      429: components['responses']['RateLimited'];
    };
  };
}
