import { test } from 'node:test';
import assert from 'node:assert/strict';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
import { watchCatalogIdentity } from './broker-fixture.js';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { setImmediate } from 'node:timers/promises';

test('idle reconciliation refreshes by the minute and transport failures back off even on guardian changes', async (t) => {
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
  for (let i = 0; i < 59; i++) {
    now += 1000;
    await service.reconcile({ changed: true });
  }
  assert.equal(
    calls,
    failedCalls,
    'state events cannot bypass failure backoff',
  );
  now += 1000;
  await assert.rejects(service.reconcile({ changed: true }));
  const secondFailure = calls;
  now += 60000;
  await service.reconcile({ changed: true });
  assert.equal(
    calls,
    secondFailure,
    'the second failure backs off for two minutes',
  );
  now += 60000;
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
