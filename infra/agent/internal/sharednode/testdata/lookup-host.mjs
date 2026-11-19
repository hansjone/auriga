// Component regression: production Host/runtime/transport -> production Relay
// -> synthetic gRPC W. This does not boot or attest a complete DSH profile.
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { spawn } from 'node:child_process';
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { mkdtemp, rmdir } from 'node:fs/promises';
import { createServer, request } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { once } from 'node:events';
import { clearTimeout, setTimeout } from 'node:timers';
import { installSharedHost } from '../../../../../clients/dsh-plugin/src/shared/host.js';
import { createSharedRuntime } from '../../../../../clients/dsh-plugin/src/shared/runtime.js';
import { createSharedNodeTransport } from '../../../../../clients/dsh-plugin/src/shared/node-transport.js';
import { canonical } from '../../../../../clients/dsh-plugin/src/shared/protocol.js';

assert.ok(process.argv[2], 'pass the built history-relay fixture executable');
const id = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const tenantId = id(1),
  physicalInstanceId = id(2),
  alive = id(3);
const expired = id(4),
  loggedOut = id(5),
  outage = id(6),
  unknown = id(7),
  foreign = id(8);
const binding = {
  tenantId,
  memberId: alive,
  placementId: alive,
  physicalInstanceId,
  mode: 'shared',
  epoch: 1,
  revision: 1,
};
const child = spawn(resolve(process.argv[2]), [], {
  stdio: ['pipe', 'pipe', 'pipe'],
  windowsHide: true,
  env: { ...process.env, GOMAXPROCS: '2', GOMEMLIMIT: '512MiB' },
});
const exit = new Promise((resolve, reject) => {
  child.once('error', reject);
  child.once('exit', (code, signal) => resolve({ code, signal }));
});
// Observe launch errors immediately, including before the readiness race.
exit.catch(() => {});
let stderr = '';
child.stderr.on('data', (chunk) => {
  stderr = (stderr + chunk).slice(-4096);
});
const lines = createInterface({ input: child.stdout });
let proxy, transport, host, temporary;
const bounded = async (promise, ms, message) => {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), ms);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
};
async function stopChild() {
  child.stdin.end();
  try {
    const result = await bounded(exit, 10000, 'fixture cleanup timeout');
    assert.equal(result.code, 0, `fixture exit: ${stderr}`);
  } catch (error) {
    child.kill();
    await bounded(
      exit.catch(() => {}),
      2000,
      'fixture termination timeout',
    ).catch(() => {});
    throw error;
  }
}
try {
  const [line] = await bounded(
    Promise.race([
      once(lines, 'line'),
      exit.then(() => {
        throw new Error('fixture exited before ready: ' + stderr);
      }),
    ]),
    10000,
    'fixture readiness timeout',
  );
  const { url } = JSON.parse(line);
  assert.match(url, /^http:\/\/127\.0\.0\.1:\d+$/);
  // The public Node transport only accepts a local socket. A fixture-only
  // local bridge connects it to Go's loopback httptest server on both OSes.
  temporary = await mkdtemp(join(tmpdir(), 'auriga-history-'));
  const socketPath =
    process.platform === 'win32'
      ? `\\\\.\\pipe\\auriga-history-${process.pid}-${randomUUID()}`
      : join(temporary, 'relay.sock');
  proxy = createServer((incoming, outgoing) => {
    const upstream = request(
      new URL(incoming.url, url),
      {
        method: incoming.method,
        headers: incoming.headers,
        agent: false,
      },
      (response) => {
        outgoing.writeHead(response.statusCode, response.headers);
        response.pipe(outgoing);
      },
    );
    upstream.once('error', () => outgoing.destroy());
    outgoing.once('close', () => upstream.destroy());
    incoming.pipe(upstream);
  });
  proxy.listen(socketPath);
  await once(proxy, 'listening');
  transport = createSharedNodeTransport(socketPath);
  const ports = {
    async lookup(sessionId) {
      return (
        await transport.call('lookup', {
          tenantId,
          physicalInstanceId,
          sessionId,
        })
      ).binding;
    },
    async resolve(binding, platformSessionId) {
      const result = await transport.call('resolve', {
        binding,
        platformSessionId,
      });
      return {
        active: result.state === 'ready',
        binding: result.binding,
        platformSessionId,
        authGeneration: result.authGeneration,
      };
    },
  };
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const runtime = createSharedRuntime({
    ports,
    physicalInstanceId,
    identityKeys: new Map([['synthetic-history', publicKey]]),
    capabilityKeys: new Map(),
  });
  let persisted = [alive, expired, loggedOut, foreign, 'legacy-owner'];
  const dispose = () => {};
  host = installSharedHost(
    {
      remoteAccess: { registerPolicy: () => dispose },
      tools: { guard: () => dispose, register: () => dispose },
      webServer: { register: () => dispose },
      sessions: { list: () => [{ id: alive }] },
      sessionPersistence: {
        list: async () => persisted.map((id) => ({ header: { id } })),
      },
    },
    {
      runtime,
      enterprise: { handler() {} },
      ports: {
        assertProfile: () => true,
        createPeer: ({ principal }) => ({ principal }),
      },
    },
  );
  const now = Math.floor(Date.now() / 1000);
  const claims = {
    schemaVersion: 2,
    issuer: 'auriga.edge',
    audience: 'auriga.shared.ingress',
    binding,
    platformSessionId: alive,
    method: 'GET',
    canonicalPath: '/',
    issuedAt: now,
    expiresAt: now + 60,
    jti: randomUUID(),
  };
  const unsigned = [
    {
      alg: 'EdDSA',
      kid: 'synthetic-history',
      typ: 'auriga-shared-identity+jwt',
    },
    claims,
  ]
    .map((value) => Buffer.from(canonical(value)).toString('base64url'))
    .join('.');
  const token =
    unsigned +
    '.' +
    sign(null, Buffer.from(unsigned), privateKey).toString('base64url');
  const peer = await host.admitBrowser(token, {
    method: 'GET',
    canonicalPath: '/',
  });
  assert.deepEqual(await host.refresh(peer), [alive]);
  assert.deepEqual(await host.messagingSessions(peer), [
    { sessionId: alive, label: '会话 1' },
  ]);
  console.log(
    'PASS expired/logged-out snapshots, foreign member and legacy owner are excluded; current member remains',
  );
  for (const failedSession of [outage, unknown]) {
    persisted = [alive, failedSession];
    for (const operation of [
      () => host.refresh(peer),
      () => host.messagingSessions(peer),
    ]) {
      await assert.rejects(operation, (error) => {
        assert.equal(error.code, 'common.unavailable');
        assert.equal(error.message, 'common.unavailable');
        return true;
      });
    }
  }
  console.log(
    'PASS real authority outage and unknown not_found text still fail closed without private errors',
  );
  persisted = [alive, expired, loggedOut];
  assert.deepEqual(await host.refresh(peer), [alive]);
  console.log('PASS fresh enumeration recovers after authority restoration');
} finally {
  host?.close();
  transport?.close();
  if (proxy?.listening) {
    proxy.closeAllConnections();
    await new Promise((resolve, reject) =>
      proxy.close((error) => (error ? reject(error) : resolve())),
    );
  }
  lines.close();
  try {
    await stopChild();
  } finally {
    if (temporary) await rmdir(temporary);
  }
}
