/** Native DSH main surface and independent settings.section content. */
// @ts-nocheck — browser ModuleLoader globals
window.__ModuleLoader__.load({
  id: 'auriga-dsh/client',
  factory(require) {
    const BRAND_NAME = 'Auriga DSH';
    const React = require('react');
    const {
      IconUsersOutlineRegular,
      IconUsersOutlineMedium,
    } = require('@deepseek-ai/dsh-client-ui-primitives');
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
      panel: BRAND_NAME,
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
        '在 DSH 会话中使用 auriga_list_endpoints、auriga_open_thread、auriga_reply_thread 和 auriga_read_thread。接收消息会创建真实 DSH 会话；退出登录不会停止已有任务。',
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
      same_org: '同组织自动允许',
      allowlist: '仅白名单自动允许',
      deny_all: '全部拒绝',
      locked: '由组织政策锁定，来源版本：',
      persistent: '总是允许名单',
      blocked: '屏蔽名单',
      revoke: '撤销',
      empty: '暂无待处理请求',
      noRules: '暂无规则',
      revokeHint: '撤销后重新核权；已执行的动作无法撤回，拒绝冷却仍然有效。',
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
      panel: BRAND_NAME,
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
        'Use auriga_list_endpoints, auriga_open_thread, auriga_reply_thread and auriga_read_thread in a DSH conversation. Incoming messages create real DSH sessions. Signing out does not stop existing tasks.',
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
      same_org: 'Automatically allow same organization',
      allowlist: 'Automatically allow listed sources only',
      deny_all: 'Deny all',
      locked: 'Locked by organization policy, source revision: ',
      persistent: 'Always-allow list',
      blocked: 'Block list',
      revoke: 'Revoke',
      empty: 'No pending requests',
      noRules: 'No rules',
      revokeHint:
        'Revocation requires authorization again. Completed actions cannot be undone; rejection cooldowns remain active.',
    };
    const css = `
.auriga-shell{box-sizing:border-box;color:var(--dsw-alias-label-primary,inherit);font:inherit;min-width:0}
.auriga-shell button{font:inherit;padding:5px 10px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:6px;background:var(--dsw-alias-bg-layer-1,transparent);color:var(--dsw-alias-label-primary,inherit);cursor:pointer}
.auriga-shell button:hover{background:var(--dsw-alias-interactive-bg-hover,transparent)}
.auriga-shell button:focus-visible,.auriga-shell summary:focus-visible{outline:var(--dsw-focus-ring-width,2px) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary,currentColor));outline-offset:2px}
.auriga-shell button:disabled{opacity:.5;cursor:default}
.auriga-shell-status{padding:14px 0;display:flex;gap:12px;align-items:center;flex-wrap:wrap}
.auriga-settings{padding:4px 4px 24px;font-size:13px;line-height:1.5}
.auriga-settings h2{font-size:20px;font-weight:600;margin:0 0 24px}
.auriga-settings h3{font-size:14px;font-weight:600;margin:24px 0 0}
.auriga-settings p{color:var(--dsw-alias-label-secondary,inherit);font-size:12px;margin:8px 0 16px;overflow-wrap:anywhere}
.auriga-setting{padding:14px 0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor);display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.auriga-setting>span:first-child{flex:1}.auriga-setting-value{color:var(--dsw-alias-label-secondary,inherit);overflow-wrap:anywhere}
.auriga-settings-content{max-width:760px;display:grid;gap:24px}.auriga-settings-content h3{margin:0 0 4px}.auriga-status-card{border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:10px;padding:16px;display:flex;align-items:center;gap:12px;flex-wrap:wrap}.auriga-status-card p:first-child{font-size:14px;font-weight:600;flex:1;min-width:220px;color:inherit}.auriga-status-card p{margin:0}.auriga-status-card p+ p{flex-basis:100%;order:2}.auriga-setting-actions{display:flex;gap:8px;margin:12px 0}.auriga-setting input{font:inherit;color:inherit;background:var(--dsw-alias-bg-layer-1,transparent);border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:6px;padding:8px;min-width:0;flex:2}.auriga-settings details{display:block}.auriga-settings summary{cursor:pointer}.auriga-settings details p{margin-bottom:0}
.auriga-consent{padding:16px 0;min-width:0;overflow:auto}
.auriga-consent article{padding:16px 0;border-bottom:1px solid var(--dsw-alias-border-l1,currentColor)}
.auriga-consent pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;max-height:12em;overflow:auto;unicode-bidi:plaintext}
.auriga-consent select{font:inherit;max-width:100%;padding:6px 9px;border:1px solid var(--dsw-alias-border-l2,currentColor);border-radius:6px;background:var(--dsw-alias-bg-layer-1,transparent);color:var(--dsw-alias-label-primary,inherit)}
.auriga-consent select:focus-visible{outline:2px solid var(--dsw-alias-label-secondary,currentColor)}
.auriga-consent-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.auriga-consent p{overflow-wrap:anywhere}.auriga-consent small{color:var(--dsw-alias-label-secondary,inherit)}
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
            const status = await fetch(localURL('auriga/local-login/status'), {
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
            throw Object.assign(failure(reason), { reason });
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
            const controller = new AbortController();
            setPhase({ status: 'loading' });
            requestBootstrap(controller.signal)
              .then(async (boot) => {
                if (panel && alive && !disposed)
                  await ensurePanel(boot, controller.signal);
                if (alive && !disposed) setPhase({ status: 'ready', boot });
              })
              .catch((error) => {
                if (alive && !disposed)
                  setPhase({
                    status: 'error',
                    reason: error?.reason ?? 'unavailable',
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
            const status = await fetch(localURL('auriga/local-login/status'), {
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
            localURL('auriga/local-login/consent/' + action),
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
          const active = React.useRef(false);
          const busy = React.useRef(false);
          const generation = React.useRef(0);
          React.useEffect(() => {
            generation.current++;
            active.current = true;
            let current = true;
            setView(null);
            setStatus('checking');
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
                ? h(
                    'div',
                    null,
                    ...['conversation', 'execution'].map((layer) => {
                      const locked = view.organization[layer] !== 'allow';
                      return h(
                        'div',
                        { key: layer, className: 'auriga-setting' },
                        h(
                          'label',
                          null,
                          layerName(layer),
                          h(
                            'select',
                            {
                              'aria-label': layerName(layer),
                              disabled: locked,
                              value: locked
                                ? view.organization[layer]
                                : view.defaults[layer],
                              onChange: (event) => {
                                if (!locked)
                                  return change(() =>
                                    api.setDefault(layer, event.target.value),
                                  );
                              },
                            },
                            ...[
                              'ask_each',
                              'same_org',
                              'allowlist',
                              'deny_all',
                            ].map((mode) =>
                              h('option', { key: mode, value: mode }, t(mode)),
                            ),
                          ),
                        ),
                        locked &&
                          h(
                            'small',
                            null,
                            t('locked') + view.organization.revision,
                          ),
                      );
                    }),
                    ...['allow_source', 'block'].map((choice) => {
                      const rules = view.rules.filter(
                        (rule) => rule.choice === choice,
                      );
                      return h(
                        'details',
                        { key: choice, open: true },
                        h(
                          'summary',
                          null,
                          t(choice === 'block' ? 'blocked' : 'persistent'),
                        ),
                        rules.length === 0 && h('p', null, t('noRules')),
                        ...rules.map((rule) =>
                          h(
                            'div',
                            { key: rule.id, className: 'auriga-setting' },
                            h(
                              'span',
                              null,
                              layerName(rule.layer) +
                                ' · ' +
                                sourceName(rule.source) +
                                ' · ' +
                                t(rule.scope),
                            ),
                            h(
                              'button',
                              {
                                type: 'button',
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
                          ),
                        ),
                      );
                    }),
                    h('p', null, t('revokeHint')),
                  )
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
              className: 'auriga-consent',
              'aria-label': t(settings ? 'consent' : 'inbox'),
            },
            h('h3', null, t(settings ? 'consent' : 'inbox')),
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
        function Settings() {
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
              'aria-label': BRAND_NAME,
            },
            h('h2', null, BRAND_NAME),
            phase.status !== 'ready' && notice(phase, retry, true),
            View
              ? h(View, {
                  locale: host.locale,
                  boot,
                  consent: h(ConsentSection, { settings: true }),
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
        function Main() {
          const [desktop, desktopAction] = useDesktopUpdate();
          const host = useHostState();
          const [phase, retry] = useBootstrap(true);
          const [authRequired, setAuthRequired] = React.useState(false);
          const ref = React.useRef(null);
          const boot = phase.status === 'ready' ? phase.boot : undefined;
          React.useEffect(() => {
            const el = ref.current;
            if (!el || !boot) return;
            el.context = {
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
              resourceDistributionAvailable: boot.resourceDistributionAvailable,
            };
          }, [boot, host.locale, host.theme]);
          React.useEffect(() => {
            const el = ref.current;
            if (!el || !boot) return;
            const onNav = (ev) => navigate(ev.detail?.to);
            const onNotify = (ev) => notify(ev.detail);
            const onAuth = () => setAuthRequired(true);
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
          if (phase.status !== 'ready')
            return h(
              'div',
              { className: 'auriga-shell' },
              notice(phase, retry),
              h(ConsentSection),
            );
          return h(
            'div',
            {
              className: 'auriga-shell',
              style: {
                height: '100%',
                display: 'flex',
                flexDirection: 'column',
              },
            },
            desktop &&
              (desktop.available || desktop.restartRequired || desktop.error) &&
              h(
                'section',
                {
                  role: desktop.required ? 'alert' : 'status',
                  className: 'auriga-shell-status',
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
            authRequired &&
              h(
                'div',
                { role: 'status' },
                h('span', null, t('authRequired')),
                h(
                  'button',
                  { type: 'button', onClick: () => setAuthRequired(false) },
                  t('dismiss'),
                ),
              ),
            host.toast &&
              h(
                'div',
                { role: 'status', 'data-level': host.toast.level },
                host.toast.message,
              ),
            boot.connection &&
              h(
                'section',
                { className: 'auriga-shell-status' },
                h(
                  'span',
                  null,
                  t(boot.connection.connected ? 'connected' : 'disconnected'),
                ),
                h(
                  'button',
                  {
                    type: 'button',
                    disabled: !boot.connection.connected || desktop?.required,
                    onClick: () => navigate('session:new'),
                  },
                  t('conversation'),
                ),
              ),
            h(
              'details',
              null,
              h('summary', null, t('inbox')),
              h(ConsentSection),
            ),
            h(ELEMENT, {
              ref,
              style: {
                flex: 1,
                display: desktop?.required ? 'none' : undefined,
              },
            }),
          );
        }
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
        effect(
          () =>
            ctx.slots.inject('main', () =>
              ctx.slots.register(
                { name: 'main', key: PANEL_ID, locale: NS },
                Main,
              ),
            ),
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
        effect(
          () =>
            ctx.slots.inject('settings.section', () =>
              ctx.slots.register(
                {
                  name: 'settings.section',
                  id: PANEL_ID,
                  order: 22,
                  label: BRAND_NAME,
                  locale: NS,
                  icon: 'users',
                },
                Settings,
              ),
            ),
          'auriga-shell: settings',
        );
        const dispose = () => {
          if (disposed) return;
          disposed = true;
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
