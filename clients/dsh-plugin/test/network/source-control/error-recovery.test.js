import test from 'node:test';
import assert from 'node:assert/strict';
import { connectionSourceClient } from '../../../src/network/source-control/connection.js';
import { IDENTITY } from '../host/fixtures.js';
import { setup } from './fixtures.js';

// A verified generic ERROR reaches the same type guard as the SEND incident.
// This contract peer does not reproduce the incident's unknown server cause.
function errorReply(result) {
  for (const key of Object.keys(result))
    if (!['app_version', 'request_id', 'server_auth'].includes(key))
      delete result[key];
  Object.assign(result, {
    type: 'error',
    code: 'network.unauthorized',
    scope: 'request',
    retryable: false,
    retry_after: null,
  });
}

test('signed source Register ERROR retains the pending operation; reconciliation can recover its original receipt', async (t) => {
  let reject = true;
  const f = await setup(t, {
    resign: true,
    beforeReply: (request, result) => {
      if (reject && request.type === 'source_register') errorReply(result);
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  const pending = f.store.snapshot().sourceControl;
  const operation = Object.values(pending.operations)[0];
  assert.equal(operation.receipt, null);
  assert.equal(pending.entries['1'].head, null);
  assert.equal(pending.entries['1'].observation, null);
  assert.equal(pending.entries['1'].stopped, false);
  assert.equal(f.requests.length, 1);

  const signal = new AbortController().signal;
  const request = connectionSourceClient({
    transport: f.transport,
    trust: f.trust,
    identity: IDENTITY,
    ready: f.ready,
    signal,
    assertCurrent() {},
    now: f.signer.now,
  });
  await assert.rejects(
    request({ ...f.requests[0], request_id: 'error-probe' }, signal),
    {
      name: 'SessionError',
      code: 'network.protocol_error',
      message: 'source control mismatch',
    },
  );

  reject = false;
  await f.control.refresh('1');
  const recovered = f.store.snapshot().sourceControl;
  assert.deepEqual(Object.keys(recovered.operations), [operation.id]);
  assert.equal(f.requests[2].operation_id, operation.id);
  assert.equal(f.requests[2].mode, 'reconcile');
  assert.deepEqual(recovered.operations[operation.id].body, operation.body);
  assert.equal(recovered.operations[operation.id].receipt.result, 'registered');
  assert.equal(recovered.entries['1'].observation.source.state, 'active');
});

test('signed source Get ERROR clears the prior observation until a fresh verified Get succeeds', async (t) => {
  let reject = false;
  const f = await setup(t, {
    resign: true,
    beforeReply: (request, result) => {
      if (reject && request.type === 'source_get') errorReply(result);
    },
  });
  await f.control.establish('1');
  await f.control.idle();
  const before = f.store.snapshot().sourceControl;
  assert.equal(before.entries['1'].observation.source.state, 'active');

  reject = true;
  await f.control.refresh('1');
  const denied = f.store.snapshot().sourceControl;
  assert.equal(denied.entries['1'].observation, null);
  assert.equal(denied.entries['1'].head, before.entries['1'].head);
  assert.deepEqual(denied.operations, before.operations);
  assert.equal(denied.entries['1'].stopped, false);

  reject = false;
  await f.control.refresh('1');
  const recovered = f.store.snapshot().sourceControl;
  assert.equal(recovered.entries['1'].observation.source.state, 'active');
  assert.equal(recovered.entries['1'].head, before.entries['1'].head);
  const gets = f.requests.filter((request) => request.type === 'source_get');
  assert.notEqual(
    gets[1].native_witness.witness_id,
    gets[2].native_witness.witness_id,
  );
});
