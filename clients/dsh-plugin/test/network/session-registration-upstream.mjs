// Explicit rc.2 public API + native JSONL + real NATS/WSS integration.
// The remote signer is a fixture; this is not the Java/PG end-to-end proof.
import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { sign } from 'node:crypto';
import { openConnectionRuntime } from '../../src/network/connection-runtime.js';
import {
  createNativeHostPort,
  registrationSession,
} from '../../src/network/host/native/host-port.js';
import { createFileLedgerStore } from '../../src/network/host/store.js';
import { NatsTransport } from '../../src/network/transport/nats-transport.js';
import { signingInput } from '../../src/network/trust/verifier.js';
import { decodeJson, encodeJson } from '../../src/network/session/frames.js';
import {
  publicationFixture,
  signedAnnouncement,
  signedKeyset,
  signedReadyResult,
  testRoots,
} from './trust/keyset-fixtures.js';
import { startNatsServer } from './transport/support/nats-server.js';
import { ensureNatsServer } from './transport/support/nats-binary.js';
import {
  connectPeer,
  createCaSocketFactory,
  waitFor,
} from './transport/support/client.js';

const baseline = new URL(
  '../../../../deploy/compose/real-dsh/',
  import.meta.url,
);
const requireBaseline = createRequire(new URL('package.json', baseline));
const load = (name) =>
  import(pathToFileURL(requireBaseline.resolve(name)).href);
const { Context } = await load('@deepseek-ai/cordis');
const { SessionStore } = await load('@deepseek-ai/dsh-session');
const { AgentRegistry } = await load('@deepseek-ai/dsh-agent');
const { SystemPrompt } = await load('@deepseek-ai/dsh-system-prompt');
const { ToolRuntime } = await load('@deepseek-ai/dsh-tools');
const { default: JsonlPersistence } = await load(
  '@deepseek-ai/dsh-session-persistence-jsonl',
);

test('public rc.2 ToolRuntime joins an existing JSONL Session over real NATS WSS and reopens the same file witness', async (t) => {
  const evidence = fileURLToPath(
    new URL('../../../../.agent-evidence/', import.meta.url),
  );
  await mkdir(evidence, { recursive: true });
  const dir = await mkdtemp(join(evidence, 'public-session-register-'));
  const sessionDir = join(dir, 'sessions');
  await mkdir(sessionDir);
  const ctx = new Context();
  new SystemPrompt(ctx, {});
  const tools = new ToolRuntime(ctx);
  const sessions = new SessionStore(ctx);
  const persistence = new JsonlPersistence(ctx, {
    root: sessionDir,
    compression: 'none',
  });
  const agents = new AgentRegistry(ctx);
  const session = sessions.prepare('existing-session', { meta: { cwd: dir } });
  const detachSession = sessions.enter(session);
  const writer = await persistence.create(session.header);
  const agent = {
    id: session.id,
    session,
    ctx,
    inbox: { nextTurn: [], nextStep: [] },
  };
  const detachAgent = await agents.register(agent);
  let creates = 0,
    prompts = 0;
  ctx.provide('workspaceRegistry', { archivedSessionIds: [] });
  const detachController = ctx.provide('sessionController', {
    async inspect(id) {
      const reader = await persistence.open(id, 'read');
      try {
        return { meta: reader.header, events: (await reader.read()).events };
      } finally {
        await reader.close();
      }
    },
    async resolveAgent(id) {
      return { agent: agents.get(id) };
    },
    create() {
      creates++;
      throw new Error('registration must not create');
    },
    prompt() {
      prompts++;
      throw new Error('registration must not prompt');
    },
  });
  const signer = publicationFixture();
  const server = await startNatsServer({ binary: await ensureNatsServer() });
  const peer = await connectPeer(server, { token: 'fixture-no-auth-broker' });
  let runtime;
  t.after(async () => {
    await runtime?.close();
    await peer.close();
    await server.close();
    await writer.close();
    await detachAgent();
    detachSession();
    assert.equal(
      resolve(dir).startsWith(resolve(evidence) + '\\') ||
        resolve(dir).startsWith(resolve(evidence) + '/'),
      true,
    );
    await rm(dir, { recursive: true, force: true });
  });
  const ingresses = new Map(),
    requests = [];
  peer.subscribe('auriga.net.up.*.*', {
    callback(error, message) {
      assert.ifError(error);
      const request = decodeJson(message.data);
      const ingress = message.subject.split('.').at(-1);
      requests.push(request);
      let result;
      if (request.type === 'ready') {
        const state = {
          epoch: String(ingresses.size + 1),
          nonce: request.channel_nonce,
        };
        ingresses.set(ingress, state);
        result = signedReadyResult(signer, request, { epoch: state.epoch });
      } else {
        assert.equal(request.type, 'local_register');
        const state = ingresses.get(ingress);
        result = {
          app_version: '1.0',
          type: 'register_result',
          request_id: request.request_id,
          local: request.candidate_local,
          allocation_op_id: request.allocation_op_id,
          address_type: request.address_type,
          registration_id: 'registration-wss',
          result: 'registered',
          registration_revision: '1',
          local_high_water: '1',
          server_auth: {
            version: 1,
            purpose: 'protocol_response',
            alg: 'Ed25519',
            kid: 'response',
            issuer_realm: '2',
            issued_at: signer.payload.issued_at,
            not_after: new Date(signer.now() + 120000)
              .toISOString()
              .replace('.000Z', 'Z'),
            audience: {
              realm: '2',
              node: '64',
              incarnation: 'inc-1',
              epoch: state.epoch,
            },
            request_id: request.request_id,
            channel_nonce: state.nonce,
            keyset_version: '5',
          },
        };
        result.server_auth.signature = sign(
          null,
          signingInput(result),
          signer.pair.privateKey,
        ).toString('base64url');
      }
      peer.publish('auriga.net.down.' + ingress, encodeJson(result));
    },
  });
  await peer.flush();
  const ledgerStore = createFileLedgerStore({ dir: join(dir, 'ledger') });
  let ticket = 0;
  const prepared = {
    kind: 'device-connection',
    url: server.url,
    roots: testRoots,
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
  runtime = await openConnectionRuntime({
    kind: 'device-connection',
    prepared,
    ctx,
    directory: join(dir, 'network'),
    stores: { ledger: ledgerStore },
    now: signer.now,
    getAccess: async () => 'unused',
    authorizeLocal: async () => false,
    transportFactory(options) {
      return new NatsTransport({
        ...options,
        createSocket: createCaSocketFactory(server.rootPem),
      });
    },
  });
  await runtime.start();
  await waitFor(() => tools.get('auriga_network_join'), 'public registry join');
  const host = await createNativeHostPort({ ctx });
  await registrationSession(ctx, { agent }).verify();
  assert.equal((await host.getSession(session.id)).cwd, dir);
  assert.equal(await host.hasDurableProvider(session.id), true);
  assert.equal(await host.flush(session.id), true);
  const execution = {
    callId: 'join-public',
    name: 'auriga_network_join',
    arguments: {},
    agent,
    signal: new AbortController().signal,
  };
  const result = await tools.execute(execution);
  assert.equal(result.isError, false, JSON.stringify(result));
  assert.deepEqual(result.value, {
    status: 'registered',
    address: 'aur1:2.40.1',
  });
  assert.deepEqual(
    (await tools.execute({ ...execution, callId: 'join-again' })).value,
    result.value,
  );
  assert.deepEqual(
    requests.map((request) => request.type),
    ['ready', 'ready', 'local_register'],
  );
  assert.equal(creates, 0);
  assert.equal(prompts, 0);
  const staleTool = tools.get('auriga_network_join');
  await detachController();
  await waitFor(
    () => !tools.get('auriga_network_join'),
    'public Host unload unregisters join',
  );
  assert.equal(runtime.status.connected, true);
  assert.deepEqual(await staleTool.execute({}, execution), {
    status: 'unavailable',
  });
  await runtime.close();
  assert.equal(tools.get('auriga_network_join'), undefined);
  const h = await ledgerStore.open();
  const state = await h.load();
  await h.close();
  assert.equal(state.bindings['1'].hostSessionId, session.id);
  assert.equal(state.locals['1'].registrationId, 'registration-wss');
  assert.equal(
    state.locals['1'].fingerprint,
    requests.at(-1).allocation_fingerprint.slice(7),
  );
});
