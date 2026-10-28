import test from 'node:test';
import assert from 'node:assert/strict';
import { createMailboxClient } from '../../src/network/durable/client.js';
import { signingFixture } from './trust/fixtures.js';
import { IDENTITY } from './host/fixtures.js';
import { decodeJson, encodeJson } from '../../src/network/session/frames.js';

async function fixture(t, options = {}) {
  const signer = await signingFixture();
  const abort = new AbortController();
  t.after(() => abort.abort());
  const listeners = new Set(),
    sent = [];
  const transport = {
    epoch: 1,
    state: 'ready',
    async stop() {},
    onMessage(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    async send(bytes) {
      sent.push(Buffer.from(bytes));
      const frame = decodeJson(bytes);
      const value =
        options.always || sent.length === 1
          ? {
              app_version: '1.0',
              type: 'error',
              request_id: frame.request_id,
              code: 'network.overloaded',
              scope: 'request',
              retryable: true,
              retry_after: options.retryAfter ?? null,
            }
          : {
              app_version: '1.0',
              type: 'receive_set_result',
              request_id: frame.request_id,
            };
      const signed = signer.seal(value, {
        ...signer.context,
        requestId: frame.request_id,
        ...(options.audience ?? {}),
      });
      if (options.badSignature) signed.server_auth.signature = 'A'.repeat(86);
      for (const listener of listeners) listener(encodeJson(signed), 1);
      if (options.cancel) abort.abort();
    },
  };
  const client = createMailboxClient({
    transport,
    trust: signer.trust,
    identity: IDENTITY,
    ready: { epoch: '7', bootId: 'boot-test', channelNonce: 'nonce-test' },
    signal: abort.signal,
    assertCurrent() {},
    now: signer.now,
  });
  return { client, abort, sent, listeners };
}
test('经验签瞬时 overloaded 在生产交换内重发原字节', async (t) => {
  const f = await fixture(t);
  const result = await f.client.request(
    f.client.frame('receive_set', {}),
    'receive_set_result',
  );
  assert.equal(result.type, 'receive_set_result');
  assert.equal(f.sent.length, 2);
  assert.deepEqual(f.sent[0], f.sent[1]);
  assert.equal(f.listeners.size, 0);
});
test('持续 overloaded 有界五次，保留明确原因与重试提示', async (t) => {
  const f = await fixture(t, { always: true });
  await assert.rejects(
    f.client.request(f.client.frame('fetch', {}), 'fetch_result'),
    (e) =>
      e.code === 'network.overloaded' &&
      e.attempts === 5 &&
      e.retryable === true,
  );
  assert.equal(f.sent.length, 5);
  assert.ok(f.sent.every((v) => v.equals(f.sent[0])));
  assert.equal(f.listeners.size, 0);
});
for (const [name, options] of [
  ['坏签名', { badSignature: true }],
  ['异受众', { audience: { nodeIncarnation: 'foreign' } }],
  ['取消', { cancel: true }],
  ['原期限容不下 retry_after', { retryAfter: 86400 }],
])
  test(`${name} 不获后续重试`, async (t) => {
    const f = await fixture(t, options);
    await assert.rejects(
      f.client.request(f.client.frame('receive_set', {}), 'receive_set_result'),
    );
    assert.equal(f.sent.length, 1);
    assert.equal(f.listeners.size, 0);
  });
