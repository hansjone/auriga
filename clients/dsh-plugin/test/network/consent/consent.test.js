import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  openConsent as openRawConsent,
  createFileConsentStore,
  publicReceipt,
} from '../../../src/network/consent/index.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';

const identity = {
  tenant: 'tenant-example',
  realm: '2',
  node: '10',
  nodeIncarnation: 'install-example',
};
const local = { session: 'authenticated-local-ui' };
const source = { tenant: identity.tenant, realm: '2', node: '20' };
const layers = ['conversation', 'execution'];

// Existing policy assertions explicitly inspect the authenticated local view.
// Source-facing privacy/timing is tested against the raw service in rework.test.js.
async function openConsent(options) {
  const service = await openRawConsent(options);
  return {
    ...service,
    request: async (input) => {
      await service.request(input);
      const row = await service.getRequest(local, input);
      if (!row) throw new Error('request not retained');
      return row;
    },
  };
}

async function fixture(t, options = {}) {
  let time = 1_000_000;
  let owner = 'alice';
  let ownershipRevision = 'owner-1';
  let org = {
    available: true,
    revision: 'org-1',
    sameOrganization: true,
    conversation: 'allow',
    execution: 'allow',
  };
  const store = options.store ?? createMemoryLedgerStore();
  const ports = {
    store,
    identity,
    callbackTimeoutMs: 20,
    actionTimeoutMs: 50,
    responseDelayMs: 100,
    now: () => time,
    resolveSource: async (s) => ({ ...s, userId: owner, ownershipRevision }),
    organization: async () => org,
    authorizeLocal: async (context) => context?.session === local.session,
    ...options,
  };
  const service = await openConsent(ports);
  t.after(() => service.close());
  return {
    service,
    store,
    ports,
    advance: (ms) => {
      time += ms;
    },
    owner: (id) => {
      owner = id;
      ownershipRevision += '-new';
    },
    org: (patch) => {
      org = { ...org, ...patch };
    },
    request: (layer = 'conversation', id = 'request-1', peer = source) => ({
      layer,
      requestId: id,
      source: peer,
      digest: 'a'.repeat(64),
      createdAt: time,
      expiresAt: time + 86_400_000,
      bytes: 100,
    }),
  };
}

for (const layer of layers) {
  for (const choice of ['allow_once', 'allow_source', 'deny', 'block']) {
    test(`${layer}: ${choice} is durable, scoped and replay-safe`, async (t) => {
      const f = await fixture(t);
      const request = f.request(layer);
      assert.equal((await f.service.request(request)).status, 'pending');
      const decision = await f.service.decide(local, {
        layer,
        requestId: request.requestId,
        choice,
        sourceScope: 'source_node',
        expectedRevision: 0,
      });
      assert.equal(
        decision.status,
        choice.startsWith('allow') ? 'allowed' : 'denied',
      );
      assert.equal(
        (await f.service.request(request)).decisionId,
        decision.decisionId,
      );
      assert.equal(
        (await f.service.request(f.request(layer, 'next'))).status,
        choice === 'allow_source'
          ? 'allowed'
          : choice === 'allow_once'
            ? 'pending'
            : 'denied',
      );
      assert.equal(
        (await f.service.getSettings(local)).defaults[layer],
        'ask_each',
      );
      const other = layer === 'conversation' ? 'execution' : 'conversation';
      // Use another node/user so the shared pending-source quota is irrelevant.
      assert.equal(
        (
          await f.service.request(
            f.request(other, 'other', { ...source, node: '21' }),
          )
        ).status,
        'pending',
      );
      await f.service.close();
      const reopened = await openConsent(f.ports);
      t.after(() => reopened.close());
      assert.equal((await reopened.request(request)).status, decision.status);
    });
  }

  test(`${layer}: defaults and organization ceilings only tighten`, async (t) => {
    const f = await fixture(t);
    for (const mode of ['same_org', 'allowlist', 'deny_all', 'ask_each']) {
      await f.service.setDefault(local, layer, mode);
      const r = await f.service.request(f.request(layer, mode));
      assert.equal(
        r.status,
        mode === 'same_org'
          ? 'allowed'
          : mode === 'deny_all'
            ? 'denied'
            : 'pending',
      );
      if (r.status === 'pending')
        await f.service.decide(local, {
          layer,
          requestId: mode,
          choice: 'deny',
          expectedRevision: r.policyRevision,
        });
      f.advance(86_400_001);
    }
    await f.service.setDefault(local, layer, 'same_org');
    f.org({ [layer]: 'ask_each' });
    const r = await f.service.request(f.request(layer, 'org-ask'));
    assert.equal(r.status, 'pending');
    f.org({ [layer]: 'deny_all' });
    assert.equal(
      (
        await f.service.decide(local, {
          layer,
          requestId: 'org-ask',
          choice: 'allow_once',
          expectedRevision: r.policyRevision,
        })
      ).status,
      'denied',
    );
    f.org({ available: false });
    assert.equal(
      (await f.service.request(f.request(layer, 'unavailable'))).status,
      'denied',
    );
  });
}

test('IAM-verified user rules span source nodes, not owners or target installations', async (t) => {
  const f = await fixture(t);
  const r = await f.service.request(f.request());
  await f.service.decide(local, {
    layer: r.request.layer,
    requestId: r.request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_user',
    expectedRevision: r.policyRevision,
  });
  assert.equal(
    (
      await f.service.request(
        f.request('conversation', 'another-node', { ...source, node: '22' }),
      )
    ).status,
    'allowed',
  );
  f.owner('bob');
  assert.equal(
    (await f.service.request(f.request('conversation', 'new-owner'))).status,
    'pending',
  );
  const another = await fixture(t, { identity: { ...identity, node: '11' } });
  assert.equal(
    (await another.service.request(another.request())).status,
    'pending',
  );
  await f.service.close();
  await assert.rejects(
    openConsent({
      ...f.ports,
      store: f.store,
      identity: { ...identity, node: '11' },
    }),
  );
});

test('persistent allowlist, same-org boundaries, block precedence and unblock', async (t) => {
  const f = await fixture(t);
  await f.service.setDefault(local, 'conversation', 'same_org');
  f.org({ sameOrganization: false });
  const pending = await f.service.request(f.request());
  assert.equal(pending.status, 'pending');
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: pending.request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_user',
    expectedRevision: pending.policyRevision,
  });
  await f.service.setDefault(local, 'conversation', 'allowlist');
  assert.equal(
    (await f.service.request(f.request('conversation', 'listed'))).status,
    'allowed',
  );
  const blockRequest = await f.service.request(
    f.request('conversation', 'block-node'),
  );
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: blockRequest.request.requestId,
    choice: 'block',
    sourceScope: 'source_node',
    expectedRevision: blockRequest.policyRevision,
  });
  f.advance(86_400_001);
  assert.equal(
    (await f.service.request(f.request('conversation', 'blocked'))).status,
    'denied',
  );
  assert.equal((await f.service.listPending(local)).length, 0);
  await f.service.revoke(local, {
    layer: 'conversation',
    source,
    sourceScope: 'source_node',
  });
  assert.equal(
    (await f.service.request(f.request('conversation', 'unblocked'))).status,
    'allowed',
  );
  await f.service.setDefault(local, 'conversation', 'deny_all');
  assert.equal(
    (await f.service.request(f.request('conversation', 'deny-listed'))).status,
    'denied',
  );
});

test('policy changes in one layer preserve the other layer once grant and refresh pending revision', async (t) => {
  const f = await fixture(t);
  const request = f.request('execution');
  let r = await f.service.request(request);
  await f.service.decide(local, {
    layer: 'execution',
    requestId: request.requestId,
    choice: 'allow_once',
    expectedRevision: r.policyRevision,
  });
  await f.service.setDefault(local, 'conversation', 'deny_all');
  assert.equal((await f.service.request(request)).status, 'allowed');
  r = await f.service.request(f.request('execution', 'another'));
  await f.service.setDefault(local, 'conversation', 'ask_each');
  const [pending] = await f.service.listPending(local);
  assert.ok(pending.policyRevision > r.policyRevision);
  await f.service.decide(local, {
    layer: 'execution',
    requestId: pending.request.requestId,
    choice: 'allow_once',
    expectedRevision: pending.policyRevision,
  });
});

test('node rules and existing once grants are invalidated by current IAM ownership', async (t) => {
  const f = await fixture(t);
  const request = f.request();
  const r = await f.service.request(request);
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  f.owner('bob');
  assert.equal((await f.service.request(request)).status, 'pending');
  await assert.rejects(
    f.service.request(
      f.request('execution', 'cross-tenant', {
        ...source,
        tenant: 'another-tenant',
      }),
    ),
    /intake unavailable/,
  );
  await assert.rejects(
    f.service.request(
      f.request('execution', 'cross-realm', { ...source, realm: '3' }),
    ),
    /intake unavailable/,
  );
});

test('user pending quota spans source nodes and the deadline cannot be extended by retry', async (t) => {
  const f = await fixture(t);
  for (const node of ['20', '21', '22'])
    assert.equal(
      (
        await f.service.request(
          f.request('conversation', node, { ...source, node }),
        )
      ).status,
      'pending',
    );
  assert.equal(
    (
      await f.service.request(
        f.request('conversation', 'fourth', { ...source, node: '23' }),
      )
    ).status,
    'denied',
  );
  const original = f.request('execution', 'short', { ...source, node: '24' });
  f.owner('bob');
  original.expiresAt = original.createdAt + 10;
  assert.equal(
    (await f.service.request(original)).deadline,
    original.expiresAt,
  );
  f.advance(10);
  assert.equal((await f.service.request(original)).status, 'expired');
  await assert.rejects(
    f.service.request({ ...original, expiresAt: original.expiresAt + 100 }),
    /intake unavailable/,
  );
});

test('revocation and an in-flight authorized action have a single local order', async (t) => {
  const f = await fixture(t);
  const request = f.request();
  const r = await f.service.request(request);
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  let started;
  const began = new Promise((resolve) => {
    started = resolve;
  });
  const action = f.service.withAuthorization(request, async () => {
    started();
    await gate;
    return 'started-before-revocation';
  });
  await began;
  let revoked = false;
  const revoke = f.service
    .revoke(local, {
      layer: 'conversation',
      source,
      sourceScope: 'source_node',
    })
    .then(() => {
      revoked = true;
    });
  await Promise.resolve();
  assert.equal(revoked, false);
  release();
  assert.deepEqual(await action, { status: 'request_submitted' });
  await revoke;
  await assert.rejects(
    f.service.withAuthorization(request, async () =>
      assert.fail('must not start'),
    ),
    /authorization/,
  );
});

test('deadline crossing during trusted lookups never grants or starts an action', async (t) => {
  let at = 100;
  let slow = false;
  const f = await fixture(t, {
    now: () => at,
    organization: async () => {
      if (slow) at += 100;
      return {
        available: true,
        revision: '1',
        sameOrganization: true,
        conversation: 'allow',
        execution: 'allow',
      };
    },
  });
  const request = { ...f.request(), createdAt: 100, expiresAt: 200 };
  const r = await f.service.request(request);
  slow = true;
  await assert.rejects(
    f.service.decide(local, {
      layer: 'conversation',
      requestId: request.requestId,
      choice: 'allow_source',
      sourceScope: 'source_node',
      expectedRevision: r.policyRevision,
    }),
    /terminal/,
  );
  assert.deepEqual((await f.service.getSettings(local)).rules, []);
});

test('reopen rejects malformed authority; outbox acknowledgment is durable and IDs are immutable', async (t) => {
  const f = await fixture(t);
  const request = f.request();
  await f.service.request(request);
  assert.equal(
    (
      await f.service.request({
        ...request,
        source: {
          node: source.node,
          realm: source.realm,
          tenant: source.tenant,
        },
      })
    ).status,
    'pending',
  );
  const events = await f.service.listOutbox(local);
  await f.service.acknowledgeOutbox(
    local,
    events.map((e) => e.id),
  );
  assert.deepEqual(await f.service.listOutbox(local), []);
  await f.service.close();
  const reopened = await openConsent(f.ports);
  assert.deepEqual(await reopened.listOutbox(local), []);
  await reopened.close();
  const corrupt = f.store.snapshot();
  corrupt.requests[Object.keys(corrupt.requests)[0]].manual = 'yes';
  f.store.restore(corrupt);
  await assert.rejects(openConsent(f.ports), /invalid request/);
});

test('cooldown aggregates verified users across nodes and locals; unblock and revoke ask again', async (t) => {
  const f = await fixture(t);
  let r = await f.service.request(f.request());
  await f.service.decide(local, {
    layer: r.request.layer,
    requestId: r.request.requestId,
    choice: 'deny',
    expectedRevision: r.policyRevision,
  });
  assert.equal(
    (
      await f.service.request(
        f.request('conversation', 'cooldown', { ...source, node: '22' }),
      )
    ).status,
    'denied',
  );
  f.advance(86_400_001);
  r = await f.service.request(f.request('conversation', 'after'));
  assert.equal(r.status, 'pending');
  await f.service.decide(local, {
    layer: r.request.layer,
    requestId: r.request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  await f.service.revoke(local, {
    layer: r.request.layer,
    source,
    sourceScope: 'source_node',
  });
  assert.equal(
    (await f.service.request(f.request('conversation', 'revoked'))).status,
    'pending',
  );
  await assert.rejects(
    f.service.decide(local, {
      layer: r.request.layer,
      requestId: r.request.requestId,
      choice: 'allow_once',
      expectedRevision: r.policyRevision,
    }),
    /revision/,
  );
});

test('pending limits, original deadlines, stable IDs and metadata-only intake', async (t) => {
  const f = await fixture(t, {
    limits: { maxPending: 2, maxPendingBytes: 200 },
  });
  const request = f.request();
  assert.equal((await f.service.request(request)).status, 'pending');
  assert.equal((await f.service.listPending(local)).length, 1);
  await assert.rejects(
    f.service.request({ ...request, digest: 'b'.repeat(64) }),
    /intake unavailable/,
  );
  await assert.rejects(
    f.service.request({ ...request, body: 'untrusted prompt' }),
    /metadata/,
  );
  assert.equal(
    (await f.service.request(f.request('conversation', 'same-node'))).status,
    'denied',
  );
  f.owner('bob');
  await f.service.request(
    f.request('conversation', 'second', { ...source, node: '21' }),
  );
  f.owner('charlie');
  assert.equal(
    (
      await f.service.request(
        f.request('execution', 'over', { ...source, node: '22' }),
      )
    ).status,
    'denied',
  );
  f.advance(86_400_000);
  assert.equal((await f.service.listPending(local)).length, 0);
  assert.equal((await f.service.request(request)).status, 'expired');
  await assert.rejects(
    f.service.decide(local, {
      layer: 'conversation',
      requestId: request.requestId,
      choice: 'allow_once',
      expectedRevision: 0,
    }),
    /terminal/,
  );
});

test('all rejection receipts have the same public appearance and no private metadata', async () => {
  const expected = { status: 'target_unavailable' };
  for (const status of ['denied', 'expired']) {
    for (const reason of [
      'deny',
      'block',
      'cooldown',
      'quota',
      'organization',
    ]) {
      assert.deepEqual(
        publicReceipt({ status, localReason: reason }, true),
        expected,
      );
      assert.deepEqual(publicReceipt({ status, localReason: reason }, false), {
        status: 'request_submitted',
      });
    }
  }
  assert.deepEqual(publicReceipt({ status: 'pending' }, false), {
    status: 'request_submitted',
  });
});

test('local mutations require authenticated UI; identity spoofing and missing IAM fail closed', async (t) => {
  const f = await fixture(t);
  await assert.rejects(
    f.service.setDefault({}, 'execution', 'same_org'),
    /local/,
  );
  await assert.rejects(f.service.listPending({}), /local/);
  await assert.rejects(
    f.service.request({
      ...f.request(),
      source: { ...source, userId: 'admin' },
    }),
    /source/,
  );
  const unavailable = await fixture(t, {
    resolveSource: async () => {
      throw new Error('IAM offline');
    },
  });
  await assert.rejects(
    unavailable.service.request(unavailable.request()),
    /intake unavailable/,
  );
  const spoof = await fixture(t, {
    resolveSource: async (s) => ({
      ...s,
      node: '99',
      userId: 'alice',
      ownershipRevision: '1',
    }),
  });
  await assert.rejects(
    spoof.service.request(spoof.request()),
    /intake unavailable/,
  );
});

test('revocation serializes with action authorization; stale approvals cannot run', async (t) => {
  const f = await fixture(t);
  const request = f.request();
  const r = await f.service.request(request);
  await f.service.decide(local, {
    layer: r.request.layer,
    requestId: r.request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  let calls = 0;
  await f.service.withAuthorization(request, async () => {
    calls++;
  });
  await f.service.revoke(local, {
    layer: r.request.layer,
    source,
    sourceScope: 'source_node',
  });
  await assert.rejects(
    f.service.withAuthorization(request, async () => {
      calls++;
    }),
    /authorization/,
  );
  assert.equal(calls, 1);
  const events = await f.service.listOutbox(local);
  assert.ok(events.some((e) => e.kind === 'policy_changed'));
  assert.ok(events.every((e) => !('source' in e) && !('localReason' in e)));
});

test('file persistence, atomic decision/outbox and uncertain write poison the service', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-consent-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  let failure = false;
  const store = createFileConsentStore({
    dir,
    beforeStep: (step) => {
      if (failure && step === 'sync_dir') throw new Error('disk failure');
    },
  });
  const f = await fixture(t, { store });
  const r = await f.service.request(f.request());
  await f.service.decide(local, {
    layer: r.request.layer,
    requestId: r.request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  await f.service.close();
  const reopened = await openConsent(f.ports);
  t.after(() => reopened.close());
  assert.equal(
    (await reopened.request(f.request('conversation', 'persisted'))).status,
    'allowed',
  );
  assert.ok((await reopened.listOutbox(local)).length >= 2);
  failure = true;
  await assert.rejects(
    reopened.revoke(local, {
      layer: 'conversation',
      source,
      sourceScope: 'source_node',
    }),
    /disk failure/,
  );
  await assert.rejects(
    reopened.request(f.request('conversation', 'after-failure')),
    /unavailable/,
  );
});

test('stored rule IDs allow revocation after ownership transfer without IAM availability', async (t) => {
  let offline = false;
  const f = await fixture(t, {
    resolveSource: async (s) => {
      if (offline) throw new Error('IAM offline');
      return { ...s, userId: 'alice', ownershipRevision: '1' };
    },
  });
  const r = await f.service.request(f.request());
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: r.request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_user',
    expectedRevision: r.policyRevision,
  });
  const [rule] = (await f.service.getSettings(local)).rules;
  offline = true;
  await f.service.revoke(local, { layer: 'conversation', ruleId: rule.id });
  assert.deepEqual((await f.service.getSettings(local)).rules, []);
  const revision = (await f.service.getSettings(local)).policyRevision;
  assert.equal(
    await f.service.revoke(local, { layer: 'conversation', ruleId: rule.id }),
    revision,
  );
  const events = await f.service.listOutbox(local);
  assert.ok(!events.some((e) => e.status === 'allowed'));
  assert.ok(events.some((e) => e.status === 'denied'));
  await f.service.acknowledgeOutbox(local, [r.decisionId]);
  assert.deepEqual(await f.service.listOutbox(local), events);
});

test('organization ask-each suppresses stored auto allow and new deny stops an already allowed action', async (t) => {
  const f = await fixture(t);
  const request = f.request('execution');
  const r = await f.service.request(request);
  await f.service.decide(local, {
    layer: 'execution',
    requestId: request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  f.org({ execution: 'ask_each' });
  assert.equal(
    (await f.service.request(f.request('execution', 'ask'))).status,
    'pending',
  );
  f.org({ execution: 'deny_all' });
  await assert.rejects(
    f.service.withAuthorization(request, async () =>
      assert.fail('no model call'),
    ),
    /authorization/,
  );
});

test('request capacity keeps revocation usable and pending byte limits stay enforced', async (t) => {
  const f = await fixture(t, { limits: { maxRecords: 1 } });
  const request = f.request();
  const r = await f.service.request(request);
  await f.service.decide(local, {
    layer: 'conversation',
    requestId: request.requestId,
    choice: 'allow_source',
    sourceScope: 'source_node',
    expectedRevision: r.policyRevision,
  });
  await assert.rejects(
    f.service.request(f.request('conversation', 'over')),
    /not retained/,
  );
  await f.service.revoke(local, {
    layer: 'conversation',
    source,
    sourceScope: 'source_node',
  });
  await assert.rejects(
    f.service.withAuthorization(request, async () => assert.fail('no action')),
    /authorization/,
  );
  const small = await fixture(t, { limits: { maxPendingBytes: 99 } });
  assert.equal((await small.service.request(small.request())).status, 'denied');
  assert.deepEqual(await small.service.listPending(local), []);
});

test('clock rollback and commit failure before rename cannot expose an approval', async (t) => {
  const f = await fixture(t);
  const request = f.request();
  const r = await f.service.request(request);
  f.advance(-1);
  await assert.rejects(
    f.service.decide(local, {
      layer: 'conversation',
      requestId: request.requestId,
      choice: 'allow_once',
      expectedRevision: r.policyRevision,
    }),
    /clock rollback/,
  );
  let fail = false;
  const dir = await mkdtemp(join(tmpdir(), 'auriga-consent-atomic-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = await fixture(t, {
    store: createFileConsentStore({
      dir,
      beforeStep: (step) => {
        if (fail && step === 'rename') throw new Error('ENOSPC');
      },
    }),
  });
  const pending = await file.service.request(file.request());
  fail = true;
  await assert.rejects(
    file.service.decide(local, {
      layer: 'conversation',
      requestId: pending.request.requestId,
      choice: 'allow_source',
      sourceScope: 'source_node',
      expectedRevision: pending.policyRevision,
    }),
    /ENOSPC/,
  );
  await assert.rejects(
    file.service.withAuthorization(file.request(), async () =>
      assert.fail('must not run'),
    ),
    /unavailable/,
  );
  await file.service.close();
  fail = false;
  const reopened = await openConsent(file.ports);
  t.after(() => reopened.close());
  assert.deepEqual((await reopened.getSettings(local)).rules, []);
  assert.equal((await reopened.request(file.request())).status, 'pending');
  assert.ok(
    !(await reopened.listOutbox(local)).some((e) => e.status === 'allowed'),
  );
});
