import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { generateKeyPairSync } from 'node:crypto';
import { createDoorLock } from '../src/managed/door.js';
import { installHostAuthentication } from '../src/managed/host-auth.js';
import { signIdentity, INSTANCE_ID, OWNER_ID } from './managed-helpers.js';

const browserOrigin = {
  scheme: 'https',
  host: 'instance.i.example.com',
  port: 443,
};

test('rc.3 direct HTTP and socket admission share the live managed owner Peer', async (t) => {
  const keys = generateKeyPairSync('ed25519');
  const door = createDoorLock({
    instanceId: INSTANCE_ID,
    ownerId: OWNER_ID,
    getPublicKey: () => keys.publicKey,
  });
  const lifetime = new globalThis.AbortController();
  const peer = { principal: { kind: 'owner' }, signal: lifetime.signal };
  let nativeCalls = 0;
  const connection = {
    browserPeer: peer,
    admit() {
      nativeCalls++;
      return { rejection: 403 };
    },
    admitConnection(req) {
      const result = this.admit(req);
      return 'rejection' in result
        ? result
        : { ...result, release: async () => {} };
    },
    requestRejection(req) {
      return this.admit(req).rejection;
    },
    authorizeIndex() {
      return false;
    },
  };
  const original = connection.admit;
  const restore = installHostAuthentication(connection, door, {
    browserOrigin,
  });
  const server = http.createServer((req, res) => {
    const admission = connection.admit(req);
    if (!('rejection' in admission)) assert.equal(admission.peer, peer);
    res.writeHead(admission.rejection ?? 200);
    res.end();
  });
  server.on('upgrade', (req, socket) => {
    const admission = connection.admitConnection(req);
    if (!('rejection' in admission)) {
      assert.equal(admission.peer, peer);
      void admission.release();
    }
    socket.end(
      `HTTP/1.1 ${admission.rejection ?? 101} Test\r\nConnection: close\r\n\r\n`,
    );
  });
  const uninstall = door.install(server);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(async () => {
    uninstall();
    restore();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  });
  const request = (upgrade, headers = {}, token) =>
    new Promise((resolve, reject) => {
      const method = upgrade ? 'GET' : 'POST';
      const path = upgrade ? '/api/remote.mux' : '/api/llm/listProviders';
      const identity =
        token ??
        signIdentity(keys.privateKey, 'test', { htm: method, htu: path }).token;
      const req = http.request(
        {
          hostname: '127.0.0.1',
          port: server.address().port,
          path,
          method,
          agent: false,
          headers: {
            Origin: 'https://instance.i.example.com',
            'Auriga-Identity': identity,
            ...(upgrade ? { Connection: 'Upgrade', Upgrade: 'websocket' } : {}),
            ...headers,
          },
        },
        (res) => {
          res.resume();
          resolve(res.statusCode);
        },
      );
      req.on('upgrade', (res, socket) => {
        socket.on('error', () => {});
        socket.end();
        resolve(res.statusCode);
      });
      req.on('error', reject);
      req.end();
    });
  for (const upgrade of [false, true]) {
    assert.equal(await request(upgrade), upgrade ? 101 : 200);
    assert.equal(
      await request(upgrade, { Origin: 'https://attacker.example.com' }),
      403,
    );
    assert.equal(
      await request(upgrade, { 'Sec-Fetch-Site': 'cross-site' }),
      403,
    );
    assert.equal(await request(upgrade, {}, 'invalid'), 401);
    const method = upgrade ? 'GET' : 'POST';
    const htu = upgrade ? '/api/remote.mux' : '/api/llm/listProviders';
    for (const claims of [{ aud: 'other-instance' }, { sub: 'other-owner' }]) {
      const token = signIdentity(keys.privateKey, 'test', {
        htm: method,
        htu,
        ...claims,
      }).token;
      assert.equal(await request(upgrade, {}, token), 401);
    }
    const token = signIdentity(keys.privateKey, 'test', {
      htm: method,
      htu,
    }).token;
    assert.equal(await request(upgrade, {}, token), upgrade ? 101 : 200);
    assert.equal(await request(upgrade, {}, token), 401);
  }
  assert.equal(nativeCalls, 0);
  // A copied property or a valid-looking header is not the door's WeakSet grant.
  assert.deepEqual(connection.admit({ __aurigaIdentity: {}, headers: {} }), {
    rejection: 403,
  });
  assert.equal(nativeCalls, 1);
  lifetime.abort();
  assert.equal(await request(false), 401);
  assert.equal(await request(true), 401);
  restore();
  assert.equal(connection.admit, original);
});

test('Cordis original service receives the patch; missing and non-owner Peers fail closed', () => {
  const req = {
    headers: {
      host: '127.0.0.1:7860',
      origin: 'https://instance.i.example.com',
    },
  };
  const lifetime = new globalThis.AbortController();
  const original = () => ({ rejection: 401 });
  const target = {
    admit: original,
    requestRejection: () => 401,
    authorizeIndex: () => false,
  };
  const proxy = { [Symbol.for('cordis.original')]: target };
  const restore = installHostAuthentication(
    proxy,
    { isAuthenticated: (value) => value === req },
    { browserOrigin },
  );
  assert.deepEqual(target.admit(req), { rejection: 401 });
  target.browserPeer = { principal: { kind: 'owner' } };
  assert.deepEqual(target.admit(req), { rejection: 401 });
  target.browserPeer = {
    principal: { kind: 'remote' },
    signal: lifetime.signal,
  };
  assert.deepEqual(target.admit(req), { rejection: 401 });
  target.browserPeer = {
    principal: { kind: 'owner' },
    signal: lifetime.signal,
  };
  assert.equal(target.admit(req).peer, target.browserPeer);
  assert.equal(target.requestRejection(req), undefined);
  assert.equal(target.authorizeIndex(req, {}), true);
  lifetime.abort();
  assert.equal(target.requestRejection(req), 401);
  let status;
  assert.equal(
    target.authorizeIndex(req, {
      writeHead(code) {
        status = code;
      },
      end() {},
    }),
    false,
  );
  assert.equal(status, 401);
  restore();
  assert.equal(target.admit, original);
});
