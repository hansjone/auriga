import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createManagedModels,
  MANAGED_MODEL_REF,
} from '../src/managed/models.js';

const NS = 'llm-pi-ai',
  ID = 'auriga-managed';
const legacy =
  "'Bearer ' + JSON.parse(process.getBuiltinModule('node:fs').readFileSync(\"/run/auriga/model-credential.json\", 'utf8')).credential";
const row = (id, context = 64000, output = 8192) => ({
  id,
  object: 'model',
  created: 0,
  owned_by: 'auriga',
  context_window: context,
  max_output_tokens: output,
});
function fixture(t, options = {}) {
  const material = {
    origin: 'https://portal.example.com',
    value: {
      credential: 'fake-private-key',
      baseUrl: 'https://portal.example.com/ai/v1',
      models: [{ id: 'auto', contextWindow: 32768, maxOutputTokens: 4096 }],
    },
  };
  const value = {
    providers: {
      [ID]: {
        api: 'openai-completions',
        baseURL: material.value.baseUrl,
        apiKeyEnv: MANAGED_MODEL_REF,
        compat: { supportsStore: false },
        models: [
          { id: 'auto', name: 'Mine', contextWindow: 32000, maxTokens: 2000 },
        ],
      },
      personal: { apiKeyEnv: 'PERSONAL_KEY', models: [{ id: 'personal' }] },
    },
  };
  const defaults = {
    ns: 'agent-default-model',
    user: { provider: 'personal', model: 'chosen' },
    value: {},
    revision: 1,
  };
  const entry = { options: { id: NS, config: value } };
  let requests = 0,
    writes = 0,
    edits = 0,
    directory = [row('auto'), row('new', 24576, 2048)];
  const settings = {
    describe: () => [{ ns: NS, value, revision: 1 }, defaults],
    mutate: async (ns, ops) => {
      writes++;
      assert.equal(ns, 'agent-default-model');
      defaults.user = Object.fromEntries(
        ops.filter((op) => op.op === 'set').map((op) => [op.path[0], op.value]),
      );
    },
  };
  const editor = {
    configuration: () => [{ entry }],
    edit: async (_entry, change) => {
      edits++;
      const next = change(value);
      Object.assign(value, next);
      entry.options.config = value;
    },
  };
  const llm = { discoverModels: async (...args) => ({ personal: args }) };
  const credentials = {
    ctx: { fiber: { entry: { options: { id: NS } } } },
    resolve: async (ref) => ({ value: ref, source: 'personal' }),
    describe: async () => ({ configured: true, writable: true }),
  };
  const original = llm.discoverModels,
    resolve = credentials.resolve;
  const model = createManagedModels({
    loadMaterial: async () => material,
    requestCatalog: async (_material, signal) => {
      requests++;
      return options.request
        ? options.request(signal)
        : { object: 'list', data: directory };
    },
    ...options,
  });
  t.after(() => model.close());
  const attach = () => model.attach(llm, settings, editor, credentials);
  return {
    model,
    material,
    value,
    defaults,
    entry,
    settings,
    editor,
    llm,
    credentials,
    original,
    resolve,
    attach,
    discover: (patch = {}) =>
      llm.discoverModels(NS, { provider: ID, ...patch }),
    directory: (next) => {
      directory = next;
    },
    get requests() {
      return requests;
    },
    get writes() {
      return writes;
    },
    get edits() {
      return edits;
    },
  };
}

test('manual discovery returns current capacities without changing saved names, limits or choices', async (t) => {
  const f = fixture(t);
  f.attach();
  await f.model.ready;
  const before = globalThis.structuredClone(f.value);
  const found = await f.discover();
  assert.equal(found[1].contextWindow, 24576);
  assert.equal(found[1].maxTokens, 2048);
  assert.deepEqual(f.value, before);
  assert.equal(f.defaults.user.model, 'chosen');
  assert.equal(f.writes, 0);
  f.directory([row('later')]);
  assert.equal((await f.discover())[0].id, 'later');
});
test('binding and elapsed time never poll the model directory', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const f = fixture(t);
  f.attach();
  await f.model.ready;
  t.mock.timers.tick(90000);
  await Promise.resolve();
  assert.equal(f.requests, 0);
  assert.equal(f.edits, 0);
});
for (const patch of [
  { baseURL: 'https://other.example.com/v1' },
  { api: 'anthropic-messages' },
  { apiKey: 'new' },
])
  test(
    'managed draft rejects identity override ' + JSON.stringify(patch),
    async (t) => {
      const f = fixture(t);
      f.attach();
      await f.model.ready;
      await assert.rejects(f.discover(patch), { code: 'common.forbidden' });
      assert.equal(f.requests, 0);
    },
  );
test('personal discovery and personal credentials retain the original public path', async (t) => {
  const f = fixture(t);
  f.attach();
  await f.model.ready;
  assert.deepEqual(
    await f.llm.discoverModels(NS, {
      provider: 'personal',
      baseURL: 'https://personal.example.com',
    }),
    {
      personal: [
        NS,
        { provider: 'personal', baseURL: 'https://personal.example.com' },
        undefined,
      ],
    },
  );
  assert.deepEqual(await f.credentials.resolve('PERSONAL_KEY'), {
    value: 'PERSONAL_KEY',
    source: 'personal',
  });
});
test('reserved reference resolves per operation in the pi-ai owner and describe never returns a value', async (t) => {
  const f = fixture(t);
  f.attach();
  await f.model.ready;
  assert.equal(
    (await f.credentials.resolve(MANAGED_MODEL_REF)).value,
    'fake-private-key',
  );
  f.material.value.credential = 'fake-rotated-key';
  assert.equal(
    (await f.credentials.resolve(MANAGED_MODEL_REF)).value,
    'fake-rotated-key',
  );
  assert.deepEqual(await f.credentials.describe(MANAGED_MODEL_REF), {
    configured: true,
    writable: false,
    source: 'auriga-managed',
  });
});
for (const id of ['settings-controller', 'another-pi-ai', undefined])
  test(
    'non-owning server scope cannot resolve the reserved secret ' + String(id),
    async (t) => {
      const f = fixture(t);
      f.attach();
      await f.model.ready;
      await assert.rejects(
        f.credentials.resolve.call(
          { ctx: { fiber: { entry: { options: { id } } } } },
          MANAGED_MODEL_REF,
        ),
        { code: 'common.forbidden' },
      );
    },
  );
test('an imported personal provider cannot turn the reserved reference into outbound authentication', async (t) => {
  const f = fixture(t);
  f.value.providers.personal.apiKeyEnv = MANAGED_MODEL_REF;
  f.attach();
  await assert.rejects(f.model.ready, { code: 'common.forbidden' });
  await assert.rejects(f.credentials.resolve(MANAGED_MODEL_REF), {
    code: 'common.forbidden',
  });
  assert.equal(f.requests, 0);
  assert.equal(
    f.value.providers.personal.apiKeyEnv,
    MANAGED_MODEL_REF,
    'do not silently rewrite personal settings',
  );
});
test('a fresh native section is seeded from material without replacing personal providers', async (t) => {
  const f = fixture(t);
  delete f.value.providers[ID];
  const personal = globalThis.structuredClone(f.value.providers.personal);
  f.attach();
  await f.model.ready;
  assert.deepEqual(f.value.providers.personal, personal);
  assert.deepEqual(f.value.providers[ID].models, [
    { id: 'auto', name: 'auto', contextWindow: 32768, maxTokens: 4096 },
  ]);
  assert.equal(f.value.providers[ID].apiKeyEnv, MANAGED_MODEL_REF);
  assert.equal(f.value.providers[ID].headers, undefined);
  assert.equal(f.requests, 0);
});
test('normal migration only removes the known platform header expression and retains saved preferences', async (t) => {
  const f = fixture(t);
  delete f.value.providers[ID].apiKeyEnv;
  f.value.providers[ID].headers = { Authorization: { __jsExpr: legacy } };
  const before = globalThis.structuredClone(f.value.providers[ID].models);
  f.attach();
  await f.model.ready;
  assert.deepEqual(f.value.providers[ID].models, before);
  assert.equal(f.value.providers[ID].headers, undefined);
  assert.equal(f.edits, 1);
});
test('unknown header expressions are refused rather than executed or stringified', async (t) => {
  const f = fixture(t);
  delete f.value.providers[ID].apiKeyEnv;
  f.value.providers[ID].headers = {
    Authorization: { __jsExpr: 'untrusted()' },
  };
  f.attach();
  await assert.rejects(f.model.ready, { code: 'common.forbidden' });
  assert.equal(f.edits, 0);
  assert.equal(f.requests, 0);
});
test('auto is initialized only for a current native auto and an unset explicit choice', async (t) => {
  const f = fixture(t);
  f.defaults.user = {};
  f.attach();
  await f.model.ready;
  assert.equal(f.defaults.user.model, 'auto');
  assert.equal(f.writes, 1);
});
test('missing auto never invents it or changes an explicit legacy default', async (t) => {
  const f = fixture(t);
  f.value.providers[ID].models = [{ id: 'legacy' }];
  f.defaults.user = {};
  f.attach();
  await f.model.ready;
  assert.equal(f.writes, 0);
});
test('failed manual fetch preserves every saved model and propagates refusal', async (t) => {
  const f = fixture(t, {
    request: async () => {
      throw Object.assign(new Error('unavailable'), { status: 403 });
    },
  });
  f.attach();
  await f.model.ready;
  const before = globalThis.structuredClone(f.value);
  await assert.rejects(f.discover(), { status: 403 });
  assert.deepEqual(f.value, before);
});
test('a hung manual fetch is bounded without writing partial models', async (t) => {
  const f = fixture(t, { timeoutMs: 5, request: () => new Promise(() => {}) });
  f.attach();
  await f.model.ready;
  await assert.rejects(f.discover());
  assert.equal(f.writes, 0);
});
test('unloading cancels a pending discovery and a cached callback cannot revive it', async (t) => {
  let entered;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const f = fixture(t, {
    request: async (signal) => {
      entered(signal);
      return new Promise(() => {});
    },
  });
  const release = f.attach();
  await f.model.ready;
  const cached = f.llm.discoverModels;
  const pending = f.discover();
  const signal = await started;
  release();
  await assert.rejects(pending);
  assert.equal(signal.aborted, true);
  await assert.rejects(cached(NS, { provider: ID }));
  assert.equal(f.llm.discoverModels, f.original);
  assert.equal(f.credentials.resolve, f.resolve);
});
test('late old cleanup cannot close a new valid binding', async (t) => {
  const f = fixture(t);
  const old = f.attach();
  await f.model.ready;
  const next = createManagedModels({
    loadMaterial: async () => f.material,
    requestCatalog: async () => ({ object: 'list', data: [row('new-owner')] }),
  });
  t.after(() => next.close());
  next.attach(f.llm, f.settings, f.editor, f.credentials);
  await next.ready;
  old();
  f.model.close();
  assert.equal((await f.discover())[0].id, 'new-owner');
});
test('manual retry recovers a transient initialization failure without reviving an unloaded owner', async (t) => {
  let attempts = 0,
    f;
  f = fixture(t, {
    loadMaterial: async () => {
      if (++attempts === 1) throw new Error('temporarily unavailable');
      return f.material;
    },
  });
  f.attach();
  await assert.rejects(f.model.ready, /temporarily unavailable/);
  assert.equal((await f.discover())[1].id, 'new');
  f.model.close();
  assert.throws(() => f.attach(), { code: 'common.forbidden' });
});
