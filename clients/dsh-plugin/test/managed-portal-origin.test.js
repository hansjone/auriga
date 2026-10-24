import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createManaged } from '../src/modes/managed.js';
import {
  makeIdentityKeys,
  signIdentity,
  signPanelManifest,
  panelManifestBase,
  INSTANCE_ID,
  OWNER_ID,
} from './managed-helpers.js';

const browserOrigin = {
  scheme: 'https',
  host: 'instance.i.example.com',
  port: 443,
};
const portal = { scheme: 'https', host: 'portal.example.com', port: 443 };

// Exercise the installed door and native host authentication without a listener.
async function bootstrapHost(t, config) {
  const requestOrigin = `${config.browserOrigin.scheme}://${config.browserOrigin.host}:${config.browserOrigin.port}`;
  const keys = makeIdentityKeys();
  const manifest = signPanelManifest(
    keys.privateKey,
    keys.kid,
    panelManifestBase(),
  );
  let manifestFetches = 0;
  const runtime = await createManaged({
    config,
    env: {
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_PLATFORM_URL: 'http://auriga-instance.internal:8081',
      AURIGA_PORTAL_ORIGIN: 'https://untrusted.example.com',
    },
    identityKeys: keys.keySet,
    panelKeys: keys.keySet,
    watchKeys: false,
    startHeartbeat: false,
    fetchImpl: async (url) => {
      assert.equal(
        String(url),
        'http://auriga-instance.internal:8081/panel/manifest.json',
      );
      manifestFetches++;
      return new Response(JSON.stringify(manifest));
    },
  });
  const server = new EventEmitter();
  let handler;
  const dispose = runtime.apply({
    webServer: {
      server,
      register(route) {
        if (route.path === '/auriga') handler = route.handler;
      },
    },
    connection: { requestRejection: () => 401 },
    effect: (fn) => fn(),
    provide() {},
    inject() {},
  });
  server.on('request', (req, res) => handler(req, res));
  t.after(() => {
    dispose();
    runtime.close();
  });
  const request = ({
    signed = true,
    claims = {},
    headers = {},
    url = '/auriga/bootstrap',
    method = 'GET',
    token,
    forged = {},
  } = {}) => {
    const identity =
      token ??
      (signed
        ? signIdentity(keys.privateKey, keys.kid, {
            htm: method,
            htu: url.split('?')[0],
            ...claims,
          }).token
        : undefined);
    return new Promise((resolve) => {
      const response = { status: 0, headers: {}, body: '' };
      server.emit(
        'request',
        {
          method,
          url,
          headers: {
            host: '127.0.0.1:7860',
            origin: requestOrigin,
            ...(identity ? { 'auriga-identity': identity } : {}),
            ...headers,
          },
          ...forged,
        },
        {
          writeHead(status, responseHeaders = {}) {
            response.status = status;
            response.headers = responseHeaders;
          },
          end(body = '') {
            response.body = body;
            resolve(response);
          },
        },
      );
    });
  };
  return { request, keys, manifest, fetches: () => manifestFetches };
}

for (const host of ['portal.example.com', '192.0.2.10'])
  for (const scheme of ['http', 'https'])
    for (const port of [scheme === 'http' ? 80 : 443, 8443, 65535])
      test(`authenticated bootstrap preserves portal ${scheme}/${host}/${port}`, async (t) => {
        const portalOrigin = { scheme, host, port };
        const config = {
          browserOrigin: {
            scheme,
            host: host === '192.0.2.10' ? host : browserOrigin.host,
            port,
          },
          portalOrigin: { ...portalOrigin },
        };
        const app = await bootstrapHost(t, config);
        config.portalOrigin.host = 'mutated.example.com';
        config.portalOrigin = { ...portal, port: 1234 };
        const result = await app.request({
          url: '/auriga/bootstrap?portalOrigin=https://evil.example.com',
          headers: {
            'x-forwarded-host': 'evil.example.com',
            'x-forwarded-proto': 'http',
            'portal-origin': 'https://evil.example.com',
          },
        });
        assert.equal(result.status, 200);
        assert.equal(result.headers['cache-control'], 'no-store');
        const body = JSON.parse(result.body);
        assert.deepEqual(body.portalOrigin, portalOrigin);
        assert.equal(body.platformUrl, null);
        assert.deepEqual(Object.keys(body).sort(), [
          'fetchBase',
          'manifest',
          'mode',
          'panelBase',
          'platformUrl',
          'pluginVersion',
          'portalOrigin',
          'protocol',
        ]);
        assert.deepEqual(body.manifest, app.manifest);
        assert.equal(result.body.includes('auriga-instance.internal'), false);
        assert.equal(result.body.includes('untrusted.example.com'), false);
        assert.equal(app.fetches(), 1);
      });

test('missing portalOrigin stays absent despite internal URL and request hints', async (t) => {
  const app = await bootstrapHost(t, { browserOrigin });
  const result = await app.request({
    url: '/auriga/bootstrap?portalOrigin=https://evil.example.com',
    headers: { 'x-forwarded-host': 'portal.example.com' },
  });
  assert.equal(result.status, 200);
  assert.equal('portalOrigin' in JSON.parse(result.body), false);
  assert.equal(result.body.includes('auriga-instance.internal'), false);
});

test('bootstrap requires door signature, owner/path binding and unchanged browser fence', async (t) => {
  const app = await bootstrapHost(t, { browserOrigin, portalOrigin: portal });
  for (const input of [
    { signed: false },
    { signed: false, forged: { __aurigaIdentity: { sub: OWNER_ID } } },
    { claims: { sub: 'other-owner' } },
    { claims: { aud: 'other-instance' } },
    { claims: { htu: '/other-path' } },
    { claims: { exp: 1 } },
    { token: 'invalid-signature' },
  ]) {
    const result = await app.request(input);
    assert.equal(result.status, 401);
    assert.equal(JSON.parse(result.body).code, 'common.unauthenticated');
    assert.equal(result.body.includes('portalOrigin'), false);
  }
  for (const origin of [
    'https://portal.example.com',
    'https://evil.example.com',
    'http://instance.i.example.com',
    'https://instance.i.example.com:8443',
    'null',
  ]) {
    const result = await app.request({ headers: { origin } });
    assert.equal(result.status, 403);
    assert.equal(result.body.includes('portalOrigin'), false);
  }
  assert.equal(app.fetches(), 0);
  assert.equal((await app.request({ method: 'POST' })).status, 405);
  assert.equal(app.fetches(), 0);
  const token = signIdentity(app.keys.privateKey, app.keys.kid, {
    htm: 'GET',
    htu: '/auriga/bootstrap',
  }).token;
  assert.equal((await app.request({ token })).status, 200);
  const replay = await app.request({ token });
  assert.equal(replay.status, 401);
  assert.equal(JSON.parse(replay.body).code, 'common.unauthenticated');
  assert.equal(app.fetches(), 1);
});

for (const portalOrigin of [
  null,
  {},
  [],
  'https://portal.example.com',
  { scheme: 'https', host: 'portal.example.com' },
  ...[
    { scheme: 'ws' },
    { port: 0 },
    { port: 65536 },
    { port: 1.5 },
    { port: '443' },
    { extra: true },
    ...[
      'Portal.example.com',
      'portal.example.com.',
      '127.1',
      '2130706433',
      '0x7f000001',
      '192.00.2.10',
      'user@portal.example.com',
      'portal.example.com/x',
      'portal.example.com?q',
      'portal.example.com#f',
      '%70ortal.example.com',
      '*.example.com',
      'é.example.com',
      'https://portal.example.com',
      'portal..example.com',
      '[::1]',
    ].map((host) => ({ host })),
  ].map((patch) => ({ ...portal, ...patch })),
])
  test(`managed startup rejects invalid portalOrigin ${JSON.stringify(portalOrigin)}`, async () => {
    await assert.rejects(
      createManaged({ config: { browserOrigin, portalOrigin } }),
      /config\.portalOrigin requires canonical scheme, host and effective port/,
    );
  });

test('managed config rejects dotted portal fields and extra top-level fields', async () => {
  for (const extra of [
    { 'portalOrigin.host': 'evil.example.com' },
    { portalUrl: 'https://evil.example.com' },
  ])
    await assert.rejects(
      createManaged({
        config: { browserOrigin, portalOrigin: portal, ...extra },
      }),
      /unknown managed config field/,
    );
});
