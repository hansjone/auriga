/* global AbortSignal, document */
/** Native React settings. Status is supplied by the authenticated runtime. */
const messages = {
  zh: {
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
    device: '设备名',
    loginTime: '登录时间',
    missing: '当前凭据未提供',
    signedOut: '未登录',
    local: '本地可正常使用，平台功能暂停',
    managed: '系统托管',
    networkTitle: '消息网络',
    networkReady: '就绪',
    networkPending: '未就绪',
    network: '总部不可达',
    unauthorized: '未登录',
    unconfiguredNetwork: '未配置消息网络',
    joining: '正在连接',
    model: '模型',
    modelReady: '中转站可用',
    modelMissing: '中转站未就绪',
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
    invalidPlatform: '此地址不是 Auriga 平台',
    http: '平台返回 HTTP ',
    tools: '平台工具',
    managedIdentity: '托管成员凭据未提供姓名',
    notProvided: '未提供',
    expired: '平台登录已过期',
    retry: '重试',
  },
  en: {
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
    device: 'Device name',
    loginTime: 'Sign-in time',
    missing: 'Not supplied by current credential',
    signedOut: 'Signed out',
    local: 'Local DSH available; platform features paused',
    managed: 'System managed',
    networkTitle: 'Message network',
    networkReady: 'Ready',
    networkPending: 'Not ready',
    network: 'Platform unreachable',
    unauthorized: 'Signed out',
    unconfiguredNetwork: 'Message network not configured',
    joining: 'Connecting',
    model: 'Models',
    modelReady: 'Relay available',
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
    invalidPlatform: 'Not an Auriga platform',
    http: 'Platform returned HTTP ',
    tools: 'Platform tools',
    managedIdentity: 'Managed credential does not supply a member name',
    notProvided: 'Not supplied',
    expired: 'Platform sign-in expired',
    retry: 'Retry',
  },
};

/** @param {any} React @param {{localURL:(path:string)=>string,fetcher?:typeof fetch,navigate:(url:string)=>void}} ports */
export function createSettingsView(
  React,
  { localURL, fetcher = fetch, navigate },
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
        void fetcher(localURL('auriga/local-login/messaging/snapshot'), {
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
          localURL('auriga/local-login/messaging/receive'),
          {
            method: 'POST',
            credentials: 'same-origin',
            redirect: 'error',
            signal: AbortSignal.timeout(15000),
            headers: {
              'content-type': 'application/json',
              'x-auriga-csrf': account.csrf,
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
  ) {
    const response = await fetcher(localURL(path), {
      method: body === undefined ? 'GET' : 'POST',
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error',
      signal: AbortSignal.timeout(15000),
      headers:
        body === undefined
          ? {}
          : { 'content-type': 'application/json', 'X-Auriga-Settings': '1' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok)
      throw new Error(
        response.status === 401
          ? 'auth'
          : response.status === 403
            ? 'forbidden'
            : 'statusHttp:' + response.status,
      );
    try {
      return await response.json();
    } catch {
      throw new Error('invalidState');
    }
  }
  return function SettingsView(
    /** @type {{locale:string,boot?:any,consent?:any}} */ {
      locale,
      boot,
      consent,
    },
  ) {
    const t = messages[locale?.startsWith('zh') ? 'zh' : 'en'];
    const [state, setState] = React.useState(null),
      [account, setAccount] = React.useState(null);
    const [address, setAddress] = React.useState(''),
      [notice, setNotice] = React.useState(''),
      [statusError, setStatusError] = React.useState('');
    const [busy, setBusy] = React.useState(false);
    const [revision, reload] = React.useReducer(
      (/** @type {number} */ n) => n + 1,
      0,
    );
    const explain = (/** @type {any} */ error) =>
      error.message?.startsWith('statusHttp:')
        ? t.statusHttp + error.message.split(':')[1]
        : error.name === 'TimeoutError'
          ? t.timeout
          : error instanceof TypeError
            ? t.loadNetwork
            : (t[/** @type {keyof typeof t} */ (error.message)] ??
              t.unavailable);
    const readState = async () => {
      const next = await call('auriga/settings/state');
      setState(next);
      setAddress(next.platformOrigin ?? '');
      try {
        const identity = await call('auriga/local-login/status');
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
        try {
          const next = await call('auriga/settings/state');
          const identity = await call('auriga/local-login/status').catch(
            (error) => ({ readError: explain(error) }),
          );
          if (!live) return;
          setState(next);
          if (first) setAddress(next.platformOrigin ?? '');
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
    const network = account?.network;
    const ready = network?.connected === true;
    const name =
      account?.account?.displayName ??
      account?.account?.subject ??
      t.notProvided;
    const reason = !loggedIn
      ? account?.models?.reauthenticationRequired
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
    const modelText = account?.models?.available
      ? t.modelReady
      : t.modelMissing;
    const summary =
      statusError ||
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
    const enter = () => navigate(localURL('auriga/local-login'));
    const action = statusError
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
          : !loggedIn
            ? button(t.login, enter)
            : button(t.refresh, reload);
    const logout = () => navigate(localURL('auriga/local-login') + '?logout=1');
    const receiveControls = useReceiveSettings({ t, account, revision });
    return h(
      'div',
      { className: 'auriga-settings-content' },
      h(
        'section',
        { className: 'auriga-status-card', 'aria-label': 'Auriga status' },
        h('p', { role: 'status' }, summary),
        !managed && (!loggedIn || !ready) && h('p', null, t.local),
        action,
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
      h(
        'section',
        { 'aria-label': t.account },
        h('h3', null, t.account),
        row(t.identity, statusError || (loggedIn ? name : t.signedOut)),
        loggedIn &&
          account?.account?.displayName === undefined &&
          row(t.memberId, account.account.subject),
        managed
          ? h(
              'p',
              null,
              t.managed +
                (account?.account?.displayName
                  ? ''
                  : ' · ' + t.managedIdentity),
            )
          : h(
              'div',
              null,
              row(
                t.device,
                loggedIn
                  ? (account.deviceName ?? state?.deviceName ?? t.missing)
                  : t.signedOut,
              ),
              row(
                t.loginTime,
                loggedIn
                  ? account.loginTime
                    ? new Date(account.loginTime).toLocaleString(locale)
                    : t.missing
                  : t.signedOut,
              ),
              !statusError &&
                (loggedIn
                  ? button(t.logout, logout)
                  : configured
                    ? button(t.login, enter)
                    : null),
            ),
      ),
      h(
        'section',
        { 'aria-label': t.networkTitle },
        h('h3', null, t.networkTitle),
        row(t.networkTitle, statusError || networkText),
        row(
          t.endpoint,
          network?.address ?? account?.connection?.endpointId ?? t.notProvided,
        ),
        row(
          t.received,
          String(account?.connection?.received ?? network?.received ?? 0),
        ),
        loggedIn && receiveControls,
        loggedIn && consent,
      ),
      h(
        'details',
        null,
        h('summary', null, t.diagnostics),
        row(t.protocol, String(boot?.protocol ?? t.notProvided)),
        row(t.version, boot?.pluginVersion ?? t.notProvided),
        row(t.source, boot?.source ?? t.notProvided),
        row(t.model, modelText),
        row(
          t.reason,
          network?.reason ?? account?.communication ?? t.notProvided,
        ),
      ),
      notice && h('p', { role: 'status', 'aria-live': 'polite' }, notice),
    );
  };
}
