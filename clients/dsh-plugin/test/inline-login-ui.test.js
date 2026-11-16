import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setImmediate } from 'node:timers';
import { JSDOM } from 'jsdom';

// A React hook/element harness: tests actual registered component handlers.
// It does not pretend to implement native ModuleLoader resolution or rendering.
async function fixture(http, options = {}) {
  const api = undefined,
    locale = 'zh';
  const dom = new JSDOM('<!doctype html>', { url: 'http://127.0.0.1:12345/' });
  if (options.hash) dom.window.history.replaceState(null, '', options.hash);
  if (options.skipped)
    dom.window.sessionStorage.setItem('auriga.login.skipped', '1');
  Object.defineProperty(dom.window.navigator, 'locks', {
    value: options.locks ?? lockManager(),
  });
  const intervals = new Map();
  let intervalId = 0;
  let module;
  let values = [],
    effects = [],
    cursor = 0;
  const React = {
    createElement: (type, props, ...children) => ({
      type,
      props: props ?? {},
      children: children.flat(Infinity),
    }),
    useState(initial) {
      const i = cursor++;
      if (!(i in values))
        values[i] = typeof initial === 'function' ? initial() : initial;
      return [
        values[i],
        (v) => {
          values[i] = typeof v === 'function' ? v(values[i]) : v;
        },
      ];
    },
    useReducer(fn, initial) {
      const [value, set] = React.useState(initial);
      return [value, (action) => set((v) => fn(v, action))];
    },
    useRef(initial) {
      const [ref] = React.useState({ current: initial });
      return ref;
    },
    useEffect(fn, deps) {
      const i = cursor++;
      if (!values[i] || deps.some((v, j) => v !== values[i].deps[j])) {
        values[i]?.cleanup?.();
        const entry = { deps };
        values[i] = entry;
        effects.push(() => {
          entry.cleanup = fn();
        });
      }
    },
  };
  dom.window.__ModuleLoader__ = {
    load: (m) => {
      module = m;
    },
  };
  vm.runInNewContext(
    await readFile(new URL('../src/client.js', import.meta.url), 'utf8'),
    {
      window: dom.window,
      document: dom.window.document,
      URL,
      AbortController,
      customElements: dom.window.customElements,
      setTimeout,
      clearTimeout,
      setInterval: (fn, ms) => {
        const id = ++intervalId;
        intervals.set(id, { fn, ms });
        return id;
      },
      clearInterval: (id) => intervals.delete(id),
      fetch:
        http ??
        (async (url, init = {}) => {
          if (!api) return { ok: false, status: 503, json: async () => ({}) };
          if (String(url).endsWith('/status'))
            return {
              ok: true,
              json: async () => ({
                loggedIn: true,
                csrf: 'native-test-csrf',
                capabilities: { consent: true },
              }),
            };
          try {
            const input = init.body ? JSON.parse(init.body) : undefined;
            let result;
            if (String(url).endsWith('/snapshot'))
              result = await api.snapshot();
            else if (String(url).endsWith('/decide')) await api.decide(input);
            else if (String(url).endsWith('/default'))
              await api.setDefault(input.layer, input.mode);
            else if (String(url).endsWith('/revoke')) await api.revoke(input);
            return { ok: true, json: async () => result ?? { ok: true } };
          } catch (error) {
            return {
              ok: false,
              status: 503,
              json: async () => ({
                reason:
                  error.reason === 'cooldown' ? 'cooldown' : 'unavailable',
              }),
            };
          }
        }),
    },
  );
  const plugin = module.factory((name) => {
    if (name === 'react') return React;
    if (name === '@deepseek-ai/dsh-client-ui-primitives')
      return { Modal: function Modal() {} };
    throw new Error(name);
  });
  const slots = new Map();
  let dictionaries;
  const ctx = {
    locale: {
      register: (_ns, d) => {
        dictionaries = d;
      },
      bind: () => (k) => dictionaries[locale][k] ?? k,
      getLocale: () => ({ active: locale }),
    },
    theme: { getTheme: () => ({ active: { colorScheme: 'dark' } }) },
    slots: {
      inject: (_name, fn) => fn(),
      register: (options, component) => {
        slots.set(options.name, component);
      },
    },
    on: () => () => {},
    effect: (fn) => fn(),
    get: (name) => (name === 'aurigaConsentUI' ? api : undefined),
  };
  const dispose = plugin.apply(ctx);
  const shallow = (component, props = {}) => {
    cursor = 0;
    effects = [];
    return component(props);
  };
  const settings = shallow(slots.get('main'));
  const section = all(settings).find((n) => n.type?.name === 'PlatformCard');
  assert.ok(section, 'native shell includes shared consent component');
  values = [];
  effects = [];
  const render = async (shell, modal = true) => {
    let tree = shallow(section.type, {
      boot: { mode: 'connect' },
      shell,
      modal,
      reloadShell() {},
      gotoSettings() {},
    });
    for (const effect of effects) effect();
    await new Promise((resolve) => setImmediate(resolve));
    tree = shallow(section.type, {
      boot: { mode: 'connect' },
      shell,
      modal,
      reloadShell() {},
      gotoSettings() {},
    });
    return tree;
  };
  return {
    render,
    async renderLogin() {
      let tree;
      for (let i = 0; i < 5; i++) {
        tree = shallow(slots.get('shell.overlay'));
        for (const effect of effects) effect();
        await new Promise((resolve) => setImmediate(resolve));
      }
      return tree;
    },
    intervals,
    hash: () => dom.window.location.hash,
    remount() {
      for (const v of values) v?.cleanup?.();
      values = [];
      effects = [];
    },
    manual(shell) {
      const saved = values;
      values = [];
      const tree = shallow(section.type, {
        boot: { mode: 'connect' },
        shell,
        reloadShell() {},
      });
      values = saved;
      all(tree)
        .find(
          (n) =>
            n.type?.name === 'Action' && n.props.primary && n.props.onClick,
        )
        ?.props.onClick();
    },
    reopen() {
      dom.window.history.replaceState(null, '', '#auriga=platform-login');
      dom.window.dispatchEvent(new dom.window.Event('hashchange'));
    },
    hasModelsCard: () => slots.has('settings.models.provider-card'),
    close: () => {
      for (const v of values) v?.cleanup?.();
      dispose();
      dom.window.close();
    },
  };
}
function all(tree) {
  if (!tree || typeof tree !== 'object') return [];
  return [tree, ...(tree.children ?? []).flatMap(all)];
}
const text = (tree) =>
  typeof tree === 'string' ? tree : (tree?.children ?? []).map(text).join(' ');

test('native modal QR/form controls submit inline commands', async () => {
  const calls = [];
  const f = await fixture(async (url, init = {}) => {
    if (init.body) calls.push(JSON.parse(init.body));
    return { ok: true, json: async () => ({}) };
  });
  try {
    const status = {
      configured: true,
      platform: 'signed_out',
      brokerAvailable: true,
      loggedIn: false,
      csrf: 'csrf',
    };
    let tree = await f.render(status);
    all(tree)
      .find((n) => n.type?.name === 'Action' && text(n) === '登录平台')
      .props.onClick();
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(calls[0].action, 'inline_login');
    const providerId = '11111111-2222-4333-8444-555555555555';
    const inline = {
      providerId,
      providers: [
        { id: providerId, displayName: { zh: 'UAC' }, connectorType: 'uac' },
      ],
      step: { type: 'qr', status: 'waiting' },
      qr: { size: 21, path: 'M0 0h1v1h-1z' },
    };
    tree = await f.render({
      ...status,
      platform: 'authenticating',
      inlineLogin: inline,
    });
    assert.equal(all(tree).find((n) => n.type === 'svg').props.width, 208);
    assert.ok(
      all(tree).some(
        (n) => n.props?.role === 'tablist' || n.type?.name === 'SegmentedTabs',
      ),
    );
    assert.ok(
      !all(tree).some((n) => n.type === 'h3' && text(n) === '平台连接'),
    );
    assert.doesNotMatch(text(tree), /用户码|系统浏览器/);
    const step = {
      type: 'form',
      formId: 'password',
      fields: [{ name: 'password', kind: 'password' }],
    };
    tree = await f.render({
      ...status,
      platform: 'authenticating',
      inlineLogin: { ...inline, step },
    });
    const form = all(tree).find((n) => n.type === 'form');
    let cleared = false;
    form.props.onSubmit({
      preventDefault() {},
      currentTarget: {
        elements: { namedItem: () => ({ value: 'test-password' }) },
        reset() {
          cleared = true;
        },
      },
    });
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(cleared, true);
    assert.equal(calls.at(-1).action, 'inline_advance');
    assert.equal(calls.at(-1).input.formId, 'password');
  } finally {
    f.close();
  }
});

test('business denial and wrong credentials show retry feedback separately from client configuration', async () => {
  const f = await fixture(async () => ({ ok: true, json: async () => ({}) }));
  try {
    const status = {
      configured: true,
      platform: 'signed_out',
      brokerAvailable: true,
      loggedIn: false,
      csrf: 'csrf',
    };
    let tree = await f.render({
      ...status,
      reason: 'login_denied',
      loginError: { code: 'access_denied' },
    });
    assert.match(text(tree), /已拒绝.*重新登录/);
    assert.doesNotMatch(text(tree), /检查客户端配置/);
    tree = await f.render({
      ...status,
      platform: 'authenticating',
      inlineLogin: {
        providerId: 'test',
        providers: [],
        step: {
          type: 'form',
          formId: 'password',
          fields: [],
          error: { code: 'iam.invalid_credentials' },
        },
      },
    });
    assert.match(text(tree), /口令或验证码错误.*重试/);
    assert.doesNotMatch(text(tree), /检查客户端配置/);
    tree = await f.render({
      ...status,
      reason: 'platform_rejected',
      loginError: { code: 'invalid_client' },
    });
    assert.match(text(tree), /检查客户端配置/);
  } finally {
    f.close();
  }
});

test('global native modal starts login, skips for the session, reopens and closes on success', async () => {
  let status = {
    configured: true,
    brokerAvailable: true,
    platform: 'signed_out',
    csrf: 'csrf',
  };
  const calls = [];
  const f = await fixture(async (url, init = {}) => {
    if (init.body) calls.push(JSON.parse(init.body));
    return { ok: true, json: async () => status };
  });
  try {
    let tree = await f.renderLogin();
    assert.equal(tree.type.name, 'Modal');
    assert.equal(tree.props.className, 'auriga-platform-login-dialog');
    assert.ok(
      all(tree).some((n) => n.props?.className === 'auriga-login-footer'),
    );
    assert.equal(calls.filter((c) => c.action === 'inline_login').length, 1);
    tree.props.onClose();
    assert.equal(await f.renderLogin(), null);
    assert.equal(calls.at(-1).action, 'cancel_login');
    f.manual(status);
    tree = await f.renderLogin();
    assert.equal(tree.type.name, 'Modal');
    assert.equal(calls.filter((c) => c.action === 'inline_login').length, 2);
    status = { ...status, loggedIn: true, platform: 'authenticated' };
    const card = all(tree).find((n) => n.type?.name === 'PlatformCard');
    card.props.reloadShell();
    assert.equal(await f.renderLogin(), null);
    assert.equal(f.intervals.size, 0);
    assert.ok(calls.every((c) => c.action !== 'open_login'));
  } finally {
    f.close();
  }
});

function lockManager() {
  let held = false;
  const queue = [];
  const pump = () => {
    if (held || !queue.length) return;
    const job = queue.shift();
    if (job.signal.aborted) {
      job.reject(new Error('aborted'));
      pump();
      return;
    }
    held = true;
    Promise.resolve(job.fn({ name: 'login' })).finally(() => {
      held = false;
      job.resolve();
      pump();
    });
  };
  return {
    request(_name, { signal }, fn) {
      return new Promise((resolve, reject) => {
        queue.push({ signal, fn, resolve, reject });
        pump();
      });
    },
  };
}
const signedOut = {
  configured: true,
  brokerAvailable: true,
  platform: 'signed_out',
  csrf: 'csrf',
};
test('tray fragment is consumed and cannot bypass skip after a remount', async () => {
  const calls = [];
  const f = await fixture(
    async (_url, init = {}) => {
      if (init.body) calls.push(JSON.parse(init.body));
      return { ok: true, json: async () => signedOut };
    },
    { hash: '#auriga=platform-login' },
  );
  try {
    const tree = await f.renderLogin();
    assert.equal(tree.type.name, 'Modal');
    assert.equal(f.hash(), '');
    tree.props.onClose();
    await f.renderLogin();
    f.reopen();
    f.remount();
    assert.equal(await f.renderLogin(), null);
    assert.equal(f.hash(), '');
    assert.equal(calls.filter((c) => c.action === 'inline_login').length, 1);
    assert.equal(f.intervals.size, 0);
  } finally {
    f.close();
  }
});
for (const status of [
  { ...signedOut, configured: false },
  { ...signedOut, brokerAvailable: false },
])
  test(
    'tray does not open modal without configuration and broker readiness ' +
      JSON.stringify(status),
    async () => {
      const calls = [];
      const f = await fixture(
        async (_url, init = {}) => {
          if (init.body) calls.push(JSON.parse(init.body));
          return { ok: true, json: async () => status };
        },
        { hash: '#auriga=platform-login' },
      );
      try {
        assert.equal(await f.renderLogin(), null);
        assert.equal(calls.length, 0);
        assert.equal(f.hash(), '');
        assert.ok([...f.intervals.values()].every((v) => v.ms !== 2000));
      } finally {
        f.close();
      }
    },
  );
test('only one tab owns login while the other waits without sending commands', async () => {
  const locks = lockManager(),
    calls = [[], []];
  const fixtures = await Promise.all(
    calls.map((list) =>
      fixture(
        async (_url, init = {}) => {
          if (init.body) list.push(JSON.parse(init.body));
          return { ok: true, json: async () => signedOut };
        },
        { locks },
      ),
    ),
  );
  try {
    await fixtures[0].renderLogin();
    const waiting = await fixtures[1].renderLogin();
    assert.equal(calls[0].filter((c) => c.action === 'inline_login').length, 1);
    assert.equal(calls[1].length, 0);
    assert.ok(!all(waiting).some((n) => n.type?.name === 'PlatformCard'));
    assert.match(text(waiting), /\u5176\u4ed6\u7a97\u53e3/);
    assert.doesNotMatch(text(waiting), /unreachable/);
    assert.equal(fixtures[1].intervals.size, 0);
  } finally {
    fixtures.forEach((f) => f.close());
  }
});

test('overview never duplicates modal QR rendering and a busy broker is not reported offline', async () => {
  const calls = [];
  const f = await fixture(async (_url, init = {}) => {
    if (init.body) calls.push(JSON.parse(init.body));
    return { ok: false, status: 409, json: async () => ({}) };
  });
  const status = {
    ...signedOut,
    platform: 'authenticating',
    inlineLogin: {
      providerId: 'test',
      providers: [],
      step: { type: 'qr', status: 'waiting' },
      qr: { size: 21, path: '' },
    },
  };
  try {
    const overview = await f.render(status, false);
    assert.ok(!all(overview).some((n) => n.type === 'svg'));
    const tree = await f.render({ ...signedOut });
    all(tree)
      .find((n) => n.type?.name === 'Action' && n.props.primary)
      .props.onClick();
    await new Promise((resolve) => setImmediate(resolve));
    const result = await f.render(signedOut);
    assert.doesNotMatch(text(result), /\u5e73\u53f0\u4e0d\u53ef\u8fbe/);
    assert.equal(calls.at(-1).action, 'inline_login');
  } finally {
    f.close();
  }
});
