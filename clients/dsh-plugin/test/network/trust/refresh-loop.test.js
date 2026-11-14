import test from 'node:test';
import assert from 'node:assert/strict';
import {
  startTrustRefresh,
  trustRefreshDelay,
} from '../../../src/network/trust/refresh-loop.js';

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

test('normal refresh uses twelve-hour jitter and failures back off within signed expiry', () => {
  assert.equal(
    trustRefreshDelay(86400000, 0, 0, () => 0.5),
    43200000,
  );
  assert.ok(trustRefreshDelay(86400000, 0, 0, () => 0) < 43200000);
  assert.ok(trustRefreshDelay(86400000, 0, 0, () => 1) > 43200000);
  assert.equal(
    trustRefreshDelay(86400000, 0, 1, () => 0.5),
    30000,
  );
  assert.equal(
    trustRefreshDelay(86400000, 0, 2, () => 0.5),
    60000,
  );
  assert.equal(
    trustRefreshDelay(1000, 0, 7, () => 0.5),
    1000,
  );
});
