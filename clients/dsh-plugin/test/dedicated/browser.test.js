import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { runInNewContext } from 'node:vm';
import { TextDecoder } from 'node:util';
import { createDedicatedBrowser } from '../../src/dedicated/browser.js';
import { dedicatedBootstrap } from '../../src/dedicated/bootstrap.js';

test('normal native HTML and fixed carrier require signed admission, deny cookie/cross-site/old paths, and recheck after native result', async (t) => {
  const home = mkdtempSync(join(tmpdir(), 'auriga-dedicated-browser-')),
    index = join(home, 'index.html').replaceAll('\\', '/');
  writeFileSync(
    index,
    '<html><head></head><body><main>native dist fixture</main></body></html>',
  );
  let active = true,
    calls = 0,
    mutate = false;
  const row = {
    native: { signal: new AbortController().signal },
    abort: new AbortController(),
  };
  const host = {
    async admit(token, request) {
      assert.equal(token, 'edge-synthetic');
      assert.ok(request.canonicalPath);
      if (!active) throw new Error('revoked');
      return row;
    },
    async check() {
      if (!active) throw new Error('revoked');
    },
    async call() {
      calls++;
      if (mutate) active = false;
      await this.check();
      return { ok: true, value: 'native result' };
    },
    async *open() {
      yield { native: true };
    },
  };
  const browser = createDedicatedBrowser(
    {
      webServer: { renderIndex: (raw) => raw },
      typertGateway: {
        wireStream: { failure: () => ({ code: 'common.unavailable' }) },
      },
    },
    { host, distIndex: index },
  );
  const server = createServer(browser.handler);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    browser.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    rmSync(home, { recursive: true, force: true });
  });
  const base = 'http://127.0.0.1:' + server.address().port;
  const headers = {
    'auriga-identity': 'edge-synthetic',
    'content-type': 'application/json',
  };
  const html = await fetch(base, { headers });
  assert.equal(html.status, 200);
  const body = await html.text();
  assert.match(body, /native dist fixture/);
  assert.match(body, /__DSH_TRANSPORT__/);
  assert.match(body, /owner:true/);
  assert.equal(
    (await fetch(base, { headers: { cookie: 'owner=synthetic' } })).status,
    403,
  );
  assert.equal(
    (
      await fetch(base, {
        headers: { ...headers, 'sec-fetch-site': 'cross-site' },
      })
    ).status,
    403,
  );
  for (const path of [
    '/api/session/create',
    '/connect',
    '/auriga/shared/browser/rpc',
    '/auriga/dedicated/browser/rpc?mode=shared',
  ])
    assert.notEqual((await fetch(base + path, { headers })).status, 200);
  const input = {
    channel: '/api',
    endpoint: 'session/page',
    payload: { args: {} },
  };
  const call = () =>
    fetch(base + '/auriga/dedicated/browser/rpc', {
      method: 'POST',
      headers,
      body: JSON.stringify(input),
    });
  assert.equal((await call()).status, 200);
  assert.equal(calls, 1);
  mutate = true;
  assert.equal((await call()).status, 403);
});

test('dedicated native UI transport sends only fixed same-origin carriers and never contributes identity headers', async () => {
  const sent = [];
  const world = {
    URL,
    TextDecoder,
    document: { baseURI: 'https://portal.example/instances/own/' },
    fetch: async (url, init) => {
      sent.push({ url: url.href, init });
      return new Response(JSON.stringify({ ok: true, value: 'fixture' }), {
        status: 200,
      });
    },
  };
  runInNewContext(dedicatedBootstrap, world);
  const result = await world.__DSH_TRANSPORT__.rpc.call(
    '/api',
    'session/list',
    { args: {} },
  );
  assert.equal(result.value, 'fixture');
  assert.equal(
    sent[0].url,
    'https://portal.example/instances/own/auriga/dedicated/browser/rpc',
  );
  assert.equal(sent[0].init.method, 'POST');
  assert.equal(sent[0].init.credentials, 'same-origin');
  assert.equal(sent[0].init.cache, 'no-store');
  assert.deepEqual(Object.keys(sent[0].init.headers), ['content-type']);
  await assert.rejects(
    world.__DSH_TRANSPORT__.rpc.call(
      'https://evil.example/api',
      'session/list',
      {},
    ),
  );
  assert.equal(sent.length, 1);
});
