import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveNetworkTiming } from '../src/network/timing.js';
test('one bounded WAN budget reserves time for inner RPCs, authority calls and reconnect grace', () => {
  const t = resolveNetworkTiming();
  assert.deepEqual(t, {
    operationMs: 30000,
    rpcMs: 20000,
    authorityMs: 5000,
    authMs: 10000,
    redeemMs: 8000,
    graceMs: 15000,
  });
  for (const ms of [10000, 60000, 120000]) {
    const t = resolveNetworkTiming(ms);
    assert.ok(t.authorityMs >= 1000);
    assert.ok(t.rpcMs < t.operationMs);
    assert.ok(t.redeemMs < t.authMs);
  }
  for (const v of [9999, 120001, NaN, '30000', 1.5])
    assert.throws(() => resolveNetworkTiming(v), /budget/);
});
