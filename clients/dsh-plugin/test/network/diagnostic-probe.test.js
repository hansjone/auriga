/* global AbortSignal */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createConnectRuntime } from '../../src/local/runtime.js';
import { createPlatform, platformConfig } from '../../src/local/platform.js';
import {
  probeEndpoint,
  DIAGNOSTIC_PATH,
} from '../../src/network/diagnostic-probe.js';
import { fixture } from './host/native/fixtures.js';
import { registerBaseTools } from '../../src/network/base-tools.js';

async function fixtureHttp(t) {
  let status = 200,
    body = JSON.stringify({ items: [] });
  const calls = [];
  const server = createServer((req, res) => {
    calls.push({
      path: req.url,
      authorization: req.headers.authorization,
      cookie: req.headers.cookie,
    });
    res.writeHead(status, {
      'content-type': body.startsWith('<') ? 'text/html' : 'application/json',
      location: 'https://outside.example.com',
    });
    res.end(body);
  });
  await new Promise((done) => server.listen(0, '127.0.0.1', done));
  t.after(async () => {
    server.closeAllConnections();
    await new Promise((done) => server.close(done));
  });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const config = platformConfig({
    platformOrigin: origin,
    issuer: origin,
    clientId: 'auriga-dsh',
    providerId: 'team',
    allowLocalHttp: true,
  });
  return {
    config,
    origin,
    calls,
    set: (code, text) => {
      status = code;
      body = text;
    },
  };
}
for (const [code, body] of [
  [200, '<html>portal</html>'],
  [404, 'missing'],
  [401, '{}'],
  [500, '{}'],
  [503, '<html>unavailable</html>'],
  [302, ''],
])
  test(`HTTP ${code} proves ingress reachability and preserves the cache`, async (t) => {
    const f = await fixtureHttp(t),
      native = fixture(),
      agent = native.attach('session-a'),
      tools = new Map();
    native.ctx.tools = {
      register: (tool) => {
        tools.set(tool.name, tool);
        return () => tools.delete(tool.name);
      },
    };
    const directory = await mkdtemp(join(tmpdir(), 'auriga-diag-'));
    t.after(() => rm(directory, { recursive: true, force: true }));
    const runtime = await createConnectRuntime({
      config: f.config,
      directory,
      ctx: native.ctx,
    });
    t.after(() => runtime.close());
    const dispose = registerBaseTools({
      ctx: native.ctx,
      ...runtime.baseTools,
    });
    t.after(dispose);
    f.set(code, body);
    const result = await tools
      .get('auriga_net_diagnose')
      .execute({}, { agent });
    assert.equal(result.segments[1].segment, 'ingress');
    assert.equal(result.segments[1].status, 'ready');
    assert.equal(result.firstFailure, 'hq_api');
    assert.equal(
      result.segments[2].reason,
      code === 401 ? 'unauthorized' : 'unknown',
    );
    const status = await tools.get('auriga_net_status').execute({}, { agent });
    assert.equal(status.headquarters.reachable, true);
    assert.equal(status.headquarters.reason, null);
    assert.ok(status.headquarters.lastSuccessAt);
    assert.deepEqual(
      f.calls.map((r) => r.path),
      [DIAGNOSTIC_PATH, DIAGNOSTIC_PATH],
    );
    assert.ok(f.calls.every((r) => !r.authorization && !r.cookie));
  });
test('successful providers JSON reaches the API segment and an arbitrary path cannot be probed', async (t) => {
  const f = await fixtureHttp(t),
    platform = createPlatform(f.config);
  assert.deepEqual(
    await probeEndpoint(platform, DIAGNOSTIC_PATH, AbortSignal.timeout(3000)),
    { httpStatus: 200, apiReady: true, reason: null },
  );
  await assert.rejects(
    probeEndpoint(
      platform,
      'https://outside.example.com',
      AbortSignal.timeout(3000),
    ),
    /untrusted diagnostic path/,
  );
  assert.equal(f.calls.length, 1);
});
