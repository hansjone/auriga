/** Native DSH main surface; Auriga settings live in its own tab. */
// @ts-nocheck — browser ModuleLoader globals
window.__ModuleLoader__.load({
  id: 'auriga-dsh/client',
  factory(require) {
    const NAV_NAME = 'Auriga';
    const React = require('react');
    const primitives = require('@deepseek-ai/dsh-client-ui-primitives');
    const { IconUsersOutlineRegular, IconUsersOutlineMedium } = primitives;
    const probe = (name) =>
      typeof primitives?.[name] === 'function' ||
      (typeof primitives?.[name] === 'object' && primitives[name] !== null)
        ? primitives[name]
        : undefined;
    const HostModal = probe('Modal');
    const HostButton = probe('Button');
    const HostSegmentedTabs = probe('SegmentedTabs');
    const HostStateDot = probe('StateDot');
    const HostMenu = probe('Menu');
    const IconSettingsOutlineMedium = probe('IconSettingsOutlineMedium');
    const IconUserOutlineMedium = probe('IconUserOutlineMedium');
    const IconEllipsisOutlineMedium = probe('IconEllipsisOutlineMedium');
    const h = React.createElement;
    const NS = 'auriga.shell';
    const PANEL_ID = 'auriga-team';
    const ELEMENT = 'auriga-team-panel';
    const loadedPanels = new WeakMap();
    const zh = {
      updateAvailable: '发现桌面新版',
      updateRequired: '当前版本已停用，请更新并重启',
      updateNow: '更新',
      updateCancel: '取消下载',
      updateDownloading: '正在下载',
      updateRestart: '更新已准备好，请关闭并重新打开 Auriga DSH',
      updateFailed: '更新失败，旧版和数据已保留，请重试',
      loginOtherWindow:
        '\u5df2\u5728\u5176\u4ed6\u7a97\u53e3\u767b\u5f55\u4e2d',
      loginWindowUnsupported:
        '\u5f53\u524d\u6d4f\u89c8\u5668\u4e0d\u652f\u6301\u5b89\u5168\u7684\u591a\u7a97\u53e3\u767b\u5f55\uff0c\u8bf7\u4f7f\u7528\u6258\u76d8\u5907\u7528\u767b\u5f55',
      panel: NAV_NAME,
      loading: '正在加载…',
      failed: '加载失败',
      retry: '重试',
      refresh: '刷新状态',
      syncModels: '同步已有模型能力',
      syncHint:
        '更新已添加模型的上下文和输出能力；新增模型请用原生“获取可用模型”。',
      syncPreview: '将覆盖下列模型的本地容量设置，显示名称和其他模型设置保留。',
      syncApply: '应用更新',
      syncCancel: '取消',
      syncCurrent: '没有需要更新的已知模型能力。',
      syncDone: '已同步；已打开的编辑表单请取消后重新打开。',
      syncConflict: '模型设置已更改，请重新获取后再确认。',
      syncFailed: '暂时无法同步模型能力，请稍后重试。',
      syncContext: '上下文',
      syncOutput: '输出',
      syncUnknown: '未设置',
      authRequired: '需要重新登录平台',
      dismiss: '关闭',
      connection: '总部连接',
      tools: '工具',
      about: '关于',
      mode: '运行模式',
      managed: '系统托管',
      standalone: '独立模式',
      connect: '连接模式',
      unconfigured: '尚未配置总部连接',
      connected: '平台已连接，Team 工具可用',
      disconnected: '平台连接暂不可用',
      endpoint: '本机通信端点',
      received: '已接受的消息',
      conversation: '在会话中使用 Team 工具',
      logout: '退出本机登录',
      connectHint:
        '接收消息会创建 DSH 会话。退出平台后，已启动的任务继续运行。',
      packageHint: '当前界面随插件安装包提供。尚未接通总部认证与资源服务。',
      uiSource: '界面来源',
      package: '插件安装包',
      signed: '已验证的平台清单',
      protocol: '协议版本',
      pluginVersion: '插件版本',
      panelVersion: '界面版本',
      ready: '已读取托管配置与已验证的界面清单',
      managedHint:
        '连接由系统提供，无法在此修改。此状态不代表资源读写或安装已获授权。',
      unavailableHint:
        '当前无法读取托管状态。连接模式与独立模式尚未接入宿主设置。',
      deployment: '默认部署位置',
      notWired: '尚未接入',
      toolsHint: '新建工具的默认部署位置将在工具创建能力接入后提供。',
      security: '账号、认证与安全',
      securityHint:
        '账号资料、认证关联、安全与协作偏好尚未接入 Auriga DSH 设置；需迁移原有功能。',
      auth: '登录状态已失效，请重新登录后重试。',
      forbidden: '没有读取托管状态的权限。',
      missing: '当前宿主尚未接入 Auriga DSH 托管设置。',
      unsupported: '协议不受支持，请升级插件。',
      verification: '界面清单验证未通过，请检查受信发行配置。',
      unavailable: '暂时无法加载，请稍后重试。',
      inbox: '系统收件箱',
      consent: '首联授权',
      consentMissing: '首联授权尚未接入当前宿主。',
      external: '外部消息',
      previewHint: '仅供预览，不会发送给模型。允许建立对话不代表允许自动执行。',
      onceHint:
        '允许本次仅可使用一次；中断、超时或失败后也不能重复使用，请重新发起请求。',
      cooldownHint:
        '此来源处于拒绝冷却期，请等待冷却结束后再允许；不会自动重试批准，也不会保存未来允许规则。',
      verified: '经核实的来源（DSH / 用户）',
      conversationLayer: '建立对话',
      executionLayer: '自动执行',
      allow_once: '允许本次',
      allow_source: '总是允许',
      deny: '拒绝',
      block: '屏蔽',
      source_node: '此 DSH',
      source_user: '此用户的所有 DSH',
      scope: '持续规则范围',
      chooseScope: '请选择范围',
      ask_each: '每次询问（推荐）',
      ask_eachHint: '每次首联都需确认',
      same_org: '同组织自动允许',
      same_orgHint: '同组织来源自动放行',
      allowlist: '仅白名单自动允许',
      allowlistHint: '仅名单内来源自动放行',
      deny_all: '全部拒绝',
      deny_allHint: '拒绝全部首联请求',
      conversationLayerHint: '是否允许对方建立对话',
      executionLayerHint: '是否允许自动执行工具',
      defaultPolicy: '默认授权策略',
      allowLists: '授权名单',
      listOpen: '查看',
      listCount: '项',
      locked: '由组织政策锁定，来源版本：',
      persistent: '总是允许名单',
      blocked: '屏蔽名单',
      revoke: '撤销',
      empty: '暂无待处理请求',
      noRules: '暂无规则',
      revokeHint: '撤销后重新核权；已执行的动作无法撤回，拒绝冷却仍然有效。',
      listSearch: '搜索名单',
      tab_overview: '概览',
      tab_market: '资源市场',
      tab_market_short: '市场',
      tab_mine: '我的资源',
      tab_mine_short: '资源',
      tab_team: 'Team 工具',
      tab_team_short: '团队',
      tab_inbox: '收件箱',
      tab_settings: '设置',
      nav_expand: '展开导航',
      nav_collapse: '收起导航',
      nav_label: 'Auriga 导航',
      signedOut: '未登录',
      localIdentity: '本机身份',
      uacMethod: 'UAC 扫码',
      uacPrompt: '使用公司 App 扫码登录本机，平台断开时也可登录。',
      uacStart: '获取二维码',
      uacRetry: '重新获取二维码',
      uacQr: '本机登录二维码',
      uac_waiting: '请扫码登录',
      uac_scanned: '已扫码，请在手机上确认',
      uac_verifying: '正在校验登录',
      uac_authenticated: '登录成功',
      uac_expired: '二维码已过期，请重新获取',
      uac_denied: '本次登录已被拒绝',
      uac_rejected: '登录未通过校验',
      uac_unavailable: '公司登录服务暂不可用，请稍后重试',
      uacSsoAppBound: '当前浏览器不支持静默 SSO，请直接扫码登录。',
      uacSsoUnreadable:
        '浏览器凭据库暂不可读（可能被占用）。可关闭浏览器后重启 Auriga，或直接扫码登录。',
      uacDestinationRejected:
        '公司登录地址与受控配置不一致，请管理员核对地址清单及 DNS 后重试。',
      uacDnsUnavailable: '公司登录域名暂时无法解析，请检查公司网络后重试。',
      uacSsoRejected: '浏览器登录凭据已失效，请扫码重新登录。',
      uac_cancelled: '已取消登录',
      uac_failed: '登录失败，请重新获取二维码',
      uacValidity: '剩余有效期',
      uacWarning: '请仅为本人扫码，不要转发二维码或共享屏幕。',
      uacExpiredRescan: '登录已失效，请重新扫码。',
      uacUnavailable: '暂时无法完成操作，请稍后重试。',
      uacConfigInvalid: '本机扫码配置不完整或无效，请联系管理员检查配置。',
      uacStorageUnavailable: '本机身份存储不可用，请联系管理员。',
      uacOwnerDomain:
        '本机工号绑定与当前企业登录配置不一致，请联系管理员核对。',
      uacSignedIn: '已登录本机',
      uacLocalHint:
        '本机身份只用于本机 DSH；资源市场、Team 工具等平台功能仍需登录平台。',
      uacLogout: '退出本机登录',
      ownerBound: '本机已绑定工号，只允许该工号登录。',
      ownerMismatch: '工号与本机绑定的主人不一致',
      empNo: '工号',
      department: '部门',
      uacTab: '公司扫码登录',
      teamTab: '团队账号登录',
      launcherSettings: '设置',
      launcherSignIn: '登录 Auriga',
      launcherUnknown: '身份状态未知',
      launcherLocal: '本地身份已登录',
      launcherLocalSignedOut: '本地身份未登录',
      launcherLocalUnknown: '本地身份状态未知',
      launcherLocalUnconfigured: '本地身份未配置',
      launcherPlatform: '平台已连接',
      launcherPlatformSignedIn: '平台已登录，连接不可用',
      launcherPlatformUnknown: '平台状态未知',
      launcherPlatformOffline: '平台未连接',
      loginEntryUnavailable: '登录界面暂未就绪，请在概览中重试。',
      launcherSignOut: '退出登录',
      launcherMore: '更多',
      launcherSignedIn: '已登录',
      launcherMenu: '账号与设置菜单',
      loginTime: '登录时间',
      revalidation: '定期复验',
      revalidationOk: '正常',
      revalidationRetrying: '复验暂不可达，正在重试',
      platformMismatch:
        '平台账号与本机 UAC 工号不一致，请退出平台并用本人账号登录。',
      platformMatched: '平台账号和本机 UAC 工号一致。',
      platformLogout: '退出平台',
      reference: '关联编号',
      cancel: '取消',
      platformCard: '平台连接',
      platform_managed: '系统托管',
      platform_unconfigured: '未配置平台',
      platform_authenticating: '正在登录平台',
      platform_unreachable: '平台不可达',
      platform_recovering: '平台不可达，正在自动恢复',
      platform_reauth: '平台登录已过期',
      platform_signedOut: '未登录平台',
      platform_connected: '平台已连接',
      platform_degraded: '已登录，通信未就绪',
      platformHint_managed: '连接由系统提供，无法在此修改。',
      platformHint_unconfigured: '在“设置”中填写团队平台地址后即可登录平台。',
      platformHint_authenticating: '请完成当前登录和设备确认。',
      platformHint_unreachable: '本机 DSH 照常可用；网络恢复后可重新连接。',
      platformHint_recovering:
        '仍保持当前登录信息，正在用已有身份重连；多次失败后再提示重新登录。',
      platformHint_reauth: '请重新登录平台；本机 DSH 和已有任务不受影响。',
      platformHint_signedOut: '登录平台后可使用资源市场、Team 工具和云端互通。',
      platformHint_connected: '资源市场、我的资源和 Team 工具可用。',
      platformHint_degraded: '平台已登录，消息网络仍在连接；稍后刷新。',
      platformHint_communicationUnavailable:
        '平台已登录，消息网络暂不可用。可以重新连接。',
      platformHint_brokerUnavailable:
        '本机暂未连上平台认证服务。可先使用本机 DSH，再重新连接。',
      platformHint_accessDisabled:
        '平台已停用此登录来源或成员。请联系团队管理员。',
      loginBrowserHint: '请在系统浏览器完成本站登录，并核对当前设备的用户码。',
      loginLinking: '正在连接平台，请稍候。',
      loginRateLimited: '平台请求过于频繁，稍后会自动重试。',
      loginProofRequired:
        '平台无法验证本机身份。可以重试，或请管理员核对平台配置。',
      loginPlatformFailed: '平台连接需要重试',
      retryLocalUac: '用本机身份重新连接平台',
      retryConnect: '重试连接',
      overviewWelcome: '欢迎使用 Auriga',
      overviewWelcomeBack: '欢迎回来，',
      overviewTagline: '探索更多能力，连接无限可能。',
      overviewExplore: '探索 Auriga',
      overviewLocal: '本地登录',
      overviewCloud: '云平台',
      overviewNetwork: '消息网络',
      overviewStatusOn: '已登录',
      overviewStatusOff: '未登录',
      overviewCloudOn: '已连接',
      overviewCloudAuth: '已认证，未连接',
      overviewCloudOff: '未连接',
      overviewNetworkOn: '就绪',
      overviewNetworkOff: '未就绪',
      overviewLocalOnly:
        '本地功能仍可用，部分云平台功能暂不可用。',
      overviewUpdateTitle: '发现新版本',
      overviewUpdateAction: '前往更新',
      overviewConsentTitle: '待处理授权请求',
      overviewConsentAction: '去处理',
      overviewConsentDetail: ' 项',
      cap_market_desc: '发现插件与技能，拓展 AI 能力。',
      cap_mine_desc: '管理已安装的插件与技能。',
      cap_team_desc: '连接团队能力，协同完成工作。',
      cap_inbox_desc: '查看成员消息与请求。',
      loginDetails: '错误详情',
      loginCompare: '请核对门户预填的确认码与此处一致，仅确认自己发起的登录：',
      loginRejected: '平台拒绝了登录请求，请联系管理员检查客户端配置。',
      loginBadCredentials: '口令或验证码错误，请重试。',
      loginMethods: '扫码或使用团队账号登录平台。',
      platformLogin: '登录平台',
      platformInlineLogin: '扫码或团队账号登录',
      configurePlatform: '配置平台地址',
      reconnect: '重新连接',
      loginDone: '平台登录成功。',
      loginExpired: '设备登录已过期，请重新登录。',
      loginUnreachable: '暂时无法连接平台，请稍后重试。',
      loginCancelled: '已取消平台登录。',
      loginDenied: '登录已拒绝，可重新登录。',
      identityLabel: '成员',
      platformNeeded: '此功能需要平台身份。请先在“概览”登录平台或检查连接。',
      gotoOverview: '前往概览',
    };
    const en = {
      updateAvailable: 'Desktop update available',
      updateRequired:
        'This version is no longer supported. Update and restart.',
      updateNow: 'Update',
      updateCancel: 'Cancel download',
      updateDownloading: 'Downloading',
      updateRestart: 'Update ready. Close and reopen Auriga DSH.',
      updateFailed:
        'Update failed. Previous version and data preserved. Retry.',
      loginOtherWindow: 'Sign-in is active in another window',
      loginWindowUnsupported:
        'This browser cannot coordinate sign-in windows. Use browser sign-in from the tray.',
      panel: NAV_NAME,
      loading: 'Loading…',
      failed: 'Failed to load',
      retry: 'Retry',
      refresh: 'Refresh status',
      syncModels: 'Sync existing model capabilities',
      syncHint:
        'Update context and output capabilities of added models. Use native Fetch available models to add more.',
      syncPreview:
        'This replaces the listed local capacity settings. Display names and other model settings are kept.',
      syncApply: 'Apply updates',
      syncCancel: 'Cancel',
      syncCurrent: 'No known capability changes for existing models.',
      syncDone:
        'Synced. Cancel and reopen any model editor that was already open.',
      syncConflict: 'Model settings changed. Fetch again before confirming.',
      syncFailed:
        'Model capability sync is temporarily unavailable. Please retry.',
      syncContext: 'Context',
      syncOutput: 'Output',
      syncUnknown: 'Not set',
      authRequired: 'Platform sign-in required',
      dismiss: 'Dismiss',
      connection: 'Headquarters connection',
      tools: 'Tools',
      about: 'About',
      mode: 'Runtime mode',
      managed: 'System managed',
      standalone: 'Standalone',
      connect: 'Connect',
      unconfigured: 'Headquarters connection is not configured',
      connected: 'Platform connected; Team tools available',
      disconnected: 'Platform connection unavailable',
      endpoint: 'Local communication endpoint',
      received: 'Accepted messages',
      conversation: 'Use Team tools in a conversation',
      logout: 'Sign out locally',
      connectHint:
        'Incoming messages create DSH sessions. Existing tasks continue after you sign out of the platform.',
      packageHint:
        'This UI ships with the plugin package. Headquarters authentication and resource services are not connected yet.',
      uiSource: 'UI source',
      package: 'Plugin package',
      signed: 'Verified platform manifest',
      protocol: 'Protocol version',
      pluginVersion: 'Plugin version',
      panelVersion: 'UI version',
      ready: 'Managed configuration and verified UI manifest loaded',
      managedHint:
        'The system supplies this connection. Resource access and installation require their own authorization.',
      unavailableHint:
        'Managed status is unavailable. Connect and standalone modes are not wired to host settings yet.',
      deployment: 'Default deployment location',
      notWired: 'Not connected yet',
      toolsHint:
        'The default deployment location will be available when tool creation is connected.',
      security: 'Account, authentication and security',
      securityHint:
        'Account details, identity linking, security and collaboration preferences still require migration into Auriga DSH settings.',
      auth: 'Your sign-in has expired. Sign in again and retry.',
      forbidden: 'You do not have permission to read managed status.',
      missing: 'This host has not connected Auriga DSH managed settings.',
      unsupported: 'Unsupported protocol. Upgrade the plugin.',
      verification:
        'UI manifest verification failed. Check the trusted release configuration.',
      unavailable: 'Temporarily unavailable. Try again later.',
      inbox: 'System inbox',
      consent: 'First-contact authorization',
      consentMissing:
        'First-contact authorization is not connected to this host.',
      external: 'External message',
      previewHint:
        'Preview only; never sent to a model. Opening a conversation does not permit automatic execution.',
      onceHint:
        'An allow-once permission can be used only once. Interruption, timeout or failure does not restore it; submit a new request.',
      cooldownHint:
        'This source is in a rejection cooldown; wait until it ends before allowing. Approval is never retried automatically and no future allow rule is saved.',
      verified: 'Verified source (DSH / user)',
      conversationLayer: 'Open conversation',
      executionLayer: 'Automatic execution',
      allow_once: 'Allow once',
      allow_source: 'Always allow',
      deny: 'Deny',
      block: 'Block',
      source_node: 'This DSH',
      source_user: 'All DSH nodes of this user',
      scope: 'Persistent rule scope',
      chooseScope: 'Choose a scope',
      ask_each: 'Ask each time (recommended)',
      ask_eachHint: 'Confirm every first contact',
      same_org: 'Automatically allow same organization',
      same_orgHint: 'Auto-allow sources from the same organization',
      allowlist: 'Automatically allow listed sources only',
      allowlistHint: 'Auto-allow listed sources only',
      deny_all: 'Deny all',
      deny_allHint: 'Deny every first-contact request',
      conversationLayerHint: 'Whether peers may open a conversation',
      executionLayerHint: 'Whether automatic tool execution is allowed',
      defaultPolicy: 'Default authorization policy',
      allowLists: 'Authorization lists',
      listOpen: 'View',
      listCount: 'items',
      locked: 'Locked by organization policy, source revision: ',
      persistent: 'Always-allow list',
      blocked: 'Block list',
      revoke: 'Revoke',
      empty: 'No pending requests',
      noRules: 'No rules',
      revokeHint:
        'Revocation requires authorization again. Completed actions cannot be undone; rejection cooldowns remain active.',
      listSearch: 'Search list',
      tab_overview: 'Overview',
      tab_market: 'Marketplace',
      tab_market_short: 'Market',
      tab_mine: 'My resources',
      tab_mine_short: 'Library',
      tab_team: 'Team tools',
      tab_team_short: 'Team',
      tab_inbox: 'Inbox',
      tab_settings: 'Settings',
      nav_expand: 'Expand navigation',
      nav_collapse: 'Collapse navigation',
      nav_label: 'Auriga navigation',
      signedOut: 'Signed out',
      localIdentity: 'Local identity',
      uacMethod: 'UAC scan',
      uacPrompt:
        'Scan with the company app to sign in locally, even when the platform is offline.',
      uacStart: 'Get QR code',
      uacRetry: 'New QR code',
      uacQr: 'Local sign-in QR code',
      uac_waiting: 'Scan to sign in',
      uac_scanned: 'Scanned. Confirm on your phone',
      uac_verifying: 'Verifying sign-in',
      uac_authenticated: 'Signed in',
      uac_expired: 'QR code expired. Request a new code.',
      uac_denied: 'Sign-in was denied',
      uac_rejected: 'Sign-in did not pass verification',
      uac_unavailable: 'Company sign-in is unavailable. Try again later.',
      uacSsoAppBound:
        'This browser does not support silent SSO. Scan the QR code to sign in.',
      uacSsoUnreadable:
        'The browser credential store cannot be read and may be locked. Close the browser and restart Auriga, or scan the QR code.',
      uacDestinationRejected:
        'The company sign-in address does not match the approved configuration. Ask an administrator to check the address list and DNS.',
      uacDnsUnavailable:
        'The company sign-in hostname cannot be resolved. Check the company network and retry.',
      uacSsoRejected:
        'The browser sign-in has expired. Scan the QR code to sign in again.',
      uac_cancelled: 'Sign-in cancelled',
      uac_failed: 'Sign-in failed. Request a new code.',
      uacValidity: 'Expires in',
      uacWarning:
        'Scan only for yourself. Do not share this code or your screen.',
      uacExpiredRescan: 'Your sign-in expired. Scan again.',
      uacUnavailable: 'Temporarily unavailable. Try again later.',
      uacConfigInvalid:
        'Local QR sign-in configuration is invalid. Contact your administrator.',
      uacStorageUnavailable:
        'Local identity storage is unavailable. Contact your administrator.',
      uacOwnerDomain:
        'The local owner binding does not match this organization’s sign-in configuration. Contact your administrator.',
      uacSignedIn: 'Signed in locally',
      uacLocalHint:
        'The local identity applies only to this DSH. Marketplace, Team tools and other platform features still require platform sign-in.',
      uacLogout: 'Sign out locally',
      ownerBound:
        'This device is bound to its owner; only that employee can sign in.',
      ownerMismatch: 'Employee does not match the bound owner',
      empNo: 'Employee no.',
      department: 'Department',
      uacTab: 'Company QR sign-in',
      teamTab: 'Team account',
      launcherSettings: 'Settings',
      launcherSignIn: 'Sign in to Auriga',
      launcherUnknown: 'Identity status unknown',
      launcherLocal: 'Local identity signed in',
      launcherLocalSignedOut: 'Local identity signed out',
      launcherLocalUnknown: 'Local identity status unknown',
      launcherLocalUnconfigured: 'Local identity not configured',
      launcherPlatform: 'Platform connected',
      launcherPlatformSignedIn: 'Platform signed in, connection unavailable',
      launcherPlatformUnknown: 'Platform status unknown',
      launcherPlatformOffline: 'Platform disconnected',
      loginEntryUnavailable: 'Sign-in is not ready. Retry from Overview.',
      launcherSignOut: 'Sign out',
      launcherMore: 'More',
      launcherSignedIn: 'Signed in',
      launcherMenu: 'Account and settings menu',
      loginTime: 'Signed in',
      revalidation: 'Revalidation',
      revalidationOk: 'OK',
      revalidationRetrying: 'Revalidation unreachable; retrying',
      platformMismatch:
        'The platform account differs from the local UAC employee. Sign out of the platform and sign in with your own account.',
      platformMatched: 'The platform account and local UAC employee match.',
      platformLogout: 'Sign out of the platform',
      reference: 'Reference',
      cancel: 'Cancel',
      platformCard: 'Platform connection',
      platform_managed: 'System managed',
      platform_unconfigured: 'Platform not configured',
      platform_authenticating: 'Signing in to platform',
      platform_unreachable: 'Platform unreachable',
      platform_recovering: 'Platform unreachable; reconnecting',
      platform_reauth: 'Platform sign-in expired',
      platform_signedOut: 'Not signed in to platform',
      platform_connected: 'Platform connected',
      platform_degraded: 'Signed in; network not ready',
      platformHint_managed: 'The system supplies this connection.',
      platformHint_unconfigured:
        'Enter your team platform address in Settings, then sign in.',
      platformHint_authenticating:
        'Complete this sign-in and device confirmation.',
      platformHint_unreachable:
        'Local DSH keeps working. Reconnect when the network returns.',
      platformHint_recovering:
        'Keeping the current sign-in while reconnecting with saved identity. Ask to sign in again only after repeated failures.',
      platformHint_reauth:
        'Sign in to the platform again. Local DSH and running tasks are unaffected.',
      platformHint_signedOut:
        'Sign in to use Marketplace, Team tools and cloud interop.',
      platformHint_connected:
        'Marketplace, My resources and Team tools are available.',
      platformHint_degraded:
        'Signed in; the message network is still connecting. Refresh shortly.',
      platformHint_communicationUnavailable:
        'Signed in; the message network is unavailable. Try reconnecting.',
      platformHint_brokerUnavailable:
        'This client cannot reach the platform sign-in service. You can use local DSH and reconnect later.',
      platformHint_accessDisabled:
        'The platform disabled this member or sign-in method. Contact your team administrator.',
      loginLinking: 'Connecting to the platform. Please wait.',
      loginRateLimited: 'Too many platform requests. Retrying shortly.',
      loginProofRequired:
        'The platform could not verify your local identity. Try again or ask your administrator to check the platform configuration.',
      loginPlatformFailed: 'Platform connection needs attention',
      retryLocalUac: 'Reconnect with local identity',
      retryConnect: 'Retry connection',
      overviewWelcome: 'Welcome to Auriga',
      overviewWelcomeBack: 'Welcome back, ',
      overviewTagline: 'Explore more capabilities and stay connected.',
      overviewExplore: 'Explore Auriga',
      overviewLocal: 'Local sign-in',
      overviewCloud: 'Cloud platform',
      overviewNetwork: 'Message network',
      overviewStatusOn: 'Signed in',
      overviewStatusOff: 'Signed out',
      overviewCloudOn: 'Connected',
      overviewCloudAuth: 'Authenticated, not connected',
      overviewCloudOff: 'Disconnected',
      overviewNetworkOn: 'Ready',
      overviewNetworkOff: 'Not ready',
      overviewLocalOnly:
        'Local features remain available; some cloud features are temporarily unavailable.',
      overviewUpdateTitle: 'Update available',
      overviewUpdateAction: 'Go to update',
      overviewConsentTitle: 'Pending authorization requests',
      overviewConsentAction: 'Review',
      overviewConsentDetail: ' items',
      cap_market_desc: 'Discover plugins and skills to extend AI capabilities.',
      cap_mine_desc: 'Manage installed plugins and skills.',
      cap_team_desc: 'Connect team capabilities and collaborate.',
      cap_inbox_desc: 'Review member messages and requests.',
      loginDetails: 'Error details',
      loginBrowserHint:
        'Finish sign-in in your system browser and compare the code for this device.',
      loginCompare:
        'Check that the portal confirmation code matches. Only approve a login you started:',
      loginRejected:
        'The platform rejected sign-in. Ask your administrator to check the client configuration.',
      loginBadCredentials:
        'Incorrect password or verification code. Try again.',
      loginMethods:
        'Sign in with your system browser, or scan a code or use a team account here.',
      platformLogin: 'Sign in to platform',
      platformBrowserLogin: 'Sign in with system browser',
      platformInlineLogin: 'Scan or team account',
      configurePlatform: 'Configure platform',
      reconnect: 'Reconnect',
      loginDone: 'Signed in to the platform.',
      loginExpired: 'Device sign-in expired. Sign in again.',
      loginUnreachable: 'Cannot reach the platform. Try again later.',
      loginCancelled: 'Platform sign-in cancelled.',
      loginDenied: 'Sign-in was denied. You can try again.',
      identityLabel: 'Member',
      platformNeeded:
        'This feature needs a platform identity. Sign in or check the connection in Overview.',
      gotoOverview: 'Go to Overview',
    };
    const css = `
.auriga-login-modal{pointer-events:auto;position:fixed;inset:0;margin:auto;z-index:1000;width:min(544px,calc(100vw - 32px));max-height:85vh;overflow:auto;padding:24px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:16px;background:var(--dsw-alias-bg-layer-1,Canvas);color:var(--dsw-alias-label-primary,CanvasText);font:inherit;box-shadow:0 0 0 100vmax #0006}

.auriga-shell{box-sizing:border-box;color:var(--dsw-alias-label-primary,inherit);font:inherit;min-width:0}
.auriga-shell button:not(.auriga-host-control,.auriga-nav-item,.auriga-nav-toggle,.auriga-settings-nav-item,.auriga-setting-icon-btn,.auriga-setting-switch,.auriga-setting-action,.auriga-tabs button,.auriga-tabs-fallback button){font:inherit;padding:5px 10px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:6px;background:var(--dsw-alias-bg-layer-1,transparent);color:var(--dsw-alias-label-primary,inherit);cursor:pointer}
.auriga-shell button:not(.auriga-host-control,.auriga-nav-item,.auriga-nav-toggle,.auriga-settings-nav-item,.auriga-setting-icon-btn,.auriga-setting-switch,.auriga-setting-action,.auriga-tabs button,.auriga-tabs-fallback button):hover,.auriga-tabs-fallback button:hover{background:var(--dsw-alias-interactive-bg-hover,transparent)}
.auriga-shell button:not(.auriga-host-control,.auriga-nav-item,.auriga-nav-toggle,.auriga-settings-nav-item,.auriga-setting-icon-btn,.auriga-setting-switch,.auriga-setting-action,.auriga-tabs button,.auriga-tabs-fallback button):focus-visible,.auriga-nav-item:focus-visible,.auriga-nav-toggle:focus-visible,.auriga-settings-nav-item:focus-visible,.auriga-setting-icon-btn:focus-visible,.auriga-tabs-fallback button:focus-visible,.auriga-shell summary:focus-visible{outline:var(--dsw-focus-ring-width,2px) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary,currentColor));outline-offset:2px}
.auriga-shell button:not(.auriga-host-control,.auriga-nav-item,.auriga-nav-toggle,.auriga-settings-nav-item,.auriga-setting-icon-btn,.auriga-setting-switch,.auriga-setting-action,.auriga-tabs button,.auriga-tabs-fallback button):disabled{opacity:.5;cursor:default}
.auriga-shell-status{padding:14px 0;display:flex;gap:12px;align-items:center;flex-wrap:wrap}
.auriga-settings{box-sizing:border-box;height:100%;min-height:0;padding:0;font-size:14px;line-height:1.5;display:flex;flex-direction:column;overflow:hidden}
.auriga-settings>h2{font-size:22px;font-weight:600;margin:0 0 8px;padding:16px 24px 0;flex:none}
.auriga-settings-shell{flex:1;min-height:0;display:flex;overflow:hidden}
.auriga-settings-nav{box-sizing:border-box;flex:none;width:176px;height:100%;overflow:auto;padding:12px 8px;border:0;border-inline-end:1px solid var(--dsw-alias-border-l1,currentColor);background:transparent}
.auriga-settings-nav-group{margin-bottom:16px}
.auriga-settings-nav-group-label{padding:4px 10px 8px;font-size:11px;font-weight:600;letter-spacing:.04em;color:var(--dsw-alias-label-tertiary,var(--dsw-alias-label-secondary,inherit));text-transform:uppercase}
.auriga-settings-nav-item{display:flex;align-items:center;gap:8px;width:100%;box-sizing:border-box;height:36px;margin:0 0 2px;padding:0 10px;border:0;border-radius:6px;background:transparent;color:var(--dsw-alias-label-secondary,inherit);font:inherit;font-size:13px;text-align:start;cursor:pointer}
.auriga-settings-nav-item:hover{background:var(--dsw-alias-interactive-bg-hover,transparent);color:var(--dsw-alias-label-primary,inherit)}
.auriga-settings-nav-item[aria-current=page]{background:var(--dsw-alias-interactive-bg-selected,var(--dsw-alias-bg-layer-2,transparent));color:var(--dsw-alias-state-business-primary,var(--dsw-alias-label-primary,inherit));font-weight:600}
.auriga-settings-nav-icon{flex:none;display:inline-flex;width:16px;height:16px;align-items:center;justify-content:center}
.auriga-settings-nav-label{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.auriga-settings-main{flex:1;min-width:0;min-height:0;display:flex;flex-direction:column;overflow:hidden}
.auriga-settings-main-inner{flex:1;min-height:0;overflow:auto;padding:32px;max-width:720px;width:100%;box-sizing:border-box;margin:0}
.auriga-settings-notice{flex:none;margin:0;padding:10px 32px 16px;max-width:720px;color:var(--dsw-alias-label-secondary,inherit);font-size:12px}
.auriga-settings-panel[hidden]{display:none}
.auriga-settings-page-title{margin:0 0 20px;font-size:22px;font-weight:600;line-height:1.3}
.auriga-settings-section{margin:0 0 24px}
.auriga-settings-section-title{margin:0 0 8px;font-size:14px;font-weight:600}
.auriga-settings-section-desc{margin:0 0 12px;color:var(--dsw-alias-label-secondary,inherit);font-size:12px}
.auriga-settings-section-body{display:flex;flex-direction:column;gap:0}
.auriga-setting-row{display:flex;align-items:center;justify-content:space-between;gap:16px;box-sizing:border-box;min-height:44px;padding:0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-setting-row.is-readonly{display:grid;grid-template-columns:minmax(96px,140px) minmax(0,1fr);align-items:center;column-gap:12px;min-height:44px}
.auriga-setting-row.is-readonly.has-copy{grid-template-columns:minmax(96px,140px) minmax(0,1fr) 28px}
.auriga-setting-row.is-status{display:grid;grid-template-columns:minmax(0,1fr) auto 80px;align-items:center;column-gap:16px;min-height:44px;padding:0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-setting-row-stack{flex-direction:column;align-items:stretch;min-height:0}
.auriga-setting-row-meta{min-width:0;flex:1;display:flex;align-items:center;gap:10px}
.auriga-setting-row-icon{flex:none;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-setting-row-copy{min-width:0;flex:1}
.auriga-setting-row-label{font-size:14px;font-weight:500;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-setting-row.is-status .auriga-setting-row-label{color:var(--dsw-alias-label-primary,inherit);font-weight:500}
.auriga-setting-row-desc{margin-top:2px;font-size:12px;color:var(--dsw-alias-label-secondary,inherit);overflow-wrap:anywhere}
.auriga-setting-row-control{flex:none;display:flex;align-items:center;justify-content:flex-end;gap:8px;min-width:0}
.auriga-setting-row-stack .auriga-setting-row-control{width:100%;justify-content:stretch}
.auriga-setting-row-status{min-width:0;display:flex;align-items:center;justify-content:flex-end}
.auriga-setting-row-action{display:flex;align-items:center;justify-content:flex-end;width:80px;min-width:80px}
.auriga-setting-row-action-slot{display:block;width:100%;min-height:28px}
.auriga-setting-row-action-slot.is-icon{width:28px;min-width:28px;min-height:28px}
.auriga-setting-row-actions{display:inline-flex;align-items:center;gap:8px;flex-wrap:wrap;justify-content:flex-end}
.auriga-setting-input{box-sizing:border-box;width:100%;min-width:180px;height:36px;padding:0 10px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:8px;background:var(--dsw-alias-bg-layer-1,transparent);color:inherit;font:inherit}
.auriga-setting-textarea{height:auto;padding:8px 10px;resize:vertical}
.auriga-setting-filepicker{position:relative;display:flex;align-items:center;gap:6px;min-width:0;flex:1}
.auriga-setting-filepicker-input{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);border:0}
.auriga-setting-filepicker-trigger{box-sizing:border-box;display:inline-flex;align-items:center;min-width:0;flex:1;height:36px;padding:0 10px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:8px;background:var(--dsw-alias-bg-layer-1,transparent);color:inherit;font:inherit;text-align:start;cursor:pointer}
.auriga-setting-filepicker-trigger:disabled{opacity:.5;cursor:default}
.auriga-setting-filepicker-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--dsw-alias-label-primary,inherit)}
.auriga-setting-filepicker-placeholder{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--dsw-alias-label-tertiary,var(--dsw-alias-label-secondary,inherit))}
.auriga-setting-filepicker-clear{box-sizing:border-box;display:inline-flex;align-items:center;justify-content:center;flex:none;width:28px;height:28px;padding:0;border:0;border-radius:6px;background:transparent;color:var(--dsw-alias-label-secondary,inherit);cursor:pointer}
.auriga-setting-filepicker-clear:hover{background:var(--dsw-alias-interactive-bg-hover,transparent);color:var(--dsw-alias-label-primary,inherit)}
.auriga-setting-filepicker-clear:disabled{opacity:.5;cursor:default}
.auriga-network-info{display:flex;flex-direction:column;gap:0;min-width:0}
.auriga-network-info-row{display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:40px;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-network-info-row:last-child{border-bottom:0}
.auriga-network-info-label{flex:none;font-size:13px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-network-info-value{min-width:0;flex:1;display:flex;align-items:center;justify-content:flex-end;gap:6px;font-size:14px;color:var(--dsw-alias-label-primary,inherit)}
.auriga-network-info-text{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;text-align:end}
.auriga-control-import-row{display:flex;align-items:center;gap:8px;min-width:0}
.auriga-control-import-row .auriga-setting-filepicker{flex:1}
.auriga-control-import-row .auriga-setting-action{flex:none;min-width:88px}
.auriga-receive-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:8px;flex-wrap:wrap}
.auriga-receive-search{flex:1;min-width:160px;max-width:280px}
.auriga-receive-count{font-size:12px;color:var(--dsw-alias-label-secondary,inherit);white-space:nowrap}
.auriga-receive-list{display:flex;flex-direction:column;gap:0;min-width:0;max-height:360px;overflow:auto}
.auriga-receive-list .auriga-setting-row:last-child{border-bottom:0}
.auriga-setting-action,.auriga-settings a.auriga-setting-action{box-sizing:border-box;display:inline-flex;align-items:center;justify-content:center;gap:0;min-height:32px;padding:0 12px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:8px;background:var(--dsw-alias-bg-layer-1,transparent);color:var(--dsw-alias-label-primary,inherit);font:inherit;font-size:13px;cursor:pointer;text-decoration:none}
.auriga-setting-action.has-icon{gap:6px}
.auriga-setting-action-icon{flex:none}
.auriga-setting-action.is-primary{background:var(--dsw-alias-button-primary-fill,var(--dsw-alias-state-business-primary,transparent));border-color:transparent;color:var(--dsw-alias-label-primary-foreground,inherit)}
.auriga-setting-action.is-danger{color:var(--dsw-alias-state-error-label,var(--dsw-alias-state-error-primary,inherit));border-color:var(--dsw-alias-state-error-primary,currentColor);background:transparent}
.auriga-setting-action.is-quiet{border-color:transparent;background:transparent;padding-inline:8px}
.auriga-setting-action.is-compact{min-height:28px;padding:0 10px;border-radius:6px;font-size:12px}
.auriga-setting-action.is-danger.is-compact{border-color:var(--dsw-alias-border-l2,currentColor);color:var(--dsw-alias-label-secondary,inherit);background:transparent}
.auriga-setting-action.is-danger.is-compact:hover{border-color:var(--dsw-alias-state-error-primary,currentColor);color:var(--dsw-alias-state-error-label,var(--dsw-alias-state-error-primary,inherit));background:var(--dsw-alias-interactive-bg-hover,transparent)}
.auriga-setting-action:disabled{opacity:.5;cursor:default}
.auriga-setting-icon-btn{box-sizing:border-box;display:inline-flex;align-items:center;justify-content:center;width:28px;height:28px;padding:0;border:0;border-radius:6px;background:transparent;color:var(--dsw-alias-label-secondary,inherit);cursor:pointer}
.auriga-setting-icon-btn:hover{background:var(--dsw-alias-interactive-bg-hover,transparent);color:var(--dsw-alias-label-primary,inherit)}
.auriga-setting-icon-btn:disabled{opacity:.5;cursor:default}
.auriga-setting-icon-btn.is-success{color:var(--dsw-alias-state-success-primary,#5f8f6a)}
.auriga-setting-icon-btn.is-busy svg{animation:auriga-settings-spin .8s linear infinite}
@keyframes auriga-settings-spin{to{transform:rotate(360deg)}}
.auriga-setting-actions{display:flex;gap:8px;flex-wrap:wrap;margin:8px 0}
.auriga-setting-status{margin:8px 0;font-size:12px;color:var(--dsw-alias-label-secondary,inherit);overflow-wrap:anywhere}
.auriga-setting-status.is-error{color:var(--dsw-alias-state-error-label,var(--dsw-alias-state-error-primary,inherit))}
.auriga-setting-status.is-warn{color:var(--dsw-alias-state-warn-label,var(--dsw-alias-state-warn-primary,inherit))}
.auriga-status-indicator{display:inline-flex;align-items:center;gap:6px;min-height:28px;font-size:12px;color:var(--dsw-alias-label-secondary,inherit);white-space:nowrap}
.auriga-status-indicator-dot{flex:none;width:7px;height:7px;border-radius:50%;background:var(--dsw-alias-label-tertiary,var(--dsw-alias-label-secondary,currentColor))}
.auriga-status-indicator-label{line-height:1.3}
.auriga-status-indicator.is-success{color:var(--dsw-alias-state-success-label,var(--dsw-alias-label-secondary,inherit))}
.auriga-status-indicator.is-success .auriga-status-indicator-dot{background:var(--dsw-alias-state-success-primary,#5f8f6a)}
.auriga-status-indicator.is-processing{color:var(--dsw-alias-state-warn-label,var(--dsw-alias-label-secondary,inherit))}
.auriga-status-indicator.is-processing .auriga-status-indicator-dot{background:var(--dsw-alias-state-warn-primary,currentColor)}
.auriga-status-indicator.is-warn{color:var(--dsw-alias-state-warn-label,var(--dsw-alias-label-secondary,inherit))}
.auriga-status-indicator.is-warn .auriga-status-indicator-dot{background:var(--dsw-alias-state-warn-primary,currentColor)}
.auriga-status-indicator.is-error{color:var(--dsw-alias-state-error-label,var(--dsw-alias-state-error-primary,inherit))}
.auriga-status-indicator.is-error .auriga-status-indicator-dot{background:var(--dsw-alias-state-error-primary,currentColor)}
.auriga-status-indicator.is-idle{color:var(--dsw-alias-label-secondary,inherit)}
.auriga-setting-switch{position:relative;width:40px;height:22px;padding:0;border:0;border-radius:999px;background:var(--dsw-alias-bg-layer-2,transparent);cursor:pointer;flex:none}
.auriga-setting-switch::after{content:"";position:absolute;top:2px;inset-inline-start:2px;width:18px;height:18px;border-radius:50%;background:var(--dsw-alias-bg-layer-1,Canvas);transition:transform 150ms ease}
.auriga-setting-switch[aria-checked=true]{background:var(--dsw-alias-state-business-primary,currentColor)}
.auriga-setting-switch[aria-checked=true]::after{transform:translateX(18px)}
.auriga-setting-select{position:relative;min-width:140px}
.auriga-setting-select-trigger{box-sizing:border-box;display:inline-flex;align-items:center;justify-content:space-between;gap:8px;width:100%;min-width:140px;height:36px;padding:0 10px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:8px;background:var(--dsw-alias-bg-layer-1,transparent);color:inherit;font:inherit;cursor:pointer}
.auriga-setting-select-menu{position:absolute;z-index:5;inset-inline-end:0;top:calc(100% + 4px);min-width:100%;padding:4px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:8px;background:var(--dsw-alias-bg-layer-2,var(--dsw-alias-bg-layer-1,Canvas))}
.auriga-setting-select-option{display:block;width:100%;padding:8px 10px;border:0;border-radius:6px;background:transparent;color:inherit;font:inherit;text-align:start;cursor:pointer}
.auriga-setting-select-option:hover,.auriga-setting-select-option[aria-selected=true],.auriga-setting-select-option.is-selected{background:var(--dsw-alias-interactive-bg-hover,transparent)}
.auriga-setting-select.is-rich{min-width:220px}
.auriga-setting-select-option.is-rich{display:flex;align-items:flex-start;justify-content:space-between;gap:10px}
.auriga-setting-select-option-copy{min-width:0;display:flex;flex-direction:column;gap:2px}
.auriga-setting-select-option-label{font-size:13px;line-height:1.3;color:var(--dsw-alias-label-primary,inherit)}
.auriga-setting-select-option-desc{font-size:12px;line-height:1.35;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-setting-select-check{flex:none;margin-top:2px;color:var(--dsw-alias-state-business-primary,var(--dsw-alias-label-primary,inherit))}
.auriga-consent-settings{display:flex;flex-direction:column;gap:0;min-width:0}
.auriga-consent-settings>.auriga-settings-card{margin-bottom:24px}
.auriga-consent-settings>.auriga-settings-card:last-child{margin-bottom:0}
.auriga-consent-list-entry{display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:44px;padding:0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor);cursor:pointer;list-style:none}
.auriga-consent-list-entry:last-of-type{border-bottom:0}
.auriga-consent-list-entry-meta{min-width:0;display:flex;flex-direction:column;gap:2px}
.auriga-consent-list-entry-title{font-size:14px;font-weight:500;color:var(--dsw-alias-label-primary,inherit)}
.auriga-consent-list-entry-count{font-size:12px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-consent-list-panel{padding:8px 0 4px;display:flex;flex-direction:column;gap:0;max-height:320px;overflow:auto}
.auriga-consent-member{display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:44px;padding:6px 0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-consent-member:last-child{border-bottom:0}
.auriga-consent-member-copy{min-width:0;flex:1;display:flex;flex-direction:column;gap:2px}
.auriga-consent-member-name{display:flex;align-items:baseline;gap:8px;min-width:0;flex-wrap:wrap}
.auriga-consent-member-primary{font-size:14px;font-weight:500;color:var(--dsw-alias-label-primary,inherit)}
.auriga-consent-member-secondary{font-size:12px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-consent-member-desc{font-size:12px;color:var(--dsw-alias-label-secondary,inherit);overflow-wrap:anywhere}
.auriga-consent-list-search{margin:0 0 8px}
.auriga-setting-readonly{display:flex;align-items:center;gap:8px;max-width:100%}
.auriga-setting-readonly-value-wrap{min-width:0;display:flex;align-items:center;justify-content:flex-end}
.auriga-setting-readonly-value{color:var(--dsw-alias-label-primary,inherit);overflow-wrap:anywhere;text-align:end;font-size:14px;line-height:1.35}
.auriga-setting-readonly-value.is-muted{color:var(--dsw-alias-label-tertiary,var(--dsw-alias-label-secondary,inherit))}
.auriga-account-card{box-sizing:border-box;margin:0 0 24px;padding:16px 18px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:10px;background:var(--dsw-alias-bg-layer-2,var(--dsw-alias-bg-layer-1,transparent))}
.auriga-account-card-header{display:flex;align-items:center;justify-content:space-between;gap:12px}
.auriga-account-card-profile{display:flex;align-items:center;gap:14px;min-width:0;flex:1}
.auriga-account-avatar{flex:none;width:48px;height:48px;border-radius:50%;display:inline-flex;align-items:center;justify-content:center;background:var(--dsw-alias-bg-layer-1,transparent);color:var(--dsw-alias-label-primary,inherit);font-size:16px;font-weight:600}
.auriga-account-card-identity{display:flex;align-items:center;gap:10px;min-width:0;flex-wrap:wrap}
.auriga-account-card-name{font-size:20px;font-weight:600;line-height:1.2;overflow-wrap:normal}
.auriga-account-card-empno{font-size:13px;line-height:1.2;color:var(--dsw-alias-label-secondary,inherit);white-space:nowrap}
.auriga-account-card-refresh{flex:none;display:flex;align-items:center}
.auriga-account-card-status{margin-top:14px;padding-top:4px;border-top:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-account-card-status .auriga-setting-row.is-status:last-child{border-bottom:0}
.auriga-platform-card{box-sizing:border-box;margin:0 0 24px;padding:20px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:10px;background:var(--dsw-alias-bg-layer-2,var(--dsw-alias-bg-layer-1,transparent))}
.auriga-platform-card-header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px}
.auriga-platform-card-brand{display:flex;align-items:center;gap:10px;min-width:0}
.auriga-platform-card-icon{flex:none;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-platform-card-title{font-size:16px;font-weight:600;line-height:1.3;color:var(--dsw-alias-label-primary,inherit)}
.auriga-platform-card-body{min-width:0}
.auriga-platform-card-fields{display:flex;flex-direction:column;gap:14px}
.auriga-platform-field{display:flex;flex-direction:column;gap:6px;min-width:0}
.auriga-platform-field-label{font-size:12px;line-height:1.3;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-platform-address{position:relative;display:flex;align-items:center}
.auriga-platform-address-icon{position:absolute;inset-inline-start:12px;z-index:1;color:var(--dsw-alias-label-secondary,inherit);pointer-events:none}
.auriga-platform-address .auriga-setting-input{padding-inline-start:36px}
.auriga-platform-card-actions{display:flex;align-items:center;justify-content:flex-end;flex-wrap:wrap;gap:8px;margin-top:16px}
.auriga-platform-card .auriga-setting-status{margin:12px 0 0}
.auriga-settings-card{box-sizing:border-box;margin:0 0 24px;padding:20px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:10px;background:var(--dsw-alias-bg-layer-2,var(--dsw-alias-bg-layer-1,transparent))}
.auriga-settings-card-header{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:8px}
.auriga-settings-card-header>.auriga-setting-action,.auriga-settings-card-header>.auriga-status-indicator{flex:none;margin-top:0}
.auriga-settings-card-brand{display:flex;align-items:flex-start;gap:10px;min-width:0;flex:1}
.auriga-settings-card-icon{flex:none;margin-top:2px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-settings-card-copy{min-width:0;display:flex;flex-direction:column;gap:2px}
.auriga-settings-card-title{font-size:16px;font-weight:600;line-height:1.3;color:var(--dsw-alias-label-primary,inherit)}
.auriga-settings-card-desc{margin:0;font-size:12px;line-height:1.45;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-settings-card-header+.auriga-settings-card-body{margin-top:8px}
.auriga-settings-card-body{min-width:0;display:flex;flex-direction:column;gap:0}
.auriga-settings-card-body>.auriga-setting-row:last-child{border-bottom:0}
.auriga-settings-card-group{display:flex;flex-direction:column;gap:6px;margin-top:16px;min-width:0}
.auriga-settings-card-group-label{font-size:14px;font-weight:500;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-settings-card-group-hint{font-size:12px;line-height:1.4;color:var(--dsw-alias-label-tertiary,var(--dsw-alias-label-secondary,inherit))}
.auriga-settings-card-group-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:16px;margin-bottom:8px}
.auriga-settings-card-group-head .auriga-settings-card-group-label{margin:0}
.auriga-settings-card-empty{margin:0;padding:12px 0;font-size:12px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-settings-card .auriga-setting-status{margin:12px 0 0}
.auriga-settings-card-footer{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-top:16px}
.auriga-settings-card-footer-left{min-width:0;flex:1;font-size:12px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-settings-card-actions{display:flex;align-items:center;justify-content:flex-end;flex-wrap:wrap;gap:8px;margin-inline-start:auto}
.auriga-proxy-rules{display:flex;flex-direction:column;gap:0;min-width:0}
.auriga-proxy-rule{display:grid;grid-template-columns:auto minmax(0,1.2fr) minmax(100px,0.8fr) minmax(0,1fr) auto auto auto;gap:6px;align-items:center;padding:8px 0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-proxy-rule:last-child{border-bottom:0}
.auriga-proxy-rule .auriga-setting-input{min-width:0;height:32px}
.auriga-proxy-rule .auriga-setting-select{min-width:100px}
.auriga-proxy-rule .auriga-setting-select-trigger{height:32px;min-width:100px}
.auriga-proxy-test-row{display:flex;align-items:center;gap:8px;min-width:0}
.auriga-proxy-test-row .auriga-setting-input{flex:1;min-width:0}
.auriga-proxy-test-row .auriga-setting-action{flex:none;min-width:96px}
.auriga-proxy-test-result{margin-top:12px;display:flex;flex-direction:column;gap:6px;min-width:0}
.auriga-proxy-test-result-detail{margin:0;font-size:12px;line-height:1.45;color:var(--dsw-alias-label-secondary,inherit);overflow-wrap:anywhere}
.auriga-desktop-summary-card{box-sizing:border-box;margin:0 0 24px;padding:20px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:10px;background:var(--dsw-alias-bg-layer-2,var(--dsw-alias-bg-layer-1,transparent))}
.auriga-desktop-summary-header{display:flex;align-items:center;justify-content:space-between;gap:16px}
.auriga-desktop-summary-brand{display:flex;align-items:center;gap:14px;min-width:0;flex:1}
.auriga-desktop-summary-logo{flex:none;border-radius:12px}
.auriga-desktop-summary-copy{min-width:0;display:flex;flex-direction:column;gap:4px}
.auriga-desktop-summary-title{font-size:20px;font-weight:600;line-height:1.2;color:var(--dsw-alias-label-primary,inherit)}
.auriga-desktop-summary-version{font-size:13px;line-height:1.3;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-desktop-summary-action{flex:none;display:flex;align-items:center}
.auriga-desktop-summary-body{margin-top:16px;display:flex;flex-direction:column;gap:10px;min-width:0}
.auriga-desktop-summary-body .auriga-setting-status{margin:0}
.auriga-desktop-progress-meta{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}
.auriga-setting-progress{display:grid;gap:8px;margin:0;font-size:12px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-setting-progress progress{width:100%;height:6px}
.auriga-setting{padding:14px 0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor);display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.auriga-setting>span:first-child{flex:1}.auriga-setting-value{color:var(--dsw-alias-label-secondary,inherit);overflow-wrap:anywhere}
.auriga-settings-content{width:100%}
.auriga-status-card{border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:10px;padding:16px;display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.auriga-status-card p:first-child{font-size:14px;font-weight:600;flex:1;min-width:220px;color:inherit}.auriga-status-card p{margin:0}.auriga-status-card p+ p{flex-basis:100%;order:2}
.auriga-consent{padding:16px 0;min-width:0;overflow:auto}
.auriga-consent.auriga-consent-settings{padding:0}
.auriga-consent article{padding:16px 0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-consent pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;max-height:12em;overflow:auto;unicode-bidi:plaintext}
.auriga-consent select{font:inherit;max-width:100%;padding:6px 9px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:6px;background:var(--dsw-alias-bg-layer-1,transparent);color:var(--dsw-alias-label-primary,inherit)}
.auriga-consent select:focus-visible{outline:2px solid var(--dsw-alias-label-secondary,currentColor)}
.auriga-consent-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.auriga-consent p{overflow-wrap:anywhere}.auriga-consent small{color:var(--dsw-alias-label-secondary,inherit)}
.auriga-consent-list>summary{list-style:none}
.auriga-consent-list>summary::-webkit-details-marker{display:none}
.auriga-app{height:100%;display:flex;flex-direction:row;gap:0;padding:0;overflow:hidden;min-height:0}
.auriga-activity-bar{box-sizing:border-box;flex:none;width:64px;height:100%;min-height:0;display:flex;flex-direction:column;padding:8px 6px;border-inline-end:1px solid var(--dsw-alias-border-l1,currentColor);background:var(--dsw-alias-bg-layer-1,transparent);transition:width 180ms ease;overflow:hidden}
.auriga-app[data-nav-expanded=true] .auriga-activity-bar{width:176px}
.auriga-activity-bar-top{flex:1;min-height:0;overflow-x:hidden;overflow-y:auto;display:flex;flex-direction:column;gap:4px}
.auriga-activity-bar-bottom{flex:none;display:flex;flex-direction:column;gap:4px;margin-top:8px;padding-top:8px;border-top:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-nav-toggle,.auriga-nav-item{box-sizing:border-box;display:flex;align-items:center;justify-content:center;width:100%;margin:0;padding:0;border:0;border-radius:8px;background:transparent;color:var(--dsw-alias-label-secondary,inherit);font:inherit;cursor:pointer;transition:background-color 150ms ease,color 150ms ease}
.auriga-nav-toggle{flex:none;height:36px;margin-bottom:4px;gap:8px}
.auriga-app[data-nav-expanded=true] .auriga-nav-toggle{justify-content:flex-start;padding:0 10px}
.auriga-nav-item{position:relative;flex-direction:column;gap:4px;min-height:52px;padding:8px 4px;text-align:center}
.auriga-app[data-nav-expanded=true] .auriga-nav-item{flex-direction:row;justify-content:flex-start;gap:10px;min-height:36px;height:36px;padding:0 10px;text-align:start}
.auriga-nav-item:hover,.auriga-nav-toggle:hover{background:var(--dsw-alias-interactive-bg-hover,transparent);color:var(--dsw-alias-label-primary,inherit)}
.auriga-nav-item[aria-current=page]{background:var(--dsw-alias-bg-layer-2,transparent);color:var(--dsw-alias-state-business-primary,var(--dsw-alias-label-primary,inherit))}
.auriga-nav-item[aria-current=page] .auriga-nav-label{color:inherit;font-weight:600}
.auriga-nav-item:disabled{opacity:.45;cursor:default}
.auriga-nav-icon{display:inline-flex;flex:none;width:20px;height:20px;align-items:center;justify-content:center}
.auriga-nav-label{display:block;min-width:0;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:11px;line-height:1.2;color:inherit}
.auriga-app[data-nav-expanded=true] .auriga-nav-label{font-size:13px;line-height:1.3}
.auriga-nav-badge{position:absolute;top:4px;inset-inline-end:4px;min-width:16px;height:16px;padding:0 4px;border-radius:8px;background:var(--dsw-alias-state-business-primary,currentColor);color:var(--dsw-alias-bg-layer-1,Canvas);font-size:10px;line-height:16px;font-weight:600}
.auriga-app[data-nav-expanded=true] .auriga-nav-badge{position:static;margin-inline-start:auto}
.auriga-main{flex:1;min-width:0;min-height:0;display:flex;flex-direction:column;overflow:hidden}
.auriga-main-body{flex:1;min-height:0;min-width:0;display:flex;flex-direction:column;overflow:hidden;padding:20px 24px 0}
.auriga-banner{display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:8px 12px;border-radius:8px;background:var(--dsw-alias-bg-layer-2,transparent);font-size:13px;flex:none;margin:12px 24px 0}
.auriga-main>.auriga-banner{margin-inline:24px}
.auriga-tabs-scroll{box-sizing:border-box;flex:none;min-width:0;width:100%;overflow-x:auto}
.auriga-tabs{box-sizing:border-box;min-width:max-content;width:100%;white-space:nowrap}
.auriga-tabs button{flex:none;white-space:nowrap}
.auriga-tabs-fallback{display:flex;gap:4px;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-tabs-fallback>button{font:inherit;cursor:pointer;border:0;border-bottom:2px solid transparent;border-radius:0;background:transparent;padding:8px 12px;color:var(--dsw-alias-label-secondary,inherit);white-space:nowrap}
.auriga-tabs-fallback>button[aria-selected=true]{color:var(--dsw-alias-label-primary,inherit);border-bottom-color:var(--dsw-alias-state-business-primary,currentColor);font-weight:600}
.auriga-tabpanel{min-width:0;min-height:0;flex:1;overflow:auto}.auriga-tabpanel[hidden],.auriga-tabpanel>auriga-team-panel[hidden]{display:none!important}
.auriga-tabpanel:has(>auriga-team-panel):not([hidden]){display:flex;flex-direction:column;overflow:hidden}
.auriga-tabpanel>auriga-team-panel{flex:1;min-height:0}
.auriga-overview{box-sizing:border-box;height:100%;min-height:0;overflow:auto;padding:24px 28px 32px}
.auriga-overview-inner{box-sizing:border-box;width:100%;max-width:1200px;margin:0;display:flex;flex-direction:column;gap:24px}
.auriga-overview-welcome{display:flex;align-items:center;justify-content:space-between;gap:16px;min-width:0}
.auriga-overview-welcome-copy{min-width:0;flex:1}
.auriga-overview-welcome-title{margin:0;font-size:28px;font-weight:650;letter-spacing:-.02em;line-height:1.25;color:var(--dsw-alias-label-primary,inherit)}
.auriga-overview-welcome-tagline{margin:8px 0 0;font-size:14px;line-height:1.5;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-overview-welcome-avatar{flex:none;width:48px;height:48px;border-radius:50%;display:inline-flex;align-items:center;justify-content:center;background:var(--dsw-alias-bg-layer-2,transparent);color:var(--dsw-alias-label-primary,inherit);font-size:18px;font-weight:600}
.auriga-overview-status{display:flex;flex-direction:column;gap:12px;min-width:0}
.auriga-overview-status-row{display:flex;flex-wrap:wrap;align-items:center;gap:8px 20px;min-width:0}
.auriga-overview-status-item{display:inline-flex;align-items:center;gap:8px;min-width:0}
.auriga-overview-status-dot{flex:none;width:7px;height:7px;border-radius:50%;background:var(--dsw-alias-label-tertiary,currentColor)}
.auriga-overview-status-dot.is-success{background:var(--dsw-alias-state-success-primary,#5f8f6a)}
.auriga-overview-status-dot.is-processing,.auriga-overview-status-dot.is-warn{background:var(--dsw-alias-state-warn-primary,currentColor)}
.auriga-overview-status-dot.is-error{background:var(--dsw-alias-state-error-primary,currentColor)}
.auriga-overview-status-dot.is-idle{background:var(--dsw-alias-label-tertiary,currentColor)}
.auriga-overview-status-copy{min-width:0;display:flex;flex-wrap:wrap;align-items:baseline;gap:4px 8px}
.auriga-overview-status-label{font-size:13px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-overview-status-detail{font-size:13px;color:var(--dsw-alias-label-primary,inherit)}
.auriga-overview-status-alert{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px;padding:12px 14px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:10px;background:var(--dsw-alias-bg-layer-2,transparent)}
.auriga-overview-status-alert p{margin:0;font-size:13px;line-height:1.5;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-overview-status-actions{display:flex;flex-wrap:wrap;align-items:center;gap:8px}
.auriga-overview-notifications{display:flex;flex-direction:column;gap:8px}
.auriga-overview-notice{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 14px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:10px;background:var(--dsw-alias-bg-layer-2,transparent)}
.auriga-overview-notice-copy{min-width:0;display:flex;flex-wrap:wrap;align-items:baseline;gap:6px 10px;font-size:13px;line-height:1.45;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-overview-notice-copy strong{font-weight:600;color:var(--dsw-alias-label-primary,inherit)}
.auriga-overview-notice-action{flex:none;margin-inline-start:auto}
.auriga-overview-section-title{margin:0;font-size:16px;font-weight:600;line-height:1.3;color:var(--dsw-alias-label-primary,inherit)}
.auriga-overview-capabilities{display:flex;flex-direction:column;gap:14px;min-width:0}
.auriga-overview-capability-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,220px),1fr));gap:12px}
.auriga-overview-capability{box-sizing:border-box;display:flex;flex-direction:column;gap:10px;min-height:132px;padding:16px;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:12px;background:var(--dsw-alias-bg-layer-2,var(--dsw-alias-bg-layer-1,transparent));color:inherit;font:inherit;text-align:start;cursor:pointer}
.auriga-overview-capability:hover{border-color:var(--dsw-alias-border-l2,currentColor);background:var(--dsw-alias-interactive-bg-hover,var(--dsw-alias-bg-layer-2,transparent))}
.auriga-overview-capability-top{display:flex;align-items:flex-start;justify-content:space-between;gap:12px}
.auriga-overview-capability-icon{flex:none;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-overview-capability-arrow{flex:none;color:var(--dsw-alias-label-tertiary,var(--dsw-alias-label-secondary,inherit))}
.auriga-overview-capability-title{font-size:15px;font-weight:600;line-height:1.3;color:var(--dsw-alias-label-primary,inherit)}
.auriga-overview-capability-desc{margin:0;font-size:13px;line-height:1.45;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-overview-activity{display:flex;flex-direction:column;gap:12px;min-width:0}
.auriga-overview-activity-head{display:flex;align-items:center;justify-content:space-between;gap:12px}
.auriga-overview-more{border:0;background:transparent;color:var(--dsw-alias-state-business-primary,inherit);font:inherit;font-size:13px;cursor:pointer;padding:0}
.auriga-overview-activity-list{list-style:none;margin:0;padding:0;border:1px solid var(--dsw-alias-border-l1,currentColor);border-radius:12px;background:var(--dsw-alias-bg-layer-2,transparent);overflow:hidden}
.auriga-overview-activity-row{box-sizing:border-box;display:flex;align-items:center;gap:12px;width:100%;padding:12px 14px;border:0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor);background:transparent;color:inherit;font:inherit;text-align:start;cursor:pointer}
.auriga-overview-activity-list>li:last-child .auriga-overview-activity-row{border-bottom:0}
.auriga-overview-activity-row:hover{background:var(--dsw-alias-interactive-bg-hover,transparent)}
.auriga-overview-activity-icon{flex:none;width:32px;height:32px;border-radius:9px;display:inline-flex;align-items:center;justify-content:center;background:var(--dsw-alias-interactive-bg-hover,transparent);color:var(--dsw-alias-state-business-primary,inherit);font-size:12px;font-weight:600}
.auriga-overview-activity-copy{min-width:0;flex:1}
.auriga-overview-activity-title{font-size:14px;font-weight:500;color:var(--dsw-alias-label-primary,inherit);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.auriga-overview-activity-meta{margin-top:2px;font-size:12px;color:var(--dsw-alias-label-secondary,inherit);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.auriga-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,320px),1fr));gap:16px;width:100%;padding-bottom:24px}
.auriga-card{border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:12px;padding:18px 20px;display:flex;flex-direction:column;gap:10px;min-width:0;background:var(--dsw-alias-bg-layer-1,transparent)}
.auriga-card header{display:flex;align-items:center;justify-content:space-between;gap:8px}
.auriga-card h3{font-size:15px;font-weight:600;margin:0}
.auriga-card p{margin:0;font-size:13px;line-height:1.55;color:var(--dsw-alias-label-secondary,inherit);overflow-wrap:anywhere}
.auriga-card dl{display:grid;grid-template-columns:max-content 1fr;gap:6px 16px;margin:0;font-size:13px}
.auriga-card dt{color:var(--dsw-alias-label-tertiary,inherit)}.auriga-card dd{margin:0;overflow-wrap:anywhere}
.auriga-badge{font-size:12px;padding:2px 8px;border-radius:6px;background:var(--dsw-alias-bg-layer-2,transparent);color:var(--dsw-alias-label-secondary,inherit)}
.auriga-row{display:flex;align-items:center;gap:8px;font-size:14px}
.auriga-dot{display:inline-block;width:8px;height:8px;border-radius:50%;flex:none;background:var(--dsw-alias-label-tertiary,currentColor)}
.auriga-dot[data-tone=done]{background:var(--dsw-alias-state-success-primary,currentColor)}
.auriga-dot[data-tone=warning]{background:var(--dsw-alias-state-warn-primary,currentColor)}
.auriga-dot[data-tone=error]{background:var(--dsw-alias-state-error-primary,currentColor)}
.auriga-dot[data-tone=ongoing]{background:var(--dsw-alias-state-business-primary,currentColor)}
.auriga-actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:4px}
.auriga-shell button.auriga-primary{background:var(--dsw-alias-button-primary-fill,var(--dsw-alias-state-business-primary,transparent));border-color:transparent;color:var(--dsw-alias-label-primary-foreground,inherit)}
.auriga-scan{display:flex;gap:20px;align-items:flex-start;flex-wrap:wrap}
.auriga-scan-side{flex:1;min-width:200px;display:flex;flex-direction:column;gap:8px}
.auriga-lead{color:var(--dsw-alias-label-primary,inherit)!important;font-size:14px!important;font-weight:600}
.auriga-qr{position:relative;width:168px;height:168px;margin:0;flex:none;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:8px;overflow:hidden}
.auriga-qr svg{display:block}
.auriga-qr-empty{display:flex;align-items:center;justify-content:center;background:var(--dsw-alias-bg-layer-2,transparent);text-align:center;font-size:12px;padding:12px;box-sizing:border-box;color:var(--dsw-alias-label-tertiary,inherit)}
.auriga-qr-overlay{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;padding:12px;text-align:center;font-weight:600;background:var(--dsw-alias-bg-layer-1,transparent);opacity:.92;color:var(--dsw-alias-label-primary,inherit)}
.auriga-warn{color:var(--dsw-alias-state-warn-label,var(--dsw-alias-state-warn-primary,inherit))!important}
.auriga-muted{color:var(--dsw-alias-label-tertiary,inherit)!important}
.auriga-ref{font-size:11px;color:var(--dsw-alias-label-tertiary,inherit)}
.auriga-blocked{max-width:560px;margin:32px 0;display:flex;flex-direction:column;gap:10px}
.auriga-blocked p{margin:0;color:var(--dsw-alias-label-secondary,inherit);font-size:13px}
.auriga-blocked button{align-self:flex-start}
.auriga-brand-name{display:inline-flex;align-items:baseline;gap:5px;font-size:16px;font-weight:700;letter-spacing:.02em;color:var(--dsw-alias-label-primary,inherit);white-space:nowrap}.auriga-brand-suffix{font-size:11px;font-weight:600;letter-spacing:.12em;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-launcher-root{flex:1;min-width:0;position:relative}.auriga-launcher-anchor{width:100%}.auriga-launcher-trigger{user-select:none;display:flex;align-items:center;width:100%;height:44px;gap:8px;padding:6px;border:0;border-radius:var(--dsw-radius-md,8px);background:transparent;color:var(--dsw-alias-label-primary,inherit);font:inherit;font-size:14px;cursor:pointer;text-align:left}.auriga-launcher-trigger[data-collapsed="true"]{box-sizing:border-box;width:36px;height:36px;justify-content:center;gap:0;padding:0}.auriga-launcher-trigger:hover{background:var(--dsw-alias-interactive-bg-hover,rgba(128,128,128,.12))}.auriga-launcher-trigger[data-signed-out="true"]:not([data-collapsed="true"]){height:36px;padding:6px}.auriga-launcher-avatar{display:inline-flex;align-items:center;justify-content:center;flex:none;width:24px;height:24px;border-radius:50%;background:var(--dsw-alias-brand-primary,#1f6feb);color:#fff;font-size:12px;font-weight:600;line-height:1;overflow:hidden}.auriga-launcher-avatar[data-signed-out="true"]{background:var(--dsw-alias-bg-skeleton,rgba(128,128,128,.18));color:var(--dsw-alias-label-secondary,inherit)}.auriga-launcher-text{display:flex;flex-direction:row;align-items:baseline;gap:8px;min-width:0;flex:1;line-height:1.25}.auriga-launcher-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:13px;font-weight:500}.auriga-launcher-sub{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:none;font-size:12px;font-weight:400;color:var(--dsw-alias-label-primary,inherit);opacity:.82}.auriga-launcher-fallback-menu{position:absolute;left:0;bottom:calc(100% + 6px);min-width:168px;padding:4px;border-radius:var(--dsw-radius-md,8px);border:1px solid var(--dsw-alias-border-primary,rgba(128,128,128,.22));background:var(--dsw-alias-bg-elevated,#fff);box-shadow:0 8px 24px rgba(0,0,0,.16);z-index:100;display:flex;flex-direction:column;gap:2px}.auriga-launcher-fallback-item{display:flex;align-items:center;gap:8px;width:100%;padding:8px 10px;border:0;border-radius:6px;background:transparent;color:var(--dsw-alias-label-primary,inherit);font:inherit;font-size:13px;cursor:pointer;text-align:left}.auriga-launcher-fallback-item:hover{background:var(--dsw-alias-interactive-bg-hover,rgba(128,128,128,.12))}
.auriga-tools{margin:0;padding-left:18px;font-size:13px;display:grid;gap:4px}
.auriga-app>.auriga-settings,.auriga-tabpanel .auriga-settings{padding:0;height:100%;min-height:0}
.auriga-tabpanel:has(>.auriga-settings):not([hidden]){overflow:hidden;display:flex;flex-direction:column}
.auriga-tabpanel>.auriga-settings{flex:1;min-height:0}
.auriga-tabpanel:has(>.auriga-overview):not([hidden]){overflow:hidden;display:flex;flex-direction:column;padding:0}
.auriga-tabpanel>.auriga-overview{flex:1;min-height:0}

.auriga-platform-login-dialog{width:min(544px,calc(100vw - 32px));max-width:calc(100vw - 32px)}
.auriga-login-content{display:flex;flex-direction:column;gap:20px;min-width:0}
.auriga-login-intro{margin:0;color:var(--dsw-alias-label-secondary,inherit);font-size:13px;line-height:1.6}
.auriga-login-content .auriga-login-card{margin:0;padding:0;border:0;background:transparent;box-shadow:none}
.auriga-login-card>.auriga-row{font-size:13px;margin-bottom:16px;color:var(--dsw-alias-label-secondary,inherit)}
.auriga-login-card .auriga-scan{display:flex;flex-direction:column;align-items:stretch;gap:24px}
.auriga-login-methods{display:flex;gap:4px;width:100%;padding:4px;box-sizing:border-box;border-radius:8px;background:var(--dsw-alias-bg-layer-2,transparent)}
.auriga-login-methods>button{flex:1;justify-content:center;min-height:36px;border:0}
.auriga-login-methods>button[aria-selected=true]{background:var(--dsw-alias-bg-layer-1,transparent);color:var(--dsw-alias-label-primary,inherit)}
.auriga-login-card .auriga-qr{align-self:center;width:208px;height:auto;margin:0;border:0;border-radius:0;overflow:visible;background:transparent}
.auriga-login-card .auriga-qr svg{display:block;border-radius:8px}
.auriga-login-card .auriga-qr figcaption{position:static;margin-top:14px;padding:0;text-align:center;background:transparent;color:var(--dsw-alias-label-secondary,inherit);font-size:13px}
.auriga-login-card .auriga-scan form{width:100%;box-sizing:border-box}
.auriga-login-card>.auriga-actions{justify-content:flex-end;margin-top:20px}
.auriga-login-footer{display:flex;justify-content:flex-end;padding-top:16px;border-top:1px solid var(--dsw-alias-border-l1,currentColor)}
`;
    function failure(reason = 'unavailable') {
      if (
        ![
          'auth',
          'forbidden',
          'missing',
          'unsupported',
          'verification',
          'unavailable',
        ].includes(reason)
      )
        reason = 'unavailable';
      return Object.assign(new Error('Auriga DSH surface unavailable'), {
        reason,
      });
    }
    // Only local categories reach UI state. Server detail/code can contain private data.
    function responseFailure(response, body) {
      if (response.status === 401) return failure('auth');
      if (response.status === 403) return failure('forbidden');
      if (response.status === 404) return failure('missing');
      if (
        response.status === 426 ||
        body?.code === 'plugin.protocol_unsupported'
      )
        return failure('unsupported');
      if (
        [
          'common.unauthenticated',
          'common.validation_failed',
          'common.precondition_failed',
        ].includes(body?.code)
      )
        return failure('verification');
      return failure();
    }
    function localURL(path) {
      const base = new URL(document.baseURI);
      const url = new URL(path, document.baseURI);
      if (
        url.origin !== base.origin ||
        url.protocol !== base.protocol ||
        url.host !== base.host ||
        url.username ||
        url.password
      )
        throw failure('verification');
      return url;
    }
    function bootstrapView(body) {
      if (!['managed', 'standalone', 'connect'].includes(body?.mode))
        throw failure('missing');
      if (body.protocol !== 1) throw failure('unsupported');
      const manifest = body.manifest;
      const version = (value) =>
        typeof value === 'string' &&
        /^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$/.test(value);
      if (body.mode !== 'managed') {
        const panel = body.localPanel;
        if (
          !['unconfigured', 'connected', 'disconnected'].includes(
            body.status,
          ) ||
          (body.status !== 'unconfigured' &&
            typeof body.connection?.connected !== 'boolean') ||
          panel?.source !== 'package' ||
          panel.entry !== 'auriga/local-panel.js' ||
          !version(body.pluginVersion) ||
          !version(panel.version) ||
          !/^sha384-[A-Za-z0-9+/=]+$/.test(panel.integrity)
        )
          throw failure('verification');
        return {
          mode: body.mode,
          protocol: body.protocol,
          pluginVersion: body.pluginVersion,
          panelVersion: panel.version,
          source: 'package',
          entry: localURL(panel.entry).href,
          integrity: panel.integrity,
          connection: body.connection,
          memberResourcesAvailable: body.memberResourcesAvailable === true,
          fetch:
            body.mode === 'connect'
              ? makeFetch({ fetchBase: localURL('auriga/resources/').href })
              : undefined,
        };
      }
      if (
        !version(body.pluginVersion) ||
        !version(manifest?.version) ||
        typeof body.panelBase !== 'string' ||
        typeof body.fetchBase !== 'string' ||
        !/^[A-Za-z0-9._/-]+$/.test(manifest?.entry) ||
        manifest.entry
          .split('/')
          .some((part) => !part || part === '.' || part === '..')
      )
        throw failure('verification');
      const integrity = manifest.integrity?.[manifest.entry];
      if (
        typeof integrity !== 'string' ||
        !/^sha(256|384|512)-[A-Za-z0-9+/=]+$/.test(integrity)
      )
        throw failure('verification');
      const view = {
        mode: body.mode,
        protocol: body.protocol,
        pluginVersion: body.pluginVersion,
        panelVersion: manifest.version,
        entry: localURL(body.panelBase + manifest.entry).href,
        integrity,
        fetchBase: localURL(body.fetchBase).href,
        source: 'signed',
        resourceDistributionAvailable:
          body.resourceDistributionAvailable === true,
      };
      if (body.resourceIdentity !== undefined) {
        const identity = body.resourceIdentity;
        const uuid =
          /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
        if (
          !identity ||
          ![identity.instanceId, identity.tenantId, identity.ownerId].every(
            (id) => typeof id === 'string' && uuid.test(id),
          )
        )
          throw failure('verification');
        view.resourceIdentity = {
          instanceId: identity.instanceId,
          tenantId: identity.tenantId,
          ownerId: identity.ownerId,
        };
      }
      view.fetch = makeFetch(view);
      return view;
    }
    function makeFetch(boot) {
      const base = new URL(boot.fetchBase);
      return (path, init) => {
        if (
          typeof path !== 'string' ||
          /^([a-z][a-z0-9+.-]*:|\/\/)/i.test(path)
        )
          return Promise.reject(
            new TypeError('context.fetch only accepts platform API paths'),
          );
        const url = new URL(path.replace(/^\/+/, ''), base);
        if (
          url.origin !== base.origin ||
          url.protocol !== base.protocol ||
          url.host !== base.host
        )
          return Promise.reject(
            new TypeError('context.fetch only accepts same-origin paths'),
          );
        return fetch(url, {
          ...init,
          credentials: boot.mode === 'managed' ? 'omit' : 'same-origin',
          redirect: 'manual',
        });
      };
    }
    // Uses only the public rc.3 Connection observables. Entry recovery never
    // replaces the native transport or replays a lifecycle/API write.
    function installEntryRecovery(ctx) {
      const connection = ctx.connection;
      if (
        !['state', 'generation'].every(
          (name) =>
            typeof connection?.[name]?.getSnapshot === 'function' &&
            typeof connection?.[name]?.subscribe === 'function',
        )
      ) {
        console.warn(
          'Auriga DSH entry recovery unavailable: Connection observables missing',
        );
        return () => {};
      }
      let base;
      try {
        base = new URL(document.baseURI);
      } catch {
        return () => {};
      }
      if (
        window.top !== window ||
        !['http:', 'https:'].includes(base.protocol) ||
        base.origin !== window.location.origin ||
        base.username ||
        base.password ||
        base.search ||
        base.hash ||
        !(
          /^\/i\/[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\/$/.test(
            base.pathname,
          ) || base.pathname === '/'
        ) ||
        !window.location.pathname.startsWith(base.pathname)
      ) {
        console.info(
          'auriga.entry_recovery ' +
            JSON.stringify({
              event: 'scope_skipped',
              reason: 'unsupported_location',
            }),
        );
        return () => {};
      }
      const key = 'auriga.entry-recovery:' + base.pathname;
      let closed = false,
        navigated = false,
        epoch = 0,
        attempts = 0,
        settled = false;
      let generation = connection.generation.getSnapshot();
      // Timers are cancellable resources; deadlines are facts about this loss
      // cycle and must survive document visibility and browser wake events.
      let request, retryTimer, cycleTimer, deadline, lossStartedAt;
      let availability = false,
        qualifiedShortReads = 0;
      let nextProbeAt = 0;
      const now = () => window.performance.now();
      const trace = (event, reason, status) => {
        try {
          const state = connection.state.getSnapshot();
          console.info(
            'auriga.entry_recovery ' +
              JSON.stringify({
                event,
                reason,
                attempt: attempts,
                phase: availability ? 'availability' : 'short',
                state: ['connected', 'connecting', 'disconnected'].includes(
                  state,
                )
                  ? state
                  : 'initial',
                elapsedMs:
                  lossStartedAt === undefined
                    ? 0
                    : Math.max(0, Math.round(now() - lossStartedAt)),
                ...(Number.isInteger(status) && status >= 100 && status < 600
                  ? { status }
                  : {}),
              }),
          );
        } catch {
          /* diagnostics cannot change recovery or authorization */
        }
      };
      const cleanups = [];
      const cancel = () => {
        epoch++;
        if (request)
          nextProbeAt = Math.max(
            nextProbeAt,
            now() + (availability ? 10000 : 2000),
          );
        request?.abort();
        request = undefined;
        clearTimeout(retryTimer);
        retryTimer = undefined;
        clearTimeout(cycleTimer);
        cycleTimer = undefined;
      };
      const blocked = () => {
        try {
          return window.sessionStorage.getItem(key) !== null;
        } catch {
          return true;
        } // no automatic navigation without the loop fence
      };
      const pending = () =>
        ['connecting', 'disconnected'].includes(
          connection.state.getSnapshot(),
        ) && connection.generation.getSnapshot() === undefined;
      const current = (version) =>
        !closed &&
        !navigated &&
        epoch === version &&
        pending() &&
        deadline !== undefined &&
        now() < deadline &&
        !document.hidden;
      const scheduleProbe = (delay) => {
        const version = epoch;
        const timer = setTimeout(() => {
          if (closed || epoch !== version || retryTimer !== timer) return;
          retryTimer = undefined;
          void probe();
        }, delay);
        retryTimer = timer;
      };
      async function probe() {
        if (
          closed ||
          navigated ||
          request ||
          retryTimer ||
          settled ||
          attempts >= (availability ? 6 : 3) ||
          blocked() ||
          !pending() ||
          document.hidden
        )
          return;
        if (deadline === undefined || now() >= deadline) {
          trace('cycle_stopped', 'deadline');
          settled = true;
          cancel();
          return;
        }
        if (now() < nextProbeAt) {
          scheduleProbe(nextProbeAt - now());
          return;
        }
        const version = epoch,
          readDeadline = Math.min(deadline, now() + 6000),
          controller = new AbortController();
        if (cycleTimer === undefined) {
          const until = deadline;
          cycleTimer = setTimeout(() => {
            if (closed || epoch !== version || deadline !== until) return;
            trace('cycle_stopped', 'deadline');
            settled = true;
            cancel();
          }, deadline - now());
        }
        request = controller;
        attempts++;
        trace('probe_started', 'read_only');
        let abort;
        const cancelled = new Promise((_, reject) => {
          abort = () => reject(new Error('entry probe cancelled'));
          controller.signal.addEventListener('abort', abort, { once: true });
        });
        const readTimer = setTimeout(() => {
          if (request !== controller) return;
          trace('probe_timed_out', 'read_deadline');
          controller.abort();
        }, readDeadline - now());
        let outcome = 'stop';
        let status;
        try {
          outcome = await Promise.race([
            (async () => {
              // The existing exact health route bypasses interaction activity;
              // probing the native document would incorrectly touch idle time.
              const response = await fetch(
                new URL('_auriga/health', base).href,
                {
                  method: 'GET',
                  credentials: 'same-origin',
                  redirect: 'error',
                  cache: 'no-store',
                  headers: { Accept: 'application/problem+json' },
                  signal: controller.signal,
                },
              );
              status = response.status;
              if (
                [502, 503].includes(response.status) ||
                (!availability && response.status === 504)
              )
                return 'retry';
              if (
                response.status !== 401 ||
                response.headers.get('X-Auriga-Instance-Entry') !==
                  'required' ||
                !response.headers
                  .get('Content-Type')
                  ?.startsWith('application/problem+json')
              )
                return 'stop';
              const problem = await response.json().catch(() => null);
              return problem?.code === 'edge.unauthorized' &&
                problem.status === 401 &&
                problem.detail === 'instance cookie required'
                ? 'recover'
                : 'stop';
            })(),
            cancelled,
          ]);
        } catch (error) {
          outcome =
            controller.signal.aborted || error?.name === 'AbortError'
              ? 'stop'
              : 'retry';
        } finally {
          clearTimeout(readTimer);
          // Browser timers may run after a response continuation. The deadline
          // remains authoritative for HTTP, body and network-error completion.
          if (now() >= readDeadline) {
            outcome = 'stop';
            if (!controller.signal.aborted) {
              trace('probe_timed_out', 'read_deadline');
              controller.abort();
            }
          }
          controller.signal.removeEventListener('abort', abort);
          if (request === controller) request = undefined;
        }
        trace('probe_completed', outcome, status);
        if (!current(version)) return;
        if (outcome === 'recover' && !controller.signal.aborted && !blocked()) {
          navigated = true;
          trace('navigation', 'edge_entry_required');
          cancel();
          // The top-level GET receives edge's fixed normal-open recovery page.
          // That page owns the tab marker and trusted cross-origin portal hop.
          window.location.replace(base.href);
        } else if (outcome === 'retry') {
          if (
            !availability &&
            !controller.signal.aborted &&
            (status === undefined || [502, 503].includes(status))
          )
            qualifiedShortReads++;
          // A missing edge is not an authentication verdict. Only three
          // completed availability failures admit the three low-frequency reads.
          // Their absolute cap remains anchored to the original connection loss.
          if (!availability && attempts === 3 && qualifiedShortReads === 3) {
            availability = true;
            trace('availability_wait', 'edge_unavailable', status);
          }
          if (attempts < (availability ? 6 : 3)) {
            const delay = availability ? 10000 : 2000;
            nextProbeAt = now() + delay;
            scheduleProbe(delay);
          } else {
            trace('cycle_stopped', 'attempt_limit', status);
            settled = true;
            clearTimeout(cycleTimer);
            cycleTimer = undefined;
          }
        } else {
          trace('cycle_stopped', 'non_entry_reply', status);
          settled = true;
          clearTimeout(cycleTimer);
          cycleTimer = undefined;
        }
      }
      const changed = (trigger = 'connection') => {
        if (closed) return;
        trace('trigger', trigger);
        const next = connection.generation.getSnapshot();
        if (next !== generation) {
          cancel();
          generation = next;
          attempts = 0;
          settled = false;
          deadline = undefined;
          lossStartedAt = undefined;
          availability = false;
          qualifiedShortReads = 0;
          nextProbeAt = 0;
        }
        if (connection.state.getSnapshot() === 'connected' && next) {
          cancel();
          attempts = 0;
          settled = false;
          deadline = undefined;
          lossStartedAt = undefined;
          availability = false;
          qualifiedShortReads = 0;
          nextProbeAt = 0;
          try {
            window.sessionStorage.removeItem(key);
          } catch {
            /* retain fail-closed loop fence */
          }
        } else if (!pending()) cancel();
        else {
          lossStartedAt ??= now();
          deadline ??= lossStartedAt + 60000;
          if (document.hidden) cancel();
          else void probe();
        }
      };
      const resume = (trigger) => {
        if (!closed && !request) {
          changed(trigger);
        }
      };
      const stop = () => {
        if (closed) return;
        closed = true;
        trace('disposed', 'lifecycle');
        cancel();
        cleanups
          .splice(0)
          .reverse()
          .forEach((cleanup) => cleanup());
      };
      cleanups.push(
        connection.state.subscribe(() => changed('state')),
        connection.generation.subscribe(() => changed('generation')),
      );
      for (const event of ['focus', 'online']) {
        const wake = () => resume(event);
        window.addEventListener(event, wake);
        cleanups.push(() => window.removeEventListener(event, wake));
      }
      const visibility = () => changed('visibility');
      document.addEventListener('visibilitychange', visibility);
      cleanups.push(() =>
        document.removeEventListener('visibilitychange', visibility),
      );
      window.addEventListener('pagehide', stop);
      cleanups.push(() => window.removeEventListener('pagehide', stop));
      trace('installed', 'public_connection');
      changed('initial');
      return stop;
    }
    return {
      inject: ['slots', 'locale', 'theme'],
      apply(ctx) {
        globalThis.__AURIGA_SHARED_BROWSER__?.installMessaging(
          ctx,
          React,
          primitives,
        );
        const listInstalled = async () => {
          try {
            const result = await ctx.get?.('remote')?.pluginInventory?.list();
            if (result?.ok === true) {
              try {
                const bundles = await ctx
                  .get?.('remote')
                  ?.pluginManager?.listBundles?.();
                if (bundles?.ok === true && Array.isArray(bundles.value))
                  return {
                    ...result.value,
                    bundles: bundles.value.map((row) => ({
                      name: row.name,
                      version: row.version,
                      rows: row.rows?.map((entry) => ({
                        moduleName: entry.moduleName,
                      })),
                    })),
                  };
              } catch {
                /* Public inventory remains usable when management metadata is unavailable. */
              }
              return result.value;
            }
          } catch {
            /* Host errors must not cross the panel boundary. */
          }
          throw failure();
        };
        const memberRequest = async (action, input, signal) => {
          let csrf;
          if (input !== undefined) {
            const status = await fetch(localURL('auriga/shell/status'), {
              signal,
              credentials: 'same-origin',
              redirect: 'error',
              cache: 'no-store',
            });
            if (!status.ok) throw failure('auth');
            const state = await status.json();
            if (!state.loggedIn || !state.csrf) throw failure('auth');
            csrf = state.csrf;
          }
          const response = await fetch(
            localURL('auriga/resources/local/' + action),
            {
              signal,
              credentials: 'same-origin',
              redirect: 'error',
              cache: 'no-store',
              ...(input === undefined
                ? {}
                : {
                    method: 'POST',
                    headers: {
                      'content-type': 'application/json',
                      'X-Auriga-CSRF': csrf,
                    },
                    body: JSON.stringify(input),
                  }),
            },
          );
          const body = await response.json().catch(() => ({}));
          if (!response.ok) {
            const code = body.code;
            const reason = [
              'resource.signature_invalid',
              'resource.digest_mismatch',
            ].includes(code)
              ? 'verification'
              : code === 'resource.storage_unavailable'
                ? 'setup'
                : ['common.conflict', 'common.precondition_failed'].includes(
                      code,
                    )
                  ? 'conflict'
                  : response.status === 401
                    ? 'auth'
                    : response.status === 403
                      ? 'forbidden'
                      : 'unavailable';
            throw Object.assign(failure(reason), {
              reason,
              resourceReason: body.reason,
              trace: /^[a-f0-9]{32}$/.test(body.trace_id)
                ? body.trace_id
                : undefined,
            });
          }
          return body;
        };
        const memberResources = {
          list: (signal) => memberRequest('installations', undefined, signal),
          apply: (input, signal) => memberRequest('actions', input, signal),
        };
        let disposed = false;
        const cleanups = [];
        const own = (cleanup) => {
          if (typeof cleanup !== 'function') return undefined;
          let active = true;
          const once = () => {
            if (active) {
              active = false;
              cleanup();
            }
          };
          cleanups.push(once);
          return once;
        };
        // Both Cordis disposal and an explicit host disposal are idempotent.
        const effect = (install, label) => {
          const cleanup = own(install());
          ctx.effect?.(() => cleanup, label);
        };
        effect(
          () =>
            ctx.inject?.(['connection'], (scope) => {
              scope.effect(
                () => installEntryRecovery(scope),
                'auriga-shell: entry recovery',
              );
            }),
          'auriga-shell: entry connection',
        );
        effect(
          () => ctx.locale.register(NS, { zh, en }),
          'auriga-shell: dictionaries',
        );
        const t = ctx.locale.bind(NS);
        const style = document.createElement('style');
        style.textContent = css;
        document.head.append(style);
        own(() => style.remove());
        const hostScheme = (snapshot) =>
          ['dark', 'light'].includes(snapshot?.active?.colorScheme)
            ? snapshot.active.colorScheme
            : undefined;
        const listeners = new Set();
        const state = {
          locale: ctx.locale.getLocale().active,
          theme: hostScheme(ctx.theme?.getTheme()),
          toast: undefined,
        };
        const emit = () => {
          if (!disposed) listeners.forEach((fn) => fn());
        };
        effect(
          () =>
            ctx.on('locale/change', (snapshot) => {
              state.locale = snapshot.active;
              emit();
            }),
          'auriga-shell: locale',
        );
        effect(
          () =>
            ctx.on('theme/change', (snapshot) => {
              state.theme = hostScheme(snapshot);
              emit();
            }),
          'auriga-shell: theme',
        );
        const controllers = new Set();
        const timers = new Set();
        const scripts = new Set();
        const later = (fn, delay) => {
          const timer = setTimeout(() => {
            timers.delete(timer);
            fn();
          }, delay);
          timers.add(timer);
          return timer;
        };
        const clearTimer = (timer) => {
          clearTimeout(timer);
          timers.delete(timer);
        };
        const requestBootstrap = async (signal) => {
          const controller = new AbortController();
          controllers.add(controller);
          const abort = () => controller.abort();
          signal.addEventListener('abort', abort, { once: true });
          const timer = later(abort, 15_000);
          try {
            if (disposed || signal.aborted) throw failure();
            // An abort race also settles non-cooperative host fetch implementations.
            return await Promise.race([
              (async () => {
                const response = await fetch(localURL('auriga/bootstrap'), {
                  credentials: 'same-origin',
                  cache: 'no-store',
                  redirect: 'error',
                  signal: controller.signal,
                });
                const body = await response.json().catch(() => ({}));
                if (!response.ok) throw responseFailure(response, body);
                return bootstrapView(body);
              })(),
              new Promise((_, reject) =>
                controller.signal.addEventListener(
                  'abort',
                  () => reject(failure()),
                  { once: true },
                ),
              ),
            ]);
          } catch (error) {
            throw failure(error?.reason);
          } finally {
            clearTimer(timer);
            signal.removeEventListener('abort', abort);
            controllers.delete(controller);
          }
        };
        let panelPending;
        const ensurePanel = (boot, signal) => {
          const identity = `${boot.source}:${boot.entry}:${boot.integrity}`;
          if (panelPending)
            return panelPending.identity === identity
              ? panelPending.promise
              : Promise.reject(failure('verification'));
          const existing = customElements.get(ELEMENT);
          if (existing)
            return loadedPanels.get(existing) === identity
              ? Promise.resolve()
              : Promise.reject(failure('verification'));
          // A fresh URL fragment isolates this attempt in the browser module map.
          // The network resource and its manifest SRI remain unchanged.
          const moduleURL = new URL(boot.entry);
          moduleURL.hash =
            'auriga-' +
            Array.from(window.crypto.getRandomValues(new Uint32Array(4)), (n) =>
              n.toString(16),
            ).join('-');
          const pending = { identity, promise: undefined };
          panelPending = pending;
          pending.promise = new Promise((resolve, reject) => {
            const el = document.createElement('script');
            el.type = 'module';
            el.src = moduleURL.href;
            el.integrity = boot.integrity;
            el.crossOrigin = 'anonymous';
            el.dataset.aurigaPanel = '';
            const finish = (error) => {
              if (!scripts.has(el)) return;
              scripts.delete(el);
              clearTimer(timer);
              el.onload = null;
              el.onerror = null;
              el.remove();
              signal.removeEventListener('abort', el.cancel);
              if (error) reject(error);
              else resolve();
            };
            const timer = later(() => finish(failure()), 10_000);
            scripts.add(el);
            el.onload = async () => {
              try {
                // onload proves this exact URL passed browser SRI. Import the same
                // cached module namespace, never a second unverified module URL.
                const module = await import(moduleURL.href);
                if (!scripts.has(el) || disposed || signal.aborted) return;
                const Constructor = module.AurigaTeamPanel;
                if (
                  typeof Constructor !== 'function' ||
                  !(Constructor.prototype instanceof window.HTMLElement) ||
                  customElements.get(ELEMENT) !== Constructor
                )
                  throw failure('verification');
                loadedPanels.set(Constructor, identity);
                finish();
              } catch {
                finish(failure('verification'));
              }
            };
            el.onerror = () => finish(failure());
            el.cancel = () => finish(failure());
            signal.addEventListener('abort', el.cancel, { once: true });
            document.head.append(el);
          }).finally(() => {
            if (panelPending === pending) panelPending = undefined;
          });
          return pending.promise;
        };

        const useHostState = () => {
          const [, bump] = React.useReducer((n) => n + 1, 0);
          React.useEffect(() => {
            listeners.add(bump);
            return () => listeners.delete(bump);
          }, []);
          return state;
        };
        // Every surface owns its request. Settings refresh never resets the mounted resource element.
        const useBootstrap = (panel = false) => {
          const [phase, setPhase] = React.useState({ status: 'loading' });
          const [attempt, setAttempt] = React.useState(0);
          React.useEffect(() => {
            let alive = true;
            let currentBoot;
            const controller = new AbortController();
            setPhase({ status: 'loading' });
            requestBootstrap(controller.signal)
              .then(async (boot) => {
                currentBoot = boot;
                if (panel && alive && !disposed) {
                  setPhase({ status: 'loading', boot });
                  await ensurePanel(boot, controller.signal);
                }
                if (alive && !disposed) setPhase({ status: 'ready', boot });
              })
              .catch((error) => {
                if (alive && !disposed)
                  setPhase({
                    status: 'error',
                    reason: error?.reason ?? 'unavailable',
                    boot: currentBoot,
                  });
              });
            return () => {
              alive = false;
              controller.abort();
            };
          }, [attempt]);
          return [phase, () => setAttempt((n) => n + 1)];
        };
        const notice = (phase, retry, refresh = false) =>
          h(
            'div',
            {
              className: 'auriga-shell-status',
              role: phase.status === 'error' ? 'alert' : 'status',
              'aria-live': 'polite',
            },
            h(
              'span',
              null,
              t(
                phase.status === 'loading'
                  ? 'loading'
                  : phase.status === 'ready'
                    ? phase.boot.source === 'package'
                      ? phase.boot.connection
                        ? phase.boot.connection.connected
                          ? 'connected'
                          : 'disconnected'
                        : 'unconfigured'
                      : 'ready'
                    : phase.reason,
              ),
            ),
            phase.status !== 'loading' &&
              h(
                'button',
                { type: 'button', onClick: retry },
                t(refresh && phase.status === 'ready' ? 'refresh' : 'retry'),
              ),
          );
        const consentRequest = async (action, input) => {
          const write = input !== undefined;
          let csrf;
          {
            const status = await fetch(localURL('auriga/shell/status'), {
              credentials: 'same-origin',
              cache: 'no-store',
              redirect: 'error',
            });
            if (!status.ok) throw failure();
            const state = await status.json();
            if (state.capabilities?.consent !== true) {
              if (!write) return null;
              throw failure('auth');
            }
            if (
              write &&
              (!state.loggedIn || (state.mode !== 'managed' && !state.csrf))
            )
              throw failure('auth');
            csrf = state.csrf;
          }
          const response = await fetch(
            localURL('auriga/shell/consent/' + action),
            {
              credentials: 'same-origin',
              cache: 'no-store',
              redirect: 'error',
              ...(write
                ? {
                    method: 'POST',
                    headers: {
                      'content-type': 'application/json',
                      ...(csrf ? { 'X-Auriga-CSRF': csrf } : {}),
                    },
                    body: JSON.stringify(input),
                  }
                : {}),
            },
          );
          const body = await response.json().catch(() => ({}));
          if (!response.ok)
            throw Object.assign(failure(), {
              reason: body.reason === 'cooldown' ? 'cooldown' : 'unavailable',
            });
          return body;
        };
        const consentBridge = {
          snapshot: () => consentRequest('snapshot'),
          decide: (input) => consentRequest('decide', input),
          setDefault: (layer, mode) =>
            consentRequest('default', { layer, mode }),
          revoke: (input) => consentRequest('revoke', input),
        };
        function ConsentSection({ settings = false }) {
          useHostState();
          const api = consentBridge;
          const readSnapshot = async () => {
            const snapshot = await api.snapshot();
            if (snapshot === null) return null;
            if (
              !snapshot ||
              !Array.isArray(snapshot.pending) ||
              !Array.isArray(snapshot.rules) ||
              !Number.isSafeInteger(snapshot.policyRevision) ||
              !snapshot.defaults ||
              !['conversation', 'execution'].every((layer) =>
                ['ask_each', 'same_org', 'allowlist', 'deny_all'].includes(
                  snapshot.defaults[layer],
                ),
              ) ||
              snapshot.organization?.available !== true ||
              typeof snapshot.organization.revision !== 'string' ||
              !['conversation', 'execution'].every((layer) =>
                ['allow', 'ask_each', 'deny_all'].includes(
                  snapshot.organization[layer],
                ),
              )
            )
              throw failure();
            return snapshot;
          };
          const [view, setView] = React.useState(null);
          const [status, setStatus] = React.useState('checking');
          const [revision, reload] = React.useReducer((n) => n + 1, 0);
          const [scopes, setScopes] = React.useState({});
          const [listQuery, setListQuery] = React.useState({
            allow_source: '',
            block: '',
          });
          const [openSelect, setOpenSelect] = React.useState('');
          const active = React.useRef(false);
          const busy = React.useRef(false);
          const generation = React.useRef(0);
          React.useEffect(() => {
            generation.current++;
            active.current = true;
            let current = true;
            setView(null);
            setStatus('checking');
            setOpenSelect('');
            if (api) {
              Promise.resolve()
                .then(() => readSnapshot())
                .then(
                  (snapshot) => {
                    if (current) {
                      setView(snapshot);
                      setStatus(snapshot === null ? 'unsupported' : 'ready');
                    }
                  },
                  () => {
                    if (current) setStatus('failed');
                  },
                );
            }
            return () => {
              current = false;
              active.current = false;
            };
          }, [api, revision]);
          React.useEffect(() => {
            if (!openSelect) return;
            const close = () => setOpenSelect('');
            const timer = setTimeout(() => {
              document.addEventListener('click', close);
            }, 0);
            return () => {
              clearTimeout(timer);
              document.removeEventListener('click', close);
            };
          }, [openSelect]);
          const change = async (operation) => {
            if (busy.current || !active.current) return;
            const attempt = generation.current;
            const current = () =>
              active.current && generation.current === attempt;
            busy.current = true;
            setStatus('loading');
            setView(null);
            try {
              await operation();
              if (!current()) return;
              const snapshot = await readSnapshot();
              if (current()) {
                setView(snapshot);
                setStatus(snapshot === null ? 'unsupported' : 'ready');
              }
            } catch (error) {
              // A rejected or uncertain write is never replayed automatically.
              if (current()) {
                setView(null);
                setStatus(error?.reason === 'cooldown' ? 'cooldown' : 'failed');
              }
            } finally {
              busy.current = false;
            }
          };
          const layerName = (layer) =>
            t(
              layer === 'conversation' ? 'conversationLayer' : 'executionLayer',
            );
          const sourceName = (source) =>
            `${source.realm}.${source.node} / ${source.userId}`;
          const memberPrimary = (source) => {
            for (const key of ['displayName', 'name', 'userId']) {
              const value = source?.[key];
              if (typeof value === 'string' && value.trim()) return value.trim();
            }
            return '';
          };
          const memberSecondary = (source) => {
            for (const key of ['empNo', 'employeeNo']) {
              const value = source?.[key];
              if (typeof value === 'string' && value.trim()) return value.trim();
            }
            const node = [source?.realm, source?.node]
              .filter((v) => typeof v === 'string' && v.trim())
              .join('.');
            return node;
          };
          const memberDepartment = (source) => {
            const value = source?.department;
            return typeof value === 'string' && value.trim() ? value.trim() : '';
          };
          const modeOptions = [
            'ask_each',
            'same_org',
            'allowlist',
            'deny_all',
          ].map((mode) => ({
            value: mode,
            label: t(mode),
            description: t(mode + 'Hint'),
          }));
          const iconSvg = (paths, size = 18) =>
            h(
              'svg',
              {
                width: size,
                height: size,
                viewBox: '0 0 24 24',
                fill: 'none',
                stroke: 'currentColor',
                strokeWidth: 1.8,
                strokeLinecap: 'round',
                strokeLinejoin: 'round',
                'aria-hidden': true,
                className: 'auriga-settings-card-icon',
              },
              ...paths.map((d, i) => h('path', { key: i, d })),
            );
          const settingsContent =
            status === 'ready' &&
            h(
              React.Fragment,
              null,
              h(
                'section',
                { className: 'auriga-settings-card' },
                h(
                  'div',
                  { className: 'auriga-settings-card-header' },
                  h(
                    'div',
                    { className: 'auriga-settings-card-brand' },
                    iconSvg([
                      'M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z',
                      'm9 12 2 2 4-4',
                    ]),
                    h(
                      'span',
                      { className: 'auriga-settings-card-title' },
                      t('defaultPolicy'),
                    ),
                  ),
                  h(
                    'button',
                    {
                      type: 'button',
                      className: 'auriga-setting-action is-quiet is-compact',
                      onClick: () => reload(),
                    },
                    t('refresh'),
                  ),
                ),
                h(
                  'div',
                  { className: 'auriga-settings-card-body' },
                  ...['conversation', 'execution'].map((layer) => {
                    const locked = view.organization[layer] !== 'allow';
                    const value = locked
                      ? view.organization[layer]
                      : view.defaults[layer];
                    return h(
                      'div',
                      { key: layer },
                      h(
                        'div',
                        { className: 'auriga-setting-row' },
                        h(
                          'div',
                          { className: 'auriga-setting-row-meta' },
                          h(
                            'div',
                            { className: 'auriga-setting-row-copy' },
                            h(
                              'div',
                              { className: 'auriga-setting-row-label' },
                              layerName(layer),
                            ),
                            h(
                              'div',
                              { className: 'auriga-setting-row-desc' },
                              t(
                                layer === 'conversation'
                                  ? 'conversationLayerHint'
                                  : 'executionLayerHint',
                              ),
                            ),
                          ),
                        ),
                        h(
                          'div',
                          { className: 'auriga-setting-row-control' },
                          (() => {
                            const open = openSelect === layer;
                            const selected =
                              modeOptions.find((o) => o.value === value) ??
                              modeOptions[0];
                            return h(
                              'div',
                              {
                                className:
                                  'auriga-setting-select is-rich' +
                                  (open ? ' is-open' : ''),
                              },
                              h(
                                'button',
                                {
                                  type: 'button',
                                  className: 'auriga-setting-select-trigger',
                                  'aria-label': layerName(layer),
                                  'aria-haspopup': 'listbox',
                                  'aria-expanded': open,
                                  disabled: !!locked,
                                  onClick: (event) => {
                                    event.stopPropagation();
                                    if (!locked)
                                      setOpenSelect(open ? '' : layer);
                                  },
                                },
                                selected?.label ?? value ?? '',
                                h(
                                  'span',
                                  {
                                    className: 'auriga-setting-select-chevron',
                                    'aria-hidden': true,
                                  },
                                  '▾',
                                ),
                              ),
                              open &&
                                h(
                                  'div',
                                  {
                                    className: 'auriga-setting-select-menu',
                                    role: 'listbox',
                                  },
                                  ...modeOptions.map((option) => {
                                    const active = option.value === value;
                                    return h(
                                      'button',
                                      {
                                        key: option.value,
                                        type: 'button',
                                        role: 'option',
                                        className:
                                          'auriga-setting-select-option is-rich' +
                                          (active ? ' is-selected' : ''),
                                        'aria-selected': active,
                                        onClick: (event) => {
                                          event.stopPropagation();
                                          setOpenSelect('');
                                          if (option.value !== value && !locked)
                                            return change(() =>
                                              api.setDefault(
                                                layer,
                                                option.value,
                                              ),
                                            );
                                        },
                                      },
                                      h(
                                        'span',
                                        {
                                          className:
                                            'auriga-setting-select-option-copy',
                                        },
                                        h(
                                          'span',
                                          {
                                            className:
                                              'auriga-setting-select-option-label',
                                          },
                                          option.label,
                                        ),
                                        option.description
                                          ? h(
                                              'span',
                                              {
                                                className:
                                                  'auriga-setting-select-option-desc',
                                              },
                                              option.description,
                                            )
                                          : null,
                                      ),
                                      active
                                        ? h(
                                            'svg',
                                            {
                                              className:
                                                'auriga-setting-select-check',
                                              width: 14,
                                              height: 14,
                                              viewBox: '0 0 24 24',
                                              fill: 'none',
                                              stroke: 'currentColor',
                                              strokeWidth: 1.8,
                                              strokeLinecap: 'round',
                                              strokeLinejoin: 'round',
                                              'aria-hidden': true,
                                            },
                                            h('path', {
                                              d: 'M20 6 9 17l-5-5',
                                            }),
                                          )
                                        : null,
                                    );
                                  }),
                                ),
                            );
                          })(),
                        ),
                      ),
                      locked &&
                        h(
                          'p',
                          { className: 'auriga-settings-card-group-hint' },
                          t('locked') + view.organization.revision,
                        ),
                    );
                  }),
                ),
              ),
              h(
                'section',
                { className: 'auriga-settings-card' },
                h(
                  'div',
                  { className: 'auriga-settings-card-header' },
                  h(
                    'div',
                    { className: 'auriga-settings-card-brand' },
                    iconSvg([
                      'm3 17 2 2 4-4',
                      'm3 7 2 2 4-4',
                      'M13 6h8',
                      'M13 12h8',
                      'M13 18h8',
                    ]),
                    h(
                      'span',
                      { className: 'auriga-settings-card-title' },
                      t('allowLists'),
                    ),
                  ),
                ),
                h(
                  'div',
                  { className: 'auriga-settings-card-body' },
                  ...['allow_source', 'block'].map((choice) => {
                    const rules = view.rules.filter(
                      (rule) => rule.choice === choice,
                    );
                    const query = (listQuery[choice] || '').trim().toLowerCase();
                    const visible = query
                      ? rules.filter((rule) => {
                          const hay = [
                            memberPrimary(rule.source),
                            memberSecondary(rule.source),
                            memberDepartment(rule.source),
                            sourceName(rule.source),
                            layerName(rule.layer),
                            t(rule.scope),
                          ]
                            .filter(Boolean)
                            .join(' ')
                            .toLowerCase();
                          return hay.includes(query);
                        })
                      : rules;
                    return h(
                      'details',
                      { key: choice, className: 'auriga-consent-list' },
                      h(
                        'summary',
                        { className: 'auriga-consent-list-entry' },
                        h(
                          'div',
                          { className: 'auriga-consent-list-entry-meta' },
                          h(
                            'div',
                            { className: 'auriga-consent-list-entry-title' },
                            t(choice === 'block' ? 'blocked' : 'persistent'),
                          ),
                          h(
                            'div',
                            { className: 'auriga-consent-list-entry-count' },
                            `${rules.length} ${t('listCount')}`,
                          ),
                        ),
                        h(
                          'span',
                          { className: 'auriga-consent-list-entry-count' },
                          t('listOpen'),
                        ),
                      ),
                      h(
                        'div',
                        { className: 'auriga-consent-list-panel' },
                        rules.length > 8 &&
                          h('input', {
                            className:
                              'auriga-setting-input auriga-consent-list-search',
                            'aria-label': t('listSearch'),
                            placeholder: t('listSearch'),
                            value: listQuery[choice] || '',
                            onChange: (event) =>
                              setListQuery((old) => ({
                                ...old,
                                [choice]: event.target.value,
                              })),
                          }),
                        rules.length === 0 &&
                          h(
                            'p',
                            { className: 'auriga-settings-card-empty' },
                            t('noRules'),
                          ),
                        ...visible.map((rule) => {
                          const primary = memberPrimary(rule.source);
                          const secondary = memberSecondary(rule.source);
                          const department = memberDepartment(rule.source);
                          return h(
                            'div',
                            {
                              key: rule.id,
                              className: 'auriga-consent-member',
                            },
                            h(
                              'div',
                              { className: 'auriga-consent-member-copy' },
                              h(
                                'div',
                                { className: 'auriga-consent-member-name' },
                                h(
                                  'span',
                                  {
                                    className: 'auriga-consent-member-primary',
                                  },
                                  primary || sourceName(rule.source),
                                ),
                                secondary
                                  ? h(
                                      'span',
                                      {
                                        className:
                                          'auriga-consent-member-secondary',
                                      },
                                      secondary,
                                    )
                                  : null,
                              ),
                              h(
                                'div',
                                { className: 'auriga-consent-member-desc' },
                                [
                                  layerName(rule.layer),
                                  t(rule.scope),
                                  department,
                                ]
                                  .filter(Boolean)
                                  .join(' · '),
                              ),
                            ),
                            h(
                              'button',
                              {
                                type: 'button',
                                className:
                                  'auriga-setting-action is-compact is-quiet',
                                onClick: () =>
                                  change(() =>
                                    api.revoke({
                                      layer: rule.layer,
                                      ruleId: rule.id,
                                    }),
                                  ),
                              },
                              t('revoke'),
                            ),
                          );
                        }),
                      ),
                    );
                  }),
                  h(
                    'p',
                    { className: 'auriga-settings-card-group-hint' },
                    t('revokeHint'),
                  ),
                ),
              ),
            );
          const content = !api
            ? h('p', { role: 'status' }, t('consentMissing'))
            : status !== 'ready'
              ? h(
                  'div',
                  { role: 'status' },
                  t(
                    status === 'loading'
                      ? 'loading'
                      : status === 'cooldown'
                        ? 'cooldownHint'
                        : 'unavailable',
                  ),
                  status !== 'loading' &&
                    h(
                      'button',
                      { type: 'button', onClick: () => reload() },
                      t('retry'),
                    ),
                )
              : settings
                ? settingsContent
                : h(
                    'div',
                    null,
                    h('p', null, t('previewHint')),
                    h('p', null, t('onceHint')),
                    view.pending.length === 0 && h('p', null, t('empty')),
                    ...view.pending.map((row) => {
                      const { layer, requestId } = row.request;
                      const key = JSON.stringify([layer, requestId]);
                      const scope = scopes[key] ?? '';
                      return h(
                        'article',
                        { key },
                        h('h4', null, layerName(layer)),
                        h(
                          'p',
                          null,
                          t('verified') + ': ' + sourceName(row.source),
                        ),
                        h('strong', null, t('external')),
                        // Text child only: no markdown, HTML or automatic link handling.
                        h(
                          'pre',
                          { dir: 'auto' },
                          String(row.preview ?? '').slice(0, 1000),
                        ),
                        h(
                          'label',
                          null,
                          t('scope'),
                          h(
                            'select',
                            {
                              'aria-label':
                                t('scope') + ' · ' + layerName(layer),
                              value: scope,
                              onChange: (event) =>
                                setScopes((old) => ({
                                  ...old,
                                  [key]: event.target.value,
                                })),
                            },
                            h('option', { value: '' }, t('chooseScope')),
                            ...['source_node', 'source_user'].map((value) =>
                              h('option', { key: value, value }, t(value)),
                            ),
                          ),
                        ),
                        h(
                          'div',
                          { className: 'auriga-consent-actions' },
                          ...[
                            'allow_once',
                            'allow_source',
                            'deny',
                            'block',
                          ].map((choice) =>
                            h(
                              'button',
                              {
                                key: choice,
                                type: 'button',
                                disabled:
                                  (choice === 'allow_source' ||
                                    choice === 'block') &&
                                  !scope,
                                onClick: () => {
                                  if (
                                    (choice === 'allow_source' ||
                                      choice === 'block') &&
                                    !scope
                                  )
                                    return;
                                  return change(() =>
                                    api.decide({
                                      layer,
                                      requestId,
                                      choice,
                                      ...(choice === 'allow_source' ||
                                      choice === 'block'
                                        ? { sourceScope: scope }
                                        : {}),
                                      expectedRevision: view.policyRevision,
                                    }),
                                  );
                                },
                              },
                              t(choice),
                            ),
                          ),
                        ),
                      );
                    }),
                  );
          if (status === 'checking' || status === 'unsupported') return null;
          return h(
            'section',
            {
              className:
                'auriga-consent' + (settings ? ' auriga-consent-settings' : ''),
              'aria-label': t(settings ? 'consent' : 'inbox'),
            },
            !settings && h('h3', null, t('inbox')),
            !settings &&
              api &&
              status === 'ready' &&
              h(
                'button',
                {
                  type: 'button',
                  onClick: () => reload(),
                },
                t('refresh'),
              ),
            content,
          );
        }
        function Settings({ embedded = false, gotoOverview } = {}) {
          const host = useHostState();
          const [phase, retry] = useBootstrap();
          const boot = phase.status === 'ready' ? phase.boot : undefined;
          const [View, setView] = React.useState(null);
          const [loadFailed, setLoadFailed] = React.useState(false);
          React.useEffect(() => {
            let live = true;
            import(localURL('auriga/settings/view.js'))
              .then((module) => {
                if (live && !disposed)
                  setView(() =>
                    module.createSettingsView(React, {
                      localURL,
                      fetcher: fetch,
                      openLogin: () => openPlatformLogin(),
                      subscribeIdentity,
                      identityChanged,
                      navigate: (url) => window.location.assign(url),
                    }),
                  );
              })
              .catch(() => {
                if (live && !disposed) setLoadFailed(true);
              });
            return () => {
              live = false;
            };
          }, []);
          return h(
            'section',
            {
              className: 'auriga-shell auriga-settings',
              'aria-label': t('panel'),
            },
            !embedded && h('h2', null, t('panel')),
            phase.status !== 'ready' && notice(phase, retry, true),
            View
              ? h(View, {
                  locale: host.locale,
                  boot,
                  consent: h(ConsentSection, { settings: true }),
                  gotoOverview,
                })
              : h(
                  'p',
                  { role: 'status' },
                  t(loadFailed ? 'unavailable' : 'loading'),
                ),
          );
        }
        const navigate = (to) => {
          if (to === 'session:new') ctx.get('uiWorkspace')?.startSession?.();
          else if (/^https?:\/\//i.test(to))
            window.open(to, '_blank', 'noopener,noreferrer');
        };
        let toastTimer;
        const notify = (detail) => {
          if (disposed) return;
          state.toast = {
            level: detail?.level ?? 'info',
            message: String(detail?.message ?? ''),
          };
          emit();
          clearTimer(toastTimer);
          toastTimer = later(() => {
            state.toast = undefined;
            emit();
          }, 4000);
        };
        function useDesktopUpdate() {
          const [desktop, setDesktop] = React.useState(null);
          const read = async (action, signal) => {
            const response = await fetch(
              localURL(
                action ? 'auriga/settings/desktop' : 'auriga/settings/state',
              ),
              {
                credentials: 'same-origin',
                cache: 'no-store',
                redirect: 'error',
                signal,
                ...(action
                  ? {
                      method: 'POST',
                      headers: {
                        'Content-Type': 'application/json',
                        'X-Auriga-Settings': '1',
                      },
                      body: JSON.stringify({ action }),
                    }
                  : {}),
              },
            );
            if (!response.ok) throw failure();
            const result = await response.json();
            return action ? result : result.desktop;
          };
          React.useEffect(() => {
            let live = true;
            const controller = new AbortController();
            const refresh = () =>
              read(undefined, controller.signal)
                .then((value) => {
                  if (live && !disposed) setDesktop(value);
                })
                .catch(() => {});
            void refresh();
            const timer = setInterval(refresh, 5000);
            return () => {
              live = false;
              controller.abort();
              clearInterval(timer);
            };
          }, []);
          const action = async (value) => {
            try {
              setDesktop(await read(value));
            } catch {
              setDesktop((previous) => ({
                ...previous,
                error: 'updateFailed',
              }));
            }
          };
          return [desktop, action];
        }
        const identityListeners = new Set();
        const subscribeIdentity = (listener) => {
          identityListeners.add(listener);
          return () => identityListeners.delete(listener);
        };
        const identityChanged = () => {
          for (const listener of identityListeners) listener();
        };
        let desktopCatalog;
        let shellReadSequence = 0;
        let latestShellRead = 0;
        // Local logout fence: ignore authenticated status/catalog while the
        // explicit sign-out round-trip is outstanding (hang or 503).
        let shellSignOutFence = 0;
        const verifiedPanel = () => {
          const panel = customElements.get(ELEMENT);
          return panel && loadedPanels.has(panel) ? panel : undefined;
        };
        const catalogGenerationOf = (account) =>
          typeof account?.catalogGeneration === 'string' &&
          /^[0-9a-f]{64}$/.test(account.catalogGeneration)
            ? account.catalogGeneration
            : undefined;
        const updateDesktopCatalog = (status, sequence) => {
          if (
            typeof status?.loggedIn !== 'boolean' ||
            sequence <= latestShellRead
          )
            return;
          if (shellSignOutFence && status.loggedIn) return;
          latestShellRead = sequence;
          // Marketplace cache is resident until explicit sign-out. Transient
          // offline / reauth must not clearCatalog or force a full prefetch.
          if (!status.loggedIn) {
            if (desktopCatalog) verifiedPanel()?.clearCatalog?.(desktopCatalog);
            desktopCatalog = undefined;
            emit();
            return;
          }
          const ownerId =
            typeof status.account?.subject === 'string'
              ? status.account.subject
              : desktopCatalog?.ownerId;
          const generation =
            catalogGenerationOf(status.account) ?? desktopCatalog?.generation;
          if (!ownerId) return;
          if (
            desktopCatalog?.ownerId === ownerId &&
            desktopCatalog?.generation === generation
          )
            return;
          // Real generation change (identity/device fence) replaces the scope.
          if (desktopCatalog) verifiedPanel()?.clearCatalog?.(desktopCatalog);
          desktopCatalog = Object.freeze({ ownerId, generation });
          emit();
          if (
            !desktopCatalog ||
            !['127.0.0.1', '[::1]', 'localhost'].includes(
              window.location.hostname,
            )
          )
            return;
          const scope = desktopCatalog;
          const controller = new AbortController();
          controllers.add(controller);
          void (async () => {
            let panel = verifiedPanel();
            if (!panel) {
              const boot = await requestBootstrap(controller.signal);
              if (boot.mode !== 'connect' || desktopCatalog !== scope) return;
              const sharedLoad = !!panelPending;
              try {
                await ensurePanel(boot, controller.signal);
              } catch (error) {
                // A main-panel unmount can cancel the shared script attempt.
                if (
                  !sharedLoad ||
                  error?.reason !== 'unavailable' ||
                  controller.signal.aborted ||
                  desktopCatalog !== scope
                )
                  throw error;
                await ensurePanel(boot, controller.signal);
              }
              panel = verifiedPanel();
            }
            if (disposed || desktopCatalog !== scope) return;
            panel?.prefetchCatalog?.({
              mode: 'connect',
              protocol: 1,
              fetch: makeFetch({
                fetchBase: localURL('auriga/resources/').href,
              }),
              catalogSession: scope,
            });
          })()
            .catch(() => {
              // The mounted panel can prefetch once its verified bundle is ready.
            })
            .finally(() => controllers.delete(controller));
        };
        // Polls one authenticated local JSON status route. Errors become null.
        const useLocalStatus = (
          path,
          interval,
          enabled,
          keepAvailability = false,
        ) => {
          const [value, setValue] = React.useState(undefined);
          const [tick, bump] = React.useReducer((n) => n + 1, 0);
          const revision = React.useRef(0);
          const update = React.useRef(null);
          if (!update.current)
            update.current = (next) => {
              revision.current++;
              setValue(next);
            };
          React.useEffect(() => {
            if (!enabled) return undefined;
            let live = true;
            let reading = false;
            let readAgain = false;
            const controller = new AbortController();
            const read = async () => {
              if (reading) {
                readAgain = true;
                return;
              }
              reading = true;
              const started = revision.current;
              const shellRead =
                path === 'auriga/shell/status' ? ++shellReadSequence : 0;
              const next = await fetch(localURL(path), {
                credentials: 'same-origin',
                cache: 'no-store',
                redirect: 'error',
                signal: controller.signal,
              })
                .then(async (response) =>
                  response.ok ? await response.json() : null,
                )
                .catch(() => null);
              reading = false;
              if (live && !disposed && revision.current === started) {
                if (shellRead && next) updateDesktopCatalog(next, shellRead);
                setValue((previous) => {
                  if (
                    path === 'auriga/shell/status' &&
                    shellSignOutFence &&
                    next?.loggedIn
                  )
                    return previous?.loggedIn === false
                      ? previous
                      : {
                          available: true,
                          loggedIn: false,
                          connected: false,
                          recovering: false,
                          account: null,
                          platform: 'signed_out',
                          csrf: previous?.csrf ?? next?.csrf,
                          brokerAvailable: previous?.brokerAvailable,
                        };
                  if (next) return next;
                  // Keep resident account fields across poll failures, but mark
                  // soft-offline so Team/actions do not stay falsely ready.
                  if (path === 'auriga/shell/status' && previous) {
                    if (!previous.loggedIn) return previous;
                    return {
                      ...previous,
                      connected: false,
                      recovering: true,
                      platform: 'unreachable',
                    };
                  }
                  if (keepAvailability && previous?.available)
                    return { available: true, reason: 'status_unavailable' };
                  return null;
                });
              }
              if (live && readAgain) {
                readAgain = false;
                void read();
              }
            };
            void read();
            const timer = setInterval(read, interval);
            const unsubscribe = subscribeIdentity(() => {
              revision.current++;
              void read();
            });
            return () => {
              unsubscribe();
              live = false;
              controller.abort();
              clearInterval(timer);
            };
          }, [tick, interval, enabled, keepAvailability]);
          return [value, bump, update.current];
        };
        const postLocal = async (path, csrf, body = {}, signal) => {
          const response = await fetch(localURL(path), {
            method: 'POST',
            credentials: 'same-origin',
            cache: 'no-store',
            redirect: 'error',
            signal,
            headers: {
              'content-type': 'application/json',
              ...(csrf ? { 'X-Auriga-CSRF': csrf } : {}),
            },
            body: JSON.stringify(body),
          });
          if (!response.ok)
            throw Object.assign(failure(), { status: response.status });
          const value = await response.json();
          if (path === 'auriga/shell/command' && body.action === 'logout')
            updateDesktopCatalog({ loggedIn: false }, ++shellReadSequence);
          identityChanged();
          return value;
        };
        // Native host controls when the public primitives expose them;
        // plain semantic elements otherwise (capability probe, no version pin).
        const Action = ({ primary = false, children, ...props }) =>
          HostButton
            ? h(
                HostButton,
                {
                  variant: primary ? 'primary' : 'outline',
                  size: 'sm',
                  className: 'auriga-host-control',
                  ...props,
                },
                children,
              )
            : h(
                'button',
                {
                  type: 'button',
                  className: primary ? 'auriga-primary' : undefined,
                  ...props,
                },
                children,
              );
        const Dot = ({ tone }) =>
          HostStateDot
            ? h(HostStateDot, { state: tone, size: 8 })
            : h('span', {
                className: 'auriga-dot',
                'data-tone': tone,
                'aria-hidden': true,
              });
        const NAV_EXPANDED_KEY = 'auriga.activityBar.expanded';
        const AURIGA_NAV_ENTRIES = [
          {
            id: 'overview',
            labelKey: 'tab_overview',
            shortLabelKey: 'tab_overview',
            icon: 'LayoutDashboard',
            route: 'overview',
            group: 'workspace',
            order: 10,
            placement: 'top',
          },
          {
            id: 'market',
            labelKey: 'tab_market',
            shortLabelKey: 'tab_market_short',
            icon: 'Store',
            route: 'market',
            group: 'workspace',
            order: 20,
            placement: 'top',
          },
          {
            id: 'mine',
            labelKey: 'tab_mine',
            shortLabelKey: 'tab_mine_short',
            icon: 'LibraryBig',
            route: 'mine',
            group: 'workspace',
            order: 30,
            placement: 'top',
          },
          {
            id: 'team',
            labelKey: 'tab_team',
            shortLabelKey: 'tab_team_short',
            icon: 'UsersRound',
            route: 'team',
            group: 'workspace',
            order: 40,
            placement: 'top',
          },
          {
            id: 'inbox',
            labelKey: 'tab_inbox',
            shortLabelKey: 'tab_inbox',
            icon: 'Inbox',
            route: 'inbox',
            group: 'system',
            order: 10,
            placement: 'bottom',
          },
          {
            id: 'settings',
            labelKey: 'tab_settings',
            shortLabelKey: 'tab_settings',
            icon: 'Settings',
            route: 'settings',
            group: 'system',
            order: 20,
            placement: 'bottom',
          },
        ];
        function createAurigaNavigationRegistry(seed = AURIGA_NAV_ENTRIES) {
          const entries = seed.map((entry) => ({ ...entry }));
          const sortEntries = () =>
            entries.sort(
              (a, b) =>
                (a.order ?? 0) - (b.order ?? 0) ||
                String(a.id).localeCompare(String(b.id)),
            );
          sortEntries();
          return {
            register(entry) {
              const index = entries.findIndex((item) => item.id === entry.id);
              if (index >= 0) entries.splice(index, 1, { ...entry });
              else entries.push({ ...entry });
              sortEntries();
              return this;
            },
            list(placement, ctx = {}) {
              return entries.filter((item) => {
                if (item.placement !== placement) return false;
                if (item.visible === false) return false;
                if (typeof item.visible === 'function' && !item.visible(ctx))
                  return false;
                if (
                  item.permission &&
                  typeof ctx.can === 'function' &&
                  !ctx.can(item.permission)
                )
                  return false;
                return true;
              });
            },
            get(id) {
              return entries.find((item) => item.id === id);
            },
          };
        }
        const aurigaNavRegistry = createAurigaNavigationRegistry();
        function lucideIcon(children, props = {}) {
          return h(
            'svg',
            {
              xmlns: 'http://www.w3.org/2000/svg',
              width: 20,
              height: 20,
              viewBox: '0 0 24 24',
              fill: 'none',
              stroke: 'currentColor',
              strokeWidth: 1.8,
              strokeLinecap: 'round',
              strokeLinejoin: 'round',
              'aria-hidden': true,
              focusable: 'false',
              ...props,
            },
            ...children,
          );
        }
        const AurigaIcons = {
          LayoutDashboard: (props) =>
            lucideIcon(
              [
                h('rect', {
                  key: 'a',
                  width: '7',
                  height: '7',
                  x: '3',
                  y: '3',
                  rx: '1',
                }),
                h('rect', {
                  key: 'b',
                  width: '7',
                  height: '7',
                  x: '14',
                  y: '3',
                  rx: '1',
                }),
                h('rect', {
                  key: 'c',
                  width: '7',
                  height: '7',
                  x: '14',
                  y: '14',
                  rx: '1',
                }),
                h('rect', {
                  key: 'd',
                  width: '7',
                  height: '7',
                  x: '3',
                  y: '14',
                  rx: '1',
                }),
              ],
              props,
            ),
          Store: (props) =>
            lucideIcon(
              [
                h('path', {
                  key: 'a',
                  d: 'm2 7 4.41-4.41A2 2 0 0 1 7.83 2h8.34a2 2 0 0 1 1.42.59L22 7',
                }),
                h('path', {
                  key: 'b',
                  d: 'M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8',
                }),
                h('path', {
                  key: 'c',
                  d: 'M15 22v-4a2 2 0 0 0-2-2h-2a2 2 0 0 0-2 2v4',
                }),
                h('path', { key: 'd', d: 'M2 7h20' }),
                h('path', {
                  key: 'e',
                  d: 'M22 7v3a2 2 0 0 1-2 2 2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 16 12a2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 12 12a2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 8 12a2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 4 12a2 2 0 0 1-2-2V7',
                }),
              ],
              props,
            ),
          LibraryBig: (props) =>
            lucideIcon(
              [
                h('rect', {
                  key: 'a',
                  width: '8',
                  height: '18',
                  x: '3',
                  y: '3',
                  rx: '1',
                }),
                h('path', { key: 'b', d: 'M7 3v18' }),
                h('path', {
                  key: 'c',
                  d: 'M20.4 18.9c.2.5-.1 1.1-.6 1.3l-1.9.7c-.5.2-1.1-.1-1.3-.6L11.1 5.1c-.2-.5.1-1.1.6-1.3l1.9-.7c.5-.2 1.1.1 1.3.6Z',
                }),
              ],
              props,
            ),
          UsersRound: (props) =>
            lucideIcon(
              [
                h('path', { key: 'a', d: 'M18 21a8 8 0 0 0-16 0' }),
                h('circle', { key: 'b', cx: '10', cy: '8', r: '5' }),
                h('path', {
                  key: 'c',
                  d: 'M22 20c0-3.37-2-6.5-4-8a5 5 0 0 0-.45-8.3',
                }),
              ],
              props,
            ),
          Inbox: (props) =>
            lucideIcon(
              [
                h('polyline', {
                  key: 'a',
                  points: '22 12 16 12 14 15 10 15 8 12 2 12',
                }),
                h('path', {
                  key: 'b',
                  d: 'M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z',
                }),
              ],
              props,
            ),
          Settings: (props) =>
            lucideIcon(
              [
                h('path', {
                  key: 'a',
                  d: 'M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z',
                }),
                h('circle', { key: 'b', cx: '12', cy: '12', r: '3' }),
              ],
              props,
            ),
          PanelLeftOpen: (props) =>
            lucideIcon(
              [
                h('rect', {
                  key: 'a',
                  width: '18',
                  height: '18',
                  x: '3',
                  y: '3',
                  rx: '2',
                }),
                h('path', { key: 'b', d: 'M9 3v18' }),
                h('path', { key: 'c', d: 'm14 9 3 3-3 3' }),
              ],
              props,
            ),
          PanelLeftClose: (props) =>
            lucideIcon(
              [
                h('rect', {
                  key: 'a',
                  width: '18',
                  height: '18',
                  x: '3',
                  y: '3',
                  rx: '2',
                }),
                h('path', { key: 'b', d: 'M9 3v18' }),
                h('path', { key: 'c', d: 'm16 15-3-3 3-3' }),
              ],
              props,
            ),
        };
        function readNavExpanded() {
          try {
            return localStorage.getItem(NAV_EXPANDED_KEY) === '1';
          } catch {
            return false;
          }
        }
        function writeNavExpanded(expanded) {
          try {
            localStorage.setItem(NAV_EXPANDED_KEY, expanded ? '1' : '0');
          } catch {
            /* private mode */
          }
        }
        function AurigaNavigationItem({
          item,
          active,
          expanded,
          badge,
          onSelect,
          onKeyNav,
        }) {
          const label = t(item.labelKey);
          const shortLabel = t(item.shortLabelKey || item.labelKey);
          const Icon = AurigaIcons[item.icon];
          const disabled =
            item.disabled === true ||
            (typeof item.disabled === 'function' && item.disabled());
          const shown = expanded ? label : shortLabel;
          return h(
            'button',
            {
              type: 'button',
              id: 'auriga-tab-' + item.id,
              className: 'auriga-nav-item',
              'aria-current': active ? 'page' : undefined,
              'aria-controls': 'auriga-panel-' + item.id,
              'aria-label': label,
              title: expanded ? undefined : label,
              disabled: disabled || undefined,
              tabIndex: active ? 0 : -1,
              onClick: () => !disabled && onSelect(item.route),
              onKeyDown: onKeyNav,
            },
            h('span', { className: 'auriga-nav-icon' }, Icon && h(Icon)),
            h('span', { className: 'auriga-nav-label' }, shown),
            badge
              ? h(
                  'span',
                  { className: 'auriga-nav-badge', 'aria-hidden': true },
                  badge > 99 ? '99+' : String(badge),
                )
              : null,
          );
        }
        function AurigaActivityBar({
          value,
          onChange,
          expanded,
          onToggle,
          badges = {},
        }) {
          const top = aurigaNavRegistry.list('top');
          const bottom = aurigaNavRegistry.list('bottom');
          const items = [...top, ...bottom];
          const focusNav = (event, index) => {
            const next = {
              ArrowUp: (index + items.length - 1) % items.length,
              ArrowDown: (index + 1) % items.length,
              ArrowLeft: (index + items.length - 1) % items.length,
              ArrowRight: (index + 1) % items.length,
              Home: 0,
              End: items.length - 1,
            }[event.key];
            if (next === undefined) return;
            event.preventDefault();
            const target = items[next];
            onChange(target.route);
            event.currentTarget
              .closest('.auriga-activity-bar')
              ?.querySelector('#auriga-tab-' + target.id)
              ?.focus();
          };
          const ToggleIcon = expanded
            ? AurigaIcons.PanelLeftClose
            : AurigaIcons.PanelLeftOpen;
          return h(
            'nav',
            {
              className: 'auriga-activity-bar',
              'aria-label': t('nav_label'),
            },
            h(
              'button',
              {
                type: 'button',
                className: 'auriga-nav-toggle',
                'aria-label': t(expanded ? 'nav_collapse' : 'nav_expand'),
                'aria-expanded': expanded,
                title: t(expanded ? 'nav_collapse' : 'nav_expand'),
                onClick: onToggle,
              },
              h('span', { className: 'auriga-nav-icon' }, h(ToggleIcon)),
              expanded
                ? h(
                    'span',
                    { className: 'auriga-nav-label' },
                    t('nav_collapse'),
                  )
                : null,
            ),
            h(
              'div',
              { className: 'auriga-activity-bar-top', role: 'list' },
              ...top.map((item, index) =>
                h(AurigaNavigationItem, {
                  key: item.id,
                  item,
                  active: value === item.route,
                  expanded,
                  badge: badges[item.id],
                  onSelect: onChange,
                  onKeyNav: (event) => focusNav(event, index),
                }),
              ),
            ),
            h(
              'div',
              { className: 'auriga-activity-bar-bottom', role: 'list' },
              ...bottom.map((item, index) =>
                h(AurigaNavigationItem, {
                  key: item.id,
                  item,
                  active: value === item.route,
                  expanded,
                  badge: badges[item.id],
                  onSelect: onChange,
                  onKeyNav: (event) => focusNav(event, top.length + index),
                }),
              ),
            ),
          );
        }
        function AurigaLayout({
          tab,
          onTabChange,
          expanded,
          onToggleExpanded,
          badges,
          banners,
          children,
        }) {
          return h(
            'div',
            {
              className: 'auriga-shell auriga-app',
              'data-nav-expanded': expanded ? 'true' : 'false',
            },
            h(AurigaActivityBar, {
              value: tab,
              onChange: onTabChange,
              expanded,
              onToggle: onToggleExpanded,
              badges,
            }),
            h(
              'div',
              { className: 'auriga-main' },
              ...(banners || []),
              h('div', { className: 'auriga-main-body' }, children),
            ),
          );
        }
        // One empty/blocked state for every tab: what happened and the next step.
        const Blocked = ({ tone = 'idle', title, detail, action }) =>
          h(
            'div',
            { className: 'auriga-blocked', role: 'status' },
            h(
              'div',
              { className: 'auriga-row' },
              h(Dot, { tone }),
              h('strong', null, title),
            ),
            detail && h('p', null, detail),
            action,
          );
        // Platform session projection from the shell status route.
        const platformView = (boot, shell) => {
          if (boot?.mode === 'managed')
            return { kind: 'managed', tone: 'done' };
          if (boot?.mode === 'standalone' || shell?.configured === false)
            return { kind: 'unconfigured', tone: 'idle' };
          if (!shell) return { kind: 'unreachable', tone: 'warning' };
          if (shell.platform === 'authenticating')
            return { kind: 'authenticating', tone: 'ongoing' };
          if (!shell.loggedIn)
            return {
              kind:
                shell.platform === 'unreachable' || !shell.brokerAvailable
                  ? 'unreachable'
                  : shell.platform === 'reauth_required'
                    ? 'reauth'
                    : 'signedOut',
              tone: shell.platform === 'unreachable' ? 'warning' : 'idle',
            };
          // Keep account visible; only unreachable is soft-recovering. Terminal
          // reauth/revoked still offer sign-in while the market cache stays.
          if (
            shell.platform === 'revoked' ||
            shell.platform === 'reauth_required'
          )
            return { kind: 'reauth', tone: 'warning' };
          if (shell.recovering || shell.platform === 'unreachable')
            return { kind: 'recovering', tone: 'warning' };
          return shell.connected
            ? { kind: 'connected', tone: 'done' }
            : { kind: 'degraded', tone: 'warning' };
        };
        function LocalIdentityCard({
          uac,
          setUac,
          reload,
          active,
          modal = false,
        }) {
          const [busy, setBusy] = React.useState(false);
          const [error, setError] = React.useState('');
          const opened = React.useRef(false);
          const operation = React.useRef(0);
          const challenge =
            uac.challenge?.state === 'authenticated'
              ? undefined
              : uac.challenge;
          const diagnosticKey = {
            v20_app_bound: 'uacSsoAppBound',
            browser_store_unreadable: 'uacSsoUnreadable',
            destination_not_allowed: 'uacDestinationRejected',
            dns_address_mismatch: 'uacDestinationRejected',
            dns_unavailable: 'uacDnsUnavailable',
            token_rejected: 'uacSsoRejected',
          }[
            uac.configurationDiagnostic ??
              challenge?.failureCode ??
              uac.ssoDiagnostic
          ];
          const pending =
            challenge &&
            ['waiting', 'scanned', 'verifying'].includes(challenge.state);
          React.useEffect(() => {
            if (!active || !pending || busy) return undefined;
            let live = true;
            let timer;
            const controller = new AbortController();
            const poll = () => {
              const started = operation.current;
              postLocal(
                'auriga/uac/poll',
                uac.csrf,
                { id: challenge.id },
                controller.signal,
              )
                .then((next) => {
                  if (live && !disposed && started === operation.current)
                    setUac(next);
                })
                .catch(() => {
                  if (live && !disposed) reload();
                })
                .finally(() => {
                  if (live && !disposed) timer = setTimeout(poll, 2000);
                });
            };
            timer = setTimeout(poll, 2000);
            return () => {
              live = false;
              controller.abort();
              clearTimeout(timer);
            };
          }, [active, pending, busy, challenge?.id, uac.csrf]);
          const run = (path, body) => {
            operation.current++;
            setBusy(true);
            setError('');
            postLocal(path, uac.csrf, body)
              .then((next) => {
                if (!disposed) setUac(next);
              })
              .catch(() => {
                if (!disposed) setError(t('uacUnavailable'));
              })
              .finally(() => {
                if (!disposed) setBusy(false);
              });
          };
          const signedIn = uac.state === 'authenticated';
          React.useEffect(() => {
            if (!active || opened.current || uac.reason) return;
            opened.current = true;
            if (!signedIn && !challenge && !uac.lastOutcome)
              run('auriga/uac/start');
          }, [active, signedIn, challenge, uac.reason]);
          if (uac.reason)
            return h(Blocked, {
              tone: 'warning',
              title: t('localIdentity'),
              detail: diagnosticKey
                ? t(diagnosticKey)
                : t(
                    {
                      invalid: 'uacConfigInvalid',
                      storage: 'uacStorageUnavailable',
                      owner_domain: 'uacOwnerDomain',
                    }[uac.reason] ?? 'uacUnavailable',
                  ),
              action: h(Action, { onClick: reload }, t('refresh')),
            });
          const seconds = challenge
            ? Math.max(
                0,
                Math.round(
                  (Date.parse(challenge.expiresAt) - Date.now()) / 1000,
                ),
              )
            : 0;
          return h(
            'section',
            {
              className: modal
                ? 'auriga-card auriga-login-card'
                : 'auriga-card',
              'aria-label': t('localIdentity'),
            },
            !modal &&
              h(
                'header',
                null,
                h('h3', null, t('localIdentity')),
                h('span', { className: 'auriga-badge' }, t('uacMethod')),
              ),
            !signedIn &&
              diagnosticKey &&
              h('p', { role: 'status' }, t(diagnosticKey)),
            signedIn
              ? h(
                  'div',
                  { className: 'auriga-facts' },
                  h(
                    'div',
                    { className: 'auriga-row' },
                    h(Dot, { tone: 'done' }),
                    h('strong', null, t('uacSignedIn')),
                  ),
                  h(
                    'dl',
                    null,
                    h('dt', null, t('empNo')),
                    h('dd', null, uac.empNo),
                    uac.displayName && h('dt', null, t('identityLabel')),
                    uac.displayName && h('dd', null, uac.displayName),
                    uac.department && h('dt', null, t('department')),
                    uac.department && h('dd', null, uac.department),
                    h('dt', null, t('loginTime')),
                    h(
                      'dd',
                      null,
                      new Date(uac.loginAt).toLocaleString(state.locale),
                    ),
                    h('dt', null, t('revalidation')),
                    h(
                      'dd',
                      null,
                      t(
                        uac.revalidation === 'retrying'
                          ? 'revalidationRetrying'
                          : 'revalidationOk',
                      ),
                    ),
                  ),
                  uac.platformLink === 'mismatch' &&
                    h(
                      'p',
                      { role: 'alert', className: 'auriga-warn' },
                      t('platformMismatch'),
                    ),
                  uac.platformLink === 'matched' &&
                    h('p', null, t('platformMatched')),
                  h('p', null, t('uacLocalHint')),
                  h(
                    'div',
                    { className: 'auriga-actions' },
                    h(
                      Action,
                      {
                        disabled: busy,
                        onClick: () => run('auriga/uac/logout'),
                      },
                      t('uacLogout'),
                    ),
                  ),
                )
              : h(
                  'div',
                  { className: 'auriga-scan' },
                  pending && challenge.qr
                    ? h(
                        'figure',
                        {
                          className: 'auriga-qr',
                          'data-state': challenge.state,
                        },
                        h(
                          'svg',
                          {
                            viewBox: `0 0 ${challenge.qr.size} ${challenge.qr.size}`,
                            width: 168,
                            height: 168,
                            role: 'img',
                            'aria-label': t('uacQr'),
                          },
                          h('rect', {
                            width: challenge.qr.size,
                            height: challenge.qr.size,
                            fill: '#fff',
                          }),
                          h('path', { d: challenge.qr.path, fill: '#000' }),
                        ),
                        challenge.state === 'scanned' &&
                          h(
                            'figcaption',
                            { className: 'auriga-qr-overlay' },
                            t('uac_scanned'),
                          ),
                      )
                    : h(
                        'div',
                        {
                          className: 'auriga-qr auriga-qr-empty',
                          'aria-hidden': true,
                        },
                        h(
                          'span',
                          null,
                          challenge ? t('uac_' + challenge.state) : '',
                        ),
                      ),
                  h(
                    'div',
                    { className: 'auriga-scan-side' },
                    h(
                      'p',
                      {
                        role: 'status',
                        'aria-live': 'polite',
                        className: 'auriga-lead',
                      },
                      challenge
                        ? t('uac_' + challenge.state) +
                            (challenge.failureCode === 'owner_mismatch'
                              ? ' · ' + t('ownerMismatch')
                              : '')
                        : uac.lastOutcome &&
                            uac.lastOutcome.startsWith('revalidation')
                          ? t('uacExpiredRescan')
                          : t('uacPrompt'),
                    ),
                    pending &&
                      h('p', null, t('uacValidity') + ' ' + seconds + 's'),
                    h('p', null, t('uacWarning')),
                    uac.ownerBound && h('p', null, t('ownerBound')),
                    error &&
                      h(
                        'p',
                        { role: 'alert', className: 'auriga-warn' },
                        error,
                      ),
                    h(
                      'div',
                      { className: 'auriga-actions' },
                      pending
                        ? h(
                            Action,
                            {
                              disabled: busy,
                              onClick: () =>
                                run('auriga/uac/cancel', { id: challenge.id }),
                            },
                            t('cancel'),
                          )
                        : h(
                            Action,
                            {
                              primary: true,
                              disabled: busy,
                              onClick: () => run('auriga/uac/start'),
                            },
                            t(challenge ? 'uacRetry' : 'uacStart'),
                          ),
                    ),
                    challenge &&
                      h(
                        'small',
                        { className: 'auriga-ref' },
                        t('reference') +
                          ' ' +
                          challenge.correlationId.slice(0, 8),
                      ),
                  ),
                ),
          );
        }
        const launchLoginFragment = [
          '#auriga=login',
          '#auriga=platform-login',
        ].includes(window.location.hash);
        let loginOverlayAvailable = false;
        const loginRequests = new Set();
        const openPlatformLogin = (action) => {
          if (!loginRequests.size) return false;
          loginRequests.forEach((open) => open(action));
          return true;
        };
        function PlatformLogin() {
          const local = ['127.0.0.1', '[::1]', 'localhost'].includes(
            window.location.hostname,
          );
          const [open, setOpen] = React.useState(false);
          const [settled, setSettled] = React.useState(false);
          const [ownsLogin, setOwnsLogin] = React.useState(false);
          const [request, setRequest] = React.useState(0);
          const [loginTab, setLoginTab] = React.useState('uac');
          const attempted = React.useRef(false);
          const loginAction = React.useRef('inline_login');
          const manual = React.useRef(false);
          const manualPlatform = React.useRef(false);
          const cancellation = React.useRef(null);
          const uacAuthenticated = React.useRef(false);
          const [shell, reloadShell, setShell] = useLocalStatus(
            'auriga/shell/status',
            open && ownsLogin ? 2000 : 15000,
            local &&
              (!settled || (open && ownsLogin) || uacAuthenticated.current),
          );
          const [uac, reloadUac, setUac] = useLocalStatus(
            'auriga/uac/status',
            open ? 2000 : 15000,
            local && (!settled || (open && ownsLogin)),
            true,
          );
          const hasLocalUac = !!uac?.available;
          const hasLocalUacRef = React.useRef(hasLocalUac);
          hasLocalUacRef.current = hasLocalUac;
          const uacSignedIn = hasLocalUac && uac.state === 'authenticated';
          uacAuthenticated.current = uacSignedIn;
          const platformAvailable =
            !!shell?.configured && !!shell?.brokerAvailable;
          const show = (action) => {
            loginAction.current = 'inline_login';
            manual.current = true;
            manualPlatform.current = action === 'platform';
            setLoginTab(
              action !== 'platform' && hasLocalUacRef.current
                ? 'uac'
                : 'platform',
            );
            setSettled(false);
            reloadShell();
            reloadUac();
            setRequest((n) => n + 1);
          };
          const skip = () => {
            attempted.current = true;
            manual.current = false;
            manualPlatform.current = false;
            try {
              window.sessionStorage.setItem('auriga.login.skipped', '1');
            } catch {
              /* The in-memory attempt also suppresses automatic reopening. */
            }
            if (ownsLogin && shell?.csrf)
              cancellation.current = postLocal(
                'auriga/shell/command',
                shell.csrf,
                { action: 'cancel_login' },
              ).catch(() => {});
            setOpen(false);
            setSettled(true);
          };
          React.useEffect(() => {
            loginRequests.add(show);
            const onHash = () => {
              if (window.location.hash !== '#auriga=platform-login') return;
              window.history.replaceState(
                null,
                '',
                window.location.pathname + window.location.search,
              );
              // A tray navigation is automatic: it must respect the session skip.
              setSettled(false);
              reloadShell();
              reloadUac();
            };
            window.addEventListener('hashchange', onHash);
            onHash();
            return () => {
              loginRequests.delete(show);
              window.removeEventListener('hashchange', onHash);
            };
          }, []);
          React.useEffect(() => {
            if (!local || (shell === undefined && uac === undefined)) return;
            if (uac === undefined && !manualPlatform.current) return;
            // Retry startup readiness slowly until the IPC broker appears (when no local UAC).
            setSettled(
              hasLocalUac || !shell?.configured || !!shell?.brokerAvailable,
            );
            const needsPlatform = platformAvailable && !shell?.loggedIn;
            if (
              uacSignedIn &&
              !manualPlatform.current &&
              !manual.current &&
              !(
                shell?.localUacProof &&
                shell?.inlineLogin?.step?.type === 'form'
              )
            ) {
              setOpen(false);
              return;
            }
            if (hasLocalUac) {
              if (uacSignedIn && !needsPlatform) {
                manualPlatform.current = false;
                setOpen(false);
                return;
              }
              if (manualPlatform.current && !needsPlatform) {
                manualPlatform.current = false;
                setOpen(false);
                return;
              }
            } else if (!needsPlatform) {
              if (manual.current && !shell?.loggedIn) {
                manual.current = false;
                setOpen(true);
                return;
              }
              manualPlatform.current = false;
              setOpen(false);
              return;
            }
            let skipped = false;
            try {
              skipped =
                window.sessionStorage.getItem('auriga.login.skipped') === '1';
            } catch {
              /* Storage is optional; attempted still guards this mount. */
            }
            if (manual.current || (!attempted.current && !skipped)) {
              const wasManualPlatform = manualPlatform.current;
              manual.current = false;
              attempted.current = false;
              const targetTab =
                wasManualPlatform || (uacSignedIn && needsPlatform)
                  ? 'platform'
                  : hasLocalUac
                    ? 'uac'
                    : 'platform';
              setLoginTab(targetTab);
              setOpen(true);
            }
          }, [
            shell,
            uac,
            request,
            hasLocalUac,
            uacSignedIn,
            platformAvailable,
          ]);
          React.useEffect(() => {
            if (!open || loginTab !== 'platform' || !platformAvailable) return;
            const controller = new AbortController();
            let live = true,
              release;
            const locks = window.navigator?.locks;
            if (typeof locks?.request !== 'function') return;
            locks
              .request(
                'auriga-platform-login',
                { signal: controller.signal },
                async () => {
                  if (!live) return;
                  // Refresh after obtaining the lock: a previous window may have
                  // authenticated or cancelled while this window was waiting.
                  const current = await fetch(localURL('auriga/shell/status'), {
                    credentials: 'same-origin',
                    cache: 'no-store',
                    redirect: 'error',
                    signal: controller.signal,
                  })
                    .then((response) => (response.ok ? response.json() : null))
                    .catch(() => null);
                  if (!live) return;
                  setShell(current);
                  if (
                    !current?.configured ||
                    !current?.brokerAvailable ||
                    current.loggedIn
                  )
                    return;
                  setOwnsLogin(true);
                  await new Promise((resolve) => {
                    release = resolve;
                  });
                },
              )
              .catch(() => {});
            return () => {
              live = false;
              controller.abort();
              setOwnsLogin(false);
              Promise.resolve(cancellation.current).finally(() => release?.());
            };
          }, [open, loginTab, platformAvailable]);
          React.useEffect(() => {
            if (
              !open ||
              loginTab !== 'platform' ||
              !ownsLogin ||
              attempted.current ||
              !shell?.csrf ||
              !shell.configured ||
              !shell.brokerAvailable ||
              shell.loggedIn
            )
              return;
            attempted.current = true;
            if (shell.inlineLogin || shell.platform === 'authenticating')
              return;
            if (uacSignedIn && !manualPlatform.current) return;
            postLocal('auriga/shell/command', shell.csrf, {
              action: loginAction.current,
            })
              .catch(() => {})
              .finally(reloadShell);
          }, [open, loginTab, ownsLogin, shell, uacSignedIn]);
          if (!open) return null;
          const activeTab =
            hasLocalUac && (loginTab === 'uac' || !platformAvailable)
              ? 'uac'
              : 'platform';
          const showTabs = hasLocalUac && platformAvailable;
          const content = h(
            'div',
            { className: 'auriga-shell auriga-login-content' },
            h(
              'p',
              { className: 'auriga-login-intro' },
              state.locale === 'en'
                ? 'Sign in to use Auriga services. You can also sign in later.'
                : '登录账号以使用 Auriga 服务。也可以稍后登录。',
            ),
            showTabs &&
              (HostSegmentedTabs
                ? h(HostSegmentedTabs, {
                    items: [
                      { value: 'uac', label: t('uacTab') },
                      { value: 'platform', label: t('teamTab') },
                    ],
                    value: activeTab,
                    label:
                      state.locale === 'en' ? 'Sign-in method' : '登录方式',
                    className: 'auriga-login-methods',
                    onChange: (next) => {
                      attempted.current = false;
                      manualPlatform.current = next === 'platform';
                      setLoginTab(next);
                    },
                  })
                : h(
                    'div',
                    {
                      className: 'auriga-login-methods',
                      role: 'tablist',
                      'aria-label':
                        state.locale === 'en' ? 'Sign-in method' : '登录方式',
                    },
                    h(
                      'button',
                      {
                        type: 'button',
                        role: 'tab',
                        'aria-selected': activeTab === 'uac',
                        onClick: () => {
                          manualPlatform.current = false;
                          setLoginTab('uac');
                        },
                      },
                      t('uacTab'),
                    ),
                    h(
                      'button',
                      {
                        type: 'button',
                        role: 'tab',
                        'aria-selected': activeTab === 'platform',
                        onClick: () => {
                          attempted.current = false;
                          manualPlatform.current = true;
                          setLoginTab('platform');
                        },
                      },
                      t('teamTab'),
                    ),
                  )),
            activeTab === 'uac'
              ? h(LocalIdentityCard, {
                  uac,
                  setUac,
                  reload: reloadUac,
                  active: open && activeTab === 'uac',
                  modal: true,
                })
              : ownsLogin || !platformAvailable
                ? h(PlatformCard, {
                    boot: { mode: 'connect' },
                    shell,
                    reloadShell,
                    setShell,
                    modal: true,
                  })
                : h(
                    'p',
                    { role: 'status' },
                    t(
                      typeof window.navigator?.locks?.request === 'function'
                        ? 'loginOtherWindow'
                        : 'loginWindowUnsupported',
                    ),
                  ),
            h(
              'div',
              { className: 'auriga-login-footer' },
              h(
                Action,
                { onClick: skip },
                state.locale === 'en'
                  ? 'Skip, use local DSH'
                  : '\u8df3\u8fc7\uff0c\u4f7f\u7528\u672c\u5730 DSH',
              ),
            ),
          );
          return HostModal
            ? h(
                HostModal,
                {
                  open: true,
                  className: 'auriga-platform-login-dialog',
                  onClose: skip,
                  title:
                    state.locale === 'en'
                      ? 'Sign in to Auriga'
                      : '\u767b\u5f55 Auriga',
                  closeLabel: t('cancel'),
                },
                content,
              )
            : h(
                'dialog',
                {
                  ref: (dialog) => {
                    if (!dialog || dialog.open) return;
                    if (typeof dialog.showModal === 'function')
                      dialog.showModal();
                    else dialog.open = true;
                  },
                  className: 'auriga-login-modal',
                  'aria-modal': true,
                  'aria-label': 'Auriga',
                  onCancel: (e) => {
                    e.preventDefault();
                    skip();
                  },
                },
                content,
              );
        }
        function PlatformCard({
          boot,
          shell,
          reloadShell,
          setShell,
          gotoSettings,
          modal = false,
        }) {
          const [busy, setBusy] = React.useState(false);
          const [outcome, setOutcome] = React.useState('');
          const [inlineFallback, setInlineFallback] = React.useState(false);
          const intent = React.useRef(null);
          const view = platformView(boot, shell);
          const canRetryLocal = shell?.canRetryLocalUac === true;
          const proofFailed = canRetryLocal && !!shell?.loginError;
          const previous = React.useRef(view.kind);
          React.useEffect(() => {
            const before = previous.current;
            previous.current = view.kind;
            if (before !== 'authenticating' || view.kind === 'authenticating')
              return;
            setOutcome(
              view.kind === 'connected' || view.kind === 'degraded'
                ? 'loginDone'
                : view.kind === 'reauth'
                  ? shell?.reason === 'expired'
                    ? 'loginExpired'
                    : ''
                  : view.kind === 'unreachable'
                    ? 'loginUnreachable'
                    : intent.current === 'cancel'
                      ? 'loginCancelled'
                      : 'loginDenied',
            );
            intent.current = null;
          }, [view.kind]);
          const command = (action, input) => {
            intent.current = action === 'cancel_login' ? 'cancel' : action;
            setBusy(true);
            setOutcome('');
            const signOut = action === 'logout' ? ++shellSignOutFence : 0;
            if (signOut) {
              // Drop resident account/market before the remote round-trip so a
              // hung or 503 logout cannot leave the previous identity on screen.
              updateDesktopCatalog({ loggedIn: false }, ++shellReadSequence);
              if (typeof setShell === 'function')
                setShell({
                  available: true,
                  loggedIn: false,
                  connected: false,
                  recovering: false,
                  account: null,
                  platform: 'signed_out',
                  csrf: shell?.csrf,
                  brokerAvailable: shell?.brokerAvailable,
                });
            }
            postLocal('auriga/shell/command', shell?.csrf, {
              action,
              ...(input ? { input } : {}),
            })
              .catch((error) => {
                if (!disposed && error.status !== 409)
                  setOutcome('loginUnreachable');
              })
              .finally(() => {
                if (disposed) return;
                if (signOut && shellSignOutFence === signOut)
                  shellSignOutFence = 0;
                setBusy(false);
                reloadShell();
              });
          };
          const inline = shell?.inlineLogin;
          React.useEffect(() => {
            if (
              (!modal && loginOverlayAvailable && !inlineFallback) ||
              !inline ||
              inline.step.type !== 'qr' ||
              busy
            )
              return;
            const timer = setTimeout(
              () => command('inline_advance', { operation: 'poll' }),
              2000,
            );
            return () => clearTimeout(timer);
          }, [inline, busy, modal, inlineFallback]);
          const localized = (text) => text?.[state.locale] ?? text?.en ?? '';
          const inlineView =
            inline &&
            h(
              'div',
              { className: 'auriga-scan' },
              HostSegmentedTabs && modal
                ? h(HostSegmentedTabs, {
                    items: inline.providers.map((provider) => ({
                      value: provider.id,
                      label: localized(provider.displayName),
                    })),
                    value: inline.providerId,
                    label:
                      state.locale === 'en' ? 'Sign-in method' : '登录方式',
                    className: 'auriga-login-methods',
                    onChange: (providerId) => {
                      if (!busy && providerId !== inline.providerId)
                        command('inline_advance', {
                          operation: 'select',
                          providerId,
                        });
                    },
                  })
                : h(
                    'div',
                    {
                      className: modal
                        ? 'auriga-login-methods'
                        : 'auriga-actions',
                      ...(modal
                        ? {
                            role: 'tablist',
                            'aria-label':
                              state.locale === 'en'
                                ? 'Sign-in method'
                                : '登录方式',
                          }
                        : {}),
                    },
                    ...inline.providers.map((provider) =>
                      h(
                        'button',
                        {
                          key: provider.id,
                          type: 'button',
                          disabled: busy,
                          ...(modal
                            ? {
                                role: 'tab',
                                'aria-selected':
                                  provider.id === inline.providerId,
                              }
                            : {}),
                          onClick: () => {
                            if (provider.id !== inline.providerId)
                              command('inline_advance', {
                                operation: 'select',
                                providerId: provider.id,
                              });
                          },
                        },
                        localized(provider.displayName),
                      ),
                    ),
                  ),
              inline.step.type === 'qr' &&
                inline.qr &&
                h(
                  'figure',
                  { className: 'auriga-qr', 'data-state': inline.step.status },
                  h(
                    'svg',
                    {
                      viewBox: `0 0 ${inline.qr.size} ${inline.qr.size}`,
                      width: modal ? 208 : 168,
                      height: modal ? 208 : 168,
                      role: 'img',
                      'aria-label': t('uacQr'),
                    },
                    h('rect', {
                      width: inline.qr.size,
                      height: inline.qr.size,
                      fill: '#fff',
                    }),
                    h('path', { d: inline.qr.path, fill: '#000' }),
                  ),
                  h(
                    'figcaption',
                    { role: 'status' },
                    t('uac_' + inline.step.status),
                  ),
                ),
              inline.step.type === 'form' &&
                h(
                  'form',
                  {
                    key: inline.providerId + inline.step.formId,
                    onSubmit: (event) => {
                      event.preventDefault();
                      const values = {};
                      for (const field of inline.step.fields) {
                        if (field.kind === 'info') continue;
                        const control = event.currentTarget.elements.namedItem(
                          field.name,
                        );
                        if (control)
                          values[field.name] =
                            field.kind === 'checkbox'
                              ? control.checked
                              : control.value;
                      }
                      event.currentTarget.reset();
                      command('inline_advance', {
                        operation: 'submit',
                        formId: inline.step.formId,
                        values,
                      });
                    },
                  },
                  inline.step.title &&
                    h('h4', null, localized(inline.step.title)),
                  inline.step.error &&
                    h(
                      'p',
                      { role: 'alert' },
                      inline.step.error.code === 'iam.invalid_credentials'
                        ? t('loginBadCredentials')
                        : localized(inline.step.error.message) ||
                            inline.step.error.code,
                    ),
                  ...inline.step.fields.map((field) =>
                    h(
                      'label',
                      { key: field.name },
                      field.kind !== 'hidden' &&
                        (localized(field.label) || field.name),
                      field.kind === 'info'
                        ? h('p', null, localized(field.help))
                        : field.kind === 'select'
                          ? h(
                              'select',
                              {
                                name: field.name,
                                disabled: busy,
                                required: field.required,
                              },
                              ...(field.options ?? []).map((option) =>
                                h(
                                  'option',
                                  { key: option.value, value: option.value },
                                  localized(option.label),
                                ),
                              ),
                            )
                          : h('input', {
                              name: field.name,
                              type: [
                                'password',
                                'checkbox',
                                'hidden',
                                'email',
                                'tel',
                              ].includes(field.kind)
                                ? field.kind
                                : 'text',
                              autoComplete: field.autocomplete,
                              required: field.required,
                              maxLength: field.maxLength,
                              defaultValue:
                                typeof field.value === 'string'
                                  ? field.value
                                  : undefined,
                              disabled: busy,
                            }),
                    ),
                  ),
                  h(
                    'button',
                    { type: 'submit', disabled: busy },
                    localized(inline.step.submitLabel) || t('platformLogin'),
                  ),
                ),
            );
          const account = shell?.account;
          const hint = [
            'source_disabled',
            'admin_disabled',
            'member_disabled',
          ].includes(shell?.reason)
            ? 'accessDisabled'
            : view.kind === 'unreachable' && !shell?.brokerAvailable
              ? 'brokerUnavailable'
              : view.kind === 'degraded' &&
                  shell?.communication === 'unavailable'
                ? 'communicationUnavailable'
                : view.kind;
          return h(
            'section',
            {
              className: modal
                ? 'auriga-card auriga-login-card'
                : 'auriga-card',
              'aria-label': t('platformCard'),
            },
            !modal && h('header', null, h('h3', null, t('platformCard'))),
            h(
              'div',
              { className: 'auriga-row' },
              h(Dot, { tone: view.tone }),
              h(
                'strong',
                { role: 'status' },
                t(
                  proofFailed ? 'loginPlatformFailed' : 'platform_' + view.kind,
                ),
              ),
            ),
            !modal && !proofFailed && h('p', null, t('platformHint_' + hint)),
            shell?.loggedIn &&
              h(
                'dl',
                null,
                account?.displayName && h('dt', null, t('identityLabel')),
                account?.displayName && h('dd', null, account.displayName),
                boot?.connection?.endpointId && h('dt', null, t('endpoint')),
                boot?.connection?.endpointId &&
                  h('dd', null, boot.connection.endpointId),
                h('dt', null, t('received')),
                h('dd', null, String(boot?.connection?.received ?? 0)),
              ),
            view.kind === 'authenticating' &&
              !inline &&
              h(
                'p',
                null,
                t(shell?.localUacProof ? 'loginLinking' : 'loginBrowserHint'),
              ),
            !canRetryLocal &&
              ['signedOut', 'reauth'].includes(view.kind) &&
              h('p', { className: 'auriga-muted' }, t('loginMethods')),
            (modal ||
              !loginOverlayAvailable ||
              inlineFallback ||
              shell?.localUacProof) &&
              inlineView,
            view.kind === 'authenticating' &&
              !inline &&
              shell?.deviceAttempt &&
              h(
                'p',
                { role: 'status', 'aria-live': 'polite' },
                t('loginCompare'),
                ' ',
                h('strong', null, shell.deviceAttempt.userCode),
              ),
            shell?.reason === 'rate_limited' &&
              view.kind === 'unreachable' &&
              h('p', { role: 'status' }, t('loginRateLimited')),
            [
              'iam.uac_proof_subject_required',
              'iam.uac_proof_rejected',
              'iam.uac_proof_provider_required',
            ].includes(shell?.loginError?.code) &&
              h('p', { role: 'alert' }, t('loginProofRequired')),
            shell?.reason === 'platform_rejected' &&
              h('p', { role: 'alert' }, t('loginRejected')),
            shell?.reason === 'login_denied' &&
              h(
                'p',
                { role: 'alert' },
                t(
                  shell?.loginError?.code === 'iam.invalid_credentials'
                    ? 'loginBadCredentials'
                    : 'loginDenied',
                ),
              ),
            shell?.loginError &&
              h(
                'details',
                { className: 'auriga-muted' },
                h('summary', null, t('loginDetails')),
                shell.loginError.code,
                ' / ',
                shell.loginError.correlationId,
              ),
            outcome &&
              h('p', { role: 'status', 'aria-live': 'polite' }, t(outcome)),
            h(
              'div',
              { className: 'auriga-actions' },
              view.kind === 'unconfigured' &&
                typeof gotoSettings === 'function' &&
                h(
                  Action,
                  { primary: true, onClick: gotoSettings },
                  t('configurePlatform'),
                ),
              canRetryLocal &&
                h(
                  Action,
                  {
                    primary: true,
                    disabled: busy || !shell?.csrf,
                    onClick: () => command('retry_local_uac'),
                  },
                  t('retryLocalUac'),
                ),
              ['signedOut', 'reauth'].includes(view.kind) &&
                h(
                  Action,
                  {
                    primary: !canRetryLocal,
                    disabled: busy || !shell?.csrf,
                    onClick: !modal
                      ? () => {
                          if (
                            !openPlatformLogin(
                              canRetryLocal ? 'platform' : 'inline_login',
                            )
                          ) {
                            setInlineFallback(true);
                            command('inline_login');
                          }
                        }
                      : () => command('inline_login'),
                  },
                  t('platformInlineLogin'),
                ),
              view.kind === 'authenticating' &&
                h(
                  Action,
                  { disabled: busy, onClick: () => command('cancel_login') },
                  t('cancel'),
                ),
              ['unreachable', 'recovering', 'degraded', 'connected'].includes(
                view.kind,
              ) &&
                h(
                  Action,
                  {
                    disabled: busy,
                    onClick:
                      view.kind !== 'connected' && shell?.csrf
                        ? () => command('reconnect')
                        : reloadShell,
                  },
                  t(view.kind === 'connected' ? 'refresh' : 'reconnect'),
                ),
              shell?.loggedIn &&
                h(
                  Action,
                  { disabled: busy, onClick: () => command('logout') },
                  t('platformLogout'),
                ),
            ),
          );
        }
        function OverviewHome({
          shell,
          uac,
          desktop,
          pendingCount = 0,
          onNavigate,
          onLogin,
          onRetryLocal,
          phase,
          retry,
        }) {
          const [View, setView] = React.useState(null);
          const [loadFailed, setLoadFailed] = React.useState(false);
          React.useEffect(() => {
            let live = true;
            import(localURL('auriga/shell-ui/overview.js'))
              .then((module) => {
                if (live && !disposed)
                  setView(() => module.createOverviewUI(React));
              })
              .catch(() => {
                if (live && !disposed) setLoadFailed(true);
              });
            return () => {
              live = false;
            };
          }, []);
          if (loadFailed)
            return h(
              'div',
              { className: 'auriga-card', role: 'alert' },
              h('p', null, t('failed')),
              h(
                'div',
                { className: 'auriga-actions' },
                h(
                  Action,
                  {
                    onClick: () => {
                      setLoadFailed(false);
                      setView(null);
                      import(localURL('auriga/shell-ui/overview.js'))
                        .then((module) =>
                          setView(() => module.createOverviewUI(React)),
                        )
                        .catch(() => setLoadFailed(true));
                    },
                  },
                  t('retry'),
                ),
              ),
            );
          if (!View)
            return h('p', { className: 'auriga-muted' }, t('loading'));
          const {
            OverviewPage,
            OverviewWelcome,
            OverviewStatus,
            OverviewNotifications,
            OverviewCapabilities,
            OverviewActivity,
          } = View;
          const localSignedIn =
            uac?.available === true && uac.state === 'authenticated';
          const cloudConnected =
            shell?.loggedIn === true && shell?.connected === true;
          const networkReady = shell?.network?.connected === true;
          const displayName =
            (localSignedIn && (uac.displayName || uac.empNo)) ||
            shell?.account?.displayName ||
            shell?.account?.empNo ||
            '';
          const signedIn = localSignedIn || shell?.loggedIn === true;
          const canRetryLocal =
            localSignedIn &&
            (shell?.canRetryLocalUac === true || shell?.loggedIn !== true);
          const statusItems = [
            {
              id: 'local',
              tone: localSignedIn ? 'success' : 'idle',
              label: t('overviewLocal'),
              detail: localSignedIn
                ? t('overviewStatusOn')
                : t('overviewStatusOff'),
            },
            {
              id: 'cloud',
              tone: cloudConnected
                ? 'success'
                : shell?.loggedIn
                  ? 'warn'
                  : 'idle',
              label: t('overviewCloud'),
              detail: cloudConnected
                ? t('overviewCloudOn')
                : shell?.loggedIn
                  ? t('overviewCloudAuth')
                  : t('overviewCloudOff'),
            },
            {
              id: 'network',
              tone: networkReady ? 'success' : 'idle',
              label: t('overviewNetwork'),
              detail: networkReady
                ? t('overviewNetworkOn')
                : t('overviewNetworkOff'),
            },
          ];
          const notices = [];
          if (desktop?.available && !desktop.required) {
            notices.push({
              id: 'desktop-update',
              title: t('overviewUpdateTitle'),
              detail: desktop.available.version,
              action: h(
                Action,
                {
                  primary: true,
                  onClick: () => onNavigate('settings'),
                },
                t('overviewUpdateAction'),
              ),
            });
          }
          if (pendingCount > 0) {
            notices.push({
              id: 'pending-consent',
              title: t('overviewConsentTitle'),
              detail: pendingCount + t('overviewConsentDetail'),
              action: h(
                Action,
                { onClick: () => onNavigate('inbox') },
                t('overviewConsentAction'),
              ),
            });
          }
          const actions = [];
          if (!signedIn && typeof onLogin === 'function') {
            actions.push(
              h(
                Action,
                {
                  key: 'login',
                  primary: true,
                  onClick: onLogin,
                },
                t('platformLogin'),
              ),
            );
          } else if (canRetryLocal) {
            actions.push(
              h(
                Action,
                {
                  key: 'retry-local',
                  primary: true,
                  disabled: !shell?.csrf,
                  onClick: onRetryLocal,
                },
                t('retryConnect'),
              ),
            );
          }
          const capabilities = [
            {
              id: 'market',
              route: 'market',
              icon: 'Store',
              title: t('tab_market'),
              description: t('cap_market_desc'),
            },
            {
              id: 'mine',
              route: 'mine',
              icon: 'LibraryBig',
              title: t('tab_mine'),
              description: t('cap_mine_desc'),
            },
            {
              id: 'team',
              route: 'team',
              icon: 'UsersRound',
              title: t('tab_team'),
              description: t('cap_team_desc'),
            },
            {
              id: 'inbox',
              route: 'inbox',
              icon: 'Inbox',
              title: t('tab_inbox'),
              description: t('cap_inbox_desc'),
            },
          ];
          return h(
            OverviewPage,
            null,
            h(OverviewWelcome, {
              name: displayName,
              signedIn,
              title: signedIn
                ? t('overviewWelcomeBack') + (displayName || t('identityLabel'))
                : t('overviewWelcome'),
              tagline: t('overviewTagline'),
            }),
            h(OverviewStatus, {
              items: statusItems,
              alert:
                localSignedIn && !cloudConnected
                  ? t('overviewLocalOnly')
                  : null,
              actions: actions.length ? actions : null,
            }),
            h(OverviewNotifications, { items: notices }),
            h(OverviewCapabilities, {
              title: t('overviewExplore'),
              items: capabilities,
              onNavigate,
            }),
            // No formal activity feed API yet — hide without real data.
            h(OverviewActivity, { items: undefined }),
            phase?.status &&
              phase.status !== 'ready' &&
              notice(phase, retry),
          );
        }
        function Main() {
          const [desktop, desktopAction] = useDesktopUpdate();
          const host = useHostState();
          const [phase, retry] = useBootstrap(true);
          const [authRequired, setAuthRequired] = React.useState(false);
          const [tab, setTab] = React.useState('overview');
          const [navExpanded, setNavExpanded] = React.useState(readNavExpanded);
          const [inboxBadge, setInboxBadge] = React.useState(0);
          const ref = React.useRef(null);
          const boot = phase.status === 'ready' ? phase.boot : undefined;
          // Desktop is a loopback DSH host. Start its identity route even
          // when the resource bundle is missing or has failed integrity checks.
          const local = phase.boot
            ? phase.boot.mode !== 'managed'
            : ['127.0.0.1', '[::1]', 'localhost'].includes(
                window.location.hostname,
              );
          const [shell, reloadShell, setShell] = useLocalStatus(
            'auriga/shell/status',
            4000,
            // Managed instances also expose shell status for overview connection state.
            local || phase.boot?.mode === 'managed',
          );
          const [uac, reloadUac, setUac] = useLocalStatus(
            'auriga/uac/status',
            15000,
            local,
            true,
          );
          const platform = platformView(phase.boot, local ? shell : undefined);
          const teamReady = local
            ? shell?.loggedIn === true && shell.connected === true
            : boot?.mode === 'managed' && boot.connection?.connected === true;
          const catalogSession =
            boot?.mode === 'connect' &&
            desktopCatalog?.ownerId === shell?.account?.subject &&
            desktopCatalog?.generation === catalogGenerationOf(shell?.account)
              ? desktopCatalog
              : undefined;
          const resourcesReady =
            !!boot &&
            (boot.mode === 'managed' ||
              (boot.mode === 'connect' &&
                shell?.loggedIn === true &&
                (!shell.account?.subject || !!catalogSession)));
          React.useEffect(() => {
            const el = ref.current;
            if (!el || !boot) return;
            el.context =
              boot.mode === 'connect' && !resourcesReady
                ? undefined
                : {
                    mode: boot.mode,
                    protocol: boot.protocol,
                    locale: host.locale,
                    theme: host.theme,
                    apiBase: '',
                    fetch: boot.fetch,
                    listInstalled,
                    memberResources:
                      boot.mode === 'connect' && boot.memberResourcesAvailable
                        ? memberResources
                        : undefined,
                    resourceIdentity: boot.resourceIdentity,
                    resourceDistributionAvailable:
                      boot.resourceDistributionAvailable,
                    catalogSession,
                  };
          }, [boot, resourcesReady, catalogSession, host.locale, host.theme]);
          React.useEffect(() => {
            const el = ref.current;
            if (!el || !boot) return;
            const onNav = (ev) => navigate(ev.detail?.to);
            const onNotify = (ev) => notify(ev.detail);
            const onAuth = () => {
              setAuthRequired(true);
              if (local) reloadShell();
            };
            el.addEventListener('auriga-navigate', onNav);
            el.addEventListener('auriga-notify', onNotify);
            el.addEventListener('auriga-auth-required', onAuth);
            const cleanup = () => {
              el.removeEventListener('auriga-navigate', onNav);
              el.removeEventListener('auriga-notify', onNotify);
              el.removeEventListener('auriga-auth-required', onAuth);
              el.context = undefined;
            };
            cleanups.push(cleanup);
            return () => {
              cleanup();
              const index = cleanups.indexOf(cleanup);
              if (index >= 0) cleanups.splice(index, 1);
            };
          }, [boot]);
          React.useEffect(() => {
            const el = ref.current;
            if (tab === 'market' || tab === 'mine') el?.showTab?.(tab);
          }, [tab, boot]);
          React.useEffect(() => {
            const root = ref.current?.shadowRoot;
            if (!root || !boot) return;
            // Activity Bar owns Auriga navigation; hide the panel's duplicate chrome.
            const style = document.createElement('style');
            style.textContent =
              'aside.resources > .resource-header,aside.resources > nav[role="tablist"]{display:none}';
            root.append(style);
            return () => style.remove();
          }, [boot]);
          React.useEffect(() => {
            let cancelled = false;
            const refreshBadge = () => {
              Promise.resolve()
                .then(() => consentBridge?.snapshot?.())
                .then((snapshot) => {
                  if (cancelled) return;
                  const count = Array.isArray(snapshot?.pending)
                    ? snapshot.pending.length
                    : 0;
                  setInboxBadge(count);
                })
                .catch(() => {
                  if (!cancelled) setInboxBadge(0);
                });
            };
            refreshBadge();
            const timer = setInterval(refreshBadge, 15000);
            return () => {
              cancelled = true;
              clearInterval(timer);
            };
          }, [tab]);
          const toggleNavExpanded = () => {
            setNavExpanded((current) => {
              const next = !current;
              writeNavExpanded(next);
              return next;
            });
          };
          const gotoOverview = h(
            Action,
            { onClick: () => setTab('overview') },
            t('gotoOverview'),
          );
          const platformBlocked = () =>
            h(Blocked, {
              tone: platform.tone,
              title: t('platform_' + platform.kind),
              detail: t('platformNeeded'),
              action: gotoOverview,
            });
          const panel = (id, ...children) =>
            h(
              'section',
              {
                role: 'tabpanel',
                id: 'auriga-panel-' + id,
                'aria-labelledby': 'auriga-tab-' + id,
                className: 'auriga-tabpanel',
                hidden: tab !== id,
              },
              ...children,
            );
          const resourceTab = tab === 'market' || tab === 'mine';
          const banners = [
            desktop &&
              (desktop.required ||
                desktop.restartRequired ||
                desktop.error ||
                desktop.downloading) &&
              h(
                'section',
                {
                  key: 'desktop-update',
                  role: desktop.required ? 'alert' : 'status',
                  className: 'auriga-shell-status auriga-banner',
                },
                h(
                  'span',
                  null,
                  t(
                    desktop.required
                      ? 'updateRequired'
                      : desktop.restartRequired
                        ? 'updateRestart'
                        : desktop.error
                          ? 'updateFailed'
                          : desktop.downloading
                            ? 'updateDownloading'
                            : 'updateAvailable',
                  ),
                  desktop.available && ' · ' + desktop.available.version,
                ),
                desktop.downloading &&
                  h('progress', {
                    max: desktop.totalBytes,
                    value: desktop.downloadedBytes,
                  }),
                !desktop.restartRequired &&
                  h(
                    'button',
                    {
                      type: 'button',
                      onClick: () =>
                        desktopAction(
                          desktop.downloading ? 'cancel' : 'update',
                        ),
                    },
                    t(desktop.downloading ? 'updateCancel' : 'updateNow'),
                  ),
              ),
            host.toast &&
              h(
                'div',
                {
                  key: 'host-toast',
                  role: 'status',
                  'data-level': host.toast.level,
                  className: 'auriga-banner',
                },
                host.toast.message,
              ),
          ].filter(Boolean);
          return h(
            AurigaLayout,
            {
              tab,
              onTabChange: setTab,
              expanded: navExpanded,
              onToggleExpanded: toggleNavExpanded,
              badges: { inbox: inboxBadge || undefined },
              banners,
            },
            panel(
              'overview',
              h(OverviewHome, {
                shell,
                uac: local ? uac : undefined,
                desktop,
                pendingCount: inboxBadge,
                phase,
                retry,
                onNavigate: setTab,
                onLogin: local
                  ? () => {
                      if (!openPlatformLogin())
                        notify({
                          level: 'warning',
                          message: t('loginEntryUnavailable'),
                        });
                    }
                  : undefined,
                onRetryLocal: local
                  ? () => {
                      if (!shell?.csrf) return;
                      postLocal('auriga/shell/command', shell.csrf, {
                        action: 'retry_local_uac',
                      })
                        .catch(() => {})
                        .finally(() => {
                          reloadShell();
                          reloadUac();
                        });
                    }
                  : undefined,
              }),
            ),
            panel(tab === 'mine' ? 'market' : 'mine'),
            panel(
              tab === 'mine' ? 'mine' : 'market',
              phase.status !== 'ready'
                ? resourceTab && notice(phase, retry)
                : !resourcesReady && resourceTab && platformBlocked(),
              authRequired &&
                h(
                  'div',
                  { role: 'status', className: 'auriga-banner' },
                  h('span', null, t('authRequired')),
                  h(
                    Action,
                    { onClick: () => setAuthRequired(false) },
                    t('dismiss'),
                  ),
                ),
              // Keep the trusted element mounted when switching pages and tabs.
              boot &&
                h(ELEMENT, {
                  ref,
                  tabs: 'host',
                  // React 18 serializes false on custom elements as hidden="false".
                  // Null removes the boolean HTML attribute so the panel is visible.
                  hidden: desktop?.required || !resourcesReady ? true : null,
                }),
            ),
            panel(
              'team',
              teamReady
                ? h(
                    'div',
                    { className: 'auriga-card' },
                    h(
                      'div',
                      { className: 'auriga-row' },
                      h(Dot, { tone: 'done' }),
                      h('strong', null, t('connected')),
                    ),
                    h('p', null, t('connectHint')),
                    h(
                      'ul',
                      { className: 'auriga-tools' },
                      ...[
                        'auriga_list_endpoints',
                        'auriga_open_thread',
                        'auriga_reply_thread',
                        'auriga_read_thread',
                      ].map((name) =>
                        h('li', { key: name }, h('code', null, name)),
                      ),
                    ),
                    h(
                      'div',
                      { className: 'auriga-actions' },
                      h(
                        Action,
                        {
                          primary: true,
                          disabled: desktop?.required,
                          onClick: () => navigate('session:new'),
                        },
                        t('conversation'),
                      ),
                    ),
                  )
                : platformBlocked(),
            ),
            panel('inbox', h(ConsentSection)),
            panel(
              'settings',
              tab === 'settings' &&
                h(Settings, {
                  embedded: true,
                  gotoOverview: () => setTab('overview'),
                }),
            ),
          );
        }
        effect(() => {
          try {
            return ctx.slots.inject('auriga.platform-card', () =>
              ctx.slots.register(
                {
                  name: 'auriga.platform-card',
                  locale: NS,
                },
                PlatformCard,
              ),
            );
          } catch {
            /* Hosts without this seat skip the login-card harness registration. */
          }
        }, 'auriga-shell: platform card harness');
        effect(
          () =>
            ctx.slots.inject('sidebar.panellist', () =>
              ctx.slots.register(
                {
                  name: 'sidebar.panellist',
                  id: PANEL_ID,
                  locale: NS,
                  label: () => t('panel'),
                },
                ({ size, active }) =>
                  h(active ? IconUsersOutlineMedium : IconUsersOutlineRegular, {
                    size,
                    'aria-hidden': true,
                  }),
              ),
            ),
          'auriga-shell: sidebar',
        );
        // Public sidebar brand slots (single, root scope). A dynamically
        // registered occupant shadows the shipped brand; disposal restores it.
        const BrandMark = ({ size = 24 }) =>
          h(
            'svg',
            {
              width: size,
              height: size,
              viewBox: '0 0 64 64',
              fill: 'none',
              'aria-hidden': true,
              className: 'auriga-brand-mark',
            },
            h('rect', { width: 64, height: 64, rx: 16, fill: '#10283b' }),
            // Regular pentagon centred on (32,33), circumradius 21.
            h('path', {
              d: 'M32 12 51.97 26.51 44.34 49.99H19.66L12.03 26.51Z',
              stroke: '#8fb9d6',
              strokeWidth: 3,
              strokeLinejoin: 'round',
            }),
            h('path', {
              d: 'M23.5 44 32 23l8.5 21M26.5 37h11',
              stroke: '#e8f0f7',
              strokeWidth: 3,
              strokeLinecap: 'round',
              strokeLinejoin: 'round',
            }),
            ...[
              [32, 12],
              [51.97, 26.51],
              [44.34, 49.99],
              [19.66, 49.99],
              [12.03, 26.51],
            ].map(([cx, cy]) =>
              h('circle', { key: cx, cx, cy, r: 3, fill: '#8fb9d6' }),
            ),
          );
        const BrandName = () =>
          h(
            'span',
            { className: 'auriga-brand-name' },
            h('span', null, 'Auriga'),
            h('span', { className: 'auriga-brand-suffix' }, 'DSH'),
          );
        effect(
          () =>
            ctx.slots.inject('sidebar.brand.mark', () =>
              ctx.slots.register(
                { name: 'sidebar.brand.mark', priority: -1 },
                BrandMark,
              ),
            ),
          'auriga-shell: brand mark',
        );
        effect(
          () =>
            ctx.slots.inject('sidebar.brand.name', () =>
              ctx.slots.register(
                { name: 'sidebar.brand.name', priority: -1 },
                BrandName,
              ),
            ),
          'auriga-shell: brand name',
        );
        function AurigaAccountLauncher({
          wide = true,
          settingsShortcut,
          openSettings,
        } = {}) {
          const local = ['127.0.0.1', '[::1]', 'localhost'].includes(
            window.location.hostname,
          );
          const [open, setOpen] = React.useState(false);
          const [busy, setBusy] = React.useState(false);
          const [failedAvatar, setFailedAvatar] = React.useState(null);
          const trigger = React.useRef(null);
          const [shell, reloadShell] = useLocalStatus(
            'auriga/shell/status',
            15000,
            local,
          );
          const [uac, reloadUac] = useLocalStatus(
            'auriga/uac/status',
            15000,
            local,
            true,
          );
          const hasLocalUac = !!uac?.available;
          const uacSignedIn =
            hasLocalUac && !uac.reason && uac.state === 'authenticated';
          const platformSignedIn = !!shell?.loggedIn;
          // Local identity owns the account presentation whenever configured.
          // Platform authentication remains an independent second layer.
          const signedIn = hasLocalUac
            ? uacSignedIn
            : uac?.available === false && platformSignedIn;
          const unknown = hasLocalUac
            ? !!uac.reason || !uac.state
            : !uac || !shell;
          const selectedProfile = uacSignedIn
            ? {
                ...(platformSignedIn &&
                uac.platformLink === 'matched' &&
                typeof uac.platformBinding === 'string' &&
                /^[0-9a-f]{64}$/.test(uac.platformBinding) &&
                uac.platformBinding === shell?.account?.profileBinding
                  ? shell.account
                  : {}),
                ...uac,
              }
            : !hasLocalUac
              ? shell?.account
              : undefined;
          const displayName =
            typeof selectedProfile?.displayName === 'string'
              ? selectedProfile.displayName.trim()
              : '';
          let avatarUrl;
          const candidate = selectedProfile?.avatarUrl;
          if (
            typeof candidate === 'string' &&
            candidate.length <= 2048 &&
            /^https?:\/\//.test(candidate) &&
            !/[\s"'<>\\]/.test(candidate) &&
            !Array.from(candidate).some(
              (char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127,
            )
          ) {
            try {
              const parsed = new URL(candidate);
              if (
                parsed.hostname &&
                !parsed.username &&
                !parsed.password &&
                !parsed.hash &&
                parsed.port !== '0'
              )
                avatarUrl = candidate;
            } catch {
              /* Optional presentation data must not interrupt login. */
            }
          }
          const empNo =
            signedIn && typeof selectedProfile?.empNo === 'string'
              ? selectedProfile.empNo.trim()
              : '';
          const primaryLabel = signedIn
            ? displayName || empNo || t('launcherSignedIn')
            : t(unknown ? 'launcherUnknown' : 'launcherSignIn');
          const localLabel = t(
            uacSignedIn
              ? 'launcherLocal'
              : !uac || uac.reason
                ? 'launcherLocalUnknown'
                : hasLocalUac
                  ? 'launcherLocalSignedOut'
                  : 'launcherLocalUnconfigured',
          );
          const secondaryLabel = signedIn
            ? displayName && empNo && !displayName.includes(empNo)
              ? empNo
              : ''
            : [
                localLabel,
                t(
                  !shell
                    ? 'launcherPlatformUnknown'
                    : platformSignedIn
                      ? shell.connected
                        ? 'launcherPlatform'
                        : 'launcherPlatformSignedIn'
                      : 'launcherPlatformOffline',
                ),
              ].join('，');
          const avatarGlyph = signedIn
            ? displayName
              ? Array.from(displayName)[0].toUpperCase()
              : empNo
                ? empNo.slice(-2)
                : 'A'
            : null;
          const beginSignIn = () => {
            setOpen(false);
            if (!openPlatformLogin()) {
              notify({ level: 'warning', message: t('loginEntryUnavailable') });
              ctx.get?.('layout')?.selectPanel?.(PANEL_ID);
            }
          };
          const signOut = () => {
            if (busy) return;
            setBusy(true);
            setOpen(false);
            const tasks = [];
            if (uacSignedIn && uac?.csrf)
              tasks.push(
                postLocal('auriga/uac/logout', uac.csrf, {}).catch(() => {}),
              );
            if (platformSignedIn && shell?.csrf)
              tasks.push(
                postLocal('auriga/shell/command', shell.csrf, {
                  action: 'logout',
                }).catch(() => {}),
              );
            Promise.all(tasks).finally(() => {
              if (disposed) return;
              setBusy(false);
              reloadUac();
              reloadShell();
            });
          };
          const avatarNode = h(
            'span',
            {
              className: 'auriga-launcher-avatar',
              'data-signed-out': !signedIn && !unknown ? 'true' : undefined,
              'aria-hidden': true,
            },
            signedIn
              ? avatarUrl && failedAvatar !== avatarUrl
                ? h('img', {
                    src: avatarUrl,
                    alt: '',
                    referrerPolicy: 'no-referrer',
                    draggable: false,
                    width: 24,
                    height: 24,
                    style: {
                      width: '100%',
                      height: '100%',
                      objectFit: 'cover',
                    },
                    onError: () => setFailedAvatar(avatarUrl),
                  })
                : avatarGlyph
              : IconUserOutlineMedium
                ? h(IconUserOutlineMedium, { size: 14 })
                : IconEllipsisOutlineMedium
                  ? h(IconEllipsisOutlineMedium, { size: 14 })
                  : '?',
          );
          const triggerButton = h(
            'button',
            {
              ref: trigger,
              type: 'button',
              className: 'auriga-launcher-trigger',
              'data-collapsed': !wide ? 'true' : undefined,
              'data-signed-out': !signedIn && !unknown ? 'true' : undefined,
              'aria-label': t('launcherMenu'),
              'aria-haspopup': 'menu',
              'aria-expanded': open,
              title: secondaryLabel
                ? primaryLabel + ' (' + secondaryLabel + ')'
                : primaryLabel,
              onClick: () => setOpen((value) => !value),
            },
            avatarNode,
            wide &&
              h(
                'span',
                { className: 'auriga-launcher-text' },
                h('span', { className: 'auriga-launcher-name' }, primaryLabel),
                secondaryLabel &&
                  h(
                    'span',
                    { className: 'auriga-launcher-sub' },
                    secondaryLabel,
                  ),
              ),
          );
          const items = [
            {
              id: 'settings',
              label: t('launcherSettings'),
              ...(IconSettingsOutlineMedium
                ? { icon: h(IconSettingsOutlineMedium, { size: 16 }) }
                : {}),
              ...(settingsShortcut !== undefined
                ? { shortcut: settingsShortcut }
                : {}),
            },
            ...(!signedIn
              ? [
                  {
                    id: 'signin',
                    label: t('launcherSignIn'),
                    ...(IconUserOutlineMedium
                      ? { icon: h(IconUserOutlineMedium, { size: 16 }) }
                      : {}),
                  },
                ]
              : []),
            ...(uacSignedIn || platformSignedIn
              ? [
                  {
                    id: 'signout',
                    label: t('launcherSignOut'),
                    disabled: busy,
                  },
                ]
              : []),
          ];
          const onSelect = (id) => {
            if (id === 'settings') {
              setOpen(false);
              trigger.current?.focus();
              openSettings?.();
            } else if (id === 'signin') {
              beginSignIn();
            } else if (id === 'signout') {
              signOut();
            }
          };
          if (HostMenu)
            return h(
              'div',
              { className: 'auriga-launcher-root' },
              h(HostMenu, {
                open,
                side: 'top',
                portal: true,
                autoFocus: true,
                className: 'auriga-launcher-anchor',
                anchor: triggerButton,
                items,
                onClose: () => setOpen(false),
                onSelect,
              }),
            );
          return h(
            'div',
            { className: 'auriga-launcher-root' },
            triggerButton,
            open &&
              h(
                'div',
                {
                  className: 'auriga-launcher-fallback-menu',
                  role: 'menu',
                  'aria-label': t('launcherMenu'),
                },
                ...items.map((item) =>
                  h(
                    'button',
                    {
                      key: item.id,
                      type: 'button',
                      role: 'menuitem',
                      className: 'auriga-launcher-fallback-item',
                      disabled: !!item.disabled,
                      onClick: () => onSelect(item.id),
                    },
                    item.label,
                  ),
                ),
              ),
          );
        }
        effect(() => {
          try {
            return ctx.slots.inject('settings.launcher', () =>
              ctx.slots.register(
                {
                  name: 'settings.launcher',
                  priority: -1,
                  locale: NS,
                },
                AurigaAccountLauncher,
              ),
            );
          } catch {
            /* Older hosts without settings.launcher keep their default footer. */
          }
        }, 'auriga-shell: account launcher');
        effect(() => {
          try {
            return ctx.slots.inject('shell.overlay', () => {
              loginOverlayAvailable = true;
              const remove = ctx.slots.register(
                {
                  name: 'shell.overlay',
                  id: 'auriga-platform-login',
                  locale: NS,
                },
                PlatformLogin,
              );
              return () => {
                loginOverlayAvailable = false;
                remove?.();
              };
            });
          } catch {
            // Hosts without the public overlay seat retain the overview login.
            loginOverlayAvailable = false;
          }
        }, 'auriga-shell: platform login');
        let launchSelected = false;
        effect(
          () =>
            ctx.slots.inject('main', () => {
              const unregister = ctx.slots.register(
                { name: 'main', key: PANEL_ID, locale: NS },
                Main,
              );
              // The browser retains this fragment through native admission.
              // It only selects an existing panel after the host loads it.
              const navigate =
                launchLoginFragment ||
                ['#auriga=login', '#auriga=platform-login'].includes(
                  window.location.hash,
                )
                  ? ctx.inject?.(['layout'], (scope) => {
                      if (!launchSelected && !disposed) {
                        scope.layout.selectPanel(PANEL_ID);
                        launchSelected = true;
                        window.history.replaceState(
                          null,
                          '',
                          window.location.pathname + window.location.search,
                        );
                      }
                    })
                  : undefined;
              return () => {
                if (typeof navigate === 'function') navigate();
                if (typeof unregister === 'function') unregister();
              };
            }),
          'auriga-shell: main',
        );
        effect(
          () =>
            ctx.inject?.(
              ['remote', 'remote.llm', 'remote.settings'],
              (scope) => {
                const remote = scope.remote;
                if (
                  typeof remote?.llm?.discoverModels !== 'function' ||
                  typeof remote?.settings?.describe !== 'function' ||
                  typeof remote?.settings?.mutate !== 'function'
                )
                  return;
                let active = true;
                scope.effect(
                  () => () => {
                    active = false;
                  },
                  'auriga-shell: model sync lifetime',
                );
                function ModelsSync({ provider }) {
                  const [view, setView] = React.useState({});
                  const operation = React.useRef({ live: true, busy: false });
                  React.useEffect(() => {
                    operation.current.live = true;
                    return () => {
                      operation.current.live = false;
                    };
                  }, []);
                  if (provider.provider !== 'auriga-managed') return null;
                  const alive = () =>
                    operation.current.live && active && !disposed;
                  const run = async (action) => {
                    if (!alive() || operation.current.busy) return;
                    operation.current.busy = true;
                    setView((old) => ({
                      ...old,
                      busy: true,
                      message: undefined,
                    }));
                    try {
                      await action();
                    } catch {
                      if (alive()) setView({ message: 'syncFailed' });
                    } finally {
                      operation.current.busy = false;
                      if (alive()) setView((old) => ({ ...old, busy: false }));
                    }
                  };
                  const preview = () =>
                    run(async () => {
                      const found = await remote.llm.discoverModels(
                        'llm-pi-ai',
                        {
                          provider: 'auriga-managed',
                        },
                      );
                      if (!alive()) return;
                      if (
                        !found.ok ||
                        !Array.isArray(found.value) ||
                        found.value.length > 256
                      )
                        throw failure();
                      const directory = new Map();
                      for (const item of found.value) {
                        if (
                          typeof item?.id !== 'string' ||
                          directory.has(item.id) ||
                          ['contextWindow', 'maxTokens'].some(
                            (key) =>
                              item[key] !== undefined &&
                              (!Number.isSafeInteger(item[key]) ||
                                item[key] < 1),
                          )
                        )
                          throw failure();
                        directory.set(item.id, item);
                      }
                      const answer = await remote.settings.describe();
                      if (!alive()) return;
                      const namespace =
                        answer.ok &&
                        answer.value?.namespaces?.find(
                          (row) => row.ns === 'llm-pi-ai',
                        );
                      const models =
                        namespace?.value?.providers?.['auriga-managed']?.models;
                      if (
                        !Number.isSafeInteger(namespace?.revision) ||
                        !Array.isArray(models) ||
                        models.length > 256
                      )
                        throw failure();
                      const changes = [];
                      const operations = [];
                      models.forEach((model, index) => {
                        const candidate = directory.get(model.id);
                        if (!candidate) return;
                        const updated = { ...model };
                        for (const key of ['contextWindow', 'maxTokens'])
                          if (
                            candidate[key] !== undefined &&
                            candidate[key] !== model[key]
                          ) {
                            updated[key] = candidate[key];
                            operations.push({
                              op: 'set',
                              path: [
                                'providers',
                                'auriga-managed',
                                'models',
                                String(index),
                                key,
                              ],
                              value: candidate[key],
                            });
                          }
                        if (
                          updated.contextWindow !== model.contextWindow ||
                          updated.maxTokens !== model.maxTokens
                        )
                          changes.push({ before: model, after: updated });
                      });
                      setView(
                        changes.length
                          ? {
                              preview: {
                                revision: namespace.revision,
                                operations,
                                changes,
                              },
                            }
                          : { message: 'syncCurrent' },
                      );
                    });
                  const apply = () =>
                    run(async () => {
                      if (!view.preview) return;
                      const answer = await remote.settings.mutate(
                        'llm-pi-ai',
                        view.preview.operations,
                        view.preview.revision,
                      );
                      if (!alive()) return;
                      setView({
                        message: answer.ok
                          ? 'syncDone'
                          : answer.error?.code === 'settings/conflict' ||
                              answer.error?.code === 'SETTINGS_CONFLICT'
                            ? 'syncConflict'
                            : 'syncFailed',
                      });
                    });
                  const capacity = (value) =>
                    value === undefined ? t('syncUnknown') : String(value);
                  return h(
                    'section',
                    {
                      className: 'auriga-shell auriga-model-sync',
                      'aria-label': t('syncModels'),
                    },
                    h('p', null, t('syncHint')),
                    h(
                      'button',
                      { type: 'button', disabled: view.busy, onClick: preview },
                      t('syncModels'),
                    ),
                    view.preview &&
                      h(
                        'div',
                        null,
                        h('p', null, t('syncPreview')),
                        h(
                          'ul',
                          null,
                          ...view.preview.changes.map(({ before, after }) =>
                            h(
                              'li',
                              { key: before.id },
                              before.name || before.id,
                              ' · ',
                              t('syncContext'),
                              ': ',
                              capacity(before.contextWindow),
                              ' → ',
                              capacity(after.contextWindow),
                              ' · ',
                              t('syncOutput'),
                              ': ',
                              capacity(before.maxTokens),
                              ' → ',
                              capacity(after.maxTokens),
                            ),
                          ),
                        ),
                        h(
                          'div',
                          { className: 'auriga-setting-actions' },
                          h(
                            'button',
                            {
                              type: 'button',
                              disabled: view.busy,
                              onClick: apply,
                            },
                            t('syncApply'),
                          ),
                          h(
                            'button',
                            {
                              type: 'button',
                              disabled: view.busy,
                              onClick: () => setView({}),
                            },
                            t('syncCancel'),
                          ),
                        ),
                      ),
                    view.message &&
                      h(
                        'p',
                        { role: 'status', 'aria-live': 'polite' },
                        t(view.message),
                      ),
                  );
                }
                scope.effect(
                  () =>
                    ctx.slots.inject('settings.models.provider-card', () =>
                      ctx.slots.register(
                        {
                          name: 'settings.models.provider-card',
                          key: 'llm-pi-ai',
                          locale: NS,
                        },
                        ModelsSync,
                      ),
                    ),
                  'auriga-shell: native model sync',
                );
              },
            ),
          'auriga-shell: model sync services',
        );
        const dispose = () => {
          if (disposed) return;
          disposed = true;
          if (desktopCatalog) verifiedPanel()?.clearCatalog?.(desktopCatalog);
          controllers.forEach((controller) => controller.abort());
          controllers.clear();
          scripts.forEach((el) => el.cancel());
          scripts.clear();
          timers.forEach(clearTimeout);
          timers.clear();
          cleanups.reverse().forEach((cleanup) => cleanup());
          cleanups.length = 0;
          listeners.clear();
          state.toast = undefined;
        };
        ctx.effect?.(() => dispose, 'auriga-shell: lifecycle');
        return dispose;
      },
    };
  },
});
