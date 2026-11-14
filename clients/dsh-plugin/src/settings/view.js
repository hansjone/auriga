/* global AbortSignal, document */
/** Native React settings. Status is supplied by the authenticated runtime. */
const messages = {
  zh: {
    desktop: '桌面更新',
    update: '更新',
    checkUpdate: '检查更新',
    autoCheck: '自动检查',
    updateRestart:
      '下载后请关闭 Auriga Desktop，退出时安装，下次启动使用新版。',
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
    local: '本地可正常使用，平台功能暂停',
    managed: '系统托管',
    managedExpired: '实例会话已失效，请从门户重新进入',
    managedVerifying: '正在验证实例会话',
    memberLoading: '正在读取成员资料',
    networkTitle: '消息网络',
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
    invalidPlatform: '此地址不是 Auriga 平台',
    http: '平台返回 HTTP ',
    tools: '平台工具',
    notProvided: '未提供',
    expired: '平台登录已过期',
    retry: '重试',
  },
  en: {
    desktop: 'Desktop updates',
    update: 'Update',
    checkUpdate: 'Check for updates',
    autoCheck: 'Check automatically',
    updateRestart:
      'After download, close Auriga Desktop to install. Reopen to use the new version.',
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
    local: 'Local DSH available; platform features paused',
    managed: 'System managed',
    managedExpired: 'Instance session has expired. Re-enter from the portal.',
    managedVerifying: 'Verifying instance session',
    memberLoading: 'Loading member profile',
    networkTitle: 'Message network',
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
    invalidPlatform: 'Not an Auriga platform',
    http: 'Platform returned HTTP ',
    tools: 'Platform tools',
    notProvided: 'Not supplied',
    expired: 'Platform sign-in expired',
    retry: 'Retry',
  },
};

/** @param {any} React @param {{localURL:(path:string)=>string,fetcher?:typeof fetch,navigate:(url:string)=>void}} ports */
export function createSettingsView(
  React,
  { localURL, fetcher = fetch, navigate: _navigate },
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
    /** @type {string|undefined} */ csrf = undefined,
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
          : {
              'content-type': 'application/json',
              ...(csrf ? { 'x-auriga-csrf': csrf } : {}),
              ...(path.startsWith('auriga/resources/catalog/')
                ? { 'x-auriga-catalog': '1' }
                : { 'X-Auriga-Settings': '1' }),
            },
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
        try {
          const next = await call('auriga/settings/state');
          const identity = await call('auriga/shell/status').catch((error) => ({
            readError: explain(error),
          }));
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
    const summary =
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
      await call('auriga/shell/command', { action }, account?.csrf);
      await reload();
    };
    const enter = () => {
      void shellCommand('open_login').catch((error) =>
        setNotice(explain(error)),
      );
    };
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
          : recovering
            ? button(t.retry, reload)
            : !loggedIn
              ? button(t.login, enter)
              : button(t.refresh, reload);
    const logout = () => {
      void shellCommand('logout').catch((error) => setNotice(explain(error)));
    };
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
        state?.desktop &&
        h(
          'section',
          null,
          h('h3', null, t.desktop),
          h(
            'label',
            null,
            h('input', {
              type: 'checkbox',
              checked: state.desktop.enabled,
              disabled: busy,
              onChange: (/** @type {any} */ e) =>
                run(async () => {
                  await call('auriga/settings/desktop', {
                    enabled: e.target.checked,
                  });
                  await readState();
                }),
            }),
            t.autoCheck,
          ),
          state.desktop.available &&
            h('p', null, state.desktop.available.version),
          h('p', null, t.updateRestart),
          button(t.checkUpdate, () =>
            run(async () => {
              await call('auriga/settings/desktop', { action: 'check' });
              await readState();
            }),
          ),
          state.desktop.available &&
            !state.desktop.restartRequired &&
            button(t.update, () =>
              run(async () => {
                await call('auriga/settings/desktop', { action: 'update' });
                await readState();
              }),
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
      h(
        'section',
        { 'aria-label': t.account },
        h('h3', null, t.account),
        row(
          t.identity,
          managed
            ? name
            : statusError || (loggedIn || recovering ? name : t.signedOut),
        ),
        managed
          ? h('p', null, t.managed)
          : h(
              'div',
              null,
              (loggedIn || recovering) &&
                (account.deviceName ?? state?.deviceName) &&
                row(t.device, account.deviceName ?? state?.deviceName),
              account.uacLoginInfo?.status === 'available' &&
                row(t.department, account.uacLoginInfo.department),
              (loggedIn || recovering) &&
                account.loginTime &&
                row(
                  t.loginTime,
                  new Date(account.loginTime).toLocaleString(locale),
                ),
              !statusError &&
                (loggedIn
                  ? button(t.logout, logout)
                  : configured && !recovering
                    ? button(t.login, enter)
                    : null),
            ),
      ),
      h(
        'section',
        { 'aria-label': t.networkTitle },
        h('h3', null, t.networkTitle),
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
