import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { shellRuntimePeer } from '../shell-runtime-peer.js';

async function models(t, options = {}) {
  const f = await shellRuntimePeer(t, options),
    registrations = new Set(),
    retired = [];
  const detach = await f.runtime.attachModels({
    registerAdapter(_providers, adapter) {
      registrations.add(adapter);
      return () => {
        registrations.delete(adapter);
        retired.push(adapter);
      };
    },
  });
  t.after(() => detach());
  return { ...f, registrations, retired, detach };
}
async function waitFor(predicate) {
  const until = Date.now() + 5000;
  while (!predicate()) {
    assert.ok(Date.now() < until, 'runtime state did not settle');
    await nextTurn();
  }
}
test('runtime Team broker failure leaves inherited identity and model registration independent', async (t) => {
  const f = await models(t, {
    ctx: {
      tools: {
        register() {
          return () => {};
        },
      },
    },
  });
  await waitFor(
    () => f.runtime.status(f.request).communication === 'unavailable',
  );
  assert.ok(f.runtime.resolve(f.request));
  assert.equal(f.registrations.size, 1);
  assert.equal(f.runtime.modelStatus.reauthenticationRequired, false);
  assert.equal(f.runtime.status(f.request).connected, false);
});
for (const state of ['reauth_required', 'revoked', 'signed_out'])
  test(`runtime shell ${state} removes models and all old identity authority`, async (t) => {
    const f = await models(t);
    assert.equal(f.registrations.size, 1);
    f.connection.transition(state);
    assert.equal(f.registrations.size, 0);
    assert.equal(f.runtime.resolve(f.request), undefined);
    assert.equal(
      f.runtime.modelStatus.reauthenticationRequired,
      state === 'reauth_required',
    );
    assert.equal(f.runtime.status(f.request).available, true);
    assert.equal(f.runtime.getAccess, undefined);
    assert.equal(f.runtime.vault.snapshot().credential, undefined);
  });
for (const scenario of ['same_member_revision', 'other_member', 'other_tenant'])
  test(`runtime ${scenario} fences the old adapter and awaits identity isolation before new registration`, async (t) => {
    const f = await models(t),
      old = [...f.registrations][0],
      gate = Promise.withResolvers(),
      entered = Promise.withResolvers();
    const off = f.runtime.vault.subscribeResourceIdentityGate(async () => {
      entered.resolve();
      await gate.promise;
    });
    t.after(off);
    const patch =
      scenario === 'other_member'
        ? { memberId: randomUUID() }
        : scenario === 'other_tenant'
          ? { tenantId: randomUUID() }
          : {};
    f.connection.transition('authenticated', patch);
    await entered.promise;
    assert.equal(f.registrations.size, 0);
    assert.equal(f.runtime.resolve(f.request), undefined);
    assert.throws(() => old.providerInfo('auriga'), { code: 'NO_ADAPTER' });
    gate.resolve();
    await waitFor(() => f.registrations.size === 1);
    assert.notEqual([...f.registrations][0], old);
    assert.equal(
      f.runtime.resolve(f.request).ownerId,
      f.connection.identity.memberId,
    );
  });
for (const failure of ['permission', 'unavailable', 'none'])
  test(`runtime catalog ${failure} never performs plugin token exchange and leaves shell login authoritative`, async (t) => {
    const f = await models(t),
      original = f.connection.ipc.call.bind(f.connection.ipc);
    let calls = 0;
    f.connection.ipc.call = async (message) => {
      if (message.call?.operation !== 'listAiModels') return original(message);
      calls++;
      if (failure !== 'none')
        throw Object.assign(Error('PRIVATE_UPSTREAM_DETAIL'), {
          code:
            failure === 'permission'
              ? 'common.forbidden'
              : 'common.unavailable',
        });
      return {
        requestId: message.requestId,
        operation: message.call.operation,
        output: {
          statusCode: 200,
          headers: { 'content-type': 'application/json' },
          body: {
            object: 'list',
            data: [
              {
                id: 'authorized',
                object: 'model',
                created: 1,
                owned_by: 'auriga',
              },
            ],
          },
        },
      };
    };
    t.mock.method(globalThis, 'fetch', () =>
      assert.fail('no direct platform fetch'),
    );
    const adapter = [...f.registrations][0];
    if (failure === 'none')
      assert.equal((await adapter.listModels('auriga')).length, 1);
    else
      await assert.rejects(
        adapter.listModels('auriga'),
        (error) => !error.message.includes('PRIVATE_UPSTREAM_DETAIL'),
      );
    assert.equal(calls, 1);
    assert.ok(f.runtime.resolve(f.request));
    assert.equal(f.registrations.size, 1);
    assert.equal(
      f.runtime.modelStatus.available,
      false,
      'discovery cannot invent generation capacities or tokenizer',
    );
    await f.rpc('aurigaAuth/logout');
    assert.equal(f.registrations.size, 0);
    assert.equal(f.runtime.resolve(f.request), undefined);
  });
