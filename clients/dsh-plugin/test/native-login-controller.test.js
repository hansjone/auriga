import test from 'node:test';
import assert from 'node:assert/strict';
import { shellRuntimePeer } from './shell-runtime-peer.js';

test('native sign in delegates only open_login and keeps credentials and CSRF out of RPC', async (t) => {
  const f = await shellRuntimePeer(t);
  const result = await f.rpc('aurigaAuth/login');
  assert.equal(result.ok, true);
  assert.equal(result.value.csrf, undefined);
  assert.equal(result.value.account.subject, f.connection.identity.memberId);
  assert.equal(f.connection.calls.at(-1).action, 'open_login');
  assert.equal(f.runtime.getAccess, undefined);
  assert.equal(f.runtime.vault.snapshot().credential, undefined);
  assert.equal(JSON.stringify(result).includes(f.connection.token()), false);
});
test('native carrier rejects foreign peer, cancelled request and unknown method before broker work', async (t) => {
  const f = await shellRuntimePeer(t);
  const before = f.connection.calls.length;
  const aborted = new AbortController();
  aborted.abort();
  for (const result of [
    await f.rpc('aurigaAuth/status', { args: {} }, {}),
    await f.rpc('aurigaAuth/status', { args: {} }, f.peer, aborted.signal),
    await f.rpc('unknown'),
  ])
    assert.equal(result.ok, false);
  assert.equal(f.connection.calls.length, before);
});
