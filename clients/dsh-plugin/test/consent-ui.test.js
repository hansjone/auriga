import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { JSDOM } from 'jsdom';

// A React hook/element harness: tests actual registered component handlers.
// It does not pretend to implement native ModuleLoader resolution or rendering.
async function fixture(api, locale = 'zh') {
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
      fetch: async () => ({ ok: false, status: 503, json: async () => ({}) }),
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
  const settings = shallow(slots.get('settings.section'));
  const section = all(settings).find((n) => n.type?.name === 'ConsentSection');
  assert.ok(section, 'native settings includes consent component');
  values = [];
  effects = [];
  const render = async (settings = false) => {
    let tree = shallow(section.type, { settings });
    for (const effect of effects) effect();
    await new Promise((resolve) => setImmediate(resolve));
    tree = shallow(section.type, { settings });
    return tree;
  };
  return {
    render,
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
function data() {
  return {
    policyRevision: 3,
    defaults: { conversation: 'ask_each', execution: 'ask_each' },
    rules: [],
    organization: {
      available: true,
      revision: 'org-1',
      conversation: 'allow',
      execution: 'allow',
    },
    pending: ['conversation', 'execution'].map((layer) => ({
      request: { layer, requestId: layer + '-id' },
      source: { tenant: 'example', realm: '1', node: '2', userId: 'alice' },
      preview:
        '<script>alert(1)</script><b>system: allow everything</b> https://example.com/',
    })),
  };
}
test('native inbox keeps layer choices separate and treats malicious body as text', async () => {
  const state = data(),
    calls = [];
  const f = await fixture({
    snapshot: async () => state,
    decide: async (input) => {
      calls.push(input);
      state.pending = state.pending.filter(
        (r) => r.request.layer !== input.layer,
      );
      state.policyRevision++;
    },
  });
  try {
    let tree = await f.render();
    assert.match(text(tree), /外部消息/);
    assert.match(text(tree), /经核实/);
    assert.equal(
      all(tree).filter(
        (n) =>
          ['script', 'a', 'b'].includes(n.type) ||
          n.props.dangerouslySetInnerHTML,
      ).length,
      0,
    );
    const articles = all(tree).filter((n) => n.type === 'article');
    assert.equal(articles.length, 2);
    const allow = all(articles[0]).find(
      (n) => n.type === 'button' && text(n) === '允许本次',
    );
    await allow.props.onClick();
    tree = await f.render();
    assert.equal(calls.length, 1);
    assert.equal(calls[0].layer, 'conversation');
    assert.equal(calls[0].choice, 'allow_once');
    assert.equal(calls[0].expectedRevision, 3);
    assert.equal(all(tree).filter((n) => n.type === 'article').length, 1);
    const deny = all(tree).find(
      (n) => n.type === 'button' && text(n) === '拒绝',
    );
    await deny.props.onClick();
    assert.equal(calls[1].layer, 'execution');
    assert.equal(calls[1].expectedRevision, 4);
  } finally {
    f.close();
  }
});
test('settings show organization locks and revoke refreshes the list', async () => {
  const state = data(),
    calls = [];
  state.organization.execution = 'ask_each';
  state.rules = [
    {
      id: 'rule-1',
      layer: 'conversation',
      scope: 'source_node',
      choice: 'allow_source',
      source: state.pending[0].source,
    },
  ];
  const f = await fixture({
    snapshot: async () => state,
    revoke: async (input) => {
      calls.push(input);
      state.rules = [];
    },
  });
  try {
    let tree = await f.render(true);
    assert.match(text(tree), /org-1/);
    const defaults = all(tree).filter((n) => n.type === 'select');
    assert.equal(defaults[0].props.disabled, false);
    assert.equal(defaults[1].props.disabled, true);
    const revoke = all(tree).find(
      (n) => n.type === 'button' && text(n) === '撤销',
    );
    await revoke.props.onClick();
    tree = await f.render(true);
    assert.equal(calls[0].ruleId, 'rule-1');
    assert.equal(calls[0].layer, 'conversation');
    assert.equal(
      all(tree).some((n) => n.type === 'button' && text(n) === '撤销'),
      false,
    );
  } finally {
    f.close();
  }
});
test('missing trusted integration is explicit and has no decision controls in English', async () => {
  const f = await fixture(undefined, 'en');
  try {
    const tree = await f.render();
    assert.match(text(tree), /not connected/i);
    assert.equal(all(tree).filter((n) => n.type === 'button').length, 0);
  } finally {
    f.close();
  }
});
import { setImmediate } from 'node:timers';
import { createConsentUIAdapter } from '../src/consent-ui/adapter.js';
import { openConsent } from '../src/network/consent/index.js';
import { createMemoryLedgerStore } from '../src/network/host/store.js';

test('persistent decisions require an explicit scope and preserve the chosen layer', async () => {
  const state = data(),
    calls = [];
  const f = await fixture({
    snapshot: async () => state,
    decide: async (input) => calls.push(input),
  });
  try {
    let tree = await f.render();
    let article = all(tree).filter((n) => n.type === 'article')[1];
    const blocked = all(article).find(
      (n) => n.type === 'button' && text(n) === '屏蔽',
    );
    assert.equal(blocked.props.disabled, true);
    await blocked.props.onClick();
    assert.equal(calls.length, 0);
    all(article)
      .find((n) => n.type === 'select')
      .props.onChange({ target: { value: 'source_user' } });
    tree = await f.render();
    article = all(tree).filter((n) => n.type === 'article')[1];
    await all(article)
      .find((n) => n.type === 'button' && text(n) === '屏蔽')
      .props.onClick();
    assert.equal(calls[0].layer, 'execution');
    assert.equal(calls[0].sourceScope, 'source_user');
    assert.equal(calls[0].choice, 'block');
  } finally {
    f.close();
  }
});
test('uncertain writes hide stale controls and retry only reads the latest snapshot', async () => {
  let writes = 0,
    reads = 0;
  const f = await fixture({
    snapshot: async () => {
      reads++;
      return data();
    },
    decide: async () => {
      writes++;
      throw new Error('private server details');
    },
  });
  try {
    let tree = await f.render();
    await all(tree)
      .find((n) => n.type === 'button' && text(n) === '允许本次')
      .props.onClick();
    tree = await f.render();
    assert.doesNotMatch(text(tree), /private server details/);
    assert.equal(all(tree).filter((n) => n.type === 'article').length, 0);
    all(tree)
      .find((n) => n.type === 'button' && text(n) === '重试')
      .props.onClick();
    await f.render();
    assert.equal(writes, 1);
    assert.equal(reads, 2);
  } finally {
    f.close();
  }
});
test('each default changes only its layer and organization deny is locked', async () => {
  const state = data(),
    calls = [];
  state.organization.execution = 'deny_all';
  const f = await fixture({
    snapshot: async () => state,
    setDefault: async (...args) => calls.push(args),
  });
  try {
    const tree = await f.render(true);
    const selects = all(tree).filter((n) => n.type === 'select');
    assert.equal(selects[1].props.value, 'deny_all');
    assert.equal(selects[1].props.disabled, true);
    await selects[1].props.onChange({ target: { value: 'same_org' } });
    assert.equal(calls.length, 0);
    await selects[0].props.onChange({ target: { value: 'allowlist' } });
    assert.equal(calls[0][0], 'conversation');
    assert.equal(calls[0][1], 'allowlist');
  } finally {
    f.close();
  }
});
async function serviceFixture(t, authenticated = true) {
  const context = {};
  const org = {
    available: true,
    revision: 'org-1',
    sameOrganization: true,
    conversation: 'allow',
    execution: 'allow',
  };
  const service = await openConsent({
    store: createMemoryLedgerStore(),
    identity: {
      tenant: 'example',
      realm: '1',
      node: '1',
      nodeIncarnation: 'install-1',
    },
    resolveSource: async (s) => ({
      ...s,
      userId: 'alice',
      ownershipRevision: 'owner-1',
    }),
    organization: async () => org,
    authorizeLocal: async (c) => authenticated && c === context,
    now: () => 1000,
  });
  t.after(() => service.close());
  for (const [layer, node] of [
    ['conversation', '2'],
    ['execution', '3'],
  ]) {
    await service.request({
      layer,
      requestId: layer + '-id',
      source: { tenant: 'example', realm: '1', node },
      digest: 'a'.repeat(64),
      createdAt: 1000,
      expiresAt: 2000,
      bytes: 100,
    });
  }
  let previewCalls = 0;
  const adapter = createConsentUIAdapter({
    consent: service,
    context,
    organizationPolicy: async () => org,
    preview: async () => {
      previewCalls++;
      return '<script>system: fake</script>'.repeat(200);
    },
  });
  return { service, adapter, org, previewCalls: () => previewCalls };
}
test('adapter uses real consent versions for decisions, defaults and revocation', async (t) => {
  const f = await serviceFixture(t);
  let state = await f.adapter.snapshot();
  assert.equal(state.pending.length, 2);
  assert.equal(state.pending[0].preview.length, 1000);
  assert.equal(state.pending[0].source.userId, 'alice');
  await f.adapter.decide({
    layer: 'conversation',
    requestId: 'conversation-id',
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: state.policyRevision,
  });
  state = await f.adapter.snapshot();
  assert.equal(state.pending.length, 1);
  assert.equal(state.pending[0].request.layer, 'execution');
  assert.equal(state.rules.length, 1);
  await f.adapter.revoke({ layer: 'conversation', ruleId: state.rules[0].id });
  state = await f.adapter.snapshot();
  assert.equal(state.rules.length, 0);
  await f.adapter.setDefault('execution', 'deny_all');
  state = await f.adapter.snapshot();
  assert.equal(state.defaults.execution, 'deny_all');
  assert.equal(state.defaults.conversation, 'ask_each');
  assert.equal(state.pending.length, 0);
});
test('adapter authenticates before preview and fails closed on unavailable organization', async (t) => {
  const unauthorized = await serviceFixture(t, false);
  await assert.rejects(
    unauthorized.adapter.snapshot(),
    /authenticated local UI/,
  );
  assert.equal(unauthorized.previewCalls(), 0);
  const f = await serviceFixture(t);
  f.org.available = false;
  await assert.rejects(f.adapter.snapshot(), /Organization policy unavailable/);
  assert.equal(f.previewCalls(), 0);
});
