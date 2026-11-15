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
  let runtime, contributor, jobResult;
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
  const session = { id: randomUUID(), header: { id: randomUUID() } };
  session.header.id = session.id;
  const agent = { id: session.id, session };
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
  runtime = await createConnectRuntime({
    directory,
    ctx,
    config: { platformOrigin: '' },
    connection,
  });
  const controller = new AbortController();
  const execution = {
    agent,
    callId: randomUUID(),
    rootCallId: randomUUID(),
    token: {},
    signal: controller.signal,
  };
  const environment = await contributor.resolve(execution);
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
  connection.transition('authenticated', {
    uacSessionBinding: {
      ...connection.identity.uacSessionBinding,
      authGeneration: 2,
    },
  });
  assert.equal((await raw()).status, 401);
});
