/* global AbortSignal */
/** Native React content loaded from the plugin's authenticated asset route. */
const messages = {
  zh: {
    connection: '总部连接',
    address: '平台地址',
    save: '保存',
    test: '测试连接',
    saved: '设置已保存',
    reachable: '平台接口可达；不代表已登录或模型已授权。',
    unconfigured: '尚未配置平台连接',
    connected: 'Team 已连接；模型授权需单独确认。',
    disconnected: '通信未就绪；身份登录与非通信功能可继续使用。',
    pending: '设置已保存，但连接暂未生效，请确认平台可用后重新保存。',
    reauthenticate: '平台已更换，请重新登录。',
    login: '登录',
    security: '账号与安全',
    identity: '当前身份',
    device: '设备名',
    loginTime: '登录时间',
    missing: '当前登录接口未提供',
    signedOut: '未登录',
    statusUnavailable: '登录状态暂时无法读取，请重试。',
    refresh: '刷新设置与登录状态',
    logout: '退出本机登录',
    portal: '在门户管理账号安全',
    tools: '工具',
    deployment: '默认部署位置',
    local: '本机',
    cloud: '云端',
    toolsHint: '已保存默认位置；工具创建页尚未读取此设置。',
    unavailable: '设置服务不可用，请重试。',
    auth: '登录状态不可用，请重新登录。',
    forbidden: '无权执行此操作。',
    invalidSettings: '设置无效。',
    invalidOrigin: '请填写纯平台地址，不含路径、凭据、查询或片段。',
    httpsRequired: '请使用 HTTPS；HTTP 仅允许回环或明确的内网 IP。',
    network: '网络、DNS 或 TLS 连接失败，或平台返回了重定向。',
    timeout: '连接测试超时。',
    invalidPlatform: '响应不是有效的平台 JWKS 接口。',
    http: '平台接口返回 HTTP ',
    busy: '正在处理…',
  },
  en: {
    connection: 'Platform connection',
    address: 'Platform address',
    save: 'Save',
    test: 'Test connection',
    saved: 'Settings saved',
    reachable:
      'Platform API reachable; sign-in and model authorization are separate.',
    unconfigured: 'Platform connection is not configured',
    connected: 'Team connected; model authorization is separate.',
    disconnected:
      'Communication is not ready; sign-in and non-communication features remain available.',
    pending:
      'Settings saved, but the connection could not be applied. Check the platform and save again.',
    reauthenticate: 'Platform changed. Please sign in again.',
    login: 'Sign in',
    security: 'Account and security',
    identity: 'Current identity',
    device: 'Device name',
    loginTime: 'Sign-in time',
    missing: 'Not provided by the current sign-in API',
    signedOut: 'Signed out',
    statusUnavailable: 'Sign-in status is unavailable. Please retry.',
    refresh: 'Refresh settings and sign-in status',
    logout: 'Sign out locally',
    portal: 'Manage account security in portal',
    tools: 'Tools',
    deployment: 'Default deployment',
    local: 'Local',
    cloud: 'Cloud',
    toolsHint: 'Preference saved; tool creation does not consume it yet.',
    unavailable: 'Settings service unavailable. Please retry.',
    auth: 'Sign-in unavailable. Please sign in again.',
    forbidden: 'Permission denied.',
    invalidSettings: 'Invalid settings.',
    invalidOrigin:
      'Enter a platform origin without a path, credentials, query or fragment.',
    httpsRequired:
      'HTTPS required; HTTP only allows loopback or explicit private IPs.',
    network: 'Network, DNS or TLS failure, or a refused redirect.',
    timeout: 'Connection test timed out.',
    invalidPlatform: 'Response is not a valid platform JWKS endpoint.',
    http: 'Platform API returned HTTP ',
    busy: 'Working…',
  },
};

/** @param {any} React @param {{localURL:(path:string)=>string,fetcher?:typeof fetch,navigate:(url:string)=>void}} ports */
export function createSettingsView(
  React,
  { localURL, fetcher = fetch, navigate },
) {
  const h = React.createElement;
  /** @param {string} path @param {unknown} [body] */
  async function call(path, body) {
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
    const data = await response.json();
    if (!response.ok)
      throw new Error(
        response.status === 401
          ? 'auth'
          : response.status === 403
            ? 'forbidden'
            : (data.reason ?? 'unavailable'),
      );
    return data;
  }
  return function SettingsView(/** @type {{locale:string}} */ { locale }) {
    const t = messages[locale?.startsWith('zh') ? 'zh' : 'en'];
    const [state, setState] = React.useState(null);
    const [account, setAccount] = React.useState(null);
    const [address, setAddress] = React.useState('');
    const [deployment, setDeployment] = React.useState('cloud');
    const [notice, setNotice] = React.useState('');
    const [busy, setBusy] = React.useState(false);
    const [revision, reload] = React.useReducer(
      (/** @type {number} */ n) => n + 1,
      0,
    );
    const explain = (/** @type {any} */ error) =>
      t[/** @type {keyof typeof t} */ (error?.message)] ?? t.unavailable;
    React.useEffect(() => {
      let live = true;
      setBusy(true);
      void Promise.all([
        call('auriga/settings/state'),
        call('auriga/local-login/status')
          .then((identity) => {
            if (
              !identity ||
              typeof identity.loggedIn !== 'boolean' ||
              (identity.loggedIn &&
                (typeof identity.connected !== 'boolean' ||
                  typeof identity.account?.subject !== 'string' ||
                  !identity.account.subject))
            )
              throw new Error('unavailable');
            return identity;
          })
          .catch(() => null),
      ])
        .then(([next, identity]) => {
          if (!live) return;
          setState(next);
          setAddress(next.platformOrigin);
          setDeployment(next.deployment);
          setAccount(identity);
          setNotice('');
        })
        .catch((error) => {
          if (live) setNotice(explain(error));
        })
        .finally(() => {
          if (live) setBusy(false);
        });
      return () => {
        live = false;
      };
    }, [revision]);
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
    const row = (/** @type {string} */ label, /** @type {any} */ value) =>
      h('div', { className: 'auriga-setting' }, h('span', null, label), value);
    const controlStyle = {
      font: 'inherit',
      color: 'inherit',
      background: 'var(--dsw-alias-bg-layer-1,transparent)',
      border: '1px solid var(--dsw-alias-border-l2,currentColor)',
      borderRadius: 6,
      padding: 6,
      maxWidth: '100%',
    };
    return h(
      'div',
      null,
      h('h3', null, t.connection),
      h(
        'p',
        { role: 'status' },
        !state?.activeOrigin
          ? t.unconfigured
          : account === null
            ? t.statusUnavailable
            : account?.loggedIn === true && account?.connected === true
              ? t.connected
              : t.disconnected,
      ),
      row(
        t.address,
        h('input', {
          'aria-label': t.address,
          value: address,
          disabled: busy || !state,
          style: controlStyle,
          onChange: (/** @type {any} */ event) =>
            setAddress(event.target.value),
        }),
      ),
      h(
        'button',
        {
          type: 'button',
          disabled: busy || !state || !address.trim(),
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
                    : (t[/** @type {keyof typeof t} */ (result.reason)] ??
                      t.unavailable),
              );
            }),
        },
        t.test,
      ),
      state &&
        state.platformOrigin !== state.activeOrigin &&
        h('p', null, t.pending),
      h('h3', null, t.security),
      row(
        t.identity,
        account === null
          ? t.statusUnavailable
          : account?.loggedIn === true
            ? (account.account?.subject ?? t.missing)
            : t.signedOut,
      ),
      state?.activeOrigin &&
        account?.loggedIn === false &&
        h(
          'button',
          {
            type: 'button',
            disabled: busy,
            onClick: () => navigate(localURL('auriga/local-login')),
          },
          t.login,
        ),
      row(
        t.device,
        account?.loggedIn === true
          ? (account.deviceName ?? state?.deviceName ?? t.missing)
          : '—',
      ),
      row(
        t.loginTime,
        account?.loggedIn === true ? (account.loginTime ?? t.missing) : '—',
      ),
      account?.loggedIn === true &&
        h(
          'button',
          {
            type: 'button',
            disabled: busy,
            onClick: () =>
              run(async () => {
                const fresh = await call('auriga/local-login/status');
                const response = await fetcher(
                  localURL('auriga/local-login/logout'),
                  {
                    method: 'POST',
                    credentials: 'same-origin',
                    redirect: 'error',
                    headers: {
                      'content-type': 'application/json',
                      'X-Auriga-CSRF': fresh.csrf,
                    },
                    body: '{}',
                  },
                );
                if (!response.ok) throw new Error('auth');
                setAccount(null);
                navigate(localURL('auriga/local-login'));
              }),
          },
          t.logout,
        ),
      state?.platformOrigin &&
        h(
          'a',
          {
            href: state.platformOrigin + '/',
            target: '_blank',
            rel: 'noopener noreferrer',
          },
          t.portal,
        ),
      h('h3', null, t.tools),
      row(
        t.deployment,
        h(
          'select',
          {
            'aria-label': t.deployment,
            value: deployment,
            disabled: busy || !state,
            style: controlStyle,
            onChange: (/** @type {any} */ event) =>
              setDeployment(event.target.value),
          },
          h('option', { value: 'local' }, t.local),
          h('option', { value: 'cloud' }, t.cloud),
        ),
      ),
      h('p', null, t.toolsHint),
      h(
        'button',
        {
          type: 'button',
          disabled: busy || !state,
          onClick: () =>
            run(async () => {
              const next = await call('auriga/settings/save', {
                platformOrigin: address,
                deployment,
              });
              setState(next);
              setAddress(next.platformOrigin);
              const changed = next.platformOrigin !== state.platformOrigin;
              const applied = next.platformOrigin === next.activeOrigin;
              if (changed && applied)
                setAccount({ loggedIn: false, connected: false });
              setNotice(changed && applied ? t.reauthenticate : t.saved);
              if (changed && applied && next.activeOrigin)
                navigate(localURL('auriga/local-login'));
            }),
        },
        t.save,
      ),
      h(
        'button',
        {
          type: 'button',
          disabled: busy,
          onClick: reload,
          'aria-label': t.refresh,
        },
        '↻',
      ),
      h('p', { role: 'status', 'aria-live': 'polite' }, busy ? t.busy : notice),
    );
  };
}
