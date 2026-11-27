/* global AbortSignal, document */
/** Native React settings. Status is supplied by the authenticated runtime. */
const messages = {
  zh: {
    desktop: '桌面更新',
    requiredUpdate:
      '此版本已低于最低可用版本，请完成更新并重启后继续使用平台功能。',
    downloadProgress: '正在下载',
    updateFailed: '更新未完成，旧版和用户数据已保留，可重试。',
    cancelUpdate: '取消下载（保留进度）',
    updatePaused: '下载已暂停，可继续下载',
    updateChannel: '更新渠道',
    stable: '正式',
    beta: '测试',
    desktopConfirmationRequired:
      '更新地址尚未明确确认。请使用平台地址、IP 模式及地址确认参数重新安装 Desktop。',
    update: '更新',
    checkUpdate: '检查更新',
    autoCheck: '自动检查',
    updateRestart: '下载后请关闭 Auriga DSH，退出时安装，下次启动使用新版。',
    platform: '平台',
    address: '平台地址',
    save: '保存',
    test: '测试连接',
    saved: '已保存',
    reachable: '平台可达',
    unconfigured: '未配置平台',
    configure: '填写平台地址',
    login: '登录',
    logout: '退出登录',
    account: '账号',
    identity: '成员',
    memberId: '成员 ID',
    department: '部门',
    device: '设备名',
    loginTime: '登录时间',
    signedOut: '未登录',
    local: '平台功能暂不可用',
    localIdentity: '本地身份',
    localSignedIn: '已登录',
    localUnknown: '状态未知',
    platformConnection: '平台连接',
    platformConnected: '已连接',
    platformSignedIn: '已登录，连接不可用',
    platformDisconnected: '未连接',
    loginEntryUnavailable: '登录界面暂未就绪，请在概览中重试。',
    managed: '系统托管',
    managedExpired: '实例会话已失效，请从门户重新进入',
    managedVerifying: '正在验证实例会话',
    memberLoading: '正在读取成员资料',
    networkTitle: '消息网络',
    controlFile: '公有控制包',
    controlImport: '导入控制包',
    controlSaved: '控制已保存；联网后自动提交确认。',
    controlReceipt: '保存确认回执',
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
    proxyGlobalPlaceholder: 'http://127.0.0.1:7890（留空则仅按域名规则代理）',
    proxyNoProxy: '全局直连（NoProxy）',
    proxyNoProxyPlaceholder: '*.corp.local, 192.0.2.0/24',
    proxyBuiltinNoProxy: '已内置强制直连：',
    proxyRules: '按域名分流规则',
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
    proxyTestTarget: '测试目标',
    proxyTestPlaceholder: 'https://example.com 或域名',
    proxyTestRun: '测试命中与连通',
    proxyTestReachable: '可达',
    proxyTestUnreachable: '不可达',
    invalidProxyUrl: '代理地址无效（仅支持 http:// 或 https://）',
    invalidPattern: '域名或 CIDR 规则格式无效',
    invalidTarget: '请输入有效的测试域名或 URL',
    proxy_loop: '代理地址不能指向客户端本地分流端口',
    proxy_unreachable: '代理不可达',
    proxy_auth_required: '代理需要身份验证 (407)',
    tls_error: 'TLS 证书校验失败',
  },
  en: {
    desktop: 'Desktop updates',
    requiredUpdate:
      'This version is no longer supported. Update and restart to continue using platform features.',
    downloadProgress: 'Downloading',
    updateFailed:
      'Update incomplete. Your previous version and data are preserved. Retry to continue.',
    cancelUpdate: 'Cancel download (keep progress)',
    updatePaused: 'Download paused; resume when ready',
    updateChannel: 'Update channel',
    stable: 'Stable',
    beta: 'Beta',
    desktopConfirmationRequired:
      'The update address needs confirmation. Reinstall Desktop with the platform address, IP mode and address confirmation parameters.',
    update: 'Update',
    checkUpdate: 'Check for updates',
    autoCheck: 'Check automatically',
    updateRestart:
      'After download, close Auriga DSH to install. Reopen to use the new version.',
    platform: 'Platform',
    address: 'Platform address',
    save: 'Save',
    test: 'Test connection',
    saved: 'Saved',
    reachable: 'Platform reachable',
    unconfigured: 'Platform not configured',
    configure: 'Enter platform address',
    login: 'Sign in',
    logout: 'Sign out',
    account: 'Account',
    identity: 'Member',
    memberId: 'Member ID',
    department: 'Department',
    device: 'Device name',
    loginTime: 'Sign-in time',
    signedOut: 'Signed out',
    local: 'Platform features are unavailable',
    localIdentity: 'Local identity',
    localSignedIn: 'Signed in',
    localUnknown: 'Status unknown',
    platformConnection: 'Platform connection',
    platformConnected: 'Connected',
    platformSignedIn: 'Signed in, connection unavailable',
    platformDisconnected: 'Disconnected',
    loginEntryUnavailable: 'Sign-in is not ready. Retry from Overview.',
    managed: 'System managed',
    managedExpired: 'Instance session has expired. Re-enter from the portal.',
    managedVerifying: 'Verifying instance session',
    memberLoading: 'Loading member profile',
    networkTitle: 'Message network',
    controlFile: 'Public control package',
    controlImport: 'Import control package',
    controlSaved: 'Control saved. Confirmation will be sent when online.',
    controlReceipt: 'Save installation receipt',
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
    proxyGlobalPlaceholder: 'http://127.0.0.1:7890 (empty = domain rules only)',
    proxyNoProxy: 'Global NoProxy',
    proxyNoProxyPlaceholder: '*.corp.local, 192.0.2.0/24',
    proxyBuiltinNoProxy: 'Always direct: ',
    proxyRules: 'Per-domain rules',
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
    proxyTestTarget: 'Test target',
    proxyTestPlaceholder: 'https://example.com or hostname',
    proxyTestRun: 'Test route',
    proxyTestReachable: 'Reachable',
    proxyTestUnreachable: 'Unreachable',
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
    return h(
      'section',
      null,
      h('h3', null, t.receive),
      h('p', null, t.receiveHint),
      sessions?.length === 0 && h('p', null, t.receiveEmpty),
      ...(sessions ?? []).map((/** @type {any} */ session) =>
        h(
          'label',
          { key: session.local, style: { display: 'block' } },
          h('input', {
            type: 'checkbox',
            checked: session.enabled,
            disabled: busy || !account?.csrf || session.pending,
            onChange: (/** @type {any} */ event) =>
              writeReceive(session, event.target.checked),
          }),
          ` ${session.sessionId} · ${session.address}`,
          session.pending && ` · ${t.receivePending}`,
          session.pending &&
            h(
              'button',
              {
                type: 'button',
                disabled: busy || !account?.csrf,
                onClick: () => writeReceive(session, session.pendingEnabled),
              },
              t.receiveRetry,
            ),
        ),
      ),
      error && h('p', { role: 'status' }, error),
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
    const proxyFingerprint = JSON.stringify({
      mode: proxy.mode ?? 'direct',
      globalProxy: proxy.globalProxy ?? '',
      noProxy: proxy.noProxy ?? [],
      rules: proxy.rules ?? [],
      source: proxy.source ?? '',
    });
    const lastSyncedRef = React.useRef(proxyFingerprint);
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

    return h(
      'section',
      { 'aria-label': t.proxyTitle },
      h('h3', null, t.proxyTitle),
      h('p', null, t.proxyHint),
      h('p', { role: 'status' }, sourceLabel),
      proxy.degraded && h('p', { role: 'alert' }, t.proxyDegraded),
      h(
        'label',
        { className: 'auriga-setting' },
        h('span', null, t.proxyMode),
        h(
          'select',
          {
            'aria-label': t.proxyMode,
            value: mode,
            disabled: busy,
            onChange: (/** @type {any} */ e) => {
              setDirty(true);
              setMode(e.target.value);
            },
          },
          h('option', { value: 'direct' }, t.proxyModeDirect),
          h('option', { value: 'proxy' }, t.proxyModeCustom),
        ),
      ),
      mode === 'proxy' &&
        h(
          React.Fragment,
          null,
          h(
            'label',
            { className: 'auriga-setting' },
            h('span', null, t.proxyGlobal),
            h('input', {
              'aria-label': t.proxyGlobal,
              value: globalProxy,
              placeholder: t.proxyGlobalPlaceholder,
              disabled: busy,
              onChange: (/** @type {any} */ e) => {
                setDirty(true);
                setGlobalProxy(e.target.value);
              },
            }),
          ),
          h(
            'label',
            { className: 'auriga-setting' },
            h('span', null, t.proxyNoProxy),
            h('textarea', {
              'aria-label': t.proxyNoProxy,
              rows: 2,
              value: noProxyText,
              placeholder: t.proxyNoProxyPlaceholder,
              disabled: busy,
              onChange: (/** @type {any} */ e) => {
                setDirty(true);
                setNoProxyText(e.target.value);
              },
            }),
          ),
          h(
            'p',
            null,
            t.proxyBuiltinNoProxy +
              (proxy.builtinNoProxy ?? ['localhost', '127.0.0.1', '::1']).join(
                ', ',
              ),
          ),
          h('h4', null, t.proxyRules),
          ...rules.map((/** @type {any} */ rule, /** @type {number} */ idx) =>
            h(
              'div',
              { key: rule.id ?? idx, className: 'auriga-proxy-rule' },
              h('input', {
                type: 'checkbox',
                checked: rule.enabled !== false,
                disabled: busy,
                onChange: (/** @type {any} */ e) =>
                  updateRule(idx, { enabled: e.target.checked }),
              }),
              h('input', {
                'aria-label': t.proxyRulePattern,
                placeholder: t.proxyRulePattern,
                value: rule.pattern ?? '',
                disabled: busy,
                onChange: (/** @type {any} */ e) =>
                  updateRule(idx, { pattern: e.target.value }),
              }),
              h(
                'select',
                {
                  'aria-label': t.proxyRuleAction,
                  value: rule.action === 'direct' ? 'direct' : 'proxy',
                  disabled: busy,
                  onChange: (/** @type {any} */ e) =>
                    updateRule(idx, { action: e.target.value }),
                },
                h('option', { value: 'proxy' }, t.proxyRuleProxy),
                h('option', { value: 'direct' }, t.proxyRuleDirect),
              ),
              h('input', {
                'aria-label': t.proxyRuleTarget,
                placeholder: t.proxyRuleTarget,
                value: rule.action === 'direct' ? '' : (rule.proxy ?? ''),
                disabled: busy || rule.action === 'direct',
                onChange: (/** @type {any} */ e) =>
                  updateRule(idx, { proxy: e.target.value }),
              }),
              h(
                'button',
                {
                  type: 'button',
                  disabled: busy || idx === 0,
                  onClick: () => moveRule(idx, -1),
                },
                t.proxyRuleUp,
              ),
              h(
                'button',
                {
                  type: 'button',
                  disabled: busy || idx === rules.length - 1,
                  onClick: () => moveRule(idx, 1),
                },
                t.proxyRuleDown,
              ),
              h(
                'button',
                {
                  type: 'button',
                  disabled: busy,
                  onClick: () => removeRule(idx),
                },
                t.proxyRuleDelete,
              ),
            ),
          ),
          h(
            'div',
            { className: 'auriga-setting-actions' },
            h(
              'button',
              { type: 'button', disabled: busy, onClick: addRule },
              t.proxyRuleAdd,
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
            disabled: busy,
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
        (proxy.source === 'user' || proxy.hasBundledDefault) &&
          h(
            'button',
            {
              type: 'button',
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
          ),
      ),
      h(
        'div',
        { className: 'auriga-setting-actions' },
        h('input', {
          'aria-label': t.proxyTestTarget,
          placeholder: t.proxyTestPlaceholder,
          value: testUrl,
          disabled: busy,
          onChange: (/** @type {any} */ e) => setTestUrl(e.target.value),
        }),
        h(
          'button',
          {
            type: 'button',
            disabled: busy || !testUrl.trim(),
            onClick: async () => {
              const res = await onTest(testUrl.trim());
              if (res) setTestResult(res);
            },
          },
          t.proxyTestRun,
        ),
      ),
      testResult &&
        h(
          'p',
          { role: 'status' },
          `${testResult.matchedBy} → ${
            testResult.action === 'proxy' ? testResult.proxy : t.proxyRuleDirect
          } · ${
            testResult.reachable
              ? `${t.proxyTestReachable} (${testResult.latencyMs}ms)`
              : `${t.proxyTestUnreachable}: ${
                  t[/** @type {keyof typeof t} */ (testResult.reason)] ??
                  testResult.reason
                }`
          }`,
        ),
    );
  }

  return function SettingsView(
    /** @type {{locale:string,boot?:any,consent?:any,gotoOverview?:()=>void}} */ {
      locale,
      boot,
      consent,
      gotoOverview,
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
    const stateSeqRef = React.useRef(0);
    const readState = async () => {
      const seq = ++stateSeqRef.current;
      const next = await call('auriga/settings/state');
      if (seq === stateSeqRef.current) {
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
          if (seq === stateSeqRef.current) {
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
    const platformConnection =
      statusError ||
      (!account
        ? t.localUnknown
        : loggedIn
          ? account.connected
            ? t.platformConnected
            : t.platformSignedIn
          : t.platformDisconnected);
    const recovering = !loggedIn && account?.resumeAvailable === true;
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
              t.identity +
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
        ? t.localIdentity +
          '：' +
          localName +
          '；' +
          t.platformConnection +
          '：' +
          platformConnection
        : platformSummary;
    const row = (/** @type {string} */ label, /** @type {any} */ value) =>
      h(
        'div',
        { className: 'auriga-setting' },
        h('span', null, label),
        h('span', { className: 'auriga-setting-value' }, value),
      );
    const button = (
      /** @type {string} */ label,
      /** @type {()=>any} */ action,
      disabled = false,
    ) =>
      h(
        'button',
        { type: 'button', disabled: busy || disabled, onClick: action },
        label,
      );
    const shellCommand = async (/** @type {string} */ action) => {
      await call('auriga/shell/command', { action }, { csrf: account?.csrf });
      await reload();
    };
    const enter = () =>
      run(async () => {
        if (openLogin) {
          if (!openLogin()) {
            setNotice(t.loginEntryUnavailable);
            gotoOverview?.();
          }
          return;
        }
        if (localUac?.available) {
          gotoOverview?.();
          return;
        }
        await shellCommand('inline_login');
        gotoOverview?.();
      });
    const action =
      !managed && localUac?.available && !localSignedIn
        ? button(t.login, enter)
        : statusError
          ? button(t.retry, reload)
          : managed
            ? button(t.refresh, reload)
            : !configured
              ? button(t.configure, () =>
                  document
                    .querySelector(
                      /** @type {string} */ ('#auriga-platform-address'),
                    )
                    ?.scrollIntoView({ block: 'center' }),
                )
              : recovering
                ? button(t.retry, reload)
                : !loggedIn
                  ? button(t.login, enter)
                  : button(t.refresh, reload);
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
    const receiveControls = useReceiveSettings({ t, account, revision });
    return h(
      'div',
      { className: 'auriga-settings-content' },
      h(
        'section',
        { className: 'auriga-status-card', 'aria-label': 'Auriga DSH status' },
        h('p', { role: 'status' }, summary),
        !managed && (!loggedIn || !ready) && h('p', null, t.local),
        action,
      ),
      h(
        'section',
        { 'aria-label': t.account },
        h('h3', null, t.account),
        row(
          t.identity,
          managed
            ? name
            : localName ||
                statusError ||
                (loggedIn || recovering
                  ? name
                  : !account
                    ? t.localUnknown
                    : t.signedOut),
        ),
        !managed &&
          localUac?.available !== false &&
          row(
            t.localIdentity,
            localSignedIn
              ? localName
              : localUac?.state === 'signed_out' && !localUac.reason
                ? t.signedOut
                : t.localUnknown,
          ),
        !managed && row(t.platformConnection, platformConnection),
        !managed &&
          localSignedIn &&
          localUac.department &&
          row(t.department, localUac.department),
        managed
          ? h('p', null, t.managed)
          : h(
              'div',
              null,
              (loggedIn || recovering) &&
                (account.deviceName ?? state?.deviceName) &&
                row(t.device, account.deviceName ?? state?.deviceName),
              account?.uacLoginInfo?.status === 'available' &&
                row(t.department, account.uacLoginInfo.department),
              (loggedIn || recovering) &&
                account.loginTime &&
                row(
                  t.loginTime,
                  new Date(account.loginTime).toLocaleString(locale),
                ),
              // Sign-in lives on the status card above; only sign-out here.
              (localSignedIn || (!statusError && loggedIn)) &&
                button(t.logout, logout),
            ),
      ),
      !managed &&
        state?.desktop &&
        h(
          'section',
          null,
          h('h3', null, t.desktop),
          state.desktop.confirmationRequired &&
            h('p', { role: 'alert' }, t.desktopConfirmationRequired),
          h(
            'label',
            { className: 'auriga-setting' },
            h('span', null, t.autoCheck),
            h('input', {
              type: 'checkbox',
              checked: state.desktop.enabled,
              disabled:
                busy ||
                state.desktop.confirmationRequired ||
                state.desktop.required,
              onChange: (/** @type {any} */ e) =>
                run(async () => {
                  await call('auriga/settings/desktop', {
                    enabled: e.target.checked,
                  });
                  await readState();
                }),
            }),
          ),
          state.desktop.required && h('p', { role: 'alert' }, t.requiredUpdate),
          h(
            'label',
            { className: 'auriga-setting' },
            h('span', null, t.updateChannel),
            h(
              'select',
              {
                value: state.desktop.channel ?? 'stable',
                disabled:
                  busy ||
                  state.desktop.downloading ||
                  state.desktop.required ||
                  state.desktop.restartRequired,
                onChange: (/** @type {any} */ e) =>
                  run(async () => {
                    await call('auriga/settings/desktop', {
                      channel: e.target.value,
                    });
                    await call('auriga/settings/desktop', { action: 'check' });
                    await readState();
                  }),
              },
              h('option', { value: 'stable' }, t.stable),
              h('option', { value: 'beta' }, t.beta),
            ),
          ),
          state.desktop.available &&
            h('p', { role: 'status' }, state.desktop.available.version),
          state.desktop.downloading &&
            h(
              'div',
              { role: 'status' },
              t.downloadProgress +
                ' ' +
                (state.desktop.downloadedBytes / 1048576).toFixed(1) +
                ' / ' +
                (state.desktop.totalBytes / 1048576).toFixed(1) +
                ' MiB',
              h('progress', {
                max: state.desktop.totalBytes,
                value: state.desktop.downloadedBytes,
              }),
              button(t.cancelUpdate, () =>
                run(async () => {
                  await call('auriga/settings/desktop', { action: 'cancel' });
                  await readState();
                }),
              ),
            ),
          state.desktop.phase === 'cancelled' && h('p', null, t.updatePaused),
          state.desktop.error && h('p', { role: 'alert' }, t.updateFailed),
          (state.desktop.available || state.desktop.downloading) &&
            !state.desktop.restartRequired &&
            h('p', null, t.updateRestart),
          h(
            'div',
            { className: 'auriga-setting-actions' },
            !state.desktop.confirmationRequired &&
              button(t.checkUpdate, () =>
                run(async () => {
                  await call('auriga/settings/desktop', { action: 'check' });
                  await readState();
                }),
              ),
            state.desktop.available &&
              !state.desktop.restartRequired &&
              !state.desktop.busy &&
              button(t.update, () =>
                run(async () => {
                  // The runtime bounds transfer idle time. A status-read deadline
                  // must not cancel a healthy package download before it queues.
                  await call(
                    'auriga/settings/desktop',
                    { action: 'update' },
                    { timeoutMs: null },
                  );
                  await readState();
                }),
              ),
          ),
          state.desktop.restartRequired &&
            h('p', { role: 'status' }, t.updateRestart),
        ),
      !managed &&
        h(
          'section',
          { 'aria-label': t.platform },
          h('h3', null, t.platform),
          h(
            'label',
            { className: 'auriga-setting' },
            h('span', null, t.address),
            h('input', {
              id: 'auriga-platform-address',
              'aria-label': t.address,
              value: address,
              disabled: busy || !state,
              onChange: (/** @type {any} */ event) =>
                setAddress(event.target.value),
            }),
          ),
          h(
            'div',
            { className: 'auriga-setting-actions' },
            button(
              t.test,
              () =>
                run(async () => {
                  const result = await call('auriga/settings/test', {
                    platformOrigin: address,
                  });
                  setNotice(
                    result.reachable
                      ? t.reachable
                      : result.reason === 'http'
                        ? t.http + result.status
                        : (t[/** @type {keyof typeof t} */ (result.reason)] ??
                          t.unavailable),
                  );
                }),
              !address.trim(),
            ),
            button(
              t.save,
              () =>
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
              !state,
            ),
          ),
          state &&
            state.platformOrigin !== state.activeOrigin &&
            h('p', null, t.pending),
        ),
      !managed &&
        state?.proxy &&
        h(NetworkProxyView, {
          t,
          proxy: state.proxy,
          busy,
          onSave: async (/** @type {any} */ payload) => {
            let ok = false;
            await run(async () => {
              const seq = ++stateSeqRef.current;
              const next = await call('auriga/settings/proxy', payload);
              if (seq === stateSeqRef.current) setState(next);
              setNotice(t.saved);
              ok = true;
            });
            return ok;
          },
          onReset: async () => {
            let ok = false;
            await run(async () => {
              const seq = ++stateSeqRef.current;
              const next = await call('auriga/settings/proxy/reset', {});
              if (seq === stateSeqRef.current) setState(next);
              setNotice(t.proxyResetDone);
              ok = true;
            });
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
      h(
        'section',
        { 'aria-label': t.networkTitle },
        h('h3', null, t.networkTitle),
        h(
          'label',
          null,
          t.controlFile,
          h('input', {
            type: 'file',
            accept: '.json,application/json',
            disabled: busy || !state?.networkControl?.available,
            onChange: (/** @type {any} */ event) =>
              run(async () => {
                const file = event.target.files?.[0];
                setControlReceipt(null);
                setControlText('');
                if (!file) return;
                if (file.size > 32 * 1024 * 1024)
                  throw new Error('invalidSettings');
                setControlText(await file.text());
              }),
          }),
        ),
        button(
          t.controlImport,
          () =>
            run(async () => {
              const result = await call('auriga/settings/network-control', {
                packageJson: controlText,
              });
              setControlReceipt(result.receipt);
              setNotice(t.controlSaved);
            }),
          !controlText || !state?.networkControl?.available,
        ),
        !state?.networkControl?.available && h('p', null, t.controlMissing),
        controlReceipt &&
          h(
            'a',
            {
              download: 'auriga-control-receipt.json',
              href:
                'data:application/json;charset=utf-8,' +
                encodeURIComponent(JSON.stringify(controlReceipt)),
            },
            t.controlReceipt,
          ),
        row(t.networkTitle, statusError || networkText),
        (network?.address ?? account?.connection?.endpointId) &&
          row(t.endpoint, network?.address ?? account?.connection?.endpointId),
        row(
          t.received,
          String(account?.connection?.received ?? network?.received ?? 0),
        ),
        loggedIn && receiveControls,
        loggedIn && account?.capabilities?.consent === true && consent,
      ),
      h(
        'details',
        null,
        h('summary', null, t.diagnostics),
        (loggedIn || recovering) &&
          account?.account?.subject &&
          row(t.memberId, account.account.subject),
        row(t.protocol, String(boot?.protocol ?? t.notProvided)),
        row(t.version, boot?.pluginVersion ?? t.notProvided),
        row(t.source, boot?.source ?? t.notProvided),
        row(t.model, modelText),
        row(
          t.reason,
          network?.reason ?? account?.communication ?? t.notProvided,
        ),
      ),
      h(PrivateSkillsView, { locale }),
      notice && h('p', { role: 'status', 'aria-live': 'polite' }, notice),
    );
  };
}
