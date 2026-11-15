import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { revokeLegacyCredentials } from '../src/local/legacy-revocation.js';

test('migration revokes active and queued legacy tokens through the bound issuer without browser credentials', async (t) => {
  const current = randomUUID(),
    queued = randomUUID(),
    calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    assert.equal(
      String(url),
      'https://platform.example.com/realm/oauth2/revoke',
    );
    assert.equal(init.redirect, 'error');
    assert.equal(init.credentials, 'omit');
    assert.equal(init.headers.authorization, undefined);
    const body = new URLSearchParams(init.body);
    assert.equal(body.get('client_id'), 'auriga-dsh');
    calls.push(body.get('token'));
    return Response.json({});
  });
  const pending = await revokeLegacyCredentials(
    {
      credential: {
        refreshToken: current,
        platformOrigin: 'https://platform.example.com',
        issuer: 'https://platform.example.com/realm',
        clientId: 'auriga-dsh',
      },
      revocations: [{ refreshToken: queued }],
    },
    {},
  );
  assert.deepEqual(pending, []);
  assert.deepEqual(calls.sort(), [current, queued].sort());
});
test('failed revocations retain their encrypted-queue input for the next startup', async (t) => {
  const row = {
    refreshToken: randomUUID(),
    platformOrigin: 'https://platform.example.com',
    issuer: 'https://platform.example.com',
    clientId: 'auriga-dsh',
  };
  t.mock.method(globalThis, 'fetch', async () =>
    Response.json({}, { status: 503 }),
  );
  const pending = await revokeLegacyCredentials(
    { legacyRevocations: [row] },
    {},
  );
  assert.deepEqual(pending, [row]);
});
