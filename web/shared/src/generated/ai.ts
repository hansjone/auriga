export interface paths {
  '/api/ai/v1/admin/providers': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取当前租户脱敏供应商目录。 */
    get: operations['listAiProviders'];
    put?: never;
    /** @description 登记受信端点；客户端不可自选代理或认证头。 */
    post: operations['createAiProvider'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/admin/providers/{providerId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 仅脱敏元数据。 */
    get: operations['getAiProvider'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    /** @description 修改端点与限额，ETag防覆盖。 */
    patch: operations['patchAiProvider'];
    trace?: never;
  };
  '/api/ai/v1/admin/providers/{providerId}/credentials': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 只写加密凭据，初始staged；无读取secret接口。 */
    post: operations['createAiCredential'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/admin/providers/{providerId}/credentials/{credentialId}/activate': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 新请求只用新key，原流可按旧key完成。 */
    post: operations['activateAiCredential'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/admin/providers/{providerId}/credentials/{credentialId}/revoke': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 紧急撤销取消旧key上的流，禁止新准入。 */
    post: operations['revokeAiCredential'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/admin/models': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 管理模型目录。 */
    get: operations['listAiManagedModels'];
    put?: never;
    /** @description 登记受限模型别名和单一币种价格快照。 */
    post: operations['createAiModel'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/admin/models/{modelId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取当前租户的模型配置；ETag 用于此模型的条件更新。 */
    get: operations['getAiManagedModel'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    /** @description 配额/价格改变仅影响新准入，既有账本价格固定。 */
    patch: operations['patchAiModel'];
    trace?: never;
  };
  '/api/ai/v1/admin/models/{modelId}/grants': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取当前租户模型的完整授权集合；ETag 属于整个授权集合，用于替换授权，不可使用模型 ETag。 */
    get: operations['getAiModelGrants'];
    /** @description 整体替换租户内ACL，核验成员/组/角色均同租户且不超本人授予上限。 */
    put: operations['replaceAiModelGrants'];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/me/usage': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 仅本人；不含prompt或正文；门户会话可以读取本人用量。 门户/管理台读取使用auriga audience；受信内部instance或设备本人用量使用专用AI audience与同等在线绑定检查，不允许普通业务服务混收instance。 */
    get: operations['listOwnAiUsage'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/admin/usage': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 当前租户用量，不能跨租户。 */
    get: operations['listTenantAiUsage'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/ai/v1/models': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 仅返回本主体当前可用且已授权的模型；device权限映射见catalog。 */
    get: operations['listAiModels'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/ai/v1/chat/completions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description JSON或SSE，一次上游提交。幂等例外与断流未知计费见ai/README.md。 */
    post: operations['createAiChatCompletion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/ai/v1/admin/providers/{providerId}/credentials/{credentialId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取当前租户指定供应商的脱敏凭据元数据，不返回 secret；ETag 用于此凭据的激活和撤销。 */
    get: operations['getAiCredential'];
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
    Provider: {
      /** Format: uuid */
      id: string;
      name: string;
      /** @enum {string} */
      kind: 'openai_compatible';
      /**
       * Format: uri
       * @description 由受信管理者配置；默认禁止内网/重定向。显式部署的gateway端点例外另审，不能放宽实例出网。
       */
      baseUrl: string;
      enabled: boolean;
      /** Format: int64 */
      connectTimeoutSeconds: number;
      /** Format: int64 */
      firstByteTimeoutSeconds: number;
      /** Format: int64 */
      idleTimeoutSeconds: number;
      /** Format: int64 */
      totalTimeoutSeconds: number;
      /** Format: int64 */
      maxConcurrency: number;
      /** Format: uuid */
      activeCredentialId: string | null;
      /** Format: int64 */
      revision: number;
      /** @description 当前资源的强 ETag（含双引号），与同一版本单项 GET 的 ETag 响应头完全一致，可原样用于该资源的 If-Match。 */
      readonly etag: string;
    };
    ProviderInput: {
      name: string;
      /** @enum {string} */
      kind: 'openai_compatible';
      /**
       * Format: uri
       * @description 由受信管理者配置；默认禁止内网/重定向。显式部署的gateway端点例外另审，不能放宽实例出网。
       */
      baseUrl: string;
      enabled: boolean;
      /** Format: int64 */
      connectTimeoutSeconds: number;
      /** Format: int64 */
      firstByteTimeoutSeconds: number;
      /** Format: int64 */
      idleTimeoutSeconds: number;
      /** Format: int64 */
      totalTimeoutSeconds: number;
      /** Format: int64 */
      maxConcurrency: number;
    };
    ProviderPatch: {
      name?: string;
      /** @enum {string} */
      kind?: 'openai_compatible';
      /**
       * Format: uri
       * @description 由受信管理者配置；默认禁止内网/重定向。显式部署的gateway端点例外另审，不能放宽实例出网。
       */
      baseUrl?: string;
      enabled?: boolean;
      /** Format: int64 */
      connectTimeoutSeconds?: number;
      /** Format: int64 */
      firstByteTimeoutSeconds?: number;
      /** Format: int64 */
      idleTimeoutSeconds?: number;
      /** Format: int64 */
      totalTimeoutSeconds?: number;
      /** Format: int64 */
      maxConcurrency?: number;
    };
    ProviderPage: {
      items: components['schemas']['Provider'][];
      nextCursor?: string;
    };
    CredentialInput: {
      /** @description 仅写；不得进入任何响应、审计正文、实例规格或浏览器。 */
      secret: string;
    };
    Credential: {
      /** Format: uuid */
      id: string;
      /** @enum {string} */
      state: 'staged' | 'active' | 'retiring' | 'revoked';
      /** Format: date-time */
      createdAt: string;
      /** Format: int64 */
      revision: number;
    };
    CredentialAction: {
      reason: string;
    };
    Price: {
      currency: string;
      /** Format: int64 */
      inputMicrosPerMillionTokens: number;
      /** Format: int64 */
      outputMicrosPerMillionTokens: number;
      version: string;
      /** Format: date-time */
      effectiveAt: string;
    };
    Model: {
      id: string;
      /** Format: uuid */
      providerId: string;
      upstreamModel: string;
      displayName: string;
      enabled: boolean;
      /** Format: int64 */
      contextWindow: number;
      /** Format: int64 */
      maxOutputTokens: number;
      price: components['schemas']['Price'];
      /** Format: int64 */
      revision: number;
      /** @description 当前资源的强 ETag（含双引号），与同一版本单项 GET 的 ETag 响应头完全一致，可原样用于该资源的 If-Match。 */
      readonly etag: string;
    };
    ModelInput: {
      id: string;
      /** Format: uuid */
      providerId: string;
      upstreamModel: string;
      displayName: string;
      enabled: boolean;
      /** Format: int64 */
      contextWindow: number;
      /** Format: int64 */
      maxOutputTokens: number;
      price: components['schemas']['Price'];
    };
    ModelPatch: {
      /** Format: uuid */
      providerId?: string;
      upstreamModel?: string;
      displayName?: string;
      enabled?: boolean;
      /** Format: int64 */
      contextWindow?: number;
      /** Format: int64 */
      maxOutputTokens?: number;
      price?: components['schemas']['Price'];
    };
    ModelPage: {
      items: components['schemas']['Model'][];
      nextCursor?: string;
    };
    Grant: {
      /** @enum {string} */
      kind: 'member' | 'group' | 'role';
      /** Format: uuid */
      id: string;
    };
    ModelGrants: {
      items: components['schemas']['Grant'][];
    };
    Usage: {
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      accountId: string;
      modelId: string;
      /** @enum {string} */
      outcome:
        | 'reserved'
        | 'dispatching'
        | 'completed'
        | 'failed'
        | 'cancelled'
        | 'unknown';
      /** @enum {string} */
      usageQuality: 'confirmed' | 'estimated' | 'unknown';
      /** Format: int64 */
      inputTokens: number | null;
      /** Format: int64 */
      outputTokens: number | null;
      /** Format: int64 */
      costMicros: number | null;
      /** Format: int64 */
      frozenMicros: number;
      /** Format: int64 */
      frozenTokens: number;
      currency: string;
      /** Format: date-time */
      dayPeriodStart: string;
      /** Format: date-time */
      monthPeriodStart: string;
      priceVersion: string;
      /** Format: date-time */
      createdAt: string;
    };
    UsagePage: {
      items: components['schemas']['Usage'][];
      nextCursor?: string;
    };
    ModelList: {
      /** @constant */
      object: 'list';
      data: {
        id: string;
        /** @constant */
        object: 'model';
        /** Format: int64 */
        created: number;
        /** @constant */
        owned_by: 'auriga';
      }[];
    };
    ChatMessage: {
      /** @enum {string} */
      role: 'system' | 'user' | 'assistant' | 'tool';
      content: string | null;
      name?: string;
      tool_call_id?: string;
      tool_calls?: components['schemas']['ToolCall'][];
    };
    ToolCall: {
      id: string;
      /** @constant */
      type: 'function';
      function: {
        name: string;
        arguments: string;
      };
    };
    Tool: {
      /** @constant */
      type: 'function';
      function: {
        name: string;
        description?: string;
        parameters: {
          [key: string]: unknown;
        };
      };
    };
    ChatCompletionRequest: {
      model: string;
      messages: components['schemas']['ChatMessage'][];
      /** @default false */
      stream: boolean;
      /** Format: int64 */
      max_tokens: number;
      temperature?: number;
      top_p?: number;
      /** @description 首批仅接受最多4个字符串的数组，不接受单字符串stop。 */
      stop?: string[];
      tools?: components['schemas']['Tool'][];
      /** @enum {string} */
      tool_choice?: 'none' | 'auto' | 'required';
    };
    TokenUsage: {
      /** Format: int64 */
      prompt_tokens: number;
      /** Format: int64 */
      completion_tokens: number;
      /** Format: int64 */
      total_tokens: number;
    };
    ChatCompletion: {
      id: string;
      /** @constant */
      object: 'chat.completion';
      /** Format: int64 */
      created: number;
      model: string;
      choices: {
        /** Format: int64 */
        index: number;
        message: components['schemas']['AssistantMessage'];
        /** @enum {string} */
        finish_reason: 'stop' | 'length' | 'tool_calls' | 'content_filter';
      }[];
      usage: components['schemas']['TokenUsage'];
    };
    ToolCallDelta: {
      /** Format: int64 */
      index: number;
      id?: string;
      /** @constant */
      type?: 'function';
      function?: {
        name?: string;
        arguments?: string;
      };
    };
    ChatCompletionChunk: {
      id: string;
      /** @constant */
      object: 'chat.completion.chunk';
      /** Format: int64 */
      created: number;
      model: string;
      choices: {
        /** Format: int64 */
        index: number;
        delta: {
          /** @enum {string} */
          role?: 'assistant';
          content?: string | null;
          tool_calls?: components['schemas']['ToolCallDelta'][];
        };
        /** @enum {string|null} */
        finish_reason:
          'stop' | 'length' | 'tool_calls' | 'content_filter' | null;
      }[];
      usage?: components['schemas']['TokenUsage'];
    };
    AiError: {
      error: {
        code: string;
        message: string;
        /** @enum {string} */
        type:
          | 'authentication_error'
          | 'permission_error'
          | 'invalid_request_error'
          | 'rate_limit_error'
          | 'upstream_error'
          | 'conflict_error'
          | 'internal_error';
      };
      requestId: string;
      originalRequestId?: string;
      /** @enum {string} */
      outcome?: 'completed' | 'failed' | 'cancelled' | 'unknown';
    };
    /** @description 部署配置，不接受数据面覆盖。按(tid,sub)跨设备/实例统一原子执行。周期不限时对应正数绝对上限必填；unknown预占继续占用；0配额禁止。 */
    AdmissionPolicy: {
      /**
       * Format: int64
       * @default 2
       */
      maxActiveRequestsPerSubject: number;
      /**
       * Format: int64
       * @default 25
       */
      maxInFlightPercent: number;
      /** Format: int64 */
      maxInFlightMicros?: number;
      /** Format: int64 */
      maxInFlightTokens?: number;
    };
    AssistantMessage: {
      /** @constant */
      role: 'assistant';
      content: string | null;
      tool_calls?: components['schemas']['ToolCall'][];
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
  parameters: {
    Limit: number;
    /** @description 上一页响应里的 nextCursor。 */
    Cursor: string;
    IdempotencyKey: string;
  };
  requestBodies: never;
  headers: {
    /** @description 资源版本。 */
    ETag: string;
  };
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  listAiProviders: {
    parameters: {
      query?: {
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
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ProviderPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  createAiProvider: {
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
        'application/json': components['schemas']['ProviderInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Provider'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getAiProvider: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Provider'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  patchAiProvider: {
    parameters: {
      query?: never;
      header: {
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        providerId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ProviderPatch'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Provider'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  createAiCredential: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path: {
        providerId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['CredentialInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Credential'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  activateAiCredential: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        providerId: string;
        credentialId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['CredentialAction'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Credential'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  revokeAiCredential: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        providerId: string;
        credentialId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['CredentialAction'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Credential'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listAiManagedModels: {
    parameters: {
      query?: {
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
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ModelPage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  createAiModel: {
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
        'application/json': components['schemas']['ModelInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Model'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getAiManagedModel: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        modelId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Model'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  patchAiModel: {
    parameters: {
      query?: never;
      header: {
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        modelId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ModelPatch'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Model'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  getAiModelGrants: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        modelId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ModelGrants'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  replaceAiModelGrants: {
    parameters: {
      query?: never;
      header: {
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        modelId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ModelGrants'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ModelGrants'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listOwnAiUsage: {
    parameters: {
      query?: {
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
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UsagePage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listTenantAiUsage: {
    parameters: {
      query?: {
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
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['UsagePage'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listAiModels: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ModelList'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.model_not_allowed */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.budget_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
    };
  };
  createAiChatCompletion: {
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
        'application/json': components['schemas']['ChatCompletionRequest'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ChatCompletion'];
          'text/event-stream': string;
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.model_not_allowed */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.request_in_progress, ai.replay_unavailable */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.budget_exceeded */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.upstream_unavailable, ai.upstream_rejected, ai.stream_interrupted */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.budget_unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description ai.upstream_timeout */
      504: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AiError'];
        };
      };
    };
  };
  getAiCredential: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: string;
        credentialId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Credential'];
        };
      };
      /** @description common.validation_failed */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unauthenticated */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.forbidden */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.not_found */
      404: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.conflict */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.precondition_failed */
      412: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.rate_limited */
      429: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.internal */
      500: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description common.unavailable */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      /** @description 未知失败使用脱敏common.internal；具体错误与HTTP/gRPC映射见catalog/m3.json。 */
      default: {
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
