import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createNativeHostPort as createInstalledPort,
  createNativeSourceResolver as createInstalledResolver,
  PINNED_HOST,
  verifyRuntimeFingerprint,
} from '../../../../src/network/host/native/index.js';
import { createHostPortOperations as createPort } from '../../../../src/network/host/native/host-port.js';
import { createSourceResolver as createProjection } from '../../../../src/network/host/native/source-context.js';
import { fixture } from './fixtures.js';

const incompatible = { code: 'network.host_incompatible' };

test('runtime fingerprint requires exact version/build/clean state and both source hashes', () => {
  // The dsh-session hash has not been verified: even exact copied metadata fails.
  assert.throws(
    () => verifyRuntimeFingerprint({ ...PINNED_HOST }),
    incompatible,
  );
  for (const key of Object.keys(PINNED_HOST)) {
    assert.throws(
      () =>
        verifyRuntimeFingerprint({
          ...PINNED_HOST,
          [key]: key === 'dirty' ? true : 'other',
        }),
      incompatible,
    );
    const missing = { ...PINNED_HOST };
    delete missing[key];
    assert.throws(() => verifyRuntimeFingerprint(missing), incompatible);
  }
});

test('no security service/config boolean can enable a native port', async () => {
  const f = fixture();
  await assert.rejects(createInstalledPort({ ctx: f.ctx }), incompatible);
  assert.throws(
    () => createInstalledResolver({ ...f, lookupBinding: async () => null }),
    incompatible,
  );
  await assert.rejects(
    createInstalledPort({
      ctx: f.ctx,
      security: { isolated: true, durable: true },
    }),
    incompatible,
  );
  f.state.isolated = false;
  await assert.rejects(createInstalledPort(f), incompatible);
});

test('boolean isolation/provider attestations are rejected rather than treated as proof', async () => {
  const f = fixture();
  f.security.assertIsolated = async () => true;
  await assert.rejects(createPort(f), incompatible);
  f.security.assertIsolated = async () => {};
  const host = await createPort(f);
  f.security.assertDurableProvider = async () => false;
  assert.equal(await host.hasDurableProvider('session-a'), false);
});

test('runtime and isolation are rechecked before operations and after async native calls', async () => {
  const f = fixture();
  const host = await createPort(f);
  f.security.inspectRuntime = async () => ({ ...PINNED_HOST, build: 'other' });
  await assert.rejects(host.getSession('session-a'), incompatible);
  f.security.inspectRuntime = async () => ({ ...PINNED_HOST });
  const prompt = f.ctx.sessionController.prompt;
  f.ctx.sessionController.prompt = async (...args) => {
    const out = await prompt(...args);
    f.state.isolated = false;
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
