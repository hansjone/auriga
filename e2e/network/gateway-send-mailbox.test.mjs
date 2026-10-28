import test from 'node:test';
import assert from 'node:assert/strict';
import { sign } from 'node:crypto';
import {
  requestAssemblyMailbox,
  assemblyMailboxTransport,
} from './support/assembly-mailbox.mjs';
import { signingInput } from '../../clients/dsh-plugin/src/network/trust/verifier.js';
import {
  decodeJson,
  encodeJson,
} from '../../clients/dsh-plugin/src/network/session/frames.js';
import { IDENTITY } from '../../clients/dsh-plugin/test/network/host/fixtures.js';
import {
  publicationFixture,
  testRoots,
  signedAnnouncement,
  signedKeyset,
  signedReadyResult,
} from '../../clients/dsh-plugin/test/network/trust/keyset-fixtures.js';

function fixture(t, mode = 'busy') {
  const publication = publicationFixture();
  const abort = new AbortController();
  t.after(() => abort.abort());
  const sent = [],
    listeners = new Set();
  let cached;
  const frame = {
    app_version: '1.0',
    type: 'fetch',
    request_id: 'driver-fetch',
    epoch: '7',
    resume_cursor: '0',
    continuation: null,
    max_items: 1,
    max_bytes: 65536,
    held_ids: [],
    paused: [],
    state_barrier: [],
  };
  const ready = { epoch: '7', channelNonce: 'nonce-test', bootId: 'boot-test' };
  const transport = {
    epoch: 1,
    state: 'ready',
    async send(bytes) {
      sent.push(Buffer.from(bytes));
      const request = decodeJson(bytes);
      let value = signedReadyResult(
        publication,
        { request_id: request.request_id, channel_nonce: ready.channelNonce },
        { epoch: '7' },
      );
      // Model the single ordinary mailbox lane: a production auto-FETCH
      // occupies it for the first probe. The rejected probe is never an RPC.
      const overloaded = mode === 'always' || sent.length === 1;
      if (overloaded) {
        Object.assign(value, {
          type: 'error',
          code: 'network.overloaded',
          scope: 'request',
          retryable: true,
          retry_after: null,
        });
      } else Object.assign(value, { type: 'fetch_page' });
      if (mode === 'foreign') value.server_auth.audience.node = '65';
      value.server_auth.signature = sign(
        null,
        signingInput(value),
        publication.pair.privateKey,
      ).toString('base64url');
      if (mode === 'signature') value.server_auth.signature = 'A'.repeat(86);
      for (const listener of listeners)
        listener(
          encodeJson(
            overloaded
              ? value
              : {
                  app_version: '1.0',
                  type: 'fetch_result',
                  page: value,
                  items: [],
                },
          ),
          1,
        );
      if (mode === 'cancel') abort.abort();
    },
  };
  return {
    sent,
    listeners,
    run: () =>
      requestAssemblyMailbox({
        prepared: {
          roots: testRoots,
          async refresh(trust) {
            await trust.announce(signedAnnouncement(publication.payload));
            await trust.update(signedKeyset(publication.payload));
          },
        },
        cache: {
          async load() {
            return cached;
          },
          async commit(value) {
            cached = structuredClone(value);
          },
        },
        transport: assemblyMailboxTransport(transport, listeners),
        identity: IDENTITY,
        ready,
        frame,
        expected: 'fetch_result',
        signal: abort.signal,
      }),
  };
}

test('SEND-2探针与生产FETCH冲突时验签合法过载，复用同字节后仍须得到真实FETCH结果', async (t) => {
  const f = fixture(t);
  const result = await f.run();
  assert.equal(result.type, 'fetch_result');
  assert.equal(f.sent.length, 2);
  assert.deepEqual(f.sent[0], f.sent[1]);
  assert.equal(f.listeners.size, 0);
});

test('SEND-2探针持续合法过载最多五次，绝不冒成功', async (t) => {
  const f = fixture(t, 'always');
  await assert.rejects(
    f.run(),
    (e) => e.code === 'network.overloaded' && e.attempts === 5,
  );
  assert.equal(f.sent.length, 5);
  assert.ok(f.sent.every((bytes) => bytes.equals(f.sent[0])));
  assert.equal(f.listeners.size, 0);
});

for (const mode of ['signature', 'foreign', 'cancel'])
  test(`SEND-2探针${mode}拒绝重试且清理监听`, async (t) => {
    const f = fixture(t, mode);
    await assert.rejects(f.run());
    assert.equal(f.sent.length, 1);
    assert.equal(f.listeners.size, 0);
  });
