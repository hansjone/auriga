import { ownerRequest } from './owner-request.js';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { Readable } from 'node:stream';
import { setImmediate } from 'node:timers';
import { performance } from 'node:perf_hooks';
import { JSDOM } from 'jsdom';
import { createConnectRoutes } from '../src/local/routes.js';
import { createLocalLoginController } from '../src/login/local-controller.js';
import { mountLocalLoginPage } from '../src/login/page.js';
import { runtimePeer } from './local-runtime-peer.js';

// Cookie handling mirrors browser fetch, while requests use the real local
// routes, CSRF, runtime, encrypted vault, JWKS and Team handshake.
function browser(runtime, cookie = '') {
  const cookies = new Map(cookie ? [cookie.split('=')] : []);
  const calls = [];
  let fail = '';
  const handler = createConnectRoutes({
    runtime,
    admission: {
      nativeAuth: { requestRejection: () => undefined },
      sameOrigin: (req, write) =>
        req.headers.host === 'localhost' &&
        (!write || req.headers.origin === 'http://localhost'),
    },
  });
  const transport = async (url, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : undefined;
    const req = Readable.from(options.body ? [Buffer.from(options.body)] : []);
    Object.assign(req, {
      url,
      method: options.method ?? 'GET',
      headers: {
        ...Object.fromEntries(
          Object.entries(options.headers ?? {}).map(([key, value]) => [
            key.toLowerCase(),
            value,
          ]),
        ),
        host: 'localhost',
        origin: 'http://localhost',
        cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; '),
      },
    });
    ownerRequest(runtime, req);
    const call = {
      path: url,
      action: body?.action,
      authenticated: !!runtime.resolve(req),
      csrfValid: runtime.checkCsrf(req),
    };
    calls.push(call);
    if (
      (fail === 'resume' && call.action === 'resume') ||
      (fail === 'logout' && url.endsWith('/logout'))
    )
      throw new TypeError('private network failure');
    const res = new EventEmitter();
    res.writeHead = (status, headers) => {
      res.status = status;
      res.headers = headers;
    };
    res.end = (body) => {
      res.body = body;
      res.writableEnded = true;
    };
    await handler(req, res);
    const setCookie = res.headers?.['set-cookie'];
    for (const line of Array.isArray(setCookie)
      ? setCookie
      : setCookie
        ? [setCookie]
        : []) {
      const [pair] = line.split(';');
      const [name, value] = pair.split('=');
      if (line.includes('Max-Age=0')) cookies.delete(name);
      else cookies.set(name, value);
    }
    return new Response(res.body, {
      status: res.status,
      headers: { 'content-type': 'application/json' },
    });
  };
  return {
    calls,
    transport,
    failAt: (value) => {
      fail = value;
    },
  };
}
function page(t, client) {
  const dom = new JSDOM('<main></main>');
  const root = dom.window.document.querySelector('main');
  const view = mountLocalLoginPage(root, {
    logout: true,
    fetch: client.transport,
    navigate() {},
  });
  t.after(() => {
    view.dispose();
    dom.window.close();
  });
  return root;
}
async function settled(root) {
  const deadline = performance.now() + 5000;
  while (!/已退出本机|退出未完成/.test(root.textContent)) {
    if (performance.now() >= deadline) throw new Error('logout did not settle');
    await new Promise((resolve) => setImmediate(resolve));
  }
}
function assertDeviceGone(runtime, request, registered) {
  assert.equal(runtime.vault.snapshot().credential === null, true);
  assert.equal(runtime.isReady(), false);
  assert.equal(runtime.resolve(request), null);
  assert.equal(registered.size, 0);
  assert.equal(
    runtime.prepare(ownerRequest(runtime, { headers: {} })).body
      .resumeAvailable,
    false,
  );
}

test('expired browser session: explicit confirmation resumes ownership, uses fresh CSRF and truly revokes the backing', async (t) => {
  const { runtime, request, registered, requests } = await runtimePeer(t);
  const now = Date.now();
  t.mock.method(Date, 'now', () => now + 1800001);
  assert.equal(runtime.resolve(request), null);
  assert.equal(runtime.isReady(), true);
  const client = browser(runtime, request.headers.cookie);
  const root = page(t, client);
  assert.equal(client.calls.length, 0);
  root.querySelector('[data-action=confirm]').click();
  await settled(root);
  assert.match(root.textContent, /已退出本机登录/);
  assertDeviceGone(runtime, request, registered);
  assert.equal(client.calls.filter((c) => c.action === 'resume').length, 1);
  const writes = client.calls.filter((c) => c.path.endsWith('/logout'));
  assert.equal(writes.length, 1);
  assert.equal(writes[0].authenticated, true);
  assert.equal(writes[0].csrfValid, true);
  assert.equal(requests.filter((path) => path === '/oauth2/revoke').length, 1);
  const controller = createLocalLoginController({ fetch: client.transport });
  await assert.rejects(
    controller.start({ signal: new AbortController().signal }),
  );
  assert.equal(client.calls.filter((c) => c.action === 'resume').length, 1);
});

test('another anonymous browser: GET and cancel preserve A; only explicitly confirmed owner resume can globally sign out', async (t) => {
  const { runtime, request, registered } = await runtimePeer(t);
  const client = browser(runtime);
  const root = page(t, client);
  assert.equal(client.calls.length, 0);
  const controller = createLocalLoginController({ fetch: client.transport });
  await controller.cancel(new AbortController().signal);
  assert.ok(runtime.vault.snapshot().credential);
  assert.ok(runtime.resolve(request));
  assert.equal(runtime.isReady(), true);
  assert.equal(
    client.calls.some((c) => c.path.endsWith('/logout')),
    false,
  );
  root.querySelector('[data-action=confirm]').click();
  await settled(root);
  assert.match(root.textContent, /已退出本机登录/);
  assertDeviceGone(runtime, request, registered);
  assert.equal(
    client.calls.filter((c) => c.path.endsWith('/logout') && c.authenticated)
      .length,
    1,
  );
});

for (const failure of ['resume', 'jwks', 'logout']) {
  test(`confirmed logout ${failure} failure remains visibly failed and explicitly retryable`, async (t) => {
    const { runtime, request, registered } = await runtimePeer(t);
    const now = Date.now();
    t.mock.method(Date, 'now', () => now + 1800001);
    const client = browser(runtime);
    const originalFetch = globalThis.fetch;
    let badProof = failure === 'jwks';
    t.mock.method(globalThis, 'fetch', async (url, init) =>
      badProof && new URL(url).pathname === '/oauth2/jwks'
        ? Response.json({ keys: [] })
        : originalFetch(url, init),
    );
    client.failAt(failure);
    const root = page(t, client);
    root.querySelector('[data-action=confirm]').click();
    await settled(root);
    assert.match(root.textContent, /退出未完成/);
    assert.doesNotMatch(root.textContent, /已退出本机|private/);
    assert.ok(runtime.vault.snapshot().credential);
    assert.equal(runtime.status(request).resumeAvailable, true);
    client.failAt('');
    badProof = false;
    root.querySelector('[data-action=confirm]').click();
    await settled(root);
    assert.match(root.textContent, /已退出本机登录/);
    assertDeviceGone(runtime, request, registered);
  });
}

test('remote revoke failure reports pending only after local backing is gone, repeated confirmation cannot resume it', async (t) => {
  const { runtime, request, registered } = await runtimePeer(t);
  const originalFetch = globalThis.fetch;
  t.mock.method(globalThis, 'fetch', async (url, init) =>
    new URL(url).pathname === '/oauth2/revoke'
      ? Response.json({ code: 'common.unavailable' }, { status: 503 })
      : originalFetch(url, init),
  );
  const client = browser(runtime);
  const root = page(t, client);
  root.querySelector('[data-action=confirm]').click();
  await settled(root);
  assert.match(root.textContent, /远端设备撤销正在重试/);
  assertDeviceGone(runtime, request, registered);
  assert.equal(runtime.vault.snapshot().revocations.length, 1);
  const controller = createLocalLoginController({ fetch: client.transport });
  assert.equal(
    (await controller.logout(new AbortController().signal)).remoteRevocation,
    'pending',
  );
  assert.equal(client.calls.filter((c) => c.action === 'resume').length, 0);
});

test('truly signed-out device confirmation is idempotent without resume or anonymous global logout', async (t) => {
  const { runtime, request } = await runtimePeer(t);
  await runtime.logout(request);
  const client = browser(runtime);
  const root = page(t, client);
  root.querySelector('[data-action=confirm]').click();
  await settled(root);
  assert.match(root.textContent, /已退出本机登录/);
  assert.equal(client.calls.length, 1);
  assert.equal(client.calls[0].path, '/auriga/local-login/status');
});

test('missing backing status and a fake POST success cannot prove device logout', async () => {
  const signal = new AbortController().signal;
  const unknown = createLocalLoginController({
    fetch: async () =>
      Response.json({
        available: true,
        loggedIn: false,
        connected: false,
        remoteRevocation: 'complete',
      }),
  });
  await assert.rejects(unknown.logout(signal));
  let calls = 0;
  const fake = createLocalLoginController({
    fetch: async () =>
      ++calls === 2
        ? Response.json({ loggedIn: false, remoteRevocation: 'complete' })
        : Response.json({
            available: true,
            loggedIn: true,
            connected: true,
            csrf: 'proof',
          }),
  });
  await assert.rejects(fake.logout(signal));
});
