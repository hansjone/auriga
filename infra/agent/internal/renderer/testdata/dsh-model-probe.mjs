// Module composition only: real locked DSH Loader/settings/credentials/pi-ai,
// disposable profile and random-port fake OpenAI upstream. No platform or user profile.
import assert from 'node:assert/strict';
import { classifyModelExposure } from './model-exposure.mjs';
import {
  readFileSync,
  writeFileSync,
  mkdirSync,
  openSync,
  closeSync,
  unlinkSync,
} from 'node:fs';
import { randomBytes } from 'node:crypto';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [runtimeRoot, patchFile, metaRoot, scratch, requestFixture] = process.argv
  .slice(2)
  .filter((arg) => arg !== '--read-only-model');
const readOnlyModel = process.argv.includes('--read-only-model');
const req = createRequire(
  pathToFileURL(join(resolve(runtimeRoot), 'package.json')),
);
const load = (name) => import(pathToFileURL(req.resolve(name)).href);
const version = JSON.parse(
  readFileSync(req.resolve('@deepseek-ai/dsh/package.json'), 'utf8'),
).version;
assert.equal(version, '0.2.0-rc.2');
const [
  { boot, initProfile, readProfilePatches, loadOverlayPatches },
  { default: Editor },
  { default: Settings },
  { default: Credentials },
  { default: Llm },
  Pi,
  { default: SettingsController, CredentialsController },
  { credentialRef, credentialKey },
  yaml,
] = await Promise.all([
  load('@deepseek-ai/dsh-app-boot'),
  load('@deepseek-ai/dsh-config-editor'),
  load('@deepseek-ai/dsh-settings'),
  load('@deepseek-ai/dsh-credentials-local'),
  load('@deepseek-ai/dsh-llm'),
  load('@deepseek-ai/dsh-llm-pi-ai'),
  load('@deepseek-ai/dsh-api-settings-controller'),
  load('@deepseek-ai/dsh-credentials'),
  load('js-yaml'),
]);
const patch = loadOverlayPatches('model-probe', patchFile);
assert.ok(
  !patch.some((row) => row.id === 'credentials'),
  'managed overlay must leave the user credential store intact',
);
const rows = patch.filter((row) => row.id === 'llm-pi-ai');
assert.equal(rows.length, 1);
const keyFile = join(metaRoot, 'model-credential.json');
const key = JSON.parse(readFileSync(keyFile, 'utf8')).credential;
const exposures = [];
const acceptedExposures = [];
const inspectExposure = (value, surface, label) => {
  const result = classifyModelExposure(value, surface);
  exposures.push(...result.failures.map((path) => label + ':' + path));
  acceptedExposures.push(...result.warnings.map((path) => label + ':' + path));
};
if (readOnlyModel) {
  const cannotWrite = (file, flags) => {
    assert.throws(
      () => {
        const fd = openSync(file, flags);
        closeSync(fd);
        if (flags === 'wx') unlinkSync(file);
      },
      (error) => ['EROFS', 'EACCES', 'EPERM'].includes(error.code),
    );
  };
  cannotWrite(keyFile, 'r+');
  cannotWrite(join(metaRoot, 'write-probe'), 'wx');
}
const requests = [];
const upstream = createServer(async (request, response) => {
  try {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    assert.ok(
      request.headers.authorization === 'Bearer ' + key,
      'native file expression must supply Authorization',
    );
    requests.push(JSON.parse(Buffer.concat(chunks)));
    response.writeHead(200, { 'Content-Type': 'text/event-stream' });
    response.end(
      'data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}\n\ndata: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}\n\ndata: [DONE]\n\n',
    );
  } catch {
    response.writeHead(500);
    response.end();
  }
});
await new Promise((resolve) => upstream.listen(0, '127.0.0.1', resolve));
try {
  for (const mode of ['managed', 'without-compat', 'missing-adapter']) {
    const compatEnabled = mode !== 'without-compat';
    const missingAdapter = mode === 'missing-adapter';
    const home = join(scratch, mode);
    const dir = join(home, 'profiles', 'managed');
    initProfile(dir, ['model-probe-bundle']);
    const userKey = randomBytes(32).toString('base64url');
    const addedKey = randomBytes(32).toString('base64url');
    const grant = {
      kind: 'grant',
      payload: {
        access: randomBytes(16).toString('hex'),
        refresh: randomBytes(16).toString('hex'),
        expires: 1,
      },
    };
    const userStore = join(home, '.credentials.yaml');
    writeFileSync(
      userStore,
      JSON.stringify({
        version: 1,
        refs: { USER_EXISTING_KEY: userKey },
        records: { 'llm-pi-ai/openai-codex': grant },
      }),
      { mode: 0o600 },
    );
    const bundle = join(dir, 'node_modules', 'model-probe-bundle');
    mkdirSync(bundle, { recursive: true });
    writeFileSync(
      join(home, 'package.json'),
      '{"name":"isolated-model-probe"}\n',
    );
    writeFileSync(
      join(bundle, 'package.json'),
      JSON.stringify({
        name: 'model-probe-bundle',
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
            ...(missingAdapter ? [] : [{ id: 'llm-pi-ai', name: 'cordis:pi' }]),
          ],
        },
      ]),
    );
    writeFileSync(join(dir, 'cordis.yml'), '[]\n');
    const overlay = structuredClone(rows);
    const provider = overlay.find((row) => row.id === 'llm-pi-ai').config
      .providers['auriga-managed'];
    provider.baseURL = `http://127.0.0.1:${upstream.address().port}/v1`;
    if (!compatEnabled) delete provider.compat;
    const authorization = provider.headers.Authorization;
    assert.equal(typeof authorization.__jsExpr, 'string');
    authorization.__jsExpr = authorization.__jsExpr.replace(
      '/run/auriga/model-credential.json',
      keyFile.replaceAll('\\', '/'),
    );
    const overlayPath = join(home, 'managed.patch.json');
    writeFileSync(overlayPath, JSON.stringify(overlay));
    const profile = {
      name: 'managed',
      startedBundles: ['model-probe-bundle'],
      dir,
      patchPath: join(dir, 'cordis.patch.yml'),
      installAnchor: join(home, 'package.json'),
      cwd: home,
      home,
      overlays: loadOverlayPatches('model-probe', overlayPath),
      telemetryDisabledEnv: '1',
    };
    let ctx;
    try {
      ctx = await boot(
        'model-probe',
        join(dir, 'cordis.yml'),
        readProfilePatches('model-probe', profile),
        (host) => {
          host.provide('profileContext', profile);
          host.provide('appReady', {
            onReady: (listener) => {
              listener();
              return () => {};
            },
          });
          Object.assign(host.loader.builtins, {
            editor: Editor,
            settings: Settings,
            llm: Llm,
            credentials: Credentials,
            pi: Pi,
          });
        },
      );
      const controller = new SettingsController(ctx);
      const credentialsController = new CredentialsController(ctx);
      assert.ok(
        (await ctx.credentials.resolve(credentialRef('USER_EXISTING_KEY')))
          ?.value === userKey,
        'existing personal key must remain readable',
      );
      await credentialsController.set('USER_ADDED_KEY', addedKey);
      assert.ok(
        (await ctx.credentials.resolve(credentialRef('USER_ADDED_KEY')))
          ?.value === addedKey,
        'credentials/set must succeed with read-only model material',
      );
      const recordKey = credentialKey('llm-pi-ai', 'openai-codex');
      assert.deepEqual(await ctx.credentials.readRecord(recordKey), grant);
      const refreshed = {
        ...grant,
        payload: {
          ...grant.payload,
          access: randomBytes(16).toString('hex'),
          expires: 2,
        },
      };
      await ctx.credentials.modifyRecord(recordKey, async (current) => {
        assert.deepEqual(current, grant);
        return refreshed;
      });
      assert.deepEqual(await ctx.credentials.readRecord(recordKey), refreshed);
      const persisted = yaml.load(readFileSync(userStore, 'utf8'));
      assert.ok(
        persisted.refs.USER_EXISTING_KEY === userKey &&
          persisted.refs.USER_ADDED_KEY === addedKey,
        'personal keys must persist in the original writable store',
      );
      assert.deepEqual(persisted.records['llm-pi-ai/openai-codex'], refreshed);
      assert.ok(
        !readFileSync(userStore, 'utf8').includes('amc.'),
        'managed key must not enter the personal credential store',
      );
      const described = controller.describe();
      inspectExposure(described, 'describe', mode + ':settings/describe');
      const ns = described.namespaces.find((row) => row.ns === 'llm-pi-ai');
      if (missingAdapter) {
        assert.equal(
          ns,
          undefined,
          'unsupported profile must not fabricate an adapter',
        );
        assert.ok(!ctx.llm.listProviders().includes('auriga-managed'));
        const before = requests.length;
        let failure;
        for await (const chunk of ctx.llm.stream({
          provider: 'auriga-managed',
          model: 'pool-a',
          messages: [],
        })) {
          if (chunk.type === 'finish') failure = chunk.reason?.failure?.code;
        }
        assert.equal(failure, 'NO_ADAPTER');
        assert.equal(requests.length, before);
        continue;
      }
      assert.ok(ns, 'native model settings namespace must exist');
      try {
        const saved = await controller.mutate(
          ns.ns,
          [
            {
              op: 'set',
              path: ['providers', 'auriga-managed', 'displayName'],
              value: 'Managed model',
            },
          ],
          ns.revision,
        );
        inspectExposure(saved, 'mutate', mode + ':settings/mutate');
      } catch (error) {
        assert.equal(error.code, 'settings/rejected');
        assert.ok(
          !String(error).includes('amc.'),
          'mutation rejection must not expose model key',
        );
      }
      inspectExposure(
        controller.describe(),
        'describe',
        mode + ':settings/describe-after-mutate',
      );
      for (const file of ['cordis.yml', 'cordis.patch.yml']) {
        assert.ok(
          !readFileSync(join(dir, file), 'utf8').includes('amc.'),
          'profile must contain no model key',
        );
      }
      const before = requests.length;
      let finished = false;
      let lastChunk;
      for await (const chunk of ctx.llm.stream({
        provider: 'auriga-managed',
        model: 'pool-a',
        maxTokens: 64,
        messages: [
          { role: 'user', content: [{ type: 'text', text: 'Hello' }] },
        ],
      })) {
        if (chunk.error)
          throw new Error(
            String(chunk.error.message ?? chunk.error).replace(
              /amc\.[A-Za-z0-9_.-]+/g,
              '[redacted]',
            ),
          );
        lastChunk = chunk;
        if (chunk.type === 'finish') finished = true;
      }
      assert.equal(
        requests.length,
        before + 1,
        'native pi-ai must issue one HTTP request: ' +
          JSON.stringify(lastChunk).replaceAll(key, '[redacted]'),
      );
      assert.ok(finished, 'native stream must finish');
      const actual = requests.at(-1);
      if (compatEnabled) assert.equal(Object.hasOwn(actual, 'store'), false);
      else
        assert.equal(
          actual.store,
          false,
          'control must reproduce the missing-compat store field',
        );
    } catch (error) {
      if (!missingAdapter) throw error;
      assert.ok(!String(error).includes('amc.'));
      assert.match(
        String(error),
        /llm-pi-ai|patch|target/i,
        'unsupported profile must diagnose the missing adapter',
      );
    } finally {
      await ctx?.fiber.dispose();
    }
  }
  if (requestFixture) {
    writeFileSync(requestFixture, JSON.stringify(requests[0]), { mode: 0o600 });
    // Preserve the actual rendered input (synthetic test key only) for an
    // independent run with this directory bind-mounted read-only at /run/auriga.
    const fixtureDir = requestFixture + '.readonly';
    mkdirSync(fixtureDir, { recursive: true, mode: 0o700 });
    writeFileSync(
      join(fixtureDir, 'model-credential.json'),
      readFileSync(keyFile),
      { mode: 0o600 },
    );
    writeFileSync(join(fixtureDir, 'cordis.patch.json'), JSON.stringify(rows), {
      mode: 0o600,
    });
  }
  console.log(
    JSON.stringify({
      dshVersion: version,
      requests: requests.length,
      noUnexpectedPlaintextInSettings: exposures.length === 0,
      acceptedDescribeWarnings: acceptedExposures.length,
      noPlaintextInProfile: true,
      fileAuthorizationResolved: true,
      readOnlyModelMount: readOnlyModel,
      existingUserCredentialReadable: true,
      credentialsSetSucceeded: true,
      oauthRecordRefreshed: true,
      storeOmittedWithCompat: true,
      controlReproducesStore: true,
      missingAdapterFailsClosed: true,
    }),
  );
  if (acceptedExposures.length)
    console.warn(
      'WARNING B-MM-DESCRIBE: accepted owner-only exposure within MANAGED-N1 boundary; paths=' +
        acceptedExposures.join(','),
    );
  assert.equal(
    exposures.length,
    0,
    'model key exposure locations=' + exposures.join(','),
  );
} finally {
  await new Promise((resolve) => upstream.close(resolve));
}
