import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  readFileSync,
  readdirSync,
  writeFileSync,
  existsSync,
  unlinkSync,
} from 'node:fs';
import { join } from 'node:path';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
import { nativePluginPolicy } from '../../src/resources/native-policy.js';
import { verifyRevocations } from '../../src/resources/trust.js';

test('real revocation fixture signs the unsigned body, not a prior signature', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const roots = await f.roots();
  const checked = verifyRevocations(
    f.revoke(),
    f.who.tenantId,
    roots.revocationKeys,
    Date.now(),
  );
  assert.equal(
    checked.list.revokedArtifacts[0].digest,
    f.resource.identity.manifestDigest,
  );
});

test('authenticated policy persists across service restart and is enforced before an offline refresh', async (t) => {
  const f = catalogFixture(t, undefined, { allowPersonalPlugins: false });
  const calls = [];
  const host = {
    enforcePersonalPolicy: async (allowed, names) => {
      calls.push({ allowed, names });
      return { blocked: ['personal'], restartRequired: false };
    },
  };
  const options = { ...f, privateSkills: {}, host: {}, pluginHost: host };
  await catalogService(options).list();
  assert.equal(calls.at(-1).allowed, false);
  calls.length = 0;
  const offline = async () => {
    calls.push('refresh');
    throw Object.assign(new Error('offline'), { code: 'common.unavailable' });
  };
  const offlineState = await catalogService({
    ...options,
    fetchImpl: offline,
  }).list();
  assert.equal(offlineState.offline, true);
  assert.equal(offlineState.allowPersonalPlugins, false);
  assert.deepEqual(offlineState.blockedPersonalPlugins, ['personal']);
  assert.deepEqual(calls[0], { allowed: false, names: [] });
  assert.equal(calls[1], 'refresh');
  const store = nativePluginPolicy({
    home: f.home,
    who: f.who,
    origin: f.runtime.config.platformOrigin,
  });
  assert.equal(store.read(), false);
  for (const who of [
    { ...f.who, ownerId: '019a0000-0000-7000-8000-000000000099' },
    { ...f.who, hostId: '019a0000-0000-7000-8000-000000000099' },
  ])
    assert.equal(
      nativePluginPolicy({
        home: f.home,
        who,
        origin: f.runtime.config.platformOrigin,
      }).read(),
      true,
    );
  assert.equal(
    nativePluginPolicy({
      home: f.home,
      who: f.who,
      origin: 'https://different.example.test',
    }).read(),
    true,
  );
});

test('missing policy defaults true, but corrupt persisted policy denies until authenticated replacement', (t) => {
  const f = catalogFixture(t);
  const options = {
    home: f.home,
    who: f.who,
    origin: f.runtime.config.platformOrigin,
  };
  const initial = nativePluginPolicy(options);
  assert.equal(initial.read(), true);
  initial.write(false);
  const base = join(f.home, 'auriga/native-plugin-policy');
  const platform = readdirSync(base)[0];
  const file = join(
    base,
    platform,
    f.who.tenantId,
    f.who.ownerId,
    f.who.hostId + '.json',
  );
  const before = readFileSync(file, 'utf8');
  initial.write(false);
  assert.equal(readFileSync(file, 'utf8'), before);
  writeFileSync(file, '{invalid');
  const damaged = nativePluginPolicy(options);
  assert.equal(damaged.read(), false);
  damaged.write(true);
  assert.equal(nativePluginPolicy(options).read(), true);
});

test('waiting for the exact guardian permit does not repeatedly enter the C writer lock and a new generation invalidates the wait', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const service = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: {},
  });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'wait',
  });
  f.desktop.finish = () =>
    assert.fail(
      'an unchanged waiting snapshot must leave the writer lock for E',
    );
  f.desktop.read = () =>
    assert.fail('waiting status must not acquire a C lock');
  assert.equal((await service.list()).items[0].state, 'applying');
  assert.equal(
    f.desktop.awaitingGuardian(f.who, [
      { ...f.getRow(), generation: f.getRow().generation + 1 },
    ]),
    false,
  );
});

test('a previously applied remote report cannot claim a fresh local installation before the guardian permit', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  f.setRow({ ...f.getRow(), state: 'applied' });
  const service = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: {},
  });
  assert.equal((await service.list()).items[0].state, 'applying');
  assert.equal((await service.list()).items[0].state, 'applying');
});

test('all ledger plugin names remain exempt across disabled removed revoked expired and failed states', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const service = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: {},
  });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'policy-names',
  });
  const file = join(f.directory, 'ledger.json');
  const ledger = JSON.parse(readFileSync(file, 'utf8'));
  for (const state of [
    'applying',
    'applied',
    'pending_restart',
    'disabled',
    'removed',
    'revoked',
    'expired',
    'failed',
  ]) {
    ledger.installations[0].state = state;
    writeFileSync(file, JSON.stringify(ledger));
    assert.deepEqual(
      f.desktop.policyNames(f.who),
      [f.manifest.plugin.packageName],
      state,
    );
  }
});

test('corrupt catalog manifest cache stops policy reconciliation before any host mutation or applied report', async (t) => {
  const f = catalogFixture(t, undefined, {
    kind: 'plugin',
    allowPersonalPlugins: false,
  });
  let mutations = 0;
  const service = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: {
      enforcePersonalPolicy: async () => {
        mutations++;
        return { blocked: [], restartRequired: false };
      },
      install: async () => {
        mutations++;
      },
      enable: async () => {
        mutations++;
      },
    },
  });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'corrupt-policy',
  });
  f.allow();
  mutations = 0;
  writeFileSync(
    join(
      f.directory,
      'cache',
      f.resource.identity.manifestDigest.slice(7),
      'bytes',
    ),
    'broken',
  );
  await assert.rejects(service.list(), {
    code: 'resource.storage_unavailable',
  });
  assert.equal(mutations, 0);
  assert.equal(
    f.requests.some(
      (r) =>
        r.path.endsWith('/installation-reports') &&
        JSON.parse(r.options.body).state === 'applied',
    ),
    false,
  );
});

test('guardian preparation times out durably reports failed and rejects a late permit', async (t) => {
  const f = catalogFixture(t, undefined, { kind: 'plugin' });
  const service = catalogService({
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: {},
  });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'wait-timeout',
  });
  const directory = join(f.directory, 'transactions');
  for (const name of readdirSync(directory)) {
    const path = join(directory, name),
      tx = JSON.parse(readFileSync(path, 'utf8'));
    if (tx.phase === 'prepared') {
      tx.recordedAt = new Date(Date.now() - 301000).toISOString();
      writeFileSync(path, JSON.stringify(tx));
    }
  }
  assert.equal((await service.list()).items[0].state, 'failed');
  assert.equal(f.getRow().state, 'failed');
  assert.equal(f.desktop.read(f.who).installations[0].state, 'failed');
  f.allow();
  // Direct C finish must also refuse this snapshot after a service restart.
  f.desktop.finish(f.who, () => true);
  assert.equal(existsSync(f.active), false);
});

test('a missing guardian does not turn persisted catalog plugins into personal plugins', async (t) => {
  const f = catalogFixture(t, undefined, {
    kind: 'plugin',
    allowPersonalPlugins: false,
  });
  const names = [];
  const options = {
    ...f,
    privateSkills: {},
    host: {},
    pluginHost: {
      enforcePersonalPolicy: async (_allowed, value) => {
        names.push(value);
        return { blocked: [], restartRequired: false };
      },
    },
  };
  await catalogService(options).apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'no-guardian',
  });
  unlinkSync(join(f.home, 'auriga/guardian/state.json'));
  const result = await catalogService({
    ...options,
    desktop: undefined,
  }).list();
  assert.equal(result.sharedAvailable, false);
  assert.deepEqual(names.at(-1), [f.manifest.plugin.packageName]);
});
