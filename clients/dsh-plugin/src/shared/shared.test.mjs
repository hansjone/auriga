import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID, generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { canonical, SHARED_RISK, verifyToken } from './protocol.js';
import { createSharedRuntime } from './runtime.js';
import { createEnterpriseAdapter } from './enterprise.js';
import { installSharedHost } from './host.js';
import { createSharedCarrier } from './carrier.js';

function setup() {
  const key = generateKeyPairSync('ed25519'),
    keys = new Map([['test', key.publicKey]]);
  const physicalInstanceId = randomUUID(),
    tenantId = randomUUID();
  const bindings = [0, 1].map(() => ({
    tenantId,
    physicalInstanceId,
    memberId: randomUUID(),
    placementId: randomUUID(),
    mode: 'shared',
    epoch: 1,
    revision: 1,
  }));
  const states = bindings.map((binding) => ({
    binding,
    active: true,
    platformSessionId: randomUUID(),
    authGeneration: 1,
    credentialVersion: 1,
    allowRawAgentToken: true,
    authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
  }));
  const rows = new Map(),
    tasks = new Map(),
    audit = [],
    events = [];
  let clock = 1000,
    raw = true;
  const stateFor = (b) =>
    states.find((s) => s.binding.placementId === b.placementId);
  const signed = (c, type = 'capability') => {
    const input = [
      { alg: 'EdDSA', kid: 'test', typ: `auriga-shared-${type}+jwt` },
      c,
    ]
      .map((v) => Buffer.from(canonical(v)).toString('base64url'))
      .join('.');
    return (
      input +
      '.' +
      sign(null, Buffer.from(input), key.privateKey).toString('base64url')
    );
  };
  const ports = {
    async resolve(b) {
      return stateFor(b);
    },
    async register(b, parent) {
      events.push('register');
      if (parent) assert.equal(rows.get(parent).memberId, b.memberId);
      rows.set(b.sessionId, b);
      return b;
    },
    async lookup(id) {
      return rows.get(id);
    },
    async authorizeTask(c) {
      return { ...stateFor(c.binding), active: tasks.get(c.taskId) === c.jti };
    },
    async resourceView(b) {
      return {
        schemaVersion: 1,
        binding: b,
        source: 'platform',
        viewId: randomUUID(),
        authorizationSetDigest: stateFor(b).authorizationSetDigest,
        resources: [],
      };
    },
  };
  const runtime = createSharedRuntime({
    ports,
    identityKeys: keys,
    capabilityKeys: keys,
    physicalInstanceId,
    now: () => clock,
  });
  async function peer(i) {
    const s = states[i];
    return runtime.admit(
      signed(
        {
          schemaVersion: 2,
          issuer: 'auriga.edge',
          audience: 'auriga.shared.ingress',
          binding: s.binding,
          platformSessionId: s.platformSessionId,
          method: 'POST',
          canonicalPath: '/shared',
          issuedAt: clock,
          expiresAt: clock + 60,
          jti: randomUUID(),
        },
        'identity',
      ),
      { method: 'POST', canonicalPath: '/shared' },
      (principal) => ({ principal }),
    );
  }
  const host = {
    prepare: async (id) => ({ id }),
    publish: async () => {
      events.push('publish');
    },
    discard: async () => {
      events.push('discard');
    },
  };
  function token(b, extra = {}) {
    const c = {
      schemaVersion: 1,
      binding: b,
      taskId: randomUUID(),
      audience: 'auriga.shared.enterprise',
      scopes: ['outbound', 'credentials'],
      credentialVersion: stateFor(b).credentialVersion,
      issuedAt: clock,
      expiresAt: clock + 600,
      jti: randomUUID(),
      ...extra,
    };
    tasks.set(c.taskId, c.jti);
    return signed(c);
  }
  const enterprisePorts = {
    taskId: async (t) => verifyToken(t, 'Capability', keys).taskId,
    credentials: async (b) => ({
      empNo: b.memberId,
      token: 'runtime-material-' + stateFor(b).credentialVersion,
      credentialVersion: stateFor(b).credentialVersion,
      updatedAt: null,
    }),
    outbound: async (req, material, b) => {
      audit.push(b.memberId);
      return {
        statusCode: 200,
        headers: { 'set-cookie': 'forbidden' },
        body: material.token,
        json: { actor: material.empNo, application: new URL(req.url).hostname },
      };
    },
  };
  const enterprise = createEnterpriseAdapter({
    runtime,
    ports: enterprisePorts,
    config: () => ({
      allowRawAgentToken: raw,
      outboundAllowedHosts: ['app-one.invalid', 'app-two.invalid'],
    }),
  });
  const call = (b, t, url = 'https://app-one.invalid/api') => ({
    remoteAddress: '127.0.0.1',
    body: { sessionId: b.sessionId, taskCapability: t, url },
  });
  return {
    runtime,
    peer,
    host,
    token,
    states,
    ports,
    rows,
    events,
    enterprise,
    enterprisePorts,
    call,
    audit,
    keys,
    signed,
    setRaw: (v) => (raw = v),
    setClock: (v) => (clock = v),
  };
}
test('packaged K schema has no drift', () => {
  assert.deepEqual(
    JSON.parse(
      readFileSync(new URL('./contract.schema.json', import.meta.url)),
    ),
    JSON.parse(
      readFileSync(
        new URL(
          '../../../../contracts/shared-dsh/shared-dsh.schema.json',
          import.meta.url,
        ),
      ),
    ),
  );
});
test('two members: register before publish, child inheritance, restore and strict actions', async () => {
  const f = setup(),
    a = await f.peer(0),
    b = await f.peer(1);
  const sa = await f.runtime.create(a, f.host),
    sb = await f.runtime.create(b, f.host);
  assert.deepEqual(f.events, ['register', 'publish', 'register', 'publish']);
  const child = await f.runtime.create(a, f.host, {
    parentSessionId: sa.sessionId,
  });
  assert.equal(child.memberId, sa.memberId);
  await assert.rejects(f.runtime.create(a, f.host, { cwd: '/tmp' }));
  await assert.rejects(
    f.runtime.create(a, f.host, { parentSessionId: sb.sessionId }),
  );
  await assert.rejects(f.runtime.authorize(a, 'session/prompt', sb.sessionId));
  await assert.rejects(f.runtime.authorize(a, 'terminal/create', sa.sessionId));
  await assert.rejects(
    f.runtime.authorize(
      { principal: { kind: 'owner' } },
      'session/page',
      sa.sessionId,
    ),
  );
  assert.equal((await f.runtime.session(sa.sessionId)).memberId, sa.memberId);
  assert.equal((await f.runtime.resources(sa.sessionId)).risk, SHARED_RISK);
});
test('dual enterprise applications, concurrent member binding and response redaction', async () => {
  const f = setup(),
    members = await Promise.all([f.peer(0), f.peer(1)]),
    sessions = await Promise.all(
      members.map((p) => f.runtime.create(p, f.host)),
    );
  const results = await Promise.all(
    sessions.map((b, i) =>
      f.enterprise.outbound(
        f.call(b, f.token(b), `https://app-${i ? 'two' : 'one'}.invalid/api`),
      ),
    ),
  );
  assert.deepEqual(
    results.map((r) => r.json.actor),
    sessions.map((b) => b.memberId),
  );
  assert(
    results.every((r) => r.body === '[REDACTED]' && !r.headers['set-cookie']),
  );
  await assert.rejects(
    f.enterprise.credentials(f.call(sessions[0], f.token(sessions[1]))),
  );
  await assert.rejects(
    f.enterprise.outbound(
      f.call(
        sessions[0],
        f.token(sessions[0]),
        'https://app-one.invalid.evil/api',
      ),
    ),
  );
  await assert.rejects(
    f.enterprise.credentials({
      ...f.call(sessions[0], f.token(sessions[0])),
        remoteAddress: '192.0.2.1',
    }),
  );
  await assert.rejects(
    f.enterprise.credentials({
      ...f.call(sessions[0], f.token(sessions[0])),
      headers: { 'x-dsh-session-id': sessions[1].sessionId },
    }),
  );
});
test('rotation, raw default and next-call disable, task end and late responses', async () => {
  const f = setup(),
    b = await f.runtime.create(await f.peer(0), f.host),
    old = f.token(b);
  assert.equal(
    (await f.enterprise.credentials(f.call(b, old))).token,
    'runtime-material-1',
  );
  f.states[0].credentialVersion = 2;
  await assert.rejects(f.enterprise.credentials(f.call(b, old)));
  const fresh = f.token(b);
  assert.equal(
    (await f.enterprise.credentials(f.call(b, fresh))).token,
    'runtime-material-2',
  );
  f.setRaw(false);
  await assert.rejects(f.enterprise.credentials(f.call(b, fresh)));
  await f.enterprise.outbound(f.call(b, fresh));
  let release;
  const pending = new Promise((r) => (release = r));
  f.enterprisePorts.outbound = async () => {
    await pending;
    return { statusCode: 200, body: 'late' };
  };
  const work = f.enterprise.outbound(f.call(b, fresh));
  await new Promise((r) => setTimeout(r, 10));
  f.states[0].active = false;
  release();
  await assert.rejects(work);
  await assert.rejects(f.enterprise.outbound(f.call(b, fresh)));
});
test('expiry and revoked epochs never resume old sessions', async () => {
  const f = setup(),
    b = await f.runtime.create(await f.peer(0), f.host),
    t = f.token(b);
  f.setClock(1600);
  await assert.rejects(f.enterprise.credentials(f.call(b, t)));
  f.setClock(1000);
  f.runtime.revoke({
    schemaVersion: 1,
    ...Object.fromEntries(
      [
        'tenantId',
        'memberId',
        'placementId',
        'epoch',
        'authGeneration',
        'revision',
      ].map((k) => [k, b[k]]),
    ),
    authGeneration: 2,
    reason: 'logout',
    revokedAt: 1000,
  });
  await assert.rejects(f.runtime.session(b.sessionId));
});
test('accepted counterexample: native owner drives B, business and audit bind B', async () => {
  const f = setup(),
    b = await f.runtime.create(await f.peer(1), f.host);
  // Native owner bypasses self-owned authorize(). A can drive B's task there.
  const nativeTask = f.token(b),
    r = await f.enterprise.outbound(f.call(b, nativeTask));
  assert.equal(r.json.actor, b.memberId);
  assert.deepEqual(f.audit, [b.memberId]);
  assert.match(SHARED_RISK, /审计记在被冒用者名下/);
});
test('host refuses missing profile and guard rejects execution tools', () => {
  const f = setup();
  let guard;
  const ctx = {
    tools: {
      register: () => () => {},
      guard: (fn) => {
        guard = fn;
        return () => {};
      },
    },
    remoteAccess: { registerPolicy: () => () => {} },
    connection: { createPeer: () => {} },
    webServer: { register: () => () => {} },
  };
  assert.throws(() =>
    installSharedHost(ctx, {
      runtime: f.runtime,
      enterprise: f.enterprise,
      ports: {},
    }),
  );
  const installed = installSharedHost(ctx, {
    runtime: f.runtime,
    enterprise: f.enterprise,
    ports: {
      createPeer: () => {},
      assertProfile: (p) => {
        assert.equal(p.shell, false);
        return true;
      },
    },
  });
  for (const name of ['shell', 'terminal', 'jobs', 'subagent', 'unknown'])
    assert.equal(guard({ name }), 'shared-mode-disabled');
  installed.close();
});

test('capability binding, task, audience, scopes, signature and maximum lifetime reject', async () => {
  const f = setup(),
    b = await f.runtime.create(await f.peer(0), f.host);
  for (const extra of [
    { binding: { ...b, memberId: randomUUID() } },
    { binding: { ...b, sessionId: randomUUID() } },
    { binding: { ...b, epoch: 2 } },
    { audience: 'auriga.shared.model', scopes: ['model.invoke'] },
    { scopes: ['outbound', 'model.invoke'] },
    { expiresAt: 1601 },
    { issuedAt: 1001 },
    { credentialVersion: 2 },
    { unexpected: true },
  ])
    await assert.rejects(f.enterprise.outbound(f.call(b, f.token(b, extra))));
  const t = f.token(b);
  await assert.rejects(f.enterprise.outbound(f.call(b, t + 'a')));
  await assert.rejects(
    f.runtime.capability(t, b.sessionId, randomUUID(), 'outbound'),
  );
  f.ports.authorizeTask = async () => ({ active: false, credentialVersion: 1 });
  await assert.rejects(f.enterprise.outbound(f.call(b, t)));
});
test('register failure never publishes and authority outage denies recovery', async () => {
  const f = setup(),
    p = await f.peer(0);
  f.ports.register = async () => {
    throw new Error('unavailable');
  };
  await assert.rejects(f.runtime.create(p, f.host));
  assert.deepEqual(f.events, ['discard']);
  f.ports.resolve = async () => {
    throw new Error('unavailable');
  };
  await assert.rejects(f.runtime.create(p, f.host));
});

test('identity binds method/path, rejects replay, unknown fields and noncanonical JSON', async () => {
  const f = setup(),
    s = f.states[0];
  const claims = {
    schemaVersion: 2,
    issuer: 'auriga.edge',
    audience: 'auriga.shared.ingress',
    binding: s.binding,
    platformSessionId: s.platformSessionId,
    method: 'POST',
    canonicalPath: '/auriga/shared/session',
    issuedAt: 1000,
    expiresAt: 1060,
    jti: randomUUID(),
  };
  const request = { method: 'POST', canonicalPath: claims.canonicalPath },
    peer = (p) => ({ principal: p });
  const t = f.signed(claims, 'identity');
  await assert.rejects(f.runtime.admit(t, { ...request, method: 'GET' }, peer));
  await f.runtime.admit(t, request, peer);
  await assert.rejects(f.runtime.admit(t, request, peer));
  for (const extra of [
    { schemaVersion: 1 },
    { expiresAt: 1061 },
    { canonicalPath: '/a/%2e%2e/b' },
    { extra: true },
    { constructor: 'unknown' },
  ]) {
    await assert.rejects(
      f.runtime.admit(
        f.signed({ ...claims, jti: randomUUID(), ...extra }, 'identity'),
        request,
        peer,
      ),
    );
  }
  const [h, c, sig] = f
    .signed({ ...claims, jti: randomUUID() }, 'identity')
    .split('.');
  const pretty = Buffer.from(
    JSON.stringify(JSON.parse(Buffer.from(c, 'base64url').toString()), null, 2),
  ).toString('base64url');
  await assert.rejects(
    f.runtime.admit([h, pretty, sig].join('.'), request, peer),
  );
});
test('cold restoration uses authority, resource expiry and digest mismatch deny', async () => {
  const f = setup(),
    b = await f.runtime.create(await f.peer(0), f.host);
  const cold = createSharedRuntime({
    ports: f.ports,
    identityKeys: f.keys,
    capabilityKeys: f.keys,
    physicalInstanceId: b.physicalInstanceId,
    now: () => 1000,
  });
  assert.equal((await cold.session(b.sessionId)).memberId, b.memberId);
  await assert.rejects(cold.session(randomUUID()));
  f.ports.resourceView = async (binding) => ({
    schemaVersion: 1,
    binding,
    viewId: randomUUID(),
    source: 'platform',
    authorizationSetDigest: 'sha256:' + 'b'.repeat(64),
    resources: [],
  });
  await assert.rejects(cold.resources(b.sessionId));
  f.ports.resourceView = async (binding) => ({
    schemaVersion: 1,
    binding,
    viewId: randomUUID(),
    source: 'platform',
    authorizationSetDigest: f.states[0].authorizationSetDigest,
    resources: [
      {
        resourceId: randomUUID(),
        leaseId: randomUUID(),
        digest: 'sha256:' + 'a'.repeat(64),
        expiresAt: 1000,
      },
    ],
  });
  await assert.rejects(cold.resources(b.sessionId));
  cold.close();
});
test('real temporary HTTP helper preserves aliases, statuses, raw flag and no-store', async () => {
  const f = setup(),
    b = await f.runtime.create(await f.peer(0), f.host),
    t = f.token(b);
  const server = createServer(f.enterprise.handler);
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  try {
    const base = `http://127.0.0.1:${server.address().port}/dsh-team`;
    const headers = {
      'X-DSH-Session-Id': b.sessionId,
      'X-Dsh-Team-Task-Capability': t,
    };
    let r = await fetch(base + '/agent-credentials', { headers });
    assert.equal(r.status, 200);
    assert.equal(r.headers.get('cache-control'), 'no-store');
    assert.equal((await r.json()).token, 'runtime-material-1');
    f.setRaw(false);
    r = await fetch(base + '/agent-credentials', { headers });
    assert.equal(r.status, 403);
    assert.equal((await r.json()).error, 'raw_token_disabled');
    r = await fetch(base + '/outbound', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        dshSessionId: b.sessionId,
        capability: t,
        url: 'https://app-two.invalid/api',
      }),
    });
    assert.equal(r.status, 200);
    assert.equal(r.headers.get('x-dsh-team-outbound-status'), '200');
    assert.equal((await r.json()).json.application, 'app-two.invalid');
    r = await fetch(base + '/agent-credentials', {
      method: 'POST',
      headers,
      body: JSON.stringify({ sessionId: randomUUID(), taskCapability: t }),
    });
    assert.equal(r.status, 400);
  } finally {
    server.closeAllConnections();
    await new Promise((r) => server.close(r));
  }
});
test('self-owned carrier rejects unknown methods and arbitrary create paths', async () => {
  const f = setup(),
    p = await f.peer(0);
  const host = {
    admit: async () => p,
    create: (peer, body) => f.runtime.create(peer, f.host, body),
  };
  const server = createServer(
    createSharedCarrier({ host, runtime: f.runtime }),
  );
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  try {
    const base = `http://127.0.0.1:${server.address().port}/auriga/shared`;
    let r = await fetch(base + '/unknown', { method: 'POST' });
    assert.equal(r.status, 400);
    r = await fetch(base + '/session', {
      method: 'POST',
      body: JSON.stringify({ cwd: '/foreign' }),
    });
    assert.equal(r.status, 400);
    r = await fetch(base + '/status', { method: 'POST', body: '{}' });
    assert.equal((await r.json()).risk, SHARED_RISK);
    r = await fetch(base + '/session', { method: 'POST', body: '{}' });
    assert.equal(r.status, 200);
  } finally {
    server.closeAllConnections();
    await new Promise((r) => server.close(r));
  }
});
