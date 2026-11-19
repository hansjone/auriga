import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fixture } from '../network/host/native/fixtures.js';
import { createSharedMessageNative } from '../../src/shared/message-native.js';

function setup() {
  const f = fixture();
  const sessionId = randomUUID();
  f.seed(sessionId);
  const agent = f.attach(sessionId);
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId: randomUUID(),
    mode: 'shared',
    epoch: 1,
    revision: 1,
    sessionId,
    platformSessionId: randomUUID(),
    authGeneration: 1,
  };
  const state = { now: 1_800_000_000_000 };
  const native = createSharedMessageNative(f.ctx, { now: () => state.now });
  const abort = new AbortController();
  const execution = { agent, signal: abort.signal };
  return { ...f, agent, binding, state, native, abort, execution };
}

test('message native witness binds a real session and retains its independent identity', async () => {
  const f = setup();
  const captured = await f.native.capture(f.execution, f.binding);
  const next = await f.native.capture(f.execution, f.binding);
  assert.equal(captured.bootId, next.bootId);
  assert.equal(captured.operationId, next.operationId);
  assert.equal(captured.witness.binding_id, next.witness.binding_id);
  assert.notEqual(captured.witness.binding_id, f.binding.sessionId);
  assert.notEqual(captured.witness.witness_id, next.witness.witness_id);
  assert.equal(captured.witness.binding_revision, '1');
  assert.equal(captured.witness.observed_at, '2027-01-15T08:00:00Z');
  assert.equal(captured.witness.valid_until, '2027-01-15T08:00:30Z');
  const resolved = await captured.bindLocal('42');
  assert.deepEqual(resolved, {
    local: '42',
    bindingId: captured.witness.binding_id,
    hostSessionId: f.agent.id,
    cwd: f.agent.session.header.cwd,
  });
  assert.equal(Object.isFrozen(resolved), true);
  assert.equal(captured.bootstrap, true);
  const business = await captured.refresh();
  assert.equal(business.bootstrap, false);
  assert.notEqual(business.witness.witness_id, captured.witness.witness_id);
  captured.check();
  await assert.rejects(captured.bindLocal('43'));
  f.native.close();
});

test('concurrent members never share native binding ids or selected typed locals', async () => {
  const f = setup();
  const other = randomUUID();
  f.seed(other);
  const agent = f.attach(other);
  const b = { ...f.binding, memberId: randomUUID(), sessionId: other };
  const [a, c] = await Promise.all([
    f.native.capture(f.execution, f.binding),
    f.native.capture({ agent }, b),
  ]);
  assert.notEqual(a.witness.binding_id, c.witness.binding_id);
  assert.equal(a.bootId, c.bootId);
  assert.equal((await a.bindLocal('7')).hostSessionId, f.agent.id);
  assert.equal((await c.bindLocal('8')).hostSessionId, agent.id);
  await assert.rejects(f.native.capture(f.execution, b));
  f.native.close();
});

test('native witness rejects JSON agents, cold history and inactive tasks before any registration', async (t) => {
  for (const kind of ['json', 'cold', 'archived', 'subagent', 'aborted']) {
    await t.test(kind, async () => {
      const f = setup();
      if (kind === 'json')
        f.execution.agent = JSON.parse(JSON.stringify(f.agent));
      if (kind === 'cold') f.agents.delete(f.agent.id);
      if (kind === 'archived') f.archivedSessionIds.push(f.agent.id);
      if (kind === 'subagent') f.agent.session.header.origin = 'subagent';
      if (kind === 'aborted') f.abort.abort();
      await assert.rejects(f.native.capture(f.execution, f.binding));
      f.native.close();
    });
  }
});

test('all late native changes invalidate captured proofs and do not silently rebind', async (t) => {
  for (const kind of [
    'session',
    'agent',
    'cwd',
    'provider',
    'archived',
    'abort',
    'closed',
  ]) {
    await t.test(kind, async () => {
      const f = setup();
      const captured = await f.native.capture(f.execution, f.binding);
      if (kind === 'session')
        f.sessions.set(f.agent.id, { ...f.agent.session });
      if (kind === 'agent') f.agents.set(f.agent.id, { ...f.agent });
      if (kind === 'cwd') f.agent.session.header.cwd = '/other';
      if (kind === 'provider')
        f.ctx.sessionPersistence.identity = Symbol('new');
      if (kind === 'archived') f.archivedSessionIds.push(f.agent.id);
      if (kind === 'abort') f.abort.abort();
      if (kind === 'closed') f.native.close();
      assert.throws(() => captured.check());
      await assert.rejects(captured.bindLocal('1'));
      await assert.rejects(f.native.capture(f.execution, f.binding));
      f.native.close();
    });
  }
});

test('witness expires on its original local clock and rejects rollback', async (t) => {
  for (const change of [-1, 30_000]) {
    await t.test(String(change), async () => {
      const f = setup();
      const captured = await f.native.capture(f.execution, f.binding);
      f.state.now += change;
      assert.throws(() => captured.check());
      await assert.rejects(captured.bindLocal('1'));
      f.native.close();
    });
  }
});

test('placement and auth-generation changes cannot reuse an established native source', async (t) => {
  for (const key of [
    'memberId',
    'platformSessionId',
    'authGeneration',
    'epoch',
    'revision',
  ]) {
    await t.test(key, async () => {
      const f = setup();
      await f.native.capture(f.execution, f.binding);
      const changed = {
        ...f.binding,
        [key]:
          typeof f.binding[key] === 'number'
            ? f.binding[key] + 1
            : randomUUID(),
      };
      await assert.rejects(f.native.capture(f.execution, changed));
      f.native.close();
    });
  }
});

test('a platform local is never supplied by a model-shaped execution or an invalid address', async (t) => {
  for (const local of ['0', '-1', '01', '18446744073709551616', 1, null]) {
    await t.test(String(local), async () => {
      const f = setup();
      const captured = await f.native.capture(f.execution, f.binding);
      await assert.rejects(captured.bindLocal(local));
      f.native.close();
    });
  }
});

test('native liveness is checked again after an asynchronous public-host lookup', async () => {
  const f = setup();
  const captured = await f.native.capture(f.execution, f.binding);
  f.ctx.sessionController.resolveAgent = async () => {
    f.sessions.delete(f.agent.id);
    return { agent: f.agent };
  };
  await assert.rejects(captured.bindLocal('1'));
  f.native.close();
});

test('concurrent platform responses cannot bind one native source to two locals', async () => {
  const f = setup();
  const a = await f.native.capture(f.execution, f.binding);
  const b = await f.native.capture(f.execution, f.binding);
  const first = a.bindLocal('1');
  await assert.rejects(b.bindLocal('2'));
  assert.equal((await first).local, '1');
  f.native.close();
});
