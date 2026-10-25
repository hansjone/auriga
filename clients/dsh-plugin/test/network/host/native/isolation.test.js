import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createNativeHostPort as createInstalledPort,
  createNativeSourceResolver as createInstalledResolver,
} from '../../../../src/network/host/native/index.js';
import { createHostPortOperations as createPort } from '../../../../src/network/host/native/host-port.js';
import { createSourceResolver as createProjection } from '../../../../src/network/host/native/source-context.js';
import { fixture } from './fixtures.js';

const incompatible = { code: 'network.host_incompatible' };

test('public probe reports API and non-function failures without package identities', async () => {
  for (const value of [undefined, true, {}, 'function']) {
    const f = fixture();
    f.ctx.sessionController.inspect = value;
    const host = await createInstalledPort({ ctx: f.ctx });
    assert.deepEqual(host.getDiagnostics().capabilities.read.missing, [
      'sessionController.inspect',
    ]);
    await assert.rejects(host.getSession('session-a'), incompatible);
  }
});

test('security/config booleans cannot replace missing public capabilities', async () => {
  const f = fixture();
  delete f.ctx.sessions.flush;
  const host = await createInstalledPort({
    ctx: f.ctx,
    security: { durable: true },
  });
  assert.equal(await host.flush('session-a'), false);
  const resolve = await createInstalledResolver({
    ctx: f.ctx,
    lookupBinding: async () => null,
  });
  await assert.rejects(resolve({}), incompatible);
});

test('durability boolean attestations are rejected rather than treated as provider evidence', async () => {
  const f = fixture();
  const host = await createPort(f);
  f.security.assertDurableProvider = async () => true;
  assert.equal(await host.hasDurableProvider('session-a'), false);
  f.security.assertDurableProvider = async () => false;
  assert.equal(await host.hasDurableProvider('session-a'), false);
});

test('public capabilities are rechecked before operations and after async calls', async () => {
  const f = fixture();
  const host = await createInstalledPort({ ctx: f.ctx });
  const inspect = f.ctx.sessionController.inspect;
  delete f.ctx.sessionController.inspect;
  await assert.rejects(host.getSession('session-a'), incompatible);
  f.ctx.sessionController.inspect = inspect;
  const prompt = f.ctx.sessionController.prompt;
  f.ctx.sessionController.prompt = async (...args) => {
    const out = await prompt(...args);
    delete f.ctx.sessionPersistence.stat;
    return out;
  };
  await assert.rejects(
    host.prompt('session-a', { requestId: 'r', text: 'body', mode: 'queue' }),
    incompatible,
  );
  assert.equal(await host.flush('session-a'), false);
});

test('tool source comes from live host object identity and trusted local binding', async () => {
  const f = fixture();
  const agent = f.attach('session-a');
  const binding = {
    local: '1',
    bindingId: 'bind-a',
    hostSessionId: 'session-a',
    cwd: '/work/project',
    state: 'bound',
    lifecycle: 'open',
    kind: 'session',
    sourceBinding: 'must-not-leak',
  };
  const resolve = createProjection({
    ...f,
    lookupBinding: async () => binding,
  });
  assert.deepEqual(await resolve({ agent }), {
    local: '1',
    bindingId: 'bind-a',
    hostSessionId: 'session-a',
    cwd: '/work/project',
  });
  for (const exec of [
    { sessionId: 'session-a' },
    { agent: { session: { id: 'session-a' } } },
    { agent: { ...agent } },
  ])
    await assert.rejects(resolve(exec), incompatible);
  binding.hostSessionId = 'other';
  await assert.rejects(resolve({ agent }), incompatible);
  binding.hostSessionId = 'session-a';
  binding.lifecycle = 'closed';
  await assert.rejects(resolve({ agent }), incompatible);
});

test('source resolver revalidates live identity after awaits, rejects subagent/ambiguous local', async () => {
  const f = fixture();
  const agent = f.attach('session-a');
  const binding = {
    local: '01',
    bindingId: 'b',
    hostSessionId: 'session-a',
    cwd: '/work/project',
    state: 'bound',
    lifecycle: 'open',
    kind: 'session',
  };
  let resolve = createProjection({
    ...f,
    lookupBinding: async () => binding,
  });
  await assert.rejects(resolve({ agent }), incompatible);
  binding.local = '1';
  resolve = createProjection({
    ...f,
    lookupBinding: async () => {
      f.agents.delete('session-a');
      return binding;
    },
  });
  await assert.rejects(resolve({ agent }), incompatible);
  f.agents.set('session-a', agent);
  agent.session.header.origin = 'subagent';
  await assert.rejects(resolve({ agent }), incompatible);
});

test('native parent ownership vetoes an otherwise ordinary live tool source', async () => {
  const f = fixture();
  const agent = f.attach('session-a');
  let lookedUp = false;
  const resolve = createProjection({
    ...f,
    lookupBinding: async () => {
      lookedUp = true;
      return null;
    },
  });
  f.ctx.sessionController.resolveAgent = async () => ({
    error: { code: 'session/subagent-owned' },
  });
  await assert.rejects(resolve({ agent }), incompatible);
  assert.equal(lookedUp, false);
});
