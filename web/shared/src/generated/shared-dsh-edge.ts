export interface paths {
  '/_auriga/placement-ticket': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 原子兑换共享票据并建立实例cookie
     * @description 只接受JSON body，不从query/cookie/外部身份头取票。票据本身是唯一认证材料。
     *     验证签名、用途、60秒期限、当前成员及平台会话有效性、完整Binding和epoch。
     *     domain实例Origin或IP实例路径必须与签名physicalInstanceId及可信部署映射匹配。
     *     验证成功后原子消费jti；仅一个并发请求成功，后续重放返回409，不复发cookie。
     *     按部署契约下发HttpOnly实例cookie，服务端绑定完整Binding/platformSessionId。
     *     Location只可为domain的/或IP的/i/<physicalInstanceId>/，不接收returnUrl。
     *     响应Cache-Control=no-store，所有成功/错误均不得回显票据；日志脱敏。
     *     本端点不调用业务写操作，票据授权只用于建立实例会话，不代替后续Origin/CSRF检查。
     */
    post: operations['exchangeDshPlacementTicket'];
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
    DshPlacementTicketExchangeRequest: components['schemas']['TicketExchangeRequest'];
    TicketExchangeRequest: {
      ticket: string;
    };
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
  exchangeDshPlacementTicket: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['TicketExchangeRequest'];
      };
    };
    responses: {
      /** @description 票据已消费；cookie设置成功，跳至正常实例页面，无响应body。 */
      302: {
        headers: {
          Location: string;
          /** @description 沿deployment/access契约，domain与IP模式的名称/Path/Secure规则保持一致。 */
          'Set-Cookie': string;
          'Cache-Control': 'no-store';
          [name: string]: unknown;
        };
        content?: never;
      };
      /** @description body缺失、额外字段、非法票据形状或不支持的输入，common.validation_failed。 */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 签名/用途无效或已过期；不签cookie。 */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 成员/会话已撤销或Binding与入口不匹配；不签cookie。 */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 票据已被消费、分配未ready或切换中，common.conflict；不复发cookie。 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 权威状态或原子消费存储不可用，common.unavailable；失败关闭。 */
      503: {
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
