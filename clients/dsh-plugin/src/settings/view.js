/* global AbortSignal */
/** Native React settings. Status is supplied by the authenticated runtime. */
import {
  createSettingsUI,
  readSettingsCategory,
  writeSettingsCategory,
} from './ui.js';

const messages = {
  zh: {
    navLabel: '设置分类',
    navGeneral: '常规',
    navMessaging: '消息与安全',
    navAdvanced: '高级',
    navAccount: '账户',
    navPlatform: '平台连接',
    navProxy: '网络代理',
    navDesktop: '桌面更新',
    navNetwork: '消息网络',
    navConsent: '同意策略',
    navSkills: '私有技能',
    navDiagnostics: '诊断信息',
    diagnosticsHint: '用于排查连接与运行状态的只读信息',
    copy: '复制',
    copyAll: '复制全部',
    copied: '已复制',
    copyFailed: '复制失败，请重试',
    currentVersion: '当前版本',
    desktop: '桌面更新',
    desktopProduct: 'Auriga DSH',
    updatePreferences: '更新偏好',
    requiredUpdate:
      '此版本已低于最低可用版本，请完成更新并重启后继续使用平台功能。',
    downloadProgress: '正在下载',
    updateFailed: '更新未完成，旧版和用户数据已保留，可重试。',
    cancelUpdate: '取消下载（保留进度）',
    updatePaused: '下载已暂停，可继续下载',
    updateChannel: '更新渠道',
    updateChannelHint: '切换渠道后将立即检查更新',
    stable: '正式',
    beta: '测试',
    desktopConfirmationRequired:
      '更新地址尚未明确确认。请使用平台地址、IP 模式及地址确认参数重新安装 Desktop。',
    update: '更新',
    checkUpdate: '检查更新',
    checkingUpdate: '正在检查…',
    upToDate: '已是最新版本',
    updateAvailable: '发现新版本',
    awaitingInstall: '已下载，等待安装',
    autoCheck: '自动检查更新',
    autoCheckHint: '有可用更新时在客户端内提示',
    updateRestart: '下载后请关闭 Auriga DSH，退出时安装，下次启动使用新版。',
    platform: '平台',
    aurigaCloud: 'Auriga 云平台',
    address: '云平台地址',
    save: '保存',
    test: '测试连接',
    saved: '已保存',
    reachable: '平台可达',
    unconfigured: '未配置平台',
    configure: '填写平台地址',
    login: '登录',
    logout: '退出登录',
    account: '账户',
    accountStatus: '账户状态',
    identityInfo: '身份信息',
    accountActions: '账户操作',
    fullName: '姓名',
    empNo: '工号',
    memberId: '成员 ID',
    department: '部门',
    device: '设备名',
    loginTime: '登录时间',
    connect: '连接',
    signedOut: '未登录',
    local: '本地功能仍可用，云平台相关功能暂不可用',
    localLogin: '本地登录',
    localSignedIn: '已登录',
    localSignedOut: '未登录',
    localUnknown: '状态未知',
    cloudConnection: '云平台连接',
    platformConnection: '平台连接',
    platformConnected: '已连接',
    platformSignedIn: '已认证，未连接',
    platformDisconnected: '未连接',
    reconnect: '重新连接',
    reauthenticate: '重新认证',
    retryConnect: '重试连接',
    loginEntryUnavailable: '登录弹层暂未就绪，请稍后重试。',
    managed: '系统托管',
    managedExpired: '实例会话已失效，请从门户重新进入',
    managedVerifying: '正在验证实例会话',
    memberLoading: '正在读取成员资料',
    networkTitle: '消息网络',
    controlFile: '公有控制包',
    controlImport: '导入',
    controlPick: '选择 JSON 控制包…',
    controlClear: '清除已选文件',
    controlSaved: '控制已保存；联网后自动提交确认。',
    controlReceipt: '下载确认回执',
    controlMissing: '安装身份未配置，请联系管理员。',
    networkReady: '就绪',
    networkPending: '未就绪',
    network: '总部不可达',
    recovering: '总部不可达，联网后自动恢复',
    unauthorized: '未登录',
    unconfiguredNetwork: '未配置消息网络',
    joining: '正在连接',
    model: '模型',
    modelReady: '中转站可用',
    modelMissing: '中转站未就绪',
    modelManual: '在原生模型设置中获取并保存目录',
    refresh: '刷新状态',
    diagnostics: '诊断',
    protocol: '协议版本',
    version: '插件版本',
    source: '界面来源',
    endpoint: '本机端点',
    received: '已收消息',
    reason: '原因',
    receive: '接收消息',
    receiveHint: '开启后，此会话可接收成员消息。',
    receiveEmpty: '暂无已登记会话',
    receivePending: '设置待确认',
    receiveRetry: '重试',
    receiveSearch: '搜索会话',
    receiveEnabledCount: '已开启',
    unavailable: '状态服务不可用',
    auth: 'DSH 主人认证已失效',
    forbidden: 'DSH 拒绝访问',
    invalidState: '状态响应格式无效',
    statusHttp: '状态接口返回 HTTP ',
    timeout: '状态读取超时',
    loadNetwork: '无法连接本机状态服务',
    busy: '正在处理…',
    pending: '平台连接未生效',
    invalidSettings: '设置无效',
    invalidOrigin: '请输入纯平台地址',
    httpsRequired: '请使用 HTTPS',
    invalidPlatform: '此地址不是 Auriga DSH 平台',
    http: '平台返回 HTTP ',
    tools: '平台工具',
    notProvided: '未提供',
    expired: '平台登录已过期',
    retry: '重试',
    proxyTitle: '网络代理',
    proxyHint:
      '默认忽略操作系统代理，所有网络请求、自动升级与工具子进程均受此处控制。',
    proxySourceBundled: '当前使用安装包内置默认配置',
    proxySourceUser: '当前使用自定义配置',
    proxySourceBuiltin: '当前使用默认直连配置',
    proxyDegraded: '本地代理配置无效，已回退默认配置。',
    proxyMode: '代理模式',
    proxyModeDirect: '直连（不使用系统代理）',
    proxyModeCustom: '自定义代理',
    proxyGlobal: '全局代理地址',
    proxyGlobalPlaceholder: 'http://127.0.0.1:7890',
    proxyGlobalHint: '留空则仅按域名规则代理',
    proxyNoProxy: '全局直连（NoProxy）',
    proxyNoProxyPlaceholder: '*.corp.local, 192.0.2.0/24',
    proxyBuiltinNoProxy: '已内置强制直连：',
    proxyRules: '按域名分流规则',
    proxyRulesEmpty: '暂无分流规则',
    proxyRulePattern: '域名 / 通配 / CIDR',
    proxyRuleAction: '策略',
    proxyRuleProxy: '走代理',
    proxyRuleDirect: '直连',
    proxyRuleTarget: '代理地址',
    proxyRuleAdd: '新增规则',
    proxyRuleUp: '上移',
    proxyRuleDown: '下移',
    proxyRuleDelete: '删除',
    proxyReset: '恢复默认',
    proxyResetDone: '已恢复默认代理配置',
    proxyUnsaved: '有未保存的修改',
    proxyTestTitle: '网络测试',
    proxyTestHint:
      '使用当前已生效的代理配置检测规则命中与连通性。未保存的修改不会参与测试。',
    proxyTestTarget: '测试目标',
    proxyTestPlaceholder: 'https://example.com 或域名',
    proxyTestRun: '开始测试',
    proxyTestRunning: '测试中…',
    proxyTestReachable: '可达',
    proxyTestUnreachable: '不可达',
    proxyTestMatched: '命中',
    proxyTestVia: '访问方式',
    invalidProxyUrl: '代理地址无效（仅支持 http:// 或 https://）',
    invalidPattern: '域名或 CIDR 规则格式无效',
    invalidTarget: '请输入有效的测试域名或 URL',
    proxy_loop: '代理地址不能指向客户端本地分流端口',
    proxy_unreachable: '代理不可达',
    proxy_auth_required: '代理需要身份验证 (407)',
    tls_error: 'TLS 证书校验失败',
  },
  en: {
    navLabel: 'Settings categories',
    navGeneral: 'General',
    navMessaging: 'Messaging & security',
    navAdvanced: 'Advanced',
    navAccount: 'Account',
    navPlatform: 'Platform',
    navProxy: 'Network proxy',
    navDesktop: 'Desktop updates',
    navNetwork: 'Message network',
    navConsent: 'Consent policy',
    navSkills: 'Private skills',
    navDiagnostics: 'Diagnostics',
    diagnosticsHint:
      'Read-only details for connection and runtime troubleshooting',
    copy: 'Copy',
    copyAll: 'Copy all',
    copied: 'Copied',
    copyFailed: 'Copy failed. Please try again.',
    currentVersion: 'Current version',
    desktop: 'Desktop updates',
    desktopProduct: 'Auriga DSH',
    updatePreferences: 'Update preferences',
    requiredUpdate:
      'This version is no longer supported. Update and restart to continue using platform features.',
    downloadProgress: 'Downloading',
    updateFailed:
      'Update incomplete. Your previous version and data are preserved. Retry to continue.',
    cancelUpdate: 'Cancel download (keep progress)',
    updatePaused: 'Download paused; resume when ready',
    updateChannel: 'Update channel',
    updateChannelHint: 'Changing channel checks for updates immediately',
    stable: 'Stable',
    beta: 'Beta',
    desktopConfirmationRequired:
      'The update address needs confirmation. Reinstall Desktop with the platform address, IP mode and address confirmation parameters.',
    update: 'Update',
    checkUpdate: 'Check for updates',
    checkingUpdate: 'Checking…',
    upToDate: 'You are up to date',
    updateAvailable: 'Update available',
    awaitingInstall: 'Downloaded — waiting to install',
    autoCheck: 'Check for updates automatically',
    autoCheckHint: 'Notify in the client when an update is available',
    updateRestart:
      'After download, close Auriga DSH to install. Reopen to use the new version.',
    platform: 'Platform',
    aurigaCloud: 'Auriga Cloud',
    address: 'Cloud platform address',
    save: 'Save',
    test: 'Test connection',
    saved: 'Saved',
    reachable: 'Platform reachable',
    unconfigured: 'Platform not configured',
    configure: 'Enter platform address',
    login: 'Sign in',
    logout: 'Sign out',
    account: 'Account',
    accountStatus: 'Account status',
    identityInfo: 'Profile',
    accountActions: 'Account actions',
    fullName: 'Name',
    empNo: 'Employee no.',
    memberId: 'Member ID',
    department: 'Department',
    device: 'Device name',
    loginTime: 'Sign-in time',
    connect: 'Connect',
    signedOut: 'Signed out',
    local:
      'Local features remain available; cloud features are temporarily unavailable',
    localLogin: 'Local sign-in',
    localSignedIn: 'Signed in',
    localSignedOut: 'Signed out',
    localUnknown: 'Status unknown',
    cloudConnection: 'Cloud connection',
    platformConnection: 'Platform connection',
    platformConnected: 'Connected',
    platformSignedIn: 'Authenticated, not connected',
    platformDisconnected: 'Disconnected',
    reconnect: 'Reconnect',
    reauthenticate: 'Reauthenticate',
    retryConnect: 'Retry connection',
    loginEntryUnavailable:
      'Sign-in overlay is not ready. Please try again shortly.',
    managed: 'System managed',
    managedExpired: 'Instance session has expired. Re-enter from the portal.',
    managedVerifying: 'Verifying instance session',
    memberLoading: 'Loading member profile',
    networkTitle: 'Message network',
    controlFile: 'Public control package',
    controlImport: 'Import',
    controlPick: 'Choose a JSON control package…',
    controlClear: 'Clear selected file',
    controlSaved: 'Control saved. Confirmation will be sent when online.',
    controlReceipt: 'Download confirmation receipt',
    controlMissing:
      'Installation identity is not configured. Contact your administrator.',
    networkReady: 'Ready',
    networkPending: 'Not ready',
    network: 'Platform unreachable',
    recovering: 'Platform unreachable; reconnects automatically',
    unauthorized: 'Signed out',
    unconfiguredNetwork: 'Message network not configured',
    joining: 'Connecting',
    model: 'Models',
    modelReady: 'Relay available',
    modelManual: 'Fetch and save the catalog in native Models settings',
    modelMissing: 'Relay not ready',
    refresh: 'Refresh status',
    diagnostics: 'Diagnostics',
    protocol: 'Protocol version',
    version: 'Plugin version',
    source: 'UI source',
    endpoint: 'Local endpoint',
    received: 'Received messages',
    reason: 'Reason',
    receive: 'Receive messages',
    receiveHint: 'Allow member messages to this session.',
    receiveEmpty: 'No registered sessions',
    receivePending: 'Awaiting confirmation',
    receiveRetry: 'Retry',
    receiveSearch: 'Search sessions',
    receiveEnabledCount: 'Enabled',
    unavailable: 'Status service unavailable',
    auth: 'DSH owner authentication expired',
    forbidden: 'DSH denied access',
    invalidState: 'Invalid status response',
    statusHttp: 'Status endpoint returned HTTP ',
    timeout: 'Status read timed out',
    loadNetwork: 'Cannot reach local status service',
    busy: 'Working…',
    pending: 'Platform connection not applied',
    invalidSettings: 'Invalid settings',
    invalidOrigin: 'Enter a platform origin',
    httpsRequired: 'HTTPS required',
    invalidPlatform: 'Not an Auriga DSH platform',
    http: 'Platform returned HTTP ',
    tools: 'Platform tools',
    notProvided: 'Not supplied',
    expired: 'Platform sign-in expired',
    retry: 'Retry',
    proxyTitle: 'Network proxy',
    proxyHint:
      'System proxy is ignored by default. All requests, auto-updates, and tool subprocesses follow this configuration.',
    proxySourceBundled: 'Using installer bundled default',
    proxySourceUser: 'Using custom configuration',
    proxySourceBuiltin: 'Using built-in direct mode',
    proxyDegraded:
      'Saved proxy configuration was invalid; fell back to default.',
    proxyMode: 'Mode',
    proxyModeDirect: 'Direct (ignore system proxy)',
    proxyModeCustom: 'Custom proxy',
    proxyGlobal: 'Global proxy URL',
    proxyGlobalPlaceholder: 'http://127.0.0.1:7890',
    proxyGlobalHint: 'Leave empty to use domain rules only',
    proxyNoProxy: 'Global NoProxy',
    proxyNoProxyPlaceholder: '*.corp.local, 192.0.2.0/24',
    proxyBuiltinNoProxy: 'Always direct: ',
    proxyRules: 'Per-domain rules',
    proxyRulesEmpty: 'No routing rules yet',
    proxyRulePattern: 'Domain / Wildcard / CIDR',
    proxyRuleAction: 'Action',
    proxyRuleProxy: 'Proxy',
    proxyRuleDirect: 'Direct',
    proxyRuleTarget: 'Proxy URL',
    proxyRuleAdd: 'Add rule',
    proxyRuleUp: 'Up',
    proxyRuleDown: 'Down',
    proxyRuleDelete: 'Remove',
    proxyReset: 'Reset to default',
    proxyResetDone: 'Proxy reset to default',
    proxyUnsaved: 'Unsaved changes',
    proxyTestTitle: 'Network test',
    proxyTestHint:
      'Tests rule matching and connectivity against the currently applied proxy configuration. Unsaved edits are not used.',
    proxyTestTarget: 'Test target',
    proxyTestPlaceholder: 'https://example.com or hostname',
    proxyTestRun: 'Start test',
    proxyTestRunning: 'Testing…',
    proxyTestReachable: 'Reachable',
    proxyTestUnreachable: 'Unreachable',
    proxyTestMatched: 'Matched',
    proxyTestVia: 'Via',
    invalidProxyUrl: 'Invalid proxy URL (http:// or https:// only)',
    invalidPattern: 'Invalid domain or CIDR pattern',
    invalidTarget: 'Enter a valid target URL or hostname',
    proxy_loop: 'Proxy URL cannot point to the local router port',
    proxy_unreachable: 'Proxy unreachable',
    proxy_auth_required: 'Proxy authentication required (407)',
    tls_error: 'TLS verification failed',
  },
};

/** @param {any} React @param {{localURL:(path:string)=>string,fetcher?:typeof fetch,navigate:(url:string)=>void,openLogin?:()=>boolean,subscribeIdentity?:(listener:()=>void)=>()=>void,identityChanged?:()=>void}} ports */
export function createSettingsView(
  React,
  {
    localURL,
    fetcher = fetch,
    navigate: _navigate,
    openLogin,
    subscribeIdentity,
    identityChanged,
  },
) {
  const h = React.createElement;
  const ui = createSettingsUI(React);
  const {
    SettingsLayout,
    SettingsNavigation,
    SettingsPanel,
    SettingsSection,
    SettingRow,
    SettingSwitch,
    SettingSelect,
    SettingInput,
    SettingFilePicker,
    SettingAction,
    SettingIconButton,
    StatusIndicator,
    SettingStatus,
    SettingReadonly,
    AccountCard,
    PlatformCard,
    PlatformAddressField,
    SettingsCard,
    DesktopSummaryCard,
  } = ui;

  /** Real session display name from messaging snapshot fields only. */
  function sessionDisplayName(/** @type {any} */ session) {
    for (const key of ['title', 'name', 'label', 'sessionId']) {
      const value = session?.[key];
      if (typeof value === 'string' && value.trim()) return value.trim();
    }
    if (typeof session?.local === 'string' && session.local.trim())
      return session.local.trim();
    return '';
  }

  function sessionEnabled(/** @type {any} */ session) {
    if (typeof session?.enabled === 'boolean') return session.enabled;
    if (typeof session?.receiveEnabled === 'boolean')
      return session.receiveEnabled;
    return false;
  }
  const useRef = (/** @type {any} */ initial) =>
    typeof React.useRef === 'function'
      ? React.useRef(initial)
      : React.useState({ current: initial })[0];
  function useReceiveSettings(
    /** @type {{t:typeof messages.zh,account:any,revision:number}} */ {
      t,
      account,
      revision,
    },
  ) {
    const [sessions, setSessions] = React.useState(null);
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState('');
    const [query, setQuery] = React.useState('');
    const [refresh, reload] = React.useReducer(
      (/** @type {number} */ n) => n + 1,
      0,
    );
    React.useEffect(() => {
      let live = true;
      if (account?.loggedIn)
        void fetcher(localURL('auriga/shell/messaging/snapshot'), {
          credentials: 'same-origin',
          cache: 'no-store',
        })
          .then(async (response) => {
            if (!response.ok) throw new Error('unavailable');
            const value = await response.json();
            if (!Array.isArray(value.sessions)) throw new Error('unavailable');
            if (live) {
              setSessions(value.sessions);
              setError('');
            }
          })
          .catch(() => {
            if (live) setError(t.unavailable);
          });
      return () => {
        live = false;
      };
    }, [account, revision, refresh, t]);
    const writeReceive = async (
      /** @type {any} */ session,
      /** @type {boolean} */ enabled,
    ) => {
      setBusy(true);
      setError('');
      try {
        const response = await fetcher(
          localURL('auriga/shell/messaging/receive'),
          {
            method: 'POST',
            credentials: 'same-origin',
            redirect: 'error',
            signal: AbortSignal.timeout(15000),
            headers: {
              'content-type': 'application/json',
              ...(account.csrf ? { 'x-auriga-csrf': account.csrf } : {}),
            },
            body: JSON.stringify({ local: session.local, enabled }),
          },
        );
        if (!response.ok) throw new Error('unavailable');
      } catch {
        setError(t.receivePending);
      } finally {
        setBusy(false);
        reload();
      }
    };

    const list = sessions ?? [];
    const needle = query.trim().toLowerCase();
    const visible = needle
      ? list.filter((/** @type {any} */ session) => {
          const hay = [
            sessionDisplayName(session),
            session.local,
            session.address,
          ]
            .filter((v) => typeof v === 'string' && v)
            .join(' ')
            .toLowerCase();
          return hay.includes(needle);
        })
      : list;
    const enabledCount = list.filter((/** @type {any} */ s) =>
      sessionEnabled(s),
    ).length;
    const showTools = list.length > 8;

    return h(
      SettingsCard,
      {
        icon: 'MessagesSquare',
        title: t.receive,
        description: t.receiveHint,
      },
      showTools &&
        h(
          'div',
          { className: 'auriga-receive-toolbar' },
          h(SettingInput, {
            className: 'auriga-receive-search',
            'aria-label': t.receiveSearch,
            placeholder: t.receiveSearch,
            value: query,
            disabled: busy,
            onChange: (/** @type {any} */ e) => setQuery(e.target.value),
          }),
          h(
            'span',
            { className: 'auriga-receive-count' },
            `${t.receiveEnabledCount} ${enabledCount}/${list.length}`,
          ),
        ),
      list.length === 0 &&
        h('p', { className: 'auriga-settings-card-empty' }, t.receiveEmpty),
      list.length > 0 &&
        h(
          'div',
          { className: 'auriga-receive-list' },
          ...visible.map((/** @type {any} */ session) => {
            const name = sessionDisplayName(session);
            const address =
              typeof session.address === 'string' && session.address
                ? session.address
                : '';
            const description = [
              address,
              session.pending ? t.receivePending : '',
            ]
              .filter(Boolean)
              .join(' · ');
            return h(SettingRow, {
              key: session.local,
              icon: 'MessageSquare',
              label: name,
              description: description || undefined,
              control: h(
                'div',
                { className: 'auriga-setting-row-actions' },
                h(SettingSwitch, {
                  'aria-label': name || String(session.local ?? ''),
                  checked: sessionEnabled(session),
                  disabled: busy || !account?.csrf || session.pending,
                  onChange: (/** @type {boolean} */ enabled) =>
                    writeReceive(session, enabled),
                }),
                session.pending &&
                  h(
                    SettingAction,
                    {
                      compact: true,
                      disabled: busy || !account?.csrf,
                      onClick: () =>
                        writeReceive(session, session.pendingEnabled),
                    },
                    t.receiveRetry,
                  ),
              ),
            });
          }),
        ),
      error && h(SettingStatus, { tone: 'error' }, error),
    );
  }

  async function call(
    /** @type {string} */ path,
    /** @type {any} */ body = undefined,
    /** @type {{ timeoutMs?: number | null, csrf?: string }} */ options = {},
  ) {
    const response = await fetcher(localURL(path), {
      method: body === undefined ? 'GET' : 'POST',
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error',
      signal:
        options.timeoutMs === null
          ? undefined
          : AbortSignal.timeout(options.timeoutMs ?? 15000),
      headers:
        body === undefined
          ? {}
          : {
              'content-type': 'application/json',
              ...(options.csrf ? { 'x-auriga-csrf': options.csrf } : {}),
              ...(path.startsWith('auriga/resources/catalog/')
                ? { 'x-auriga-catalog': '1' }
                : { 'X-Auriga-Settings': '1' }),
            },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) {
      let reason = '';
      if (response.status === 400) {
        try {
          const errBody = await response.json();
          if (typeof errBody?.reason === 'string') reason = errBody.reason;
        } catch {
          /* ignore non-JSON error body */
        }
      }
      throw new Error(
        response.status === 401
          ? 'auth'
          : response.status === 403
            ? 'forbidden'
            : reason || 'statusHttp:' + response.status,
      );
    }
    try {
      const value = await response.json();
      if (
        body !== undefined &&
        (path.startsWith('auriga/uac/') || path === 'auriga/shell/command')
      )
        identityChanged?.();
      return value;
    } catch {
      throw new Error('invalidState');
    }
  }
  function NetworkProxyView(
    /** @type {{t:typeof messages.zh,proxy:any,busy:boolean,onSave:(payload:any)=>Promise<boolean>,onReset:()=>Promise<boolean>,onTest:(url:string)=>Promise<any>}} */ {
      t,
      proxy,
      busy,
      onSave,
      onReset,
      onTest,
    },
  ) {
    const [mode, setMode] = React.useState(proxy.mode ?? 'direct');
    const [globalProxy, setGlobalProxy] = React.useState(
      proxy.globalProxy ?? '',
    );
    const [noProxyText, setNoProxyText] = React.useState(
      (proxy.noProxy ?? []).join(', '),
    );
    const [rules, setRules] = React.useState(
      (proxy.rules ?? []).map((/** @type {any} */ r) => ({ ...r })),
    );
    const [dirty, setDirty] = React.useState(false);
    const [testUrl, setTestUrl] = React.useState('');
    const [testResult, setTestResult] = React.useState(null);
    const [testing, setTesting] = React.useState(false);
    const proxyFingerprint = JSON.stringify({
      mode: proxy.mode ?? 'direct',
      globalProxy: proxy.globalProxy ?? '',
      noProxy: proxy.noProxy ?? [],
      rules: proxy.rules ?? [],
      source: proxy.source ?? '',
    });
    const lastSyncedRef = useRef(proxyFingerprint);
    React.useEffect(() => {
      if (dirty || proxyFingerprint === lastSyncedRef.current) return;
      lastSyncedRef.current = proxyFingerprint;
      setMode(proxy.mode ?? 'direct');
      setGlobalProxy(proxy.globalProxy ?? '');
      setNoProxyText((proxy.noProxy ?? []).join(', '));
      setRules((proxy.rules ?? []).map((/** @type {any} */ r) => ({ ...r })));
    }, [proxyFingerprint, dirty, proxy]);

    const updateRule = (
      /** @type {number} */ idx,
      /** @type {Record<string, any>} */ patch,
    ) => {
      setDirty(true);
      setRules((/** @type {any[]} */ prev) =>
        prev.map((item, i) => (i === idx ? { ...item, ...patch } : item)),
      );
    };
    const moveRule = (
      /** @type {number} */ idx,
      /** @type {number} */ delta,
    ) => {
      setDirty(true);
      setRules((/** @type {any[]} */ prev) => {
        const next = [...prev];
        const target = idx + delta;
        if (target < 0 || target >= next.length) return prev;
        [next[idx], next[target]] = [next[target], next[idx]];
        return next;
      });
    };
    const removeRule = (/** @type {number} */ idx) => {
      setDirty(true);
      setRules((/** @type {any[]} */ prev) => prev.filter((_, i) => i !== idx));
    };
    const addRule = () => {
      setDirty(true);
      setRules((/** @type {any[]} */ prev) => [
        ...prev,
        {
          id: 'rule-' + Date.now() + '-' + (prev.length + 1),
          enabled: true,
          pattern: '',
          action: 'proxy',
          proxy: '',
        },
      ]);
    };

    const buildPayload = () => ({
      schemaVersion: 1,
      mode,
      globalProxy: globalProxy.trim(),
      noProxy: noProxyText
        .split(/[\n,]/)
        .map((/** @type {string} */ s) => s.trim())
        .filter(Boolean),
      rules: rules.map((/** @type {any} */ r, /** @type {number} */ idx) => ({
        id: r.id || `rule-${idx + 1}`,
        enabled: r.enabled !== false,
        pattern: String(r.pattern ?? '').trim(),
        action: r.action === 'direct' ? 'direct' : 'proxy',
        proxy: r.action === 'direct' ? '' : String(r.proxy ?? '').trim(),
      })),
    });

    const sourceLabel =
      proxy.source === 'user'
        ? t.proxySourceUser
        : proxy.source === 'bundled'
          ? t.proxySourceBundled
          : t.proxySourceBuiltin;
    const canReset = proxy.source === 'user' || proxy.hasBundledDefault;
    const PlusIcon = ui.icons.Plus;

    const testStatus = testing
      ? { tone: 'processing', label: t.proxyTestRunning }
      : testResult
        ? testResult.reachable
          ? {
              tone: 'success',
              label: `${t.proxyTestReachable}${
                testResult.latencyMs != null
                  ? ` (${testResult.latencyMs}ms)`
                  : ''
              }`,
            }
          : {
              tone: 'error',
              label: `${t.proxyTestUnreachable}${
                testResult.reason
                  ? `: ${
                      t[/** @type {keyof typeof t} */ (testResult.reason)] ??
                      testResult.reason
                    }`
                  : ''
              }`,
            }
        : null;

    const testDetail = testResult
      ? [
          testResult.matchedBy
            ? `${t.proxyTestMatched}: ${testResult.matchedBy}`
            : null,
          `${t.proxyTestVia}: ${
            testResult.action === 'proxy'
              ? testResult.proxy || t.proxyRuleProxy
              : t.proxyRuleDirect
          }`,
        ]
          .filter(Boolean)
          .join(' · ')
      : '';

    return h(
      React.Fragment,
      null,
      h(
        SettingsCard,
        {
          icon: 'Globe',
          title: t.proxyTitle,
          description: t.proxyHint,
          status: h(StatusIndicator, {
            tone: proxy.degraded ? 'warn' : 'idle',
            label: sourceLabel,
          }),
          notice: proxy.degraded
            ? h(SettingStatus, { tone: 'warn' }, t.proxyDegraded)
            : null,
          footer: dirty ? h('span', { role: 'status' }, t.proxyUnsaved) : null,
          actions: [
            canReset
              ? h(
                  SettingAction,
                  {
                    key: 'reset',
                    disabled: busy,
                    onClick: async () => {
                      const reset = await onReset();
                      if (reset) {
                        lastSyncedRef.current = '';
                        setDirty(false);
                      }
                    },
                  },
                  t.proxyReset,
                )
              : null,
            h(
              SettingAction,
              {
                key: 'save',
                primary: true,
                disabled: busy || !dirty,
                onClick: async () => {
                  const saved = await onSave(buildPayload());
                  if (saved) {
                    lastSyncedRef.current = '';
                    setDirty(false);
                  }
                },
              },
              t.save,
            ),
          ],
        },
        h(SettingRow, {
          label: t.proxyMode,
          control: h(SettingSelect, {
            'aria-label': t.proxyMode,
            value: mode,
            disabled: busy,
            options: [
              { value: 'direct', label: t.proxyModeDirect },
              { value: 'proxy', label: t.proxyModeCustom },
            ],
            onChange: (/** @type {string} */ next) => {
              setDirty(true);
              setMode(next);
            },
          }),
        }),
        mode === 'proxy' &&
          h(
            React.Fragment,
            null,
            h(
              'div',
              { className: 'auriga-settings-card-group' },
              h(
                'div',
                { className: 'auriga-settings-card-group-label' },
                t.proxyGlobal,
              ),
              h(SettingInput, {
                'aria-label': t.proxyGlobal,
                value: globalProxy,
                placeholder: t.proxyGlobalPlaceholder,
                disabled: busy,
                onChange: (/** @type {any} */ e) => {
                  setDirty(true);
                  setGlobalProxy(e.target.value);
                },
              }),
              h(
                'div',
                { className: 'auriga-settings-card-group-hint' },
                t.proxyGlobalHint,
              ),
            ),
            h(
              'div',
              { className: 'auriga-settings-card-group' },
              h(
                'div',
                { className: 'auriga-settings-card-group-label' },
                t.proxyNoProxy,
              ),
              h(SettingInput, {
                multiline: true,
                rows: 2,
                'aria-label': t.proxyNoProxy,
                value: noProxyText,
                placeholder: t.proxyNoProxyPlaceholder,
                disabled: busy,
                onChange: (/** @type {any} */ e) => {
                  setDirty(true);
                  setNoProxyText(e.target.value);
                },
              }),
              h(
                'div',
                { className: 'auriga-settings-card-group-hint' },
                t.proxyBuiltinNoProxy +
                  (
                    proxy.builtinNoProxy ?? ['localhost', '127.0.0.1', '::1']
                  ).join(', '),
              ),
            ),
            h(
              'div',
              { className: 'auriga-settings-card-group-head' },
              h(
                'div',
                { className: 'auriga-settings-card-group-label' },
                t.proxyRules,
              ),
              h(
                SettingAction,
                {
                  compact: true,
                  quiet: true,
                  disabled: busy,
                  onClick: addRule,
                },
                PlusIcon
                  ? h(PlusIcon, {
                      size: 14,
                      style: { marginInlineEnd: 4 },
                    })
                  : null,
                t.proxyRuleAdd,
              ),
            ),
            rules.length === 0
              ? h(
                  'p',
                  { className: 'auriga-settings-card-empty' },
                  t.proxyRulesEmpty,
                )
              : h(
                  'div',
                  { className: 'auriga-proxy-rules' },
                  ...rules.map(
                    (/** @type {any} */ rule, /** @type {number} */ idx) =>
                      h(
                        'div',
                        {
                          key: rule.id ?? idx,
                          className: 'auriga-proxy-rule',
                        },
                        h(SettingSwitch, {
                          'aria-label': t.proxyRulePattern,
                          checked: rule.enabled !== false,
                          disabled: busy,
                          onChange: (/** @type {boolean} */ enabled) =>
                            updateRule(idx, { enabled }),
                        }),
                        h(SettingInput, {
                          'aria-label': t.proxyRulePattern,
                          placeholder: t.proxyRulePattern,
                          value: rule.pattern ?? '',
                          disabled: busy,
                          onChange: (/** @type {any} */ e) =>
                            updateRule(idx, { pattern: e.target.value }),
                        }),
                        h(SettingSelect, {
                          'aria-label': t.proxyRuleAction,
                          value: rule.action === 'direct' ? 'direct' : 'proxy',
                          disabled: busy,
                          options: [
                            { value: 'proxy', label: t.proxyRuleProxy },
                            { value: 'direct', label: t.proxyRuleDirect },
                          ],
                          onChange: (/** @type {string} */ action) =>
                            updateRule(idx, { action }),
                        }),
                        h(SettingInput, {
                          'aria-label': t.proxyRuleTarget,
                          placeholder: t.proxyRuleTarget,
                          value:
                            rule.action === 'direct' ? '' : (rule.proxy ?? ''),
                          disabled: busy || rule.action === 'direct',
                          onChange: (/** @type {any} */ e) =>
                            updateRule(idx, { proxy: e.target.value }),
                        }),
                        h(
                          SettingAction,
                          {
                            compact: true,
                            quiet: true,
                            disabled: busy || idx === 0,
                            onClick: () => moveRule(idx, -1),
                          },
                          t.proxyRuleUp,
                        ),
                        h(
                          SettingAction,
                          {
                            compact: true,
                            quiet: true,
                            disabled: busy || idx === rules.length - 1,
                            onClick: () => moveRule(idx, 1),
                          },
                          t.proxyRuleDown,
                        ),
                        h(
                          SettingAction,
                          {
                            compact: true,
                            quiet: true,
                            disabled: busy,
                            onClick: () => removeRule(idx),
                          },
                          t.proxyRuleDelete,
                        ),
                      ),
                  ),
                ),
          ),
      ),
      h(
        SettingsCard,
        {
          icon: 'Activity',
          title: t.proxyTestTitle,
          description: t.proxyTestHint,
        },
        h(
          'div',
          { className: 'auriga-proxy-test-row' },
          h(SettingInput, {
            'aria-label': t.proxyTestTarget,
            placeholder: t.proxyTestPlaceholder,
            value: testUrl,
            disabled: busy || testing,
            onChange: (/** @type {any} */ e) => setTestUrl(e.target.value),
          }),
          h(
            SettingAction,
            {
              primary: true,
              disabled: busy || testing || !testUrl.trim(),
              onClick: async () => {
                setTesting(true);
                setTestResult(null);
                try {
                  const res = await onTest(testUrl.trim());
                  if (res) setTestResult(res);
                } finally {
                  setTesting(false);
                }
              },
            },
            t.proxyTestRun,
          ),
        ),
        (testing || testResult) &&
          h(
            'div',
            { className: 'auriga-proxy-test-result' },
            testStatus
              ? h(StatusIndicator, {
                  tone: testStatus.tone,
                  label: testStatus.label,
                })
              : null,
            testDetail
              ? h(
                  'p',
                  { className: 'auriga-proxy-test-result-detail' },
                  testDetail,
                )
              : null,
          ),
      ),
    );
  }

  function DesktopUpdateView(
    /** @type {{t:typeof messages.zh,desktop:any,busy:boolean,currentVersion?:string,onCheck:()=>Promise<void>,onUpdate:()=>Promise<void>,onCancel:()=>Promise<void>,onToggleEnabled:(enabled:boolean)=>Promise<void>,onChangeChannel:(channel:string)=>Promise<void>}} */ {
      t,
      desktop,
      busy,
      currentVersion,
      onCheck,
      onUpdate,
      onCancel,
      onToggleEnabled,
      onChangeChannel,
    },
  ) {
    const [checkedFresh, setCheckedFresh] = React.useState(false);
    const [checkingLocal, setCheckingLocal] = React.useState(false);
    const availableVersion =
      typeof desktop.available?.version === 'string'
        ? desktop.available.version
        : typeof desktop.available === 'string'
          ? desktop.available
          : '';
    const downloading = !!desktop.downloading;
    const restartRequired = !!desktop.restartRequired;
    const checking =
      checkingLocal ||
      (!!desktop.busy && !downloading && !restartRequired && !availableVersion);
    const hasUpdate =
      !!availableVersion && !restartRequired && !desktop.busy && !downloading;
    const upToDate =
      checkedFresh &&
      !availableVersion &&
      !restartRequired &&
      !desktop.error &&
      !downloading &&
      !checking &&
      desktop.phase !== 'cancelled';

    React.useEffect(() => {
      if (availableVersion || desktop.error || restartRequired)
        setCheckedFresh(false);
    }, [availableVersion, desktop.error, restartRequired]);

    const runCheck = async () => {
      setCheckingLocal(true);
      try {
        await onCheck();
        setCheckedFresh(true);
      } finally {
        setCheckingLocal(false);
      }
    };

    /** @type {any} */
    let primaryAction = null;
    if (!desktop.confirmationRequired) {
      if (downloading || restartRequired) {
        primaryAction = null;
      } else if (hasUpdate) {
        primaryAction = h(
          SettingAction,
          {
            primary: true,
            disabled: busy,
            onClick: () => onUpdate(),
          },
          t.update,
        );
      } else if (checking) {
        primaryAction = h(
          SettingAction,
          { primary: true, disabled: true },
          t.checkingUpdate,
        );
      } else {
        primaryAction = h(
          SettingAction,
          {
            primary: true,
            disabled: busy,
            onClick: () => runCheck(),
          },
          t.checkUpdate,
        );
      }
    }

    const versionLabel = currentVersion
      ? `${t.currentVersion} v${currentVersion}`
      : null;

    return h(
      React.Fragment,
      null,
      h(
        DesktopSummaryCard,
        {
          title: t.desktopProduct,
          version: versionLabel,
          action: primaryAction,
          'aria-label': t.desktop,
        },
        desktop.confirmationRequired &&
          h(SettingStatus, { tone: 'error' }, t.desktopConfirmationRequired),
        desktop.required &&
          h(SettingStatus, { tone: 'error' }, t.requiredUpdate),
        checking &&
          h(StatusIndicator, {
            tone: 'processing',
            label: t.checkingUpdate,
          }),
        upToDate &&
          h(StatusIndicator, {
            tone: 'success',
            label: t.upToDate,
          }),
        hasUpdate &&
          h(StatusIndicator, {
            tone: 'warn',
            label: availableVersion
              ? `${t.updateAvailable} v${availableVersion}`
              : t.updateAvailable,
          }),
        downloading &&
          h(
            'div',
            { className: 'auriga-setting-progress', role: 'status' },
            h(
              'div',
              { className: 'auriga-desktop-progress-meta' },
              h(StatusIndicator, {
                tone: 'processing',
                label:
                  t.downloadProgress +
                  ' ' +
                  (desktop.downloadedBytes / 1048576).toFixed(1) +
                  ' / ' +
                  (desktop.totalBytes / 1048576).toFixed(1) +
                  ' MiB',
              }),
              h(
                SettingAction,
                {
                  compact: true,
                  disabled: busy,
                  onClick: () => onCancel(),
                },
                t.cancelUpdate,
              ),
            ),
            h('progress', {
              max: desktop.totalBytes,
              value: desktop.downloadedBytes,
            }),
          ),
        desktop.phase === 'cancelled' &&
          h(StatusIndicator, { tone: 'idle', label: t.updatePaused }),
        desktop.error && h(SettingStatus, { tone: 'error' }, t.updateFailed),
        restartRequired &&
          h(StatusIndicator, {
            tone: 'success',
            label: t.awaitingInstall,
          }),
        (availableVersion || downloading || restartRequired) &&
          h(SettingStatus, null, t.updateRestart),
      ),
      h(
        SettingsCard,
        {
          icon: 'Settings2',
          title: t.updatePreferences,
        },
        h(SettingRow, {
          label: t.autoCheck,
          description: t.autoCheckHint,
          control: h(SettingSwitch, {
            'aria-label': t.autoCheck,
            checked: !!desktop.enabled,
            disabled: busy || desktop.confirmationRequired || desktop.required,
            onChange: (/** @type {boolean} */ enabled) =>
              onToggleEnabled(enabled),
          }),
        }),
        h(SettingRow, {
          label: t.updateChannel,
          description: t.updateChannelHint,
          control: h(SettingSelect, {
            'aria-label': t.updateChannel,
            value: desktop.channel ?? 'stable',
            disabled:
              busy || downloading || desktop.required || restartRequired,
            options: [
              { value: 'stable', label: t.stable },
              { value: 'beta', label: t.beta },
            ],
            onChange: (/** @type {string} */ channel) =>
              onChangeChannel(channel),
          }),
        }),
      ),
    );
  }

  function PrivateSkillsView(/** @type {{locale:string}} */ { locale }) {
    const zh = locale?.startsWith('zh');
    const t = zh
      ? {
          title: '私有技能',
          hint: '内容仅保存在当前 DSH。修改、关闭和卸载后请新建会话。导入可粘贴原生 SKILL.md；不允许 YAML 别名或标签。',
          refresh: '刷新',
          create: '新建／导入',
          edit: '打开',
          save: '保存修改',
          disable: '关闭',
          enable: '启用',
          remove: '卸载并保留本地副本',
          freeze: '冻结本地快照',
          frozen: '本地快照已冻结；尚未上传或送审',
          conflict: '操作失败，请刷新检查内容是否已被修改或同名技能已存在。',
          writeLock:
            '私有写锁仍存在。若重启后仍不能写入，请先关闭此 DSH 的全部进程，确认没有写操作，再用文件管理器把下列私有锁目录移到备份位置后重启。不要处理 guardian 的共享锁：',
          disabled: '已关闭',
          unavailable: '内容格式不支持，请使用原生文件工具编辑',
          source: 'SKILL.md 内容',
        }
      : {
          title: 'Private skills',
          hint: 'Stored only in this DSH. Start a new session after editing, disabling or removing. Paste native SKILL.md to import. YAML aliases and tags are not allowed.',
          refresh: 'Refresh',
          create: 'Create / import',
          edit: 'Open',
          save: 'Save changes',
          disable: 'Disable',
          enable: 'Enable',
          remove: 'Remove and keep a local copy',
          freeze: 'Freeze local snapshot',
          frozen: 'Local snapshot frozen; not uploaded or submitted',
          conflict:
            'Operation failed. Refresh to check for edits or a name conflict.',
          writeLock:
            'A private write lock remains. If writes still fail after restart, close all processes for this DSH, confirm no writer remains, move this private lock directory to a backup location and restart. Do not touch the guardian shared lock:',
          disabled: 'Disabled',
          unavailable: 'Unsupported content; edit with native file tools',
          source: 'SKILL.md content',
        };
    const [items, setItems] = React.useState([]);
    const [frozen, setFrozen] = React.useState(null);
    const [targets, setTargets] = React.useState([]);
    const [submission, setSubmission] = React.useState({
      version: '1.0.0',
      license: '',
      author: '',
      reason: '',
      tools: '',
      outboundHosts: '',
      dataAccess: '',
      confirmed: false,
      packageId: '',
    });
    const [selected, setSelected] = React.useState(null);
    const [source, setSource] = React.useState(
      '---\nname: my-skill\ndescription: "Describe this skill"\n---\n\n',
    );
    const [notice, setNotice] = React.useState('');
    const [busy, setBusy] = React.useState(false);
    const refresh = async () => {
      const result = await call('auriga/settings/skills');
      if (!Array.isArray(result.items)) throw new Error('invalidState');
      setItems(result.items);
      if (result.writeLock)
        setNotice(t.writeLock + ' ' + result.writeLock.path);
    };
    React.useEffect(() => {
      let live = true;
      call('auriga/settings/skills')
        .then((value) => {
          if (!Array.isArray(value.items)) throw new Error('invalidState');
          if (live) {
            setItems(value.items);
            if (value.writeLock)
              setNotice(t.writeLock + ' ' + value.writeLock.path);
          }
        })
        .catch(() => {
          if (live) setNotice(t.conflict);
        });
      return () => {
        live = false;
      };
    }, []);
    const run = async (/** @type {()=>Promise<any>} */ work) => {
      setBusy(true);
      setNotice('');
      try {
        await work();
        await refresh();
      } catch {
        setNotice(t.conflict);
        try {
          await refresh();
        } catch {
          /* Preserve the original failure. */
        }
      } finally {
        setBusy(false);
      }
    };
    const change = (/** @type {any} */ item, /** @type {string} */ action) =>
      run(async () => {
        const result = await call('auriga/settings/skills', {
          action,
          name: item.name,
          enabled: item.enabled,
          revision: item.revision,
        });
        if (action === 'freeze') {
          const preview = await call('auriga/settings/skills', {
            action: 'preview',
            snapshotId: result.snapshotId,
          });
          setFrozen({
            ...preview,
            snapshotId: result.snapshotId,
            key: globalThis.crypto.randomUUID(),
          });
          setSubmission({ ...submission, confirmed: false, packageId: '' });
          setTargets([]);
          try {
            const value = await call(
              'auriga/resources/catalog/submission-targets',
              { snapshotId: result.snapshotId },
            );
            if (Array.isArray(value.items)) setTargets(value.items);
          } catch {
            /* The local snapshot remains available while signed out or offline. */
          }
          setNotice(t.frozen + ' · ' + result.snapshotId);
        } else setSelected(null);
      });
    const button = (
      /** @type {string} */ label,
      /** @type {()=>any} */ onClick,
      disabled = false,
    ) =>
      h(
        'button',
        { type: 'button', disabled: busy || disabled, onClick },
        label,
      );
    return h(
      'section',
      { 'aria-label': t.title },
      h('h3', null, t.title),
      h('p', null, t.hint),
      button(t.refresh, () => run(refresh)),
      frozen &&
        h(
          'section',
          { 'aria-label': zh ? '冻结送审预览' : 'Frozen submission preview' },
          h('h4', null, zh ? '冻结送审预览' : 'Frozen submission preview'),
          h(
            'p',
            null,
            zh
              ? '以下文件为冻结副本，后续私有编辑不会改变送审内容。命令、工具、外连和数据访问权限均须声明并由平台审核。'
              : 'These frozen files remain independent of subsequent private edits. Declare commands, tools, outbound hosts and data access for platform review.',
          ),
          ...Object.entries(frozen.files).map(([path, content]) =>
            h(
              'details',
              { key: path },
              h('summary', null, path),
              h('pre', null, String(content)),
            ),
          ),
          h(
            'label',
            null,
            zh ? '共享目标' : 'Shared target',
            h(
              'select',
              {
                value: submission.packageId,
                disabled: busy,
                onChange: (/** @type {any} */ event) => {
                  setSubmission({
                    ...submission,
                    packageId: event.target.value,
                    confirmed: false,
                  });
                  setFrozen({ ...frozen, key: globalThis.crypto.randomUUID() });
                },
              },
              h(
                'option',
                { value: '' },
                zh ? '新建共享技能' : 'Create a shared skill',
              ),
              ...targets.map((/** @type {any} */ target) =>
                h(
                  'option',
                  { key: target.id, value: target.id },
                  (zh
                    ? '更新已有共享技能：'
                    : 'Update existing shared skill: ') + target.name,
                ),
              ),
            ),
          ),
          button(zh ? '刷新共享目标' : 'Refresh shared targets', () =>
            run(async () => {
              const value = await call(
                'auriga/resources/catalog/submission-targets',
                { snapshotId: frozen.snapshotId },
              );
              setTargets(value.items);
            }),
          ),
          ...[
            ['version', '版本', 'Version'],
            ['license', '许可证', 'License'],
            ['author', '作者显示名', 'Author display name'],
            ['reason', '送审说明', 'Submission reason'],
            ['tools', '工具声明（逗号分隔）', 'Tools (comma separated)'],
            [
              'outboundHosts',
              '外连域名（逗号分隔）',
              'Outbound hosts (comma separated)',
            ],
            [
              'dataAccess',
              '数据访问（workspace_read / workspace_write / user_selected_files）',
              'Data access (workspace_read / workspace_write / user_selected_files)',
            ],
          ].map(([key, cn, en]) =>
            h(
              'label',
              { key, style: { display: 'block' } },
              zh ? cn : en,
              h('input', {
                value: submission[key],
                disabled: busy,
                onChange: (/** @type {any} */ event) => {
                  setSubmission({
                    ...submission,
                    [key]: event.target.value,
                    confirmed: false,
                  });
                  setFrozen({ ...frozen, key: globalThis.crypto.randomUUID() });
                },
              }),
            ),
          ),
          h(
            'label',
            null,
            h('input', {
              type: 'checkbox',
              checked: submission.confirmed,
              disabled: busy,
              onChange: (/** @type {any} */ event) =>
                setSubmission({
                  ...submission,
                  confirmed: event.target.checked,
                }),
            }),
            zh
              ? '我确认上传以上冻结文件到平台供审核'
              : 'I confirm uploading these frozen files to the platform for review',
          ),
          button(
            zh ? '上传并送审' : 'Upload and submit',
            () =>
              run(async () => {
                const list = (/** @type {string} */ value) =>
                  value
                    .split(',')
                    .map((entry) => entry.trim())
                    .filter(Boolean);
                const response = await fetcher(
                  localURL('auriga/resources/catalog/submissions'),
                  {
                    method: 'POST',
                    credentials: 'same-origin',
                    redirect: 'error',
                    signal: AbortSignal.timeout(90000),
                    headers: {
                      'content-type': 'application/json',
                      'x-auriga-catalog': '1',
                    },
                    body: JSON.stringify({
                      snapshotId: frozen.snapshotId,
                      key: frozen.key,
                      version: submission.version,
                      license: submission.license,
                      author: submission.author,
                      reason: submission.reason,
                      permissions: {
                        tools: list(submission.tools),
                        outboundHosts: list(submission.outboundHosts),
                        dataAccess: list(submission.dataAccess),
                      },
                      ...(submission.packageId
                        ? { packageId: submission.packageId }
                        : {}),
                    }),
                  },
                );
                if (!response.ok) throw new Error('submission failed');
                const result = await response.json();
                setFrozen(null);
                setNotice(
                  (zh ? '平台已接受送审：' : 'Submission accepted: ') +
                    result.versionId +
                    ' · ' +
                    result.state,
                );
              }),
            !submission.confirmed ||
              !submission.license ||
              !submission.author ||
              !submission.reason,
          ),
        ),
      ...items.map((/** @type {any} */ item) =>
        h(
          'div',
          { key: item.name + item.enabled },
          h(
            'p',
            null,
            item.name,
            item.enabled ? '' : ' · ' + t.disabled,
            item.unavailable ? ' · ' + t.unavailable : '',
          ),
          !item.unavailable &&
            h(
              'div',
              null,
              button(t.edit, () =>
                run(async () => {
                  const result = await call('auriga/settings/skills', {
                    action: 'read',
                    name: item.name,
                    enabled: item.enabled,
                  });
                  setSelected(result);
                  setSource(result.files['SKILL.md']);
                }),
              ),
              button(item.enabled ? t.disable : t.enable, () =>
                change(item, item.enabled ? 'disable' : 'enable'),
              ),
              button(t.remove, () => change(item, 'remove')),
              button(t.freeze, () => change(item, 'freeze')),
            ),
        ),
      ),
      h(
        'label',
        null,
        t.source,
        h('textarea', {
          value: source,
          rows: 12,
          style: { display: 'block', width: '100%' },
          onChange: (/** @type {any} */ event) => setSource(event.target.value),
        }),
      ),
      button(t.create, () =>
        run(async () => {
          await call('auriga/settings/skills', { action: 'create', source });
          setSelected(null);
        }),
      ),
      selected &&
        button(t.save, () =>
          run(async () => {
            const result = await call('auriga/settings/skills', {
              action: 'edit',
              name: selected.name,
              enabled: selected.enabled,
              revision: selected.revision,
              source,
            });
            setSelected(result);
          }),
        ),
      notice && h('p', { role: 'status' }, notice),
    );
  }

  return function SettingsView(
    /** @type {{locale:string,boot?:any,consent?:any,gotoOverview?:()=>void}} */ {
      locale,
      boot,
      consent,
      gotoOverview: _gotoOverview,
    },
  ) {
    const t = messages[locale?.startsWith('zh') ? 'zh' : 'en'];
    const [state, setState] = React.useState(null),
      [account, setAccount] = React.useState(null);
    const [localUac, setLocalUac] = React.useState(null);
    const [address, setAddress] = React.useState(''),
      [notice, setNotice] = React.useState(''),
      [statusError, setStatusError] = React.useState('');
    const [controlText, setControlText] = React.useState('');
    const [controlFile, setControlFile] = React.useState(
      /** @type {File|null} */ (null),
    );
    const [controlReceipt, setControlReceipt] = React.useState(null);
    const [busy, setBusy] = React.useState(false);
    const [revision, reload] = React.useReducer(
      (/** @type {number} */ n) => n + 1,
      0,
    );
    React.useEffect(() => subscribeIdentity?.(reload), []);
    const explain = (/** @type {any} */ error) =>
      error.message?.startsWith('statusHttp:')
        ? t.statusHttp + error.message.split(':')[1]
        : error.name === 'TimeoutError'
          ? t.timeout
          : error instanceof TypeError
            ? t.loadNetwork
            : (t[/** @type {keyof typeof t} */ (error.message)] ??
              t.unavailable);
    const stateSeqRef = useRef(0);
    const writeInFlightRef = useRef(0);
    const readState = async () => {
      const seq = ++stateSeqRef.current;
      const next = await call('auriga/settings/state');
      if (writeInFlightRef.current === 0 && seq === stateSeqRef.current) {
        setState(next);
        setAddress(next.platformOrigin ?? '');
      }
      try {
        const identity = await call('auriga/shell/status');
        if (
          typeof identity?.loggedIn !== 'boolean' ||
          (identity.loggedIn && typeof identity.account?.subject !== 'string')
        )
          throw new Error('invalidState');
        setAccount(identity);
        setStatusError('');
      } catch (error) {
        setAccount(null);
        setStatusError(explain(error));
      }
    };
    React.useEffect(() => {
      let live = true;
      let first = true;
      const update = async () => {
        // Each read owns its cancellation guard; unmounted windows never publish.
        const seq = ++stateSeqRef.current;
        try {
          const next = await call('auriga/settings/state');
          const identity = await call('auriga/shell/status').catch((error) => ({
            readError: explain(error),
          }));
          if (!live) return;
          if (writeInFlightRef.current === 0 && seq === stateSeqRef.current) {
            setState(next);
            if (first) setAddress(next.platformOrigin ?? '');
          }
          first = false;
          if (
            identity.readError ||
            typeof identity.loggedIn !== 'boolean' ||
            (identity.loggedIn && typeof identity.account?.subject !== 'string')
          ) {
            setAccount(null);
            setStatusError(identity.readError ?? t.invalidState);
          } else {
            setAccount(identity);
            setStatusError('');
          }
        } catch (error) {
          if (live) {
            setAccount(null);
            setStatusError(explain(error));
          }
        }
      };
      void update();
      const timer = setInterval(() => {
        void update();
      }, 5000);
      return () => {
        live = false;
        clearInterval(timer);
      };
    }, [revision, locale]);
    React.useEffect(() => {
      if (state?.mode === 'managed' || boot?.mode === 'managed') return;
      let live = true;
      const update = async () => {
        try {
          const value = await call('auriga/uac/status');
          if (live)
            setLocalUac(typeof value?.available === 'boolean' ? value : null);
        } catch {
          if (live) setLocalUac(null);
        }
      };
      void update();
      const timer = setInterval(() => {
        void update();
      }, 5000);
      return () => {
        live = false;
        clearInterval(timer);
      };
    }, [revision, state?.mode, boot?.mode]);
    const run = async (/** @type {()=>Promise<void>} */ action) => {
      setBusy(true);
      setNotice('');
      try {
        await action();
      } catch (error) {
        setNotice(explain(error));
      } finally {
        setBusy(false);
      }
    };
    const managed = state?.mode === 'managed' || boot?.mode === 'managed';
    const configured = managed || !!state?.platformOrigin;
    const loggedIn = account?.loggedIn === true;
    const localSignedIn =
      localUac?.available === true &&
      !localUac.reason &&
      localUac.state === 'authenticated';
    const localName = localSignedIn
      ? localUac.displayName || localUac.empNo || t.localSignedIn
      : '';
    const recovering =
      account?.recovering === true ||
      (!loggedIn && account?.resumeAvailable === true);
    const platformConnection =
      statusError ||
      (!account
        ? t.localUnknown
        : recovering
          ? t.recovering
          : loggedIn
            ? account.connected
              ? t.platformConnected
              : t.platformSignedIn
            : t.platformDisconnected);
    const network = account?.network;
    const ready = network?.connected === true;
    const name =
      account?.account?.displayName ??
      (managed ? t.memberLoading : t.notProvided);
    const managedFailure =
      managed &&
      (account?.session?.available === false || account?.loggedIn === false);
    const reason = managedFailure
      ? t.managedExpired
      : !account
        ? t.localUnknown
        : !loggedIn
          ? recovering
            ? t.recovering
            : account?.models?.reauthenticationRequired
              ? t.expired
              : t.signedOut
          : network?.state === 'unconfigured' ||
              network?.reason === 'configuration_missing'
            ? t.unconfiguredNetwork
            : ['connecting', 'registering', 'joining'].includes(network?.state)
              ? t.joining
              : account?.communication === 'unavailable' ||
                  ['unavailable', 'reconnecting', 'disconnected'].includes(
                    network?.state,
                  )
                ? t.network
                : network?.reason
                  ? network.reason
                  : t.networkPending;
    const networkText = ready
      ? t.networkReady
      : t.networkPending + ' · ' + reason;
    const modelText = managed
      ? t.modelManual
      : account?.models?.available
        ? t.modelReady
        : t.modelMissing;
    const platformSummary =
      (managed && (managedFailure || statusError || !account)
        ? t.managed +
          ' · ' +
          (managedFailure || statusError
            ? t.managedExpired
            : t.managedVerifying)
        : statusError) ||
      (!configured
        ? t.unconfigured
        : !loggedIn
          ? reason
          : managed
            ? t.managed +
              ' · ' +
              t.fullName +
              ' ' +
              name +
              ' · ' +
              t.networkTitle +
              ' ' +
              networkText +
              ' · ' +
              t.model +
              ' ' +
              modelText
            : name + ' · ' + t.networkTitle + ' ' + networkText);
    const summary =
      !managed && localSignedIn
        ? t.fullName +
          '：' +
          localName +
          '；' +
          t.platformConnection +
          '：' +
          platformConnection
        : platformSummary;
    const [category, setCategoryState] = React.useState(() =>
      readSettingsCategory('account'),
    );
    const setCategory = (/** @type {string} */ id) => {
      setCategoryState(id);
      writeSettingsCategory(id);
    };
    const [copyNotice, setCopyNotice] = React.useState('');
    const [copiedKey, setCopiedKey] = React.useState('');
    const shellCommand = async (/** @type {string} */ action) => {
      await call('auriga/shell/command', { action }, { csrf: account?.csrf });
      await reload();
    };
    const enter = () =>
      run(async () => {
        if (!openLogin || !openLogin()) {
          setNotice(t.loginEntryUnavailable);
          return;
        }
      });
    const retryLocalConnect = () =>
      run(async () => {
        await shellCommand('retry_local_uac');
      });
    const goConfigure = () => {
      setCategory('platform');
      queueMicrotask(() =>
        document
          .querySelector('#auriga-platform-address')
          ?.scrollIntoView({ block: 'center' }),
      );
    };
    const logout = () =>
      run(async () => {
        const tasks = [];
        if (localSignedIn)
          tasks.push(call('auriga/uac/logout', {}, { csrf: localUac.csrf }));
        if (loggedIn)
          tasks.push(
            call(
              'auriga/shell/command',
              { action: 'logout' },
              { csrf: account.csrf },
            ),
          );
        const results = await Promise.allSettled(tasks);
        reload();
        const failed = results.find((result) => result.status === 'rejected');
        if (failed?.status === 'rejected') throw failed.reason;
      });
    const localTone = localSignedIn
      ? 'success'
      : localUac?.available === false
        ? 'idle'
        : localUac?.available
          ? 'idle'
          : 'idle';
    const localStatusLabel = localSignedIn
      ? t.localSignedIn
      : localUac?.available === false
        ? t.localUnknown
        : localUac?.available
          ? t.localSignedOut
          : t.localUnknown;
    const localAction =
      !managed && localUac?.available && !localSignedIn
        ? h(
            SettingAction,
            { compact: true, disabled: busy, onClick: enter },
            t.login,
          )
        : !managed && localSignedIn
          ? h(
              SettingAction,
              {
                danger: true,
                compact: true,
                disabled: busy,
                onClick: logout,
              },
              t.logout,
            )
          : null;
    const needsReauth =
      !loggedIn && account?.models?.reauthenticationRequired === true;
    const cloudTone = managed
      ? managedFailure || statusError
        ? 'error'
        : loggedIn && account?.connected
          ? 'success'
          : !account || statusError
            ? 'processing'
            : loggedIn
              ? 'warn'
              : 'idle'
      : statusError
        ? 'error'
        : recovering ||
            ['connecting', 'registering', 'joining'].includes(network?.state)
          ? 'processing'
          : loggedIn && account?.connected
            ? 'success'
            : loggedIn
              ? 'warn'
              : needsReauth
                ? 'error'
                : 'idle';
    const cloudStatusLabel = managed
      ? managedFailure || statusError
        ? t.managedExpired
        : !account && !statusError
          ? t.managedVerifying
          : loggedIn && account?.connected
            ? t.platformConnected
            : loggedIn
              ? t.platformSignedIn
              : t.platformDisconnected
      : statusError
        ? statusError
        : !account
          ? t.localUnknown
          : recovering
            ? t.recovering
            : loggedIn
              ? account.connected
                ? t.platformConnected
                : t.platformSignedIn
              : needsReauth
                ? t.expired
                : t.platformDisconnected;
    const canRetryWithLocal =
      localSignedIn &&
      (!loggedIn || account?.canRetryLocalUac === true || needsReauth);
    const cloudAction = managed
      ? null
      : statusError
        ? h(
            SettingAction,
            { compact: true, disabled: busy, onClick: reload },
            t.retry,
          )
        : !configured
          ? h(
              SettingAction,
              { compact: true, disabled: busy, onClick: goConfigure },
              t.configure,
            )
          : recovering
            ? h(
                SettingAction,
                { compact: true, disabled: busy, onClick: reload },
                t.reconnect,
              )
            : canRetryWithLocal
              ? h(
                  SettingAction,
                  {
                    compact: true,
                    disabled: busy,
                    onClick: retryLocalConnect,
                  },
                  t.retryConnect,
                )
              : needsReauth
                ? h(
                    SettingAction,
                    { compact: true, disabled: busy, onClick: enter },
                    t.reauthenticate,
                  )
                : !loggedIn
                  ? h(
                      SettingAction,
                      { compact: true, disabled: busy, onClick: enter },
                      t.connect,
                    )
                  : loggedIn && !account?.connected
                    ? h(
                        SettingAction,
                        { compact: true, disabled: busy, onClick: reload },
                        t.reconnect,
                      )
                    : !localSignedIn
                      ? h(
                          SettingAction,
                          {
                            danger: true,
                            compact: true,
                            disabled: busy,
                            onClick: logout,
                          },
                          t.logout,
                        )
                      : null;
    const cloudDescription =
      !managed &&
      (localSignedIn || loggedIn) &&
      (!!statusError || !loggedIn || !account?.connected)
        ? t.local
        : undefined;
    const connectionTone = cloudTone;
    const platformConnectionLabel = cloudStatusLabel;
    const receiveControls = useReceiveSettings({ t, account, revision });
    const showConsent =
      loggedIn && account?.capabilities?.consent === true && consent;
    const showDesktop = !managed && !!state?.desktop;
    const showPlatform = !managed;
    const showProxy = !managed && !!state?.proxy;
    const identityValue = managed
      ? name
      : localName ||
        statusError ||
        (loggedIn || recovering
          ? name
          : !account
            ? t.localUnknown
            : t.signedOut);
    const displayName =
      (managed ? name : localName || (loggedIn || recovering ? name : '')) ||
      identityValue;
    const department =
      (!managed && localSignedIn && localUac.department) ||
      (account?.uacLoginInfo?.status === 'available' &&
        account.uacLoginInfo.department) ||
      '';
    const empNoValue =
      (localSignedIn && localUac.empNo) ||
      account?.account?.empNo ||
      (account?.uacLoginInfo?.status === 'available' &&
        account.uacLoginInfo.empNo) ||
      '';
    const profileName =
      (localSignedIn && (localUac.displayName || localUac.empNo)) ||
      account?.account?.displayName ||
      (managed ? name : '') ||
      '';
    const initials = String(displayName || '?')
      .trim()
      .slice(0, 1)
      .toUpperCase();
    const memberSubject =
      (loggedIn || recovering) && account?.account?.subject
        ? String(account.account.subject)
        : '';
    const protocolValue =
      boot?.protocol != null && String(boot.protocol) !== ''
        ? String(boot.protocol)
        : '';
    const versionValue = boot?.pluginVersion ? String(boot.pluginVersion) : '';
    const sourceValue = boot?.source ? String(boot.source) : '';
    const reasonRaw = network?.reason ?? account?.communication;
    const reasonValue =
      reasonRaw != null && String(reasonRaw) !== '' ? String(reasonRaw) : '';
    const modelTone = managed
      ? 'idle'
      : account?.models?.available
        ? 'success'
        : 'idle';
    const diagnosticFields = [
      {
        key: 'memberId',
        label: t.memberId,
        value: memberSubject,
      },
      {
        key: 'protocol',
        label: t.protocol,
        value: protocolValue,
      },
      {
        key: 'version',
        label: t.version,
        value: versionValue,
      },
      {
        key: 'source',
        label: t.source,
        value: sourceValue,
      },
      {
        key: 'model',
        label: t.model,
        value: modelText,
        provided: true,
        status: h(StatusIndicator, {
          tone: modelTone,
          label: modelText,
        }),
      },
      {
        key: 'reason',
        label: t.reason,
        value: reasonValue,
      },
    ].map((field) => {
      const provided =
        field.provided === true ||
        (field.value != null && String(field.value) !== '');
      return {
        ...field,
        provided,
        display: provided ? field.value : t.notProvided,
      };
    });
    /** @param {string} value @param {string} [key] */
    const copyText = async (value, key) => {
      try {
        if (navigator.clipboard?.writeText)
          await navigator.clipboard.writeText(value);
        else {
          const area = document.createElement('textarea');
          area.value = value;
          document.body.append(area);
          area.select();
          document.execCommand('copy');
          area.remove();
        }
        setCopyNotice(t.copied);
        if (key) {
          setCopiedKey(key);
          setTimeout(
            () =>
              setCopiedKey((/** @type {string} */ current) =>
                current === key ? '' : current,
              ),
            1500,
          );
        }
        setTimeout(() => setCopyNotice(''), 1500);
      } catch {
        setCopiedKey('');
        setCopyNotice(t.copyFailed);
        setTimeout(() => setCopyNotice(''), 1500);
      }
    };
    const navGroups = [
      {
        id: 'general',
        label: t.navGeneral,
        items: [
          { id: 'account', label: t.navAccount, icon: 'User' },
          showPlatform && {
            id: 'platform',
            label: t.navPlatform,
            icon: 'Link2',
          },
          showProxy && { id: 'proxy', label: t.navProxy, icon: 'Globe' },
          showDesktop && {
            id: 'desktop',
            label: t.navDesktop,
            icon: 'Download',
          },
          { id: 'skills', label: t.navSkills, icon: 'Package' },
        ].filter(Boolean),
      },
      {
        id: 'messaging',
        label: t.navMessaging,
        items: [
          { id: 'network', label: t.navNetwork, icon: 'Radio' },
          showConsent && {
            id: 'consent',
            label: t.navConsent,
            icon: 'Shield',
          },
        ].filter(Boolean),
      },
      {
        id: 'advanced',
        label: t.navAdvanced,
        items: [
          { id: 'diagnostics', label: t.navDiagnostics, icon: 'Stethoscope' },
        ],
      },
    ].filter((group) => group.items.length > 0);
    const visibleIds = navGroups.flatMap((g) => g.items.map((i) => i.id));
    const active = visibleIds.includes(category) ? category : 'account';
    void summary;
    return h(
      SettingsLayout,
      {
        notice: notice || copyNotice || undefined,
        nav: h(SettingsNavigation, {
          label: t.navLabel,
          groups: navGroups,
          value: active,
          onChange: setCategory,
        }),
      },
      h(
        SettingsPanel,
        { id: 'account', active: active === 'account' },
        h(
          AccountCard,
          {
            initials,
            title: displayName,
            empNo: empNoValue || undefined,
            refresh: h(SettingIconButton, {
              icon: 'RefreshCw',
              label: t.refresh,
              disabled: busy,
              busy,
              onClick: reload,
            }),
          },
          !managed &&
            h(SettingRow, {
              icon: 'Monitor',
              label: t.localLogin,
              status: h(StatusIndicator, {
                tone: localTone,
                label: localStatusLabel,
              }),
              action: localAction,
            }),
          h(SettingRow, {
            icon: 'Cloud',
            label: managed ? t.managed : t.cloudConnection,
            description: cloudDescription,
            status: h(StatusIndicator, {
              tone: cloudTone,
              label: cloudStatusLabel,
            }),
            action: cloudAction,
          }),
        ),
        h(
          SettingsSection,
          {
            'aria-label': t.identityInfo,
          },
          h(SettingReadonly, {
            label: t.fullName,
            value: profileName || identityValue || t.notProvided,
          }),
          h(SettingReadonly, {
            label: t.empNo,
            value: empNoValue || t.notProvided,
          }),
          h(SettingReadonly, {
            label: t.department,
            value: department || t.notProvided,
          }),
          managed && h(SettingStatus, null, t.managed),
        ),
      ),
      showPlatform &&
        h(
          SettingsPanel,
          {
            id: 'platform',
            active: active === 'platform',
            'aria-label': t.platform,
          },
          h(PlatformCard, {
            title: t.aurigaCloud,
            status: h(StatusIndicator, {
              tone: connectionTone,
              label: platformConnectionLabel,
            }),
            notice:
              state && state.platformOrigin !== state.activeOrigin
                ? h(SettingStatus, { tone: 'warn' }, t.pending)
                : null,
            actions: [
              h(
                SettingAction,
                {
                  key: 'test',
                  compact: true,
                  disabled: busy || !address.trim(),
                  onClick: () =>
                    run(async () => {
                      const result = await call('auriga/settings/test', {
                        platformOrigin: address,
                      });
                      setNotice(
                        result.reachable
                          ? t.reachable
                          : result.reason === 'http'
                            ? t.http + result.status
                            : (t[
                                /** @type {keyof typeof t} */ (result.reason)
                              ] ?? t.unavailable),
                      );
                    }),
                },
                t.test,
              ),
              h(
                SettingAction,
                {
                  key: 'save',
                  primary: true,
                  compact: true,
                  disabled:
                    busy ||
                    !state ||
                    address.trim() === (state?.platformOrigin ?? '').trim(),
                  onClick: () =>
                    run(async () => {
                      try {
                        await call('auriga/settings/save', {
                          platformOrigin: address,
                          deployment: state?.deployment ?? 'local',
                        });
                      } finally {
                        await readState();
                      }
                      setNotice(t.saved);
                    }),
                },
                t.save,
              ),
            ],
            children: h(
              'div',
              { className: 'auriga-platform-card-fields' },
              h(
                'div',
                { className: 'auriga-platform-field' },
                h(
                  'label',
                  {
                    className: 'auriga-platform-field-label',
                    htmlFor: 'auriga-platform-address',
                  },
                  t.address,
                ),
                h(
                  PlatformAddressField,
                  null,
                  h(SettingInput, {
                    id: 'auriga-platform-address',
                    'aria-label': t.address,
                    value: address,
                    disabled: busy || !state,
                    onChange: (/** @type {any} */ event) =>
                      setAddress(event.target.value),
                  }),
                ),
              ),
            ),
          }),
        ),
      showProxy &&
        h(
          SettingsPanel,
          {
            id: 'proxy',
            active: active === 'proxy',
            'aria-label': t.proxyTitle,
          },
          h(NetworkProxyView, {
            t,
            proxy: state.proxy,
            busy,
            onSave: async (/** @type {any} */ payload) => {
              let ok = false;
              writeInFlightRef.current += 1;
              try {
                await run(async () => {
                  const next = await call('auriga/settings/proxy', payload);
                  stateSeqRef.current += 1;
                  setState(next);
                  setNotice(t.saved);
                  ok = true;
                });
              } finally {
                writeInFlightRef.current -= 1;
              }
              return ok;
            },
            onReset: async () => {
              let ok = false;
              writeInFlightRef.current += 1;
              try {
                await run(async () => {
                  const next = await call('auriga/settings/proxy/reset', {});
                  stateSeqRef.current += 1;
                  setState(next);
                  setNotice(t.proxyResetDone);
                  ok = true;
                });
              } finally {
                writeInFlightRef.current -= 1;
              }
              return ok;
            },
            onTest: async (/** @type {string} */ url) => {
              let result = null;
              await run(async () => {
                result = await call('auriga/settings/proxy/test', { url });
              });
              return result;
            },
          }),
        ),
      showDesktop &&
        h(
          SettingsPanel,
          {
            id: 'desktop',
            active: active === 'desktop',
            'aria-label': t.desktop,
          },
          h(DesktopUpdateView, {
            t,
            desktop: state.desktop,
            busy,
            currentVersion: boot?.pluginVersion,
            onCheck: () =>
              run(async () => {
                await call('auriga/settings/desktop', { action: 'check' });
                await readState();
              }),
            onUpdate: () =>
              run(async () => {
                await call(
                  'auriga/settings/desktop',
                  { action: 'update' },
                  { timeoutMs: null },
                );
                await readState();
              }),
            onCancel: () =>
              run(async () => {
                await call('auriga/settings/desktop', { action: 'cancel' });
                await readState();
              }),
            onToggleEnabled: (/** @type {boolean} */ enabled) =>
              run(async () => {
                await call('auriga/settings/desktop', { enabled });
                await readState();
              }),
            onChangeChannel: (/** @type {string} */ channel) =>
              run(async () => {
                await call('auriga/settings/desktop', { channel });
                await call('auriga/settings/desktop', { action: 'check' });
                await readState();
              }),
          }),
        ),
      h(
        SettingsPanel,
        {
          id: 'network',
          active: active === 'network',
          'aria-label': t.networkTitle,
        },
        h(
          SettingsCard,
          {
            icon: 'Radio',
            title: t.networkTitle,
            status: h(StatusIndicator, {
              tone: statusError
                ? 'error'
                : ready
                  ? 'success'
                  : recovering ||
                      ['connecting', 'registering', 'joining'].includes(
                        network?.state,
                      )
                    ? 'processing'
                    : 'idle',
              label: statusError || networkText,
            }),
          },
          h(
            'div',
            { className: 'auriga-network-info' },
            (() => {
              const endpoint =
                network?.address ?? account?.connection?.endpointId ?? '';
              return endpoint
                ? h(
                    'div',
                    { className: 'auriga-network-info-row' },
                    h(
                      'span',
                      { className: 'auriga-network-info-label' },
                      t.endpoint,
                    ),
                    h(
                      'div',
                      { className: 'auriga-network-info-value' },
                      h(
                        'span',
                        {
                          className: 'auriga-network-info-text',
                          title: endpoint,
                        },
                        endpoint,
                      ),
                      h(SettingIconButton, {
                        icon: 'Copy',
                        label: t.copy,
                        onClick: () => copyText(endpoint),
                      }),
                    ),
                  )
                : null;
            })(),
            h(
              'div',
              { className: 'auriga-network-info-row' },
              h('span', { className: 'auriga-network-info-label' }, t.received),
              h(
                'div',
                { className: 'auriga-network-info-value' },
                h(
                  'span',
                  { className: 'auriga-network-info-text' },
                  String(
                    account?.connection?.received ?? network?.received ?? 0,
                  ),
                ),
              ),
            ),
          ),
        ),
        h(
          SettingsCard,
          {
            icon: 'Package',
            title: t.controlFile,
            notice: !state?.networkControl?.available
              ? h(SettingStatus, { tone: 'warn' }, t.controlMissing)
              : controlReceipt
                ? h(
                    SettingStatus,
                    null,
                    h(
                      'a',
                      {
                        className: 'auriga-setting-action',
                        download: 'auriga-control-receipt.json',
                        href:
                          'data:application/json;charset=utf-8,' +
                          encodeURIComponent(JSON.stringify(controlReceipt)),
                      },
                      t.controlReceipt,
                    ),
                  )
                : null,
          },
          h(
            'div',
            { className: 'auriga-control-import-row' },
            h(SettingFilePicker, {
              accept: '.json,application/json',
              'aria-label': t.controlFile,
              placeholder: t.controlPick,
              clearLabel: t.controlClear,
              disabled: busy || !state?.networkControl?.available,
              file: controlFile,
              onChange: (/** @type {File|null} */ file) =>
                run(async () => {
                  setControlReceipt(null);
                  setControlFile(null);
                  setControlText('');
                  if (!file) return;
                  if (file.size > 32 * 1024 * 1024)
                    throw new Error('invalidSettings');
                  const text = await file.text();
                  setControlFile(file);
                  setControlText(text);
                }),
            }),
            h(
              SettingAction,
              {
                primary: true,
                disabled:
                  busy || !controlText || !state?.networkControl?.available,
                onClick: () =>
                  run(async () => {
                    const result = await call(
                      'auriga/settings/network-control',
                      { packageJson: controlText },
                    );
                    setControlReceipt(result.receipt);
                    setNotice(t.controlSaved);
                  }),
              },
              t.controlImport,
            ),
          ),
        ),
        loggedIn && receiveControls,
      ),
      showConsent &&
        h(
          SettingsPanel,
          {
            id: 'consent',
            active: active === 'consent',
            'aria-label': t.navConsent,
          },
          consent,
        ),
      h(
        SettingsPanel,
        {
          id: 'skills',
          active: active === 'skills',
          'aria-label': t.navSkills,
        },
        active === 'skills' ? h(PrivateSkillsView, { locale }) : null,
      ),
      h(
        SettingsPanel,
        {
          id: 'diagnostics',
          active: active === 'diagnostics',
          'aria-label': t.diagnostics,
        },
        h(
          SettingsCard,
          {
            icon: 'Stethoscope',
            title: t.navDiagnostics,
            description: t.diagnosticsHint,
            status: h(
              SettingAction,
              {
                compact: true,
                icon: 'Copy',
                onClick: () =>
                  copyText(
                    diagnosticFields
                      .map(
                        (/** @type {any} */ field) =>
                          field.label + ': ' + field.display,
                      )
                      .join('\n'),
                    'all',
                  ),
              },
              t.copyAll,
            ),
          },
          ...diagnosticFields.map((/** @type {any} */ field) =>
            h(SettingReadonly, {
              key: field.key,
              label: field.label,
              value: field.display,
              status: field.status,
              muted: !field.provided,
              copied: copiedKey === field.key,
              copyLabel: t.copy,
              copySlot: true,
              onCopy: field.provided
                ? () => copyText(String(field.value), field.key)
                : undefined,
            }),
          ),
        ),
      ),
    );
  };
}
