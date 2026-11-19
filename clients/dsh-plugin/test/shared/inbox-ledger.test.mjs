import test from 'node:test';
import assert from 'node:assert/strict';
import { setup } from './message-fixture.mjs';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { openSharedInboxLedger } from '../../src/shared/message-inbox-ledger.js';

async function fixture() {
  const f = setup();
  const context = await f.contexts.prepare(f.request, f.task);
  const store = createMemoryLedgerStore();
  return { ...f, context, inboxStore: store };
}

test('shared inbox adopts only the actual platform registration without an owner/local allocation log', async () => {
  const f = await fixture();
  const ledger = await openSharedInboxLedger({
    context: f.context,
    store: f.inboxStore,
  });
  try {
    assert.deepEqual(ledger.identity, {
      tenant: f.binding.tenantId,
      realm: '2',
      node: '64',
      nodeIncarnation: f.context.connection.incarnation,
    });
    assert.equal(
      ledger.peek().locals['7'].registrationId,
      f.registration.registration_id,
    );
    assert.equal(
      ledger.peek().bindings['7'].hostSessionId,
      f.binding.sessionId,
    );
    assert.equal(ledger.peek().bindings['7'].receiveMode, 'receive_none');
    assert.equal(ledger.peek().durable.receive['7'], undefined);
    assert.equal(ledger.peek().localQueue, undefined);
    await ledger.update((draft) => {
      draft.durable.receive['7'] = {
        enabled: false,
        version: '1',
        pending: null,
      };
    });
    assert.equal(ledger.peek().revision, 1);
    assert.throws(() => {
      ledger.peek().bindings['7'].hostSessionId = 'forged';
    });
  } finally {
    await ledger.close();
  }
  const reopened = await openSharedInboxLedger({
    context: f.context,
    store: f.inboxStore,
  });
  assert.equal(reopened.peek().revision, 1);
  await reopened.close();
  await f.contexts.close();
});

test('a missing cache never erases a platform receive decision, message history or committed cursor', async (t) => {
  for (const [key, value] of [
    ['receiveEnabled', true],
    ['receiveVersion', '2'],
    ['headSeq', '1'],
    ['committedCursor', '1'],
  ])
    await t.test(key, async () => {
      const f = await fixture();
      const context = {
        ...f.context,
        mailbox: { ...f.context.mailbox, [key]: value },
      };
      await assert.rejects(
        openSharedInboxLedger({ context, store: f.inboxStore }),
      );
      assert.equal(f.inboxStore.snapshot(), undefined);
      await f.contexts.close();
    });
});

test('reopening refuses changed K, native or platform registration authority', async (t) => {
  for (const kind of ['member', 'local', 'boot', 'producer'])
    await t.test(kind, async () => {
      const f = await fixture();
      const ledger = await openSharedInboxLedger({
        context: f.context,
        store: f.inboxStore,
      });
      await ledger.close();
      const saved = f.inboxStore.snapshot();
      if (kind === 'member') saved.authority.binding.memberId = 'changed';
      if (kind === 'local') saved.authority.registration.local = '8';
      if (kind === 'boot') saved.authority.connection.bootId = 'changed';
      if (kind === 'producer') saved.authority.producerEpoch = '99';
      f.inboxStore.restore(saved);
      await assert.rejects(
        openSharedInboxLedger({ context: f.context, store: f.inboxStore }),
      );
      await f.contexts.close();
    });
});

test('an uncertain inbox commit poisons that writer and never overwrites the prior receipt evidence', async () => {
  const f = await fixture();
  let fail = false;
  const store = createMemoryLedgerStore({
    beforeCommit: () => {
      if (fail) throw new Error('synthetic disk failure');
    },
  });
  const ledger = await openSharedInboxLedger({ context: f.context, store });
  fail = true;
  await assert.rejects(
    ledger.update((draft) => {
      draft.durable.receive['7'] = {
        enabled: false,
        version: '1',
        pending: null,
      };
    }),
  );
  assert.throws(() => ledger.assertAvailable());
  await assert.rejects(ledger.update(() => {}));
  assert.equal(store.snapshot().state.revision, 0);
  await ledger.close();
  await f.contexts.close();
});
