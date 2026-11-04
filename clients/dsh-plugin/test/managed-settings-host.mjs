// Real public Host integration, run in the built runtime with a disposable profile:
// node /test-plugin/test/managed-settings-host.mjs /opt/dsh
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { installManagedSettingsPolicy } from '../src/managed/settings-policy.js';

const require = createRequire(join(process.argv[2], 'package.json'));
const load = (name) => import(pathToFileURL(require.resolve(name)).href);
const [
  { boot, initProfile, readProfilePatches },
  { default: Editor },
  { default: Settings },
  { default: Credentials },
  { default: Llm },
  Pi,
  Preferences,
  { default: SettingsController, CredentialsController },
] = await Promise.all([
  load('@deepseek-ai/dsh-app-boot'),
  load('@deepseek-ai/dsh-config-editor'),
  load('@deepseek-ai/dsh-settings'),
  load('@deepseek-ai/dsh-credentials-local'),
  load('@deepseek-ai/dsh-llm'),
  load('@deepseek-ai/dsh-llm-pi-ai'),
  load('@deepseek-ai/dsh-client-ui-settings'),
  load('@deepseek-ai/dsh-api-settings-controller'),
]);
const home = mkdtempSync(join(tmpdir(), 'auriga-settings-policy-'));
const dir = join(home, 'profile');
initProfile(dir, ['policy-probe']);
const bundle = join(dir, 'node_modules', 'policy-probe');
mkdirSync(bundle, { recursive: true });
writeFileSync(join(home, 'package.json'), '{"name":"settings-policy-probe"}');
writeFileSync(
  join(bundle, 'package.json'),
  JSON.stringify({
    name: 'policy-probe',
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
        {
          id: 'credentials',
          name: 'cordis:credentials',
          config: { dshHome: home, watch: false },
        },
        { id: 'llm', name: 'cordis:llm' },
        {
          id: 'llm-pi-ai',
          name: 'cordis:pi',
          config: {
            providers: {
              'auriga-managed': {
                api: 'openai-completions',
                baseURL: 'https://example.com/v1',
                models: [{ id: 'fixture-model' }],
                headers: { Authorization: 'Bearer test-only' },
              },
            },
          },
        },
        { id: 'ui-settings', name: 'cordis:preferences' },
        { id: 'policy', name: 'cordis:policy' },
      ],
    },
  ]),
);
writeFileSync(join(dir, 'cordis.yml'), '[]');
const profile = {
  name: 'managed',
  startedBundles: ['policy-probe'],
  dir,
  patchPath: join(dir, 'cordis.patch.yml'),
  installAnchor: join(home, 'package.json'),
  cwd: home,
  home,
  overlays: [],
  telemetryDisabledEnv: '1',
};
let ctx;
try {
  ctx = await boot(
    'settings-policy-probe',
    join(dir, 'cordis.yml'),
    readProfilePatches('settings-policy-probe', profile),
    (host) => {
      host.provide('profileContext', profile);
      Object.assign(host.loader.builtins, {
        editor: Editor,
        settings: Settings,
        credentials: Credentials,
        llm: Llm,
        pi: Pi,
        preferences: Preferences,
        policy: { apply: installManagedSettingsPolicy },
      });
    },
  );
  const settings = new SettingsController(ctx);
  const credentials = new CredentialsController(ctx);
  const before = settings
    .describe()
    .namespaces.find((row) => row.ns === 'llm-pi-ai');
  assert.ok(before);
  await settings.mutate('ui-settings', [
    { op: 'set', path: ['enabled'], value: true },
  ]);
  assert.equal(
    settings.describe().namespaces.find((row) => row.ns === 'ui-settings').value
      .enabled,
    true,
  );
  await settings.update('llm-pi-ai', {
    providers: {
      personal: {
        api: 'openai-completions',
        baseURL: 'https://example.com/personal',
        models: [{ id: 'personal-model' }],
      },
    },
  });
  assert.equal(
    settings.describe().namespaces.find((row) => row.ns === 'llm-pi-ai').value
      .providers.personal.baseURL,
    'https://example.com/personal',
  );
  await credentials.set('PERSONAL_API_KEY', 'test-only');
  await credentials.unset('PERSONAL_API_KEY');
  for (const write of [
    () =>
      settings.mutate('llm-pi-ai', [
        {
          op: 'set',
          path: ['providers', 'auriga-managed', 'baseURL'],
          value: 'https://example.com/other',
        },
      ]),
    () => settings.update('llm-pi-ai', { providers: { 'auriga-managed': {} } }),
    () => settings.replace('llm-pi-ai', {}),
    () => settings.mutate('llm-pi-ai', [{ op: 'unset', path: [] }]),
    () => settings.update('auriga-dsh', {}),
  ])
    await assert.rejects(
      write(),
      (error) =>
        error.code === 'settings/rejected' && /Auriga/.test(error.message),
    );
  await assert.rejects(
    credentials.set('AURIGA_MODEL_CREDENTIAL', 'test-only'),
    (error) =>
      error.code === 'credential/rejected' && /Auriga/.test(error.message),
  );
  const after = settings
    .describe()
    .namespaces.find((row) => row.ns === 'llm-pi-ai');
  assert.deepEqual(
    after.value.providers['auriga-managed'],
    before.value.providers['auriga-managed'],
  );
  console.log(
    JSON.stringify({
      status: 'passed',
      realHostControllers: true,
      personalPreference: true,
      personalProvider: true,
      managedProviderUnchanged: true,
      platformCredentialsRejected: true,
    }),
  );
} finally {
  await ctx?.fiber.dispose();
}
