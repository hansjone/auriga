// Root-only real-host probe. Run with Node >=22 and the installed rc.3
// connection package.json as argv[2]. It uses public exports, a disposable
// loopback server and an in-memory credential-provider fixture; no user home.
import assert from 'node:assert/strict';
import http from 'node:http';
import { generateKeyPairSync, randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createDoorLock } from '../../src/managed/door.js';
import { installHostAuthentication } from '../../src/managed/host-auth.js';
import { signIdentity, INSTANCE_ID, OWNER_ID } from '../managed-helpers.js';

const hostRequire = createRequire(resolve(process.argv[2]));
const { Context } = await import(
  pathToFileURL(hostRequire.resolve('@deepseek-ai/cordis'))
);
const connectionApi = await import(
  pathToFileURL(hostRequire.resolve('@deepseek-ai/dsh-client-connection'))
);
// ws is CommonJS in the pinned host. Its require entry exposes the constructors
// even when dynamic import only supplies the default export.
const { WebSocketServer, WebSocket } = hostRequire('ws');
assert.equal(
  hostRequire('@deepseek-ai/dsh-client-connection/package.json').version,
  '0.2.1-rc.3',
);
const ctx = new Context();
const routes = [];
const records = new Map();
ctx.provide('credentials', {
  async modifyRecord(key, update) {
    const next = await update(records.get(key));
    if (next !== undefined) records.set(key, next);
    return records.get(key);
  },
});
ctx.provide('webServer', {
  host: '127.0.0.1',
  protocol: 'http:',
  register(route) {
    routes.push(route);
    return () => routes.splice(routes.indexOf(route), 1);
  },
});
const keys = generateKeyPairSync('ed25519');
const door = createDoorLock({
  instanceId: INSTANCE_ID,
  ownerId: OWNER_ID,
  getPublicKey: () => keys.publicKey,
});
let connection, peer, restore, uninstall;
const server = http.createServer((req, res) => {
  const route = routes.find(
    (r) => r.kind === 'prefix' && req.url.startsWith(r.path),
  );
  if (route) void route.handler(req, res);
  else {
    res.writeHead(404);
    res.end();
  }
});
const wss = new WebSocketServer({ noServer: true });
server.on('upgrade', (req, socket, head) => {
  const admission = connection.admitConnection(req);
  if ('rejection' in admission) {
    socket.end(
      `HTTP/1.1 ${admission.rejection} Rejected\r\nConnection: close\r\n\r\n`,
    );
    return;
  }
  assert.equal(admission.peer, peer);
  wss.handleUpgrade(req, socket, head, (ws) => {
    ws.once('close', () => void admission.release());
    ws.send('ready');
  });
});
const token = (method, path) =>
  signIdentity(keys.privateKey, 'test', { htm: method, htu: path }).token;
async function rpc(headers = {}, identity = token('POST', '/api/probe')) {
  const response = await globalThis.fetch(
    `http://127.0.0.1:${server.address().port}/api/probe`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Origin: 'https://instance.i.example.com',
        'Auriga-Identity': identity,
        ...headers,
      },
      body: JSON.stringify({
        type: 'client-request',
        rpcId: randomUUID(),
        method: 'probe',
        payload: {},
      }),
    },
  );
  const body = await response.text();
  if (response.status === 200)
    assert.equal(JSON.parse(body).result.value.samePeer, true);
  return response.status;
}
function socket(headers = {}) {
  return new Promise((resolveStatus, reject) => {
    const ws = new WebSocket(
      `ws://127.0.0.1:${server.address().port}/api/remote.mux`,
      {
        headers: {
          Origin: 'https://instance.i.example.com',
          'Auriga-Identity': token('GET', '/api/remote.mux'),
          ...headers,
        },
      },
    );
    ws.on('message', (value) => {
      assert.equal(value.toString(), 'ready');
      ws.close();
    });
    ws.on('close', () => resolveStatus(101));
    ws.on('unexpected-response', (_request, response) => {
      response.resume();
      resolveStatus(response.statusCode);
    });
    ws.on('error', reject);
  });
}
try {
  await ctx.plugin(connectionApi, {
    trustedHosts: ['instance.i.example.com:443'],
  });
  connection = ctx.connection;
  assert.ok(connection instanceof connectionApi.HostConnectionService);
  peer = connection.browserPeer;
  assert.equal(peer.principal.kind, 'owner');
  assert.equal(peer.signal.aborted, false);
  connection.rpc.intercept(
    '/api',
    (endpoint) => endpoint === 'probe',
    async (_endpoint, _payload, _signal, admitted) => ({
      ok: true,
      value: { samePeer: admitted === peer },
    }),
  );
  uninstall = door.install(server);
  await new Promise((done) => server.listen(0, '127.0.0.1', done));
  const before = { http: await rpc(), ws: await socket() };
  assert.deepEqual(before, { http: 403, ws: 403 });
  restore = installHostAuthentication(connection, door, {
    browserOrigin: {
      scheme: 'https',
      host: 'instance.i.example.com',
      port: 443,
    },
  });
  const after = { http: await rpc(), ws: await socket() };
  assert.deepEqual(after, { http: 200, ws: 101 });
  assert.equal(await rpc({ Origin: 'https://attacker.example.com' }), 403);
  assert.equal(await socket({ Origin: 'https://attacker.example.com' }), 403);
  assert.equal(await rpc({ 'Sec-Fetch-Site': 'cross-site' }), 403);
  assert.equal(await rpc({}, 'invalid'), 401);
  const reused = token('POST', '/api/probe');
  assert.equal(await rpc({}, reused), 200);
  assert.equal(await rpc({}, reused), 401);
  assert.equal(
    peer.signal.aborted,
    false,
    'closing a browser socket must preserve the shared owner Peer',
  );
  await peer.dispose();
  assert.equal(await rpc(), 401);
  assert.equal(await socket(), 401);
  console.log(
    JSON.stringify({
      result: 'passed',
      dsh: '0.2.1-rc.3',
      actualCordis: true,
      actualHostConnectionService: true,
      actualBrowserAuth: true,
      actualPeer: true,
      before,
      after,
      negativeCases: [
        'foreign-origin-http-ws',
        'cross-site',
        'unsigned',
        'replay',
        'disposed-peer',
      ],
      userDataRead: false,
    }),
  );
} finally {
  uninstall?.();
  restore?.();
  for (const ws of wss.clients) ws.terminate();
  wss.close();
  server.closeAllConnections();
  if (server.listening) await new Promise((done) => server.close(done));
  await ctx.fiber.dispose();
}
