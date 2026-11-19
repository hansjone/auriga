import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { createSharedRuntime } from '../../src/shared/runtime.js';
import { canonical } from '../../src/shared/protocol.js';
import { createDedicatedHost } from '../../src/dedicated/host.js';
import { test, load } from './public.js';

const request = (endpoint, args) => ({
  channel: '/api',
  endpoint,
  payload: { args },
});
async function setup(t) {
  const [
    { Context },
    { Peer },
    { default: Registry },
    { default: Gateway },
    { RemoteAccessService },
  ] = await Promise.all([
    load('cordis'),
    load('dsh-client-connection'),
    load('dsh-typert-registry'),
    load('dsh-api-gateway'),
    load('dsh-api-remote-access'),
  ]);
  const ctx = new Context();
  new Registry(ctx);
  new RemoteAccessService(ctx);
  const gateway = new Gateway(ctx, {
    websocketHeartbeatIntervalMs: 1000,
    streamInboxBytes: 262144,
  });
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId: randomUUID(),
    mode: 'dedicated',
    epoch: 1,
    revision: 1,
  };
  const key = generateKeyPairSync('ed25519'),
    rows = new Map(),
    audit = [];
  const state = {
    binding,
    platformSessionId: randomUUID(),
    authGeneration: 1,
    active: true,
  };
  const ports = {
    resolve: async () => state,
    lookup: async (id) => {
      if (!rows.has(id))
        throw Object.assign(new Error('missing'), { code: 'common.not_found' });
      return rows.get(id);
    },
    register: async (row) => {
      audit.push('register');
      rows.set(row.sessionId, row);
      return row;
    },
    createPeer: (options) => new Peer(ctx, options),
    sessions: {
      prepare: async (id) => {
        audit.push('prepare');
        return { id };
      },
      publish: async () => {
        audit.push('publish');
      },
      discard: async () => {},
    },
  };
  const runtime = createSharedRuntime({
    ports,
    identityKeys: new Map([['test', key.publicKey]]),
    capabilityKeys: {},
    physicalInstanceId: binding.physicalInstanceId,
    expectedMode: 'dedicated',
    now: () => 1000,
  });
  const host = createDedicatedHost(ctx, {
    runtime,
    ports,
    binding,
    resources: { verify: async () => {} },
  });
  const issue = (override = {}) => {
    const claims = {
      schemaVersion: 2,
      issuer: 'auriga.edge',
      audience: 'auriga.shared.ingress',
      binding: { ...binding },
      platformSessionId: state.platformSessionId,
      method: 'POST',
      canonicalPath: '/auriga/dedicated/browser/rpc',
      jti: randomUUID(),
      issuedAt: 1000,
      expiresAt: 1030,
      ...override,
    };
    const raw = [
      { alg: 'EdDSA', kid: 'test', typ: 'auriga-shared-identity+jwt' },
      claims,
    ]
      .map((part) => Buffer.from(canonical(part)).toString('base64url'))
      .join('.');
    return (
      raw +
      '.' +
      sign(null, Buffer.from(raw), key.privateKey).toString('base64url')
    );
  };
  const admit = (token = issue()) =>
    host.admit(token, {
      method: 'POST',
      canonicalPath: '/auriga/dedicated/browser/rpc',
    });
  t.after(async () => {
    await host.close();
    runtime.close();
    await ctx.fiber.dispose();
  });
  return {
    ctx,
    gateway,
    binding,
    rows,
    audit,
    state,
    host,
    runtime,
    issue,
    admit,
  };
}

test('fresh dedicated admission maps only the pinned member, retains actual native Peer and K precedes publish', async (t) => {
  const f = await setup(t),
    row = await f.admit(),
    signal = new AbortController().signal;
  assert.equal(row.native.principal.kind, 'owner');
  assert.equal((await f.admit()).native, row.native);
  const created = await f.host.call(
    row,
    request('session/create', { request: {} }),
    signal,
  );
  assert.equal(created.ok, true);
  assert.deepEqual(f.audit, ['prepare', 'register', 'publish']);
  for (const extra of [
    { memberId: randomUUID() },
    { cwd: '/' },
    { agentPreset: 'shell' },
  ])
    await assert.rejects(
      f.host.call(row, request('session/create', { request: extra }), signal),
    );
  for (const changed of [
    { mode: 'shared' },
    { memberId: randomUUID() },
    { epoch: 2 },
    { revision: 2 },
  ])
    await assert.rejects(
      f.admit(f.issue({ binding: { ...f.binding, ...changed } })),
    );
  const token = f.issue();
  await f.admit(token);
  await assert.rejects(f.admit(token));
  f.state.active = false;
  await assert.rejects(
    f.host.call(row, request('session/create', { request: {} }), signal),
  );
});

test('real rc.3 Gateway waterfall answers work across fresh HTTP admission and reject unobserved/foreign events', async (t) => {
  const f = await setup(t),
    row = await f.admit(),
    signal = new AbortController().signal;
  const created = await f.host.call(
      row,
      request('session/create', { request: {} }),
      signal,
    ),
    id = created.value.sessionId;
  let answer;
  const agent = { id };
  const detach = f.gateway.registerRemoteEvents(
    async function* (abort) {
      const foreign = { id: randomUUID() };
      yield {
        event: 'approval/request',
        context: { agentId: foreign.id, subject: foreign, value: f.ctx },
        request: { agent: foreign, question: 'FOREIGN' },
        resolve() {},
        reject() {},
      };
      yield {
        event: 'approval/request',
        context: { agentId: id, subject: agent, value: f.ctx },
        request: { agent, question: 'fixture' },
        resolve: (outcome) => {
          answer = outcome;
        },
        reject: (error) => {
          throw error;
        },
      };
      await new Promise((resolve) =>
        abort.addEventListener('abort', resolve, { once: true }),
      );
    },
    { home: '/fixture' },
  );
  t.after(detach);
  const stop = new AbortController(),
    source = await f.host.open(row, request('$events', {}), stop.signal),
    iterator = source[Symbol.asyncIterator]();
  const ready = (await iterator.next()).value,
    event = (await iterator.next()).value;
  assert.equal(ready.type, 'ready');
  assert.equal(event.type, 'waterfall');
  assert.equal(event.agentId, id);
  await assert.rejects(
    f.host.call(
      row,
      request('$events/result', {
        clientId: ready.clientId,
        eventId: randomUUID(),
        outcome: { kind: 'result', value: 'bad' },
      }),
      signal,
    ),
  );
  const next = await f.admit();
  const result = await f.host.call(
    next,
    request('$events/result', {
      clientId: ready.clientId,
      eventId: event.eventId,
      outcome: { kind: 'result', value: 'approved' },
    }),
    signal,
  );
  assert.equal(result.ok, true);
  assert.deepEqual(answer, { kind: 'result', value: 'approved' });
  await assert.rejects(
    f.host.call(
      next,
      request('$events/result', {
        clientId: ready.clientId,
        eventId: event.eventId,
        outcome: { kind: 'result' },
      }),
      signal,
    ),
  );
  stop.abort();
  await iterator.return();
});

test('native resource mutation and unknown session endpoints fail before Gateway, epoch replacement invalidates old Peer', async (t) => {
  const f = await setup(t),
    row = await f.admit(),
    signal = new AbortController().signal;
  for (const endpoint of [
    'pluginManager/installBundle',
    'pluginManager/setBundleEnabled',
    'session/fork',
    'session/restore',
    'session/unknown',
  ])
    await assert.rejects(f.host.call(row, request(endpoint, {}), signal));
  f.state.binding = { ...f.binding, epoch: 2 };
  await assert.rejects(f.host.check(row));
});

test('actual native SessionController list/control remain usable for dedicated owner while K filters foreign history', async (t) => {
  const f = await setup(t),
    row = await f.admit(),
    signal = new AbortController().signal;
  const own = (
    await f.host.call(row, request('session/create', { request: {} }), signal)
  ).value.sessionId;
  const foreign = randomUUID();
  f.rows.set(foreign, {
    ...f.rows.get(own),
    sessionId: foreign,
    memberId: randomUUID(),
  });
  const [{ SessionController }, { TYPERT }, { TypertRemoteService }] =
    await Promise.all([
      load('dsh-api-session-controller'),
      load('dsh-api-session-controller/typert'),
      load('dsh-typert-protocol'),
    ]);
  f.ctx.typert.register(TYPERT);
  // Persistence/projection bytes are fixtures; both controller functions,
  // descriptors, Gateway and private owner Peer are installed rc.3 code.
  class ControllerFixture extends TypertRemoteService {
    constructor(ctx) {
      super(ctx, 'sessionController', { namespace: 'session' });
      this.listState = {
        list: async () =>
          [own, foreign].map((sessionId) => ({
            sessionId,
            updatedAt: 1000,
            blank: true,
            running: false,
            agentAvailable: true,
          })),
      };
      this.controlState = {
        async *control() {
          yield {
            type: 'baseline',
            value: {
              projections: {
                [own]: { asOfSeq: -1, values: {} },
                [foreign]: { asOfSeq: -1, values: {} },
              },
            },
          };
          yield {
            type: 'projection',
            sessionId: foreign,
            key: 'test',
            value: 'FOREIGN',
            seq: 0,
          };
          yield {
            type: 'projection',
            sessionId: own,
            key: 'test',
            value: 'OWN',
            seq: 0,
          };
        },
      };
    }
    list(...args) {
      return SessionController.prototype.list.apply(this, args);
    }
    control(...args) {
      return SessionController.prototype.control.apply(this, args);
    }
  }
  new ControllerFixture(f.ctx);
  const listed = await f.host.call(
    row,
    request('session/list', { _request: {} }),
    signal,
  );
  assert.equal(listed.ok, true, JSON.stringify(listed));
  assert.deepEqual(
    listed.value.items.map((item) => item.sessionId),
    [own],
  );
  const frames = [];
  for await (const frame of await f.host.open(
    row,
    request('session/control', {}),
    signal,
  ))
    frames.push(frame);
  assert.deepEqual(Object.keys(frames[0].value.projections), [own]);
  assert.equal(frames.length, 2);
  assert.equal(frames[1].sessionId, own);
});
