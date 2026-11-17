import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resourceFixture, id } from './resource-fixture.mjs';
import { tempBase } from './helpers.mjs';
import { Supervisor } from '../runtime/supervisor.mjs';
import { resourcePluginControl } from '../runtime/resource-plugins.mjs';
import { durableJson } from '../runtime/host-lock.mjs';

test('plugin payload is verified independently without a skill scan and revocation removes it after stopping the host', async (t) => {
  const f = resourceFixture(tempBase(t), Date.now(), 3600000, true);
  const events = [];
  let stopped = true;
  const profileFile = join(f.home, 'profiles/auriga/package.json');
  mkdirSync(join(profileFile, '..'), { recursive: true });
  const profile = (installed) =>
    writeFileSync(
      profileFile,
      JSON.stringify({
        dependencies: installed
          ? { '@example/catalog-plugin': 'file:verified.tgz' }
          : {},
      }),
    );
  profile(false);
  const control = resourcePluginControl({
    home: f.home,
    trust: f.trust,
    profileFile,
    remove: async (names) => {
      assert.equal(stopped, true);
      assert.deepEqual(names, ['@example/catalog-plugin']);
      events.push('remove');
      profile(false);
    },
  });
  const supervisor = new Supervisor(
    f.home,
    {
      record: { launcher: null, root: null, children: [] },
      isStopped: async () => stopped,
      stopTree: async () => {
        events.push('stop');
        stopped = true;
      },
    },
    f.trust,
    { ...control, time: () => ({ bootId: id(9), monotonicMillis: 1000 }) },
  );
  await supervisor.prepare();
  assert.equal(supervisor.guardian.read().guardian.state, 'active');
  assert.equal(supervisor.marks().entries[0].releaseSequence, 7);
  profile(true);
  stopped = false;
  f.cache(
    JSON.stringify(
      f.revocations(2, [
        {
          packageId: id(2),
          versionId: id(4),
          digest: f.ref.identity.manifestDigest,
        },
      ]),
    ),
  );
  const result = await supervisor.reconcile();
  assert.equal(result.privateOnly, true);
  assert.deepEqual(events, ['stop', 'remove']);
  assert.equal(supervisor.guardian.read().guardian.reason, 'revoked');
  assert.equal(control.pluginsCleared(), true);
});

test('failed public plugin removal blocks private-only restart', async (t) => {
  const f = resourceFixture(tempBase(t), Date.now(), 3600000, true);
  let installed = false;
  const supervisor = new Supervisor(
    f.home,
    {
      record: { launcher: null, root: null, children: [] },
      isStopped: async () => true,
      stopTree: async () => {},
    },
    f.trust,
    {
      pluginsCleared: () => !installed,
      clearPlugins: async () => {
        throw new Error('public removal failed');
      },
      time: () => ({ bootId: id(9), monotonicMillis: 1000 }),
    },
  );
  await supervisor.prepare();
  installed = true;
  await assert.rejects(
    supervisor.guardian.disable('revoked'),
    /public removal failed/,
  );
  assert.equal(supervisor.guardian.read().guardian.state, 'blocked');
  assert.equal(supervisor.guardian.cleared(), false);
});

test('tampered unsigned cleanup metadata cannot select a public plugin uninstall target', async (t) => {
  const f = resourceFixture(tempBase(t), Date.now(), 3600000, true);
  const profileFile = join(f.home, 'profiles/auriga/package.json');
  mkdirSync(join(profileFile, '..'), { recursive: true });
  writeFileSync(
    profileFile,
    JSON.stringify({
      dependencies: { '@example/catalog-plugin': 'file:verified.tgz' },
    }),
  );
  f.ref.envelope.signature.value = 'A'.repeat(86);
  durableJson(join(f.directory, 'ledger.json'), f.ledger);
  let removed = false;
  const control = resourcePluginControl({
    home: f.home,
    profileFile,
    trust: f.trust,
    remove: async () => {
      removed = true;
    },
  });
  assert.throws(() => control.pluginsCleared(), /plugin manifest binding/);
  await assert.rejects(control.clearPlugins(), /plugin manifest binding/);
  assert.equal(removed, false);
});
