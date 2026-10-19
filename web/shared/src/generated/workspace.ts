export interface paths {
  '/me/instances': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listMyInstances'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/instance/open': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 打开我的默认实例
     * @description 没有实例时按配额创建；已停止时启动（desired=running）。立即返回，前端用 `/instances/{id}/events` 等 ready，
     *     然后跳到 entryUrl。准入满时返回 429 `workspace.admission_full` 和 Retry-After。
     */
    post: operations['openMyInstance'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/instances/{instanceId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    /** @description 主人或有 workspace.instances.read 权限的人可以查看。 */
    get: operations['getInstance'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/instances/{instanceId}/events': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    /**
     * 订阅实例状态（SSE）
     * @description 每次状态变化发 `event: instance`，data 是 Instance 的 JSON。连接建立时先发一次当前状态。
     */
    get: operations['streamInstance'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/instances/{instanceId}/start': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['startInstance'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/instances/{instanceId}/stop': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['stopInstance'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/instances/{instanceId}/restart': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 有待重启的变更（pendingRestart）时，用户可以自己选时机重启。 */
    post: operations['restartInstance'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/instances': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listInstances'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/instances/{instanceId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['deleteInstance'];
    options?: never;
    head?: never;
    patch: operations['updateInstance'];
    trace?: never;
  };
  '/admin/nodes': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listNodes'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/nodes/{nodeId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        nodeId: components['parameters']['NodeId'];
      };
      cookie?: never;
    };
    get: operations['getNode'];
    put?: never;
    post?: never;
    /** @description 吊销节点证书。节点上还有实例时 409 workspace.node_not_empty（先 drain 并迁走）。 */
    delete: operations['revokeNode'];
    options?: never;
    head?: never;
    patch: operations['updateNode'];
    trace?: never;
  };
  '/admin/nodes/{nodeId}/drain': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        nodeId: components['parameters']['NodeId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 不再调度新实例到这个节点；已有实例在空闲时迁走。 */
    post: operations['drainNode'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/join-tokens': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listJoinTokens'];
    put?: never;
    post: operations['createJoinToken'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/join-tokens/{tokenId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['deleteJoinToken'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/runtimes': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listRuntimes'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/runtimes/default': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** @description 切换默认 DSH 版本。已有实例的规格代数加一并标为 pendingRestart，按灰度策略在空闲时重启。 */
    put: operations['setDefaultRuntime'];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/instance/self': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getInstanceSelf'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/instance/heartbeat': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** 插件上报活动（每 30 秒，以及计数变化时） */
    post: operations['postInstanceHeartbeat'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/connect/manifest': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * 连接模式插件的接入清单
     * @description 需要 ptyp=device 的访问令牌（设备授权流程获得）。
     */
    get: operations['getConnectManifest'];
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
    /**
     * @description 与 NodeControl 的 InstancePhase 对应；scheduling 是还没分配到节点。
     * @enum {string}
     */
    InstancePhase:
      | 'scheduling'
      | 'absent'
      | 'preparing'
      | 'migrating'
      | 'starting'
      | 'running'
      | 'pending_restart'
      | 'stopping'
      | 'stopped'
      | 'deleting'
      | 'failed';
    Resources: {
      cpuMillis?: number;
      memoryMb?: number;
      diskGb?: number;
    };
    Instance: {
      id: components['schemas']['Uuid'];
      tenantId: components['schemas']['Uuid'];
      ownerId: components['schemas']['Uuid'];
      name: string;
      /** @enum {string} */
      runtime: 'docker' | 'linux_process' | 'windows_process';
      /** @enum {string} */
      desired: 'running' | 'stopped';
      phase: components['schemas']['InstancePhase'];
      ready: boolean;
      generation: number;
      observedGeneration: number;
      pendingRestart: boolean;
      pendingRestartReasons?: (
        'dsh_version' | 'plugins' | 'config' | 'resources'
      )[];
      alwaysOn: boolean;
      dshVersion: string;
      resources: components['schemas']['Resources'];
      node: {
        id: components['schemas']['Uuid'];
        name: string;
      } | null;
      error: {
        code: string;
        message?: string;
        attempts?: number;
      } | null;
      /** Format: date-time */
      lastActivityAt?: string;
      /**
       * Format: date-time
       * @description 按当前闲置状态预计的回收时间。
       */
      idleStopAt?: string;
      /** Format: date-time */
      nextWakeAt?: string;
      /** @description 浏览器入口，形如 `/i/<实例 id>/`。 */
      entryUrl: string;
      /** Format: date-time */
      createdAt: string;
      /** Format: date-time */
      updatedAt: string;
    };
    OpenResult: {
      instance: components['schemas']['Instance'];
      created: boolean;
    };
    Node: {
      id: components['schemas']['Uuid'];
      name: string;
      hostname: string;
      /** @enum {string} */
      os: 'linux' | 'windows';
      /** @enum {string} */
      arch: 'amd64' | 'arm64';
      agentVersion: string;
      drivers: ('docker' | 'linux_process' | 'windows_process')[];
      /** @enum {string} */
      status: 'online' | 'offline' | 'draining' | 'revoked';
      schedulable: boolean;
      labels: {
        [key: string]: string;
      };
      capacity: {
        cpuMillis?: number;
        memoryMb?: number;
        diskGb?: number;
        maxInstances?: number;
      };
      usage?: {
        cpuMillisUsed?: number;
        memoryMbUsed?: number;
        diskGbUsed?: number;
        instancesRunning?: number;
        instancesTotal?: number;
      };
      /** @description 绝对值超过 5000 时管理台告警。 */
      clockSkewMs?: number;
      /** Format: date-time */
      lastSeenAt?: string;
      /** Format: date-time */
      certificateExpiresAt?: string;
      /** Format: date-time */
      joinedAt: string;
    };
    JoinToken: {
      id: components['schemas']['Uuid'];
      name?: string;
      /** Format: date-time */
      expiresAt: string;
      createdBy: components['schemas']['Uuid'];
      /** Format: date-time */
      createdAt: string;
    };
    Runtime: {
      /** @description DSH 版本。 */
      version: string;
      artifacts: {
        artifactId: string;
        /** @enum {string} */
        os: 'linux' | 'windows';
        /** @enum {string} */
        arch: 'amd64' | 'arm64';
        digest: string;
        sizeBytes?: number;
        /** @description Docker 驱动的实例镜像（带 digest）。 */
        image?: string;
      }[];
      /** @description 正在使用该版本的实例数。 */
      instances: number;
      /** Format: date-time */
      createdAt: string;
    };
    ProtocolInfo: {
      current: number;
      minSupported: number;
    };
    HeartbeatRequest: {
      pluginVersion: string;
      dshVersion: string;
      /** @description 插件看到的活动；workspace 另外合并节点代理观测到的 CPU 活动。四个条件都为空闲才算闲置。 */
      activity: {
        /** @description 正在执行的对话轮次。 */
        runningTurns: number;
        openTerminals: number;
        /** @description 在线的 IM 机器人连接。 */
        activeImBots: number;
        /** @description 打开着的浏览器 / 桌面端连接。 */
        connectedClients: number;
        /**
         * Format: date-time
         * @description 最近一次用户操作。
         */
        lastInteractionAt?: string;
      };
      health: {
        ok: boolean;
        problems?: string[];
      };
    };
    HeartbeatResponse: {
      /** Format: date-time */
      serverTime: string;
      protocol: components['schemas']['ProtocolInfo'];
      /** @default 30 */
      nextHeartbeatSeconds: number;
      /** @description 有待重启的变更，插件可以在界面提示用户。 */
      pendingRestart?: boolean;
    };
    ConnectManifest: {
      protocol: components['schemas']['ProtocolInfo'];
      account: {
        id: components['schemas']['Uuid'];
        displayName: string;
        tenantName: string;
      };
      /** @description 键是写进 DSH 配置的名字。用访问令牌鉴权（Authorization: Bearer）。 */
      mcpServers: {
        [key: string]: {
          /** Format: uri */
          url: string;
        };
      };
      /** @description 键是写进 DSH 配置的名字。OpenAI 兼容接口，用访问令牌鉴权。 */
      modelProviders: {
        [key: string]: {
          /** Format: uri */
          baseUrl: string;
          models: {
            id: string;
            displayName?: string;
            contextWindow?: number;
          }[];
        };
      };
      panel: {
        /** Format: uri */
        manifestUrl: string;
        /** @description 面板签名公钥（JWKS 子集，同 identity-keys.schema.json 的 keys）。 */
        keys: Record<string, never>[];
      };
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
  };
  responses: {
    /** @description 插件协议版本过旧（plugin.protocol_unsupported），params 里有 current 和 minSupported */
    ProtocolUnsupported: {
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
    /** @description 资源不存在（common.not_found）。 */
    NotFound: {
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
    /** @description 状态冲突（common.conflict 等）。 */
    Conflict: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
    /** @description 没有登录或凭据无效（common.unauthenticated）。 */
    Unauthenticated: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
  };
  parameters: {
    InstanceId: components['schemas']['Uuid'];
    NodeId: components['schemas']['Uuid'];
    /** @description 插件协议主版本。 */
    AurigaProtocol: number;
    Limit: number;
    /** @description 上一页响应里的 nextCursor。 */
    Cursor: string;
  };
  requestBodies: never;
  headers: {
    /** @description 多少秒后重试。 */
    RetryAfter: number;
  };
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  listMyInstances: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 我的实例（目前每人一个） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Instance'][];
          };
        };
      };
    };
  };
  openMyInstance: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 实例（可能还在启动） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['OpenResult'];
        };
      };
      /** @description 账号停用、外部协作者不允许实例等（workspace.not_allowed） */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
      429: components['responses']['RateLimited'];
    };
  };
  getInstance: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 实例 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Instance'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  streamInstance: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 事件流 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'text/event-stream': string;
        };
      };
    };
  };
  startInstance: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已接受 */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Instance'];
        };
      };
      429: components['responses']['RateLimited'];
    };
  };
  stopInstance: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已接受 */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Instance'];
        };
      };
    };
  };
  restartInstance: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已接受 */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Instance'];
        };
      };
    };
  };
  listInstances: {
    parameters: {
      query?: {
        ownerId?: components['schemas']['Uuid'];
        nodeId?: components['schemas']['Uuid'];
        phase?: components['schemas']['InstancePhase'];
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
      /** @description 实例列表 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Instance'][];
            nextCursor?: string;
          };
        };
      };
    };
  };
  deleteInstance: {
    parameters: {
      query?: {
        purgeData?: boolean;
      };
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已接受（发 instance.deleted） */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  updateInstance: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        instanceId: components['parameters']['InstanceId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': {
          alwaysOn?: boolean;
          resourcesOverride?: components['schemas']['Resources'];
          /** @description 固定 DSH 版本；null 表示跟随默认。 */
          dshVersionPin?: string | null;
          /**
           * Format: uuid
           * @description 迁移到指定节点（下次启动时生效）。
           */
          nodeId?: string | null;
        };
      };
    };
    responses: {
      /** @description 已更新（规格代数加一；需要重启的变化标为 pendingRestart） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Instance'];
        };
      };
      400: components['responses']['BadRequest'];
    };
  };
  listNodes: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 节点 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Node'][];
          };
        };
      };
    };
  };
  getNode: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        nodeId: components['parameters']['NodeId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 节点 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Node'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  revokeNode: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        nodeId: components['parameters']['NodeId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已吊销 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      409: components['responses']['Conflict'];
    };
  };
  updateNode: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        nodeId: components['parameters']['NodeId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': {
          name?: string;
          labels?: {
            [key: string]: string;
          };
          schedulable?: boolean;
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
          'application/json': components['schemas']['Node'];
        };
      };
    };
  };
  drainNode: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        nodeId: components['parameters']['NodeId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已接受 */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Node'];
        };
      };
    };
  };
  listJoinTokens: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 未用、未过期的入网令牌（不含令牌值） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['JoinToken'][];
          };
        };
      };
    };
  };
  createJoinToken: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: {
      content: {
        'application/json': {
          name?: string;
          /** @default 60 */
          ttlMinutes?: number;
        };
      };
    };
    responses: {
      /** @description 已创建。token 和 command 只返回这一次 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['JoinToken'] & {
            token: string;
            /** @description 平台 CA 证书的 SHA-256 指纹（十六进制）。 */
            caSha256: string;
            /** @description 例如 `auriga-agent join --server https://… --token … --ca-sha256 …`。 */
            command: string;
          };
        };
      };
    };
  };
  deleteJoinToken: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        tokenId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已作废 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  listRuntimes: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 可用的 DSH 运行时制品和默认版本 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            defaultVersion: string;
            items: components['schemas']['Runtime'][];
          };
        };
      };
    };
  };
  setDefaultRuntime: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          version: string;
        };
      };
    };
    responses: {
      /** @description 已切换 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      400: components['responses']['BadRequest'];
    };
  };
  getInstanceSelf: {
    parameters: {
      query?: never;
      header: {
        /** @description 插件协议主版本。 */
        'Auriga-Protocol': components['parameters']['AurigaProtocol'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 本实例的身份和平台信息 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            instanceId: components['schemas']['Uuid'];
            ownerId: components['schemas']['Uuid'];
            tenantId: components['schemas']['Uuid'];
            generation: number;
            protocol: components['schemas']['ProtocolInfo'];
          };
        };
      };
      401: components['responses']['Unauthenticated'];
      426: components['responses']['ProtocolUnsupported'];
    };
  };
  postInstanceHeartbeat: {
    parameters: {
      query?: never;
      header: {
        /** @description 插件协议主版本。 */
        'Auriga-Protocol': components['parameters']['AurigaProtocol'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['HeartbeatRequest'];
      };
    };
    responses: {
      /** @description 已收到 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['HeartbeatResponse'];
        };
      };
      401: components['responses']['Unauthenticated'];
      426: components['responses']['ProtocolUnsupported'];
    };
  };
  getConnectManifest: {
    parameters: {
      query?: never;
      header: {
        /** @description 插件协议主版本。 */
        'Auriga-Protocol': components['parameters']['AurigaProtocol'];
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 清单 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ConnectManifest'];
        };
      };
      401: components['responses']['Unauthenticated'];
      426: components['responses']['ProtocolUnsupported'];
    };
  };
}
