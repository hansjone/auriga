export interface paths {
  '/api/resource/v1/packages': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 只列当前主体可见包；不可见或跨租户对象统一404。 */
    get: operations['listResourcePackages'];
    put?: never;
    /** @description 创建本人草稿；L4强制private且WorkspacePolicy.allowPersonalPlugins为true。 */
    post: operations['createResourcePackage'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 目录可见不自动授予包字节或安装权。 */
    get: operations['getResourcePackage'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    /** @description 仅本人草稿，不能更改主人、租户或等级；L4禁止共享。 */
    patch: operations['patchResourcePackage'];
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}': {
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
    /** @description 仅owner；不允许改L0门锁；等级与可见范围分别校验。 */
    patch: operations['patchResourcePackageLevel'];
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取可见包版本；不能列出其他作者的私有草稿。 */
    get: operations['listResourceVersions'];
    put?: never;
    /** @description 限64MiB gzip归档：流式暂存、摘要/归档验证后登记；不执行安装脚本，不代取任意URL。同版本内容不可覆盖。 L4私有分支在对象验证后签名保存，仍标未审核；不得由此进入共享published。 */
    post: operations['uploadResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取可见版本元数据；不把签名等同于ACL。 */
    get: operations['getResourceVersion'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}/submissions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 仅本人；送审绑定精确digest+revision，修改草稿使旧审核失效。 */
    post: operations['submitResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/reviews/{reviewId}/decisions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 审核者不能是版本作者；审核结果绑定精确摘要/修订。 */
    post: operations['decideResourceReview'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}/versions/{versionId}/publications': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 发布者不能是版本作者；审核后签名并提交不可变发布，审核者可与发布者相同。 */
    post: operations['publishResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}/versions/{versionId}/withdrawals': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 普通下架阻新下载/分发；malicious须签名撤销并由workspace/agent禁用，不能宣称立即清除。 */
    post: operations['withdrawResourceVersion'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/distributions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 仅本人实例；逐项核等级/可见/版本/策略，仅记录意图，202不代表已安装。 */
    post: operations['requestResourceDistribution'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/distributions/{distributionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 仅本人或显式授权管理者；必须generation=observedGeneration且ready才applied。 */
    get: operations['getResourceDistribution'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/packages/{packageId}/versions/{versionId}/content': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 先完成业务鉴权再读对象；不可见404，无无鉴权digest接口。 */
    get: operations['downloadResourceContent'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/internal/resource/v1/downloads/{downloadId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 仅内部节点mTLS。locator不构成授权，每次重试在线核SAN/吊销、授权expiry、租户/实例/版本/digest、当前调度/selectionRevision。首批只整包200，不支持Range。 */
    get: operations['downloadNodeResource'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/reviews': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 当前租户待审队列，不含其他租户或无审查授权的包。 */
    get: operations['listResourceReviews'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/reviews/{reviewId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 读取精确待审版本/摘要/修订，审核者不能是作者。 */
    get: operations['getResourceReview'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/api/resource/v1/admin/packages/{packageId}/versions/{versionId}/downgrade-authorizations': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 仅owner显式审计授权：核当前实例高水位/归属/精确目标版本、摘要、有效撤销清单，签发一次性限实例授权，最长10分钟；不降低持久高水位或授权其他旧版本。 */
    post: operations['issueResourceDowngrade'];
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
    ResourceEnvelope: {
      /** @constant */
      schemaVersion: 1;
      tenantId: components['schemas']['Uuid'];
      packageId: components['schemas']['Uuid'];
      versionId: components['schemas']['Uuid'];
      /** @description 同一租户/包的单调安全序号，不能用 SemVer 或 build metadata 替代。 */
      releaseSequence: components['schemas']['Counter'];
      /** @description 必须晚于 statement.issuedAt，最长 24 小时；续签不改变身份/字节/安全序号且必须重新授权审计。 */
      notAfter: components['schemas']['Time'];
      statement: components['schemas']['Statement'];
      signature: components['schemas']['Signature'];
    };
    RevocationList: {
      /** @constant */
      schemaVersion: 1;
      tenantId: components['schemas']['Uuid'];
      revision: components['schemas']['Counter'];
      issuedAt: components['schemas']['Time'];
      /** @description issuedAt 之后且最多 24 小时；离线最迟于此失效并停用受管资源。 */
      expiresAt: components['schemas']['Time'];
      revokedKids: string[];
      revokedArtifacts: components['schemas']['RevokedArtifact'][];
      signature: components['schemas']['Signature'];
    };
    DowngradeAuthorization: {
      /** @constant */
      schemaVersion: 1;
      authorizationId: components['schemas']['Uuid'];
      tenantId: components['schemas']['Uuid'];
      instanceId: components['schemas']['Uuid'];
      packageId: components['schemas']['Uuid'];
      versionId: components['schemas']['Uuid'];
      digest: components['schemas']['Digest'];
      fromReleaseSequence: components['schemas']['Counter'];
      toReleaseSequence: components['schemas']['Counter'];
      issuedAt: components['schemas']['Time'];
      expiresAt: components['schemas']['Time'];
      reason: string;
      signature: components['schemas']['Signature'];
    };
    Package: {
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
    /** @description 新建只允许本人L3待审草稿或L4/private个人包；level/owner/tenant由服务端决定，不能直接创建L0-L2。 */
    PackageInput: {
      name: string;
      /** @constant */
      kind: 'plugin';
      /** @enum {string} */
      visibility: 'tenant' | 'members' | 'groups' | 'private';
      groupIds?: string[];
    };
    PackagePatch: {
      name?: string;
      /** @enum {string} */
      visibility?: 'tenant' | 'members' | 'groups' | 'private';
      groupIds?: string[];
    };
    AdminPackagePatch: {
      /** @enum {string} */
      level?: 'l1' | 'l2' | 'l3';
      enabled?: boolean;
      /** Format: uuid */
      defaultVersionId?: string;
      reason: string;
    };
    PackagePage: {
      items: components['schemas']['Package'][];
      nextCursor?: string;
    };
    Version: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      version: string;
      digest: components['schemas']['Digest'];
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
      resourceEnvelope?: components['schemas']['ResourceEnvelope'];
      /** Format: date-time */
      createdAt: string;
    };
    UploadMetadata: {
      version: string;
      expectedDigest: components['schemas']['Digest'];
      manifest: {
        name: string;
        dshVersionRange: string;
        /** @enum {string} */
        os: 'linux' | 'windows' | 'any';
        /** @enum {string} */
        arch: 'amd64' | 'arm64' | 'any';
      };
    };
    Reason: {
      reason: string;
    };
    Review: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: components['schemas']['Digest'];
      /** Format: int64 */
      versionRevision: number;
      /** @enum {string} */
      state: 'pending' | 'approved' | 'rejected';
      /** Format: int64 */
      revision: number;
    };
    ReviewDecision: {
      /** @enum {string} */
      decision: 'approve' | 'reject';
      reason: string;
      /** Format: int64 */
      versionRevision: number;
      digest: components['schemas']['Digest'];
    };
    Withdrawal: {
      /** @enum {string} */
      mode: 'withdrawn' | 'malicious';
      reason: string;
    };
    DistributionInput: {
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
    Distribution: {
      /** Format: uuid */
      id: string;
      /** Format: uuid */
      instanceId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: int64 */
      selectionRevision: number;
      /** @enum {string} */
      state: 'requested' | 'resolved' | 'applying' | 'applied' | 'failed';
      /** Format: int64 */
      generation?: number;
      /** Format: int64 */
      observedGeneration?: number;
      ready?: boolean;
      errorCode?: string;
      /** Format: int64 */
      revision: number;
    };
    DownloadAuthorization: {
      /** Format: uuid */
      downloadId: string;
      /** Format: uuid */
      tenantId: string;
      /** Format: uuid */
      nodeId: string;
      /** Format: uuid */
      instanceId: string;
      /** Format: uuid */
      ownerId: string;
      /** Format: uuid */
      packageId: string;
      /** Format: uuid */
      versionId: string;
      digest: components['schemas']['Digest'];
      /** Format: int64 */
      selectionRevision: number;
      /** Format: date-time */
      expiresAt: string;
      /** Format: uri */
      url: string;
    };
    VersionPage: {
      items: components['schemas']['Version'][];
      nextCursor?: string;
    };
    ReviewPage: {
      items: components['schemas']['Review'][];
      nextCursor?: string;
    };
    DowngradeInput: {
      /** Format: uuid */
      instanceId: string;
      /** Format: int64 */
      fromReleaseSequence: number;
      reason: string;
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
    Digest: string;
    /** Format: uuid */
    Uuid: string;
    /** Format: int64 */
    Counter: number;
    /** Format: date-time */
    Time: string;
    Statement: {
      /** @constant */
      purpose: 'plugin';
      name: string;
      version: string;
      /** @enum {string} */
      os: 'linux' | 'windows' | 'any';
      /** @enum {string} */
      arch: 'amd64' | 'arm64' | 'any';
      digest: components['schemas']['Digest'];
      issuedAt: components['schemas']['Time'];
    };
    Signature: {
      /** @constant */
      alg: 'Ed25519';
      kid: string;
      /** @description 64 字节签名的 base64url 无填充编码；解码后须重编码比较，拒绝非规范尾位。 */
      value: string;
    };
    RevokedArtifact: {
      packageId: components['schemas']['Uuid'];
      versionId: components['schemas']['Uuid'];
      digest: components['schemas']['Digest'];
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
  listResourcePackages: {
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
          'application/json': components['schemas']['PackagePage'];
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
  createResourcePackage: {
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
        'application/json': components['schemas']['PackageInput'];
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
          'application/json': components['schemas']['Package'];
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
  getResourcePackage: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
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
          'application/json': components['schemas']['Package'];
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
  patchResourcePackage: {
    parameters: {
      query?: never;
      header: {
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['PackagePatch'];
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
          'application/json': components['schemas']['Package'];
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
      /** @description resource.level_locked */
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
      /** @description resource.state_conflict */
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
  patchResourcePackageLevel: {
    parameters: {
      query?: never;
      header: {
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['AdminPackagePatch'];
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
          'application/json': components['schemas']['Package'];
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
      /** @description resource.level_locked */
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
      /** @description resource.state_conflict */
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
  listResourceVersions: {
    parameters: {
      query?: {
        limit?: components['parameters']['Limit'];
        /** @description 上一页响应里的 nextCursor。 */
        cursor?: components['parameters']['Cursor'];
      };
      header?: never;
      path: {
        packageId: string;
      };
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
          'application/json': components['schemas']['VersionPage'];
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
  uploadResourceVersion: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path: {
        packageId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'multipart/form-data': {
          metadata: components['schemas']['UploadMetadata'];
          /** Format: binary */
          archive: string;
        };
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
          'application/json': components['schemas']['Version'];
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
      /** @description resource.level_locked, resource.download_denied */
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
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
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
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
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
      /** @description resource.storage_unavailable */
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
  getResourceVersion: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
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
          'application/json': components['schemas']['Version'];
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
  submitResourceVersion: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
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
          'application/json': components['schemas']['Review'];
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
      /** @description resource.state_conflict */
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
  decideResourceReview: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        reviewId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ReviewDecision'];
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
          'application/json': components['schemas']['Review'];
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
      /** @description resource.state_conflict */
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
  publishResourceVersion: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Reason'];
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
          'application/json': components['schemas']['Version'];
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
      /** @description resource.level_locked, resource.download_denied */
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
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
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
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
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
      /** @description resource.storage_unavailable */
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
  withdrawResourceVersion: {
    parameters: {
      query?: never;
      header: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
        /** @description 当前资源ETag；缺失/不匹配返回common.precondition_failed。 */
        'If-Match': string;
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['Withdrawal'];
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
          'application/json': components['schemas']['Version'];
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
      /** @description resource.level_locked, resource.download_denied */
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
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
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
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
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
      /** @description resource.storage_unavailable */
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
  requestResourceDistribution: {
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
        'application/json': components['schemas']['DistributionInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Distribution'];
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
      /** @description resource.level_locked, resource.download_denied */
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
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
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
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
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
      /** @description resource.storage_unavailable */
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
  getResourceDistribution: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        distributionId: string;
      };
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
          'application/json': components['schemas']['Distribution'];
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
  downloadResourceContent: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已授权的不可变归档，校验摘要/长度；不接受重定向。 */
      200: {
        headers: {
          /** @description 固定摘要 */
          ETag?: string;
          /** @description 完整对象长度 */
          'Content-Length'?: number;
          [name: string]: unknown;
        };
        content: {
          'application/gzip': string;
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
      /** @description resource.level_locked, resource.download_denied */
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
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
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
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
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
      /** @description resource.storage_unavailable */
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
  downloadNodeResource: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        downloadId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已授权的不可变归档，校验摘要/长度；不接受重定向。 */
      200: {
        headers: {
          /** @description 固定摘要 */
          ETag?: string;
          /** @description 完整对象长度 */
          'Content-Length'?: number;
          [name: string]: unknown;
        };
        content: {
          'application/gzip': string;
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
      /** @description resource.level_locked, resource.download_denied */
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
      /** @description resource.version_immutable, resource.review_required, resource.state_conflict */
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
      /** @description resource.digest_mismatch, resource.signature_invalid */
      422: {
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
      /** @description resource.storage_unavailable */
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
  listResourceReviews: {
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
          'application/json': components['schemas']['ReviewPage'];
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
  getResourceReview: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        reviewId: string;
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
          'application/json': components['schemas']['Review'];
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
  issueResourceDowngrade: {
    parameters: {
      query?: never;
      header?: {
        'Idempotency-Key'?: components['parameters']['IdempotencyKey'];
      };
      path: {
        packageId: string;
        versionId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['DowngradeInput'];
      };
    };
    responses: {
      /** @description 成功；响应不包含供应商密钥、令牌、prompt或生成正文（模型响应除外）。 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DowngradeAuthorization'];
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
      /** @description resource.level_locked */
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
      /** @description resource.state_conflict */
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
      /** @description resource.signature_invalid */
      422: {
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
