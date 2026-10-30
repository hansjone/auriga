import assert from 'node:assert/strict';
import test from 'node:test';
import { createNetworkSession } from '../../../src/network/session/session.js';
import { decodeJson, encodeJson } from '../../../src/network/session/frames.js';
import { signingFixture } from '../trust/fixtures.js';
import { IDENTITY } from '../host/fixtures.js';

const intent = {
  candidate_local: '1',
  allocation_op_id: 'original-op',
  allocation_fingerprint: 'sha256:' + 'a'.repeat(64),
  address_type: 'subagent',
  expected_registration_revision: '0',
};

// Fake byte transport with real Ed25519 signatures. Mutations never bypass the
// production session's verification, request correlation or RegisterResult check.
async function register(
  t,
  { fields = {}, auth = {}, afterSeal = () => {} } = {},
) {
  const signer = await signingFixture();
  let receive, nonce;
  const session = createNetworkSession({
    identity: IDENTITY,
    adapter: {},
    trust: signer.trust,
    now: signer.now,
    transport: {
      onMessage(handler) {
        receive = handler;
        return () => {};
      },
      async send(bytes) {
        const request = decodeJson(bytes);
        assert.equal(request.app_version, '1.0');
        const ready = request.type === 'ready';
        if (ready) nonce = request.channel_nonce;
        const result = ready
          ? {
              app_version: '1.0',
              type: 'ready_result',
              request_id: request.request_id,
              status: 'ready',
              epoch: '7',
              required_features: [],
              node_identity: {
                realm: '2',
                node: '64',
                incarnation: IDENTITY.nodeIncarnation,
              },
            }
          : {
              app_version: '1.0',
              type: 'register_result',
              request_id: request.request_id,
              local: request.candidate_local,
              allocation_op_id: request.allocation_op_id,
              address_type: request.address_type,
              registration_id: 'original-registration',
              result: 'registered',
              registration_revision: '1',
              local_high_water: '1',
              ...fields,
            };
        signer.seal(result, {
          ...signer.context,
          requestId: request.request_id,
          channelNonce: nonce,
          epoch: '7',
          ...(!ready ? auth : {}),
        });
        if (!ready) afterSeal(result);
        receive(encodeJson(result));
      },
    },
  });
  t.after(() => session.stop());
  await session.start();
  return session.request('local_register', intent, 'register_result');
}

for (const fields of [
  { app_version: '1.1' },
  { optional_server_notice: 'ignored informational field' },
  { app_version: '1.9999', optional_server_notice: 'not an execution grant' },
]) {
  test(`signed compatible RegisterResult completes original request: ${JSON.stringify(fields)}`, async (t) => {
    const result = await register(t, { fields });
    assert.equal(result.allocation_op_id, intent.allocation_op_id);
    assert.equal(result.local, intent.candidate_local);
    assert.equal(result.address_type, intent.address_type);
    assert.equal(result.registration_id, 'original-registration');
    assert.equal(result.registration_revision, '1');
    assert.equal(result.local_high_water, '1');
    for (const [key, value] of Object.entries(fields))
      assert.equal(result[key], value);
    assert.equal('epoch' in result, false);
    assert.equal('boot_id' in result, false);
    assert.equal('allocation_fingerprint' in result, false);
  });
}

for (const app_version of [
  '2.0',
  '0.1',
  '1',
  '1.01',
  '1.0.1',
  '1.10000',
  '',
  null,
  1,
]) {
  test(`signed invalid or unsupported registration version ${JSON.stringify(app_version)} is rejected`, async (t) => {
    await assert.rejects(register(t, { fields: { app_version } }), {
      code: 'network.unsupported_app_version',
    });
  });
}

for (const fields of [
  { address_type: undefined },
  { address_type: 'session' },
  { allocation_op_id: 'another-op' },
  { local: '2' },
  { registration_id: '' },
  { registration_revision: '0' },
  { registration_revision: '2' },
  { registration_revision: '01' },
  { registration_revision: 1 },
  { local_high_water: '0' },
  { local_high_water: '01' },
  { local_high_water: 1 },
  { result: 'unknown' },
]) {
  test(`compatible signed extension cannot bypass original fields: ${JSON.stringify(fields)}`, async (t) => {
    await assert.rejects(
      register(t, {
        fields: {
          app_version: '1.1',
          optional_server_notice: 'ignored',
          ...fields,
        },
      }),
      { code: 'network.protocol_error' },
    );
  });
}

for (const auth of [
  { nodeIncarnation: 'another-incarnation' },
  { channelNonce: 'another-nonce' },
  { requestId: 'another-request' },
]) {
  test(`valid signature with incompatible request binding ${JSON.stringify(auth)} is rejected`, async (t) => {
    await assert.rejects(
      register(t, {
        fields: {
          app_version: '1.1',
          optional_server_notice: 'ignored',
        },
        auth,
      }),
      { code: 'network.signature_invalid' },
    );
  });
}

for (const key of ['optional_server_notice', 'address_type']) {
  test(`tampered ${key} without re-signing is rejected before confirmation`, async (t) => {
    await assert.rejects(
      register(t, {
        fields: {
          app_version: '1.1',
          optional_server_notice: 'signed information',
        },
        afterSeal(result) {
          result[key] = 'tampered';
        },
      }),
      { code: 'network.signature_invalid' },
    );
  });
}

test('compatible extension without server signature is rejected', async (t) => {
  await assert.rejects(
    register(t, {
      fields: {
        app_version: '1.1',
        optional_server_notice: 'unsigned information',
      },
      afterSeal(result) {
        delete result.server_auth;
        result.epoch = '7';
      },
    }),
    { code: 'network.signature_required' },
  );
});
