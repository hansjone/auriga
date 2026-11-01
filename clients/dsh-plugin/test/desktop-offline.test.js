import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { installLocalAdmission } from '../src/local/host.js';
import { savedPlatformConfig } from '../src/local/platform.js';

test('native owner local API and WS survive offline cold start without a platform runtime', () => {
  const peer = { principal: { kind: 'owner' } };
  const connection = {
    admit: (req) =>
      req.headers.origin === 'https://evil.example'
        ? { rejection: 403 }
        : req.headers.cookie === 'api=valid'
          ? { peer }
          : { rejection: 401 },
    authorizeIndex: () => false,
    isTrusted: (req) => req.headers.origin !== 'https://evil.example',
  };
  const admission = installLocalAdmission(
    connection,
    {
      isConfigured: () => true,
      resolve: () => null,
      installHttpTracking: () => () => {},
    },
    { protocol: 'http:' },
  );
  try {
    for (const upgrade of [undefined, 'websocket']) {
      const req = {
        method: 'POST',
        url: '/api/session/create',
        headers: { host: 'localhost:3000', cookie: 'api=valid', upgrade },
        rawHeaders: ['Host', 'localhost:3000'],
      };
      assert.equal(connection.admit(req).peer, peer);
      assert.equal(
        connection.admit({
          ...req,
          headers: { ...req.headers, cookie: '', 'x-auriga-native': 'true' },
        }).rejection,
        401,
      );
      assert.equal(
        connection.admit({
          ...req,
          headers: { ...req.headers, origin: 'https://evil.example' },
        }).rejection,
        403,
      );
    }
  } finally {
    admission.dispose();
  }
});

test('Electron startup has no plugin login injection', async () => {
  const source = await readFile(
    new URL('../src/plugin.js', import.meta.url),
    'utf8',
  );
  assert.doesNotMatch(source, /installLocalLoginBootstrap|login\/startup/);
});

test('saved platform configuration can be loaded while discovery is unreachable', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => {
    throw new Error('offline');
  });
  const config = await savedPlatformConfig(
    undefined,
    'https://portal.example.com',
    { saved: true },
  );
  assert.equal(config.platformOrigin, 'https://portal.example.com');
  assert.equal(config.providerId, 'discover');
});
