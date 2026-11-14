import test from 'node:test';
import assert from 'node:assert/strict';
import { nativeContexts } from './host-context-fixture.js';
test('native contexts hand off session then tool without caller owner selection', async (t) => {
  const f = await nativeContexts(t),
    tool = await f.contexts.tool(f.execution);
  const registrations = f.connection.calls
    .filter((m) => m.path === '/host/contexts')
    .map((m) => m.body);
  assert.deepEqual(
    registrations.map((m) => m.context.kind),
    ['session', 'tool'],
  );
  assert.equal(
    registrations[1].context.parentExecutionContextId,
    [...f.connection.contexts.keys()][0],
  );
  assert.equal(tool.hostSessionId, f.session.id);
  assert.equal(registrations[0].context.ownerId, undefined);
  assert.equal(f.contexts.owner(f.session.id), f.connection.identity.memberId);
});
test('forged native execution fails before any IPC effect', async (t) => {
  const f = await nativeContexts(t),
    before = f.connection.calls.length;
  await assert.rejects(
    f.contexts.tool({ ...f.execution, agent: { ...f.agent } }),
    { code: 'common.forbidden' },
  );
  assert.equal(f.connection.calls.length, before);
});
test('archived or detached live agent cannot register a host context', async (t) => {
  const f = await nativeContexts(t);
  f.ctx.workspaceRegistry.archivedSessionIds.push(f.session.id);
  await assert.rejects(f.contexts.tool(f.execution), {
    code: 'common.forbidden',
  });
  f.ctx.workspaceRegistry.archivedSessionIds.length = 0;
  f.agents.clear();
  await assert.rejects(f.contexts.tool(f.execution), {
    code: 'common.forbidden',
  });
});
test('tenant switch with the same member id cannot take over an old native session', async (t) => {
  const f = await nativeContexts(t);
  await f.contexts.tool(f.execution);
  f.switchTenant();
  assert.equal(f.contexts.owner(f.session.id), null);
  await assert.rejects(f.contexts.tool(f.execution), {
    code: 'common.forbidden',
  });
});
test('lost host authority and cancelled execution fail closed', async (t) => {
  const f = await nativeContexts(t);
  f.contexts.setHost(undefined);
  await assert.rejects(f.contexts.tool(f.execution), {
    code: 'common.forbidden',
  });
  f.contexts.setHost(f.ctx);
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(
    f.contexts.tool({ ...f.execution, signal: controller.signal }),
    { code: 'common.forbidden' },
  );
});
test('cancellation after registration revokes the tool and releases its listener', async (t) => {
  const f = await nativeContexts(t),
    controller = new AbortController();
  const tool = await f.contexts.tool({
    ...f.execution,
    signal: controller.signal,
  });
  controller.abort();
  assert.equal(f.connection.contexts.has(tool.executionContextId), false);
});
test('host unload revokes registered sessions and tools without transferring their ownership', async (t) => {
  const f = await nativeContexts(t);
  await f.contexts.tool(f.execution);
  assert.equal(f.connection.contexts.size, 2);
  f.contexts.setHost(undefined);
  assert.equal(f.connection.contexts.size, 0);
  await assert.rejects(f.contexts.tool(f.execution), {
    code: 'common.forbidden',
  });
});
test('tool completion removes its authority while preserving the parent session', async (t) => {
  const f = await nativeContexts(t),
    tool = await f.contexts.tool(f.execution);
  await f.contexts.finish(tool.executionContextId);
  assert.equal(f.connection.contexts.has(tool.executionContextId), false);
  assert.equal(f.connection.contexts.size, 1);
  await f.contexts.finish(tool.executionContextId);
  assert.equal(f.connection.contexts.size, 1);
});
test('replacing a same-id native session revokes its cached context and registers afresh', async (t) => {
  const f = await nativeContexts(t),
    old = await f.contexts.session(f.session.id);
  f.sessions.set(f.session.id, { ...f.session });
  const next = await f.contexts.session(f.session.id);
  assert.notEqual(next.executionContextId, old.executionContextId);
  assert.equal(f.connection.contexts.has(old.executionContextId), false);
});
