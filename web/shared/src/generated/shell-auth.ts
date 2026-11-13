export interface paths {
  '/messages': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** 受控本地 IPC 消息投影 */
    post: operations['exchangeShellAuthMessage'];
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
    ShellAuthSnapshot: {
      /** @enum {integer} */
      protocolVersion: 1;
      /** Format: uuid */
      shellEpoch: string;
      revision: number;
      /** @enum {string} */
      state:
        | 'signed_out'
        | 'authenticating'
        | 'authenticated'
        | 'unreachable'
        | 'reauth_required'
        | 'revoked';
      /** Format: date-time */
      expiresAt: string;
      identity?: components['schemas']['ShellAuthIdentity'];
      /** @enum {string} */
      reason?:
        | 'user_action'
        | 'expired'
        | 'offline'
        | 'source_disabled'
        | 'account_changed'
        | 'shell_restarted'
        | 'revalidation_rejected'
        | 'revalidation_unreachable'
        | 'user_logout'
        | 'admin_disabled'
        | 'member_disabled';
      /** @description 仅authenticated且身份匹配时的可选只读展示资料，注销或切换身份立即清除 */
      uacLoginInfo?: components['schemas']['UacLoginInfo'];
    } & (unknown & unknown & unknown);
    ShellAuthIdentity: {
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      memberId: string;
      /** Format: uuid */
      deviceId: string;
      /** Format: uuid */
      providerId: string;
      empNo?: string;
      uacSessionBinding?: components['schemas']['UacSessionBinding'];
    };
    ShellAuthCommand: {
      /** @enum {integer} */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      shellEpoch: string;
      expectedRevision: number;
      /** @enum {string} */
      action:
        'snapshot' | 'subscribe' | 'open_login' | 'cancel_login' | 'logout';
    };
    ShellAuthEvent: {
      /** @enum {integer} */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** @enum {string} */
      type: 'snapshot' | 'changed' | 'ack' | 'error';
      snapshot?: components['schemas']['ShellAuthSnapshot'];
      /** @enum {string} */
      code?:
        | 'common.conflict'
        | 'common.unauthenticated'
        | 'common.forbidden'
        | 'common.unavailable'
        | 'common.validation_failed';
    } & (unknown & unknown);
    UacSessionBinding: {
      /** Format: uuid */
      sourceSessionId: string;
      authGeneration: number;
      /** Format: uuid */
      providerId: string;
    };
    /** @description 当前认证会话的展示资料。purpose固定login_info。available时必须具有department和updatedAt，pending/unavailable时省略department，由生产方执行状态关联校验。该资料不参与授权或复验截止计算。 */
    UacLoginInfo: {
      /** @constant */
      purpose: 'login_info';
      /** @enum {string} */
      status: 'pending' | 'available' | 'unavailable';
      /** @description 仅available时展示，禁止写入普通日志和诊断包 */
      department?: string;
      /**
       * Format: date-time
       * @description 该次信息查询状态持久化的时间，不作为复验时钟
       */
      updatedAt?: string;
    } & unknown;
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
  };
  responses: never;
  parameters: never;
  requestBodies: never;
  headers: never;
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  exchangeShellAuthMessage: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthCommand'];
      };
    };
    responses: {
      /** @description 草案响应 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthEvent'];
        };
      };
      /** @description 请求失败，使用既有 common.* 错误码 */
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
}
