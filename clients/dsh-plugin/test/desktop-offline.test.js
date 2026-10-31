import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';
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

test('Electron startup never redirects to a platform login even when offline', async () => {
  const script = await readFile(
    new URL('../src/login/startup.js', import.meta.url),
    'utf8',
  );
  const navigations = [];
  await runInNewContext(script, {
    location: {
      protocol: 'dsh-app:',
      replace: (path) => navigations.push(path),
    },
    fetch: async () => ({
      ok: true,
      json: async () => ({
        platformOrigin: 'https://portal.example.com',
        result: { ok: true, value: { loggedIn: false } },
      }),
    }),
    Date,
    setTimeout,
  });
  assert.deepEqual(navigations, []);
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
