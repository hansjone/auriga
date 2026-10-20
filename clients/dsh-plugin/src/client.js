/**
 * Browser shell for the team panel (managed / connect).
 * Registers sidebar + main slots and mounts <auriga-team-panel>.
 * Loaded via package.json "dsh.client" / exports "./client".
 */
// @ts-nocheck — browser ModuleLoader globals
window.__ModuleLoader__.load({
  id: 'auriga-dsh/client',
  factory(require) {
    const React = require('react');
    const h = React.createElement;
    const NS = 'auriga.shell';
    const PANEL_ID = 'auriga-team';
    const ELEMENT = 'auriga-team-panel';

    const zh = {
      panel: '团队',
      loading: '正在加载团队面板…',
      failed: '团队面板加载失败',
      code: '原因代码',
      retry: '重试',
      authRequired: '需要重新登录平台',
      authOpen: '打开平台登录',
      dismiss: '关闭',
    };
    const en = {
      panel: 'Team',
      loading: 'Loading team panel…',
      failed: 'Team panel failed to load',
      code: 'Reason code',
      retry: 'Retry',
      authRequired: 'Platform sign-in required',
      authOpen: 'Open platform sign-in',
      dismiss: 'Dismiss',
    };

    function loadScript(src, integrity) {
      return new Promise((resolve, reject) => {
        const el = document.createElement('script');
        el.type = 'module';
        el.src = src;
        el.integrity = integrity;
        el.crossOrigin = 'anonymous';
        el.dataset.aurigaPanel = '';
        el.onload = () => resolve();
        el.onerror = () =>
          reject(
            Object.assign(new Error('panel script blocked or failed'), {
              code: 'common.unavailable',
            }),
          );
        document.head.append(el);
      });
    }

    let pending;
    function ensurePanel() {
      pending ??= (async () => {
        const response = await fetch(
          new URL('auriga/bootstrap', document.baseURI),
          { credentials: 'same-origin', cache: 'no-store' },
        );
        const body = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw Object.assign(
            new Error(
              body?.detail ?? body?.error?.message ?? 'bootstrap failed',
            ),
            { code: body?.code ?? body?.error?.code ?? 'common.unavailable' },
          );
        }
        const { manifest, panelBase } = body;
        if (!customElements.get(ELEMENT)) {
          await loadScript(
            new URL(panelBase + manifest.entry, document.baseURI).href,
            manifest.integrity[manifest.entry],
          );
          await Promise.race([
            customElements.whenDefined(ELEMENT),
            new Promise((_, reject) =>
              setTimeout(
                () =>
                  reject(
                    Object.assign(new Error('element not defined'), {
                      code: 'common.unavailable',
                    }),
                  ),
                10_000,
              ),
            ),
          ]);
        }
        return body;
      })().catch((error) => {
        pending = undefined;
        throw error;
      });
      return pending;
    }

    function makeFetch(boot) {
      const base = new URL(boot.fetchBase, document.baseURI);
      return (path, init) => {
        if (
          typeof path !== 'string' ||
          /^([a-z][a-z0-9+.-]*:|\/\/)/i.test(path)
        ) {
          return Promise.reject(
            new TypeError('context.fetch only accepts platform API paths'),
          );
        }
        return fetch(new URL(path.replace(/^\/+/, ''), base), {
          ...init,
          credentials: 'same-origin',
          redirect: 'manual',
        });
      };
    }

    return {
      inject: ['slots', 'locale'],
      apply(ctx) {
        ctx.effect?.(
          () => ctx.locale.register(NS, { zh, en }),
          'auriga-shell: dictionaries',
        );
        const t = ctx.locale.bind(NS);

        const dark = window.matchMedia('(prefers-color-scheme: dark)');
        const listeners = new Set();
        const state = {
          locale: ctx.locale.getLocale().active,
          theme: dark.matches ? 'dark' : 'light',
          toast: undefined,
        };
        const emit = () => listeners.forEach((fn) => fn());
        ctx.effect?.(
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
        ctx.effect?.(() => {
          dark.addEventListener('change', onScheme);
          return () => dark.removeEventListener('change', onScheme);
        }, 'auriga-shell: theme');

        const useHostState = () => {
          const [, bump] = React.useReducer((n) => n + 1, 0);
          React.useEffect(() => {
            listeners.add(bump);
            return () => {
              listeners.delete(bump);
            };
          }, []);
          return state;
        };

        const navigate = (to) => {
          if (to === 'session:new') {
            const workspace = ctx.get('uiWorkspace');
            if (workspace?.startSession) workspace.startSession();
          } else if (/^https?:\/\//i.test(to)) {
            window.open(to, '_blank', 'noopener,noreferrer');
          }
        };
        const notify = (detail) => {
          state.toast = {
            level: detail?.level ?? 'info',
            message: String(detail?.message ?? ''),
            at: Date.now(),
          };
          emit();
          setTimeout(() => {
            if (state.toast && Date.now() - state.toast.at >= 3900) {
              state.toast = undefined;
              emit();
            }
          }, 4000);
        };

        function Main() {
          const host = useHostState();
          const [phase, setPhase] = React.useState({ status: 'loading' });
          const [authRequired, setAuthRequired] = React.useState(false);
          const [attempt, setAttempt] = React.useState(0);
          const ref = React.useRef(null);

          React.useEffect(() => {
            let alive = true;
            setPhase({ status: 'loading' });
            ensurePanel().then(
              (boot) => {
                if (alive) setPhase({ status: 'ready', boot });
              },
              (error) => {
                if (alive)
                  setPhase({
                    status: 'error',
                    code: error?.code ?? 'unknown',
                    message: String(error?.message ?? error),
                  });
              },
            );
            return () => {
              alive = false;
            };
          }, [attempt]);

          const boot = phase.status === 'ready' ? phase.boot : undefined;

          React.useEffect(() => {
            const el = ref.current;
            if (!el || !boot) return;
            const context = {
              mode: boot.mode,
              protocol: boot.protocol,
              locale: host.locale,
              theme: host.theme,
              apiBase: boot.platformUrl || '',
              fetch: makeFetch(boot),
            };
            el.context = context;
            const onNav = (ev) => navigate(ev.detail?.to);
            const onNotify = (ev) => notify(ev.detail);
            const onAuth = () => setAuthRequired(true);
            el.addEventListener('auriga-navigate', onNav);
            el.addEventListener('auriga-notify', onNotify);
            el.addEventListener('auriga-auth-required', onAuth);
            return () => {
              el.removeEventListener('auriga-navigate', onNav);
              el.removeEventListener('auriga-notify', onNotify);
              el.removeEventListener('auriga-auth-required', onAuth);
            };
          }, [boot, host.locale, host.theme]);

          if (phase.status === 'loading') return h('div', null, t('loading'));
          if (phase.status === 'error') {
            return h('div', null, [
              h('p', { key: 'm' }, t('failed')),
              h('p', { key: 'c' }, `${t('code')}: ${phase.code}`),
              h(
                'button',
                {
                  key: 'r',
                  type: 'button',
                  onClick: () => setAttempt((n) => n + 1),
                },
                t('retry'),
              ),
            ]);
          }
          return h(
            'div',
            {
              style: {
                height: '100%',
                display: 'flex',
                flexDirection: 'column',
              },
            },
            [
              authRequired
                ? h('div', { key: 'a', role: 'status' }, [
                    h('span', { key: 't' }, t('authRequired')),
                    h(
                      'button',
                      {
                        key: 'd',
                        type: 'button',
                        onClick: () => setAuthRequired(false),
                      },
                      t('dismiss'),
                    ),
                  ])
                : null,
              host.toast
                ? h(
                    'div',
                    {
                      key: 'toast',
                      role: 'status',
                      'data-level': host.toast.level,
                    },
                    host.toast.message,
                  )
                : null,
              h(ELEMENT, { key: 'p', ref, style: { flex: 1 } }),
            ],
          );
        }

        ctx.effect?.(
          () =>
            ctx.slots.inject('sidebar.panellist', () =>
              ctx.slots.register(
                {
                  name: NS,
                  id: PANEL_ID,
                  locale: NS,
                  label: 'panel',
                },
                () => null,
              ),
            ),
          'auriga-shell: sidebar',
        );
        ctx.effect?.(
          () =>
            ctx.slots.inject('main', () =>
              ctx.slots.register({ name: NS, key: PANEL_ID }, Main),
            ),
          'auriga-shell: main',
        );
      },
    };
  },
});
