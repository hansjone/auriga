import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import https from 'node:https';
import { mkdtempSync, readFileSync, rmSync, mkdirSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
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

test('observer authenticates both TLS hops and preserves entry metadata without credential capture', async (t) => {
  const out = mkdtempSync(join(tmpdir(), 'auriga-observer-mtls-'));
  t.after(() => rmSync(out, { recursive: true, force: true }));
  const proofs = mkdtempSync(join(tmpdir(), 'auriga-observer-proofs-'));
  t.after(() => rmSync(proofs, { recursive: true, force: true }));
  const sha256 = raw => createHash('sha256').update(raw).digest('hex');
  const config = Buffer.from('{"os":"linux","architecture":"amd64"}');
  const manifest = Buffer.from(JSON.stringify({
    schemaVersion: 2, mediaType: 'application/vnd.oci.image.manifest.v1+json',
    config: { mediaType: 'application/vnd.oci.image.config.v1+json', digest: `sha256:${sha256(config)}`, size: config.length },
    layers: [],
  }));
  const digest = sha256(manifest);
  mkdirSync(join(proofs, digest));
  writeFileSync(join(proofs, digest, 'manifest.json'), manifest);
  writeFileSync(join(proofs, digest, 'config.json'), config);
  const generated = spawnSync(
    'go',
    [
      'run',
      '.',
      '-out',
      out,
      '-image',
      '127.0.0.1:5000/auriga/fake-dsh@sha256:' + digest,
      '-runtime-proofs',
      proofs,
      '-heartbeat-observer',
    ],
    {
      cwd: fileURLToPath(
        new URL('../../deploy/compose/bootstrap/', import.meta.url),
      ),
      env: { ...process.env, GOWORK: 'off' },
      encoding: 'utf8',
      timeout: 120_000,
    },
  );
  assert.equal(
    generated.status,
    0,
    'bootstrap must generate the real TLS fixture',
  );
  const read = (name) => readFileSync(join(out, 'pki', name));
  const tls = {
    ca: read('ca.crt'),
    cert: read('iam.crt'),
    key: read('iam.key'),
    clientCert: read('edge-iam.crt'),
    clientKey: read('edge-iam.key'),
    serverName: 'platform',
    clientDNSName: 'edge.auriga.internal',
  };
  const received = [];
  const captured = [];
  let status = 200;
  const upstream = https.createServer(
    {
      ca: tls.ca,
      cert: tls.cert,
      key: tls.key,
      requestCert: true,
      rejectUnauthorized: true,
    },
    async (req, res) => {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      received.push({
        headers: req.headers,
        body: Buffer.concat(chunks).toString(),
        authorized: req.socket.authorized,
        peer: req.socket.getPeerCertificate().subjectaltname,
      });
      res
        .writeHead(status, { 'content-type': 'application/json' })
        .end('{"ok":true}');
    },
  );
  upstream.listen(0, '127.0.0.1');
  await once(upstream, 'listening');
  t.after(() => upstream.close());
  const makeObserver = async (options = tls) => {
    const observer = createHeartbeatObserver({
      upstream: `https://127.0.0.1:${upstream.address().port}`,
      tls: options,
      onHeartbeat: (entry) => captured.push(entry),
    });
    observer.listen(0, '127.0.0.1');
    await once(observer, 'listening');
    t.after(() => observer.close());
    return observer;
  };
  const observer = await makeObserver();
  const body = JSON.stringify({
    pluginVersion: '0.0.1',
    dshVersion: '0.2.0-rc.2',
    activity: {
      runningTurns: 0,
      openTerminals: 0,
      activeImBots: 0,
      connectedClients: 0,
    },
    health: { ok: true, problems: ['private-error'] },
    secret: 'private-secret',
  });
  const credential =
    'Bearer ait_00000000-0000-4000-8000-000000000001.opaque-suffix';
  const send = (overrides = {}, target = observer) =>
    new Promise((resolve, reject) => {
      const req = https.request(
        {
          hostname: '127.0.0.1',
          port: target.address().port,
          servername: 'platform',
          ca: tls.ca,
          cert: tls.clientCert,
          key: tls.clientKey,
          agent: false,
          method: 'POST',
          path: '/api/workspace/v1/instance/heartbeat',
          headers: {
            host: 'admin.example.com:8084',
            authorization: credential,
            cookie: 'private-cookie',
            'content-type': 'application/json',
            'x-auriga-entry-origin': 'http://admin.example.com:8084',
            'x-auriga-entry-role': 'admin',
          },
          ...overrides,
        },
        (res) => {
          res.resume();
          res.on('end', () => resolve(res.statusCode));
        },
      );
      req.on('error', reject);
      req.end(body);
    });
  assert.equal(await send(), 200);
  assert.equal(received[0].authorized, true);
  assert.match(received[0].peer, /DNS:edge\.auriga\.internal/);
  assert.equal(received[0].headers.host, 'admin.example.com:8084');
  assert.equal(
    received[0].headers['x-auriga-entry-origin'],
    'http://admin.example.com:8084',
  );
  assert.equal(received[0].headers['x-auriga-entry-role'], 'admin');
  assert.equal(received[0].headers.authorization, credential);
  assert.equal(received[0].body, body);
  assert.equal(captured.length, 1);
  assert.doesNotMatch(
    JSON.stringify(captured),
    /opaque|secret|cookie|private-error|entry-origin/i,
  );
  status = 401;
  assert.equal(await send(), 401);
  assert.equal(
    captured.length,
    1,
    'upstream rejection must not become evidence',
  );
  assert.equal(
    await send({ cert: read('edge.crt'), key: read('edge.key') }),
    403,
    'same CA and clientAuth without the fixed edge DNS identity must fail',
  );
  assert.equal(received.length, 2, 'wrong client SAN must never reach IAM');
  await assert.rejects(send({ cert: undefined, key: undefined }));
  await assert.rejects(send({ servername: 'wrong.internal' }));
  await assert.rejects(send({ ca: read('edge.crt') }));
  const wrongServer = await makeObserver({
    ...tls,
    serverName: 'wrong.internal',
  });
  assert.equal(
    await send({}, wrongServer),
    502,
    'outbound DNS verification must fail closed',
  );
  const wrongClient = await makeObserver({
    ...tls,
    clientCert: tls.cert,
    clientKey: tls.key,
  });
  assert.equal(
    await send({}, wrongClient),
    502,
    'serverAuth key cannot replace an outbound client certificate',
  );
  assert.equal(received.length, 2);
  assert.equal(captured.length, 1);
  for (const upstream of [
    'https://localhost:8081',
    'https://192.0.2.1',
    'https://user@127.0.0.1',
  ]) {
    assert.throws(() =>
      createHeartbeatObserver({ upstream, tls, onHeartbeat() {} }),
    );
  }
  assert.throws(() =>
    createHeartbeatObserver({
      upstream: 'https://127.0.0.1',
      tls: { ...tls, clientDNSName: '' },
      onHeartbeat() {},
    }),
  );
});
