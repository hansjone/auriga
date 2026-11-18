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

function fixture() {
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
    live: true,
  };
  const settings = () => ({
    profile: SHARED_PROFILE,
    skillRoots: [],
    model: 'synthetic-model',
    enterprise: { allowRawAgentToken: true, outboundAllowedHosts: [] },
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
    async call(operation, request) {
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
        return { token: token(request) };
      }
      if (operation === 'finishTask') {
        activeTasks.delete(request.taskId);
        return {};
      }
      if (operation === 'authorizeCapability')
        return {
          active: f.live && activeTasks.has(request.capability.taskId),
          credentialVersion: f.version,
          allowRawAgentToken: true,
        };
      if (operation === 'readCredentials') {
        const value = {
          empNo: '00001234',
          token: 'synthetic-material-' + f.version,
          credentialVersion: f.version,
        };
        f.afterRead?.();
        return value;
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
  });
  const runtime = createSharedRuntime({ ...options, ports, now: () => f.now });
  const enterprise = createEnterpriseAdapter({
    runtime,
    ports: ports.enterprise,
    config: ports.config,
  });
  return { ...f, state: f, ctx, ports, runtime, enterprise, listeners };
}

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
        config: { includeDefaultRoots: false, customSkillDirs: ['/fixed'] },
      },
    ],
  });
  assert.equal(assertSharedProfile(f.ctx, SHARED_PROFILE, ['/fixed']), true);
  assert.equal(assertSharedProfile(f.ctx, SHARED_PROFILE, ['/other']), false);
  await f.ports.close();
  f.runtime.close();
});

test('task-local uppercase variables preserve leading zeroes and a running helper renews its own material', async () => {
  const f = fixture();
  const execution = { agent: f.agent, signal: new AbortController().signal };
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
  const execution = { agent: f.agent, signal: new AbortController().signal };
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
  const execution = { agent: f.agent, signal: controller.signal };
  f.state.beforeBegin = async () => controller.abort();
  await assert.rejects(f.ports.issueTask(f.binding, execution, ['outbound']));
  assert.equal(f.activeTasks.size, 0);
  assert.ok(f.events.includes('finishTask'));
  await assert.rejects(
    f.ports.issueTask(f.binding, { agent: { ...f.agent } }, ['outbound']),
  );
  f.state.beforeBegin = undefined;
  await f.ports.issueTask(
    f.binding,
    { agent: f.agent, signal: new AbortController().signal },
    ['outbound'],
  );
  assert.equal(f.activeTasks.size, 1);
  await f.ports.close();
  assert.equal(f.activeTasks.size, 0);
  f.runtime.close();
});

test('model task is derived from the live request event and is finished at idle', async () => {
  const f = fixture();
  const signal = new AbortController().signal;
  const purpose = { audience: 'auriga.shared.model', scope: 'model.invoke' };
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
    assert.deepEqual(first, again);
    assert.equal(f.activeTasks.size, 1);
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
