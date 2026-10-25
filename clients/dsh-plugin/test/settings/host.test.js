import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Readable } from 'node:stream';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import {
  createSettingsHandler,
  installSettings,
} from '../../src/settings/host.js';
import { createSettingsStore } from '../../src/settings/store.js';

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
    installSettings(ctx, {
      connect: { platformOrigin: 'https://example.com' },
    });
    await ctx.ready;
    assert.equal(route.path, '/auriga/settings');
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
