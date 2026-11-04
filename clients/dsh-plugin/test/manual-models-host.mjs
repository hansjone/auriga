// Public, locked rc.3 modules with a disposable profile and fake upstream only.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createServer } from 'node:http';
import {
  createManagedModels,
  MANAGED_MODEL_REF,
} from '../src/managed/models.js';
import { installManagedSettingsPolicy } from '../src/managed/settings-policy.js';

export async function runNativeModels(
  runtimeRoot,
  patchFile,
  metaRoot,
  scratch,
) {
  const req = createRequire(join(resolve(runtimeRoot), 'package.json'));
  const load = (name) =>
    import(pathToFileURL(req.resolve('@deepseek-ai/' + name)).href);
  const [
    {
      boot,
      initProfile,
      readProfilePatches,
      loadOverlayPatches,
      reconcileProfilePatches,
    },
    { default: Editor },
    { default: Settings },
    { default: Credentials },
    { default: Llm, markAgentLoopRequest, isAgentLoopRequest },
    Pi,
    { default: SettingsController, CredentialsController },
    { remoteMethods },
  ] = await Promise.all([
    load('dsh-app-boot'),
    load('dsh-config-editor'),
    load('dsh-settings'),
    load('dsh-credentials-local'),
    load('dsh-llm'),
    load('dsh-llm-pi-ai'),
    load('dsh-api-settings-controller'),
    load('dsh-typert-protocol'),
  ]);
  assert.equal(
    req('@deepseek-ai/dsh-llm-pi-ai/package.json').version,
    '0.2.1-rc.3',
  );
  const rows = loadOverlayPatches('manual-model-probe', patchFile).filter(
    (row) => row.id === 'llm-pi-ai',
  );
  assert.equal(rows.length, 1);
  assert.equal(
    rows[0].config,
    undefined,
    'producer must not pin the native editable section',
  );
  const material = JSON.parse(
    readFileSync(join(metaRoot, 'model-credential.json'), 'utf8'),
  );
  const requests = [];
  let redirect = false,
    catalogContext = 24576,
    catalogOutput = 2048;
  const receive = async (request, response) => {
    requests.push({
      path: request.url,
      authorization: request.headers.authorization,
      origin: 'http://' + request.headers.host,
    });
    if (request.url.endsWith('/models')) {
      if (redirect) {
        response.writeHead(302, { location: '/personal/models' });
        response.end();
        return;
      }
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(
        JSON.stringify({
          object: 'list',
          data: [
            {
              id: 'added',
              object: 'model',
              created: 0,
              owned_by: 'auriga',
              context_window: catalogContext,
              max_output_tokens: catalogOutput,
            },
          ],
        }),
      );
      return;
    }
    for await (const _chunk of request) {
      // Drain the fake request before returning its deterministic stream.
    }
    response.writeHead(200, { 'content-type': 'text/event-stream' });
    response.end(
      'data: {"id":"fake","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}\n\ndata: {"id":"fake","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}\n\ndata: [DONE]\n\n',
    );
  };
  const upstream = createServer(receive),
    personalUpstream = createServer(receive);
  await new Promise((done) => upstream.listen(0, '127.0.0.1', done));
  await new Promise((done) => personalUpstream.listen(0, '127.0.0.1', done));
  const origin = 'http://127.0.0.1:' + upstream.address().port;
  const personalOrigin = 'http://127.0.0.1:' + personalUpstream.address().port;
  material.baseUrl = origin + '/ai/v1';
  let paused;
  const loadMaterial = async () => {
    const gate = paused;
    if (gate) {
      gate.enter();
      await gate.wait;
    }
    return { value: material, origin };
  };
  const pauseMaterial = () => {
    let enter, release;
    const entered = new Promise((resolve) => {
      enter = resolve;
    });
    const wait = new Promise((resolve) => {
      release = resolve;
    });
    const gate = { enter, release, entered, wait };
    paused = gate;
    return gate;
  };
  const legacy =
    "'Bearer ' + JSON.parse(process.getBuiltinModule('node:fs').readFileSync(\"/run/auriga/model-credential.json\", 'utf8')).credential";
  const builtin = process.getBuiltinModule;
  // Evaluate only this known legacy producer expression against fake material.
  process.getBuiltinModule = (name) =>
    name === 'node:fs'
      ? {
          ...builtin(name),
          readFileSync: (file, ...args) =>
            file === '/run/auriga/model-credential.json'
              ? JSON.stringify(material)
              : builtin(name).readFileSync(file, ...args),
        }
      : builtin(name);
  let ctx, release, binding;
  try {
    const home = join(scratch, 'native-models'),
      dir = join(home, 'profile');
    initProfile(dir, ['model-probe']);
    const bundle = join(dir, 'node_modules', 'model-probe');
    mkdirSync(bundle, { recursive: true });
    writeFileSync(join(home, 'package.json'), '{"name":"manual-model-probe"}');
    writeFileSync(
      join(bundle, 'package.json'),
      JSON.stringify({
        name: 'model-probe',
        version: '1.0.0',
        dsh: { bundle: { patch: 'cordis.patch.yml' } },
      }),
    );
    writeFileSync(
      join(bundle, 'cordis.patch.yml'),
      JSON.stringify([
        {
          insert: [
            { id: 'editor', name: 'cordis:editor' },
            { id: 'settings', name: 'cordis:settings' },
            { id: 'llm', name: 'cordis:llm' },
            {
              id: 'credentials',
              name: 'cordis:credentials',
              config: { dshHome: home, watch: false },
            },
            { id: 'llm-pi-ai', name: 'cordis:pi' },
          ],
        },
      ]),
    );
    writeFileSync(join(dir, 'cordis.yml'), '[]');
    const personal = {
      api: 'openai-completions',
      baseURL: personalOrigin + '/personal',
      apiKeyEnv: 'PERSONAL_KEY',
      models: [{ id: 'personal' }],
    };
    const models = [
      { id: 'pool-a', name: 'My name', contextWindow: 16000, maxTokens: 1000 },
    ];
    const overlay = globalThis.structuredClone(rows);
    const provider = {
      displayName: 'Auriga',
      api: 'openai-completions',
      baseURL: material.baseUrl,
      models,
      compat: { supportsStore: false },
      headers: { Authorization: { __jsExpr: legacy } },
    };
    overlay[0].config = { providers: { 'auriga-managed': provider } };
    const overlayPath = join(home, 'legacy.patch.json');
    writeFileSync(overlayPath, JSON.stringify(overlay));
    writeFileSync(
      join(dir, 'cordis.patch.yml'),
      JSON.stringify([
        {
          id: 'llm-pi-ai',
          config: { providers: { personal, 'auriga-managed': provider } },
        },
      ]),
    );
    const profile = {
      name: 'managed',
      startedBundles: ['model-probe'],
      dir,
      patchPath: join(dir, 'cordis.patch.yml'),
      installAnchor: join(home, 'package.json'),
      cwd: home,
      home,
      overlays: loadOverlayPatches('manual-model-probe', overlayPath),
      telemetryDisabledEnv: '1',
    };
    const start = () =>
      boot(
        'manual-model-probe',
        join(dir, 'cordis.yml'),
        readProfilePatches('manual-model-probe', profile),
        (host) => {
          host.provide('profileContext', profile);
          Object.assign(host.loader.builtins, {
            editor: Editor,
            settings: Settings,
            llm: Llm,
            credentials: Credentials,
            pi: Pi,
          });
        },
      );
    ctx = await start();
    let settings = new SettingsController(ctx),
      credentials = new CredentialsController(ctx);
    const originalPrepare = ctx.llm.prepareCall;
    const describe = () =>
      settings.describe().namespaces.find((row) => row.ns === 'llm-pi-ai');
    await assert.rejects(
      settings.mutate(
        'llm-pi-ai',
        [
          {
            op: 'set',
            path: ['providers', 'auriga-managed', 'models'],
            value: models,
          },
        ],
        describe().revision,
      ),
      (error) => /headers.Authorization expected string/.test(error.message),
    );
    console.log(
      'legacy producer: exact native save schema rejection reproduced',
    );
    profile.overlays = rows;
    await reconcileProfilePatches(
      ctx,
      readProfilePatches('manual-model-probe', profile),
      'manual-model-probe',
    );
    installManagedSettingsPolicy(ctx);
    binding = createManagedModels({ loadMaterial });
    release = binding.attach(
      ctx.llm,
      ctx.settings,
      ctx.configEditor,
      ctx.credentials,
    );
    await binding.ready;
    const personalBefore = globalThis.structuredClone(
      describe().value.providers.personal,
    );
    assert.deepEqual(
      describe().value.providers['auriga-managed'].headers ?? {},
      {},
    );
    const candidate = await ctx.llm.remoteDiscoverModels('llm-pi-ai', {
      provider: 'auriga-managed',
      baseURL: material.baseUrl,
      api: 'openai-completions',
    });
    assert.deepEqual(candidate, [
      { id: 'added', name: 'added', contextWindow: 24576, maxTokens: 2048 },
    ]);
    await settings.mutate(
      'llm-pi-ai',
      [
        {
          op: 'set',
          path: ['providers', 'auriga-managed', 'models'],
          value: [...models, ...candidate],
        },
      ],
      describe().revision,
    );
    assert.deepEqual(
      describe().value.providers['auriga-managed'].models.map(
        ({ id, name, contextWindow, maxTokens }) => ({
          id,
          name,
          contextWindow,
          maxTokens,
        }),
      ),
      [...models, ...candidate],
    );
    assert.deepEqual(describe().value.providers.personal, personalBefore);
    // The native picker preserves old same-ID objects. The explicit capability
    // sync uses the current authenticated directory and a normal revision CAS.
    catalogContext = 1000000;
    catalogOutput = 131072;
    const updatedDirectory = await ctx.llm.remoteDiscoverModels('llm-pi-ai', {
      provider: 'auriga-managed',
    });
    assert.equal(updatedDirectory[0].contextWindow, catalogContext);
    assert.equal(updatedDirectory[0].maxTokens, catalogOutput);
    assert.equal(
      describe().value.providers['auriga-managed'].models[1].contextWindow,
      24576,
      'fetching alone never overwrites the saved same-ID model',
    );
    const sameIDSnapshot = describe();
    const synchronized = sameIDSnapshot.value.providers[
      'auriga-managed'
    ].models.map((model) =>
      model.id === 'added'
        ? {
            ...model,
            contextWindow: updatedDirectory[0].contextWindow,
            maxTokens: updatedDirectory[0].maxTokens,
          }
        : model,
    );
    await settings.mutate(
      'llm-pi-ai',
      [
        {
          op: 'set',
          path: ['providers', 'auriga-managed', 'models', '1', 'contextWindow'],
          value: catalogContext,
        },
        {
          op: 'set',
          path: ['providers', 'auriga-managed', 'models', '1', 'maxTokens'],
          value: catalogOutput,
        },
      ],
      sameIDSnapshot.revision,
    );
    candidate[0] = {
      ...candidate[0],
      contextWindow: catalogContext,
      maxTokens: catalogOutput,
    };
    assert.deepEqual(
      describe().value.providers['auriga-managed'].models,
      synchronized,
    );
    assert.deepEqual(describe().value.providers.personal, personalBefore);
    assert.equal(typeof credentials.resolve, 'undefined');
    assert.equal(
      remoteMethods(credentials).some(
        (method) => (method.exportName ?? method.method) === 'resolve',
      ),
      false,
      'the browser Remote manifest must not expose resolve',
    );
    await assert.rejects(ctx.credentials.resolve(MANAGED_MODEL_REF), {
      code: 'common.forbidden',
    });
    const info = (await credentials.describe([MANAGED_MODEL_REF]))[
      MANAGED_MODEL_REF
    ];
    assert.equal(info.writable, false);
    assert.equal('value' in info, false);
    assert.ok(
      !JSON.stringify(settings.describe()).includes(material.credential),
    );
    await credentials.set('PERSONAL_KEY', 'fake-personal-key');
    const stream = async (provider, model, prepared) => {
      const chunks = [];
      const options = {
        provider,
        model,
        maxTokens: 4,
        messages: [{ role: 'user', content: [{ type: 'text', text: 'fake' }] }],
      };
      for await (const chunk of prepared
        ? prepared.stream(options)
        : ctx.llm.stream(options))
        chunks.push(chunk);
      return chunks;
    };
    await stream('auriga-managed', 'added');
    assert.ok(
      requests.some(
        (row) =>
          row.path === '/ai/v1/chat/completions' &&
          row.authorization === 'Bearer ' + material.credential,
      ),
    );
    await stream('personal', 'personal');
    assert.ok(
      requests.some(
        (row) =>
          row.path === '/personal/chat/completions' &&
          row.authorization === 'Bearer fake-personal-key',
      ),
    );
    for (const patch of [
      { baseURL: origin + '/personal' },
      { api: 'anthropic-messages' },
      { apiKey: 'fake-key' },
    ]) {
      const count = requests.length;
      await assert.rejects(
        ctx.llm.remoteDiscoverModels('llm-pi-ai', {
          provider: 'auriga-managed',
          ...patch,
        }),
      );
      assert.equal(requests.length, count);
    }
    await assert.rejects(credentials.set(MANAGED_MODEL_REF, 'fake-overwrite'));
    await assert.rejects(
      settings.update('llm-pi-ai', {
        providers: { personal: { apiKeyEnv: MANAGED_MODEL_REF } },
      }),
    );
    let entry = ctx.configEditor
      .entries()
      .find((row) => row.options.id === 'llm-pi-ai');
    // An old/imported personal route must not turn the reserved name into a key.
    await ctx.configEditor.edit(entry, (current) => ({
      ...current,
      providers: {
        ...current.providers,
        personal: { ...personal, apiKeyEnv: MANAGED_MODEL_REF },
      },
    }));
    const count = requests.length;
    await stream('personal', 'personal');
    assert.equal(requests.length, count);
    for (const action of ['repair', 'remove', 'rebind', 'prepared-repair']) {
      await ctx.configEditor.edit(entry, (current) => ({
        ...current,
        providers: {
          ...current.providers,
          personal: { ...personal, apiKeyEnv: MANAGED_MODEL_REF },
        },
      }));
      const prepared =
        action === 'prepared-repair'
          ? await ctx.llm.prepareCall({
              provider: 'personal',
              model: 'personal',
              maxTokens: 4,
            })
          : undefined;
      const gate = pauseMaterial();
      const pending = stream('personal', 'personal', prepared);
      let timeout;
      await Promise.race([
        gate.entered,
        new Promise((_, reject) => {
          timeout = setTimeout(
            () =>
              reject(
                new Error('credential read did not enter the controlled pause'),
              ),
            4000,
          );
        }),
      ]).finally(() => clearTimeout(timeout));
      await settings.mutate(
        'llm-pi-ai',
        [
          action === 'remove'
            ? { op: 'unset', path: ['providers', 'personal'] }
            : {
                op: 'set',
                path: ['providers', 'personal', 'apiKeyEnv'],
                value: 'PERSONAL_KEY',
              },
        ],
        describe().revision,
      );
      if (action === 'rebind') {
        release();
        binding.close();
        binding = createManagedModels({ loadMaterial });
        release = binding.attach(
          ctx.llm,
          ctx.settings,
          ctx.configEditor,
          ctx.credentials,
        );
      }
      paused = undefined;
      gate.release();
      await pending;
      await binding.ready;
      assert.equal(
        requests.filter(
          (row) =>
            row.origin === personalOrigin &&
            row.authorization === 'Bearer ' + material.credential,
        ).length,
        0,
        'a repaired/deleted/rebound current route cannot authorize a captured personal operation: ' +
          action,
      );
      await ctx.configEditor.edit(entry, (current) => ({
        ...current,
        providers: { ...current.providers, personal },
      }));
    }
    // Prepared handles retain the target/configuration at preparation, even if the current route is repaired before dispatch.
    for (const owned of [true, false]) {
      await ctx.configEditor.edit(entry, (current) => ({
        ...current,
        providers: {
          ...current.providers,
          'auriga-managed': {
            ...current.providers['auriga-managed'],
            baseURL: personalOrigin + '/personal',
          },
        },
      }));
      const config = {
        provider: 'auriga-managed',
        model: 'added',
        maxTokens: 4,
      };
      const prepared = await (owned
        ? ctx.llm.prepareCall(config)
        : originalPrepare.call(ctx.llm, config));
      await ctx.configEditor.edit(entry, (current) => ({
        ...current,
        providers: {
          ...current.providers,
          'auriga-managed': {
            ...current.providers['auriga-managed'],
            baseURL: material.baseUrl,
          },
        },
      }));
      const before = requests.length;
      await stream('auriga-managed', 'added', prepared);
      assert.equal(
        requests.length,
        before,
        'old or unowned prepared targets never acquire a new grant',
      );
    }
    await Promise.all([
      stream('auriga-managed', 'added'),
      stream('personal', 'personal'),
    ]);
    const loopRequest = Object.freeze(
      markAgentLoopRequest({
        provider: 'auriga-managed',
        model: 'added',
        maxTokens: 4,
        messages: [{ role: 'user', content: [{ type: 'text', text: 'fake' }] }],
      }),
    );
    let loopIdentityPreserved = false;
    const stopLoop = ctx.on('llm/stream', (options, next) => {
      if (options === loopRequest)
        loopIdentityPreserved =
          isAgentLoopRequest(options) && Object.isFrozen(options);
      return next();
    });
    try {
      for await (const _chunk of ctx.llm.stream(loopRequest)) {
        /* Drain the fake marked request. */
      }
    } finally {
      stopLoop();
    }
    assert.equal(
      loopIdentityPreserved,
      true,
      'native loop request identity must survive the credential scope',
    );
    assert.ok(
      (await ctx.llm.listModels('auriga-managed')).some(
        (model) => model.id === 'added',
      ),
    );
    await ctx.configEditor.edit(entry, (current) => ({
      ...current,
      providers: { ...current.providers, personal },
    }));
    redirect = true;
    const before = requests.length;
    await assert.rejects(
      ctx.llm.remoteDiscoverModels('llm-pi-ai', { provider: 'auriga-managed' }),
    );
    assert.equal(requests.length, before + 1);
    redirect = false;
    for (const file of ['cordis.yml', 'cordis.patch.yml'])
      assert.ok(
        !readFileSync(join(dir, file), 'utf8').includes(material.credential),
      );
    assert.ok(
      !readFileSync(join(home, '.credentials.yaml'), 'utf8').includes(
        material.credential,
      ),
    );
    release();
    binding.close();
    assert.equal(await ctx.credentials.resolve(MANAGED_MODEL_REF), undefined);
    await ctx.fiber.dispose();
    ctx = await start();
    settings = new SettingsController(ctx);
    credentials = new CredentialsController(ctx);
    installManagedSettingsPolicy(ctx);
    binding = createManagedModels({ loadMaterial });
    release = binding.attach(
      ctx.llm,
      ctx.settings,
      ctx.configEditor,
      ctx.credentials,
    );
    await binding.ready;
    assert.deepEqual(
      describe().value.providers['auriga-managed'].models.map(
        ({ id, name, contextWindow, maxTokens }) => ({
          id,
          name,
          contextWindow,
          maxTokens,
        }),
      ),
      [...models, ...candidate],
    );
    assert.equal(
      (await ctx.credentials.resolve('PERSONAL_KEY')).value,
      'fake-personal-key',
    );
    entry = ctx.configEditor
      .entries()
      .find((row) => row.options.id === 'llm-pi-ai');
    release();
    binding.close();
    await ctx.configEditor.edit(entry, (current) => {
      const next = globalThis.structuredClone(current);
      delete next.providers['auriga-managed'];
      return next;
    });
    const next = createManagedModels({ loadMaterial });
    const oldRelease = release;
    binding = next;
    release = next.attach(
      ctx.llm,
      ctx.settings,
      ctx.configEditor,
      ctx.credentials,
    );
    await next.ready;
    oldRelease();
    assert.deepEqual(
      describe().value.providers['auriga-managed'].models.map(
        ({ id, contextWindow, maxTokens }) => ({
          id,
          contextWindow,
          maxTokens,
        }),
      ),
      material.models.map((model) => ({
        id: model.id,
        contextWindow: model.contextWindow,
        maxTokens: model.maxOutputTokens,
      })),
    );
    await ctx.llm.remoteDiscoverModels('llm-pi-ai', {
      provider: 'auriga-managed',
    });
    console.log(
      JSON.stringify({
        nativeSave: true,
        persistedReload: true,
        legacyMigrated: true,
        capacityAndPreferences: true,
        manualSameIDCapacitySync: true,
        managedAndPersonalWire: true,
        reservedReferenceIsolated: true,
        capturedReferenceRacesRejected: true,
        loopRequestIdentityPreserved: true,
        crossOriginManagedCredentialCount: requests.filter(
          (row) =>
            row.origin === personalOrigin &&
            row.authorization === 'Bearer ' + material.credential,
        ).length,
        noSecretInSettingsOrStore: true,
        redirectRefused: true,
      }),
    );
  } finally {
    release?.();
    binding?.close();
    await ctx?.fiber.dispose();
    process.getBuiltinModule = builtin;
    await new Promise((done) => upstream.close(done));
    await new Promise((done) => personalUpstream.close(done));
  }
}
