// Real NATS TLS WebSocket transport; collab/IAM are explicit contract fixtures.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import tls from 'node:tls';
import { platformConfig } from '../../../src/local/platform.js';
import { openConnectionRuntime } from '../../../src/network/connection-runtime.js';
import { createFileLedgerStore } from '../../../src/network/host/store.js';
import { decodeJson, encodeJson } from '../../../src/network/session/frames.js';
import { fixture as nativeFixture } from '../host/native/fixtures.js';
import {
  publicationFixture,
  signedAnnouncement,
  signedKeyset,
  signedReadyResult,
  testRoots,
} from '../trust/keyset-fixtures.js';
import { startNatsServer } from '../transport/support/nats-server.js';
import { ensureNatsServer } from '../transport/support/nats-binary.js';
import { connectPeer, waitFor } from '../transport/support/client.js';
import { sourcePeer, seal, latch } from './fixtures.js';

test('automatic source lifecycle over real NATS WSS: join, signed issue/Get, native close overtakes Get, hostless revoke, file recovery', async (t) => {
  const root = fileURLToPath(
    new URL('../../../node_modules/.cache/source-wss/', import.meta.url),
  );
  await mkdir(root, { recursive: true });
  const dir = await mkdtemp(join(root, 'run-'));
  const native = nativeFixture(),
    agent = native.attach('session-a');
  const tools = new Map(),
    listeners = new Map();
  let cleanup = () => {};
  native.ctx.tools = {
    register(tool) {
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  native.ctx.on = (event, callback) => {
    listeners.set(event, callback);
    return () => listeners.delete(event);
  };
  native.ctx.inject = (_services, callback) => {
    native.ctx.effect = (effect) => {
      cleanup = effect();
    };
    callback(native.ctx);
    return {
      dispose() {
        cleanup();
      },
    };
  };
  const signer = publicationFixture(),
    fixture = sourcePeer(signer);
  const server = await startNatsServer({ binary: await ensureNatsServer() });
  const installation = platformConfig({
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'source-fixture',
    providerId: 'source-fixture',
    network: { realm: '2', wssUrl: server.url, roots: testRoots },
  });
  // Trust only this isolated test CA/port. Keep default production runtime,
  // transport, Origin, and TLS name/chain verification unchanged.
  const originalTls = tls.connect;
  t.mock.method(tls, 'connect', function (options, ...args) {
    return originalTls.call(
      this,
      Number(options.port) === server.port
        ? { ...options, ca: server.rootPem }
        : options,
      ...args,
    );
  });
  const peer = await connectPeer(server, { token: 'fixture-no-auth-broker' });
  const store = createFileLedgerStore({ dir: join(dir, 'ledger') });
  const connections = new Map(),
    frames = [],
    gate = latch(),
    getEntered = latch();
  let runtime,
    holdGet = false,
    epoch = 0,
    ticket = 0,
    allowRevoke = false;
  t.after(async () => {
    gate.release();
    await runtime?.close();
    await peer.close();
    await server.close();
    assert.ok(
      resolve(dir).startsWith(
        resolve(root) + (process.platform === 'win32' ? '\\' : '/'),
      ),
    );
    await rm(dir, { recursive: true, force: true });
  });
  peer.subscribe('auriga.net.up.*.*', {
    callback(error, message) {
      assert.ifError(error);
      void (async () => {
        const request = decodeJson(message.data),
          ingress = message.subject.split('.').at(-1);
        frames.push(request);
        let result;
        if (request.type === 'ready') {
          const ready = {
            epoch: String(++epoch),
            channelNonce: request.channel_nonce,
            bootId: request.boot_id,
          };
          connections.set(ingress, ready);
          result = signedReadyResult(signer, request, { epoch: ready.epoch });
          if (request.local_high_water !== '0') {
            result.local_high_water = request.local_high_water;
            result.registration_revision = request.registration_revision;
            result = seal(signer, result, ready);
          }
        } else if (request.type === 'local_register') {
          result = seal(
            signer,
            {
              app_version: '1.0',
              type: 'register_result',
              request_id: request.request_id,
              local: request.candidate_local,
              allocation_op_id: request.allocation_op_id,
              address_type: request.address_type,
              registration_id: 'wss-registration',
              result: 'registered',
              registration_revision: '1',
              local_high_water: '1',
            },
            connections.get(ingress),
          );
        } else {
          assert.match(request.type, /^source_(register|get|revoke)$/);
          result = fixture.reply(request, connections.get(ingress));
          if (request.type === 'source_revoke' && !allowRevoke) {
            // A signed enqueue acknowledgment is deliberately NOT an IAM stop
            // confirmation. The plugin must retain its outbox across restart.
            result.lineage_stopped = false;
            result = seal(signer, result, connections.get(ingress));
          }
          if (holdGet && request.type === 'source_get') {
            getEntered.release();
            await gate.promise;
          }
        }
        peer.publish('auriga.net.down.' + ingress, encodeJson(result));
      })().catch((error) => {
        gate.release();
        throw error;
      });
    },
  });
  await peer.flush();
  const prepared = {
    kind: 'device-connection',
    url: installation.network.wssUrl,
    origin: installation.platformOrigin,
    roots: installation.network.roots,
    async register() {
      return {
        tenant: 'tenant-fixture',
        realm: '2',
        node: '64',
        device: 'device-fixture',
        systemAddress: 'aur1:2.40.0',
        created: true,
      };
    },
    async refresh(trust) {
      await trust.announce(signedAnnouncement(signer.payload));
      await trust.update(signedKeyset(signer.payload));
    },
    async getTicket() {
      const ingress = String(++ticket).padStart(26, 'a');
      return {
        auth: { token: 'fixture-no-auth-broker' },
        expiresAtMs: Date.now() + 3600000,
        subjects: {
          upSubject: 'auriga.net.up.a.' + ingress,
          downSubject: 'auriga.net.down.' + ingress,
          inboxPrefix: 'auriga.net.reply.' + ingress,
        },
      };
    },
  };
  async function connect() {
    const result = await openConnectionRuntime({
      kind: 'device-connection',
      prepared,
      ctx: native.ctx,
      directory: dir,
      stores: { ledger: store },
      now: signer.now,
      getAccess: async () => 'unused',
      authorizeLocal: async () => false,
    });
    await result.start();
    return result;
  }
  runtime = await connect();
  await waitFor(() => tools.has('auriga_network_join'), 'join tool');
  assert.equal(
    frames.some((r) => r.type.startsWith('source_')),
    false,
  );
  // First Get is held remotely. Closing the native session must not wait for it.
  holdGet = true;
  assert.deepEqual(
    await tools.get('auriga_network_join').execute({}, { agent }),
    { status: 'registered', address: 'aur1:2.40.1' },
  );
  await getEntered.promise;
  assert.equal(fixture.requests[0].mode, 'issue');
  native.sessions.delete('session-a');
  native.agents.delete('session-a');
  listeners.get('session/disposed')({ id: 'session-a' });
  await waitFor(
    () => frames.some((r) => r.type === 'source_revoke'),
    'reserved revoke lane over WSS',
  );
  assert.equal(
    frames.some((r) => r.type === 'send'),
    false,
  );
  gate.release();
  await runtime.close();
  let handle = await store.open();
  const snapshot = await handle.load();
  await handle.close();
  const original = Object.values(snapshot.sourceControl.revocations)[0];
  assert.equal(original.receipt, null);
  assert.equal(snapshot.sourceControl.entries['1'].stopped, true);
  assert.equal(snapshot.sourceControl.entries['1'].observation, null);
  assert.deepEqual([...tools.keys()], []);
  allowRevoke = true;
  holdGet = false;
  runtime = await connect();
  await waitFor(async () => {
    // The runtime owns the writer; inspect the actual persisted file read-only.
    const { readFile } = await import('node:fs/promises');
    const disk = JSON.parse(
      await readFile(join(dir, 'ledger/ledger.json'), 'utf8'),
    );
    return Object.values(disk.sourceControl.revocations)[0].receipt
      ?.lineage_stopped;
  }, 'hostless file-outbox recovery over WSS');
  const revocations = frames.filter((r) => r.type === 'source_revoke');
  assert.ok(revocations.length >= 2);
  assert.equal(revocations.at(-1).operation_id, original.id);
  assert.equal('native_witness' in revocations.at(-1), false);
  assert.equal(frames.filter((r) => r.type === 'source_register').length, 1);
  assert.equal(
    fixture.requests.some((r) => JSON.stringify(r).includes('session-a')),
    false,
  );
});
