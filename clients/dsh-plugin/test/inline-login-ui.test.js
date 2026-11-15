import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setImmediate } from 'node:timers';
import { JSDOM } from 'jsdom';

// A React hook/element harness: tests actual registered component handlers.
// It does not pretend to implement native ModuleLoader resolution or rendering.
async function fixture(http) {
  const api = undefined,
    locale = 'zh';
  const dom = new JSDOM('<!doctype html>', { url: 'https://example.com/' });
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
    if (name === '@deepseek-ai/dsh-client-ui-primitives') return {};
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
  const render = async (shell) => {
    let tree = shallow(section.type, {
      boot: { mode: 'connect' },
      shell,
      reloadShell() {},
      gotoSettings() {},
    });
    for (const effect of effects) effect();
    await new Promise((resolve) => setImmediate(resolve));
    tree = shallow(section.type, {
      boot: { mode: 'connect' },
      shell,
      reloadShell() {},
      gotoSettings() {},
    });
    return tree;
  };
  return {
    render,
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

test('overview login stays inline and its QR/form controls submit native commands', async () => {
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
    assert.ok(all(tree).some((n) => n.type === 'svg'));
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
