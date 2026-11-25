import assert from 'node:assert/strict';
import { test } from 'node:test';
import { randomUUID } from 'node:crypto';
import { bindLocalUacProof } from '../src/local/uac/platform-proof.js';

test('backend local proof lease renews, changes immediately and withdraws on close without page exposure', async () => {
  let credential = {
      generation: randomUUID(),
      empNo: '001Ab',
      token: 'synthetic-secret',
    },
    listener,
    tick,
    cleared = false,
    unsubscribed = false;
  const sent = [];
  const identity = {
    proofCredentials: () => credential,
    subscribe(fn) {
      listener = fn;
      return () => {
        unsubscribed = true;
      };
    },
  };
  const stop = bindLocalUacProof({
    identity,
    broker: {
      async localUac(value) {
        sent.push(value);
      },
    },
    setTimer(fn, ms) {
      assert.equal(ms, 10000);
      tick = fn;
      return { unref() {} };
    },
    clearTimer() {
      cleared = true;
    },
  });
  assert.equal(sent.length, 1);
  tick();
  assert.equal(sent.length, 2);
  const original = credential.generation;
  credential = { ...credential, generation: randomUUID(), empNo: '002Cd' };
  listener();
  assert.notEqual(sent.at(-1).generation, original);
  credential = null;
  listener();
  assert.equal(sent.at(-1), null);
  stop();
  assert.equal(cleared, true);
  assert.equal(unsubscribed, true);
  const count = sent.length;
  tick();
  assert.equal(sent.length, count);
});

test('a disconnected platform never changes the valid local identity', async () => {
  const credential = {
    generation: randomUUID(),
    empNo: '001Ab',
    token: 'synthetic-secret',
  };
  let tick;
  const stop = bindLocalUacProof({
    identity: { proofCredentials: () => credential, subscribe: () => () => {} },
    broker: {
      async localUac() {
        throw new Error('offline');
      },
    },
    setTimer(fn) {
      tick = fn;
      return { unref() {} };
    },
    clearTimer() {},
  });
  tick();
  await Promise.resolve();
  assert.equal(credential.empNo, '001Ab');
  stop();
  await Promise.resolve();
});
