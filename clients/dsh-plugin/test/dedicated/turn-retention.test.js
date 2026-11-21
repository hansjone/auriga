import nodeTest from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate, setTimeout as delay } from 'node:timers/promises';
import { publicTurn } from '../shared/turn-public-fixture.mjs';
import { openSharedExecution } from '../../src/shared/task-context.js';
import { installDedicatedEnvironment } from '../../src/dedicated/task-environment.js';
import { createDedicatedEnterprisePorts } from '../../src/dedicated/enterprise-ports.js';
import { createEnterpriseAdapter } from '../../src/shared/enterprise.js';
import { root, load } from './public.js';

const test = (name, body) =>
  nodeTest(
    name,
    { skip: !root && 'requires the locked rc.3 public SDK', timeout: 10000 },
    body,
  );
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const begins = (f) =>
  f.state.events
    .filter((event) => event.operation === 'beginTask')
    .map((event) => event.body);
const ordinary = (f) =>
  begins(f).filter((body) => body.scopes.includes('task.execute'));
async function idle(f) {
  for (
    let i = 0;
    i < 40 && (f.state.active.size || f.environment.activeExecutions.size);
    i++
  )
    await delay(5);
}

async function setup(t) {
  const f = await publicTurn(t, { toolEnvironment: false, mode: 'dedicated' });
  assert.equal(f.binding.mode, 'dedicated');
  const { LocalJobRegistry } = await load('dsh-jobs-local');
  await f.ctx.plugin(LocalJobRegistry, {
    maxConcurrentJobsPerOwner: 4,
    liveRetainBytes: 4096,
    settledRetainBytes: 4096,
  });
  const detachController = f.ctx.jobs.attachController();
  const pending = new Set(),
    tasks = [],
    sessionEvents = [],
    results = [];
  f.ctx.on('session/event', (session, event) => {
    if (session === f.agent.session) sessionEvents.push(event);
  });
  f.ctx.on('tools/result', (execution, result) => {
    results.push({ execution, result });
  });
  let valid = true;
  const environment = installDedicatedEnvironment(f.ctx, {
    runtime: f.runtime,
    resolveTaskId: (token) => f.ports.enterprise.taskId(token),
    resources: {
      isCurrent: () => valid,
      verify: async () => {
        if (!valid) throw new Error('synthetic revoked selection');
      },
    },
    openTask: async (execution) => {
      const task = await openSharedExecution({
        ctx: f.ctx,
        runtime: f.runtime,
        ports: f.ports,
        execution,
        raw: true,
      });
      tasks.push({ execution, task });
      return task;
    },
  });
  f.state.company = true;
  // Only the synthetic Node business-material bytes vary. The original
  // production credential relay/authorization/version checks still execute.
  const credentials = f.ports.enterprise.credentials;
  f.ports.enterprise.credentials = async (...args) => ({
    ...(await credentials(...args)),
    token: 'synthetic-company-v' + f.state.version,
  });
  const enterprise = createEnterpriseAdapter({
    runtime: f.runtime,
    ports: createDedicatedEnterprisePorts(f.ports.enterprise, environment),
    config: f.ports.config,
  });
  let execute;
  f.ctx.tools.register({
    name: 'auriga_dedicated_job_fixture',
    description: 'native registry process fixture',
    parameters: { type: 'object', properties: {}, additionalProperties: false },
    output: {
      schema: { type: 'object', additionalProperties: true },
      render: (_args, value) => [{ type: 'text', text: JSON.stringify(value) }],
    },
    execute: (...args) => execute(...args),
  });
  f.state.model = async function* () {
    if (f.state.models === 1) {
      yield {
        type: 'block-end',
        index: 0,
        block: {
          type: 'tool-call',
          id: 'native-dedicated-job-call',
          name: 'auriga_dedicated_job_fixture',
          arguments: '{}',
        },
      };
      yield { type: 'finish', reason: { kind: 'tool-calls' } };
    } else {
      yield { type: 'text-delta', index: 0, text: 'fixture done' };
      yield { type: 'finish', reason: { kind: 'stop' } };
    }
  };
  const start = (
    execution,
    { finishOnCancel = true, wrongCall = false } = {},
  ) => {
    const done = deferred();
    let cancelled = 0;
    pending.add(done);
    const id = f.ctx.jobs.start({
      kind: 'fixture',
      label: 'synthetic process',
      owner: f.agent.id,
      ...(execution
        ? {
            toolCall: {
              callId: execution.callId,
              rootCallId: wrongCall
                ? 'foreign-root-call'
                : execution.rootCallId,
            },
          }
        : {}),
      run: () => ({
        done: done.promise,
        cancel: () => {
          cancelled++;
          if (finishOnCancel) done.resolve({ status: 'killed' });
        },
      }),
    });
    return {
      id,
      done: (status = 'completed') => {
        pending.delete(done);
        done.resolve({ status });
      },
      get cancelled() {
        return cancelled;
      },
    };
  };
  t.after(async () => {
    for (const done of pending) done.resolve({ status: 'killed' });
    await environment.close();
    detachController();
  });
  return {
    ...f,
    environment,
    enterprise,
    tasks,
    sessionEvents,
    results,
    start,
    tool: (body) => {
      execute = body;
    },
    revokeResource: () => {
      valid = false;
    },
  };
}

test('real dedicated JobRegistry registration retains the native turn, refreshes rotated material, and blocks replacement until settle', async (t) => {
  const f = await setup(t);
  let job;
  f.tool(async (_args, execution) => {
    job = f.start(execution);
    return { kind: 'background', jobId: job.id };
  });
  await f.prompt();
  assert.equal(ordinary(f).length, 1);
  assert.equal(f.environment.activeExecutions.size, 1);
  assert.equal(
    f.sessionEvents.filter((event) => event.type === 'turn/end').length,
    1,
  );
  const { task, execution } = f.tasks[0];
  assert.equal(Object.isFrozen(execution), true);
  assert.equal(
    f.ctx.jobs.get(job.id, f.agent.id).toolCall,
    undefined,
    'public job view has no invented provenance fields',
  );
  f.state.version++;
  assert.equal((await task.refresh()).DSH_AURIGA_TOKEN, 'synthetic-company-v8');
  await delay(1050);
  assert.equal(
    f.ctx.shellEnv.collect(execution).DSH_AURIGA_TOKEN,
    'synthetic-company-v8',
  );
  assert.equal(
    (
      await f.enterprise.credentials({
        remoteAddress: '127.0.0.1',
        body: { sessionId: f.binding.sessionId, taskCapability: task.token },
      })
    ).token,
    'synthetic-company-v8',
  );
  const next = f.prompt('next');
  await setImmediate();
  assert.equal(ordinary(f).length, 1);
  job.done();
  await next;
  await idle(f);
  assert.equal(ordinary(f).length, 2);
  assert.equal(f.state.active.size, 0);
  assert.equal(f.environment.activeExecutions.size, 0);
  const finished = f.state.events
      .filter((event) => event.operation === 'finishTask')
      .map((event) => event.body.taskId),
    parent = ordinary(f)[0].taskId;
  for (const child of begins(f).filter(
    (body) =>
      body.parentCapability &&
      f.state.tokens.get(body.parentCapability).taskId === parent,
  ))
    assert.ok(finished.indexOf(child.taskId) < finished.indexOf(parent));
});

for (const revoke of ['placement', 'resource'])
  test(
    'native ' +
      revoke +
      ' revocation fences helpers immediately and keeps budget through stopping until real settlement',
    async (t) => {
      const f = await setup(t);
      let job;
      f.tool(async (_args, execution) => {
        job = f.start(execution, { finishOnCancel: false });
        return { kind: 'promoted', jobId: job.id };
      });
      await f.prompt();
      const { task, execution } = f.tasks[0];
      if (revoke === 'placement') f.ports.disconnect(f.binding);
      else f.revokeResource();
      if (revoke === 'resource') await delay(1100);
      else await setImmediate();
      assert.throws(() => f.ctx.shellEnv.collect(execution));
      await assert.rejects(task.refresh());
      await assert.rejects(
        f.enterprise.credentials({
          remoteAddress: '127.0.0.1',
          body: { sessionId: f.binding.sessionId, taskCapability: task.token },
        }),
      );
      assert.equal(job.cancelled, 1);
      assert.equal(f.ctx.jobs.get(job.id, f.agent.id).status, 'stopping');
      assert.ok(f.state.active.has(ordinary(f)[0].taskId));
      assert.equal(f.environment.activeExecutions.size, 1);
      job.done('killed');
      await idle(f);
      assert.equal(f.state.active.size, 0);
      assert.equal(f.environment.activeExecutions.size, 0);
    },
  );

test('a result JSON naming an older same-member job cannot manufacture native execution retention', async (t) => {
  const f = await setup(t),
    older = f.start(undefined);
  f.tool(async () => ({ kind: 'background', jobId: older.id }));
  await f.prompt();
  await idle(f);
  assert.equal(f.ctx.jobs.get(older.id, f.agent.id).status, 'running');
  assert.equal(older.cancelled, 0);
  assert.equal(f.state.active.size, 0);
  assert.equal(f.environment.activeExecutions.size, 0);
  assert.equal(f.results[0].result.isError, true);
  older.done();
});

test('actual registered job with foreign native rootCallId is killed without receiving a trusted background lease', async (t) => {
  const f = await setup(t);
  let job;
  f.tool(async (_args, execution) => {
    job = f.start(execution, { wrongCall: true });
    return { kind: 'background', jobId: job.id };
  });
  await f.prompt();
  await idle(f);
  assert.equal(job.cancelled, 1);
  assert.equal(f.state.active.size, 0);
  assert.equal(f.environment.activeExecutions.size, 0);
});

test('terminal native job before tool return settles its private retain without inventing a running background task', async (t) => {
  const f = await setup(t);
  let job;
  f.tool(async (_args, execution) => {
    job = f.start(execution);
    job.done();
    await setImmediate();
    return { kind: 'background', jobId: job.id };
  });
  await f.prompt();
  await idle(f);
  assert.equal(job.cancelled, 0);
  assert.equal(f.state.active.size, 0);
  assert.equal(f.environment.activeExecutions.size, 0);
});

test('native job settlement does not clear the dedicated resource barrier before child Finish acknowledgement', async (t) => {
  const f = await setup(t),
    entered = deferred(),
    ack = deferred();
  let job;
  f.tool(async (_args, execution) => {
    job = f.start(execution);
    return { kind: 'background', jobId: job.id };
  });
  await f.prompt();
  f.state.beforeFinish = async (body) => {
    if (f.state.active.get(body.taskId)?.scopes.includes('credentials')) {
      entered.resolve();
      await ack.promise;
    }
  };
  try {
    job.done();
    await entered.promise;
    assert.equal(f.environment.activeExecutions.size, 1);
    assert.ok(f.state.active.has(ordinary(f)[0].taskId));
    const next = f.prompt('waiting on Finish');
    await setImmediate();
    assert.equal(ordinary(f).length, 1);
    ack.resolve();
    await next;
    await idle(f);
    assert.equal(f.environment.activeExecutions.size, 0);
    assert.equal(f.state.active.size, 0);
  } finally {
    ack.resolve();
  }
});

test('one native execution keeps its private lease until every witnessed native job settles', async (t) => {
  const f = await setup(t);
  let first, second;
  f.tool(async (_args, execution) => {
    first = f.start(execution);
    second = f.start(execution);
    return { kind: 'background', jobId: first.id };
  });
  await f.prompt();
  first.done();
  await delay(15);
  assert.equal(f.environment.activeExecutions.size, 1);
  assert.ok(f.state.active.has(ordinary(f)[0].taskId));
  assert.equal((await f.tasks[0].task.refresh()).DSH_AURIGA_ID, '00001234');
  second.done();
  await idle(f);
  assert.equal(f.environment.activeExecutions.size, 0);
  assert.equal(f.state.active.size, 0);
});

test('cancelled native body cannot create a late authorized job or release its budget before it drains', async (t) => {
  const f = await setup(t),
    entered = deferred(),
    spawn = deferred();
  let job;
  f.tool(async (_args, execution) => {
    entered.resolve();
    await spawn.promise;
    job = f.start(execution);
    return { kind: 'background', jobId: job.id };
  });
  const running = f.prompt();
  try {
    await entered.promise;
    f.agent.cancel({ kind: 'user' });
    await setImmediate();
    assert.ok(f.state.active.has(ordinary(f)[0].taskId));
    assert.equal(f.environment.activeExecutions.size, 1);
    assert.equal(
      f.state.events.filter((event) => event.operation === 'finishTask').length,
      0,
    );
  } finally {
    spawn.resolve();
  }
  await running;
  await idle(f);
  assert.equal(job.cancelled, 1);
  assert.equal(f.environment.activeExecutions.size, 0);
  assert.equal(f.state.active.size, 0);
});

test('native job stopping aborts an in-flight helper business call and rejects its late result without releasing live budget', async (t) => {
  const f = await setup(t),
    entered = deferred(),
    business = deferred();
  let job, signal;
  f.tool(async (_args, execution) => {
    job = f.start(execution, { finishOnCancel: false });
    return { kind: 'background', jobId: job.id };
  });
  await f.prompt();
  const task = f.tasks[0].task,
    token = task.token,
    claims = f.state.tokens.get(token);
  const guarded = createDedicatedEnterprisePorts(
    {
      ...f.ports.enterprise,
      outbound: async (request) => {
        signal = request.signal;
        entered.resolve();
        await business.promise;
        return { statusCode: 200, json: { synthetic: true } };
      },
    },
    f.environment,
  );
  const pending = guarded.outbound({}, claims, f.binding, token);
  void pending.catch(() => {});
  try {
    await entered.promise;
    assert.equal(signal.aborted, false);
    f.ctx.jobs.kill(job.id, f.agent.id, 'synthetic user job stop');
    assert.equal(signal.aborted, true);
    assert.equal(f.environment.activeExecutions.size, 1);
    assert.ok(f.state.active.has(ordinary(f)[0].taskId));
    business.resolve();
    await assert.rejects(pending);
    job.done('killed');
    await idle(f);
    assert.equal(f.environment.activeExecutions.size, 0);
    assert.equal(f.state.active.size, 0);
  } finally {
    business.resolve();
    job.done('killed');
  }
});

test('team-only dedicated background work retains its ordinary parent without an enterprise task and keeps company variables empty', async (t) => {
  const f = await setup(t);
  f.state.company = false;
  let job;
  f.tool(async (_args, execution) => {
    job = f.start(execution);
    return { kind: 'background', jobId: job.id };
  });
  await f.prompt();
  const { task, execution } = f.tasks[0];
  assert.equal(task.company, false);
  assert.equal(ordinary(f).length, 1);
  assert.ok(f.state.active.has(ordinary(f)[0].taskId));
  assert.equal(
    begins(f).some(
      (body) =>
        body.scopes.includes('credentials') || body.scopes.includes('outbound'),
    ),
    false,
  );
  const env = f.ctx.shellEnv.collect(execution);
  assert.equal(env.DSH_AURIGA_ID, '');
  assert.equal(env.DSH_AURIGA_TOKEN, '');
  assert.equal((await task.refresh()).DSH_AURIGA_TOKEN, '');
  await assert.rejects(
    f.environment.helperContext(f.binding.sessionId, { token: task.token }),
  );
  job.done();
  await idle(f);
  assert.equal(f.state.active.size, 0);
  assert.equal(f.environment.activeExecutions.size, 0);
});
