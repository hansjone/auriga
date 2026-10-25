import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createInboundPolicy } from '../../../src/network/policy/inbound-policy.js';
import { createWorld, peer } from '../host/fixtures.js';

const source = (node) => ({ ...peer('1'), node });

test('1024-source pressure preserves evicted slow-scan strikes and alerts across restart and acknowledgement', async () => {
  const w = await createWorld();
  let now = 0;
  let policy = createInboundPolicy({ ledger: w.ledger, now: () => now });
  await policy.recordInvalid({
    from: peer('1'),
    recipientId: 'slow-one',
    local: '100',
  });
  now = 61_000;
  await policy.recordInvalid({
    from: peer('1'),
    recipientId: 'slow-two',
    local: '101',
  });
  now = 62_000;
  for (let i = 0; i < 1023; i++) {
    await policy.recordInvalid({
      from: source(`${1000 + i}`),
      recipientId: `fill-${i}`,
      local: '999',
    });
  }
  assert.equal(Object.keys(w.ledger.read().inboundPolicy.sources).length, 1024);
  await policy.recordInvalid({
    from: source('2023'),
    recipientId: 'overflow',
    local: '999',
  });
  const state = w.ledger.read().inboundPolicy;
  assert.equal(Object.keys(state.sources).length, 1024);
  assert.equal(state.sources['2.63'], undefined);
  assert.deepEqual(state.evictedScan, {
    sources: 1,
    shortHits: 1,
    longTargetStrikes: 2,
    lastAt: now,
  });
  assert.equal(policy.isBlocked(source('2024')), false);
  const pressure = policy
    .pendingReports()
    .find((r) => r.kind === 'source_capacity');
  assert.deepEqual(pressure.evictedScan, state.evictedScan);
  await w.restart();
  policy = createInboundPolicy({ ledger: w.ledger, now: () => now });
  assert.deepEqual(
    w.ledger.read().inboundPolicy.evictedScan,
    state.evictedScan,
  );
  assert.deepEqual(
    policy.pendingReports().find((r) => r.kind === 'source_capacity'),
    pressure,
  );
  await policy.markReportSent(pressure.id);
  assert.deepEqual(policy.pendingReports(), []);
  now++;
  // Fresh strike loss must produce an alert even if the previous warning
  // was acknowledged less than one short window ago.
  await policy.recordInvalid({
    from: source('2024'),
    recipientId: 'another-overflow',
    local: '999',
  });
  assert.deepEqual(w.ledger.read().inboundPolicy.evictedScan, {
    sources: 2,
    shortHits: 2,
    longTargetStrikes: 3,
    lastAt: now,
  });
  assert.deepEqual(
    policy.pendingReports().find((r) => r.kind === 'source_capacity')
      .evictedScan,
    w.ledger.read().inboundPolicy.evictedScan,
  );
  await w.ledger.close();
});

test('a restored source with no strikes is reclaimed ahead of an older slow scanner', async () => {
  const w = await createWorld();
  let now = 0;
  const policy = createInboundPolicy({
    ledger: w.ledger,
    now: () => now,
    maxSources: 2,
  });
  await policy.recordInvalid({
    from: peer('1'),
    recipientId: 'slow',
    local: '100',
  });
  // Valid durable state left after another source's evidence expired.
  await w.ledger.update((draft) => {
    draft.inboundPolicy.sources['2.64'] = {
      hits: [],
      longTargets: [],
      manual: false,
      blockedUntil: 0,
      lastSeen: 100,
    };
  });
  now = 101;
  await policy.recordInvalid({
    from: source('65'),
    recipientId: 'new',
    local: '999',
  });
  const state = w.ledger.read().inboundPolicy;
  assert.deepEqual(Object.keys(state.sources).sort(), ['2.63', '2.65']);
  assert.equal(state.sources['2.63'].longTargets.length, 1);
  assert.equal(state.evictedScan?.sources ?? 0, 0);
  assert.deepEqual(policy.pendingReports(), []);
  await w.ledger.close();
});

for (const order of ['scan_first', 'capacity_first']) {
  test(`independent report quotas preserve unacknowledged scans when ${order}`, async () => {
    const w = await createWorld();
    const policy = createInboundPolicy({
      ledger: w.ledger,
      maxSources: 2,
      maxReports: 1,
      hitThreshold: 2,
    });
    const hit = (node, id) =>
      policy.recordInvalid({
        from: source(node),
        recipientId: id,
        local: '999',
      });
    let scan;
    if (order === 'scan_first') {
      await hit('61', 'scan-one');
      await hit('61', 'scan-two');
      scan = policy.pendingReports().find((r) => r.kind === 'scan');
      await hit('62', 'other-one');
      await hit('63', 'capacity-one');
      assert.deepEqual(
        policy.pendingReports().find((r) => r.kind === 'scan'),
        scan,
      );
      // A second scan cannot consume the first scan's reserved slot.
      await hit('63', 'scan-three');
    } else {
      await hit('61', 'fill-one');
      await hit('62', 'fill-two');
      await hit('63', 'capacity-one');
      await hit('62', 'scan-one');
      scan = policy.pendingReports().find((r) => r.kind === 'scan');
      assert.ok(scan);
      await hit('64', 'capacity-two');
    }
    const reports = policy.pendingReports();
    assert.equal(reports.filter((r) => r.kind === 'scan').length, 1);
    assert.equal(reports.filter((r) => r.kind === 'source_capacity').length, 1);
    assert.equal(reports.length, 2);
    assert.deepEqual(
      reports.find((r) => r.kind === 'scan'),
      scan,
    );
    await policy.markReportSent(
      reports.find((r) => r.kind === 'source_capacity').id,
    );
    assert.deepEqual(policy.pendingReports(), [scan]);
    await w.ledger.close();
  });
}
