import { mkdtemp, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { createFileLedgerStore } from '../../src/network/host/store.js';
import { openLedger } from '../../src/network/host/ledger.js';
import { readDeviceLedger } from '../../src/network/base-tools.js';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fixture } from './host/native/fixtures.js';
import {
  registerBaseTools,
  BASE_TOOL_NAMES,
  baseStatus,
  connectionReason,
} from '../../src/network/base-tools.js';
import { registerTeamTools } from '../../src/local/team.js';

function setup(t, overrides = {}) {
  const f = fixture();
  const agent = f.attach('session-a');
  const tools = new Map();
  f.ctx.tools = {
    register(tool) {
      tools.set(tool.name, tool);
      return () => tools.delete(tool.name);
    },
  };
  const state = {
    origin: 'https://hq.example.com',
    verified: true,
    expiresAt: Date.now() + 60000,
    memberId: 'alice',
    displayName: 'Alice',
    reachable: true,
    remoteAvailable: true,
    network: { available: true, connected: true },
    accessToken: 'secret-token',
    privateKey: 'secret-key',
    ...overrides,
  };
  const local = {
    identity: { realm: '2', node: '64' },
    bindings: { 1: { bindingId: 'b', hostSessionId: 'session-a' } },
    durable: {
      submissions: { one: { result: null } },
      inbound: Object.fromEntries(
        Array.from({ length: 65 }, (_, i) => [
          String(i),
          {
            recipientId: String(i).padStart(3, '0'),
            hostSessionId: 'session-a',
            bindingId: 'b',
            local: '1',
            stage: 'committed',
            admission: { token: 'secret-admission' },
            delivery: {
              message: { message_id: String(i), body: { text: 'hello' } },
            },
          },
        ]),
      ),
    },
  };
  local.durable.inbound.other = {
    recipientId: 'other',
    hostSessionId: 'other',
    local: '1',
  };
  const calls = [];
  const dispose = registerBaseTools({
    ctx: f.ctx,
    getState: () => state,
    readLocal: async () => local,
    probe: async (path, signal) => {
      calls.push(path);
      assert.ok(signal);
      if (state.reason === 'hq_unreachable')
        throw Object.assign(new Error('secret-path'), { code: 'ECONNREFUSED' });
    },
  });
  t.after(dispose);
  return {
    ...f,
    agent,
    state,
    tools,
    local,
    calls,
    dispose,
    call: (name, args = {}, exec = { agent }) =>
      tools.get('auriga_net_' + name).execute(args, exec),
  };
}
for (const [scenario, overrides] of [
  ['online', {}],
  [
    'offline',
    { reachable: false, remoteAvailable: false, reason: 'hq_unreachable' },
  ],
  ['expired', { expiresAt: 1, remoteAvailable: false }],
  ['degraded', { network: { available: true, connected: false } }],
]) {
  test(`all builtins remain local and sanitized: ${scenario}`, async (t) => {
    const f = setup(t, overrides);
    assert.equal(f.tools.size, 4);
    const status = await f.call('status');
    assert.equal(status.queued, 1);
    if (scenario === 'offline')
      assert.equal(status.remoteTools.reason, 'hq_unreachable');
    if (scenario === 'expired')
      assert.equal(status.credential.state, 'expired');
    if (scenario === 'degraded')
      assert.equal(status.messageNetwork.reason, 'message_network_degraded');
    const identity = await f.call('identity');
    assert.equal(identity.nodeAddress, 'aur1:2.40.0');
    const inbox = await f.call('inbox');
    const diagnose = await f.call('diagnose');
    for (const output of [status, identity, inbox, diagnose])
      assert.doesNotMatch(
        JSON.stringify(output),
        /secret-|accessToken|privateKey|admission|cwd/,
      );
    assert.deepEqual(
      f.calls,
      scenario === 'offline'
        ? ['/actuator/health']
        : ['/actuator/health', '/api/iam/v1/login/providers'],
    );
  });
}
test('inbox bounds and session isolation; local reads require verified credential', async (t) => {
  const f = setup(t);
  const a = await f.call('inbox', { limit: 50 });
  assert.equal(a.items.length, 50);
  assert.equal(a.nextCursor, 50);
  const b = await f.call('inbox', { limit: 50, cursor: a.nextCursor });
  assert.equal(b.items.length, 15);
  assert.equal(b.nextCursor, undefined);
  for (const args of [
    { limit: 51 },
    { limit: 0 },
    { cursor: -1 },
    { cursor: '0' },
    { sessionId: 'other' },
  ])
    await assert.rejects(f.call('inbox', args), /network.protocol_error/);
  f.state.verified = false;
  await assert.rejects(f.call('inbox'), /network.unauthorized/);
  assert.equal((await f.call('identity')).memberId, null);
});
test('fixed diagnose inputs and forged or archived executions fail closed', async (t) => {
  const f = setup(t);
  for (const args of [
    { url: 'https://evil.example.com' },
    { host: 'localhost' },
    { port: 80 },
  ])
    await assert.rejects(f.call('diagnose', args), /network.protocol_error/);
  for (const name of ['status', 'identity', 'inbox', 'diagnose'])
    await assert.rejects(
      f.call(name, {}, { agent: { session: { id: 'session-a' } } }),
    );
  assert.equal(f.calls.length, 0);
  f.ctx.workspaceRegistry.archivedSessionIds.push('session-a');
  await assert.rejects(f.call('status'));
});
test('remote reserved names are refused and logged without reflecting names', () => {
  const registered = [],
    logs = [];
  const names = [
    ...BASE_TOOL_NAMES,
    'auriga_net_future',
    'auriga_send_message',
    'auriga_network_join',
    'auriga_list_endpoints',
  ];
  const dispose = registerTeamTools(
    {
      register: (t) => {
        registered.push(t.name);
        return () => {};
      },
    },
    names.map((name) => ({ name })),
    async () => {},
    { warn: (line) => logs.push(line) },
  );
  assert.deepEqual(registered, ['auriga_list_endpoints']);
  assert.equal(logs.length, 7);
  dispose();
});
test('only explicit evidence maps failure reasons', () => {
  assert.equal(connectionReason({ status: 401 }), 'unauthorized');
  assert.equal(
    connectionReason({ cause: { code: 'ENOTFOUND' } }),
    'hq_unreachable',
  );
  assert.equal(connectionReason(new Error('keyset expired secret')), 'unknown');
  assert.equal(
    baseStatus({ network: { reason: 'keyset_expired' } }).messageNetwork.reason,
    'keyset_expired',
  );
});

test('offline ledger reads preserve the active writer and reject a foreign tenant', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-base-tools-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const claims = {
    iss: 'https://hq.example.com',
    tid: 'tenant-fixture',
    did: 'device-a',
  };
  const key = createHash('sha256')
    .update(JSON.stringify([claims.iss, claims.tid, claims.did]))
    .digest('hex');
  const dir = join(directory, 'devices', key, 'host');
  const ledger = await openLedger({
    store: createFileLedgerStore({ dir }),
    identity: {
      tenant: claims.tid,
      realm: '2',
      node: '64',
      nodeIncarnation: 'inc-1',
    },
    initialize: true,
  });
  try {
    const before = await readFile(join(dir, 'ledger.json'), 'utf8');
    const lock = await readFile(join(dir, 'ledger.lock'), 'utf8');
    assert.equal(
      (await readDeviceLedger(directory, { claims })).identity.node,
      '64',
    );
    assert.equal(await readFile(join(dir, 'ledger.json'), 'utf8'), before);
    assert.equal(await readFile(join(dir, 'ledger.lock'), 'utf8'), lock);
  } finally {
    await ledger.close();
  }
  assert.equal(
    await readDeviceLedger(directory, { claims: { ...claims, did: 'other' } }),
    null,
  );
});
test('native subagent and disposed tool handles cannot read data', async (t) => {
  const f = setup(t);
  f.agent.session.header.origin = 'subagent';
  await assert.rejects(f.call('status'));
  delete f.agent.session.header.origin;
  const tool = f.tools.get('auriga_net_status');
  f.dispose();
  await assert.rejects(
    tool.execute({}, { agent: f.agent }),
    /network.unauthorized/,
  );
});
