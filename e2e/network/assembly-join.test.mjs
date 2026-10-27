/* global structuredClone */
import test from 'node:test';
import assert from 'node:assert/strict';
import { sign } from 'node:crypto';
import { joinWithRegistrationRetry } from './support/assembly-join.mjs';
import {
  publicationFixture,
  openTestTrust,
  testRoots,
} from '../../clients/dsh-plugin/test/network/trust/keyset-fixtures.js';
import { signingInput } from '../../clients/dsh-plugin/src/network/trust/verifier.js';

// Driver/signature fixtures only; not remote IAM/Org/native acceptance.
async function fixture({
  reject = (frame) => frame,
  mutate = () => {},
  always = false,
  resign = false,
} = {}) {
  const signer = publicationFixture();
  let cached;
  const trust = await openTestTrust({
    config: signer.payload,
    now: signer.now,
    roots: testRoots,
    cache: {
      async load() {
        return structuredClone(cached);
      },
      async commit(value) {
        cached = structuredClone(value);
      },
    },
  });
  const frames = [],
    evidence = [],
    pauses = [];
  const allocation = {
    opId: 'host:allocation',
    local: '3',
    nonce: 'nonce',
    addressType: 'session',
    expectedRegistrationRevision: '2',
    fingerprint: 'a'.repeat(64),
    purpose: 'bind_existing',
    mappingDigest: 'b'.repeat(64),
    state: 'reserved',
    registrationId: null,
  };
  const state = { locals: { 3: allocation } };
  const context = {
    realm: '2',
    node: '64',
    nodeIncarnation: 'incarnation',
    epoch: '3',
    channelNonce: 'channel',
    bootId: 'boot',
  };
  let calls = 0,
    nativeLive = true;
  return {
    frames,
    state,
    context,
    evidence,
    pauses,
    get calls() {
      return calls;
    },
    stop() {
      nativeLive = false;
    },
    options: {
      frames,
      getState: async () => state,
      getContext: () => ({ ...context }),
      getTrust: () => trust,
      now: signer.now,
      assertNative: async () => {
        assert.equal(nativeLive, true, 'native object retired');
      },
      onAttempt: (event) => evidence.push(event),
      pause: async (ms) => {
        pauses.push(ms);
        mutate({
          allocation,
          context,
          stop: () => {
            nativeLive = false;
          },
        });
      },
      async invoke() {
        calls++;
        const request = {
          app_version: '1.0',
          type: 'local_register',
          request_id: 'request-' + calls,
          epoch: context.epoch,
          boot_id: context.bootId,
          candidate_local: allocation.local,
          allocation_op_id: allocation.opId,
          allocation_fingerprint: 'sha256:' + allocation.fingerprint,
          address_type: allocation.addressType,
          expected_registration_revision:
            allocation.expectedRegistrationRevision,
        };
        frames.push({ direction: 'up', frame: request });
        if (calls > 1 && !always) {
          allocation.state = 'registered';
          allocation.registrationId = 'genuine-result';
          return {
            isError: false,
            value: { status: 'registered', address: 'aur1:2.40.3' },
          };
        }
        const frame = {
          app_version: '1.0',
          type: 'error',
          request_id: request.request_id,
          code: 'network.overloaded',
          scope: 'request',
          retryable: true,
          retry_after: null,
          server_auth: {
            version: 1,
            purpose: 'protocol_response',
            alg: 'Ed25519',
            kid: 'response',
            issuer_realm: context.realm,
            issued_at: signer.payload.issued_at,
            not_after: new Date(signer.now() + 30000)
              .toISOString()
              .replace('.000Z', 'Z'),
            audience: {
              realm: context.realm,
              node: context.node,
              incarnation: context.nodeIncarnation,
              epoch: context.epoch,
            },
            request_id: request.request_id,
            channel_nonce: context.channelNonce,
            keyset_version: signer.payload.keyset_version,
          },
        };
        frame.server_auth.signature = sign(
          null,
          signingInput(frame),
          signer.pair.privateKey,
        ).toString('base64url');
        const response = reject(frame);
        if (resign && response?.server_auth)
          response.server_auth.signature = sign(
            null,
            signingInput(response),
            signer.pair.privateKey,
          ).toString('base64url');
        frames.push({ direction: 'down', frame: response });
        return { isError: false, value: { status: 'unavailable' } };
      },
    },
  };
}

test('real tool retry requires verified overload and preserves complete C/allocation intent', async () => {
  const f = await fixture();
  const original = structuredClone(f.state.locals['3']);
  assert.equal(
    (await joinWithRegistrationRetry(f.options)).status,
    'registered',
  );
  assert.equal(f.calls, 2);
  assert.deepEqual(f.pauses, [500]);
  const requests = f.frames
    .filter((event) => event.direction === 'up')
    .map((event) => event.frame);
  assert.notEqual(requests[0].request_id, requests[1].request_id);
  assert.deepEqual(
    { ...requests[0], request_id: null },
    { ...requests[1], request_id: null },
  );
  for (const field of Object.keys(original).filter(
    (field) => !['state', 'registrationId'].includes(field),
  ))
    assert.deepEqual(f.state.locals['3'][field], original[field]);
  assert.equal(f.evidence[0].retryVerified, true);
});

test('unsigned, wrong-context, nonretryable and unrelated errors cannot trigger a second tool call', async () => {
  for (const reject of [
    () => null,
    (frame) => {
      delete frame.server_auth;
      return frame;
    },
    (frame) => {
      frame.server_auth.signature = 'A'.repeat(86);
      return frame;
    },
    (frame) => ({ ...frame, request_id: 'another-request' }),
    (frame) => ({ ...frame, code: 'network.outcome_unknown' }),
    (frame) => ({ ...frame, retryable: false }),
    (frame) => {
      frame.server_auth.audience.node = '65';
      return frame;
    },
  ]) {
    const f = await fixture({ reject });
    await assert.rejects(joinWithRegistrationRetry(f.options));
    assert.equal(f.calls, 1);
    assert.deepEqual(f.pauses, []);
  }
});

test('even valid signatures for another node, epoch, nonce or expired response cannot authorize retry', async () => {
  for (const reject of [
    (frame) => {
      frame.server_auth.audience.node = '65';
      return frame;
    },
    (frame) => {
      frame.server_auth.audience.epoch = '4';
      return frame;
    },
    (frame) => {
      frame.server_auth.channel_nonce = 'another-channel';
      return frame;
    },
    (frame) => {
      frame.server_auth.not_after = new Date(
        Date.parse(frame.server_auth.issued_at) + 1000,
      )
        .toISOString()
        .replace('.000Z', 'Z');
      return frame;
    },
  ]) {
    const f = await fixture({ reject, resign: true });
    await assert.rejects(joinWithRegistrationRetry(f.options), {
      code: 'network.signature_invalid',
    });
    assert.equal(f.calls, 1);
    assert.deepEqual(f.pauses, []);
  }
});

test('native retirement, allocation/body change or a changed READY context aborts retry', async () => {
  for (const mutate of [
    ({ stop }) => stop(),
    ({ allocation }) => {
      allocation.nonce = 'replacement';
    },
    ({ allocation }) => {
      allocation.fingerprint = 'c'.repeat(64);
    },
    ({ context }) => {
      context.epoch = '4';
    },
    ({ context }) => {
      context.channelNonce = 'replacement';
    },
  ]) {
    const f = await fixture({ mutate });
    await assert.rejects(joinWithRegistrationRetry(f.options));
    assert.equal(f.calls, 1);
  }
});

test('five signed overload refusals remain a registration failure without new allocation', async () => {
  const f = await fixture({ always: true });
  await assert.rejects(
    joinWithRegistrationRetry(f.options),
    /typed-local production registration failed/,
  );
  assert.equal(f.calls, 5);
  assert.deepEqual(f.pauses, [500, 500, 500, 500]);
  assert.equal(f.state.locals['3'].state, 'reserved');
});
