import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';

for (const input of [
  null,
  [],
  { args: [] },
  { args: 'password' },
  { args: { action: 'begin' } },
  { args: { type: 'form', values: { password: 'private' } } },
  { args: {}, endpoint: 'https://other.example.com' },
])
  test(`native login never accepts plugin credentials or arbitrary targets: ${JSON.stringify(input)}`, async (t) => {
    const f = await shellRuntimePeer(t);
    const before = f.connection.calls.length;
    const result = await f.rpc('aurigaAuth/login', input);
    assert.equal(result.ok, false);
    assert.equal(result.error.code, 'common.validation_failed');
    assert.equal(f.connection.calls.length, before);
    assert.ok(f.runtime.resolve(f.request));
  });
