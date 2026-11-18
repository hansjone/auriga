import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from 'node:http';
import { setImmediate } from 'node:timers/promises';
import { createConnectRuntime } from '../src/local/runtime.js';
import { brokerFixture } from './broker-fixture.js';

test('native background skill capability reacquires current host context after offline recovery', async (t) => {
  const directory = await mkdtemp(
    join(tmpdir(), 'auriga-enterprise-recovery-'),
  );
  const connection = brokerFixture();
  let runtime, contributor, dispatchTool, jobResult;
  const effects = [];
  const server = createServer(
    (req, res) => void runtime.enterprise.handle(req, res),
  );
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(async () => {
    for (const dispose of effects.reverse()) await dispose?.();
    await runtime?.close();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    await rm(directory, { recursive: true, force: true });
  });
  let session = { id: randomUUID(), header: { id: randomUUID() } };
  session.header.id = session.id;
  let agent = { id: session.id, session };
  const ctx = {
    sessions: { get: (id) => (id === session.id ? session : undefined) },
    agents: { get: (id) => (id === session.id ? agent : undefined) },
    workspaceRegistry: { archivedSessionIds: [] },
    sessionPersistence: {},
    sessionController: { resolveAgent: async () => ({ agent }) },
    webServer: { server, port: server.address().port },
    shellEnv: {
      register(value) {
        contributor = value;
      },
    },
    jobs: {
      list: () => [{ id: 'job', status: 'running' }],
      events: { subscribe: () => () => {} },
    },
    on(name, fn) {
      if (name === 'tools/execute') dispatchTool = fn;
      if (name === 'tools/result') jobResult = fn;
      return () => {};
    },
    effect(fn) {
      effects.push(fn());
    },
    inject(names, fn) {
      if (names.every((name) => ctx[name])) fn(ctx);
    },
  };
  const start = () =>
    createConnectRuntime({
      directory,
      ctx,
      config: { platformOrigin: '' },
      connection,
    });
  runtime = await start();
  const controller = new AbortController();
  const execution = {
    agent,
    callId: randomUUID(),
    rootCallId: randomUUID(),
    token: {},
    signal: controller.signal,
  };
  assert.equal(contributor.resolve(execution).DSH_AURIGA_TOKEN, '');
  const environment = await dispatchTool(execution, () =>
    contributor.resolve(execution),
  );
  assert.equal(environment.DSH_AURIGA_TOKEN, connection.token());
  const raw = () =>
    fetch(
      `http://127.0.0.1:${server.address().port}/dsh-team/agent-credentials`,
      {
        headers: {
          'x-dsh-session-id': session.id,
          'x-dsh-team-task-capability': environment.DSH_TEAM_TASK_CAPABILITY,
        },
      },
    );
  assert.equal((await raw()).status, 200);
  jobResult(execution, { value: { kind: 'background', jobId: 'job' } });
  controller.abort();
  connection.transition('unreachable');
  assert.equal((await raw()).status, 503);
  connection.transition('authenticated');
  await setImmediate();
  await setImmediate();
  connection.rotate();
  const response = await raw();
  assert.equal(response.status, 200);
  assert.equal((await response.json()).token, connection.token());
  const resumed = {
    ...execution,
    callId: randomUUID(),
    token: {},
    signal: new AbortController().signal,
  };
  const updated = await dispatchTool(resumed, () =>
    contributor.resolve(resumed),
  );
  assert.equal(updated.DSH_AURIGA_TOKEN, connection.token());
  assert.notEqual(environment.DSH_AURIGA_TOKEN, updated.DSH_AURIGA_TOKEN);
  jobResult(resumed, {});
  connection.transition('authenticated', {
    uacSessionBinding: {
      ...connection.identity.uacSessionBinding,
      authGeneration: 2,
    },
  });
  assert.equal((await raw()).status, 401);

  const original = {
    memberId: connection.identity.memberId,
    uacSessionBinding: {
      ...connection.identity.uacSessionBinding,
      authGeneration: 1,
    },
  };
  async function restart(memberId) {
    for (const dispose of effects.splice(0).reverse()) await dispose?.();
    await runtime.close();
    connection.transition('authenticated', { ...original, memberId });
    session = { id: session.id, header: { ...session.header } };
    agent = { id: session.id, session };
    runtime = await start();
    const next = {
      agent,
      callId: randomUUID(),
      rootCallId: randomUUID(),
      token: {},
      signal: new AbortController().signal,
    };
    const result = await dispatchTool(next, () => contributor.resolve(next));
    jobResult(next, {});
    return result;
  }
  const wrongOwner = await restart(randomUUID());
  assert.deepEqual(
    [wrongOwner.DSH_AURIGA_ID, wrongOwner.DSH_AURIGA_TOKEN],
    ['', ''],
  );
  connection.rotate();
  const sameOwner = await restart(original.memberId);
  assert.equal(sameOwner.DSH_AURIGA_ID, '00001');
  assert.equal(sameOwner.DSH_AURIGA_TOKEN, connection.token());
});
