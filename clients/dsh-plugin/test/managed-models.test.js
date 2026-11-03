import test from 'node:test';
import assert from 'node:assert/strict';
import { createManagedModels } from '../src/managed/models.js';

const row = (id, context = 32768, output = 4096) => ({
  id,
  object: 'model',
  created: 0,
  owned_by: 'auriga',
  context_window: context,
  max_output_tokens: output,
});
function fixture(t, options = {}) {
  let directory = [row('auto')],
    requests = 0,
    writes = 0;
  let fail;
  const value = {
    providers: {
      'auriga-managed': {
        models: [{ id: 'legacy', contextWindow: 32768, maxTokens: 4096 }],
      },
    },
  };
  let revision = 1;
  const defaults = { ns: 'agent-default-model', value: {}, user: {} };
  const settings = {
    describe: () => [{ ns: 'llm-pi-ai', value, revision }, defaults],
    async mutate(ns, ops, expected) {
      if (ns === 'agent-default-model') {
        defaults.user = Object.fromEntries(
          ops.map((op) => [op.path[0], op.value]),
        );
        return;
      }
      assert.equal(expected, revision);
      writes++;
      value.providers['auriga-managed'].models = globalThis.structuredClone(
        ops[0].value,
      );
      revision++;
    },
  };
  const llm = {
    listModels: async () => value.providers['auriga-managed'].models,
    discoverModels: async (...args) => ({ personal: args }),
  };
  const originalDiscovery = llm.discoverModels;
  const models = createManagedModels({
    loadMaterial: async () => ({
      value: {
        baseUrl: 'https://portal.example.com/ai/v1',
        credential: 'private-fixture',
        models: [{ id: 'legacy', contextWindow: 32768, maxOutputTokens: 4096 }],
      },
      origin: 'https://portal.example.com',
    }),
    requestCatalog: async (_material, signal) => {
      requests++;
      if (fail) throw fail;
      if (options.request) return options.request(signal);
      return { object: 'list', data: directory };
    },
    ...options,
  });
  t.after(() => models.close());
  return {
    models,
    llm,
    settings,
    value,
    defaults,
    originalDiscovery,
    directory: (next) => {
      directory = next;
    },
    fail: (next) => {
      fail = next;
    },
    get requests() {
      return requests;
    },
    get writes() {
      return writes;
    },
  };
}

test('online models supersede sealed ids, update live native settings and seed auto only without explicit selection', async (t) => {
  const f = fixture(t);
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  assert.deepEqual(
    f.value.providers['auriga-managed'].models.map((m) => m.id),
    ['auto'],
  );
  assert.equal(f.defaults.user.model, 'auto');
  assert.equal(f.models.status.available, true);
  const writes = f.writes;
  await f.models.refresh();
  assert.equal(f.writes, writes, 'unchanged catalog never rewrites settings');
  f.directory([row('auto', 64000, 8192), row('new')]);
  await f.models.refresh();
  assert.equal(
    f.value.providers['auriga-managed'].models[0].contextWindow,
    64000,
  );
  assert.equal(f.value.providers['auriga-managed'].models[1].id, 'new');
  assert.doesNotMatch(
    JSON.stringify(f.models.status),
    /private-fixture|portal|legacy/,
  );
});

test('native discovery pins only the managed route and leaves personal providers untouched', async (t) => {
  const f = fixture(t);
  const detach = f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  for (const patch of [
    { baseURL: 'https://other.example/v1' },
    { api: 'anthropic-messages' },
    { apiKey: 'new' },
  ]) {
    const count = f.requests;
    await assert.rejects(
      f.llm.discoverModels('llm-pi-ai', {
        provider: 'auriga-managed',
        ...patch,
      }),
      { code: 'common.forbidden' },
    );
    assert.equal(f.requests, count);
  }
  const found = await f.llm.discoverModels('llm-pi-ai', {
    provider: 'auriga-managed',
  });
  assert.equal(found[0].name, 'auto');
  assert.deepEqual(
    await f.llm.discoverModels('llm-pi-ai', {
      provider: 'personal',
      baseURL: 'https://personal.example',
    }),
    {
      personal: [
        'llm-pi-ai',
        { provider: 'personal', baseURL: 'https://personal.example' },
        undefined,
      ],
    },
  );
  detach();
  assert.equal(f.llm.discoverModels, f.originalDiscovery);
});

test('refresh retains explicit defaults and custom names, does not invent auto, and empties a successful revoked catalog', async (t) => {
  const f = fixture(t);
  f.defaults.user = { provider: 'personal', model: 'chosen' };
  f.directory([row('legacy')]);
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  f.value.providers['auriga-managed'].models[0].name = '我的模型';
  f.directory([row('legacy', 64000)]);
  await f.models.refresh();
  assert.equal(f.value.providers['auriga-managed'].models[0].name, '我的模型');
  assert.equal(f.defaults.user.model, 'chosen');
  f.directory([]);
  await f.models.refresh();
  assert.deepEqual(f.value.providers['auriga-managed'].models, []);
  assert.equal(f.models.status.authorized, false);
});

test('failed refresh retains the directory with stale status, permission refusal never reports ready', async (t) => {
  const f = fixture(t);
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  f.fail(new Error('offline private details'));
  await f.models.refresh();
  assert.equal(f.value.providers['auriga-managed'].models[0].id, 'auto');
  assert.equal(f.models.status.stale, true);
  assert.equal(f.models.status.available, false);
  f.fail(Object.assign(new Error('secret'), { status: 403 }));
  await f.models.refresh();
  assert.equal(f.models.status.state, 'managed_model_forbidden');
  assert.equal(f.models.status.authorized, false);
});

test('concurrent refresh is single flight, close cancels and late results never write', async (t) => {
  let resolve, entered;
  const ready = new Promise((r) => {
    entered = r;
  });
  const f = fixture(t, {
    request: (signal) => {
      entered(signal);
      return new Promise((r) => {
        resolve = r;
      });
    },
  });
  f.models.attach(f.llm, f.settings);
  const first = f.models.refresh();
  const signal = await ready;
  const second = f.models.refresh();
  f.models.close();
  await Promise.all([first, second]);
  assert.equal(f.requests, 1);
  assert.equal(signal.aborted, true);
  resolve({ object: 'list', data: [row('auto')] });
  await Promise.resolve();
  assert.equal(f.writes, 0);
});

test('the running instance fetches added models on its 30 second timer and stops on disposal', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const f = fixture(t);
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  f.directory([row('auto'), row('published-later')]);
  t.mock.timers.tick(30000);
  await f.models.refresh();
  assert.equal(f.requests, 2);
  assert.equal(
    f.value.providers['auriga-managed'].models[1].id,
    'published-later',
  );
  f.models.close();
  t.mock.timers.tick(60000);
  assert.equal(f.requests, 2);
  assert.equal(f.llm.discoverModels, f.originalDiscovery);
});

test('a revision conflict rereads once and preserves concurrently saved model preferences', async (t) => {
  const f = fixture(t);
  f.directory([row('legacy', 64000)]);
  const mutate = f.settings.mutate;
  let conflict = true;
  f.settings.mutate = async (...args) => {
    if (conflict) {
      conflict = false;
      f.value.providers['auriga-managed'].models[0].name = '并发保存';
      throw Object.assign(new Error('conflict'), { code: 'SETTINGS_CONFLICT' });
    }
    return mutate(...args);
  };
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  assert.equal(f.value.providers['auriga-managed'].models[0].name, '并发保存');
  assert.equal(
    f.value.providers['auriga-managed'].models[0].contextWindow,
    64000,
  );
  assert.equal(f.requests, 1);
});

test('a hung catalog is bounded and never writes a partial directory', async (t) => {
  const f = fixture(t, { timeoutMs: 5, request: () => new Promise(() => {}) });
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  assert.equal(f.writes, 0);
  assert.equal(f.models.status.available, false);
});

test('explicit capacity preferences survive equal platform limits and later expansion', async (t) => {
  const f = fixture(t);
  f.directory([row('auto', 64000, 8192)]);
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  Object.assign(f.value.providers['auriga-managed'].models[0], {
    name: '我的模型',
    contextWindow: 32000,
    maxTokens: 2000,
  });
  f.directory([row('auto', 32000, 2000)]);
  await f.models.refresh();
  f.directory([row('auto', 64000, 8192)]);
  await f.models.refresh();
  assert.deepEqual(f.value.providers['auriga-managed'].models[0], {
    id: 'auto',
    name: '我的模型',
    contextWindow: 32000,
    maxTokens: 2000,
  });
});

test('temporary platform clamping retains the requested capacities and still refreshes uncustomized models', async (t) => {
  const f = fixture(t);
  f.directory([row('auto', 64000, 8192), row('unmodified', 64000, 8192)]);
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  Object.assign(f.value.providers['auriga-managed'].models[0], {
    contextWindow: 48000,
    maxTokens: 4000,
  });
  f.directory([row('auto', 32000, 2000), row('unmodified', 32000, 2000)]);
  await f.models.refresh();
  assert.equal(
    f.value.providers['auriga-managed'].models[0].contextWindow,
    32000,
  );
  f.directory([row('auto', 64000, 8192), row('unmodified', 64000, 8192)]);
  await f.models.refresh();
  const [custom, unmodified] = f.value.providers['auriga-managed'].models;
  assert.equal(custom.contextWindow, 48000);
  assert.equal(custom.maxTokens, 4000);
  assert.equal(unmodified.contextWindow, 64000);
  assert.equal(unmodified.maxTokens, 8192);
});

test('personal names and exclusions survive temporary platform withdrawal, and native readdition remains possible', async (t) => {
  const f = fixture(t);
  f.directory([row('auto'), row('hidden')]);
  f.models.attach(f.llm, f.settings);
  await f.models.refresh();
  f.value.providers['auriga-managed'].models = [
    { ...f.value.providers['auriga-managed'].models[0], name: '保留名称' },
  ];
  f.directory([]);
  await f.models.refresh();
  f.directory([row('auto'), row('hidden')]);
  await f.models.refresh();
  assert.deepEqual(
    f.value.providers['auriga-managed'].models.map((model) => [
      model.id,
      model.name,
    ]),
    [['auto', '保留名称']],
  );
  f.value.providers['auriga-managed'].models.push({
    id: 'hidden',
    name: 'hidden',
    contextWindow: 32768,
    maxTokens: 4096,
  });
  await f.models.refresh();
  assert.equal(f.value.providers['auriga-managed'].models.length, 2);
});
