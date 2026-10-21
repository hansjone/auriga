import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { once } from 'node:events';
import { createHeartbeatObserver } from './heartbeat-observer.mjs';

test('observer forwards actual credentials/body but persists only successful allowlisted heartbeats', async (t) => {
  const captured = [];
  const forwarded = [];
  let errors = 0;
  let status = 200;
  const upstream = http.createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    forwarded.push({
      auth: req.headers.authorization,
      body: Buffer.concat(chunks).toString(),
    });
    res
      .writeHead(status, { 'content-type': 'application/json' })
      .end('{"serverTime":"2026-01-01T00:00:00Z"}');
  });
  upstream.listen(0, '127.0.0.1');
  await once(upstream, 'listening');
  t.after(() => upstream.close());
  const observer = createHeartbeatObserver({
    upstream: `http://127.0.0.1:${upstream.address().port}`,
    onHeartbeat: (entry) => captured.push(entry),
    onCaptureError: () => errors++,
  });
  observer.listen(0, '127.0.0.1');
  await once(observer, 'listening');
  t.after(() => observer.close());
  const url = `http://127.0.0.1:${observer.address().port}/api/workspace/v1/instance/heartbeat`;
  const credential =
    'Bearer ait_00000000-0000-4000-8000-000000000001.' + 'test-opaque-suffix';
  const body = JSON.stringify({
    pluginVersion: '0.0.1',
    dshVersion: '0.2.0-rc.2',
    activity: {
      runningTurns: null,
      openTerminals: 0,
      activeImBots: 0,
      connectedClients: 0,
      lastInteractionAt: '2026-01-01T00:00:01Z',
      secret: 'activity-secret',
    },
    health: { ok: false, problems: ['private-error'] },
    password: 'private-password',
  });
  const send = (value = body) =>
    fetch(url, {
      method: 'POST',
      headers: {
        authorization: credential,
        'content-type': 'application/json',
        cookie: 'private-cookie',
      },
      body: value,
    });
  assert.equal((await send()).status, 200);
  assert.deepEqual(forwarded[0], { auth: credential, body });
  assert.equal(captured.length, 1);
  assert.deepEqual(captured[0].activity, {
    runningTurns: null,
    openTerminals: 0,
    activeImBots: 0,
    connectedClients: 0,
    lastInteractionAt: '2026-01-01T00:00:01.000Z',
  });
  assert.equal(captured[0].health.ok, false);
  assert.doesNotMatch(
    JSON.stringify(captured),
    /opaque|secret|password|cookie|private-error/i,
  );
  status = 401;
  assert.equal((await send()).status, 401);
  assert.equal(
    captured.length,
    1,
    'rejected heartbeat must not become evidence',
  );
  status = 200;
  assert.equal((await send('{"password":"must-not-log"}')).status, 200);
  assert.equal(captured.length, 1);
  assert.equal(errors, 1);
  assert.equal((await send(' '.repeat(65537))).status, 413);
  assert.equal(forwarded.length, 3, 'oversize heartbeat never forwarded');
  assert.equal(captured.length, 1);
});

test('observer cannot target an external or credential-bearing upstream', () => {
  for (const upstream of [
    'https://127.0.0.1',
    'http://192.0.2.1',
    'http://user@127.0.0.1',
    'http://127.0.0.1/api',
    'http://127.0.0.1?token=x',
  ]) {
    assert.throws(() =>
      createHeartbeatObserver({ upstream, onHeartbeat() {} }),
    );
  }
});
