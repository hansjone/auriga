import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync } from 'node:crypto';
import { mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { resourceHost } from '../../src/resources/host.js';
import { nativePluginFailure } from '../../src/resources/diagnostics.js';
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
  return {
    ...f,
    service,
    input,
    calls,
    pluginHost,
    setActual: (value) => {
      actual = value;
    },
  };
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
  const restarted = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: f.pluginHost,
  });
  await restarted.list();
  assert.deepEqual(
    f.calls,
    ['install', 'enable'],
    'restart reuses durable byte proof',
  );
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

test('restart-required installation is reported and is not retried by polling or service reconstruction', async (t) => {
  let installs = 0;
  const f = setup(t, {
    install: async () => {
      installs++;
      return { restartRequired: true };
    },
  });
  await f.service.apply(f.input);
  f.allow();
  await f.service.list();
  assert.equal(f.getRow().state, 'pending_restart');
  await f.service.list();
  await catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: f.pluginHost,
  }).list();
  assert.equal(installs, 1);
});

test('native dependency failure survives restart and only a new selection retries', async (t) => {
  let attempts = 0;
  const f = setup(t, {
    install: async (file, name) => {
      if (++attempts === 1)
        throw nativePluginFailure(
          { packageResult: { kind: 'network', stderr: 'private detail' } },
          'install',
          'ab123456-7890-4321-8765-abcdef123456',
        );
      f.setActual({ name, installed: true, enabled: false, version: '1.0.0' });
      return { restartRequired: false };
    },
  });
  await f.service.apply(f.input);
  f.allow();
  const result = await f.service.list();
  const failure = {
    reason: 'plugin_install_network',
    trace_id: 'ab123456789043218765abcdef123456',
  };
  assert.equal(result.items[0].state, 'failed');
  assert.deepEqual(result.items[0].pluginFailure, failure);
  assert.equal(JSON.stringify(result).includes('private detail'), false);
  const restarted = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: f.pluginHost,
  });
  assert.deepEqual((await restarted.list()).items[0].pluginFailure, failure);
  assert.equal(attempts, 1);
  await restarted.apply({ ...f.input, revision: 1, key: 'retry-once' });
  f.allow();
  assert.equal((await restarted.list()).items[0].state, 'applied');
  assert.equal(attempts, 2);
});

test('desktop selection is accepted before download and a download failure can be retried', async (t) => {
  const f = setup(t);
  let downloads = 0;
  const service = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: f.pluginHost,
    fetchImpl: async (url, options) =>
      url.pathname.endsWith('/member/downloads') && ++downloads === 1
        ? Response.json({ code: 'common.unavailable' }, { status: 503 })
        : f.fetchImpl(url, options),
  });
  assert.equal(
    (await service.apply(f.input, true)).installation.state,
    'applying',
  );
  assert.equal(downloads, 0);
  const first = (await service.list()).items[0];
  assert.equal(first.state, 'failed');
  assert.equal(first.pluginFailure.reason, 'platform_unreachable');
  assert.deepEqual(
    (await service.list()).items[0].pluginFailure,
    first.pluginFailure,
  );
  assert.equal(downloads, 1);
  assert.deepEqual(f.calls, []);
  await service.apply({ ...f.input, revision: 1, key: 'retry-download' }, true);
  assert.equal(downloads, 1);
  f.allow();
  assert.equal((await service.list()).items[0].state, 'applied');
  assert.deepEqual(f.calls, ['install', 'enable']);
});

test('native disk-full remains visible and is not retried when the failure record cannot be written', async (t) => {
  let installs = 0;
  const f = setup(t, {
    install: async () => {
      installs++;
      mkdirSync(
        join(
          f.directory,
          'plugin-installations',
          f.packageId + '.json.failure',
        ),
        { recursive: true },
      );
      throw nativePluginFailure(
        { packageResult: { kind: 'disk-full' } },
        'install',
      );
    },
  });
  await f.service.apply(f.input);
  f.allow();
  const first = (await f.service.list()).items[0];
  assert.equal(first.state, 'failed');
  assert.equal(first.pluginFailure.reason, 'plugin_install_disk_full');
  assert.deepEqual(
    (await f.service.list()).items[0].pluginFailure,
    first.pluginFailure,
  );
  assert.equal(installs, 1);
});

for (const needsRestart of [false, true])
  test(`a failed update preserves another removal's actual outcome (restart=${needsRestart})`, async (t) => {
    const keys = {
      release: generateKeyPairSync('ed25519'),
      revocation: generateKeyPairSync('ed25519'),
      nowMillis: Date.now(),
    };
    const broken = catalogFixture(t, undefined, { kind: 'plugin', ...keys });
    const other = catalogFixture(t, undefined, {
      ...keys,
      kind: 'plugin',
      name: 'another-plugin',
      packageName: '@example/another-plugin',
      packageId: '019a0000-0000-7000-8000-000000000192',
      versionId: '019a0000-0000-7000-8000-000000000194',
    });
    const otherName = other.manifest.plugin.packageName;
    const brokenName = broken.manifest.plugin.packageName;
    const previous = broken.getRow();
    broken.desktop.prepare(
      broken.who,
      [previous, other.getRow()],
      [broken, other].map((item) => ({
        manifest: item.raw,
        artifacts: [item.archive],
        revocations: Buffer.from(JSON.stringify(item.revoked)),
      })),
    );
    broken.setRow({
      ...previous,
      installationRevision: 2,
      resource: { ...previous.resource, installationRevision: 2 },
    });
    const live = {
      enabled: true,
      version: '1.0.0',
      installed: true,
      active: true,
    };
    const actual = new Map([
      [brokenName, live],
      [otherName, live],
    ]);
    let removals = 0;
    let retireAttempts = 0;
    let downloadAttempts = 0;
    const service = catalogService({
      ...broken,
      privateSkills: {},
      host: {},
      pluginHost: {
        async observe(name) {
          return actual.get(name);
        },
        async enable(name, enabled) {
          assert.equal(enabled, false);
          assert.ok(actual.has(name));
          if (name === brokenName) {
            retireAttempts++;
            if (needsRestart) return { restartRequired: true };
          }
          actual.set(name, { ...actual.get(name), enabled, active: false });
          return { restartRequired: false };
        },
        async remove(name) {
          assert.equal(name, otherName);
          actual.delete(name);
          removals++;
          return { restartRequired: false };
        },
      },
      fetchImpl: async (url, options) => {
        const path = url.pathname.split('/').at(-1);
        if (path === 'installations' && options.method === 'GET')
          return Response.json({ items: [broken.getRow(), other.getRow()] });
        const body = options.body && JSON.parse(options.body);
        if (path === 'downloads') {
          downloadAttempts++;
          return Response.json({ code: 'common.unavailable' }, { status: 503 });
        }
        if (
          body?.selection?.identity.packageId === other.packageId ||
          body?.identity?.packageId === other.packageId ||
          url.pathname.includes(other.packageId)
        )
          return other.fetchImpl(url, options);
        return broken.fetchImpl(url, options);
      },
    });
    const brokenResult = await service.list();
    assert.equal(
      brokenResult.items.find(
        (row) => row.resource.identity.packageId === broken.packageId,
      ).state,
      'failed',
    );
    assert.equal(downloadAttempts, 2);
    assert.equal(
      actual.get(brokenName).enabled,
      true,
      'keep old selection until a new group is prepared',
    );
    const result = await service.apply({
      packageId: other.packageId,
      versionId: other.versionId,
      action: 'remove',
      revision: 1,
      key: 'remove-another-plugin',
    });
    if (needsRestart) {
      assert.equal(result.installation.state, 'applying');
      const pending = await service.list();
      assert.equal(pending.pendingRestart, true);
      assert.equal(
        pending.items.find(
          (row) => row.resource.identity.packageId === broken.packageId,
        ).state,
        'failed',
      );
      assert.equal(removals, 0);
      assert.equal(retireAttempts, 1);
      return;
    }
    assert.equal(result.installation.state, 'removed');
    assert.equal(other.getRow().state, 'removed');
    assert.equal(removals, 1);
    assert.equal(downloadAttempts, 2);
    assert.equal(actual.get(brokenName).enabled, false);
    assert.equal(retireAttempts, 1);
    const ledger = broken.desktop.read(broken.who);
    const retired = ledger.installations.find(
      (entry) => entry.resource.identity.packageId === broken.packageId,
    );
    assert.equal(retired.state, 'failed');
    assert.equal(retired.selectionRevision, previous.selectionRevision);
    assert.equal(retired.generation, previous.generation);
    assert.deepEqual(JSON.parse(JSON.stringify(retired.resource)), {
      ...previous.resource,
      desiredState: 'DISABLED',
      enabled: false,
    });
  });

test('all-disabled native entries never prove the bundle active', async () => {
  const host = resourceHost({
    get: () => ({
      listBundles: async () => [
        {
          name: 'disabled',
          installed: true,
          enabled: true,
          rows: [{ entryId: 'entry' }],
        },
      ],
      listPlugins: async () => [{ entryId: 'entry', enabled: false }],
      installBundle() {},
      setBundleEnabled() {},
      removeBundle() {},
    }),
  });
  assert.equal((await host.observe('disabled')).active, false);
});

test('an empty native bundle row list never proves active', async () => {
  const host = resourceHost({
    get: () => ({
      listBundles: async () => [
        { name: 'empty', installed: true, enabled: true, rows: [] },
      ],
      listPlugins: async () => [],
      installBundle() {},
      setBundleEnabled() {},
      removeBundle() {},
    }),
  });
  assert.equal((await host.observe('empty')).active, false);
});

test('restart-required disable and enable operations are reported once without reinstall loops', async (t) => {
  for (const stage of ['disable', 'enable']) {
    let attempts = 0;
    const f = setup(t, {
      enable: async () => {
        attempts++;
        return { restartRequired: true };
      },
    });
    await f.service.apply(f.input);
    if (stage === 'disable')
      f.setActual({
        name: f.manifest.plugin.packageName,
        installed: true,
        enabled: true,
        version: '0.9.0',
      });
    f.allow();
    await f.service.list();
    assert.equal(f.getRow().state, 'pending_restart', stage);
    const installs = f.calls.filter((call) => call === 'install').length;
    await f.service.list();
    await catalogService({
      ...f,
      privateSkills: {},
      host: {},
      pluginHost: f.pluginHost,
    }).list();
    assert.equal(attempts, 1, stage);
    assert.equal(
      f.calls.filter((call) => call === 'install').length,
      installs,
      stage,
    );
    assert.equal(installs, stage === 'disable' ? 0 : 1);
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
