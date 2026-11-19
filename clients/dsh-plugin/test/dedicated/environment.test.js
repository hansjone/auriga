import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { fixture } from '../shared/normal-task-fixture.mjs';
import { openSharedExecution } from '../../src/shared/task-context.js';
import { installDedicatedEnvironment } from '../../src/dedicated/task-environment.js';
import { test, load } from './public.js';

// The common task issuer/signature fixture is shared; dedicated mode admission
// itself is exercised with the actual runtime and Peer in host.test.js.
async function setup(t, company = true) {
  const f = fixture();
  f.state.company = company;
  const [
    { Context },
    { ToolRuntime },
    { default: SystemPrompt },
    { ShellEnvRegistry },
    { LocalJobRegistry },
  ] = await Promise.all([
    load('cordis'),
    load('dsh-tools'),
    load('dsh-system-prompt'),
    load('dsh-shell-env'),
    load('dsh-jobs-local'),
  ]);
  const ctx = new Context();
  await ctx.plugin(SystemPrompt, {});
  await ctx.plugin(ToolRuntime, { mode: 'native' });
  await ctx.plugin(ShellEnvRegistry, {});
  ctx.provide('agents', f.ctx.agents);
  ctx.provide('sessions', f.ctx.sessions);
  f.agent.ctx = ctx;
  await ctx.plugin(LocalJobRegistry, {
    maxConcurrentJobsPerOwner: 4,
    liveRetainBytes: 4096,
    settledRetainBytes: 4096,
  });
  const detachController = ctx.jobs.attachController();
  let valid = true;
  const resources = {
    isCurrent: () => valid,
    verify: async () => {
      if (!valid) throw new Error('resource revoked');
    },
  };
  const environment = installDedicatedEnvironment(ctx, {
    runtime: f.runtime,
    resources,
    openTask: (execution) =>
      openSharedExecution({
        ctx,
        runtime: f.runtime,
        ports: f.ports,
        execution,
        raw: true,
      }),
  });
  const controller = new AbortController();
  const run = (name = 'fixture') =>
    ctx.tools.execute({
      name,
      callId: randomUUID(),
      arguments: {},
      agent: f.agent,
      signal: controller.signal,
    });
  const tool = (execute) =>
    ctx.tools.register({
      name: 'fixture',
      description: 'Fixture',
      parameters: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      execute,
      output: {
        schema: { type: 'object', additionalProperties: true },
        render: (_a, value) => [{ type: 'text', text: JSON.stringify(value) }],
      },
    });
  t.after(async () => {
    controller.abort();
    await environment.close();
    detachController();
    await ctx.fiber.dispose();
    await f.contexts.close();
    f.runtime.close();
  });
  return {
    ...f,
    ctx,
    environment,
    run,
    tool,
    controller,
    revokeResource: () => {
      valid = false;
    },
  };
}

test('actual ToolRuntime/ShellEnvRegistry consume refreshed task getters without touching parent environment', async (t) => {
  // This isolated test process uses only these synthetic values. No host
  // environment snapshot, credential read or full-environment diagnostic.
  process.env.DSH_AURIGA_ID = 'synthetic-parent-id';
  process.env.DSH_AURIGA_TOKEN = 'synthetic-parent-token';
  t.after(() => {
    delete process.env.DSH_AURIGA_ID;
    delete process.env.DSH_AURIGA_TOKEN;
  });
  const f = await setup(t);
  let execution;
  f.tool(async (_args, exec) => {
    execution = exec;
    assert.equal(typeof exec.token, 'symbol');
    const first = f.ctx.shellEnv.collect(exec);
    assert.equal(first.DSH_AURIGA_ID, '00001234');
    assert.equal(first.DSH_AURIGA_TOKEN, 'synthetic-company-material');
    const task = f.environment.companyTask(exec);
    f.state.version++;
    f.state.material = 'rotated-synthetic';
    await task.refresh();
    // collect is synchronous: the wrapper publishes refreshed snapshots at
    // lifecycle boundaries/timer; the live helper handle updates immediately.
    assert.equal(task.env.DSH_AURIGA_TOKEN, 'rotated-synthetic');
    await assert.rejects(async () => f.ctx.shellEnv.collect({ ...exec }));
    return { complete: true };
  });
  const result = await f.run();
  assert.equal(result.isError, false);
  assert.equal(f.active.size, 0);
  assert.equal(f.issued.length, 2);
  assert.equal(f.ctx.shellEnv.collect(execution).DSH_AURIGA_TOKEN, '');
  assert.equal(process.env.DSH_AURIGA_ID, 'synthetic-parent-id');
  assert.equal(process.env.DSH_AURIGA_TOKEN, 'synthetic-parent-token');
});

test('ordinary ABSENT task injects empty company pair, typed state changes and resource revocation reject publication', async (t) => {
  const f = await setup(t, false);
  f.tool(async (_args, exec) => {
    const env = f.ctx.shellEnv.collect(exec);
    assert.equal(env.DSH_AURIGA_ID, '');
    assert.equal(env.DSH_AURIGA_TOKEN, '');
    assert.ok(env.DSH_TEAM_TASK_CAPABILITY);
    assert.throws(() => f.environment.companyTask(exec));
    f.state.company = true;
    return { complete: true };
  });
  assert.equal((await f.run()).isError, true);
  assert.equal(f.active.size, 0);
  assert.equal(f.issued.length, 1);
});
