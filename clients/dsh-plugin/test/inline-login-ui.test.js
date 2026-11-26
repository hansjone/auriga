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
  const timeouts = new Map();
  const surfaceValues = new Map();
  let intervalId = 0;
  let timeoutId = 0;
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
      const hooks = values;
      if (!(i in hooks))
        hooks[i] = typeof initial === 'function' ? initial() : initial;
      return [
        hooks[i],
        (v) => {
          hooks[i] = typeof v === 'function' ? v(hooks[i]) : v;
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
    options.source ??
      (await readFile(new URL('../src/client.js', import.meta.url), 'utf8')),
    {
      window: dom.window,
      document: dom.window.document,
      URL,
      AbortController,
      customElements: dom.window.customElements,
      setTimeout: options.controlTimeouts
        ? (fn, ms) => {
            const id = ++timeoutId;
            timeouts.set(id, { fn, ms });
            return id;
          }
        : setTimeout,
      clearTimeout: options.controlTimeouts
        ? (id) => timeouts.delete(id)
        : clearTimeout,
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
  const renderSurface = async (name, component, props) => {
    let tree;
    for (let pass = 0; pass < 2; pass++) {
      const saved = { values, effects, cursor };
      values = surfaceValues.get(name) ?? [];
      tree = shallow(component, props);
      surfaceValues.set(name, values);
      const pending = effects;
      ({ values, effects, cursor } = saved);
      for (const effect of pending) effect();
      await new Promise((resolve) => setImmediate(resolve));
    }
    return tree;
  };
  const clearSurface = (name) => {
    for (const value of surfaceValues.get(name) ?? []) value?.cleanup?.();
    surfaceValues.delete(name);
  };
  return {
    render,
    renderOverview: (shell) =>
      renderSurface('overview', section.type, {
        boot: { mode: 'connect' },
        shell,
        modal: false,
        reloadShell() {},
      }),
    async renderLocalIdentity(uac) {
      const overlay = await this.renderLogin();
      const card = all(overlay).find(
        (node) => node.type?.name === 'LocalIdentityCard',
      );
      assert.ok(card);
      return renderSurface('local-card', card.type, {
        ...card.props,
        uac,
        active: false,
      });
    },
    async renderLoginContent() {
      const overlay = await this.renderLogin();
      if (!overlay) {
        clearSurface('login-card');
        return null;
      }
      const card = all(overlay).find(
        (node) => node.type?.name === 'PlatformCard',
      );
      // Mount the real overlay's child and its actual props, never force modal.
      return card
        ? renderSurface('login-card', card.type, card.props)
        : overlay;
    },
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
    timeouts,
    fireTimeout(id) {
      const timer = timeouts.get(id);
      assert.ok(timer, 'the component must have scheduled this timeout');
      timeouts.delete(id);
      timer.fn();
    },
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
    hasAccountLauncher: () => slots.has('settings.launcher'),
    renderLauncher: (props = { wide: true }) =>
      renderSurface('settings-launcher', slots.get('settings.launcher'), props),
    close: () => {
      for (const v of values) v?.cleanup?.();
      for (const name of surfaceValues.keys()) clearSurface(name);
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

for (const platform of ['signed_out', 'reauth_required'])
  test(`default inline primary action for ${platform} (no system browser jump)`, async () => {
    const calls = [];
    const f = await fixture(async (_url, init = {}) => {
      if (init.body) calls.push(JSON.parse(init.body));
      return { ok: true, json: async () => ({}) };
    });
    try {
      const tree = await f.render({
        configured: true,
        brokerAvailable: true,
        loggedIn: false,
        platform,
        csrf: 'csrf',
      });
      const primary = all(tree).find(
        (node) => node.type?.name === 'Action' && node.props.primary,
      );
      assert.match(text(primary), /扫码或团队账号/);
      primary.props.onClick();
      await new Promise((resolve) => setImmediate(resolve));
      assert.deepEqual(calls, [{ action: 'inline_login' }]);
    } finally {
      f.close();
    }
  });

test('existing browser device attempt is displayed without starting another login', async () => {
  const calls = [];
  const f = await fixture(async (_url, init = {}) => {
    if (init.body) calls.push(JSON.parse(init.body));
    return {
      ok: true,
      json: async () => ({
        configured: true,
        brokerAvailable: true,
        loggedIn: false,
        platform: 'authenticating',
        csrf: 'csrf',
        deviceAttempt: { userCode: 'BCDF-GHJK' },
      }),
    };
  });
  try {
    const tree = await f.renderLogin();
    const card = all(tree).find((node) => node.type?.name === 'PlatformCard');
    assert.equal(card.props.shell.deviceAttempt.userCode, 'BCDF-GHJK');
    assert.deepEqual(calls, []);
  } finally {
    f.close();
  }
});

test('default login modal starts inline login without opening system browser', async () => {
  const calls = [];
  const f = await fixture(
    async (url, init = {}) => {
      if (String(url).includes('/auriga/uac/status'))
        return {
          ok: true,
          json: async () => ({ available: false, state: 'signed_out' }),
        };
      if (init.body) calls.push(JSON.parse(init.body));
      return {
        ok: true,
        json: async () => ({
          configured: true,
          brokerAvailable: true,
          loggedIn: false,
          platform: 'signed_out',
          csrf: 'csrf',
        }),
      };
    },
    { hash: '#auriga=platform-login' },
  );
  try {
    const tree = await f.renderLogin();
    assert.equal(tree.type.name, 'Modal');
    assert.equal(calls.filter((c) => c.action === 'inline_login').length, 1);
    assert.equal(calls.filter((c) => c.action === 'open_login').length, 0);
  } finally {
    f.close();
  }
});

test('local UAC silent SSO suppresses modal; unsigned local UAC defaults to direct QR even when platform is offline', async () => {
  const ssoCalls = [];
  const ssoFixture = await fixture(async (url, init = {}) => {
    if (String(url).includes('/auriga/uac/status'))
      return {
        ok: true,
        json: async () => ({
          available: true,
          state: 'authenticated',
          empNo: '0010329667',
          department: 'Platform',
          authMethod: 'sso',
          loginAt: new Date().toISOString(),
          csrf: 'uac-csrf',
        }),
      };
    if (init.body) ssoCalls.push(JSON.parse(init.body));
    return {
      ok: true,
      json: async () => ({
        configured: true,
        brokerAvailable: true,
        loggedIn: false,
        platform: 'signed_out',
        csrf: 'csrf',
      }),
    };
  });
  try {
    const tree = await ssoFixture.renderLogin();
    assert.equal(
      tree,
      null,
      'valid local identity waits for automatic association without a second login',
    );
    assert.equal(
      ssoCalls.length,
      0,
      'the renderer never initiates a second native challenge',
    );
  } finally {
    ssoFixture.close();
  }

  // Silent SSO authenticated + platform offline/unconfigured: modal stays closed
  const standaloneSsoFixture = await fixture(async (url) => {
    if (String(url).includes('/auriga/uac/status'))
      return {
        ok: true,
        json: async () => ({
          available: true,
          state: 'authenticated',
          empNo: '0010329667',
          authMethod: 'sso',
          loginAt: new Date().toISOString(),
          csrf: 'uac-csrf',
        }),
      };
    return {
      ok: true,
      json: async () => ({
        configured: false,
        brokerAvailable: false,
        loggedIn: false,
        platform: 'signed_out',
        csrf: 'csrf',
      }),
    };
  });
  try {
    assert.equal(await standaloneSsoFixture.renderLogin(), null);
  } finally {
    standaloneSsoFixture.close();
  }

  // Platform offline/broker unavailable: local UAC scan still opens and runs independently
  const offlineCalls = [];
  const offlineFixture = await fixture(async (url, init = {}) => {
    if (String(url).includes('/auriga/uac/status'))
      return {
        ok: true,
        json: async () => ({
          available: true,
          state: 'signed_out',
          ownerBound: false,
          csrf: 'uac-csrf',
        }),
      };
    if (String(url).includes('/auriga/uac/start')) {
      offlineCalls.push('uac_start');
      return {
        ok: true,
        json: async () => ({
          available: true,
          state: 'signed_out',
          ownerBound: false,
          csrf: 'uac-csrf',
          challenge: {
            id: '11111111-2222-4333-8444-555555555555',
            state: 'waiting',
            expiresAt: new Date(Date.now() + 60000).toISOString(),
            qr: { size: 21, path: 'M0 0h1v1h-1z' },
            correlationId: '22222222-2222-4333-8444-555555555555',
          },
        }),
      };
    }
    if (init.body) offlineCalls.push(JSON.parse(init.body));
    return {
      ok: true,
      json: async () => ({
        configured: true,
        brokerAvailable: false,
        loggedIn: false,
        platform: 'unreachable',
        csrf: 'csrf',
      }),
    };
  });
  try {
    const tree = await offlineFixture.renderLogin();
    assert.equal(tree.type.name, 'Modal');
    assert.ok(
      all(tree).some((n) => n.type?.name === 'LocalIdentityCard'),
      'local UAC scan renders even when platform is unreachable',
    );
    assert.ok(
      offlineCalls.every((c) => typeof c === 'string'),
      'no platform shell commands are sent for local UAC scan',
    );
  } finally {
    offlineFixture.close();
  }
});

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
      .find((n) => n.type?.name === 'Action' && /扫码或团队账号/.test(text(n)))
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

test('global native modal starts inline login, skips for the session, reopens and closes on success', async () => {
  let status = {
    configured: true,
    brokerAvailable: true,
    platform: 'signed_out',
    csrf: 'csrf',
  };
  const calls = [];
  const f = await fixture(async (url, init = {}) => {
    if (String(url).includes('/auriga/uac/status'))
      return { ok: true, json: async () => ({ available: false }) };
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

for (const dismiss of ['skip', 'close'])
  test(`overview explicit scan after ${dismiss} opens visible QR, keeps polling and submits the team form`, async () => {
    let status = { ...signedOut };
    const calls = [];
    const providers = [
      { id: 'company', displayName: { zh: '公司扫码' }, connectorType: 'uac' },
      {
        id: 'team',
        displayName: { zh: '团队账号' },
        connectorType: 'team-account',
      },
    ];
    const f = await fixture(
      async (_url, init = {}) => {
        if (init.body) {
          const command = JSON.parse(init.body);
          calls.push(command);
          if (command.action === 'open_login')
            status = {
              ...signedOut,
              platform: 'authenticating',
              deviceAttempt: { userCode: 'BCDF-GHJK' },
            };
          if (command.action === 'cancel_login') status = { ...signedOut };
          if (command.action === 'inline_login')
            status = {
              ...signedOut,
              platform: 'authenticating',
              inlineLogin: {
                providerId: 'company',
                providers,
                step: { type: 'qr', status: 'waiting' },
                qr: { size: 21, path: 'M0 0h1v1h-1z' },
              },
            };
          if (command.action === 'inline_advance') {
            const inline = status.inlineLogin;
            if (command.input.operation === 'poll')
              status = {
                ...status,
                inlineLogin: {
                  ...inline,
                  step: { type: 'qr', status: 'scanned' },
                },
              };
            if (command.input.operation === 'select')
              status = {
                ...status,
                inlineLogin: {
                  ...inline,
                  providerId: command.input.providerId,
                  step: {
                    type: 'form',
                    formId: 'password',
                    fields: [{ name: 'password', kind: 'password' }],
                  },
                },
              };
            if (command.input.operation === 'submit')
              status = {
                ...signedOut,
                loggedIn: true,
                platform: 'authenticated',
              };
          }
        }
        return { ok: true, json: async () => status };
      },
      { controlTimeouts: true },
    );
    const polling = () =>
      [...f.timeouts].filter(([, timer]) => timer.ms === 2000);
    try {
      const initial = await f.renderLogin();
      assert.equal(initial.type.name, 'Modal');
      assert.equal(
        calls.filter((call) => call.action === 'inline_login').length,
        1,
      );
      if (dismiss === 'close') initial.props.onClose();
      else
        all(initial)
          .find(
            (node) => node.type?.name === 'Action' && /跳过/.test(text(node)),
          )
          .props.onClick();
      assert.equal(await f.renderLogin(), null);
      assert.equal(calls.at(-1).action, 'cancel_login');
      const overview = await f.renderOverview(status);
      all(overview)
        .find(
          (node) =>
            node.type?.name === 'Action' && /扫码或团队账号/.test(text(node)),
        )
        .props.onClick();
      let visible = await f.renderLoginContent();
      assert.ok(
        visible,
        'explicit Overview login must reopen a visible surface after Skip',
      );
      assert.equal(
        all(visible).find((node) => node.type === 'svg')?.props.width,
        208,
      );
      assert.equal(
        calls.filter((call) => call.action === 'inline_login').length,
        2,
      );
      assert.equal(polling().length, 1);
      const background = await f.renderOverview(status);
      assert.ok(!all(background).some((node) => node.type === 'svg'));
      assert.equal(polling().length, 1, 'only the visible login surface polls');
      f.fireTimeout(polling()[0][0]);
      visible = await f.renderLoginContent();
      assert.deepEqual(calls.at(-1), {
        action: 'inline_advance',
        input: { operation: 'poll' },
      });
      assert.equal(polling().length, 1, 'a scanned QR continues polling');
      if (dismiss === 'close') {
        (await f.renderLogin()).props.onClose();
        assert.equal(await f.renderLoginContent(), null);
        assert.equal(calls.at(-1).action, 'cancel_login');
        assert.equal(polling().length, 0, 'closing inline cancels QR polling');
        assert.equal(f.intervals.size, 0);
        const cancelled = calls.length;
        f.reopen();
        assert.equal(await f.renderLoginContent(), null);
        assert.equal(
          calls.length,
          cancelled,
          'Tray cannot reopen a skipped attempt',
        );
        const retry = await f.renderOverview(status);
        all(retry)
          .find(
            (node) =>
              node.type?.name === 'Action' && /扫码或团队账号/.test(text(node)),
          )
          .props.onClick();
        visible = await f.renderLoginContent();
        assert.equal(
          all(visible).find((node) => node.type === 'svg')?.props.width,
          208,
        );
        assert.equal(polling().length, 1);
      }
      all(visible)
        .find((node) => node.type === 'button' && text(node) === '团队账号')
        .props.onClick();
      visible = await f.renderLoginContent();
      assert.equal(calls.at(-1).input.providerId, 'team');
      assert.equal(
        polling().length,
        0,
        'switching to the form cancels QR polling',
      );
      const form = all(visible).find((node) => node.type === 'form');
      assert.ok(form, 'the selected team form is visible');
      let cleared = false;
      form.props.onSubmit({
        preventDefault() {},
        currentTarget: {
          elements: { namedItem: () => ({ value: 'fixture-password' }) },
          reset() {
            cleared = true;
          },
        },
      });
      assert.equal(await f.renderLoginContent(), null);
      assert.equal(cleared, true);
      assert.deepEqual(calls.at(-1), {
        action: 'inline_advance',
        input: {
          operation: 'submit',
          formId: 'password',
          values: { password: 'fixture-password' },
        },
      });
      assert.equal(polling().length, 0);
      assert.equal(f.intervals.size, 0);
      assert.equal(
        calls.filter((call) => call.action === 'inline_login').length,
        dismiss === 'close' ? 3 : 2,
        'each opening starts inline exactly once',
      );
      assert.equal(
        calls.filter((call) => call.action === 'open_login').length,
        0,
        'system browser open_login is never triggered automatically',
      );
    } finally {
      f.close();
    }
  });

test('settings.launcher replaces DSH bottom-left account slot with Auriga avatar, name, empNo and sign-in/out actions', async () => {
  const requests = [];
  let uacState = {
    available: true,
    state: 'authenticated',
    empNo: '001001',
    department: '部门测试名',
    platformLink: 'matched',
    platformBinding: 'c'.repeat(64),
    csrf: 'uac-csrf',
  };
  let shellState = {
    configured: true,
    brokerAvailable: true,
    loggedIn: true,
    connected: true,
    account: { displayName: '张三', profileBinding: 'c'.repeat(64) },
    csrf: 'shell-csrf',
  };
  const f = await fixture(async (url, init = {}) => {
    const href = String(url);
    if (init.method === 'POST')
      requests.push({ url: href, body: JSON.parse(init.body) });
    if (href.includes('/auriga/uac/status'))
      return { ok: true, json: async () => uacState };
    if (href.includes('/auriga/shell/status'))
      return { ok: true, json: async () => shellState };
    return { ok: true, json: async () => ({ ok: true }) };
  });
  try {
    assert.equal(f.hasAccountLauncher(), true);
    let settingsOpened = 0;
    const signedTree = await f.renderLauncher({
      wide: true,
      openSettings: () => settingsOpened++,
    });
    const renderedText = text(signedTree);
    assert.match(renderedText, /张/);
    assert.match(renderedText, /张三/);
    assert.match(renderedText, /001001/);
    assert.doesNotMatch(renderedText, /部门测试名|本地身份|平台已连接/);
    // Open fallback menu and trigger settings + sign-out
    const trigger = all(signedTree).find((n) => n.type === 'button');
    assert.equal(trigger.props.title, '张三 (001001)');
    trigger.props.onClick();
    const opened = await f.renderLauncher({
      wide: true,
      openSettings: () => settingsOpened++,
    });
    const items = all(opened).filter((n) => n.props?.role === 'menuitem');
    assert.equal(items.length, 2);
    items[0].props.onClick();
    assert.equal(settingsOpened, 1);
    items[1].props.onClick();
    await new Promise((resolve) => setImmediate(resolve));
    assert.ok(requests.some((r) => r.url.includes('/auriga/uac/logout')));
    assert.ok(
      requests.some(
        (r) =>
          r.url.includes('/auriga/shell/command') && r.body.action === 'logout',
      ),
    );
  } finally {
    f.close();
  }
});

for (const shellState of [
  null,
  { configured: false, brokerAvailable: false, loggedIn: false },
])
  test(
    'manual account login keeps local UAC visible while platform is unavailable: ' +
      (shellState ? 'unconfigured' : 'unknown'),
    async () => {
      const f = await fixture(
        async (url) =>
          String(url).includes('/auriga/uac/status')
            ? {
                ok: true,
                json: async () => ({
                  available: true,
                  state: 'signed_out',
                  csrf: 'uac-csrf',
                }),
              }
            : shellState
              ? { ok: true, json: async () => shellState }
              : { ok: false, status: 503 },
        { skipped: true },
      );
      try {
        await f.renderLogin();
        const launcher = await f.renderLauncher();
        all(launcher)
          .find((n) => n.type === 'button')
          .props.onClick();
        const menu = await f.renderLauncher();
        all(menu)
          .find((n) => n.props?.role === 'menuitem' && /登录/.test(text(n)))
          .props.onClick();
        const overlay = await f.renderLogin();
        assert.ok(
          overlay,
          'local login must not be gated by platform readiness',
        );
        assert.ok(
          all(overlay).some((n) => n.type?.name === 'LocalIdentityCard'),
        );
      } finally {
        f.close();
      }
    },
  );

test('account launcher preserves unknown state and renders safe local avatar with glyph fallback', async () => {
  let uac = null;
  const f = await fixture(async (url) =>
    String(url).includes('/auriga/uac/status') && uac
      ? { ok: true, json: async () => uac }
      : { ok: false, status: 503 },
  );
  try {
    const unknown = await f.renderLauncher();
    assert.match(text(unknown), /状态未知|正在确认/);
    uac = {
      available: true,
      state: 'authenticated',
      empNo: 'A1001',
      displayName: 'Alice Example',
      avatarUrl: 'https://profile.example.com/alice.png',
    };
    for (const timer of f.intervals.values()) timer.fn();
    await new Promise((resolve) => setImmediate(resolve));
    const signed = await f.renderLauncher();
    assert.match(text(signed), /Alice Example/);
    const avatar = all(signed).find((n) => n.type === 'img');
    assert.equal(avatar.props.src, uac.avatarUrl);
    assert.equal(avatar.props.referrerPolicy, 'no-referrer');
    avatar.props.onError();
    const fallback = await f.renderLauncher();
    assert.ok(!all(fallback).some((n) => n.type === 'img'));
    assert.match(text(fallback), /A/);
  } finally {
    f.close();
  }
});

for (const local of [
  { available: true, state: 'signed_out' },
  { available: true, reason: 'status_unavailable' },
])
  test(
    'platform login cannot replace a separate local identity state: ' +
      (local.state ?? local.reason),
    async () => {
      const f = await fixture(async (url) => ({
        ok: true,
        json: async () =>
          String(url).includes('/auriga/uac/status')
            ? local
            : {
                configured: true,
                brokerAvailable: true,
                loggedIn: true,
                connected: true,
                account: { displayName: 'Platform Only' },
                csrf: 'shell-csrf',
              },
      }));
      try {
        const launcher = await f.renderLauncher();
        assert.doesNotMatch(text(launcher), /Platform Only/);
        assert.match(text(launcher), /平台已连接/);
        assert.match(
          text(launcher),
          local.reason ? /本地身份状态未知/ : /本地身份未登录/,
        );
        if (local.reason)
          assert.ok(
            !all(launcher).some((n) => n.props?.['data-signed-out'] === 'true'),
          );
      } finally {
        f.close();
      }
    },
  );

for (const [reason, hint] of [
  ['v20_app_bound', /不支持静默 SSO.*扫码/],
  ['browser_store_unreadable', /可能被占用.*关闭浏览器.*扫码/],
  ['dns_address_mismatch', /受控配置.*管理员.*DNS/],
])
  test('local login shows an actionable diagnostic: ' + reason, async () => {
    const uac = {
      available: true,
      state: 'signed_out',
      csrf: 'synthetic',
      ssoDiagnostic: reason,
    };
    const f = await fixture(async (url) => ({
      ok: true,
      json: async () =>
        String(url).includes('/auriga/uac/status')
          ? uac
          : { configured: false, loggedIn: false },
    }));
    try {
      assert.match(text(await f.renderLocalIdentity(uac)), hint);
    } finally {
      f.close();
    }
  });

test('automatic association shows no browser instruction and keeps MFA visible in the normal card', async () => {
  const f = await fixture(async () => ({ ok: true, json: async () => ({}) }));
  const status = {
    configured: true,
    brokerAvailable: true,
    loggedIn: false,
    platform: 'authenticating',
    localUacProof: true,
    csrf: 'csrf',
  };
  try {
    const working = await f.render(status, false);
    assert.match(text(working), /正在连接平台/);
    assert.doesNotMatch(text(working), /请在系统浏览器/);
    const pending = await f.render(
      {
        ...status,
        inlineLogin: {
          providers: [],
          providerId: 'uac',
          step: {
            type: 'form',
            formId: 'platform_totp',
            fields: [{ name: 'otp', kind: 'otp', required: true }],
          },
        },
      },
      false,
    );
    assert.ok(
      all(pending).some((n) => n.type === 'form'),
      'automatic MFA remains visible even when the shared overlay is available',
    );
    assert.ok(all(pending).some((n) => n.props?.name === 'otp'));
  } finally {
    f.close();
  }
});

test('rejected local association has a one-click retry and safe diagnostics in Overview', async () => {
  const calls = [];
  const shell = {
    configured: true,
    brokerAvailable: true,
    loggedIn: false,
    platform: 'reauth_required',
    reason: 'login_denied',
    canRetryLocalUac: true,
    loginError: {
      code: 'iam.uac_proof_subject_required',
      correlationId: '135955a8-3864-42da-93a6-dc6d20bc184b',
    },
    csrf: 'csrf',
  };
  const f = await fixture(async (url, init = {}) => {
    if (init.body) calls.push(JSON.parse(init.body));
    return {
      ok: true,
      json: async () =>
        String(url).includes('/auriga/uac/status')
          ? { available: true, state: 'authenticated', empNo: '001Ab' }
          : shell,
    };
  });
  try {
    assert.equal(await f.renderLogin(), null);
    assert.deepEqual(calls, []);
    const card = await f.renderOverview(shell);
    const details = all(card).find((node) => node.type === 'details');
    assert.match(text(details), /错误详情.*iam\.uac_proof_subject_required/);
    assert.ok(text(details).includes(shell.loginError.correlationId));
    assert.doesNotMatch(text(card), /平台登录已过期/);
    const primary = all(card).find(
      (node) => node.type?.name === 'Action' && node.props.primary,
    );
    assert.match(text(primary), /用本机身份重新连接平台/);
    primary.props.onClick();
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(calls, [{ action: 'retry_local_uac' }]);
  } finally {
    f.close();
  }
});

for (const change of ['account', 'generation'])
  test(
    'launcher binds platform profile to the same UAC snapshot during ' +
      change +
      ' changes',
    async () => {
      const initialBinding = 'a'.repeat(64),
        nextBinding = 'b'.repeat(64);
      let shell = {
        configured: true,
        brokerAvailable: true,
        loggedIn: true,
        connected: true,
        account: {
          subject: 'account-a',
          displayName: 'Alice Example',
          avatarUrl: 'https://profile.example.com/alice.png',
          profileBinding: initialBinding,
        },
        csrf: 'shell-csrf',
      };
      let local = {
        available: true,
        state: 'authenticated',
        empNo: 'A1001',
        platformLink: 'matched',
        platformBinding: initialBinding,
        csrf: 'uac-csrf',
      };
      let hold = false,
        release;
      const f = await fixture(async (url) => {
        if (String(url).includes('/auriga/uac/status')) {
          if (hold)
            return new Promise((resolve) => {
              release = () => resolve({ ok: true, json: async () => local });
            });
          return { ok: true, json: async () => local };
        }
        return { ok: true, json: async () => shell };
      });
      try {
        assert.match(text(await f.renderLauncher()), /Alice Example/);
        hold = true;
        shell = {
          ...shell,
          account: {
            subject: change === 'account' ? 'account-b' : 'account-a',
            displayName: 'New Identity',
            avatarUrl: 'https://profile.example.com/new.png',
            profileBinding: nextBinding,
          },
        };
        for (const timer of f.intervals.values()) timer.fn();
        await new Promise((resolve) => setImmediate(resolve));
        let current = await f.renderLauncher();
        assert.doesNotMatch(text(current), /New Identity/);
        assert.ok(
          !all(current).some((n) => n.props?.src === shell.account.avatarUrl),
        );
        assert.match(text(current), /A1001/);
        release?.();
        await new Promise((resolve) => setImmediate(resolve));
        assert.doesNotMatch(
          text(await f.renderLauncher()),
          /New Identity/,
          'late old matched status cannot bind the new snapshot',
        );
        hold = false;
        local = {
          ...local,
          platformBinding: nextBinding,
          empNo: change === 'account' ? 'B2002' : 'A1001',
        };
        for (const timer of f.intervals.values()) timer.fn();
        await new Promise((resolve) => setImmediate(resolve));
        current = await f.renderLauncher();
        assert.match(text(current), /New Identity/);
      } finally {
        release?.();
        f.close();
      }
    },
  );
