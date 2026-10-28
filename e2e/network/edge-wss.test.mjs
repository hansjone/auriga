/* global AbortController, Buffer */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { assertBoundary, verdict } from './support/edge-wss-verdict.mjs';
import {
  brokerRejected,
  createWireObserver,
  invalidTicketVector,
} from './support/edge-wss-wire.mjs';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { createRequire } from 'node:module';
import { createDeviceProvider } from '../../clients/dsh-plugin/src/network/device-provider.js';
import { testRoots } from '../../clients/dsh-plugin/test/network/trust/keyset-fixtures.js';
const { WebSocket, WebSocketServer } = createRequire(
  new URL('../../clients/dsh-plugin/package.json', import.meta.url),
)('ws');

test('wire observer decodes the SDK ArrayBuffer binary mode on a real local socket', async () => {
  const server = createServer();
  const wss = new WebSocketServer({ server });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const incoming = once(wss, 'connection');
  const socket = new WebSocket(`ws://127.0.0.1:${server.address().port}`);
  const observer = createWireObserver();
  let oldObservation = false;
  socket.binaryType = 'arraybuffer';
  socket.on('message', (data) => {
    oldObservation ||= data.toString().includes('-ERR');
    observer.message(data);
  });
  socket.on('close', (code, reason) => observer.close(code, reason));
  try {
    const closed = once(socket, 'close');
    const [peer] = await incoming;
    peer.send(
      Buffer.from(
        `INFO ${JSON.stringify({ server_id: 'N' + 'A'.repeat(55), client_id: 12 })}\r\n`,
      ),
    );
    peer.send(Buffer.from("-ERR 'Authorization Violation'\r\n"));
    peer.close(1008, 'Authentication Failure');
    await closed;
    assert.equal(
      oldObservation,
      false,
      'original toString observer misses ArrayBuffer bytes',
    );
    assert.equal(observer.evidence().authError, 'authorization-violation');
    assert.deepEqual(observer.evidence().info, {
      serverId: 'N' + 'A'.repeat(55),
      clientId: '12',
    });
    assert.deepEqual(observer.evidence().close, {
      code: 1008,
      reason: 'authentication-failure',
    });
  } finally {
    socket.terminate();
    await new Promise((ok) => wss.close(ok));
    await new Promise((ok) => server.close(ok));
  }
});

test('invalid ticket ingress vector is checked by the production provider projection', async (t) => {
  const now = Date.now();
  const identity = {
    tenant: '019a0000-0000-7000-8000-000000000001',
    realm: '1',
    node: '1',
    device: '019a0000-0000-7000-8000-000000000002',
    systemAddress: 'aur1:1.1.0',
  };
  t.mock.method(globalThis, 'fetch', async (url) =>
    Response.json(
      url.pathname.endsWith('/register')
        ? { ...identity, created: true }
        : {
            identity,
            ticket: invalidTicketVector.ticket,
            ingress: invalidTicketVector.ingress,
            upSubject: invalidTicketVector.upSubject,
            downSubject: `auriga.net.down.${invalidTicketVector.ingress}`,
            replyPrefix: `auriga.net.reply.${invalidTicketVector.ingress}`,
            serverAudience: 'server-a',
            protocolVersion: 1,
            expiresAt: new Date(now + 30000).toISOString(),
            connectionExpiresAt: new Date(now + 3600000).toISOString(),
          },
    ),
  );
  const provider = await createDeviceProvider({
    config: {
      platformOrigin: 'https://portal.example.com',
      issuer: 'https://portal.example.com',
      clientId: 'bridge-device',
      providerId: 'bridge',
      network: {
        realm: '1',
        roots: testRoots,
        wssUrl: 'wss://network.example.com/net/v1/ws',
      },
    },
    getDevice: async () => ({
      accessToken: 'test-device',
      claims: { tid: identity.tenant, did: identity.device, sub: 'subject' },
    }),
    now: () => now,
  }).prepare({ signal: new AbortController().signal });
  const signal = new AbortController().signal;
  await provider.register(signal);
  const projected = await provider.getTicket(signal);
  assert.equal(
    projected.subjects.downSubject,
    `auriga.net.down.${invalidTicketVector.ingress}`,
  );
  assert.equal(JSON.parse(projected.auth.token).ticket, 'a'.repeat(43));
});

test('wire rejection is exact, bounded, and never scans message payloads', () => {
  const observer = createWireObserver();
  observer.message(Buffer.from('-ER'));
  observer.message(Buffer.from("R 'Authorization Violation'\r\n"));
  assert.equal(brokerRejected(observer.evidence()), true);
  const body = createWireObserver();
  body.message(
    Buffer.from("MSG subject 1 32\r\n-ERR 'Authorization Violation'\r\n"),
  );
  assert.equal(brokerRejected(body.evidence()), false);
  for (const [code, reason, expected] of [
    [1008, 'Authentication Failure', true],
    [1006, 'Authentication Failure', false],
    [1008, 'Authentication Timeout', false],
    [1008, 'arbitrary Authorization Violation text', false],
  ]) {
    const close = createWireObserver();
    close.close(code, Buffer.from(reason));
    assert.equal(brokerRejected(close.evidence()), expected);
  }
  observer.message(Buffer.alloc(65537));
  assert.equal(brokerRejected(observer.evidence()), false);
  assert.equal(JSON.stringify(body.evidence()).includes('subject'), false);
});
const proof = () => ({
  before: { edge: { dials: 0, tlsErrors: [] }, redeems: [], decisions: [] },
  after: { edge: { dials: 0, tlsErrors: [] }, redeems: [], decisions: [] },
  response: {
    observations: {
      httpStatus: 403,
      problem: 'common.forbidden',
      upgraded: false,
    },
  },
});
test('entry refusal requires exact phase and zero Broker dial/Redeem', () => {
  assertBoundary(proof(), 'origin');
  const wrong = proof();
  wrong.after.edge.dials = 1;
  assert.throws(() => assertBoundary(wrong, 'origin'), /entry reached Broker/);
  wrong.response.observations.httpStatus = 502;
  assert.throws(() => assertBoundary(wrong, 'origin'));
});
test('TLS refusal requires real typed x509 error after dial, without Redeem', () => {
  const p = proof();
  Object.assign(p.response.observations, {
    httpStatus: 502,
    problem: 'edge.upstream_error',
  });
  p.after.edge = { dials: 1, tlsErrors: ['unknown-authority'] };
  assertBoundary(p, 'wrong-ca');
  p.after.edge.tlsErrors = ['other'];
  assert.throws(() => assertBoundary(p, 'wrong-ca'));
  p.after.edge.tlsErrors = ['hostname'];
  assertBoundary(p, 'wrong-name');
});
test('CONNECT refusal cannot pass on any generic network error', () => {
  const p = proof();
  Object.assign(p.response.observations, {
    upgraded: true,
    tlsVerified: true,
    authorized: false,
    natsAuthError: true,
    wire: {
      info: { serverId: 'server-a', clientId: '12' },
      authError: 'authorization-violation',
      close: null,
      overflow: false,
      conflictingInfo: false,
    },
  });
  p.response.errorCode = 'connection_lost'; // SDK code alone is never evidence.
  p.response.expectedIngress = 'a'.repeat(26);
  p.after.redeems = [
    {
      code: 'PermissionDenied',
      serverId: 'server-a',
      clientId: '12',
      ingress: p.response.expectedIngress,
    },
  ];
  p.after.decisions = [
    {
      clientType: 'websocket',
      allowed: false,
      serverId: 'server-a',
      clientId: 12,
    },
  ];
  assertBoundary(p, 'auth');
  for (const corrupt of [
    (v) => {
      v.after.decisions[0].clientId = 99;
    },
    (v) => {
      v.after.redeems[0].serverId = 'unrelated-server';
    },
    (v) => {
      v.after.redeems[0].ingress = 'b'.repeat(26);
    },
    (v) => {
      v.after.decisions.push(v.after.decisions[0]);
    },
    (v) => {
      v.after.redeems.push(v.after.redeems[0]);
    },
    (v) => {
      v.response.observations.wire.info = null;
    },
  ]) {
    const unrelated = JSON.parse(JSON.stringify(p));
    corrupt(unrelated);
    assert.throws(() => assertBoundary(unrelated, 'auth'));
  }
  p.response.observations.wire.authError = null;
  assert.throws(() => assertBoundary(p, 'auth'));
});
test('counterevidence never accepts setup or cleanup failures', () => {
  const stages = ['java', 'gateway', 'client', 'valid'].map((name) => ({
    stage: name + '-closed',
    exitCode: 0,
  }));
  const m = {
    stages,
    completed: false,
    failurePhase: 'wrong-ca',
    failures: [
      {
        name: 'AssertionError',
        message: 'upstream TLS must reject HTTP upgrade',
      },
    ],
  };
  verdict(m, 'BypassUpstreamTLS', 1);
  assert.throws(() =>
    verdict({ ...m, failurePhase: 'initialization' }, 'BypassUpstreamTLS', 1),
  );
  assert.throws(() =>
    verdict(
      { ...m, failures: [...m.failures, { message: 'cleanup failed' }] },
      'BypassUpstreamTLS',
      1,
    ),
  );
  stages[0].exitCode = 1;
  assert.throws(() => verdict(m, 'BypassUpstreamTLS', 1));
});
test('driver retains real assembly and explicit opt-in; test-only overlay observes callout', async () => {
  const driver = await readFile(
    new URL('./source-edge-wss.mjs', import.meta.url),
    'utf8',
  );
  const build = await readFile(
    new URL('./support/edge-wss-build.mjs', import.meta.url),
    'utf8',
  );
  assert.match(driver, /javaClasspath\(root\)/);
  assert.match(driver, /NetworkSourceJavaHost/);
  assert.match(driver, /--run/);
  assert.match(driver, /assertBoundary\(proof, expected\)/);
  assert.match(build, /r\.redeemer\.onDecision\(a, c, e\)/);
  assert.doesNotMatch(build, /InsecureSkipVerify/);
});
