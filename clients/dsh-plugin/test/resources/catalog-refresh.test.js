import { test } from 'node:test';
import assert from 'node:assert/strict';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
import { watchCatalogIdentity } from './broker-fixture.js';
import { writeFileSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
import { setImmediate } from 'node:timers/promises';

test('idle refresh is minute scoped and each backoff window permits one changed-state retry', async (t) => {
  const f = catalogFixture(t);
  let now = 0,
    calls = 0,
    offline = false;
  const service = catalogService({
    ...f,
    now: () => now,
    privateSkills: {},
    host: {},
    fetchImpl: async () => {
      calls++;
      if (offline)
        throw Object.assign(new Error('offline'), {
          code: 'common.unavailable',
        });
      return Response.json({ items: [] });
    },
  });
  await service.reconcile();
  assert.equal(calls, 1);
  for (now = 1000; now < 60000; now += 1000) await service.reconcile();
  assert.equal(
    calls,
    1,
    'idle clients must not request installations every second',
  );
  await service.reconcile();
  assert.equal(calls, 2);
  offline = true;
  now++;
  await assert.rejects(service.reconcile({ changed: true }));
  const failedCalls = calls;
  await assert.rejects(service.reconcile({ changed: true }));
  assert.equal(calls, failedCalls + 1, 'one state change may bypass backoff');
  for (let i = 0; i < 59; i++) {
    now += 1000;
    await service.reconcile({ changed: true });
  }
  assert.equal(
    calls,
    failedCalls + 1,
    'repeated state events cannot repeatedly bypass backoff',
  );
  now = 180001;
  await assert.rejects(service.reconcile({ changed: true }));
  now++;
  await assert.rejects(service.reconcile({ changed: true }));
  offline = false;
  now++;
  await service.list();
  const recovered = calls;
  now += 60000;
  await service.reconcile();
  assert.equal(
    calls,
    recovered + 1,
    'a successful manual refresh clears stale failure backoff',
  );
});

test('server applying without desktop or while pending restart does not cause second polling', async (t) => {
  for (const unavailable of [
    'guardian_missing',
    'storage_unavailable',
    'pending_restart',
  ]) {
    const f = catalogFixture(t);
    let now = 0;
    let desktop;
    if (unavailable === 'pending_restart')
      desktop = {
        ...f.desktop,
        awaitingGuardian: () => false,
        switchIdentity: () => {},
        finish: () => {
          throw Object.assign(new Error('occupied'), { code: 'EBUSY' });
        },
        read: () => ({ installations: [f.getRow()] }),
      };
    else unlinkSync(join(f.home, 'auriga/guardian/state.json'));
    const options = {
      ...f,
      desktop,
      now: () => now,
      privateSkills: {},
      host: {},
    };
    if (unavailable === 'storage_unavailable')
      Object.defineProperty(options, 'desktop', {
        get() {
          throw new Error('storage unavailable');
        },
      });
    const service = catalogService(options);
    await service.reconcile();
    const before = f.requests.length;
    for (now = 1000; now < 60000; now += 1000) await service.reconcile();
    assert.equal(f.requests.length, before, unavailable);
    await service.reconcile();
    assert.ok(
      f.requests.length > before,
      unavailable + ' still refreshes by the minute',
    );
  }
});

test('applying snapshots follow once per second without acquiring C writer locks', async (t) => {
  const f = catalogFixture(t);
  let now = 0;
  const service = catalogService({
    ...f,
    now: () => now,
    privateSkills: {},
    host: {},
  });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'fast-refresh',
  });
  f.desktop.finish = () => assert.fail('waiting must leave the C lock free');
  f.desktop.read = () => assert.fail('waiting must stay read-only');
  const before = f.requests.length;
  now = 1000;
  await service.reconcile();
  assert.equal(f.requests.length, before + 1);
  now = 1999;
  await service.reconcile();
  assert.equal(f.requests.length, before + 1);
  now = 2000;
  await service.reconcile();
  assert.equal(f.requests.length, before + 2);
});

test('guardian heartbeat rewrites do not force catalog refresh but semantic state changes do', async (t) => {
  const f = catalogFixture(t);
  t.mock.timers.enable({ apis: ['setInterval'] });
  const events = [];
  const close = watchCatalogIdentity({
    current: f.current,
    home: f.home,
    managed: false,
    onChange: async (event) => {
      events.push(event.changed);
    },
  });
  t.after(close);
  await setImmediate();
  assert.deepEqual(events, [true]);
  t.mock.timers.tick(1000);
  await setImmediate();
  assert.deepEqual(events, [true, false]);
  f.state.revision++;
  f.state.clock.lastMonotonicMillis++;
  writeFileSync(
    join(f.home, 'auriga/guardian/state.json'),
    JSON.stringify(f.state),
  );
  t.mock.timers.tick(1000);
  await setImmediate();
  assert.equal(events.at(-1), false);
  f.state.guardian.state = 'active';
  writeFileSync(
    join(f.home, 'auriga/guardian/state.json'),
    JSON.stringify(f.state),
  );
  t.mock.timers.tick(1000);
  await setImmediate();
  assert.equal(events.at(-1), true);
});
