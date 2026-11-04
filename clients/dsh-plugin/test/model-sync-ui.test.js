import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { JSDOM } from 'jsdom';

async function fixture(t, locale = 'zh') {
  const dom = new JSDOM('<!doctype html>', { url: 'https://example.com/' });
  let module, copy, component;
  const states = [];
  let cursor = 0;
  const cleanups = [];
  const React = {
    createElement: (type, props, ...children) => ({
      type,
      props,
      children: children.flat(),
    }),
    useState(initial) {
      const at = cursor++;
      if (!(at in states)) states[at] = initial;
      return [
        states[at],
        (next) => {
          states[at] = typeof next === 'function' ? next(states[at]) : next;
        },
      ];
    },
    useRef(initial) {
      const at = cursor++;
      return (states[at] ??= { current: initial });
    },
    useEffect(fn) {
      const at = cursor++;
      if (!(at in states)) {
        states[at] = true;
        cleanups.push(fn());
      }
    },
  };
  const saved = [
    {
      id: 'auto',
      name: 'My Auto',
      contextWindow: 32768,
      maxTokens: 4096,
      input: ['text'],
      compat: { supportsStore: false },
    },
    { id: 'local-only', name: 'Kept', contextWindow: 9000, maxTokens: 1000 },
  ];
  let revision = 7,
    reads = 0,
    requests = 0,
    writes = 0,
    fail = false;
  let directory = [
    { id: 'auto', name: 'auto', contextWindow: 1000000, maxTokens: 131072 },
    { id: 'new', contextWindow: 64000, maxTokens: 8192 },
  ];
  const remote = {
    llm: {
      discoverModels: async (ns, request) => {
        requests++;
        assert.equal(ns, 'llm-pi-ai');
        assert.deepEqual(JSON.parse(JSON.stringify(request)), {
          provider: 'auriga-managed',
        });
        return fail
          ? { ok: false, error: { code: 'temporary' } }
          : { ok: true, value: directory };
      },
    },
    settings: {
      describe: async () => {
        reads++;
        return {
          ok: true,
          value: {
            namespaces: [
              {
                ns: 'llm-pi-ai',
                revision,
                value: {
                  providers: {
                    'auriga-managed': { models: saved },
                    personal: { models: [{ id: 'private' }] },
                  },
                },
              },
            ],
          },
        };
      },
      mutate: async (ns, ops, expected) => {
        writes++;
        assert.equal(ns, 'llm-pi-ai');
        if (expected !== revision)
          return { ok: false, error: { code: 'settings/conflict' } };
        for (const operation of ops) {
          assert.deepEqual(
            JSON.parse(JSON.stringify(operation.path.slice(0, 3))),
            ['providers', 'auriga-managed', 'models'],
          );
          assert.equal(
            operation.path.length,
            5,
            'only scalar capability paths are written, never projected model objects',
          );
          assert.ok(['contextWindow', 'maxTokens'].includes(operation.path[4]));
        }
        for (const operation of ops)
          saved[Number(operation.path[3])][operation.path[4]] = operation.value;
        revision++;
        return { ok: true, value: {} };
      },
    },
  };
  dom.window.__ModuleLoader__ = {
    load: (value) => {
      module = value;
    },
  };
  vm.runInNewContext(
    await readFile(new URL('../src/client.js', import.meta.url), 'utf8'),
    {
      window: dom.window,
      document: dom.window.document,
      URL,
      AbortController,
      setTimeout,
      clearTimeout,
    },
  );
  const plugin = module.factory((name) => (name === 'react' ? React : {}));
  const ctx = {
    remote,
    locale: {
      register: (_ns, value) => {
        copy = value;
      },
      bind: () => (key) => copy[locale][key] ?? key,
      getLocale: () => ({ active: locale }),
    },
    theme: { getTheme: () => ({ active: { colorScheme: 'dark' } }) },
    inject: (deps, fn) => {
      if (deps.includes('remote.llm')) return fn(ctx);
    },
    slots: {
      inject: (_name, fn) => fn(),
      register: (options, fn) => {
        if (options.name === 'settings.models.provider-card') component = fn;
      },
    },
    on: () => () => {},
    effect: (fn) => {
      const stop = fn();
      cleanups.push(stop);
    },
    get: () => undefined,
  };
  const stop = plugin.apply(ctx);
  let disposed = false;
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    cleanups.reverse().forEach((fn) => fn?.());
    stop();
    dom.window.close();
  };
  t.after(dispose);
  return {
    render(provider = 'auriga-managed') {
      assert.equal(
        typeof component,
        'function',
        'the public native provider-card slot is registered',
      );
      cursor = 0;
      return component({ provider: { provider, settingsNs: 'llm-pi-ai' } });
    },
    saved,
    conflict: () => {
      revision++;
    },
    fail: () => {
      fail = true;
    },
    directory: (value) => {
      directory = value;
    },
    dispose,
    hold() {
      let resume;
      remote.llm.discoverModels = async () => {
        requests++;
        return new Promise((resolve) => {
          resume = () => resolve({ ok: true, value: directory });
        });
      };
      return () => resume();
    },
    get counts() {
      return { reads, requests, writes };
    },
  };
}
const children = (node) =>
  node && typeof node === 'object'
    ? [node, ...node.children.flatMap(children)]
    : [];
const text = (node) =>
  node && typeof node === 'object'
    ? node.children.map(text).join(' ')
    : String(node ?? '');
const button = (node, label) =>
  children(node).find(
    (child) => child.type === 'button' && text(child) === label,
  );

test('manual native sync previews updated same-ID capacity and applies once without changing names or selection', async (t) => {
  const f = await fixture(t);
  let tree = f.render();
  assert.deepEqual(f.counts, { reads: 0, requests: 0, writes: 0 });
  await button(tree, '同步已有模型能力').props.onClick();
  tree = f.render();
  assert.match(text(tree), /32768.*1000000/);
  assert.match(text(tree), /4096.*131072/);
  assert.equal(f.saved[0].contextWindow, 32768);
  assert.equal(f.counts.writes, 0);
  await button(tree, '应用更新').props.onClick();
  assert.deepEqual(f.saved, [
    {
      id: 'auto',
      name: 'My Auto',
      contextWindow: 1000000,
      maxTokens: 131072,
      input: ['text'],
      compat: { supportsStore: false },
    },
    { id: 'local-only', name: 'Kept', contextWindow: 9000, maxTokens: 1000 },
  ]);
  assert.equal(f.counts.writes, 1);
  assert.match(text(f.render()), /已同步/);
});
test('cancel and failed fetch do not write; personal cards do not expose sync', async (t) => {
  const f = await fixture(t);
  assert.equal(f.render('personal'), null);
  await button(f.render(), '同步已有模型能力').props.onClick();
  button(f.render(), '取消').props.onClick();
  assert.equal(f.counts.writes, 0);
  f.fail();
  await button(f.render(), '同步已有模型能力').props.onClick();
  assert.equal(f.counts.writes, 0);
  assert.match(text(f.render()), /暂时无法/);
});
test('a concurrent native edit rejects the preview CAS without retry or overwriting the new edit', async (t) => {
  const f = await fixture(t);
  await button(f.render(), '同步已有模型能力').props.onClick();
  f.conflict();
  await button(f.render(), '应用更新').props.onClick();
  assert.equal(f.counts.writes, 1);
  assert.equal(f.saved[0].contextWindow, 32768);
  assert.match(text(f.render()), /已更改.*重新获取/);
});
test('unchanged capacities do not produce a save and English labels are available', async (t) => {
  const f = await fixture(t, 'en');
  f.directory([{ id: 'auto', contextWindow: 32768, maxTokens: 4096 }]);
  await button(f.render(), 'Sync existing model capabilities').props.onClick();
  assert.equal(f.counts.writes, 0);
  assert.match(text(f.render()), /No known capability changes/);
});
test('unknown upstream capability preserves that local value and invalid capacity rejects before preview', async (t) => {
  const f = await fixture(t);
  f.directory([{ id: 'auto', contextWindow: 1000000 }]);
  await button(f.render(), '同步已有模型能力').props.onClick();
  await button(f.render(), '应用更新').props.onClick();
  assert.equal(f.saved[0].contextWindow, 1000000);
  assert.equal(f.saved[0].maxTokens, 4096);
  f.directory([{ id: 'auto', contextWindow: Infinity }]);
  await button(f.render(), '同步已有模型能力').props.onClick();
  assert.equal(f.counts.writes, 1);
  assert.equal(button(f.render(), '应用更新'), undefined);
  assert.match(text(f.render()), /暂时无法/);
});
test('rapid clicks are single flight and disposal prevents a late result from becoming a writable preview', async (t) => {
  const f = await fixture(t);
  const resume = f.hold();
  const action = button(f.render(), '同步已有模型能力').props.onClick;
  const pending = action();
  await action();
  assert.equal(f.counts.requests, 1);
  f.dispose();
  resume();
  await pending;
  assert.deepEqual(f.counts, { requests: 1, reads: 0, writes: 0 });
});
