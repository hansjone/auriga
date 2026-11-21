import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { setImmediate } from 'node:timers';
import { SHARED_PROFILE } from '../../src/shared/host.js';
import { assertSharedProfile } from '../../src/shared/profile-state.js';
import { createProductionSharedPorts } from '../../src/shared/production-ports.js';
import {
  createSharedRuntime,
  placementBinding,
} from '../../src/shared/runtime.js';
import { openSharedTask } from '../../src/shared/task-context.js';
import { createEnterpriseAdapter } from '../../src/shared/enterprise.js';
import { canonical } from '../../src/shared/protocol.js';

function fixture({ profileValidator, available } = {}) {
  const key = generateKeyPairSync('ed25519');
  const keys = new Map([['test', key.publicKey]]);
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId: randomUUID(),
    mode: 'shared',
    epoch: 1,
    revision: 1,
    sessionId: randomUUID(),
    platformSessionId: randomUUID(),
    authGeneration: 1,
  };
  const agent = { id: binding.sessionId, session: { id: binding.sessionId } };
  const listeners = new Map();
  const registry = new Map([
    [
      function tools() {},
      { name: 'tools', fibers: [{ uid: 1, config: { mode: 'native' } }] },
    ],
  ]);
  const services = new Map();
  const events = [];
  const activeTasks = new Map();
  const f = {
    binding,
    agent,
    events,
    activeTasks,
    registry,
    services,
    now: Math.floor(Date.now() / 1000),
    version: 1,
    raw: true,
    live: true,
  };
  const settings = () => ({
    profile: SHARED_PROFILE,
    skillRoots: [],
    model: 'synthetic-model',
    enterprise: {
      allowRawAgentToken: f.raw,
      outboundAllowedHosts: ['app-one.invalid'],
    },
  });
  const ctx = {
    registry,
    reflect: { get: (name) => services.get(name) },
    sessions: {
      get: (id) => (id === agent.session.id ? agent.session : undefined),
    },
    agents: {
      get: (id) => (id === agent.id ? agent : undefined),
      currentInitiator: () => agent,
    },
    on(name, listener) {
      listeners.set(name, listener);
      return () => listeners.delete(name);
    },
    webServer: { port: 41234 },
  };
  class Peer {
    constructor(_ctx, options) {
      this.principal = options.principal;
      this.interactions = options.interactions;
      this.controller = new AbortController();
      this.signal = this.controller.signal;
    }
    async dispose() {
      this.controller.abort();
    }
  }
  const token = (request) => {
    const capability = {
      schemaVersion: 1,
      binding: request.binding,
      taskId: request.taskId,
      audience: request.audience,
      scopes: request.scopes,
      credentialVersion: f.version,
      issuedAt: f.now,
      expiresAt: f.now + 600,
      jti: randomUUID(),
    };
    const text = [
      { alg: 'EdDSA', kid: 'test', typ: 'auriga-shared-capability+jwt' },
      capability,
    ]
      .map((value) => Buffer.from(canonical(value)).toString('base64url'))
      .join('.');
    return (
      text +
      '.' +
      sign(null, Buffer.from(text), key.privateKey).toString('base64url')
    );
  };
  const bridge = {
    async call(operation, request, signal) {
      events.push(operation);
      if (operation === 'lookup') return { binding: { ...f.binding } };
      if (operation === 'resolve') {
        if (!f.live) throw new Error('shared.revoked');
        return {
          state: 'ready',
          binding: placementBinding(f.binding),
          authGeneration: f.binding.authGeneration,
        };
      }
      if (operation === 'resourceSnapshot')
        return {
          view: {
            schemaVersion: 1,
            binding: placementBinding(f.binding),
            viewId: randomUUID(),
            source: 'platform',
            authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
            resources: [],
          },
        };
      if (operation === 'beginTask') {
        if (f.beforeBegin) await f.beforeBegin();
        activeTasks.set(request.taskId, request.binding);
        return { token: token(request) };
      }
      if (operation === 'issueCapability') {
        if (!f.live || !activeTasks.has(request.taskId))
          throw new Error('shared.revoked');
        const result = { token: token(request) };
        await f.afterIssue?.();
        return result;
      }
      if (operation === 'finishTask') {
        activeTasks.delete(request.taskId);
        return {
          fenced: true,
          binding: request.binding,
          taskId: request.taskId,
        };
      }
      if (operation === 'authorizeCapability')
        return {
          active: f.live && activeTasks.has(request.capability.taskId),
          credentialVersion: f.version,
          allowRawAgentToken: f.raw,
        };
      if (operation === 'readCredentials') {
        if (f.denyRead) assert.fail('outbound must never request raw material');
        const value = {
          empNo: '00001234',
          token: 'synthetic-material-' + f.version,
          credentialVersion: f.version,
        };
        f.afterRead?.();
        return value;
      }
      if (operation === 'enterpriseOutbound') {
        f.lastOutbound = request;
        if (f.outboundHandler) return f.outboundHandler(request, signal);
        return {
          statusCode: 200,
          headers: { 'content-type': 'application/json' },
          body: '{"ok":true,"token":"[REDACTED]"}',
        };
      }
      throw new Error('Unexpected synthetic operation');
    },
    close() {
      events.push('closed');
    },
  };
  const options = {
    tenantId: binding.tenantId,
    physicalInstanceId: binding.physicalInstanceId,
    identityKeys: keys,
    capabilityKeys: {
      'auriga.shared.enterprise': keys,
      'auriga.shared.model': keys,
      'auriga.shared.message': keys,
    },
  };
  const ports = createProductionSharedPorts(ctx, {
    settings,
    bridge,
    Peer,
    options,
    profileValidator,
    available,
  });
  const runtime = createSharedRuntime({ ...options, ports, now: () => f.now });
  const enterprise = createEnterpriseAdapter({
    runtime,
    ports: ports.enterprise,
    config: ports.config,
  });
  return { ...f, state: f, ctx, ports, runtime, enterprise, listeners };
}

test('missing optional model and message relays stay local unavailable without blocking core ports', async () => {
  const core = [
    'health',
    'resolve',
    'register',
    'lookup',
    'resourceSnapshot',
    'beginTask',
    'issueCapability',
    'finishTask',
    'authorizeCapability',
    'readCredentials',
    'enterpriseOutbound',
  ];
  const f = fixture({ available: core });
  try {
    const before = f.events.length;
    assert.throws(() => f.ports.downstream.model({}), /common\.unavailable/);
    await assert.rejects(
      f.ports.downstream.send({ messageId: randomUUID() }),
      /common\.unavailable/,
    );
    assert.deepEqual(f.events.slice(before), []);
    assert.equal(
      (await f.ports.lookup(f.binding.sessionId)).tenantId,
      f.binding.tenantId,
    );
    assert.deepEqual(f.events.slice(before), ['lookup']);
  } finally {
    await f.ports.close();
  }
});

test('public registry configuration attests native tools, fixed skill roots and unloaded controllers', async () => {
  const f = fixture();
  assert.equal(assertSharedProfile(f.ctx, SHARED_PROFILE, []), true);
  f.services.set('terminal', {});
  assert.equal(assertSharedProfile(f.ctx, SHARED_PROFILE, []), false);
  f.services.clear();
  const tools = [...f.registry.values()][0];
  tools.fibers[0].config.mode = 'ptc';
  assert.equal(assertSharedProfile(f.ctx, SHARED_PROFILE, []), false);
  tools.fibers[0].config.mode = 'native';
  f.registry.set(function skills() {}, {
    name: 'skill-filesystem',
    fibers: [
      {
        uid: 2,
        config: {
          providerName: 'auriga-shared',
          includeDefaultRoots: false,
          customSkillDirs: ['/fixed'],
        },
      },
    ],
  });
  assert.equal(assertSharedProfile(f.ctx, SHARED_PROFILE, ['/fixed']), true);
  assert.equal(assertSharedProfile(f.ctx, SHARED_PROFILE, ['/other']), false);
  await f.ports.close();
  f.runtime.close();
});

test('profile factory injection is internal, synchronous and strict; shared default remains unchanged', async () => {
  const dedicated = Object.freeze({ mode: 'dedicated' }),
    plain = fixture();
  try {
    assert.equal(plain.ports.assertProfile(SHARED_PROFILE), true);
    assert.equal(plain.ports.assertProfile(dedicated), false);
  } finally {
    await plain.ports.close();
    plain.runtime.close();
  }
  const calls = [],
    custom = fixture({
      profileValidator: (ctx, expected, roots) => {
        calls.push({ ctx, expected, roots });
        return expected === dedicated;
      },
    });
  try {
    assert.equal(custom.ports.assertProfile(dedicated), true);
    assert.equal(custom.ports.assertProfile(SHARED_PROFILE), false);
    assert.equal(calls[0].ctx, custom.ctx);
    assert.deepEqual(calls[0].roots, []);
  } finally {
    await custom.ports.close();
    custom.runtime.close();
  }
  for (const value of [null, true, {}, 'validator-from-yaml'])
    assert.throws(() => fixture({ profileValidator: value }));
  for (const value of [
    () => Promise.resolve(true),
    () => Promise.reject(new Error('async rejected')),
    () => 1,
    () => ({ valid: true }),
  ]) {
    const f = fixture({ profileValidator: value });
    try {
      assert.equal(f.ports.assertProfile(dedicated), false);
    } finally {
      await f.ports.close();
      f.runtime.close();
    }
  }
});

test('a late capability renewal cannot resurrect a finished native execution', async () => {
  const f = fixture(),
    execution = {
      agent: f.agent,
      token: Symbol('native-fixture'),
      signal: new AbortController().signal,
    };
  const parent = await f.ports.acquireExecution(f.binding, execution);
  const token = await f.ports.issueTask(
    f.binding,
    execution,
    ['outbound'],
    parent,
  );
  f.state.afterIssue = () => f.ports.finishTask(f.binding, execution);
  await assert.rejects(
    f.ports.enterprise.refreshCapability(
      token,
      f.binding.sessionId,
      'outbound',
    ),
  );
  assert.equal(f.activeTasks.size, 1);
  await assert.rejects(
    f.ports.enterprise.refreshCapability(
      token,
      f.binding.sessionId,
      'outbound',
    ),
  );
  await parent.close();
  assert.equal(f.activeTasks.size, 0);
  await f.ports.close();
  f.runtime.close();
});

test('task-local uppercase variables preserve leading zeroes and a running helper renews its own material', async () => {
  const f = fixture();
  const execution = {
    agent: f.agent,
    token: Symbol('native-fixture'),
    signal: new AbortController().signal,
  };
  const inherited = Object.hasOwn(process.env, 'DSH_AURIGA_TOKEN');
  const task = await openSharedTask({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution,
    raw: true,
  });
  try {
    assert.equal(task.env.DSH_AURIGA_ID, '00001234');
    assert.equal(task.env.DSH_AURIGA_TOKEN, 'synthetic-material-1');
    assert.equal(Object.hasOwn(process.env, 'DSH_AURIGA_TOKEN'), inherited);
    f.state.now += 601;
    await assert.rejects(
      f.runtime.capability(
        task.token,
        f.binding.sessionId,
        f.ports.enterprise.taskId(task.token),
        'credentials',
      ),
      { code: 'shared.expired' },
    );
    f.state.version = 2;
    const value = await f.enterprise.credentials({
      remoteAddress: '127.0.0.1',
      body: {
        dshSessionId: f.binding.sessionId,
        taskCapability: task.token,
      },
    });
    assert.equal(value.empNo, '00001234');
    assert.equal(value.token, 'synthetic-material-2');
    assert.equal(task.env.DSH_AURIGA_TOKEN, 'synthetic-material-1');
    await task.close();
    assert.equal(f.activeTasks.size, 0);
    await assert.rejects(
      f.enterprise.credentials({
        remoteAddress: '127.0.0.1',
        body: {
          dshSessionId: f.binding.sessionId,
          taskCapability: task.token,
        },
      }),
    );
  } finally {
    await task.close();
    await f.ports.close();
    f.runtime.close();
  }
});

test('late material is discarded after rotation, logout, or a different account binding', async () => {
  const f = fixture();
  const execution = {
    agent: f.agent,
    token: Symbol('native-fixture'),
    signal: new AbortController().signal,
  };
  const task = await openSharedTask({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution,
    raw: true,
  });
  const call = {
    remoteAddress: '127.0.0.1',
    body: {
      dshSessionId: f.binding.sessionId,
      taskCapability: task.token,
    },
  };
  try {
    f.state.afterRead = () => f.state.version++;
    await assert.rejects(f.enterprise.credentials(call));
    f.state.afterRead = undefined;
    f.state.live = false;
    await assert.rejects(f.enterprise.credentials(call));
    f.state.live = true;
    f.state.binding = { ...f.binding, memberId: randomUUID() };
    const before = f.events.filter((item) => item === 'issueCapability').length;
    await assert.rejects(f.enterprise.credentials(call));
    assert.equal(
      f.events.filter((item) => item === 'issueCapability').length,
      before,
    );
  } finally {
    await task.close();
    await f.ports.close();
    f.runtime.close();
  }
});

test('abandoned begin response is compensated with the same trusted task id', async () => {
  const f = fixture();
  const controller = new AbortController();
  const execution = {
    agent: f.agent,
    token: Symbol('native-fixture'),
    signal: controller.signal,
  };
  f.state.beforeBegin = async () => controller.abort();
  await assert.rejects(f.ports.issueExecution(f.binding, execution));
  assert.equal(f.activeTasks.size, 0);
  assert.ok(f.events.includes('finishTask'));
  await assert.rejects(
    f.ports.issueTask(f.binding, { agent: { ...f.agent } }, ['outbound']),
  );
  f.state.beforeBegin = undefined;
  await f.ports.issueExecution(f.binding, {
    agent: f.agent,
    token: Symbol('native-fixture'),
    signal: new AbortController().signal,
  });
  assert.equal(f.activeTasks.size, 1);
  await f.ports.close();
  assert.equal(f.activeTasks.size, 0);
  f.runtime.close();
});

test('raw-disabled outbound delegates only the bound capability and original bytes to IAM', async () => {
  const f = fixture();
  f.state.raw = false;
  f.state.denyRead = true;
  const task = await openSharedTask({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: {
      agent: f.agent,
      token: Symbol('native-fixture'),
      signal: new AbortController().signal,
    },
    raw: true,
  });
  const call = {
    remoteAddress: '127.0.0.1',
    body: {
      sessionId: f.binding.sessionId,
      taskCapability: task.token,
      url: 'https://app-one.invalid/api',
      method: 'POST',
      body: { query: '你好' },
      headers: { authorization: 'never-forward' },
    },
  };
  try {
    assert.equal(task.env.DSH_AURIGA_ID, '');
    assert.equal(task.env.DSH_AURIGA_TOKEN, '');
    await assert.rejects(f.enterprise.credentials(call), {
      code: 'raw_token_disabled',
    });
    const result = await f.enterprise.outbound(call);
    assert.equal(result.json.ok, true);
    assert.equal(result.json.token, undefined);
    const request = f.state.lastOutbound;
    assert.equal(request.capability.binding.memberId, f.binding.memberId);
    assert.deepEqual(request.capability.scopes, ['outbound']);
    assert.equal(
      Buffer.from(request.bodyBase64, 'base64').toString('utf8'),
      '{"query":"你好"}',
    );
    assert.deepEqual(request.headers, { 'content-type': 'application/json' });
    assert.equal(f.events.includes('readCredentials'), false);
    call.body.method = 'GET';
    await f.enterprise.outbound(call);
    assert.equal(f.state.lastOutbound.bodyBase64, '');
    f.state.outboundHandler = async () => {
      f.state.version++;
      return { statusCode: 200, headers: {}, body: 'late' };
    };
    await assert.rejects(f.enterprise.outbound(call));
  } finally {
    await task.close();
    await f.ports.close();
    f.runtime.close();
  }
});

test('outbound carries actual execution and caller cancellation to the node relay', async () => {
  const f = fixture();
  const execution = new AbortController();
  const task = await openSharedTask({
    ctx: f.ctx,
    runtime: f.runtime,
    ports: f.ports,
    execution: {
      agent: f.agent,
      token: Symbol('native-fixture'),
      signal: execution.signal,
    },
  });
  try {
    for (const useExecution of [false, true]) {
      const caller = new AbortController();
      let started;
      const ready = new Promise((resolve) => {
        started = resolve;
      });
      f.state.outboundHandler = async (_request, signal) =>
        new Promise((_resolve, reject) => {
          signal.addEventListener(
            'abort',
            () => reject(new Error('cancelled')),
            { once: true },
          );
          started();
        });
      const pending = f.enterprise.outbound({
        remoteAddress: '127.0.0.1',
        signal: caller.signal,
        body: {
          sessionId: f.binding.sessionId,
          taskCapability: task.token,
          url: 'https://app-one.invalid/api',
        },
      });
      await ready;
      (useExecution ? execution : caller).abort();
      await assert.rejects(pending, { code: 'outbound_failed' });
    }
    assert.equal(f.events.includes('readCredentials'), false);
  } finally {
    await task.close();
    await f.ports.close();
    f.runtime.close();
  }
});

test('model task is derived from the live request event and is finished at idle', async () => {
  const f = fixture();
  const signal = new AbortController().signal;
  const purpose = {
    audience: 'auriga.shared.model',
    scope: 'model.invoke',
    nativeSignal: signal,
  };
  try {
    await assert.rejects(
      f.ports.downstream.taskForSession(f.binding, signal, purpose),
    );
    await f.listeners.get('agent/request')(
      { agent: f.agent, turn: 1, step: 1, signal },
      async () => ({}),
    );
    const first = await f.ports.downstream.taskForSession(
      f.binding,
      signal,
      purpose,
    );
    const again = await f.ports.downstream.taskForSession(
      f.binding,
      signal,
      purpose,
    );
    assert.equal(first.taskId, again.taskId);
    assert.equal(first.callId, again.callId);
    assert.equal(f.activeTasks.size, 2);
    await first.close();
    await again.close();
    f.listeners.get('agent/status')({ agent: f.agent, status: 'idle' });
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(f.activeTasks.size, 0);
    await assert.rejects(
      f.ports.downstream.taskForSession(f.binding, signal, purpose),
    );
  } finally {
    await f.ports.close();
    f.runtime.close();
  }
});
