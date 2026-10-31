import test from 'node:test';
import assert from 'node:assert/strict';
import { createStableNetworkTools } from '../src/network/stable-tools.js';
test('a published network tool survives transient retirement and delegates only to the current generation', async () => {
  const registered = [];
  let removed = 0;
  const registry = createStableNetworkTools({
    register(def) {
      registered.push(def);
      return () => removed++;
    },
  });
  const first = registry.register({
    name: 'auriga_network_join',
    execute: () => ({ status: 'registered', address: 'aur1:1.2.3' }),
  });
  assert.equal((await registered[0].execute({}, {})).status, 'registered');
  first();
  assert.equal(removed, 0);
  assert.deepEqual(await registered[0].execute({}, {}), {
    status: 'unavailable',
    reason: 'network.overloaded',
    retryable: true,
  });
  registry.register({
    name: 'auriga_network_join',
    execute: () => ({ status: 'registered', address: 'aur1:1.2.4' }),
  });
  first();
  assert.equal(registered.length, 1);
  assert.equal((await registered[0].execute({}, {})).address, 'aur1:1.2.4');
  registry.close();
  assert.equal(removed, 1);
  assert.deepEqual(await registered[0].execute({}, {}), {
    status: 'unavailable',
    retryable: false,
  });
});
test('an in-flight old generation cannot return success after disconnect, and a send does not bypass revocation', async () => {
  const defs = [];
  const registry = createStableNetworkTools({
    register(def) {
      defs.push(def);
      return () => {};
    },
  });
  let release;
  const retire = registry.register({
    name: 'auriga_send_message',
    execute: () => new Promise((r) => (release = r)),
  });
  const pending = defs[0].execute({}, {});
  retire();
  release({ status: 'request_submitted' });
  assert.deepEqual(await pending, {
    status: 'error',
    code: 'network.overloaded',
    retryable: true,
  });
  registry.close();
  assert.deepEqual(await defs[0].execute({}, {}), {
    status: 'error',
    code: 'network.unauthorized',
    retryable: false,
  });
  assert.throws(
    () => registry.register({ name: 'auriga_network_join' }),
    /closed/,
  );
});
