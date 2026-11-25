import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fixture, command, OWNER } from './fixtures.mjs';

function setup() {
  const f = fixture();
  const requests = [];
  let fail,
    mfa = false,
    exchange = 0;
  const transactionId = randomUUID(),
    id = randomUUID();
  f.platform.config.nativeClientId = 'auriga-dsh-native';
  f.platform.loginSession =
    () =>
    async (path, options = {}) => {
      requests.push({ path, ...options });
      if (fail) throw fail;
      if (options.method === 'DELETE') return {};
      if (path.endsWith('/providers'))
        return {
          items: [
            {
              id: f.identity.providerId,
              connectorType: 'uac',
              displayName: { en: 'UAC' },
            },
          ],
        };
      if (path.endsWith('/challenges'))
        return {
          id,
          transactionId,
          state: 'verified',
          expiresAt: new Date(Date.now() + 120000).toISOString(),
          nativeDevice: {
            deviceCode: 'synthetic-code',
            expiresAt: new Date(Date.now() + 300000).toISOString(),
          },
        };
      if (path.endsWith('/complete'))
        return {
          transactionId,
          state: mfa ? 'additional_step_required' : 'authenticated',
        };
      if (path.endsWith('/advance')) mfa = false;
      return {
        id: transactionId,
        status: mfa ? 'pending' : 'completed',
        step: mfa
          ? {
              type: 'form',
              formId: 'platform_totp',
              fields: [{ name: 'otp', kind: 'otp', required: true }],
            }
          : { type: 'complete', redirectTo: '/' },
      };
    };
  f.platform.nativePoll = async () => {
    exchange++;
    return { ...f.tokens, client_id: 'auriga-dsh-native' };
  };
  const credential = {
    generation: randomUUID(),
    empNo: '001Ab',
    token: 'synthetic-uac-secret',
  };
  const publish = (value = credential) => f.broker.localUac(value, OWNER);
  return {
    ...f,
    requests,
    credential,
    publish,
    fail(value) {
      fail = value;
    },
    mfa() {
      mfa = true;
    },
    exchanges: () => exchange,
  };
}

test('local proof silently establishes one PKCE device and repeated checks reuse refresh', async () => {
  const f = setup();
  await f.publish();
  await f.broker.check();
  assert.equal(f.broker.snapshot().state, 'authenticated');
  assert.equal(f.exchanges(), 1);
  const creation = f.requests.find((r) => r.path.endsWith('/challenges'));
  assert.equal(creation.body.uacProof.empNo, '001Ab');
  assert.equal(creation.body.uacProof.token, f.credential.token);
  assert.match(creation.body.uacProof.nonce, /^[\w-]{43}$/);
  assert.match(creation.body.nativeConnect.codeChallenge, /^[\w-]{43}$/);
  for (let i = 0; i < 5; i++) {
    await f.publish();
    await f.broker.check();
  }
  assert.equal(f.exchanges(), 1);
  for (const value of [f.saved, f.logs, f.broker.snapshot(), f.opened])
    assert.ok(!JSON.stringify(value).includes(f.credential.token));
  await f.broker.close();
});
