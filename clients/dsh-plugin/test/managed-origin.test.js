import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import https from 'node:https';
import { generateKeyPairSync, sign, X509Certificate } from 'node:crypto';
import {
  browserFenceOk,
  validateBrowserOrigin,
} from '../src/managed/host-auth.js';
import { createManaged } from '../src/modes/managed.js';
import { createDoorLock } from '../src/managed/door.js';
import { installHostAuthentication } from '../src/managed/host-auth.js';
import {
  makeIdentityKeys,
  signIdentity,
  INSTANCE_ID,
  OWNER_ID,
} from './managed-helpers.js';

for (const secure of [false, true])
  test(`signed ${secure ? 'TLS' : 'HTTP'} and WS preserve exact Origin`, async (t) => {
    const keys = makeIdentityKeys();
    const door = createDoorLock({
      instanceId: INSTANCE_ID,
      ownerId: OWNER_ID,
      getPublicKey: () => keys.publicKey,
    });
    const connection = {
      requestRejection: () => 401,
      authorizeIndex: () => false,
    };
    const transport = secure ? https : http;
    const tls = secure ? tlsMaterial() : {};
    const handler = (req, res) => {
      res.writeHead(connection.requestRejection(req) ?? 200);
      res.end();
    };
    const server = secure
      ? https.createServer(tls, handler)
      : http.createServer(handler);
    server.on('upgrade', (req, socket) => {
      socket.on('error', () => {});
      const code = connection.requestRejection(req) ?? 101;
      socket.end(`HTTP/1.1 ${code} Test\r\nConnection: close\r\n\r\n`);
    });
    const restore = installHostAuthentication(connection, door, {
      browserOrigin: {
        scheme: 'https',
        host: 'instance.i.example.com',
        port: 443,
      },
    });
    const uninstall = door.install(server);
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    t.after(async () => {
      uninstall();
      restore();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    });
    for (const upgrade of [false, true]) {
      for (const [origin, expected] of [
        ['https://instance.i.example.com', upgrade ? 101 : 200],
        ['http://instance.i.example.com', 403],
        ['https://instance.i.example.com:8443', 403],
        ['https://instance.i.example.com:443', upgrade ? 101 : 200],
        ['https://admin.example.com:443', 403],
        ['http://auriga-instance.internal:8081', 403],
        ['https://other.i.example.com', 403],
        ['null', 403],
        ['https://instance.i.example.com/', 403],
        ['https://user@instance.i.example.com', 403],
        ['https://instance.i.example.com?x=1', 403],
        ['https://instance.i.example.com#fragment', 403],
        ['https://INSTANCE.i.example.com', 403],
        ['https://instance.i.example.com.', 403],
        ['https://instance%2ei.example.com', 403],
        [
          ['https://instance.i.example.com', 'https://instance.i.example.com'],
          403,
        ],
      ]) {
        const token = signIdentity(keys.privateKey, keys.kid, {
          htm: 'GET',
          htu: '/',
        }).token;
        const status = await new Promise((resolve, reject) => {
          const req = transport.request(
            {
              ...(secure ? { ca: tls.cert, servername: 'localhost' } : {}),
              agent: false,
              host: '127.0.0.1',
              port: server.address().port,
              path: '/',
              headers: {
                Origin: origin,
                'Auriga-Identity': token,
                ...(upgrade
                  ? { Connection: 'Upgrade', Upgrade: 'websocket' }
                  : {}),
              },
            },
            (res) => {
              res.resume();
              resolve(res.statusCode);
            },
          );
          req.on('upgrade', (res, socket) => {
            socket.on('error', () => {});
            socket.end();
            resolve(res.statusCode);
          });
          req.on('error', reject);
          req.end();
        });
        assert.equal(status, expected, `${upgrade ? 'WS' : 'HTTP'} ${origin}`);
      }
    }
  });

// Generate a fresh localhost certificate in memory; the client trusts only this
// certificate, verifies the localhost SAN, and never disables TLS verification.
function tlsMaterial() {
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const der = (tag, ...values) => {
    const bytes = Buffer.concat(values);
    const length =
      bytes.length < 128
        ? Buffer.from([bytes.length])
        : Buffer.from(
            bytes.length < 256
              ? [0x81, bytes.length]
              : [0x82, bytes.length >> 8, bytes.length & 255],
          );
    return Buffer.concat([Buffer.from([tag]), length, bytes]);
  };
  const seq = (...v) => der(0x30, ...v);
  const hex = (v) => Buffer.from(v, 'hex');
  const algorithm = seq(hex('06032b6570'));
  const name = seq(
    der(0x31, seq(hex('0603550403'), der(0x0c, Buffer.from('localhost')))),
  );
  const time = (ms) =>
    der(
      0x17,
      Buffer.from(
        new Date(ms).toISOString().slice(2, 19).replace(/[-:T]/g, '') + 'Z',
      ),
    );
  const san = der(
    0xa3,
    seq(
      seq(hex('0603551d11'), der(4, seq(der(0x82, Buffer.from('localhost'))))),
    ),
  );
  const body = seq(
    der(0xa0, hex('020102')),
    hex('020101'),
    algorithm,
    name,
    seq(time(Date.now() - 60000), time(Date.now() + 3600000)),
    name,
    publicKey.export({ format: 'der', type: 'spki' }),
    san,
  );
  const raw = seq(
    body,
    algorithm,
    der(3, Buffer.from([0]), sign(null, body, privateKey)),
  );
  const cert =
    '-----BEGIN CERTIFICATE-----\n' +
    raw
      .toString('base64')
      .match(/.{1,64}/g)
      .join('\n') +
    '\n-----END CERTIFICATE-----\n';
  assert(new X509Certificate(cert).verify(publicKey));
  return { cert, key: privateKey.export({ format: 'pem', type: 'pkcs8' }) };
}

for (const origin of [
  { scheme: 'http', host: '192.0.2.10', port: 80 },
  { scheme: 'http', host: '192.0.2.10', port: 8080 },
  { scheme: 'https', host: 'instance.i.example.com', port: 443 },
  { scheme: 'https', host: 'instance.i.example.com', port: 8443 },
])
  test(`Origin matrix ${JSON.stringify(origin)}`, () => {
    const authority = `${origin.host}:${origin.port}`;
    const req = (headers) => ({
      headers: { host: '127.0.0.1:7860', ...headers },
    });
    assert(
      browserFenceOk(
        req({ origin: `${origin.scheme}://${authority}` }),
        origin,
      ),
    );
    assert(browserFenceOk(req({}), origin));
    assert(browserFenceOk(req({ 'sec-fetch-site': 'none' }), origin));
    assert.equal(
      browserFenceOk(req({ 'sec-fetch-site': 'cross-site' }), origin),
      false,
    );
    assert.equal(
      browserFenceOk(req({ 'sec-fetch-site': ['same-origin'] }), origin),
      false,
    );
    assert.equal(
      browserFenceOk(
        req({ origin: `${origin.scheme}://${origin.host}:${origin.port + 1}` }),
        origin,
      ),
      false,
    );
    assert.equal(
      browserFenceOk(req({ origin: 'http://127.0.0.1:7860' }), origin),
      false,
    );
    assert.equal(
      browserFenceOk(req({ origin: 'https://[::1]:7860' }), origin),
      false,
    );
    assert.equal(
      browserFenceOk(req({ host: 'rebind.example.com' }), origin),
      false,
    );
    assert(browserFenceOk(req({ host: authority }), origin));
    assert.equal(
      browserFenceOk(
        req({ host: `${origin.host}:${origin.port + 1}` }),
        origin,
      ),
      isPublicIp(origin.host),
    );
  });
function isPublicIp(host) {
  return host === '192.0.2.10';
}

for (const browserOrigin of [
  undefined,
  null,
  {},
  [],
  { scheme: 'https', host: 'i.example.com' },
  { scheme: 'https', host: 'i.example.com', port: 0 },
  { scheme: 'https', host: 'i.example.com', port: '443' },
  { scheme: 'https', host: 'i.example.com', port: 65536 },
  { scheme: 'ws', host: 'i.example.com', port: 443 },
  ...[
    'I.example.com',
    'i.example.com.',
    '127.1',
    '2130706433',
    '0x7f000001',
    '192.00.2.10',
    'user@i.example.com',
    'i.example.com/x',
    'i..example.com',
    '%69.example.com',
    '*.example.com',
    'é.example.com',
  ].map((host) => ({ scheme: 'https', host, port: 443 })),
  { scheme: 'https', host: 'i.example.com', port: 443, extra: true },
])
  test(`invalid browserOrigin ${JSON.stringify(browserOrigin)}`, async () => {
    assert.throws(() => validateBrowserOrigin(browserOrigin), /browserOrigin/);
    await assert.rejects(
      createManaged({ config: { browserOrigin } }),
      /browserOrigin/,
    );
  });
test('legacy trustedHosts and unknown fields fail managed startup', async () => {
  for (const extra of [{ trustedHosts: ['i.example.com'] }, { unknown: true }])
    await assert.rejects(
      createManaged({
        config: {
          browserOrigin: { scheme: 'https', host: 'i.example.com', port: 443 },
          ...extra,
        },
      }),
      /unknown managed config/,
    );
});
