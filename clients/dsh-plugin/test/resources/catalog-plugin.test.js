import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { resourceHost } from '../../src/resources/host.js';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';

function setup(t, extra = {}, config = {}) {
  const f = catalogFixture(t, undefined, { kind: 'plugin', ...config });
  let actual;
  const calls = [];
  const pluginHost = {
    observe: async () => actual,
    install: async (file, name) => {
      assert.deepEqual(readFileSync(file), f.archive);
      assert.equal(name, f.manifest.plugin.packageName);
      calls.push('install');
      actual = { name, installed: true, enabled: false, version: '1.0.0' };
      return { restartRequired: false };
    },
    enable: async (_name, enabled) => {
      calls.push(enabled ? 'enable' : 'disable');
      actual = { ...actual, enabled, active: enabled };
      return { restartRequired: false };
    },
    remove: async () => {
      calls.push('remove');
      actual = undefined;
      return { restartRequired: false };
    },
    ...extra,
  };
  const service = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost,
  });
  const input = {
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'plugin-install',
  };
  return { ...f, service, input, calls, pluginHost };
}

test('unified plugin waits for guardian, installs tgz by npm name and reports only observed host application', async (t) => {
  const f = setup(t);
  const accepted = await f.service.apply(f.input);
  assert.equal(accepted.accepted, true);
  assert.deepEqual(f.calls, []);
  assert.equal(f.getRow().state, 'applying');
  f.allow();
  const result = await f.service.list();
  assert.deepEqual(f.calls, ['install', 'enable']);
  assert.equal(result.items[0].state, 'applied');
  const ledgerBefore = readFileSync(f.directory + '/ledger.json', 'utf8');
  await f.service.list();
  assert.equal(
    readFileSync(f.directory + '/ledger.json', 'utf8'),
    ledgerBefore,
  );
  assert.deepEqual(readdirSync(f.active), []);
  const reports = f.requests
    .filter((r) => r.path.endsWith('installation-reports'))
    .map((r) => JSON.parse(r.options.body));
  assert.equal(reports.at(-1).identity.kind, 'plugin');
  assert.equal(reports.at(-1).selectionRevision, 7);
  assert.equal(reports.at(-1).observedGeneration, 9);
  assert.ok(
    f.requests.every((r) => r.path.startsWith('/api/resource/v1/catalog/')),
  );
  await f.service.apply({
    ...f.input,
    action: 'remove',
    revision: 1,
    key: 'plugin-remove',
  });
  assert.deepEqual(f.calls, ['install', 'enable', 'disable', 'remove']);
  assert.equal(f.getRow().state, 'removed');
});

test('host restart intent or wrong observation never reports unified plugin applied', async (t) => {
  for (const extra of [
    { install: async () => ({ restartRequired: true }) },
    {
      observe: async () => ({
        installed: true,
        enabled: true,
        version: '9.0.0',
      }),
    },
  ]) {
    const f = setup(t, extra);
    await f.service.apply(f.input);
    f.allow();
    await f.service.list();
    assert.notEqual(f.getRow().state, 'applied');
  }
});

test('required prevents member disable/remove locally before selecting a new intent', async (t) => {
  const f = setup(t);
  f.resource.installationMode = 'required';
  for (const action of ['disable', 'remove'])
    await assert.rejects(f.service.apply({ ...f.input, action, revision: 1 }), {
      code: 'resource.installation_required',
    });
  assert.deepEqual(f.calls, []);
  assert.equal(
    f.requests.some((r) => r.options.method === 'POST'),
    false,
  );
});

test('schema bridge remains the generated authoritative snapshot', async () => {
  const bridge = await import('../../src/resources/schema-data.js');
  const generated = await import('../../src/generated/resource-schema-data.js');
  assert.equal(bridge.default, generated.default);
});

test('automatic default installation and personal policy use the authoritative member list', async (t) => {
  const policies = [];
  const f = setup(
    t,
    {
      enforcePersonalPolicy: async (allowed, names) => {
        policies.push({ allowed, names });
        return { blocked: ['personal-plugin'], restartRequired: false };
      },
    },
    { allowPersonalPlugins: false },
  );
  f.resource.installationMode = 'default';
  await f.service.list();
  f.allow();
  const value = await f.service.list();
  assert.equal(value.items[0].state, 'applied');
  assert.equal(value.allowPersonalPlugins, false);
  assert.deepEqual(value.blockedPersonalPlugins, ['personal-plugin']);
  assert.deepEqual(policies.at(-1), {
    allowed: false,
    names: ['@example/catalog-plugin'],
  });
  assert.equal(
    f.requests.some(
      (r) => r.path.endsWith('/installations') && r.options.method === 'POST',
    ),
    false,
  );
  await f.service.apply({
    ...f.input,
    action: 'remove',
    revision: 1,
    key: 'default-remove',
  });
  assert.equal(f.getRow().state, 'removed');
});

test('a new server generation arriving during plugin installation prevents activation and stale applied reports', async (t) => {
  const f = setup(t);
  const install = f.pluginHost.install;
  f.pluginHost.install = async (...args) => {
    const result = await install(...args);
    const row = f.getRow();
    f.setRow({ ...row, generation: row.generation + 1 });
    return result;
  };
  await f.service.apply(f.input);
  f.allow();
  await assert.rejects(f.service.list(), { code: 'common.conflict' });
  assert.equal(f.calls.includes('enable'), false);
  assert.equal(f.getRow().state, 'applying');
});

test('native personal policy disables non-catalog installed bundles through the public manager, preserving L0', async () => {
  const changes = [];
  const host = resourceHost({
    get: () => ({
      listBundles: async () => [
        { name: 'auriga-dsh', installed: true, enabled: true },
        { name: 'core', installed: false, enabled: true },
        { name: 'catalog-plugin', installed: true, enabled: true },
        { name: 'personal-plugin', installed: true, enabled: true },
      ],
      installBundle() {},
      removeBundle() {},
      setBundleEnabled: async (name, enabled) => {
        changes.push([name, enabled]);
        return { target: name, application: 'applied' };
      },
    }),
  });
  assert.deepEqual(await host.enforcePersonalPolicy(true, []), {
    blocked: [],
    restartRequired: false,
  });
  assert.deepEqual(
    await host.enforcePersonalPolicy(false, ['catalog-plugin']),
    { blocked: ['personal-plugin'], restartRequired: false },
  );
  assert.deepEqual(changes, [['personal-plugin', false]]);
});
