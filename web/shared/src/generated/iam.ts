export interface paths {
  '/login/providers': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** 登录页可选的身份源 */
    get: operations['listLoginProviders'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/transactions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 开始一次登录（或绑定、补验证）
     * @description 返回第一步。按本入口下发 portal/admin 分名的登录事务cookie（见文首），绑定事务和浏览器，扫码类防转发。
     *     purpose=link / stepup 时需要已登录。
     *     nativeConnect 仅已显式登记的第一方public client在portal的team-account账号+TOTP登录使用，
     *     purpose固定login、returnTo省略或/、不带invitationCode；拒绝任何外来设备码/主体/权限和未知字段。
     *     创建响应nativeDevice只回一次，GET/SSE/advance不回放；完成登录仅批准本事务自己的grant，
     *     token端点仍须S256 verifier兑换device-only凭据，不得自动调用人工核码或consent接口。
     */
    post: operations['createLoginTransaction'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/transactions/{txId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        txId: components['parameters']['TxId'];
      };
      cookie?: never;
    };
    get: operations['getLoginTransaction'];
    put?: never;
    post?: never;
    delete: operations['cancelLoginTransaction'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/transactions/{txId}/events': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        txId: components['parameters']['TxId'];
      };
      cookie?: never;
    };
    /**
     * 订阅事务变化（SSE）
     * @description 每次步骤变化发一个 `event: step`，data 是 LoginTransaction 的 JSON；事务结束（完成、失败、过期、取消）后发
     *     `event: end` 并关闭。qr、device 步骤用它等待手机确认。
     */
    get: operations['streamLoginTransaction'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/transactions/{txId}/advance': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        txId: components['parameters']['TxId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 提交输入，推进到下一步
     * @description 完成时 step.type=complete，按本事务的规范入口/audience下发对应会话cookie并清除本audience事务cookie。
     *     另一个入口的会话和事务不变；IP管理台不下发可读CSRF cookie，完成后调用管理CSRF登记接口。
     */
    post: operations['advanceLoginTransaction'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/callback/{providerId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    /**
     * 跳转类身份源的固定回调地址
     * @description 用查询参数及本入口audience的登录事务cookie推进事务，然后302回本入口登录页（完成时回已验证的returnTo）。
     *     外部重定向的GET可缺Origin；只此精确回调允许，以固定provider回调、一次性state/nonce/PKCE、事务cookie及
     *     事务Origin/tenant/audience绑定代替普通写请求CSRF。不得推广到其它路径或其它方法。
     */
    get: operations['loginCallbackGet'];
    put?: never;
    /**
     * 回调（POST 绑定，例如 SAML、Apple）
     * @description 跨站 POST 不会带 SameSite=Lax 的事务 cookie：iam 把表单参数暂存（5 分钟、一次性，按随机 resume 键），
     *     然后 303 跳到同源的 GET /login/callback/{providerId}?resume=<键>，在 GET 里校验事务 cookie 后继续。
     *     这是精确协议例外：仅已登记provider固定POST回调，校验协议state/签名等，resume绑定原事务、
     *     tenant/audience/规范回调Origin，不产生会话或完成登录；最终GET还需对应audience事务cookie。
     */
    post: operations['loginCallbackPost'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/qr/{challengeId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        challengeId: string;
      };
      cookie?: never;
    };
    /**
     * 手机扫码后查看登录请求（内置扫码）
     * @description 手机端已登录；返回发起端信息，用户在手机上**输入**电脑上显示的两位比对码。
     *     purpose 为 link 或 stepup 时，手机端的账号必须等于事务所属的账号，否则 403。
     */
    get: operations['getQrChallenge'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/qr/{challengeId}/confirm': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        challengeId: string;
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['confirmQrChallenge'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/login/qr/{challengeId}/reject': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        challengeId: string;
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['rejectQrChallenge'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/logout': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * @description 校验本入口完整Origin和本会话CSRF，仅吊销实际消费的当前audience会话；清除该audience的session、
     *     登录事务及CSRF状态。门户登出使它关联的实例cookie/原WS失效，但不注销独立管理会话；管理登出反之。
     *     IP管理台清空全部已登记adminCsrfTokens；不能一并清除门户CSRF或另一入口的cookie。
     */
    post: operations['logout'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/session/csrf-tokens': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 为本人当前管理浏览器会话登记内存CSRF令牌（IP0，待IP1实现）
     * @description 仅IP模式管理入口启用。精确 POST /api/iam/v1/admin/session/csrf-tokens 是唯一免旧CSRF头的登记操作。
     *     edge必须验证可信admin listener、完整规范admin Origin、当前admin浏览器session、JSON及下列固定非简单头。
     *     普通同源POST fetch会自动带Origin；不使用可能没有Origin的GET恢复流程。缺失/null/多个/错scheme或端口Origin拒绝。
     *     固定头不是秘密，不能使其它路由免CSRF；不开放CORS/JSONP/重定向，不允许service/device或任意admin Bearer替代cookie。
     *     edge只向受信IAM hop换发绑定当前sid/tid的ctx=admin令牌；IAM按权威会话重查账号/tenant/audience/到期/吊销，
     *     实现使用AuthenticatedOnly+AdminOnly，不新增权限key，不允许请求指定目标会话。
     *
     *     每个tab/document先用crypto.getRandomValues生成32字节、无填充base64url token，仅保存在内存中并提交。
     *     IAM事务锁定当前会话，清理过期条目，只保存UTF-8 token的SHA-256小写十六进制hash及到期；不能从csrfHash还原原文。
     *     同一有效hash重试返回同一expiresAt且不延长；不同tab各有独立条目，不全局轮换或覆盖其它tab。
     *     每会话最多32个有效条目，满额返回429和Retry-After，不挤占有效条目；到期取min(当前时间+900秒,会话绝对/闲置到期)。
     *     响应回显本次提交的token，不需要持久化可逆明文；只有IAM事务提交成功才返回200，原文不进KV、日志或URL。
     *     刷新/新tab/丢失内存/到期使用新token；响应丢失只重试相同请求token。续期重发JWT不延长token期限。
     *     管理写请求的X-Auriga-CSRF由edge通过VerifyAdminSessionCsrf在线验证，失败关闭；IP模式不退回旧csrfHash/cookie比较。
     *     401回管理登录；403仅在确认会话仍有效且token过期/未知时登记新token，不自动重放已发出的非幂等业务写请求。
     *     登出、会话到期/吊销清空所有hash；并发登记与吊销锁同一权威会话，不能让KV延迟/旧续期写复活。
     *     响应仅同源JSON，Cache-Control:no-store、X-Content-Type-Options:nosniff；禁止持久化、URL、可读cookie和跨源postMessage。
     *     domain模式保留既有按源CSRF cookie流程，本操作不启用；完整规范及后续实测矩阵见deployment/README.md。
     */
    post: operations['registerAdminSessionCsrfToken'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getMe'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/sessions': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listMySessions'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/sessions/{sessionId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['revokeMySession'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/sessions/revoke-others': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['revokeMyOtherSessions'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/identities': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    /** @description 绑定新身份用 POST /login/transactions（purpose=link）。 */
    get: operations['listMyIdentities'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/identities/{identityId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** @description 需要 10 分钟内做过二次验证，否则 403 iam.step_up_required；不能解绑最后一个身份（409 iam.last_identity）。 */
    delete: operations['unlinkMyIdentity'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/password': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    /** @description 只适用于团队账号身份。 */
    put: operations['changeMyPassword'];
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/mfa': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getMyMfa'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/mfa/totp/enrollment': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['beginTotpEnrollment'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/mfa/totp/enrollment/{enrollmentId}/confirm': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['confirmTotpEnrollment'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/mfa/totp': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** @description 需要近期二次验证；策略要求必须有多因素时不能删除最后一种（409 iam.mfa_required）。 */
    delete: operations['deleteTotp'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/passkeys': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listMyPasskeys'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/passkeys/registration': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['beginPasskeyRegistration'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/passkeys/registration/{registrationId}/complete': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['completePasskeyRegistration'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/passkeys/{passkeyId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        passkeyId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['deleteMyPasskey'];
    options?: never;
    head?: never;
    patch: operations['renameMyPasskey'];
    trace?: never;
  };
  '/me/devices': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listMyDevices'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/me/devices/{deviceId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        deviceId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    /** @description 吊销设备凭据（刷新令牌），设备上的插件需要重新登录。 */
    delete: operations['revokeMyDevice'];
    options?: never;
    head?: never;
    patch: operations['renameMyDevice'];
    trace?: never;
  };
  '/device/{userCode}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        /** @description 8 位、不含元音的大写字母，可带一个连字符，例如 BCDF-GHJK。 */
        userCode: components['parameters']['UserCode'];
      };
      cookie?: never;
    };
    /** 门户 /device 页查看待确认的设备 */
    get: operations['getDeviceRequest'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/device/{userCode}/approve': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        /** @description 8 位、不含元音的大写字母，可带一个连字符，例如 BCDF-GHJK。 */
        userCode: components['parameters']['UserCode'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 要求 10 分钟内做过二次验证（否则 403 iam.step_up_required）；页面必须让用户手动输入或逐字核对 userCode，并显示发起端 IP。 */
    post: operations['approveDeviceRequest'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/device/{userCode}/deny': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        /** @description 8 位、不含元音的大写字母，可带一个连字符，例如 BCDF-GHJK。 */
        userCode: components['parameters']['UserCode'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['denyDeviceRequest'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/connectors': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listConnectorTypes'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/connectors/remote': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post: operations['registerRemoteConnector'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/connectors/remote/{type}/rotate-secret': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 生成新的共享密钥，只返回这一次。过渡期（默认 24 小时）内平台同时用新旧两个密钥签名，之后旧密钥作废。 */
    post: operations['rotateRemoteConnectorSecret'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/connectors/remote/{type}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['unregisterRemoteConnector'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/providers': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listProviders'];
    put?: never;
    post: operations['createProvider'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/providers/{providerId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['parameters']['ProviderId'];
      };
      cookie?: never;
    };
    get: operations['getProvider'];
    put?: never;
    post?: never;
    /** @description 应急账号不能删除（409 iam.provider_builtin）；还有账号只能用它登录时 409 iam.provider_in_use。 */
    delete: operations['deleteProvider'];
    options?: never;
    head?: never;
    /** @description JSON Merge Patch。config 里没给的机密字段保持原值。 */
    patch: operations['updateProvider'];
    trace?: never;
  };
  '/admin/providers/{providerId}/test': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['parameters']['ProviderId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** 校验配置并试连外部系统 */
    post: operations['testProvider'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/providers/{providerId}/directory/search': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['parameters']['ProviderId'];
      };
      cookie?: never;
    };
    /** 在身份源的目录里搜人（邀请时用；连接器需支持 directory 能力） */
    get: operations['searchProviderDirectory'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/accounts': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listAccounts'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/accounts/{accountId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get: operations['getAccount'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/accounts/{accountId}/mfa/reset': {
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
    /** @description 清除 TOTP 和 Passkey，下次登录要求重新设置；同时吊销全部会话。 */
    post: operations['resetAccountMfa'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/accounts/{accountId}/identities/{identityId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
        identityId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['unlinkAccountIdentity'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/accounts/{accountId}/sessions': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
      };
      cookie?: never;
    };
    get: operations['listAccountSessions'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/accounts/{accountId}/sessions/revoke': {
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
    post: operations['revokeAccountSessions'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/emergency': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['getEmergencyAccount'];
    put?: never;
    post?: never;
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/emergency/rotate': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 重新生成应急账号的口令和 TOTP 密钥。只返回这一次；通知所有超管。 */
    post: operations['rotateEmergencyAccount'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/service-accounts': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get: operations['listServiceAccounts'];
    put?: never;
    post: operations['createServiceAccount'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/service-accounts/{serviceAccountId}': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        serviceAccountId: components['parameters']['ServiceAccountId'];
      };
      cookie?: never;
    };
    get: operations['getServiceAccount'];
    put?: never;
    post?: never;
    delete: operations['deleteServiceAccount'];
    options?: never;
    head?: never;
    patch: operations['updateServiceAccount'];
    trace?: never;
  };
  '/admin/service-accounts/{serviceAccountId}/secrets': {
    parameters: {
      query?: never;
      header?: never;
      path: {
        serviceAccountId: components['parameters']['ServiceAccountId'];
      };
      cookie?: never;
    };
    get?: never;
    put?: never;
    /** @description 生成 client_credentials 用的密钥，只返回这一次。 */
    post: operations['createServiceAccountSecret'];
    delete?: never;
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/service-accounts/{serviceAccountId}/secrets/{secretId}': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    post?: never;
    delete: operations['deleteServiceAccountSecret'];
    options?: never;
    head?: never;
    patch?: never;
    trace?: never;
  };
  '/admin/keys/identity-header/rotate': {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    get?: never;
    put?: never;
    /**
     * 轮换 edge 身份头签名密钥
     * @description mode=scheduled：生成新的 NEXT；只有在所有运行中实例的规格都已包含它（或超过固定传播期，默认 24 小时）之后，edge 才把它切成 CURRENT，旧的转为 RETIRING。
     *     mode=compromise：立即把当前密钥标为 REVOKED，edge 改用新密钥，下发规格让实例立即删除被吊销的 kid（来不及更新的实例会暂时 401，这是预期）。
     */
    post: operations['rotateIdentityHeaderKey'];
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
    AdminCsrfTokenRegistration: {
      /** @description 每tab由crypto.getRandomValues生成32字节、规范无填充base64url。服务端校格式不能证明熵，客户端必须使用安全随机源。 */
      token: string;
    };
    AdminCsrfToken: {
      token: components['schemas']['AdminCsrfTokenRegistration']['token'];
      /** Format: date-time */
      expiresAt: string;
    };
    LoginProvider: {
      id: components['schemas']['Uuid'];
      connectorType: string;
      displayName: components['schemas']['LocalizedText'];
      buttonText?: components['schemas']['LocalizedText'];
      icon?: string;
      order: number;
      /**
       * @description 第一步的类型，登录页据此决定布局（例如直接显示二维码）。
       * @enum {string}
       */
      primaryStep: 'redirect' | 'qr' | 'form' | 'webauthn' | 'device';
    };
    CreateLoginTransaction: {
      providerId: components['schemas']['Uuid'];
      /**
       * @default login
       * @enum {string}
       */
      purpose: 'login' | 'link' | 'stepup';
      invitationCode?: string;
      nativeConnect?: components['schemas']['NativeConnect'];
      /** @description 完成后回到哪里；只接受本入口的绝对路径（不能以 // 或 /\ 开头），服务端另做规范化与路由白名单验证。会话audience由edge按受信listener和完整scheme/authority决定。 */
      returnTo?: string;
    };
    NativeConnect: {
      clientId: string;
      codeChallenge: string;
      /** @constant */
      codeChallengeMethod: 'S256';
      deviceName?: string;
    };
    CreateLoginTransactionResult: components['schemas']['LoginTransaction'] & {
      nativeDevice?: {
        /** @description 服务端自产256-bit明文，仅本创建响应一次回给Host内存。 */
        deviceCode: string;
        /** Format: date-time */
        expiresAt: string;
        /** @description token轮询间隔秒数，初始5。 */
        interval: number;
      };
    };
    LoginTransaction: {
      id: components['schemas']['Uuid'];
      /** @enum {string} */
      status: 'pending' | 'completed' | 'failed' | 'expired' | 'cancelled';
      /** @enum {string} */
      purpose: 'login' | 'link' | 'stepup';
      provider: {
        id: components['schemas']['Uuid'];
        connectorType: string;
        displayName: components['schemas']['LocalizedText'];
      };
      step: components['schemas']['ClientStep'];
      /** Format: date-time */
      expiresAt: string;
    };
    QrChallenge: {
      challengeId: string;
      initiator: {
        ip: string;
        userAgent?: string;
        /** @description 按 IP 粗略推断的位置，可能为空。 */
        location?: string;
      };
      /** @enum {string} */
      purpose: 'login' | 'link' | 'stepup';
      /** Format: date-time */
      expiresAt: string;
    };
    Me: {
      account: components['schemas']['Account'];
      session: components['schemas']['SessionView'];
      identities: components['schemas']['IdentityView'][];
      mfa: components['schemas']['MfaStatus'];
    };
    Account: {
      id: components['schemas']['Uuid'];
      tenantId: components['schemas']['Uuid'];
      displayName: string;
      email?: string;
      /** @enum {string} */
      status: 'active' | 'disabled';
      /** Format: date-time */
      createdAt: string;
      /** Format: date-time */
      lastLoginAt?: string;
    };
    AccountDetail: {
      account: components['schemas']['Account'];
      identities: components['schemas']['IdentityView'][];
      mfa: components['schemas']['MfaStatus'];
      activeSessions: number;
      devices: components['schemas']['Device'][];
    };
    SessionView: {
      id: components['schemas']['Uuid'];
      /** @enum {string} */
      audience: 'portal' | 'admin';
      /** @description 是否是发起本请求的会话。 */
      current: boolean;
      providerId: components['schemas']['Uuid'];
      mfa: boolean;
      ip?: string;
      userAgent?: string;
      /** Format: date-time */
      createdAt: string;
      /** Format: date-time */
      lastSeenAt?: string;
      /** Format: date-time */
      expiresAt: string;
    };
    IdentityView: {
      id: components['schemas']['Uuid'];
      provider: {
        id: components['schemas']['Uuid'];
        connectorType: string;
        displayName: components['schemas']['LocalizedText'];
      };
      /** @description 部分遮挡的外部主体，便于用户辨认。 */
      subjectHint?: string;
      displayName?: string;
      email?: string;
      /** Format: date-time */
      linkedAt: string;
      /** Format: date-time */
      lastLoginAt?: string;
    };
    MfaStatus: {
      totp: boolean;
      passkeys: number;
      /** @description 策略是否要求本账号必须有多因素。 */
      required: boolean;
    };
    TotpEnrollment: {
      enrollmentId: components['schemas']['Uuid'];
      /** @description Base32。 */
      secret: string;
      otpauthUri: string;
      /** Format: date-time */
      expiresAt: string;
    };
    Passkey: {
      id: components['schemas']['Uuid'];
      name: string;
      /** Format: date-time */
      createdAt: string;
      /** Format: date-time */
      lastUsedAt?: string;
      /** @description 是否是可同步的 Passkey。 */
      backedUp?: boolean;
    };
    Device: {
      id: components['schemas']['Uuid'];
      name: string;
      /** @enum {string} */
      kind: 'dsh_connect' | 'cli';
      /** @description 例如 `windows/amd64`。 */
      platform?: string;
      clientVersion?: string;
      /** Format: date-time */
      createdAt: string;
      /** Format: date-time */
      lastSeenAt?: string;
      lastIp?: string;
    };
    DeviceRequest: {
      userCode: string;
      clientId: string;
      clientName: components['schemas']['LocalizedText'];
      scopes: string[];
      device: {
        name?: string;
        platform?: string;
        clientVersion?: string;
        ip: string;
        /** @description 按 IP 粗略推断的位置，可能为空。 */
        location?: string;
      };
      /** Format: date-time */
      expiresAt: string;
    };
    ConnectorType: {
      descriptor: components['schemas']['ConnectorDescriptor'];
      /** @enum {string} */
      source: 'builtin' | 'plugin' | 'remote';
      /** Format: uri */
      remoteUrl?: string;
      /** @description 插件加载失败或 SPI 版本不兼容时的原因。 */
      loadError?: string;
    };
    /** @description 身份源实例的准入策略（身份源设计第六节）。 */
    AdmissionPolicy: {
      /**
       * @default invite_only
       * @enum {string}
       */
      mode: 'invite_only' | 'auto_provision' | 'linked_only';
      /**
       * @description 是否信任连接器上报的 mfaPerformed；false 时平台按 mfa 设置自己补验证。
       * @default false
       */
      trustExternalMfa: boolean;
      /** @description mode=auto_provision 时，外部属性满足任一规则即自动开通。规则用到 email 时，ExternalProfile.emailVerified 不为 true 一律不匹配。 */
      autoProvisionRules?: {
        /** @description 外部属性路径，例如 `email`、`organization.department`、`attributes.groups`。 */
        attribute: string;
        /** @enum {string} */
        operator: 'equals' | 'suffix' | 'prefix' | 'in' | 'contains';
        value: string | string[];
        /**
         * @description attribute 有对应的"已验证"标记时（例如 email ↔ emailVerified），要求已验证。
         * @default true
         */
        requireVerified: boolean;
      }[];
      /**
       * @default member
       * @enum {string}
       */
      defaultMemberType: 'member' | 'external';
      /** @description 映射到组时，保存配置前检查组的有效权限是否在操作者权限之内（超管上限，org.grant_exceeds_own）。 */
      attributeMapping?: {
        from: string;
        /** @enum {string} */
        to: 'displayName' | 'email' | 'group';
        /** @description to=group 时，外部值等于它才映射。 */
        match?: string;
        groupId?: components['schemas']['Uuid'];
      }[];
      /**
       * @default new_device
       * @enum {string}
       */
      mfa: 'never' | 'always' | 'new_device';
      /** @default 604800 */
      sessionTtlSeconds: number;
      /** @default 86400 */
      idleTimeoutSeconds: number;
      /**
       * @description 0 表示不复核；需要连接器支持 session_check。
       * @default 0
       */
      externalSessionCheckSeconds: number;
    };
    ProviderDisplay: {
      name: components['schemas']['LocalizedText'];
      buttonText?: components['schemas']['LocalizedText'];
      icon?: string;
      /** @default 100 */
      order: number;
    };
    ProviderInput: {
      connectorType: string;
      display: components['schemas']['ProviderDisplay'];
      /** @description 按连接器的 configSchema 校验。 */
      config: Record<string, never>;
      /** @default true */
      enabled: boolean;
      admission?: components['schemas']['AdmissionPolicy'];
    };
    ProviderPatch: {
      display?: components['schemas']['ProviderDisplay'];
      config?: Record<string, never>;
      enabled?: boolean;
      admission?: components['schemas']['AdmissionPolicy'];
    };
    Provider: {
      id: components['schemas']['Uuid'];
      connectorType: string;
      /** @description 内置实例（应急账号）不能删除。 */
      builtin: boolean;
      display: components['schemas']['ProviderDisplay'];
      /** @description 机密字段不回显，只返回 `{"x-secret-set": true}` 占位。 */
      config: Record<string, never>;
      enabled: boolean;
      admission: components['schemas']['AdmissionPolicy'];
      /**
       * Format: uri
       * @description 填到外部系统里的回调地址。
       */
      callbackUrl: string;
      /** Format: date-time */
      createdAt: string;
      /** Format: date-time */
      updatedAt: string;
    };
    EmergencyAccount: {
      username: string;
      totpConfigured: boolean;
      /** Format: date-time */
      lastLoginAt?: string;
      /** Format: date-time */
      rotatedAt?: string;
    };
    /** @description 服务账号的角色在 org 用角色绑定管理（subject.type=service_account），这里不重复保存。 */
    ServiceAccountInput: {
      name: string;
      description?: string;
      /** @default true */
      enabled: boolean;
    };
    ServiceAccount: {
      id: components['schemas']['Uuid'];
      name: string;
      description?: string;
      enabled: boolean;
      clientId: string;
      secrets: {
        secretId: components['schemas']['Uuid'];
        /** Format: date-time */
        createdAt: string;
        /** Format: date-time */
        expiresAt?: string;
        /** Format: date-time */
        lastUsedAt?: string;
      }[];
      /** Format: date-time */
      createdAt: string;
    };
    /** Format: uuid */
    Uuid: string;
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
    /** @description 跳转到外部地址（OIDC、OAuth、SAML、CAS、微信等），外部系统回调平台固定的回调地址后继续。 */
    RedirectStep: {
      /** @constant */
      type: 'redirect';
      /** Format: uri */
      url: string;
      /**
       * @default GET
       * @enum {unknown}
       */
      method: 'GET' | 'POST';
      /** @description method=POST 时用自动提交表单发送的字段（例如 SAML 的 SAMLRequest、RelayState）。 */
      params?: {
        [key: string]: string;
      };
    };
    /** @description 显示二维码和两位比对码；登录页订阅事务事件（SSE），手机确认后继续。比对码防止二维码被转发到别人的浏览器登录。 */
    QrStep: {
      /** @constant */
      type: 'qr';
      /** @description 要编码进二维码的字符串（通常是 URL）。 */
      content: string;
      /** @description 两位比对码。手机端确认时必须**输入**这两位数字（不是从候选里选）。 */
      matchCode: string;
      /**
       * @default waiting
       * @enum {unknown}
       */
      status: 'waiting' | 'scanned';
      /** Format: date-time */
      expiresAt: string;
      /**
       * @description 不支持 SSE 时的轮询间隔。
       * @default 2000
       */
      pollIntervalMs: number;
      hint?: components['schemas']['LocalizedText'];
    };
    FormField: {
      name: string;
      /** @enum {unknown} */
      kind:
        | 'text'
        | 'password'
        | 'email'
        | 'tel'
        | 'otp'
        | 'select'
        | 'checkbox'
        | 'hidden'
        | 'info';
      label?: components['schemas']['LocalizedText'];
      placeholder?: components['schemas']['LocalizedText'];
      help?: components['schemas']['LocalizedText'];
      /** @default false */
      required: boolean;
      /** @description HTML autocomplete 值，例如 `username`、`current-password`、`one-time-code`。 */
      autocomplete?: string;
      pattern?: string;
      minLength?: number;
      maxLength?: number;
      /** @description kind=select 时的选项。 */
      options?: {
        value: string;
        label: components['schemas']['LocalizedText'];
      }[];
      /** @description 默认值；kind=hidden 时原样提交。kind=info 时不用。 */
      value?: string | boolean;
    };
    StepError: {
      /** @description 错误码，例如 `iam.invalid_credentials`。 */
      code: string;
      message?: components['schemas']['LocalizedText'];
    };
    /** @description 按字段定义渲染表单（口令、LDAP、TOTP、注册资料等），提交后继续。 */
    FormStep: {
      /** @constant */
      type: 'form';
      /** @description 表单标识，提交时原样带回，例如 `password`、`totp`、`register`。 */
      formId: string;
      title?: components['schemas']['LocalizedText'];
      description?: components['schemas']['LocalizedText'];
      fields: components['schemas']['FormField'][];
      submitLabel?: components['schemas']['LocalizedText'];
      /** @description 上一次提交的错误（整表）。 */
      error?: components['schemas']['StepError'];
      /** @description 上一次提交的字段错误，键是字段名。 */
      fieldErrors?: {
        [key: string]: components['schemas']['StepError'];
      };
    };
    /** @description 调用浏览器 WebAuthn 接口（Passkey）。publicKey 是 WebAuthn Level 3 的 JSON 形式（PublicKeyCredentialRequestOptionsJSON / PublicKeyCredentialCreationOptionsJSON）。 */
    WebAuthnStep: {
      /** @constant */
      type: 'webauthn';
      /** @enum {unknown} */
      ceremony: 'get' | 'create';
      publicKey: Record<string, never>;
    };
    /** @description 设备码流程（RFC 8628）：显示用户码，在另一台已登录的设备上确认。 */
    DeviceStep: {
      /** @constant */
      type: 'device';
      userCode: string;
      /** Format: uri */
      verificationUri: string;
      /** Format: uri */
      verificationUriComplete?: string;
      /** Format: date-time */
      expiresAt: string;
      /** @default 5 */
      intervalSeconds: number;
    };
    /** @description 登录完成，会话 cookie 已下发。只出现在发给登录页的步骤里。 */
    CompleteStep: {
      /** @constant */
      type: 'complete';
      /** @description 只能是本平台的绝对路径（不能以 // 或 /\ 开头），服务端另做规范化。 */
      redirectTo: string;
    };
    /** @description 出错。登录页按 code 显示统一的错误文案；retryable=true 时提供重试按钮。 */
    FailStep: {
      /** @constant */
      type: 'fail';
      code: string;
      message?: components['schemas']['LocalizedText'];
      /** @default false */
      retryable: boolean;
    };
    /** @description 平台核心发给登录页的步骤。核心可能在连接器的步骤之后追加自己的步骤（多因素、绑定已有账号、注册资料）。 */
    ClientStep:
      | components['schemas']['RedirectStep']
      | components['schemas']['QrStep']
      | components['schemas']['FormStep']
      | components['schemas']['WebAuthnStep']
      | components['schemas']['DeviceStep']
      | components['schemas']['CompleteStep']
      | components['schemas']['FailStep'];
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
    /** @description 推进登录事务的输入。 */
    StepInput:
      | {
          /** @constant */
          type: 'callback';
          params: {
            [key: string]: string;
          };
        }
      | {
          /** @constant */
          type: 'form';
          formId: string;
          values: {
            [key: string]: string | boolean;
          };
        }
      | {
          /** @constant */
          type: 'webauthn';
          credential: Record<string, never>;
        }
      | {
          /** @constant */
          type: 'poll';
        }
      | {
          /** @constant */
          type: 'event';
          name: string;
          data?: Record<string, never>;
        };
    ConnectorDescriptor: {
      /** @description 连接器类型，全局唯一，例如 `team-account`、`emergency`、`oidc`、`example-sso`。远程连接器的类型必须以 `remote-` 开头，平台拒绝与内置或已有类型重名。 */
      type: string;
      /**
       * @description 实现的 SPI 主版本。平台拒绝加载不兼容的版本。
       * @constant
       */
      spiVersion: '1';
      /** @description 连接器自己的版本号。 */
      version?: string;
      displayName: components['schemas']['LocalizedText'];
      description?: components['schemas']['LocalizedText'];
      /** @description 图标：https 地址或 data URI（SVG / PNG，≤ 32 KB）。 */
      icon?: string;
      vendor?: string;
      /** Format: uri */
      docsUrl?: string;
      /** @description 可能返回的步骤类型（不含 done、fail）。 */
      steps: ('redirect' | 'qr' | 'form' | 'webauthn' | 'device')[];
      /** @description 可选能力：directory（搜人、离职同步）、session_check（复核外部会话）、credential（下游凭据）。 */
      capabilities?: ('directory' | 'session_check' | 'credential')[];
      /** @description 身份源实例配置的 JSON Schema（2020-12）。管理台按它生成表单；标了 `"x-secret": true` 的字段加密保存、不回显。 */
      configSchema: Record<string, never>;
      /**
       * @description 同一租户能否配置多个实例。
       * @default true
       */
      multipleInstances: boolean;
    };
    /** @description JSON Schema 校验之外的语义校验（例如 issuer 能否访问）。 */
    ValidateConfigResult: {
      ok: boolean;
      problems?: {
        /** @description JSON Pointer，例如 `/clientId`。 */
        path: string;
        code: string;
        message?: components['schemas']['LocalizedText'];
      }[];
    };
    organization: {
      company?: string;
      department?: string;
      departmentPath?: string[];
      employeeId?: string;
      title?: string;
    };
    DirectoryEntry: {
      subject: string;
      displayName?: string;
      email?: string;
      organization?: components['schemas']['organization'];
      /** @description 外部系统里是否在职 / 可用。 */
      active?: boolean;
    };
    DirectoryResult: {
      items: components['schemas']['DirectoryEntry'][];
    };
  };
  responses: {
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
    /** @description 资源不存在（common.not_found）。 */
    NotFound: {
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
    /** @description 没有登录或凭据无效（common.unauthenticated）。 */
    Unauthenticated: {
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
    /** @description If-Match 不匹配（common.precondition_failed）。 */
    PreconditionFailed: {
      headers: {
        [name: string]: unknown;
      };
      content: {
        'application/problem+json': components['schemas']['Problem'];
      };
    };
  };
  parameters: {
    TxId: components['schemas']['Uuid'];
    ProviderId: components['schemas']['Uuid'];
    AccountId: components['schemas']['Uuid'];
    ServiceAccountId: components['schemas']['Uuid'];
    /** @description 8 位、不含元音的大写字母，可带一个连字符，例如 BCDF-GHJK。 */
    UserCode: string;
    IdempotencyKey: string;
    /** @description 资源的 ETag；不匹配返回 412。 */
    IfMatch: string;
    Limit: number;
    /** @description 上一页响应里的 nextCursor。 */
    Cursor: string;
  };
  requestBodies: never;
  headers: {
    /** @description 多少秒后重试。 */
    RetryAfter: number;
    /** @description 资源版本。 */
    ETag: string;
  };
  pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
  listLoginProviders: {
    parameters: {
      query?: {
        purpose?: 'login' | 'link';
      };
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已启用的身份源，按显示顺序 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['LoginProvider'][];
          };
        };
      };
    };
  };
  createLoginTransaction: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['CreateLoginTransaction'];
      };
    };
    responses: {
      /** @description 已创建 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['CreateLoginTransactionResult'];
        };
      };
      400: components['responses']['BadRequest'];
      429: components['responses']['RateLimited'];
    };
  };
  getLoginTransaction: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        txId: components['parameters']['TxId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 当前状态和步骤 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['LoginTransaction'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  cancelLoginTransaction: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        txId: components['parameters']['TxId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已取消 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  streamLoginTransaction: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        txId: components['parameters']['TxId'];
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
  advanceLoginTransaction: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        txId: components['parameters']['TxId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['StepInput'];
      };
    };
    responses: {
      /** @description 下一步 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['LoginTransaction'];
        };
      };
      400: components['responses']['BadRequest'];
      404: components['responses']['NotFound'];
      /** @description 事务已结束或已过期（iam.login_tx_expired、iam.login_tx_finished） */
      409: {
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
  loginCallbackGet: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 回到登录页 */
      302: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  loginCallbackPost: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody?: {
      content: {
        'application/x-www-form-urlencoded': {
          [key: string]: string;
        };
      };
    };
    responses: {
      /** @description 跳到同源 GET 回调继续 */
      303: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  getQrChallenge: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        challengeId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 登录请求 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['QrChallenge'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  confirmQrChallenge: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        challengeId: string;
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          matchCode: string;
        };
      };
    };
    responses: {
      /** @description 已确认，发起端的事务继续 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      403: components['responses']['Forbidden'];
      /** @description 比对码不对（iam.qr_match_mismatch，挑战立即作废，发起端需重新扫码）或已过期 */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  rejectQrChallenge: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        challengeId: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已拒绝 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  logout: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 当前入口已退出，其cookie已清除 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  registerAdminSessionCsrfToken: {
    parameters: {
      query?: never;
      header: {
        /** @description 公开固定非简单头；仅此精确操作允许，不是秘密或其它路由的免检开关。 */
        'X-Auriga-CSRF-Bootstrap': 'admin-session-v1';
      };
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['AdminCsrfTokenRegistration'];
      };
    };
    responses: {
      /** @description 已登记或同hash重试；body的token严格等于提交值，不设任何CSRF cookie。 */
      200: {
        headers: {
          'Cache-Control'?: 'no-store';
          'X-Content-Type-Options'?: 'nosniff';
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AdminCsrfToken'];
        };
      };
      400: components['responses']['BadRequest'];
      401: components['responses']['Unauthenticated'];
      403: components['responses']['Forbidden'];
      429: components['responses']['RateLimited'];
      /** @description IAM权威会话或原子登记不可用（common.unavailable），不返回token成功。 */
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
  getMe: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 当前账号 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Me'];
        };
      };
      401: components['responses']['Unauthenticated'];
    };
  };
  listMySessions: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 我的会话 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['SessionView'][];
          };
        };
      };
    };
  };
  revokeMySession: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        sessionId: components['schemas']['Uuid'];
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
    };
  };
  revokeMyOtherSessions: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 吊销了几个 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            revoked: number;
          };
        };
      };
    };
  };
  listMyIdentities: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 我绑定的身份 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['IdentityView'][];
          };
        };
      };
    };
  };
  unlinkMyIdentity: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        identityId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已解绑 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      403: components['responses']['Forbidden'];
      409: components['responses']['Conflict'];
    };
  };
  changeMyPassword: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          currentPassword: string;
          newPassword: string;
        };
      };
    };
    responses: {
      /** @description 已修改，其他会话被吊销 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      400: components['responses']['BadRequest'];
      403: components['responses']['Forbidden'];
    };
  };
  getMyMfa: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 多因素状态 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['MfaStatus'];
        };
      };
    };
  };
  beginTotpEnrollment: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 新的 TOTP 密钥（只返回这一次），确认前不生效 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['TotpEnrollment'];
        };
      };
    };
  };
  confirmTotpEnrollment: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        enrollmentId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          code: string;
        };
      };
    };
    responses: {
      /** @description 已启用（替换旧的） */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      400: components['responses']['BadRequest'];
    };
  };
  deleteTotp: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
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
      403: components['responses']['Forbidden'];
      409: components['responses']['Conflict'];
    };
  };
  listMyPasskeys: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 我的 Passkey */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Passkey'][];
          };
        };
      };
    };
  };
  beginPasskeyRegistration: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description WebAuthn 注册参数 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            registrationId: components['schemas']['Uuid'];
            /** @description PublicKeyCredentialCreationOptionsJSON */
            publicKey: Record<string, never>;
          };
        };
      };
    };
  };
  completePasskeyRegistration: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        registrationId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          name?: string;
          /** @description RegistrationResponseJSON */
          credential: Record<string, never>;
        };
      };
    };
    responses: {
      /** @description 已添加 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Passkey'];
        };
      };
      400: components['responses']['BadRequest'];
    };
  };
  deleteMyPasskey: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        passkeyId: components['schemas']['Uuid'];
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
  renameMyPasskey: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        passkeyId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          name: string;
        };
      };
    };
    responses: {
      /** @description 已改名 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Passkey'];
        };
      };
    };
  };
  listMyDevices: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 我的设备（连接模式插件、命令行） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Device'][];
          };
        };
      };
    };
  };
  revokeMyDevice: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        deviceId: components['schemas']['Uuid'];
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
    };
  };
  renameMyDevice: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        deviceId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          name: string;
        };
      };
    };
    responses: {
      /** @description 已改名 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Device'];
        };
      };
    };
  };
  getDeviceRequest: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        /** @description 8 位、不含元音的大写字母，可带一个连字符，例如 BCDF-GHJK。 */
        userCode: components['parameters']['UserCode'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 设备请求 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DeviceRequest'];
        };
      };
      404: components['responses']['NotFound'];
      429: components['responses']['RateLimited'];
    };
  };
  approveDeviceRequest: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        /** @description 8 位、不含元音的大写字母，可带一个连字符，例如 BCDF-GHJK。 */
        userCode: components['parameters']['UserCode'];
      };
      cookie?: never;
    };
    requestBody?: {
      content: {
        'application/json': {
          deviceName?: string;
        };
      };
    };
    responses: {
      /** @description 已批准，设备轮询 /oauth2/token 即可拿到令牌 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      403: components['responses']['Forbidden'];
      409: components['responses']['Conflict'];
      429: components['responses']['RateLimited'];
    };
  };
  denyDeviceRequest: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        /** @description 8 位、不含元音的大写字母，可带一个连字符，例如 BCDF-GHJK。 */
        userCode: components['parameters']['UserCode'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已拒绝 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  listConnectorTypes: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 可用的连接器类型（内置、Java 插件、远程） */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['ConnectorType'][];
          };
        };
      };
    };
  };
  registerRemoteConnector: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          /** Format: uri */
          baseUrl: string;
        };
      };
    };
    responses: {
      /** @description 已登记。sharedSecret 只返回这一次，填到远程连接器里 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            connector: components['schemas']['ConnectorType'];
            sharedSecret: string;
          };
        };
      };
      400: components['responses']['BadRequest'];
    };
  };
  rotateRemoteConnectorSecret: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        type: string;
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 新密钥 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            sharedSecret: string;
            /** Format: date-time */
            oldSecretExpiresAt: string;
          };
        };
      };
    };
  };
  unregisterRemoteConnector: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        type: string;
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
      /** @description 还有身份源实例在用（iam.connector_in_use） */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listProviders: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 身份源实例 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Provider'][];
          };
        };
      };
    };
  };
  createProvider: {
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
      /** @description 已创建 */
      201: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Provider'];
        };
      };
      400: components['responses']['BadRequest'];
    };
  };
  getProvider: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['parameters']['ProviderId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 身份源实例（机密字段不回显） */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Provider'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  deleteProvider: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['parameters']['ProviderId'];
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
  updateProvider: {
    parameters: {
      query?: never;
      header?: {
        /** @description 资源的 ETag；不匹配返回 412。 */
        'If-Match'?: components['parameters']['IfMatch'];
      };
      path: {
        providerId: components['parameters']['ProviderId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': components['schemas']['ProviderPatch'];
      };
    };
    responses: {
      /** @description 已更新 */
      200: {
        headers: {
          ETag: components['headers']['ETag'];
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['Provider'];
        };
      };
      400: components['responses']['BadRequest'];
      412: components['responses']['PreconditionFailed'];
    };
  };
  testProvider: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        providerId: components['parameters']['ProviderId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 结果 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ValidateConfigResult'];
        };
      };
    };
  };
  searchProviderDirectory: {
    parameters: {
      query: {
        q: string;
        limit?: components['parameters']['Limit'];
      };
      header?: never;
      path: {
        providerId: components['parameters']['ProviderId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 结果 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['DirectoryResult'];
        };
      };
      /** @description 连接器不支持 directory（iam.capability_unsupported） */
      409: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/problem+json': components['schemas']['Problem'];
        };
      };
    };
  };
  listAccounts: {
    parameters: {
      query?: {
        /** @description 按显示名、邮箱、外部主体搜索。 */
        q?: string;
        providerId?: components['schemas']['Uuid'];
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
      /** @description 账号列表 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['Account'][];
            nextCursor?: string;
          };
        };
      };
    };
  };
  getAccount: {
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
      /** @description 账号详情 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['AccountDetail'];
        };
      };
      404: components['responses']['NotFound'];
    };
  };
  resetAccountMfa: {
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
      /** @description 已重置 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  unlinkAccountIdentity: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        accountId: components['parameters']['AccountId'];
        identityId: components['schemas']['Uuid'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已解绑 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
      409: components['responses']['Conflict'];
    };
  };
  listAccountSessions: {
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
      /** @description 会话 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['SessionView'][];
          };
        };
      };
    };
  };
  revokeAccountSessions: {
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
          /** @default false */
          includeDevices?: boolean;
        };
      };
    };
    responses: {
      /** @description 吊销了几个 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            revoked: number;
          };
        };
      };
    };
  };
  getEmergencyAccount: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 应急账号状态 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['EmergencyAccount'];
        };
      };
    };
  };
  rotateEmergencyAccount: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 新的凭据 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            username: string;
            password: string;
            totpSecret: string;
            otpauthUri: string;
          };
        };
      };
    };
  };
  listServiceAccounts: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 服务账号 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            items: components['schemas']['ServiceAccount'][];
          };
        };
      };
    };
  };
  createServiceAccount: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': components['schemas']['ServiceAccountInput'];
      };
    };
    responses: {
      /** @description 已创建 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ServiceAccount'];
        };
      };
    };
  };
  getServiceAccount: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        serviceAccountId: components['parameters']['ServiceAccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 服务账号 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ServiceAccount'];
        };
      };
    };
  };
  deleteServiceAccount: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        serviceAccountId: components['parameters']['ServiceAccountId'];
      };
      cookie?: never;
    };
    requestBody?: never;
    responses: {
      /** @description 已删除，所有密钥失效 */
      204: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
  updateServiceAccount: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        serviceAccountId: components['parameters']['ServiceAccountId'];
      };
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/merge-patch+json': components['schemas']['ServiceAccountInput'];
      };
    };
    responses: {
      /** @description 已更新 */
      200: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': components['schemas']['ServiceAccount'];
        };
      };
    };
  };
  createServiceAccountSecret: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        serviceAccountId: components['parameters']['ServiceAccountId'];
      };
      cookie?: never;
    };
    requestBody?: {
      content: {
        'application/json': {
          /** Format: date-time */
          expiresAt?: string;
        };
      };
    };
    responses: {
      /** @description 新密钥 */
      201: {
        headers: {
          [name: string]: unknown;
        };
        content: {
          'application/json': {
            secretId: components['schemas']['Uuid'];
            clientId: string;
            clientSecret: string;
            /** Format: date-time */
            expiresAt?: string;
          };
        };
      };
    };
  };
  deleteServiceAccountSecret: {
    parameters: {
      query?: never;
      header?: never;
      path: {
        serviceAccountId: components['parameters']['ServiceAccountId'];
        secretId: components['schemas']['Uuid'];
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
  rotateIdentityHeaderKey: {
    parameters: {
      query?: never;
      header?: never;
      path?: never;
      cookie?: never;
    };
    requestBody: {
      content: {
        'application/json': {
          /** @enum {string} */
          mode: 'scheduled' | 'compromise';
        };
      };
    };
    responses: {
      /** @description 已开始轮换 */
      202: {
        headers: {
          [name: string]: unknown;
        };
        content?: never;
      };
    };
  };
}
