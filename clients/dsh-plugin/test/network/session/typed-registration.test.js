import assert from 'node:assert/strict';
import test from 'node:test';
import {
  encodeLocalRegister,
  encodeReady,
  decodeJson,
  encodeJson,
  assertRegisterResult,
} from '../../../src/network/session/frames.js';
import { createNetworkSession } from '../../../src/network/session/session.js';
import { signingFixture } from '../trust/fixtures.js';
import { IDENTITY } from '../host/fixtures.js';

const input = {
  requestId: 'req-register',
  epoch: '7',
  bootId: 'boot-local',
  candidate_local: '1',
  allocation_op_id: 'op-local',
  allocation_fingerprint: 'sha256:' + 'a'.repeat(64),
  expected_registration_revision: '0',
  address_type: 'subagent',
};
const pending = {
  candidate_local: input.candidate_local,
  allocation_op_id: input.allocation_op_id,
  allocation_fingerprint: input.allocation_fingerprint,
  address_type: input.address_type,
};
const ready = {
  requestId: 'req-ready',
  channelNonce: 'nonce',
  resumeCursor: '0',
  localHighWater: '1',
  registrationRevision: '0',
  receiptCheckpoint: {
    checkpoint_seq: '0',
    checkpoint_revision: '0',
    summary_digest: 'sha256:' + '0'.repeat(64),
  },
};
const response = (request) => ({
  app_version: '1.0',
  type: 'register_result',
  request_id: request.request_id,
  local: request.candidate_local,
  allocation_op_id: request.allocation_op_id,
  address_type: request.address_type,
  registration_id: 'reg-local',
  result: 'registered',
  registration_revision: '1',
  local_high_water: '1',
});

test('pure formal frames preserve original pending type and allocation fingerprint', () => {
  const frame = decodeJson(encodeLocalRegister(input));
  assert.equal(frame.address_type, 'subagent');
  assert.equal(frame.allocation_fingerprint, pending.allocation_fingerprint);
  assert.deepEqual(
    {
      ...decodeJson(encodeReady({ ...ready, pendingRegistration: pending }))
        .pending_registration,
    },
    pending,
  );
  assert.doesNotThrow(() => assertRegisterResult(response(frame), frame));
});

for (const address_type of [undefined, '', 'system', null, false]) {
  test(`mixed version invalid type ${address_type} cannot produce READY or registration`, () => {
    assert.throws(() => encodeLocalRegister({ ...input, address_type }), {
      code: 'network.protocol_error',
    });
    assert.throws(
      () =>
        encodeReady({
          ...ready,
          pendingRegistration: { ...pending, address_type },
        }),
      { code: 'network.protocol_error' },
    );
  });
}

for (const field of [
  'address_type',
  'allocation_op_id',
  'local',
  'registration_revision',
  'local_high_water',
  'registration_id',
  'result',
  'app_version',
  'type',
]) {
  for (const mode of ['missing', 'altered']) {
    test(`signed fake broker ${mode} ${field} cannot complete registration request`, async (t) => {
      const signer = await signingFixture();
      let listener, nonce, lastRequest;
      const transport = {
        onMessage(handler) {
          listener = handler;
          return () => {};
        },
        async send(bytes) {
          const request = decodeJson(bytes);
          lastRequest = request;
          let result;
          if (request.type === 'ready') {
            nonce = request.channel_nonce;
            result = {
              app_version: '1.0',
              type: 'ready_result',
              request_id: request.request_id,
              status: 'ready',
              epoch: '7',
              node_identity: {
                realm: '2',
                node: '64',
                incarnation: IDENTITY.nodeIncarnation,
              },
              required_features: [],
            };
          } else {
            result = response(request);
            if (mode === 'missing') delete result[field];
            else
              result[field] =
                field === 'address_type'
                  ? 'session'
                  : field === 'registration_id'
                    ? ''
                    : 'wrong';
          }
          signer.seal(result, {
            ...signer.context,
            requestId: request.request_id,
            channelNonce: nonce,
            epoch: '7',
          });
          listener(encodeJson(result));
        },
      };
      const session = createNetworkSession({
        transport,
        adapter: {},
        identity: IDENTITY,
        trust: signer.trust,
        now: signer.now,
      });
      t.after(() => session.stop());
      await session.start();
      // A bad response type has no matching dispatch branch and times out;
      // the pure validator covers it without introducing a five-second wait.
      if (field === 'type') {
        const request = decodeJson(encodeLocalRegister(input));
        const result = response(request);
        if (mode === 'missing') delete result.type;
        else result.type = 'wrong';
        assert.throws(() => assertRegisterResult(result, request), {
          code: 'network.protocol_error',
        });
      } else {
        await assert.rejects(
          session.request('local_register', input, 'register_result'),
          {
            code:
              field === 'app_version'
                ? 'network.unsupported_app_version'
                : 'network.protocol_error',
          },
        );
        assert.equal(lastRequest.address_type, 'subagent');
        assert.equal(
          lastRequest.allocation_fingerprint,
          input.allocation_fingerprint,
        );
      }
    });
  }
}

test('valid signed fake registration result returns original type', async (t) => {
  const signer = await signingFixture();
  let listener, nonce;
  const session = createNetworkSession({
    identity: IDENTITY,
    adapter: {},
    trust: signer.trust,
    now: signer.now,
    transport: {
      onMessage(handler) {
        listener = handler;
        return () => {};
      },
      async send(bytes) {
        const request = decodeJson(bytes);
        let result;
        if (request.type === 'ready') {
          nonce = request.channel_nonce;
          result = {
            app_version: '1.0',
            type: 'ready_result',
            request_id: request.request_id,
            status: 'ready',
            epoch: '7',
            node_identity: {
              realm: '2',
              node: '64',
              incarnation: IDENTITY.nodeIncarnation,
            },
            required_features: [],
          };
        } else result = response(request);
        signer.seal(result, {
          ...signer.context,
          requestId: request.request_id,
          channelNonce: nonce,
          epoch: '7',
        });
        listener(encodeJson(result));
      },
    },
  });
  t.after(() => session.stop());
  await session.start();
  assert.equal(
    (await session.request('local_register', input, 'register_result'))
      .address_type,
    'subagent',
  );
});
