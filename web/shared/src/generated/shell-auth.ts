export interface paths {
  '/host/contexts': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 登记或撤销可信宿主上下文并交还句柄
     * @description 仅OS身份/受控进程/连接能力均通过的插件后端IPC投影；不是HTTP路由，页面/模型/skill禁止调用。owner从已验证连接派生，不信任消息字段。详见../plugin/shell-auth-host.md。
     */
    post: operations['exchangeShellAuthHostContext'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/host/credentials': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 插件后端读取本人最新企业凭据
     * @description 仅OS身份/受控进程/连接能力均通过的插件后端IPC投影；不是HTTP路由，页面/模型/skill禁止调用。owner从已验证连接派生，不信任消息字段。详见../plugin/shell-auth-host.md。
     */
    post: operations['readShellAuthHostCredentials'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
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
  '/business/messages': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 受控本机端口投影
     * @description 经OS认证的IPC投影，业务失败在error分支；不注册公网HTTP。流注册返回streamId，订阅持续发送ShellAuthStreamEvent，结束/取消/错误关闭。
     */
    post: operations['exchangeShellAuthBusinessCall'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/business/events': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 订阅已注册流的IPC事件
     * @description 经OS认证的IPC投影，不注册HTTP路由。只订阅当前owner/epoch/revision的streamId，订阅不增加credit；持续推送有序事件，唯一end/closed/error后结束。upload_ack不消耗下行credit。一个streamId只允许一个订阅，断线终止该流，不能重新消费旧流。
     */
    post: operations['subscribeShellAuthBusinessStream'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/business/streams': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 受控本机端口投影
     * @description 经OS认证的IPC投影，不注册HTTP路由。发送credit/cancel/close/upload_chunk控制已注册流；接受后事件经/business/events推送，控制响应只投影相应确认或终止事件。
     */
    post: operations['exchangeShellAuthStreamCommand'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/loopback/exchange': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 受控本机端口投影
     * @description 127.0.0.1端口绑定控制页HTTP；精确Host/Origin及页面双令牌/票据校验，禁止跨源。
     */
    post: operations['exchangeShellAuthLoopbackExchange'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/dsh-team/agent-credentials': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /**
     * 兼容企业 skill 原始读取
     * @description 仅真实回环HTTP对端及本人DSH任务能力；不是OS IPC。身份/owner/scope在线核验，规范见shell-auth-control.md。
     */
    get: operations['getShellAuthAgentCredentials'];
    put?: never;
    /**
     * 受控本机端口投影
     * @description 仅真实回环HTTP对端及本人DSH任务能力；不是OS IPC。身份/owner/scope在线核验，规范见shell-auth-control.md。
     */
    post: operations['exchangeShellAuthAgentCredentialsRequest'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/dsh-team/outbound': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 受控本机端口投影
     * @description 仅真实回环HTTP对端及本人DSH任务能力；不是OS IPC。身份/owner/scope在线核验，规范见shell-auth-control.md。
     */
    post: operations['exchangeShellAuthOutboundRequest'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/loopback/messages': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 端口绑定页面控制命令
     * @description 127.0.0.1端口绑定控制页HTTP；精确Host/Origin及页面双令牌/票据校验，禁止跨源。
     */
    post: operations['exchangeShellAuthPageMessage'];
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
    ShellAuthStreamSubscription: {
      /** Format: uuid */
      streamId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      expectedRevision: number;
    };
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
      deviceAttempt?: components['schemas']['ShellAuthDeviceAttempt'];
    } & (unknown & unknown & unknown & unknown);
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
    ShellAuthLoopbackExchange: {
      ticket: string;
      clientNonce: string;
    };
    ShellAuthPageSession: {
      pageToken: string;
      csrf: string;
      /** Format: uuid */
      shellEpoch: string;
      origin: string;
      port: number;
      clientNonce: string;
      /** Format: date-time */
      expiresAt: string;
      /**
       * Format: uuid
       * @description broker为票据交换生成的页面归属ID，设备尝试只回送同一owner；不作为令牌且不接受命令自报。
       */
      pageOwner?: string;
    };
    ShellAuthEnterpriseConfig: {
      /** @default true */
      allowRawAgentToken: boolean;
      /**
       * @description 缺省或空数组均拒绝全部；真实主机仅私有部署配置提供。
       * @default []
       */
      outboundAllowedHosts: string[];
      /**
       * @default 600
       * @constant
       */
      taskCapabilityTtlSeconds: 600;
    };
    ShellAuthAgentCredentialsRequest: {
      sessionId?: string;
      dshSessionId?: string;
      taskCapability?: string;
      capability?: string;
    };
    ShellAuthAgentCredentials: {
      empNo: string;
      token: string;
      /** Format: date-time */
      updatedAt: string | null;
    };
    ShellAuthOutboundRequest: {
      /** Format: uri */
      url: string;
      /**
       * @default POST
       * @enum {string}
       */
      method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD';
      /** @description 兼容原型输入；仅content-type/accept/accept-language/x-lang-id/x-requested-with不分大小写保留，非空值String转换并截为1024字符，其余静默丢弃。 */
      headers?: {
        [key: string]: unknown;
      };
      body?: unknown;
      /** @description 原型兼容：有效超时=min(Number(value)||30000,60000)，超过60000截断而不是拒绝。 */
      timeoutMs?: unknown;
      sessionId?: string;
      dshSessionId?: string;
      taskCapability?: string;
      capability?: string;
    };
    ShellAuthOutboundResult: {
      statusCode: number;
      headers: {
        [key: string]: string;
      };
      body: string;
      json: unknown;
    };
    ShellAuthAgentError: {
      error: string;
      message: string;
      hint?: string;
      sessionId?: string | null;
    };
    ShellAuthBusinessCall: {
      /** @constant */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      expectedRevision: number;
      /** Format: date-time */
      expiresAt: string;
      /** Format: uuid */
      idempotencyKey?: string;
      call:
        | {
            /** @constant */
            operation: 'listResourcePackages';
            input: components['schemas']['ShellAuthInput_listResourcePackages'];
          }
        | {
            /** @constant */
            operation: 'createResourcePackage';
            input: components['schemas']['ShellAuthInput_createResourcePackage'];
          }
        | {
            /** @constant */
            operation: 'getResourcePackage';
            input: components['schemas']['ShellAuthInput_getResourcePackage'];
          }
        | {
            /** @constant */
            operation: 'patchResourcePackage';
            input: components['schemas']['ShellAuthInput_patchResourcePackage'];
          }
        | {
            /** @constant */
            operation: 'listResourceVersions';
            input: components['schemas']['ShellAuthInput_listResourceVersions'];
          }
        | {
            /** @constant */
            operation: 'uploadResourceVersion';
            input: components['schemas']['ShellAuthInput_uploadResourceVersion'];
          }
        | {
            /** @constant */
            operation: 'getResourceVersion';
            input: components['schemas']['ShellAuthInput_getResourceVersion'];
          }
        | {
            /** @constant */
            operation: 'submitResourceVersion';
            input: components['schemas']['ShellAuthInput_submitResourceVersion'];
          }
        | {
            /** @constant */
            operation: 'requestResourceDistribution';
            input: components['schemas']['ShellAuthInput_requestResourceDistribution'];
          }
        | {
            /** @constant */
            operation: 'getResourceDistribution';
            input: components['schemas']['ShellAuthInput_getResourceDistribution'];
          }
        | {
            /** @constant */
            operation: 'downloadResourceContent';
            input: components['schemas']['ShellAuthInput_downloadResourceContent'];
          }
        | {
            /** @constant */
            operation: 'getResourceRevocations';
            input: components['schemas']['ShellAuthInput_getResourceRevocations'];
          }
        | {
            /** @constant */
            operation: 'createMemberResourceDownloadTicket';
            input: components['schemas']['ShellAuthInput_createMemberResourceDownloadTicket'];
          }
        | {
            /** @constant */
            operation: 'consumeMemberResourceDownloadTicket';
            input: components['schemas']['ShellAuthInput_consumeMemberResourceDownloadTicket'];
          }
        | {
            /** @constant */
            operation: 'listMemberResourceInstallations';
            input: components['schemas']['ShellAuthInput_listMemberResourceInstallations'];
          }
        | {
            /** @constant */
            operation: 'selectMemberResourceInstallation';
            input: components['schemas']['ShellAuthInput_selectMemberResourceInstallation'];
          }
        | {
            /** @constant */
            operation: 'reportMemberResourceInstallation';
            input: components['schemas']['ShellAuthInput_reportMemberResourceInstallation'];
          }
        | {
            /** @constant */
            operation: 'UnifiedListResourcePackages';
            input: components['schemas']['ShellAuthInput_UnifiedListResourcePackages'];
          }
        | {
            /** @constant */
            operation: 'UnifiedCreateResourcePackage';
            input: components['schemas']['ShellAuthInput_UnifiedCreateResourcePackage'];
          }
        | {
            /** @constant */
            operation: 'UnifiedGetResourcePackage';
            input: components['schemas']['ShellAuthInput_UnifiedGetResourcePackage'];
          }
        | {
            /** @constant */
            operation: 'UnifiedListResourceVersions';
            input: components['schemas']['ShellAuthInput_UnifiedListResourceVersions'];
          }
        | {
            /** @constant */
            operation: 'UnifiedUploadResourceVersion';
            input: components['schemas']['ShellAuthInput_UnifiedUploadResourceVersion'];
          }
        | {
            /** @constant */
            operation: 'UnifiedGetResourceVersion';
            input: components['schemas']['ShellAuthInput_UnifiedGetResourceVersion'];
          }
        | {
            /** @constant */
            operation: 'UnifiedSubmitResourceVersion';
            input: components['schemas']['ShellAuthInput_UnifiedSubmitResourceVersion'];
          }
        | {
            /** @constant */
            operation: 'UnifiedDownloadResourceManifest';
            input: components['schemas']['ShellAuthInput_UnifiedDownloadResourceManifest'];
          }
        | {
            /** @constant */
            operation: 'UnifiedCreateDownloadTicket';
            input: components['schemas']['ShellAuthInput_UnifiedCreateDownloadTicket'];
          }
        | {
            /** @constant */
            operation: 'UnifiedDownloadArtifact';
            input: components['schemas']['ShellAuthInput_UnifiedDownloadArtifact'];
          }
        | {
            /** @constant */
            operation: 'UnifiedListInstallations';
            input: components['schemas']['ShellAuthInput_UnifiedListInstallations'];
          }
        | {
            /** @constant */
            operation: 'UnifiedSelectInstallation';
            input: components['schemas']['ShellAuthInput_UnifiedSelectInstallation'];
          }
        | {
            /** @constant */
            operation: 'UnifiedReportInstallation';
            input: components['schemas']['ShellAuthInput_UnifiedReportInstallation'];
          }
        | {
            /** @constant */
            operation: 'UnifiedRenewInstallation';
            input: components['schemas']['ShellAuthInput_UnifiedRenewInstallation'];
          }
        | {
            /** @constant */
            operation: 'UnifiedDownloadVersionArtifact';
            input: components['schemas']['ShellAuthInput_UnifiedDownloadVersionArtifact'];
          }
        | {
            /** @constant */
            operation: 'listOwnAiUsage';
            input: components['schemas']['ShellAuthInput_listOwnAiUsage'];
          }
        | {
            /** @constant */
            operation: 'listAiModels';
            input: components['schemas']['ShellAuthInput_listAiModels'];
          }
        | {
            /** @constant */
            operation: 'createAiChatCompletion';
            input: components['schemas']['ShellAuthInput_createAiChatCompletion'];
          }
        | {
            /** @constant */
            operation: 'listNetworkSystems';
            input: components['schemas']['ShellAuthInput_listNetworkSystems'];
          }
        | {
            /** @constant */
            operation: 'createAiChatCompletionStream';
            input: components['schemas']['ShellAuthInput_createAiChatCompletionStream'];
          }
        | {
            /** @constant */
            operation: 'team_initialize';
            input: components['schemas']['ShellAuthInput_team_initialize'];
          }
        | {
            /** @constant */
            operation: 'team_list_tools';
            input: components['schemas']['ShellAuthInput_team_list_tools'];
          }
        | {
            /** @constant */
            operation: 'team_ping';
            input: components['schemas']['ShellAuthInput_team_ping'];
          }
        | {
            /** @constant */
            operation: 'network_open';
            input: components['schemas']['ShellAuthInput_network_open'];
          }
        | {
            /** @constant */
            operation: 'upload_open';
            input: components['schemas']['ShellAuthInput_upload_open'];
          }
        | {
            /** @constant */
            operation: 'upload_finish';
            input: components['schemas']['ShellAuthInput_upload_finish'];
          }
        | {
            /** @constant */
            operation: 'network_send';
            input: components['schemas']['ShellAuthInput_network_send'];
          }
        | {
            /** @constant */
            operation: 'network_submit';
            input: components['schemas']['ShellAuthInput_network_submit'];
          }
        | {
            /** @constant */
            operation: 'getNetworkKeys';
            input: components['schemas']['ShellAuthInput_getNetworkKeys'];
          }
        | {
            /** @constant */
            operation: 'getNetworkKeyAnnouncement';
            input: components['schemas']['ShellAuthInput_getNetworkKeyAnnouncement'];
          }
        | {
            /** @constant */
            operation: 'profile_read';
            input: components['schemas']['ShellAuthInput_profile_read'];
          }
        | {
            /** @constant */
            operation: 'provider_diagnostic';
            input: components['schemas']['ShellAuthInput_provider_diagnostic'];
          }
        | {
            /** @constant */
            operation: 'team_list_endpoints';
            input: components['schemas']['ShellAuthInput_team_list_endpoints'];
          }
        | {
            /** @constant */
            operation: 'team_open_thread';
            input: components['schemas']['ShellAuthInput_team_open_thread'];
          }
        | {
            /** @constant */
            operation: 'team_reply_thread';
            input: components['schemas']['ShellAuthInput_team_reply_thread'];
          }
        | {
            /** @constant */
            operation: 'team_read_thread';
            input: components['schemas']['ShellAuthInput_team_read_thread'];
          }
        | {
            /** @constant */
            operation: 'team_node_register';
            input: components['schemas']['ShellAuthInput_team_node_register'];
          }
        | {
            /** @constant */
            operation: 'team_node_bind';
            input: components['schemas']['ShellAuthInput_team_node_bind'];
          }
        | {
            /** @constant */
            operation: 'team_node_pull';
            input: components['schemas']['ShellAuthInput_team_node_pull'];
          }
        | {
            /** @constant */
            operation: 'team_node_ack';
            input: components['schemas']['ShellAuthInput_team_node_ack'];
          }
        | {
            /** @constant */
            operation: 'team_node_revoke';
            input: components['schemas']['ShellAuthInput_team_node_revoke'];
          };
    };
    ShellAuthBusinessResult:
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'listResourcePackages';
          output: components['schemas']['ShellAuthOutput_listResourcePackages'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'createResourcePackage';
          output: components['schemas']['ShellAuthOutput_createResourcePackage'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'getResourcePackage';
          output: components['schemas']['ShellAuthOutput_getResourcePackage'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'patchResourcePackage';
          output: components['schemas']['ShellAuthOutput_patchResourcePackage'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'listResourceVersions';
          output: components['schemas']['ShellAuthOutput_listResourceVersions'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'uploadResourceVersion';
          output: components['schemas']['ShellAuthOutput_uploadResourceVersion'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'getResourceVersion';
          output: components['schemas']['ShellAuthOutput_getResourceVersion'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'submitResourceVersion';
          output: components['schemas']['ShellAuthOutput_submitResourceVersion'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'requestResourceDistribution';
          output: components['schemas']['ShellAuthOutput_requestResourceDistribution'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'getResourceDistribution';
          output: components['schemas']['ShellAuthOutput_getResourceDistribution'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'downloadResourceContent';
          output: components['schemas']['ShellAuthOutput_downloadResourceContent'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'getResourceRevocations';
          output: components['schemas']['ShellAuthOutput_getResourceRevocations'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'createMemberResourceDownloadTicket';
          output: components['schemas']['ShellAuthOutput_createMemberResourceDownloadTicket'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'consumeMemberResourceDownloadTicket';
          output: components['schemas']['ShellAuthOutput_consumeMemberResourceDownloadTicket'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'listMemberResourceInstallations';
          output: components['schemas']['ShellAuthOutput_listMemberResourceInstallations'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'selectMemberResourceInstallation';
          output: components['schemas']['ShellAuthOutput_selectMemberResourceInstallation'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'reportMemberResourceInstallation';
          output: components['schemas']['ShellAuthOutput_reportMemberResourceInstallation'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedListResourcePackages';
          output: components['schemas']['ShellAuthOutput_UnifiedListResourcePackages'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedCreateResourcePackage';
          output: components['schemas']['ShellAuthOutput_UnifiedCreateResourcePackage'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedGetResourcePackage';
          output: components['schemas']['ShellAuthOutput_UnifiedGetResourcePackage'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedListResourceVersions';
          output: components['schemas']['ShellAuthOutput_UnifiedListResourceVersions'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedUploadResourceVersion';
          output: components['schemas']['ShellAuthOutput_UnifiedUploadResourceVersion'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedGetResourceVersion';
          output: components['schemas']['ShellAuthOutput_UnifiedGetResourceVersion'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedSubmitResourceVersion';
          output: components['schemas']['ShellAuthOutput_UnifiedSubmitResourceVersion'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedDownloadResourceManifest';
          output: components['schemas']['ShellAuthOutput_UnifiedDownloadResourceManifest'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedCreateDownloadTicket';
          output: components['schemas']['ShellAuthOutput_UnifiedCreateDownloadTicket'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedDownloadArtifact';
          output: components['schemas']['ShellAuthOutput_UnifiedDownloadArtifact'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedListInstallations';
          output: components['schemas']['ShellAuthOutput_UnifiedListInstallations'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedSelectInstallation';
          output: components['schemas']['ShellAuthOutput_UnifiedSelectInstallation'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedReportInstallation';
          output: components['schemas']['ShellAuthOutput_UnifiedReportInstallation'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedRenewInstallation';
          output: components['schemas']['ShellAuthOutput_UnifiedRenewInstallation'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'UnifiedDownloadVersionArtifact';
          output: components['schemas']['ShellAuthOutput_UnifiedDownloadVersionArtifact'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'listOwnAiUsage';
          output: components['schemas']['ShellAuthOutput_listOwnAiUsage'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'listAiModels';
          output: components['schemas']['ShellAuthOutput_listAiModels'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'createAiChatCompletion';
          output: components['schemas']['ShellAuthOutput_createAiChatCompletion'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'listNetworkSystems';
          output: components['schemas']['ShellAuthOutput_listNetworkSystems'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'createAiChatCompletionStream';
          output: components['schemas']['ShellAuthOutput_createAiChatCompletionStream'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_initialize';
          output: components['schemas']['ShellAuthOutput_team_initialize'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_list_tools';
          output: components['schemas']['ShellAuthOutput_team_list_tools'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_ping';
          output: components['schemas']['ShellAuthOutput_team_ping'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'network_open';
          output: components['schemas']['ShellAuthOutput_network_open'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'upload_open';
          output: components['schemas']['ShellAuthOutput_upload_open'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'upload_finish';
          output: components['schemas']['ShellAuthOutput_upload_finish'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'network_send';
          output: components['schemas']['ShellAuthOutput_network_send'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'network_submit';
          output: components['schemas']['ShellAuthOutput_network_submit'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'getNetworkKeys';
          output: components['schemas']['ShellAuthOutput_getNetworkKeys'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'getNetworkKeyAnnouncement';
          output: components['schemas']['ShellAuthOutput_getNetworkKeyAnnouncement'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'profile_read';
          output: components['schemas']['ShellAuthOutput_profile_read'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'provider_diagnostic';
          output: components['schemas']['ShellAuthOutput_provider_diagnostic'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_list_endpoints';
          output: components['schemas']['ShellAuthOutput_team_list_endpoints'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_open_thread';
          output: components['schemas']['ShellAuthOutput_team_open_thread'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_reply_thread';
          output: components['schemas']['ShellAuthOutput_team_reply_thread'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_read_thread';
          output: components['schemas']['ShellAuthOutput_team_read_thread'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_node_register';
          output: components['schemas']['ShellAuthOutput_team_node_register'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_node_bind';
          output: components['schemas']['ShellAuthOutput_team_node_bind'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_node_pull';
          output: components['schemas']['ShellAuthOutput_team_node_pull'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_node_ack';
          output: components['schemas']['ShellAuthOutput_team_node_ack'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @constant */
          operation: 'team_node_revoke';
          output: components['schemas']['ShellAuthOutput_team_node_revoke'];
        }
      | {
          /** Format: uuid */
          requestId: string;
          /** @enum {string} */
          operation:
            | 'listResourcePackages'
            | 'createResourcePackage'
            | 'getResourcePackage'
            | 'patchResourcePackage'
            | 'listResourceVersions'
            | 'uploadResourceVersion'
            | 'getResourceVersion'
            | 'submitResourceVersion'
            | 'requestResourceDistribution'
            | 'getResourceDistribution'
            | 'downloadResourceContent'
            | 'getResourceRevocations'
            | 'createMemberResourceDownloadTicket'
            | 'consumeMemberResourceDownloadTicket'
            | 'listMemberResourceInstallations'
            | 'selectMemberResourceInstallation'
            | 'reportMemberResourceInstallation'
            | 'UnifiedListResourcePackages'
            | 'UnifiedCreateResourcePackage'
            | 'UnifiedGetResourcePackage'
            | 'UnifiedListResourceVersions'
            | 'UnifiedUploadResourceVersion'
            | 'UnifiedGetResourceVersion'
            | 'UnifiedSubmitResourceVersion'
            | 'UnifiedDownloadResourceManifest'
            | 'UnifiedCreateDownloadTicket'
            | 'UnifiedDownloadArtifact'
            | 'UnifiedListInstallations'
            | 'UnifiedSelectInstallation'
            | 'UnifiedReportInstallation'
            | 'UnifiedRenewInstallation'
            | 'UnifiedDownloadVersionArtifact'
            | 'listOwnAiUsage'
            | 'listAiModels'
            | 'createAiChatCompletion'
            | 'listNetworkSystems'
            | 'createAiChatCompletionStream'
            | 'team_initialize'
            | 'team_list_tools'
            | 'team_ping'
            | 'network_open'
            | 'upload_open'
            | 'upload_finish'
            | 'network_send'
            | 'network_submit'
            | 'getNetworkKeys'
            | 'getNetworkKeyAnnouncement'
            | 'profile_read'
            | 'provider_diagnostic'
            | 'team_list_endpoints'
            | 'team_open_thread'
            | 'team_reply_thread'
            | 'team_read_thread'
            | 'team_node_register'
            | 'team_node_bind'
            | 'team_node_pull'
            | 'team_node_ack'
            | 'team_node_revoke';
          error: components['schemas']['ShellAuthBusinessError'];
        };
    ShellAuthStreamCommand: {
      /** Format: uuid */
      streamId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      expectedRevision: number;
      /** @enum {string} */
      action: 'credit' | 'cancel' | 'close' | 'upload_chunk';
      creditBytes?: number;
      /** Format: int64 */
      sequence?: number;
      data?: string & unknown;
    } & (unknown & unknown);
    ShellAuthStreamEvent: {
      /** Format: uuid */
      streamId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      revision: number;
      /** Format: int64 */
      sequence: number;
      /** @enum {string} */
      type: 'data' | 'end' | 'closed' | 'error' | 'upload_ack';
      data?: string & unknown;
      /** Format: int64 */
      ackSequence?: number;
      creditBytes?: number;
      /** @enum {string} */
      code?:
        | 'common.validation_failed'
        | 'common.unauthenticated'
        | 'common.forbidden'
        | 'common.not_found'
        | 'common.conflict'
        | 'common.precondition_failed'
        | 'common.rate_limited'
        | 'common.unavailable'
        | 'common.internal'
        | 'resource.digest_mismatch'
        | 'resource.signature_invalid'
        | 'resource.storage_unavailable';
      error?: components['schemas']['ShellAuthBusinessError'];
    } & (unknown & unknown & unknown);
    /** @description 端口绑定控制页命令子集。skip_login只在未认证/认证中取消尝试并返回本地DSH，不调用logout，不撤销已有有效身份；已认证时拒绝冲突。 */
    ShellAuthPageCommand: {
      /** @enum {integer} */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      shellEpoch: string;
      expectedRevision: number;
      /** @enum {string} */
      action: 'snapshot' | 'open_login' | 'cancel_login' | 'skip_login';
    };
    ShellAuthOutboundForwardedHeaders: {
      [key: string]: string;
    };
    ShellAuthBusinessError: {
      /** @enum {string} */
      code:
        | 'common.validation_failed'
        | 'common.unauthenticated'
        | 'common.forbidden'
        | 'common.not_found'
        | 'common.conflict'
        | 'common.precondition_failed'
        | 'common.rate_limited'
        | 'common.unavailable'
        | 'common.internal'
        | 'resource.digest_mismatch'
        | 'resource.signature_invalid'
        | 'resource.storage_unavailable';
      upstreamStatus?: number;
      retryable: boolean;
      retryAfterSeconds?: number;
    };
    ShellAuthUploadReference: {
      /** Format: uuid */
      uploadId: string;
      /** Format: int64 */
      sizeBytes: number;
      sha256: string;
    };
    ShellAuthHostError: {
      /** @enum {string} */
      code:
        | 'common.validation_failed'
        | 'common.unauthenticated'
        | 'common.forbidden'
        | 'common.conflict'
        | 'common.not_found'
        | 'common.rate_limited'
        | 'common.unavailable';
      /** @enum {string} */
      reason:
        | 'invalid_message'
        | 'peer_not_allowed'
        | 'owner_mismatch'
        | 'stale_identity'
        | 'expired'
        | 'context_revoked'
        | 'raw_token_disabled'
        | 'credentials_unavailable'
        | 'capacity_exceeded'
        | 'not_authenticated';
      retryable: boolean;
    } & (unknown & unknown & unknown & unknown & unknown & unknown);
    /** @description 由已认证插件后端从宿主可信会话/工具执行回调采集；页面和模型参数不得构造。session先登记，tool引用同owner同宿主会话的有效session句柄。 */
    ShellAuthHostContext: {
      /** @enum {string} */
      kind: 'session' | 'tool';
      hostSessionId: string;
      toolCallId?: string;
      rootCallId?: string;
      /** Format: uuid */
      parentExecutionContextId?: string;
      /** Format: date-time */
      expiresAt: string;
    } & unknown;
    ShellAuthHostContextCommand: {
      /** @constant */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      expectedRevision: number;
      /** Format: date-time */
      expiresAt: string;
      /** @enum {string} */
      action: 'register' | 'revoke';
      context?: components['schemas']['ShellAuthHostContext'];
      /** Format: uuid */
      executionContextId?: string;
    } & (
      | {
          /** @constant */
          action?: 'register';
          executionContextId?: never;
          context: unknown;
        }
      | {
          /** @constant */
          action?: 'revoke';
          context?: never;
          executionContextId: unknown;
        }
    );
    /** @description broker生成ID和ownerId；仅交还原IPC peer内存，不是OS认证凭据，禁止交模型、浏览器或跨owner转让。epoch/revision沿结果信封。 */
    ShellAuthHostContextHandle: {
      /** Format: uuid */
      executionContextId: string;
      /** Format: uuid */
      ownerId: string;
      hostSessionId: string;
      /** @enum {string} */
      kind: 'session' | 'tool';
      /** Format: date-time */
      expiresAt: string;
    };
    ShellAuthHostContextResult: {
      /** @constant */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      revision: number;
      /** @enum {string} */
      action: 'register' | 'revoke';
      context?: components['schemas']['ShellAuthHostContextHandle'];
      /** Format: uuid */
      revokedExecutionContextId?: string;
      error?: components['schemas']['ShellAuthHostError'];
    } & (
      | {
          /** @constant */
          action?: 'register';
          revokedExecutionContextId?: never;
          error?: never;
          context: unknown;
        }
      | {
          /** @constant */
          action?: 'revoke';
          context?: never;
          error?: never;
          revokedExecutionContextId: unknown;
        }
      | {
          context?: never;
          revokedExecutionContextId?: never;
          error: unknown;
        }
    );
    /** @description 仅已认证本机插件后端，读取当前身份材料；无账号/工号/tenant/session选择器。raw遵守allowRawAgentToken，outbound仅供受信handler代发。 */
    ShellAuthHostCredentialsRequest: {
      /** @constant */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      expectedRevision: number;
      /** Format: date-time */
      expiresAt: string;
      /** Format: uuid */
      executionContextId: string;
      /** @enum {string} */
      purpose: 'raw' | 'outbound';
    };
    /** @description 当前权威企业凭据，仅后端内存；credentialVersion只作换新/CAS元数据，不作为身份准入代次。binding由IAM产生且必须与broker当前身份一致。禁止平台access/refresh token。 */
    ShellAuthHostCredentials: {
      binding: components['schemas']['UacCredentialBinding'];
      /** Format: int64 */
      credentialVersion: number;
      empNo: string;
      token: string;
      /** Format: date-time */
      updatedAt: string;
      cookies?: {
        [key: string]: string;
      };
    };
    ShellAuthHostCredentialsResult: {
      /** @constant */
      protocolVersion: 1;
      /** Format: uuid */
      requestId: string;
      /** Format: uuid */
      shellEpoch: string;
      /** Format: int64 */
      revision: number;
      /** Format: uuid */
      executionContextId: string;
      /** @enum {string} */
      purpose: 'raw' | 'outbound';
      credentials?: components['schemas']['ShellAuthHostCredentials'];
      error?: components['schemas']['ShellAuthHostError'];
    } & (
      | {
          error?: never;
          credentials: unknown;
        }
      | {
          credentials?: never;
          error: unknown;
        }
    );
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
    UacCredentialBinding: {
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      memberId: string;
      /** Format: uuid */
      deviceId: string;
      /** Format: uuid */
      sourceSessionId: string;
      /** Format: uuid */
      providerId: string;
      authGeneration: number;
      /** @description 可选凭据读取/CAS元数据，不参与设备或外壳准入；token轮换不使绑定失效 */
      credentialVersion?: number;
      /** Format: date-time */
      expiresAt: string;
    };
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
    /** @description broker只向发起控制页投影本次公开用户码；pageOwner来自票据交换的服务端页面会话，不接受命令自报。必须提示用户在可信门户人工核对本次用户码。无device_code、令牌或URL。 */
    ShellAuthDeviceAttempt: {
      /** Format: uuid */
      attemptId: string;
      /** Format: uuid */
      pageOwner: string;
      userCode: string;
      /** Format: date-time */
      expiresAt: string;
      verificationRequired: boolean;
    };
    ShellAuthInput_listResourcePackages: {
      /** @default 50 */
      limit: number;
      cursor?: string;
    };
    /** @description 新建只允许本人L3待审草稿或L4/private个人包；level/owner/tenant由服务端决定，不能直接创建L0-L2。 */
    ShellAuthopenapi_resource_yaml_components_schemas_PackageInput: {
      name: string;
      /** @constant */
      kind: 'plugin';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups' | 'private';
      groupIds?: string[];
    };
    ShellAuthInput_createResourcePackage: {
      headers?: {
        'idempotency-key'?: string;
        'if-match'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_PackageInput'];
    };
    ShellAuthInput_getResourcePackage: {
      /** Format: uuid */
      packageId: string;
    };
    ShellAuthopenapi_resource_yaml_components_schemas_PackagePatch: {
      name?: string;
      /** @enum {string} */
      visibility?: 'tenant' | 'members' | 'groups' | 'private';
      groupIds?: string[];
    };
    ShellAuthInput_patchResourcePackage: {
      /** Format: uuid */
      packageId: string;
      headers: {
        'if-match': string;
        'idempotency-key'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_PackagePatch'];
    };
    ShellAuthInput_listResourceVersions: {
      /** Format: uuid */
      packageId: string;
      /** @default 50 */
      limit: number;
      cursor?: string;
    };
    ShellAuthresource_signatures_schema_json__defs_Digest: string;
    ShellAuthopenapi_resource_yaml_components_schemas_UploadMetadata: {
      version: string;
      expectedDigest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      manifest: {
        name: string;
        dshVersionRange: string;
        /** @enum {string} */
        os: 'linux' | 'windows' | 'any';
        /** @enum {string} */
        arch: 'amd64' | 'arm64' | 'any';
      };
      changeNotes?: string;
    };
    ShellAuthInput_uploadResourceVersion: {
      /** Format: uuid */
      packageId: string;
      headers: {
        'idempotency-key'?: string;
        'if-match': string;
      };
      body: {
        metadata: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UploadMetadata'];
        archive: components['schemas']['ShellAuthUploadReference'];
      };
    };
    ShellAuthInput_getResourceVersion: {
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
    };
    ShellAuthopenapi_resource_yaml_components_schemas_Reason: {
      reason: string;
    };
    ShellAuthInput_submitResourceVersion: {
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      headers: {
        'idempotency-key'?: string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Reason'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_DistributionInput: {
      /** Format: uuid */
      instanceId: string;
      selections: {
        /** Format: uuid */
        packageId: string;
        /** Format: uuid */
        versionId: string;
        /** @enum {string} */
        action: 'install' | 'enable' | 'disable' | 'remove';
      }[];
    };
    ShellAuthInput_requestResourceDistribution: {
      headers?: {
        'idempotency-key'?: string;
        'if-match'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_DistributionInput'];
    };
    ShellAuthInput_getResourceDistribution: {
      /** Format: uuid */
      distributionId: string;
    };
    ShellAuthInput_downloadResourceContent: {
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
    };
    ShellAuthInput_getResourceRevocations: Record<string, never>;
    ShellAuthopenapi_resource_yaml_components_schemas_MemberDownloadTicketInput: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
    };
    ShellAuthInput_createMemberResourceDownloadTicket: {
      headers?: {
        'idempotency-key'?: string;
        'if-match'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberDownloadTicketInput'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_MemberDownloadInput: {
      /** Format: uuid */
      clientId: string;
      ticket: string;
    };
    ShellAuthInput_consumeMemberResourceDownloadTicket: {
      headers?: {
        'idempotency-key'?: string;
        'if-match'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberDownloadInput'];
    };
    ShellAuthInput_listMemberResourceInstallations: {
      /** Format: uuid */
      afterPackageId?: string;
      /** @default 50 */
      limit: number;
    };
    ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallationInput: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
      /** @enum {string} */
      action: 'install' | 'enable' | 'disable' | 'remove';
    };
    ShellAuthInput_selectMemberResourceInstallation: {
      headers?: {
        'idempotency-key'?: string;
        'if-match'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallationInput'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallationReport: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
      /** Format: int64 */
      installationRevision: number;
      /** @enum {string} */
      state: 'applied' | 'failed';
    };
    ShellAuthInput_reportMemberResourceInstallation: {
      headers?: {
        'idempotency-key'?: string;
        'if-match'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallationReport'];
    };
    ShellAuthInput_UnifiedListResourcePackages: {
      /** @default 50 */
      limit: number;
      cursor?: string;
      /** @enum {string} */
      kind?: 'skill' | 'tool';
    };
    /** @description Shared L3 skill/tool only. Server derives tenant, owner and level. Private skill stays local. Plugin CRUD uses legacy APIs; catalog rejects plugin kind/IDs. */
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackageInput: {
      name: string;
      /** @enum {string} */
      kind: 'skill' | 'tool';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups';
      groupIds?: string[];
    };
    ShellAuthInput_UnifiedCreateResourcePackage: {
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackageInput'];
    };
    ShellAuthInput_UnifiedGetResourcePackage: {
      /** Format: uuid */
      packageId: string;
    };
    ShellAuthInput_UnifiedListResourceVersions: {
      /** Format: uuid */
      packageId: string;
      /** @default 50 */
      limit: number;
      cursor?: string;
    };
    ShellAuthresource_package_schema_json__defs_Version: string;
    /** @description Draft multipart upload. expectedDigest is exact manifest bytes SHA-256. Include manifest as a binary part, plus one binary artifacts part per manifest artifact in array order. Online tools use no artifacts. Validate all hashes before committing the immutable version. */
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedUploadMetadata: {
      version: components['schemas']['ShellAuthresource_package_schema_json__defs_Version'];
      expectedDigest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      changeNotes?: string;
    };
    ShellAuthInput_UnifiedUploadResourceVersion: {
      /** Format: uuid */
      packageId: string;
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: {
        metadata: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedUploadMetadata'];
        manifest: components['schemas']['ShellAuthUploadReference'];
        artifacts: components['schemas']['ShellAuthUploadReference'][];
      };
    };
    ShellAuthInput_UnifiedGetResourceVersion: {
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
    };
    ShellAuthInput_UnifiedSubmitResourceVersion: {
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Reason'];
    };
    ShellAuthInput_UnifiedDownloadResourceManifest: {
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedResourceIdentity: {
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      /** @enum {string} */
      kind: 'skill' | 'tool';
      version: components['schemas']['ShellAuthresource_package_schema_json__defs_Version'];
      manifestDigest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedDownloadTicketInput: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
    };
    ShellAuthInput_UnifiedCreateDownloadTicket: {
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedDownloadTicketInput'];
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedDownloadInput: {
      /** Format: uuid */
      clientId: string;
      ticket: string;
      artifactId: string;
    };
    ShellAuthInput_UnifiedDownloadArtifact: {
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedDownloadInput'];
    };
    ShellAuthInput_UnifiedListInstallations: {
      /** @enum {string} */
      kind?: 'skill' | 'tool';
      /** @default 50 */
      limit: number;
      cursor?: string;
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedSelection: {
      identity: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedResourceIdentity'];
      /** @enum {string} */
      action: 'install' | 'enable' | 'disable' | 'remove';
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedInstallationInput: {
      /** Format: uuid */
      clientId: string;
      selection: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedSelection'];
    };
    ShellAuthInput_UnifiedSelectInstallation: {
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedInstallationInput'];
    };
    /** Format: date-time */
    ShellAuthresource_signatures_schema_json__defs_Time: string;
    ShellAuthresource_unified_schema_json__defs_UnifiedMemberInstallationReport: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** Format: int64 */
      observedGeneration: number;
      /** @enum {string} */
      state:
        | 'applying'
        | 'applied'
        | 'failed'
        | 'pending_restart'
        | 'superseded'
        | 'overridden'
        | 'expired'
        | 'revoked'
        | 'disabled'
        | 'removed';
      /** @constant */
      reportSource: 'member';
      errorCode?: string;
      observedAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
    } & unknown;
    ShellAuthInput_UnifiedReportInstallation: {
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedMemberInstallationReport'];
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedRenewalInput: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      releaseSequence: number;
    };
    ShellAuthInput_UnifiedRenewInstallation: {
      headers: {
        'idempotency-key': string;
        'if-match': string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedRenewalInput'];
    };
    ShellAuthInput_UnifiedDownloadVersionArtifact: {
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      artifactId: string;
    };
    ShellAuthInput_listOwnAiUsage: {
      /** @default 50 */
      limit: number;
      cursor?: string;
    };
    ShellAuthInput_listAiModels: Record<string, never>;
    ShellAuthopenapi_ai_yaml_components_schemas_ToolCall: {
      id: string;
      /** @constant */
      type: 'function';
      function: {
        name: string;
        arguments: string;
      };
    };
    ShellAuthopenapi_ai_yaml_components_schemas_ChatMessage: {
      /** @enum {string} */
      role: 'system' | 'user' | 'assistant' | 'tool';
      content: string | null;
      name?: string;
      tool_call_id?: string;
      tool_calls?: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_ToolCall'][];
    };
    ShellAuthopenapi_ai_yaml_components_schemas_Tool: {
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
    ShellAuthChatJsonRequest: {
      model: string;
      messages: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_ChatMessage'][];
      stream?: boolean;
      /** Format: int64 */
      max_tokens: number;
      temperature?: number;
      top_p?: number;
      /** @description 首批仅接受最多4个字符串的数组，不接受单字符串stop。 */
      stop?: string[];
      tools?: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_Tool'][];
      /** @enum {string} */
      tool_choice?: 'none' | 'auto' | 'required';
    } & unknown;
    ShellAuthInput_createAiChatCompletion: {
      headers?: {
        'idempotency-key'?: string;
      };
      body: components['schemas']['ShellAuthChatJsonRequest'];
    };
    ShellAuthInput_listNetworkSystems: {
      /** @default 50 */
      limit: number;
      cursor?: string;
    };
    ShellAuthChatStreamRequest: {
      model: string;
      messages: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_ChatMessage'][];
      stream: boolean;
      /** Format: int64 */
      max_tokens: number;
      temperature?: number;
      top_p?: number;
      /** @description 首批仅接受最多4个字符串的数组，不接受单字符串stop。 */
      stop?: string[];
      tools?: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_Tool'][];
      /** @enum {string} */
      tool_choice?: 'none' | 'auto' | 'required';
    } & unknown;
    ShellAuthInput_createAiChatCompletionStream: {
      headers?: {
        'idempotency-key'?: string;
      };
      body: components['schemas']['ShellAuthChatStreamRequest'];
    };
    ShellAuthInput_team_initialize: {
      /** @enum {string} */
      protocolVersion: '2025-06-18';
    };
    ShellAuthInput_team_list_tools: {
      /** Format: uuid */
      streamId: string;
    };
    ShellAuthInput_team_ping: {
      /** Format: uuid */
      streamId: string;
    };
    ShellAuthInput_network_open: {
      /** Format: uuid */
      executionContextId: string;
    };
    ShellAuthInput_upload_open: {
      /** Format: int64 */
      sizeBytes: number;
      sha256: string;
      /** @enum {string} */
      mediaType:
        'application/json' | 'application/gzip' | 'application/octet-stream';
    };
    ShellAuthInput_upload_finish: {
      /** Format: uuid */
      uploadId: string;
    };
    ShellAuthInput_network_send: {
      /** Format: uuid */
      streamId: string;
      /** @description 解码后最多65536字节，必须严格验证 network/protocol.schema.json；保留原始受签字节，不经生成模型重序列化。 */
      frameWire: string;
    };
    ShellAuthInput_network_submit: {
      /** Format: uuid */
      executionContextId: string;
      /** @description 解码后最多65536字节，必须严格验证 network/send.schema.json#/$defs/SubmitRequest；保留原始受签字节，不经生成模型重序列化。 */
      requestWire: string;
    };
    ShellAuthInput_getNetworkKeys: Record<string, never>;
    ShellAuthInput_getNetworkKeyAnnouncement: Record<string, never>;
    ShellAuthInput_profile_read: Record<string, never>;
    ShellAuthInput_provider_diagnostic: Record<string, never>;
    ShellAuthplugin_team_mcp_schema_json__defs_Limit: number;
    /** Format: uuid */
    ShellAuthplugin_team_mcp_schema_json__defs_Uuid: string;
    ShellAuthplugin_team_mcp_schema_json__defs_ListEndpoints: {
      limit: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Limit'];
      afterEndpointId?: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Uuid'];
    };
    ShellAuthInput_team_list_endpoints: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_ListEndpoints'];
    };
    /** @description Server additionally enforces at most 16384 UTF-8 bytes. */
    ShellAuthplugin_team_mcp_schema_json__defs_Text: string;
    ShellAuthplugin_team_mcp_schema_json__defs_Key: string;
    ShellAuthplugin_team_mcp_schema_json__defs_OpenThread: {
      targetEndpointId: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Uuid'];
      text: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Text'];
      idempotencyKey: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Key'];
    };
    ShellAuthInput_team_open_thread: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_OpenThread'];
    };
    ShellAuthplugin_team_mcp_schema_json__defs_ReplyThread: {
      threadId: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Uuid'];
      text: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Text'];
      idempotencyKey: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Key'];
    };
    ShellAuthInput_team_reply_thread: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_ReplyThread'];
    };
    ShellAuthplugin_team_mcp_schema_json__defs_ReadThread: {
      threadId: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Uuid'];
      /** Format: int64 */
      afterSequence?: number;
      limit: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_Limit'];
    };
    ShellAuthInput_team_read_thread: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_mcp_schema_json__defs_ReadThread'];
    };
    ShellAuthplugin_team_transport_schema_json__defs_Register: {
      label: string;
      receiveEnabled: boolean;
    };
    ShellAuthInput_team_node_register: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Register'];
    };
    /** Format: uuid */
    ShellAuthplugin_team_transport_schema_json__defs_Uuid: string;
    ShellAuthplugin_team_transport_schema_json__defs_Session: string;
    ShellAuthplugin_team_transport_schema_json__defs_Bind: {
      endpointId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
      hostSessionId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Session'];
      threadId?: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
    };
    ShellAuthInput_team_node_bind: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Bind'];
    };
    ShellAuthplugin_team_transport_schema_json__defs_Pull: {
      endpointId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
      limit: number;
    };
    ShellAuthInput_team_node_pull: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Pull'];
    };
    ShellAuthplugin_team_transport_schema_json__defs_Ack: {
      endpointId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
      bindingId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
      hostSessionId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Session'];
      deliveryId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
      leaseToken: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
      hostRequestId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
    };
    ShellAuthInput_team_node_ack: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Ack'];
    };
    ShellAuthplugin_team_transport_schema_json__defs_Revoke: {
      endpointId: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Uuid'];
    };
    ShellAuthInput_team_node_revoke: {
      /** Format: uuid */
      executionContextId: string;
      arguments: components['schemas']['ShellAuthplugin_team_transport_schema_json__defs_Revoke'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_Package: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      tenantId: string;
      name: string;
      /** @constant */
      kind: 'plugin';
      /** Format: uuid */
      ownerId: string;
      /** @enum {string} */
      level: 'l0' | 'l1' | 'l2' | 'l3' | 'l4';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups' | 'private';
      groupIds: string[];
      /** Format: int64 */
      revision: number;
      /** Format: date-time */
      createdAt: string;
      enabled: boolean;
      /** Format: uuid */
      defaultVersionId: string | null;
    };
    ShellAuthopenapi_resource_yaml_components_schemas_PackagePage: {
      items: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Package'][];
      nextCursor?: string;
    };
    ShellAuthOutput_listResourcePackages: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_PackagePage'];
    };
    ShellAuthOutput_createResourcePackage: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Package'];
    };
    ShellAuthOutput_getResourcePackage: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Package'];
    };
    ShellAuthOutput_patchResourcePackage: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Package'];
    };
    /** Format: uuid */
    ShellAuthresource_signatures_schema_json__defs_Uuid: string;
    /** Format: int64 */
    ShellAuthresource_signatures_schema_json__defs_Counter: number;
    ShellAuthresource_signatures_schema_json__defs_Statement: {
      /** @constant */
      purpose: 'plugin';
      name: string;
      version: string;
      /** @enum {string} */
      os: 'linux' | 'windows' | 'any';
      /** @enum {string} */
      arch: 'amd64' | 'arm64' | 'any';
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      issuedAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
    };
    ShellAuthresource_signatures_schema_json__defs_Signature: {
      /** @constant */
      alg: 'Ed25519';
      kid: string;
      /** @description 64 字节签名的 base64url 无填充编码；解码后须重编码比较，拒绝非规范尾位。 */
      value: string;
    };
    ShellAuthresource_signatures_schema_json__defs_ResourceEnvelope: {
      /** @constant */
      schemaVersion: 1;
      tenantId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      packageId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      versionId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      releaseSequence: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Counter'];
      notAfter: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
      statement: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Statement'];
      signature: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Signature'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_Version: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      version: string;
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      /** Format: int64 */
      sizeBytes: number;
      /** @constant */
      mediaType: 'application/gzip';
      /** @enum {string} */
      state:
        | 'draft'
        | 'pending_review'
        | 'approved'
        | 'rejected'
        | 'published'
        | 'deprecated'
        | 'withdrawn';
      /** Format: int64 */
      revision: number;
      /** Format: int64 */
      releaseSequence?: number;
      resourceEnvelope?: components['schemas']['ShellAuthresource_signatures_schema_json__defs_ResourceEnvelope'];
      /** Format: date-time */
      createdAt: string;
    };
    ShellAuthopenapi_resource_yaml_components_schemas_VersionPage: {
      items: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Version'][];
      nextCursor?: string;
    };
    ShellAuthOutput_listResourceVersions: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_VersionPage'];
    };
    ShellAuthOutput_uploadResourceVersion: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Version'];
    };
    ShellAuthOutput_getResourceVersion: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Version'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_Review: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      /** Format: int64 */
      versionRevision: number;
      /** @enum {string} */
      state: 'pending' | 'approved' | 'rejected';
      /** Format: int64 */
      revision: number;
    };
    ShellAuthOutput_submitResourceVersion: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Review'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_Distribution: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      instanceId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: int64 */
      selectionRevision: number;
      /**
       * @description Once workspace accepts a newer selection revision, requested/resolved/applying becomes superseded. Already applied or failed rows keep the latest recorded outcome and error; appliedGeneration never replaces a later real failure.
       * @enum {string}
       */
      state:
        | 'requested'
        | 'resolved'
        | 'applying'
        | 'applied'
        | 'failed'
        | 'superseded';
      /** Format: int64 */
      generation?: number;
      /** Format: int64 */
      observedGeneration?: number;
      ready?: boolean;
      errorCode?: string;
      /** Format: int64 */
      revision: number;
      selections?: {
        /** Format: uuid */
        packageId: string;
        /** Format: uuid */
        versionId: string;
        /** @enum {string} */
        action: 'install' | 'enable' | 'disable' | 'remove';
      }[];
      /**
       * Format: int64
       * @description Last generation genuinely observed ready. Historical rows retain this successful observation after a newer selection replaces them.
       */
      appliedGeneration?: number;
    };
    ShellAuthOutput_requestResourceDistribution: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Distribution'];
    };
    ShellAuthOutput_getResourceDistribution: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_Distribution'];
    };
    ShellAuthOutput_downloadResourceContent: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthresource_signatures_schema_json__defs_RevokedArtifact: {
      packageId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      versionId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
    };
    ShellAuthresource_signatures_schema_json__defs_RevocationList: {
      /** @constant */
      schemaVersion: 1;
      tenantId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      revision: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Counter'];
      issuedAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
      expiresAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
      revokedKids: string[];
      revokedArtifacts: components['schemas']['ShellAuthresource_signatures_schema_json__defs_RevokedArtifact'][];
      signature: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Signature'];
    };
    ShellAuthOutput_getResourceRevocations: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthresource_signatures_schema_json__defs_RevocationList'];
    };
    ShellAuthOutput_createMemberResourceDownloadTicket: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** @description 解码后最多65536字节，必须严格验证 original download ticket response；保留原始受签字节，不经生成模型重序列化。 */
        wire: string & unknown;
      };
    };
    ShellAuthOutput_consumeMemberResourceDownloadTicket: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallation: {
      /** Format: uuid */
      clientId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: string;
      /** Format: int64 */
      releaseSequence: number;
      /** @enum {string} */
      level: 'l1' | 'l2' | 'l3' | 'l4';
      /** @enum {string} */
      desired: 'enabled' | 'disabled' | 'removed';
      /** @enum {string} */
      state: 'applying' | 'applied' | 'failed';
      /** Format: int64 */
      revision: number;
      /** @constant */
      reportSource: 'member';
      /** Format: int64 */
      reportedRevision?: number;
      /** Format: date-time */
      reportedAt?: string;
      /** @constant */
      errorCode?: 'resource.apply_failed';
    };
    ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallationPage: {
      items: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallation'][];
      /** Format: uuid */
      nextAfterPackageId?: string;
    };
    ShellAuthOutput_listMemberResourceInstallations: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallationPage'];
    };
    ShellAuthOutput_selectMemberResourceInstallation: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallation'];
    };
    ShellAuthOutput_reportMemberResourceInstallation: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_MemberInstallation'];
    };
    /** @description Shared L3 skill/tool. Plugin L0-L4/private views continue through legacy APIs, without catalog projection. */
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackage: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      tenantId: string;
      name: string;
      /** @enum {string} */
      kind: 'skill' | 'tool';
      /** Format: uuid */
      ownerId: string;
      /** @enum {string} */
      level: 'l3';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups';
      groupIds: string[];
      /** Format: int64 */
      revision: number;
      /** Format: date-time */
      createdAt: string;
      enabled: boolean;
      /** Format: uuid */
      defaultVersionId: string | null;
    };
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackagePage: {
      items: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackage'][];
      nextCursor?: string;
    };
    ShellAuthOutput_UnifiedListResourcePackages: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackagePage'];
    };
    ShellAuthOutput_UnifiedCreateResourcePackage: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackage'];
    };
    ShellAuthOutput_UnifiedGetResourcePackage: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedPackage'];
    };
    ShellAuthresource_package_schema_json__defs_Name: string;
    ShellAuthresource_package_schema_json__defs_Statement: {
      /** @constant */
      purpose: 'resource';
      /** @enum {string} */
      kind: 'plugin' | 'skill' | 'tool';
      name: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      version: components['schemas']['ShellAuthresource_package_schema_json__defs_Version'];
      manifestDigest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      artifactDigests: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'][];
      issuedAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
    };
    ShellAuthresource_package_schema_json__defs_Envelope: {
      /** @constant */
      schemaVersion: 2;
      tenantId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      packageId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      versionId: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Uuid'];
      releaseSequence: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Counter'];
      notAfter: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
      statement: components['schemas']['ShellAuthresource_package_schema_json__defs_Statement'];
      signature: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Signature'];
    };
    ShellAuthresource_package_schema_json__defs_Permissions: {
      tools: string[];
      outboundHosts: string[];
      dataAccess: (
        'workspace_read' | 'workspace_write' | 'user_selected_files'
      )[];
    };
    ShellAuthresource_package_schema_json__defs_Runtime: {
      /** @enum {string} */
      kind: 'node' | 'python' | 'java';
      versionRange: string;
    };
    ShellAuthresource_package_schema_json__defs_SkillArtifact: {
      id: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      /** @constant */
      role: 'payload';
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      sizeBytes: number;
      /** @constant */
      mediaType: 'application/gzip';
      /** @constant */
      os: 'any';
      /** @constant */
      arch: 'any';
    };
    ShellAuthresource_package_schema_json__defs_Skill: {
      /** @constant */
      entry: 'SKILL.md';
      /** @constant */
      contentProfile: 'text-only';
      dshVersionRange: string;
    };
    ShellAuthresource_package_schema_json__defs_SkillManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      version: components['schemas']['ShellAuthresource_package_schema_json__defs_Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['ShellAuthresource_package_schema_json__defs_Permissions'];
      runtimes: components['schemas']['ShellAuthresource_package_schema_json__defs_Runtime'][];
      artifacts: components['schemas']['ShellAuthresource_package_schema_json__defs_SkillArtifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'skill';
      skill: components['schemas']['ShellAuthresource_package_schema_json__defs_Skill'];
    };
    ShellAuthresource_package_schema_json__defs_Artifact: {
      id: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      /** @enum {string} */
      role: 'payload' | 'source_snapshot' | 'lockfile';
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      sizeBytes: number;
      /** @enum {string} */
      mediaType:
        | 'application/gzip'
        | 'application/octet-stream'
        | 'application/json'
        | 'text/plain';
      /** @enum {string} */
      os: 'linux' | 'windows' | 'any';
      /** @enum {string} */
      arch: 'amd64' | 'arm64' | 'any';
    };
    ShellAuthresource_package_schema_json__defs_OnlineTool: {
      /** @constant */
      delivery: 'online';
      /** @enum {string} */
      protocol: 'web' | 'mcp';
      /** Format: uri */
      url: string;
      /** @enum {string} */
      authentication: 'none' | 'member_oauth' | 'platform_proxy';
    };
    ShellAuthresource_package_schema_json__defs_OnlineManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      version: components['schemas']['ShellAuthresource_package_schema_json__defs_Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['ShellAuthresource_package_schema_json__defs_Permissions'];
      runtimes: components['schemas']['ShellAuthresource_package_schema_json__defs_Runtime'][];
      artifacts: components['schemas']['ShellAuthresource_package_schema_json__defs_Artifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'tool';
      tool: components['schemas']['ShellAuthresource_package_schema_json__defs_OnlineTool'];
    };
    ShellAuthresource_package_schema_json__defs_RelativePath: string;
    ShellAuthresource_package_schema_json__defs_EntryPoint: {
      artifactId: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      path: components['schemas']['ShellAuthresource_package_schema_json__defs_RelativePath'];
      args: string[];
    };
    ShellAuthresource_package_schema_json__defs_OfflineTool: {
      /** @constant */
      delivery: 'offline';
      entrypoints: components['schemas']['ShellAuthresource_package_schema_json__defs_EntryPoint'][];
    };
    ShellAuthresource_package_schema_json__defs_OfflineManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      version: components['schemas']['ShellAuthresource_package_schema_json__defs_Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['ShellAuthresource_package_schema_json__defs_Permissions'];
      runtimes: components['schemas']['ShellAuthresource_package_schema_json__defs_Runtime'][];
      artifacts: components['schemas']['ShellAuthresource_package_schema_json__defs_Artifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'tool';
      tool: components['schemas']['ShellAuthresource_package_schema_json__defs_OfflineTool'];
    };
    ShellAuthresource_package_schema_json__defs_GitTool: {
      /** @constant */
      delivery: 'git';
      /** Format: uri */
      repository: string;
      commit: string;
      sourceArtifactId: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      lockArtifactIds: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'][];
      /** @enum {string} */
      packageManager: 'npm' | 'pnpm' | 'pip' | 'maven' | 'gradle';
      installScripts: boolean;
      entrypoint: components['schemas']['ShellAuthresource_package_schema_json__defs_EntryPoint'];
    };
    ShellAuthresource_package_schema_json__defs_GitManifest: {
      /** @constant */
      schemaVersion: 1;
      name: components['schemas']['ShellAuthresource_package_schema_json__defs_Name'];
      version: components['schemas']['ShellAuthresource_package_schema_json__defs_Version'];
      description: string;
      author: {
        displayName: string;
      };
      license: string;
      permissions: components['schemas']['ShellAuthresource_package_schema_json__defs_Permissions'];
      runtimes: components['schemas']['ShellAuthresource_package_schema_json__defs_Runtime'][];
      artifacts: components['schemas']['ShellAuthresource_package_schema_json__defs_Artifact'][];
      signature: {
        /** @constant */
        mode: 'detached';
        /** @constant */
        algorithm: 'Ed25519';
      };
      /** @constant */
      kind: 'tool';
      tool: components['schemas']['ShellAuthresource_package_schema_json__defs_GitTool'];
    };
    ShellAuthresource_package_schema_json__defs_ToolManifest:
      | components['schemas']['ShellAuthresource_package_schema_json__defs_OnlineManifest']
      | components['schemas']['ShellAuthresource_package_schema_json__defs_OfflineManifest']
      | components['schemas']['ShellAuthresource_package_schema_json__defs_GitManifest'];
    /** @description Catalog manifest for skill/tool. PluginManifest remains in the package format for internal migration only; catalog APIs do not expose plugin projections. */
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedManifest:
      | components['schemas']['ShellAuthresource_package_schema_json__defs_SkillManifest']
      | components['schemas']['ShellAuthresource_package_schema_json__defs_ToolManifest'];
    /** @description Read-only server assessment, recalculated on content/permission changes. Publication transaction checks the current assessment and review identities. */
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedRiskAssessment: {
      /**
       * @description Server-derived floor: command skill or any nonempty tools/outboundHosts/dataAccess is at least r2; tool at least r2, git r3. Server scans text too; declarations cannot downgrade command content.
       * @enum {string}
       */
      minimumLevel: 'r1' | 'r2' | 'r3';
      /**
       * @description Effective server risk. Authorized reviewers may raise it only; changes increment revision and invalidate earlier reviews.
       * @enum {string}
       */
      level: 'r1' | 'r2' | 'r3';
      /** @description Distinct non-author reviewers; one for r1, two for r2/r3. Repeated identities count once. */
      requiredReviewers: number;
      /** @description Zero for r1; at least one qualified security reviewer for r2/r3. Server verifies qualification independently of input role. */
      requiredSecurityReviewers: number;
    } & (unknown & unknown & unknown);
    /** @description Draft version. Reviews bind manifest digest and version revision. Published manifests and artifacts are immutable. */
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedVersion: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      version: string;
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      /** @enum {string} */
      state:
        | 'draft'
        | 'pending_review'
        | 'approved'
        | 'rejected'
        | 'published'
        | 'deprecated'
        | 'withdrawn';
      /** Format: int64 */
      revision: number;
      /** Format: int64 */
      releaseSequence?: number;
      resourceEnvelope?: components['schemas']['ShellAuthresource_package_schema_json__defs_Envelope'];
      /** Format: date-time */
      createdAt: string;
      manifest: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedManifest'];
      /** @enum {string} */
      kind: 'skill' | 'tool';
      risk: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedRiskAssessment'];
      /** @description Same-origin locator for exact immutable raw manifest UTF-8 bytes, not a bearer URL. Every request checks tenant/package/version ACL; caller must verify digest/envelope. Published and draft reads use the current corresponding ACL. */
      manifestDownloadPath: string;
    };
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedVersionPage: {
      items: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedVersion'][];
      nextCursor?: string;
    };
    ShellAuthOutput_UnifiedListResourceVersions: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedVersionPage'];
    };
    ShellAuthOutput_UnifiedUploadResourceVersion: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedVersion'];
    };
    ShellAuthOutput_UnifiedGetResourceVersion: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedVersion'];
    };
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedReviewRecord: {
      /** Format: uuid */
      reviewerId: string;
      /** @enum {string} */
      role: 'general' | 'security';
      /** @enum {string} */
      decision: 'approve' | 'reject';
      reason: string;
      /** Format: int64 */
      versionRevision: number;
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      /** Format: date-time */
      createdAt: string;
    };
    /** @description Review identity and immutable decision history for the exact version revision. Version contains kind and server risk/quorum. Failed author/qualification/quorum checks cannot approve/publish. */
    ShellAuthopenapi_resource_yaml_components_schemas_UnifiedReview: {
      /** Format: uuid */
      reviewId: string;
      /** Format: uuid */
      authorId: string;
      version: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedVersion'];
      /** Format: int64 */
      revision: number;
      /** @enum {string} */
      status: 'pending' | 'approved' | 'rejected';
      decisions: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedReviewRecord'][];
    };
    ShellAuthOutput_UnifiedSubmitResourceVersion: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_resource_yaml_components_schemas_UnifiedReview'];
    };
    ShellAuthOutput_UnifiedDownloadResourceManifest: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_UnifiedCreateDownloadTicket: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** @description 解码后最多65536字节，必须严格验证 original download ticket response；保留原始受签字节，不经生成模型重序列化。 */
        wire: string & unknown;
      };
    };
    ShellAuthOutput_UnifiedDownloadArtifact: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedArtifactDownload: {
      artifactId: string;
      digest: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Digest'];
      /** Format: int64 */
      sizeBytes: number;
      /** @description Same-origin path only. Reject absolute URLs, authority paths, query, fragments, percent escapes, redirects and dot segments. */
      downloadPath: string;
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedLease: {
      envelopeNotAfter: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
      revocationsExpiresAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
      expiresAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
      /** Format: int64 */
      revocationRevision: number;
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedResourceRef: {
      identity: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedResourceIdentity'];
      envelope: components['schemas']['ShellAuthresource_package_schema_json__defs_Envelope'];
      /** @description Same-origin path only. Reject absolute URLs, authority paths, query, fragments, percent escapes, redirects and dot segments. */
      manifestDownloadPath: string;
      artifacts: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedArtifactDownload'][];
      lease: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedLease'];
      /** Format: int64 */
      releaseSequence: number;
      enabled: boolean;
      /**
       * Format: int64
       * @description Resource-assigned persisted per-installation CAS revision. Preserved by signature renewal and echoed by node/Desktop reports. Omission is transport-compatible only: never accept applied without a positive expected revision.
       */
      installationRevision?: number;
      /**
       * @description Preferred desired state when present. If absent on legacy data, derive ENABLED/DISABLED from enabled. REMOVED is an uninstall tombstone carrying the new installationRevision, retained until a matching removed acknowledgement and retired in the next generation. Reject unknown or unspecified values.
       * @enum {string}
       */
      desiredState?: 'ENABLED' | 'DISABLED' | 'REMOVED';
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedInstallationReport: {
      /** Format: uuid */
      clientId: string;
      identity: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedResourceIdentity'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** Format: int64 */
      observedGeneration: number;
      /** @enum {string} */
      state:
        | 'applying'
        | 'applied'
        | 'failed'
        | 'pending_restart'
        | 'superseded'
        | 'overridden'
        | 'expired'
        | 'revoked'
        | 'disabled'
        | 'removed';
      /** @enum {string} */
      reportSource: 'member' | 'node';
      errorCode?: string;
      observedAt: components['schemas']['ShellAuthresource_signatures_schema_json__defs_Time'];
    } & unknown;
    ShellAuthresource_unified_schema_json__defs_UnifiedInstallation: {
      /** Format: uuid */
      clientId: string;
      resource: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedResourceRef'];
      /** Format: int64 */
      installationRevision: number;
      /** Format: int64 */
      selectionRevision: number;
      /** Format: int64 */
      generation: number;
      /** @enum {string} */
      state:
        | 'applying'
        | 'applied'
        | 'failed'
        | 'pending_restart'
        | 'superseded'
        | 'overridden'
        | 'expired'
        | 'revoked'
        | 'disabled'
        | 'removed';
      /** @enum {string} */
      reportSource: 'member' | 'node';
      report?: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedInstallationReport'];
    };
    ShellAuthresource_unified_schema_json__defs_UnifiedInstallationPage: {
      items: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedInstallation'][];
      nextCursor?: string;
    };
    ShellAuthOutput_UnifiedListInstallations: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedInstallationPage'];
    };
    ShellAuthOutput_UnifiedSelectInstallation: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedInstallation'];
    };
    ShellAuthOutput_UnifiedReportInstallation: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthresource_unified_schema_json__defs_UnifiedInstallation'];
    };
    ShellAuthOutput_UnifiedRenewInstallation: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** @description 解码后最多65536字节，必须严格验证 original download ticket response；保留原始受签字节，不经生成模型重序列化。 */
        wire: string & unknown;
      };
    };
    ShellAuthOutput_UnifiedDownloadVersionArtifact: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthopenapi_ai_yaml_components_schemas_Usage: {
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
    ShellAuthopenapi_ai_yaml_components_schemas_UsagePage: {
      items: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_Usage'][];
      nextCursor?: string;
    };
    ShellAuthOutput_listOwnAiUsage: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_UsagePage'];
    };
    ShellAuthopenapi_ai_yaml_components_schemas_ModelList: {
      /** @constant */
      object: 'list';
      data: {
        id: string;
        /** @constant */
        object: 'model';
        /**
         * Format: int64
         * @description Unix seconds; 0 denotes an unknown creation time and is not a creation timestamp.
         */
        created: number;
        /**
         * Format: int64
         * @description Authenticated pool context capacity, when disclosed.
         */
        context_window?: number;
        /**
         * Format: int64
         * @description Authenticated pool output capacity; smaller than context_window when both are disclosed.
         */
        max_output_tokens?: number;
        /** @constant */
        owned_by: 'auriga';
      }[];
    };
    ShellAuthOutput_listAiModels: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_ModelList'];
    };
    ShellAuthopenapi_ai_yaml_components_schemas_AssistantMessage: {
      /** @constant */
      role: 'assistant';
      content: string | null;
      tool_calls?: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_ToolCall'][];
    };
    ShellAuthopenapi_ai_yaml_components_schemas_TokenUsage: {
      /** Format: int64 */
      prompt_tokens: number;
      /** Format: int64 */
      completion_tokens: number;
      /** Format: int64 */
      total_tokens: number;
    };
    ShellAuthopenapi_ai_yaml_components_schemas_ChatCompletion: {
      id: string;
      /** @constant */
      object: 'chat.completion';
      /** Format: int64 */
      created: number;
      model: string;
      choices: {
        /** Format: int64 */
        index: number;
        message: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_AssistantMessage'];
        /** @enum {string} */
        finish_reason: 'stop' | 'length' | 'tool_calls' | 'content_filter';
      }[];
      usage: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_TokenUsage'];
    };
    ShellAuthOutput_createAiChatCompletion: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthopenapi_ai_yaml_components_schemas_ChatCompletion'];
    };
    /** IAM visible DSH system directory page */
    ShellAuthnetwork_system_directory_schema_json: {
      items: ({
        systemAddress: string;
        displayName: string;
        /** Format: uuid */
        ownerUserId: string;
        ownerDisplayName: string;
        /** @enum {string} */
        onlineState?: 'unknown' | 'online' | 'offline';
        /** Format: date-time */
        statusObservedAt?: string | null;
      } & unknown)[];
      nextCursor?: string;
    };
    ShellAuthOutput_listNetworkSystems: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthnetwork_system_directory_schema_json'];
    };
    ShellAuthOutput_createAiChatCompletionStream: {
      statusCode: number;
      headers?: {
        etag?: string;
        'content-type'?: string;
        'content-length'?: string;
        'content-range'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
        /** @enum {string} */
        encoding: 'sse_utf8';
      };
    };
    ShellAuthOutput_team_initialize: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_list_tools: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        tools: Record<string, never>[];
      };
    };
    ShellAuthOutput_team_ping: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        available: boolean;
      };
    };
    /**
     * Network register result
     * @description Authenticated HTTPS registration result, not a signed network frame or authorization token. created describes only this call's final committed transaction; never replay a historical true. See README.md#https-register-initialization for the one-use initialization boundary.
     */
    'register-result.schema': {
      /** Format: uuid */
      tenant: string;
      realm: string;
      node: string;
      /** Format: uuid */
      device: string;
      systemAddress: string;
      /** @description true only if this call actually INSERTed the authenticated device's new node in its final committed transaction. An existing node, idempotent retry or concurrent loser returns false; transaction retries cannot retain true from a rolled-back attempt. Only the current authenticated HTTPS request generation may consume true once, after fully verified signed READY confirms incarnation and the complete ledger is atomically committed. Never persist as reusable initialization permission. */
      created: boolean;
    };
    ShellAuthOutput_network_open: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
        registration: components['schemas']['register-result.schema'];
        binding: {
          /** Format: uuid */
          executionContextId: string;
          /** Format: uuid */
          shellEpoch: string;
          /** Format: int64 */
          revision: number;
        };
      };
    };
    ShellAuthOutput_upload_open: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        uploadId: string;
        /** Format: uuid */
        streamId: string;
        /** @enum {integer} */
        maxChunkBytes: 65536;
        /** @enum {integer} */
        creditBytes: 1048576;
      };
    };
    ShellAuthOutput_upload_finish: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: components['schemas']['ShellAuthUploadReference'];
    };
    ShellAuthOutput_network_send: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: int64 */
        sequence: number;
      };
    };
    ShellAuthOutput_network_submit: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** @description 解码后最多65536字节，必须严格验证 network/send.schema.json#/$defs/SendResult；保留原始受签字节，不经生成模型重序列化。 */
        resultWire: string;
      };
    };
    ShellAuthOutput_getNetworkKeys: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** @description 解码后最多65536字节，必须严格验证 network/keyset.schema.json#/$defs/SignedKeyset；保留原始受签字节，不经生成模型重序列化。 */
        wire: string;
      };
    };
    ShellAuthOutput_getNetworkKeyAnnouncement: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** @description 解码后最多65536字节，必须严格验证 network/protocol.schema.json#/$defs/KeysetAnnouncement；保留原始受签字节，不经生成模型重序列化。 */
        wire: string;
      };
    };
    ShellAuthOutput_profile_read: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /**
         * Format: uuid
         * @description Authoritative memberId from /me/device-session; never inferred from account.id. Profile account.id must match verified device JWT sub (accountId).
         */
        memberId: string;
        displayName: string;
      };
    };
    ShellAuthOutput_provider_diagnostic: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        available: boolean;
      };
    };
    ShellAuthOutput_team_list_endpoints: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_open_thread: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_reply_thread: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_read_thread: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_node_register: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_node_bind: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_node_pull: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_node_ack: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
    };
    ShellAuthOutput_team_node_revoke: {
      statusCode: number;
      headers?: {
        'content-type'?: string;
        'retry-after'?: string;
      };
      body: {
        /** Format: uuid */
        streamId: string;
      };
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
  exchangeShellAuthHostContext: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthHostContextCommand'];
      };
    };
    responses: {
      /** @description 受控IPC结果，成功与安全错误互斥；no-store且不记录消息体 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthHostContextResult'];
        };
      };
      /** @description OS/peer认证失败，消息体不进入broker */
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
  readShellAuthHostCredentials: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthHostCredentialsRequest'];
      };
    };
    responses: {
      /** @description 受控IPC结果，成功与安全错误互斥；no-store且不记录消息体 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthHostCredentialsResult'];
        };
      };
      /** @description OS/peer认证失败，消息体不进入broker */
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
  exchangeShellAuthBusinessCall: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthBusinessCall'];
      };
    };
    responses: {
      /** @description 受控操作结果 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthBusinessResult'];
        };
      };
      /** @description 安全拒绝 */
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
  subscribeShellAuthBusinessStream: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthStreamSubscription'];
      };
    };
    responses: {
      /** @description 重复的IPC事件消息投影；非单次HTTP JSON响应 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthStreamEvent'];
        };
      };
      /** @description 安全拒绝 */
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
  exchangeShellAuthStreamCommand: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthStreamCommand'];
      };
    };
    responses: {
      /** @description 流事件投影，实际IPC持续推送 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthStreamEvent'];
        };
      };
      /** @description 安全拒绝 */
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
  exchangeShellAuthLoopbackExchange: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthLoopbackExchange'];
      };
    };
    responses: {
      /** @description 受控操作结果 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthPageSession'];
        };
      };
      /** @description 安全拒绝 */
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
  getShellAuthAgentCredentials: {
    parameters: {
      query?: never;
      header: {
        'X-DSH-Session-Id': string;
        'X-Dsh-Team-Task-Capability': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 本人最新企业凭据；no-store */
      200: {
        headers: {
          'Cache-Control'?: 'no-store';
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentCredentials'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      405: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
    };
  };
  exchangeShellAuthAgentCredentialsRequest: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthAgentCredentialsRequest'];
      };
    };
    responses: {
      /** @description 本人最新企业凭据；no-store */
      200: {
        headers: {
          'Cache-Control'?: 'no-store';
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentCredentials'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      400: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      401: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      403: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      405: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      502: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
      /** @description 原型兼容错误；非 Problem */
      503: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ShellAuthAgentError'];
        };
      };
    };
  };
  exchangeShellAuthOutboundRequest: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthOutboundRequest'];
      };
    };
    responses: {
      /** @description 上游状态原样作为HTTP状态；成功/4xx/5xx均返回OutboundResult。本机认证/解析/运输失败返回AgentError，用error字段区分；不包含上游秘密。 */
      default: {
        headers: {
          'X-Dsh-Team-Outbound-Status'?: number;
          [name: string]: unknown;
        };
        content: {
          'application/json':
            | components['schemas']['ShellAuthOutboundResult']
            | components['schemas']['ShellAuthAgentError'];
        };
      };
    };
  };
  exchangeShellAuthPageMessage: {
    parameters: {
      query?: never;
      header: {
        'X-Auriga-Shell-Token': string;
        'X-Auriga-Shell-CSRF': string;
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ShellAuthPageCommand'];
      };
    };
    responses: {
      /** @description 受控操作结果 */
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
