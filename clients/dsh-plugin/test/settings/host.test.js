import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createServer, request as httpRequest } from 'node:http';
import {
  createSettingsHandler,
  installSettings,
} from '../../src/settings/host.js';
import { createSettingsStore } from '../../src/settings/store.js';

test('plain HTTP ingress accepts trusted external HTTPS origin, never forwarded headers or invalid CSRF/native auth', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-settings-proxy-'));
  let server;
  try {
    const store = await createSettingsStore(dir);
    const handler = createSettingsHandler({
      store,
      activeOrigin: '',
      uiOrigin: 'https://instance.example.com:8443',
      auth: {
        requestRejection: (req) =>
          req.headers['x-native-test'] === 'valid' ? undefined : 401,
      },
    });
    const directHandler = createSettingsHandler({
      store,
      activeOrigin: '',
      auth: {
        requestRejection: (req) =>
          req.headers['x-native-test'] === 'valid' ? undefined : 401,
      },
    });
    server = createServer((req, res) => {
      const direct = req.url.startsWith('/direct/');
      if (direct) req.url = req.url.slice('/direct'.length);
      void (direct ? directHandler : handler)(req, res);
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = server.address().port;
    const sendHTTP = (headers = {}, method = 'POST', direct = false) =>
      new Promise((resolve, reject) => {
        const req = httpRequest(
          {
            hostname: '127.0.0.1',
            port,
            path:
              (direct ? '/direct' : '') +
              '/auriga/settings/' +
              (method === 'GET' ? 'state' : 'save'),
            method,
            headers: {
              host: 'upstream.internal:3000',
              origin: 'https://instance.example.com:8443',
              'content-type': 'application/json',
              'x-auriga-settings': '1',
              'x-native-test': 'valid',
              ...headers,
            },
          },
          (res) => {
            res.resume();
            res.once('end', () => resolve(res.statusCode));
          },
        );
        req.once('error', reject);
        req.end(
          method === 'GET'
            ? undefined
            : JSON.stringify({
                platformOrigin: 'https://example.com',
                deployment: 'local',
              }),
        );
      });
    assert.equal(await sendHTTP(), 200);
    assert.equal(await sendHTTP({}, 'GET'), 200);
    for (const origin of [
      'https://evil.example.com:8443',
      'http://instance.example.com:8443',
      'https://instance.example.com',
      'https://instance.example.com:9443',
      'null',
      [
        'https://instance.example.com:8443',
        'https://instance.example.com:8443',
      ],
    ]) {
      assert.equal(
        await sendHTTP({
          origin,
          'x-forwarded-proto': 'https',
          'x-forwarded-host': 'instance.example.com:8443',
          forwarded: 'proto=https;host=instance.example.com:8443',
        }),
        403,
      );
    }
    assert.equal(await sendHTTP({ 'x-auriga-settings': 'invalid' }), 403);
    assert.equal(await sendHTTP({ 'x-native-test': 'invalid' }), 401);
    assert.equal(await sendHTTP({ 'sec-fetch-site': 'cross-site' }), 403);
    assert.equal(await sendHTTP({ origin: '' }), 403);
    assert.equal(
      await sendHTTP({ origin: 'http://upstream.internal:3000' }, 'POST', true),
      200,
    );
    assert.equal(
      await sendHTTP(
        {
          origin: 'https://upstream.internal:3000',
          'x-forwarded-proto': 'https',
        },
        'POST',
        true,
      ),
      403,
    );
    assert.equal(
      await sendHTTP({ origin: 'https://example.com' }, 'POST', true),
      403,
      'saved platform origin never establishes UI trust',
    );
    assert.equal(store.snapshot().deployment, 'local');
  } finally {
    if (server) await new Promise((resolve) => server.close(resolve));
    await rm(dir, { recursive: true, force: true });
  }
});

function request(path, { method = 'GET', origin, body, headers = {} } = {}) {
  const req = Readable.from(
    body === undefined ? [] : [Buffer.from(JSON.stringify(body))],
  );
  Object.assign(req, {
    method,
    url: '/auriga/settings/' + path,
    headers: {
      host: 'localhost:3000',
      ...(origin ? { origin } : {}),
      ...headers,
    },
  });
  req.rawHeaders = Object.entries(req.headers).flat();
  return req;
}
async function send(handler, req) {
  const res = {
    status: 0,
    body: '',
    writeHead(code, headers) {
      this.status = code;
      this.headers = headers;
    },
    end(body) {
      this.body = body;
    },
  };
  await handler(req, res);
  return res;
}
test('settings route rejects missing native capability, unauthenticated, cross-origin and non-JSON writes', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-settings-route-'));
  try {
    const store = await createSettingsStore(dir);
    const make = (auth) =>
      createSettingsHandler({ store, auth, activeOrigin: '' });
    assert.equal((await send(make({}), request('state'))).status, 503);
    assert.equal(
      (await send(make({ requestRejection: () => 401 }), request('state')))
        .status,
      401,
    );
    const handler = make({ requestRejection: () => undefined });
    const spoofed = request('save', {
      method: 'POST',
      origin: 'https://localhost:3000',
      headers: {
        'x-forwarded-proto': 'https',
        'x-forwarded-host': 'localhost:3000',
        'content-type': 'application/json',
        'x-auriga-settings': '1',
      },
      body: { platformOrigin: 'https://example.com', deployment: 'local' },
    });
    assert.equal((await send(handler, spoofed)).status, 403);
    const duplicate = request('state', { origin: 'http://localhost:3000' });
    duplicate.rawHeaders.push('Host', 'localhost:3000');
    assert.equal((await send(handler, duplicate)).status, 403);
    for (const uiOrigin of [
      'https://example.com/',
      'https://user@example.com',
      'https://example.com/path',
      'https://example.com:443',
      'ftp://example.com',
      '',
    ])
      assert.throws(() =>
        createSettingsHandler({ store, auth: {}, activeOrigin: '', uiOrigin }),
      );
    const body = { platformOrigin: 'https://example.com', deployment: 'local' };
    for (const origin of [undefined, 'http://example.com', 'null']) {
      assert.equal(
        (await send(handler, request('save', { method: 'POST', origin, body })))
          .status,
        403,
      );
    }
    assert.equal(
      (
        await send(
          handler,
          request('save', {
            method: 'POST',
            origin: 'http://localhost:3000',
            body,
          }),
        )
      ).status,
      403,
    );
    const headers = {
      'content-type': 'application/json',
      'x-auriga-settings': '1',
    };
    const res = await send(
      handler,
      request('save', {
        method: 'POST',
        origin: 'http://localhost:3000',
        body,
        headers,
      }),
    );
    assert.equal(res.status, 200);
    assert.equal(JSON.parse(res.body).deployment, 'local');
    assert.equal(
      JSON.parse((await send(handler, request('state'))).body).activeOrigin,
      '',
    );
    const invalid = await send(
      handler,
      request('save', {
        method: 'POST',
        origin: 'http://localhost:3000',
        body: { ...body, platformOrigin: 'http://example.com' },
        headers,
      }),
    );
    assert.equal(invalid.status, 400);
    assert.equal(JSON.parse(invalid.body).reason, 'httpsRequired');
    assert.equal((await send(handler, request('../store.js'))).status, 404);
    assert.match(
      (await send(handler, request('view.js'))).headers['content-type'],
      /javascript/,
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('public host adapter owns a profile-scoped route and disposal', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'auriga-settings-host-'));
  let route,
    disposed = false;
  try {
    const ctx = {
      profileContext: { dir },
      connection: { requestRejection: () => undefined },
      webServer: {
        register(value) {
          route = value;
          return () => {
            disposed = true;
          };
        },
      },
      effect(fn) {
        this.dispose = fn();
      },
      inject(keys, callback) {
        assert.deepEqual(keys, ['profileContext']);
        this.ready = callback(this);
      },
    };
    installSettings(
      ctx,
      {
        browserOrigin: {
          scheme: 'https',
          host: 'instance.example.com',
          port: 8443,
        },
        connect: { platformOrigin: 'https://example.com' },
      },
      { uiOrigin: 'https://ignored.example.com' },
    );
    await ctx.ready;
    assert.equal(route.path, '/auriga/settings');
    const accepted = request('state', {
      origin: 'https://instance.example.com:8443',
    });
    assert.equal((await send(route.handler, accepted)).status, 200);
    const denied = request('state', { origin: 'https://ignored.example.com' });
    assert.equal((await send(route.handler, denied)).status, 403);
    assert.equal(
      JSON.parse((await send(route.handler, request('state'))).body).deviceName,
      'Auriga DSH',
    );
    assert.equal(
      JSON.parse((await send(route.handler, request('state'))).body)
        .activeOrigin,
      'https://example.com',
    );
    ctx.dispose();
    assert.equal(disposed, true);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
