// @ts-nocheck
/* global TextDecoder, document */

/** Serialized into the real native index before its entry module. No token,
 * owner credential or member selector is ever shipped to the browser. */
export function installBrowserTransport() {
  const base = new URL('./', document.baseURI);
  const fetcher = globalThis.fetch.bind(globalThis);
  async function send(kind, channel, endpoint, payload, signal) {
    if (channel !== '/api') throw new Error('共享入口不提供此接口');
    const response = await fetcher(
      new URL(`auriga/shared/browser/${kind}`, base),
      {
        method: 'POST',
        credentials: 'same-origin',
        cache: 'no-store',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ channel, endpoint, payload }),
        signal,
      },
    );
    if (!response.ok) throw new Error(`共享连接不可用 (${response.status})`);
    return response;
  }
  const rpc = {
    async call(channel, endpoint, payload, signal) {
      const response = await send('rpc', channel, endpoint, payload, signal);
      const result = await response.json();
      signal?.throwIfAborted();
      if (typeof result?.ok !== 'boolean') throw new Error('共享响应无效');
      return result;
    },
    async *open(channel, endpoint, payload, signal) {
      const response = await send('stream', channel, endpoint, payload, signal);
      if (!response.body) throw new Error('共享流不可用');
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let ended = false;
      try {
        while (!ended) {
          const read = await reader.read();
          signal?.throwIfAborted();
          if (read.done) throw new Error('共享流已中断');
          buffer += decoder.decode(read.value, { stream: true });
          if (buffer.length > 8388608) throw new Error('共享流帧过大');
          let index;
          while ((index = buffer.indexOf('\n')) !== -1) {
            const frame = JSON.parse(buffer.slice(0, index));
            buffer = buffer.slice(index + 1);
            signal?.throwIfAborted();
            if (frame.type === 'item') yield frame.value;
            else if (frame.type === 'end') {
              ended = true;
              break;
            } else if (frame.type === 'error') {
              const error = new Error(frame.error?.message ?? '共享流不可用');
              error.dshRemoteStreamFailure = {
                kind: 'remote',
                code: frame.error?.code ?? 'shared.revoked',
                details: frame.error?.details ?? {},
              };
              throw error;
            } else throw new Error('共享流帧无效');
          }
        }
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
    },
  };
  // Connection's documented composition input; Gateway uses rpc.open instead
  // of its default owner-cookie WebSocket mux.
  globalThis.__DSH_TRANSPORT__ = { rpc, ownsHost: false };
  // Shared members have one fixed Workspace: force the sidebar into the flat
  // session list so "工作区" grouping chrome never appears.
  try {
    const key = 'dsh.workspace.view.v5';
    const raw = globalThis.localStorage?.getItem(key);
    const view =
      raw === undefined || raw === null
        ? {
            groupBy: 'flat',
            orderBy: 'updated',
            groupExpansion: {},
            sessionOrderByAccount: {},
            archivedFilter: 'default',
          }
        : { ...JSON.parse(raw), groupBy: 'flat' };
    globalThis.localStorage?.setItem(key, JSON.stringify(view));
  } catch {
    // Persistence is best-effort; AutoBind below still opens the fixed Workspace.
  }
  // Hide the hero workspace chip and model selector; keep agent preset / permission.
  try {
    const style = document.createElement('style');
    style.setAttribute('data-auriga-shared-chrome', '');
    style.textContent = `
button[aria-label="选择工作区"],button[aria-label="Choose workspace"]{display:none!important}
button[aria-label="添加工作区"],button[aria-label="Add workspace"]{display:none!important}
[data-row-key^="workspace:"]{display:none!important}
[data-slot="conversation.input.model"]{display:none!important}
`;
    document.documentElement.appendChild(style);
  } catch {
    // Style injection is best-effort in non-DOM fixtures.
  }
  globalThis.__AURIGA_SHARED_BROWSER__ = {
    rpc,
    installMessaging(ctx, React, primitives) {
      const h = React.createElement;
      const Button = primitives.Button ?? 'button';
      const id = 'auriga-shared-messages';
      const read = async (endpoint, request, signal) => {
        const result = await rpc.call(
          '/api',
          `aurigaShared/${endpoint}`,
          { args: request === undefined ? {} : { request } },
          signal,
        );
        if (!result.ok) throw new Error('消息设置暂不可用，请重试');
        return result.value;
      };
      function Messages() {
        const [sessions, setSessions] = React.useState([]);
        const [sessionId, select] = React.useState('');
        const [value, setValue] = React.useState();
        const [error, setError] = React.useState('');
        const [busy, setBusy] = React.useState(false);
        const revision = React.useRef(0);
        React.useEffect(() => {
          const abort = new AbortController();
          read('messagingSessions', undefined, abort.signal)
            .then((rows) => {
              if (!abort.signal.aborted) {
                setSessions(rows);
                select(rows[0]?.sessionId ?? '');
              }
            })
            .catch(() => {
              if (!abort.signal.aborted)
                setError('暂时无法读取会话，请重新打开');
            });
          return () => abort.abort();
        }, []);
        React.useEffect(() => {
          const abort = new AbortController();
          const current = ++revision.current;
          setValue(undefined);
          setError('');
          setBusy(false);
          if (sessionId)
            read('messagingSettings', { sessionId }, abort.signal)
              .then((next) => {
                if (revision.current === current && !abort.signal.aborted)
                  setValue(next);
              })
              .catch(() => {
                if (revision.current === current && !abort.signal.aborted)
                  setError('暂时无法读取设置，请重试');
              });
          return () => {
            revision.current++;
            abort.abort();
          };
        }, [sessionId]);
        async function toggle() {
          if (busy || !value) return;
          const current = ++revision.current;
          setBusy(true);
          setError('');
          try {
            const next = await read('messagingSettings', {
              sessionId,
              enabled: !value.desiredReceiveEnabled,
            });
            if (revision.current === current) setValue(next);
          } catch {
            if (revision.current === current) setError('设置未完成，请重试');
          } finally {
            if (revision.current === current) setBusy(false);
          }
        }
        return h(
          'section',
          { 'aria-label': '消息接收' },
          h('h2', null, '消息接收'),
          h(
            'p',
            null,
            h(
              'a',
              { href: '/resources', target: '_blank', rel: 'noopener' },
              '管理我的 DSH 资源',
            ),
          ),
          sessions.length
            ? h(
                'label',
                null,
                '本人会话 ',
                h(
                  'select',
                  {
                    value: sessionId,
                    onChange: (event) => select(event.target.value),
                  },
                  ...sessions.map((row) =>
                    h(
                      'option',
                      { key: row.sessionId, value: row.sessionId },
                      row.label,
                    ),
                  ),
                ),
              )
            : h('p', null, '新建会话后，可在这里设置消息接收。'),
          value &&
            h(
              React.Fragment,
              null,
              h(
                'p',
                null,
                '会话地址：',
                h('code', null, value.address ?? '暂不可用'),
              ),
              h(
                'p',
                { role: 'status' },
                value.pending
                  ? '正在同步接收设置'
                  : value.receiveEnabled
                    ? '已开启接收'
                    : '接收已关闭',
              ),
              h(
                Button,
                {
                  disabled:
                    busy || (!value.available && !value.desiredReceiveEnabled),
                  onClick: toggle,
                },
                value.desiredReceiveEnabled ? '关闭接收' : '开启接收',
              ),
              h(
                'p',
                null,
                '仅在你开启后接收消息。同步状态未完成时，请稍后重新打开查看。',
              ),
            ),
          error && h('p', { role: 'alert' }, error),
        );
      }
      const registrations = [];
      const add = (name, callback) => ctx.slots.inject(name, callback);
      registrations.push(
        add('sidebar.panellist', () =>
          ctx.slots.register(
            {
              name: 'sidebar.panellist',
              id,
              label: () => '消息接收',
            },
            ({ size }) =>
              h(primitives.IconUsersOutlineRegular ?? 'span', {
                size,
                'aria-hidden': true,
              }),
          ),
        ),
      );
      registrations.push(
        add('main', () =>
          ctx.slots.register({ name: 'main', key: id }, Messages),
        ),
      );
      // Auto-pick the sole fixed Workspace so the hero input is never inert;
      // the shipped WorkspacePicker is shadowed (priority -1). Shared-only.
      function AutoBindWorkspace({ open, onPick, useWorkspaces }) {
        const items = useWorkspaces((state) => state.items);
        const picked = React.useRef(false);
        React.useEffect(() => {
          if (picked.current || items.length === 0) return;
          picked.current = true;
          onPick(items[0].workspaceId);
        }, [items, onPick]);
        React.useEffect(() => {
          if (picked.current || !open || items.length === 0) return;
          picked.current = true;
          onPick(items[0].workspaceId);
        }, [open, items, onPick]);
        return null;
      }
      registrations.push(
        add('conversation.hero.workspace', () =>
          ctx.slots.register(
            { name: 'conversation.hero.workspace', priority: -1 },
            AutoBindWorkspace,
          ),
        ),
      );
      // Model is fixed to auto by install-side rotate; hide the composer control.
      registrations.push(
        add('conversation.input.model', () =>
          ctx.slots.register(
            { name: 'conversation.input.model', priority: -1 },
            () => null,
          ),
        ),
      );
      ctx.effect(
        () => () => {
          revisionCleanup();
        },
        'auriga-shared: messages',
      );
      function revisionCleanup() {
        for (const detach of registrations.reverse()) detach?.();
      }
    },
  };
}

export const browserBootstrap = `(${installBrowserTransport.toString()})();`;
