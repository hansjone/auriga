/** Native DSH main surface and independent settings.section content. */
// @ts-nocheck — browser ModuleLoader globals
window.__ModuleLoader__.load({
  id: 'auriga-dsh/client',
  factory(require) {
    const React = require('react');
    const h = React.createElement;
    const NS = 'auriga.shell';
    const PANEL_ID = 'auriga-team';
    const ELEMENT = 'auriga-team-panel';
    const loadedPanels = new WeakMap();
    const zh = {
      panel: 'Auriga',
      loading: '正在加载…',
      failed: '加载失败',
      retry: '重试',
      refresh: '刷新状态',
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
        '账号资料、认证关联、安全与协作偏好尚未接入 Auriga 设置；需迁移原有功能。',
      auth: '登录状态已失效，请重新登录后重试。',
      forbidden: '没有读取托管状态的权限。',
      missing: '当前宿主尚未接入 Auriga 托管设置。',
      unsupported: '协议不受支持，请升级插件。',
      verification: '界面清单验证未通过，请检查受信发行配置。',
      unavailable: '暂时无法加载，请稍后重试。',
    };
    const en = {
      panel: 'Auriga',
      loading: 'Loading…',
      failed: 'Failed to load',
      retry: 'Retry',
      refresh: 'Refresh status',
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
        'Account details, identity linking, security and collaboration preferences still require migration into Auriga settings.',
      auth: 'Your sign-in has expired. Sign in again and retry.',
      forbidden: 'You do not have permission to read managed status.',
      missing: 'This host has not connected Auriga managed settings.',
      unsupported: 'Unsupported protocol. Upgrade the plugin.',
      verification:
        'UI manifest verification failed. Check the trusted release configuration.',
      unavailable: 'Temporarily unavailable. Try again later.',
    };
    const css = `
.auriga-shell{box-sizing:border-box;color:var(--dsw-alias-label-primary);font:inherit;min-width:0}
.auriga-shell button{font:inherit;padding:5px 10px;border:1px solid var(--dsw-alias-border-l2);border-radius:6px;background:var(--dsw-alias-bg-layer-1);color:var(--dsw-alias-label-primary);cursor:pointer}
.auriga-shell button:hover{background:var(--dsw-alias-interactive-bg-hover)}
.auriga-shell button:focus-visible,.auriga-shell summary:focus-visible{outline:2px solid var(--dsw-alias-label-secondary);outline-offset:2px}
.auriga-shell button:disabled{opacity:.5;cursor:default}
.auriga-shell-status{padding:14px 0;display:flex;gap:12px;align-items:center;flex-wrap:wrap}
.auriga-settings{padding:4px 4px 24px;font-size:13px;line-height:1.5}
.auriga-settings h2{font-size:20px;font-weight:600;margin:0 0 24px}
.auriga-settings h3{font-size:14px;font-weight:600;margin:24px 0 0}
.auriga-settings p{color:var(--dsw-alias-label-secondary);font-size:12px;margin:8px 0 16px;overflow-wrap:anywhere}
.auriga-setting{padding:14px 0;border-bottom:1px solid var(--dsw-alias-border-l1);display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.auriga-setting>span:first-child{flex:1}.auriga-setting-value{color:var(--dsw-alias-label-secondary);overflow-wrap:anywhere}
.auriga-settings details{display:block}.auriga-settings summary{cursor:pointer}.auriga-settings details p{margin-bottom:0}
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
      return Object.assign(new Error('Auriga surface unavailable'), { reason });
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
          body.status !== 'unconfigured' ||
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
      };
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
          credentials: 'same-origin',
          redirect: 'manual',
        });
      };
    }
    return {
      inject: ['slots', 'locale'],
      apply(ctx) {
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
          () => ctx.locale.register(NS, { zh, en }),
          'auriga-shell: dictionaries',
        );
        const t = ctx.locale.bind(NS);
        const style = document.createElement('style');
        style.textContent = css;
        document.head.append(style);
        own(() => style.remove());
        const dark = window.matchMedia('(prefers-color-scheme: dark)');
        const listeners = new Set();
        const state = {
          locale: ctx.locale.getLocale().active,
          theme: dark.matches ? 'dark' : 'light',
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
        const onScheme = () => {
          state.theme = dark.matches ? 'dark' : 'light';
          emit();
        };
        effect(() => {
          dark.addEventListener('change', onScheme);
          return () => dark.removeEventListener('change', onScheme);
        }, 'auriga-shell: theme');
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
          const existing = customElements.get(ELEMENT);
          if (existing)
            return loadedPanels.get(existing) === identity
              ? Promise.resolve()
              : Promise.reject(failure('verification'));
          panelPending ??= new Promise((resolve, reject) => {
            const el = document.createElement('script');
            el.type = 'module';
            el.src = boot.entry;
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
            el.onload = () =>
              customElements.whenDefined(ELEMENT).then(() => {
                if (!scripts.has(el)) return;
                loadedPanels.set(customElements.get(ELEMENT), identity);
                finish();
              });
            el.onerror = () => finish(failure());
            el.cancel = () => finish(failure());
            signal.addEventListener('abort', el.cancel, { once: true });
            document.head.append(el);
          }).catch((error) => {
            panelPending = undefined;
            throw error;
          });
          return panelPending;
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
                      ? 'unconfigured'
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
        function Settings() {
          useHostState();
          const [phase, retry] = useBootstrap();
          const boot = phase.status === 'ready' ? phase.boot : undefined;
          const row = (key, value) =>
            h(
              'div',
              { className: 'auriga-setting', key },
              h('span', null, t(key)),
              h('span', { className: 'auriga-setting-value' }, value ?? '—'),
            );
          return h(
            'section',
            {
              className: 'auriga-shell auriga-settings',
              'aria-label': 'Auriga',
            },
            h('h2', null, 'Auriga'),
            h('h3', null, t('connection')),
            notice(phase, retry, true),
            boot && row('mode', t(boot.mode)),
            boot && row('protocol', String(boot.protocol)),
            h(
              'p',
              null,
              t(
                boot
                  ? boot.source === 'package'
                    ? 'packageHint'
                    : 'managedHint'
                  : 'unavailableHint',
              ),
            ),
            h('h3', null, t('tools')),
            row('deployment', t('notWired')),
            h('p', null, t('toolsHint')),
            h('h3', null, t('about')),
            row('pluginVersion', boot?.pluginVersion),
            row('panelVersion', boot?.panelVersion),
            boot && row('uiSource', t(boot.source)),
            h(
              'details',
              { className: 'auriga-setting' },
              h('summary', null, t('security')),
              h('p', null, t('securityHint')),
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
        function Main() {
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
            h(ELEMENT, { ref, style: { flex: 1 } }),
          );
        }
        effect(
          () =>
            ctx.slots.inject('sidebar.panellist', () =>
              ctx.slots.register(
                { name: NS, id: PANEL_ID, locale: NS, label: 'panel' },
                () => null,
              ),
            ),
          'auriga-shell: sidebar',
        );
        effect(
          () =>
            ctx.slots.inject('main', () =>
              ctx.slots.register({ name: NS, key: PANEL_ID }, Main),
            ),
          'auriga-shell: main',
        );
        effect(
          () =>
            ctx.slots.inject('settings.section', () =>
              ctx.slots.register(
                {
                  name: 'settings.section',
                  id: PANEL_ID,
                  order: 22,
                  label: 'Auriga',
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
