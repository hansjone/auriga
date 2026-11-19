import test from 'node:test';
import assert from 'node:assert/strict';
import { inboxFixture } from './inbox-fixture.mjs';
import { createMemoryLedgerStore } from '../../src/network/host/store.js';
import { createSharedRuntime } from '../../src/shared/runtime.js';
import { createSharedMessageManager } from '../../src/shared/message-manager.js';

function setup(t, options = {}) {
  const f = inboxFixture(options),
    runtime = createSharedRuntime({ ...f.ports.options, ports: f.ports });
  const preferenceStore = options.preferenceStore ?? createMemoryLedgerStore();
  const manager = createSharedMessageManager(f.ctx, {
    runtime,
    ports: f.ports,
    preferenceStore,
    inboxStore: () => f.store,
  });
  t.after(async () => {
    await manager.close();
    runtime.close();
    await f.close();
  });
  return { ...f, manager, preferenceStore, runtime };
}

test('member status bootstraps address with receive false; one explicit persisted opt-in drives real durable inbox', async (t) => {
  const f = setup(t);
  await f.manager.start();
  const before = await f.manager.status(f.binding);
  assert.match(before.address, /^aur1/);
  assert.equal(before.receiveEnabled, false);
  await f.manager.pump();
  assert.deepEqual(f.requests, []);
  const enabled = await f.manager.setReceive(f.binding, true);
  assert.equal(enabled.receiveEnabled, true);
  assert.equal(enabled.pending, false);
  assert.equal(
    f.preferenceStore.snapshot().entries[f.binding.sessionId].enabled,
    true,
  );
  f.deliveries.push(f.delivery());
  await f.manager.pump();
  assert.equal(f.calls.filter(([name]) => name === 'prompt').length, 1);
  assert.equal(f.store.snapshot().state.durable.resumeCursor, '1');
  await f.manager.pump();
  assert.equal(
    f.requests.filter((frame) => frame.type === 'receive_set').length,
    1,
  );
});

test('disabling fences an in-flight fetch synchronously and eventually commits actual receiveSet false', async (t) => {
  let entered, release;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const f = setup(t, {
    beforeReceive: async (frame) => {
      if (frame.type === 'fetch') {
        entered();
        await gate;
      }
    },
  });
  await f.manager.setReceive(f.binding, true);
  f.deliveries.push(f.delivery());
  const pumping = f.manager.pump();
  await started;
  const disabling = f.manager.setReceive(f.binding, false);
  release();
  await pumping;
  const result = await disabling;
  assert.equal(result.receiveEnabled, false);
  assert.equal(result.desiredReceiveEnabled, false);
  assert.equal(
    f.calls.some(([name]) => name === 'prompt'),
    false,
  );
  assert.equal(f.requests.at(-1).type, 'receive_set');
  assert.equal(f.requests.at(-1).enabled, false);
});

test('explicit preference survives manager restart but never cold-resumes an unloaded native agent', async (t) => {
  const f = setup(t);
  await f.manager.setReceive(f.binding, true);
  await f.manager.close();
  const restored = createSharedMessageManager(f.ctx, {
    runtime: f.runtime,
    ports: f.ports,
    preferenceStore: f.preferenceStore,
    inboxStore: () => f.store,
  });
  t.after(() => restored.close());
  f.agents.delete(f.binding.sessionId);
  const count = f.events.length,
    resumes = f.calls.filter(([name]) => name === 'resume').length;
  await restored.pump();
  assert.equal(f.events.length, count);
  assert.equal(f.calls.filter(([name]) => name === 'resume').length, resumes);
  f.agents.set(f.binding.sessionId, f.agent);
  await restored.pump();
  assert.equal(
    f.requests.filter((frame) => frame.type === 'receive_set').length,
    1,
  );
  assert.equal((await restored.status(f.binding)).receiveEnabled, true);
});

test('uncertain preference write poisons writer and fences intake rather than overwriting from old memory', async (t) => {
  let reject = false;
  const preferenceStore = createMemoryLedgerStore({
    beforeCommit() {
      if (reject) throw new Error('uncertain commit');
    },
  });
  const f = setup(t, { preferenceStore });
  await f.manager.setReceive(f.binding, true);
  reject = true;
  await assert.rejects(f.manager.setReceive(f.binding, false));
  f.deliveries.push(f.delivery());
  await f.manager.pump();
  await assert.rejects(f.manager.setReceive(f.binding, true));
  assert.equal(
    f.calls.some(([name]) => name === 'prompt'),
    false,
  );
});

test('revocation immediately cancels member recipient and cannot re-enable the old binding', async (t) => {
  const f = setup(t);
  await f.manager.setReceive(f.binding, true);
  f.deliveries.push(f.delivery());
  f.manager.revoke({ ...f.binding, epoch: 2, revision: 2, authGeneration: 2 });
  await f.manager.pump();
  await assert.rejects(f.manager.status(f.binding));
  assert.equal(
    f.calls.some(([name]) => name === 'prompt'),
    false,
  );
});

test('provider rejection remains visible and cannot be presented as durable acceptance', async (t) => {
  const f = setup(t);
  await f.manager.setReceive(f.binding, true);
  f.deliveries.push(f.delivery());
  f.ctx.sessions.flush = async () => false;
  await f.manager.pump();
  const result = await f.manager.status(f.binding);
  assert.equal(result.available, false);
  assert.equal(result.lastError, 'common.unavailable');
  assert.equal(
    f.requests.some((frame) => frame.type === 'receipt'),
    false,
  );
});

test('an ended background task reopens through fresh K authorization without changing address or losing the inbox', async (t) => {
  let failOnce = true;
  const f = setup(t, {
    beforeReceive(frame) {
      if (frame.type === 'fetch' && failOnce) {
        failOnce = false;
        throw Object.assign(new Error('ended task'), {
          code: 'shared.revoked',
        });
      }
    },
  });
  const before = await f.manager.setReceive(f.binding, true);
  f.deliveries.push(f.delivery());
  await f.manager.pump();
  assert.equal(f.active.size, 0);
  await f.manager.pump();
  assert.equal((await f.manager.status(f.binding)).address, before.address);
  assert.equal(f.active.size, 1);
  assert.equal(f.calls.filter(([name]) => name === 'prompt').length, 1);
  assert.equal(f.store.snapshot().state.durable.resumeCursor, '1');
  assert.equal(
    f.requests.filter((frame) => frame.type === 'receive_set').length,
    1,
  );
});
