import test from 'node:test';
import assert from 'node:assert/strict';
import { startTrustRefresh } from '../../../src/network/trust/refresh-loop.js';

test(
  'refresh failure cannot extend the original signed deadline',
  { timeout: 1000 },
  async (t) => {
    const keepAlive = setTimeout(() => {}, 1000);
    t.after(() => clearTimeout(keepAlive));
    let clock = 0,
      attempts = 0;
    let expired;
    const done = new Promise((resolve) => {
      expired = resolve;
    });
    const stop = startTrustRefresh({
      now: () => clock,
      expiresAt: () => 1,
      refresh: async () => {
        attempts++;
        clock = 1;
        throw Error('offline');
      },
      onExpired: () => expired(),
    });
    t.after(stop);
    await done;
    assert.equal(attempts, 1);
  },
);
test(
  'stopping during an in-flight refresh prevents expiry callback and rescheduling',
  { timeout: 1000 },
  async (t) => {
    const keepAlive = setTimeout(() => {}, 1000);
    t.after(() => clearTimeout(keepAlive));
    let stop,
      expired = 0,
      completed;
    const done = new Promise((resolve) => {
      completed = resolve;
    });
    stop = startTrustRefresh({
      now: () => 1,
      expiresAt: () => 1,
      refresh: async () => {
        stop();
        completed();
      },
      onExpired: () => expired++,
    });
    await done;
    await Promise.resolve();
    assert.equal(expired, 0);
  },
);
