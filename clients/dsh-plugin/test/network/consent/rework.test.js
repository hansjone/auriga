import { test } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { openConsent } from '../../../src/network/consent/index.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';

const identity = {
  tenant: 'tenant-example',
  realm: '2',
  node: '10',
  nodeIncarnation: 'install-example',
};
const local = { session: 'local-ui' };
const peer = { tenant: identity.tenant, realm: '2', node: '20' };
async function setup(t, options = {}) {
  let at = 100_000;
  let ownership = 'owner-1';
  let owner = 'alice';
  const store = options.store ?? createMemoryLedgerStore();
  const ports = {
    store,
    identity,
    now: () => at,
    callbackTimeoutMs: 20,
    actionTimeoutMs: 20,
    responseDelayMs: 100,
    authorizeLocal: async (c) => c === local,
    resolveSource: async (s) => ({
      ...s,
      userId: s.node === '30' ? 'bob' : owner,
      ownershipRevision: ownership,
    }),
    organization: async () => ({
      available: true,
      sameOrganization: true,
      revision: 'org-1',
      conversation: 'allow',
      execution: 'allow',
    }),
    ...options,
  };
  const service = await openConsent(ports);
  t.after(() => service.close());
  const request = (id, node = '20') => ({
    layer: 'conversation',
    requestId: id,
    source: { ...peer, node },
    digest: 'a'.repeat(64),
    bytes: 1,
    createdAt: at,
    expiresAt: at + 1000,
  });
  async function intake(input) {
    await service.request(input);
    return service.getRequest(local, input);
  }
  return {
    service,
    store,
    ports,
    request,
    intake,
    advance: (ms) => {
      at += ms;
    },
    transfer: () => {
      ownership = 'owner-2';
      owner = 'carol';
    },
  };
}

test('M1: node block survives owner revision and same-org automatic defaults', async (t) => {
  const f = await setup(t);
  let r = await f.intake(f.request('block'));
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'block',
    choice: 'block',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  await f.service.setDefault(local, 'conversation', 'same_org');
  f.transfer();
  f.advance(86_400_001);
  r = await f.intake(f.request('transferred'));
  assert.equal(r.status, 'denied');
  assert.equal(r.localReason, 'block');
});

test('M2: one source cannot fill target records, audit or outbox; terminal retention reclaims space', async (t) => {
  const f = await setup(t, {
    limits: {
      maxRecords: 8,
      maxSourceRecords: 3,
      maxUserRecords: 4,
      maxSourceTerminal: 2,
      maxUserTerminal: 3,
      maxAudit: 8,
      maxRequestsPerMinute: 5,
      terminalRetentionMs: 100,
    },
  });
  const r = await f.intake(f.request('blocked'));
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'blocked',
    choice: 'block',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  for (let n = 0; n < 12; n++)
    await f.service.request(f.request(`spam-${n}`, n % 2 ? '20' : '21'));
  const snapshot = f.store.snapshot();
  assert.ok(Object.keys(snapshot.requests).length <= 4);
  assert.ok(snapshot.audit.length <= 8);
  assert.ok(snapshot.outbox.length <= 6);
  assert.equal((await f.intake(f.request('healthy', '30'))).status, 'pending');
  f.advance(1101);
  await f.service.listPending(local);
  assert.equal(Object.keys(f.store.snapshot().requests).length, 0);
  assert.equal(
    (await f.intake(f.request('healthy-again', '30'))).status,
    'pending',
  );
});

test('L1/L2: source APIs reveal no private fields; blocked and pending have identical fixed response timing', async (t) => {
  const f = await setup(t);
  const first = await f.intake(f.request('block'));
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'block',
    choice: 'block',
    sourceScope: 'source_node',
    expectedRevision: first.policyRevision,
  });
  const elapsed = [];
  for (const input of [f.request('blocked'), f.request('pending', '30')]) {
    const start = performance.now();
    assert.deepEqual(await f.service.request(input), {
      status: 'request_submitted',
    });
    elapsed.push(performance.now() - start);
    await assert.rejects(
      f.service.withAuthorization(input, async () =>
        assert.fail('must not run'),
      ),
      /authorization required/,
    );
  }
  assert.ok(elapsed.every((n) => n >= 95 && n < 500));
  await assert.rejects(
    f.service.getRequest({}, f.request('pending', '30')),
    /local/,
  );
});

test('L3: an allow-once request is consumed durably before an action and cannot start twice after reopen', async (t) => {
  const f = await setup(t);
  const input = f.request('once');
  const r = await f.intake(input);
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'once',
    choice: 'allow_once',
    expectedRevision: r.policyRevision,
  });
  let starts = 0;
  assert.deepEqual(
    await f.service.withAuthorization(input, async () => {
      starts++;
      return { secret: 'not-for-source' };
    }),
    { status: 'request_submitted' },
  );
  await assert.rejects(
    f.service.withAuthorization(input, async () => {
      starts++;
    }),
    /authorization required/,
  );
  await f.service.close();
  const reopened = await openConsent(f.ports);
  t.after(() => reopened.close());
  await assert.rejects(
    reopened.withAuthorization(input, async () => {
      starts++;
    }),
    /authorization required/,
  );
  assert.equal(starts, 1);
});

test('L3: a timed-out action aborts, releases policy queue, and is never retried', async (t) => {
  const f = await setup(t);
  const input = f.request('hang');
  const r = await f.intake(input);
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'hang',
    choice: 'allow_once',
    expectedRevision: r.policyRevision,
  });
  let signal;
  await assert.rejects(
    f.service.withAuthorization(input, async (s) => {
      signal = s;
      return new Promise(() => {});
    }),
    /callback timeout/,
  );
  assert.equal(signal.aborted, true);
  await f.service.setDefault(local, 'conversation', 'deny_all');
  await assert.rejects(
    f.service.withAuthorization(input, async () => assert.fail('late retry')),
    /authorization required/,
  );
});

test('L3: IAM and local authorization callbacks time out without wedging revocation', async (t) => {
  let hung = true;
  const f = await setup(t, {
    resolveSource: async (s) =>
      hung
        ? new Promise(() => {})
        : { ...s, userId: 'alice', ownershipRevision: '1' },
  });
  await assert.rejects(
    f.service.request(f.request('hung-iam')),
    /intake unavailable/,
  );
  hung = false;
  await f.service.setDefault(local, 'conversation', 'deny_all');
  const g = await setup(t, {
    authorizeLocal: async () => new Promise(() => {}),
  });
  await assert.rejects(
    g.service.setDefault(local, 'conversation', 'same_org'),
    /callback timeout/,
  );
});

test('L4: cooldown gives explicit local feedback without storing a future allow rule; source clocks do not set waiting TTL', async (t) => {
  const f = await setup(t);
  const a = await f.intake(f.request('deny'));
  const b = await f.intake(f.request('other-node', '21'));
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'deny',
    choice: 'deny',
    expectedRevision: a.policyRevision,
  });
  await assert.rejects(
    f.service.decide(local, {
      layer: 'conversation',
      requestId: 'other-node',
      choice: 'allow_source',
      sourceScope: 'source_user',
      expectedRevision: b.policyRevision,
    }),
    /cooldown active/,
  );
  assert.deepEqual((await f.service.getSettings(local)).rules, []);
  await f.service.listPending(local);
  await assert.rejects(
    f.service.decide(local, {
      layer: 'conversation',
      requestId: 'other-node',
      choice: 'allow_once',
      expectedRevision: b.policyRevision,
    }),
    /cooldown active/,
  );
  const skewed = {
    ...f.request('skewed', '30'),
    createdAt: 100_500,
    expiresAt: 101_000,
  };
  const row = await f.intake(skewed);
  assert.equal(row.receivedAt, 100_000);
  assert.equal(row.deadline, skewed.expiresAt);
});

test('M2: per-user minute budget spans source nodes, expires, and does not throttle another user', async (t) => {
  const f = await setup(t, { limits: { maxRequestsPerMinute: 2 } });
  await f.service.setDefault(local, 'conversation', 'same_org');
  await f.service.request(f.request('one'));
  await f.service.request(f.request('two', '21'));
  await f.service.request(f.request('limited', '22'));
  assert.equal(await f.service.getRequest(local, f.request('limited')), null);
  assert.equal((await f.intake(f.request('bob', '30'))).status, 'allowed');
  f.advance(60_000);
  assert.equal(
    (await f.intake(f.request('alice-after-window'))).status,
    'allowed',
  );
});

test('M2: bounded audit checkpoint and terminal cleanup survive reopen without replaying an expired request', async (t) => {
  const f = await setup(t, {
    limits: { maxAudit: 4, terminalRetentionMs: 50 },
  });
  const input = f.request('expired');
  await f.service.request(input);
  for (let n = 0; n < 8; n++)
    await f.service.setDefault(
      local,
      'execution',
      n % 2 ? 'ask_each' : 'deny_all',
    );
  const summary = await f.service.getAuditSummary(local);
  assert.ok(summary.dropped > 0);
  assert.match(summary.digest, /^[a-f0-9]{64}$/);
  assert.equal((await f.service.listAudit(local)).length, 4);
  f.advance(1051);
  await f.service.listPending(local);
  assert.equal(await f.service.getRequest(local, input), null);
  await f.service.close();
  const reopened = await openConsent(f.ports);
  t.after(() => reopened.close());
  assert.deepEqual(await reopened.request(input), {
    status: 'request_submitted',
  });
  assert.equal(await reopened.getRequest(local, input), null);
  assert.ok((await reopened.getAuditSummary(local)).dropped >= summary.dropped);
});

test('L1/L2: slow IAM failure shares the fixed response window and late lookup cannot approve a request', async (t) => {
  let finish;
  const f = await setup(t, {
    resolveSource: async () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  });
  const start = performance.now();
  await assert.rejects(
    f.service.request(f.request('slow')),
    /intake unavailable/,
  );
  assert.ok(performance.now() - start >= 95);
  finish({ ...peer, userId: 'alice', ownershipRevision: '1' });
  await f.service.setDefault(local, 'conversation', 'deny_all');
  assert.equal(await f.service.getRequest(local, f.request('slow')), null);
});

test('version-one snapshots migrate while preserving decisions and sticky node blocks', async (t) => {
  const f = await setup(t);
  const r = await f.intake(f.request('legacy'));
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'legacy',
    choice: 'block',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  await f.service.close();
  const legacy = f.store.snapshot();
  legacy.version = 1;
  delete legacy.traffic;
  delete legacy.auditSummary;
  for (const row of Object.values(legacy.requests)) {
    delete row.receivedAt;
    delete row.terminalAt;
  }
  f.store.restore(legacy);
  f.transfer();
  f.advance(86_400_001);
  const reopened = await openConsent(f.ports);
  t.after(() => reopened.close());
  const input = f.request('after-migration');
  await reopened.request(input);
  assert.equal((await reopened.getRequest(local, input)).localReason, 'block');
  assert.equal(f.store.snapshot().version, 2);
});

test('M2: a legacy overlong expiresAt cannot retain a terminal record beyond local deadline and retention', async (t) => {
  const f = await setup(t);
  const r = await f.intake(f.request('legacy-long'));
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: 'legacy-long',
    choice: 'deny',
    expectedRevision: r.policyRevision,
  });
  await f.service.close();
  const legacy = f.store.snapshot();
  legacy.version = 1;
  const row = Object.values(legacy.requests)[0];
  row.request.expiresAt = row.request.createdAt + 10 * 86_400_000;
  row.deadline = row.request.createdAt + 86_400_000;
  delete row.receivedAt;
  delete row.terminalAt;
  f.store.restore(legacy);
  f.advance(2 * 86_400_000);
  const reopened = await openConsent(f.ports);
  t.after(() => reopened.close());
  await reopened.listPending(local);
  assert.deepEqual(f.store.snapshot().requests, {});
});
