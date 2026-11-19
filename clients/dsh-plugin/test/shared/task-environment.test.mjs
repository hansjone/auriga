import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { randomUUID } from 'node:crypto';
import { fixture } from './normal-task-fixture.mjs';
import { installSharedExecutionEnvironment } from '../../src/shared/task-environment.js';
import { installSharedHost } from '../../src/shared/host.js';
import { createEnterpriseAdapter } from '../../src/shared/enterprise.js';

const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
if (process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1' && !root)
  throw new Error('Pinned public-host root is required');
const skip =
  !root &&
  'Plugin workflow runs the pinned rc.3 execution/environment registry';

async function setup(t, company = false) {
  const f = fixture();
  f.state.company = company;
  const require = createRequire(join(root, 'package.json'));
  const load = async (name) =>
    import(pathToFileURL(require.resolve('@deepseek-ai/' + name)).href);
  const [
    { Context },
    { ToolRuntime },
    { default: SystemPrompt },
    { ShellEnvRegistry },
  ] = await Promise.all([
    load('cordis'),
    load('dsh-tools'),
    load('dsh-system-prompt'),
    load('dsh-shell-env'),
  ]);
  const ctx = new Context();
  await ctx.plugin(SystemPrompt, {});
  await ctx.plugin(ToolRuntime, { mode: 'native' });
  await ctx.plugin(ShellEnvRegistry, {});
  f.agent.ctx = ctx;
  ctx.provide('agents', f.ctx.agents);
  ctx.provide('sessions', f.ctx.sessions);
  ctx.provide('remoteAccess', {
    registerPolicy: () => () => {},
    invalidate() {},
  });
  ctx.provide('webServer', { register: () => () => {} });
  const environment = installSharedExecutionEnvironment(ctx, {
    runtime: f.runtime,
    ports: f.ports,
  });
  const enterprise = createEnterpriseAdapter({
    runtime: f.runtime,
    ports: f.ports.enterprise,
    config: f.ports.config,
  });
  const host = installSharedHost(ctx, {
    runtime: f.runtime,
    enterprise,
    ports: {
      ...f.ports,
      assertProfile: () => true,
      createPeer() {},
      disconnect() {},
    },
    taskEnvironment: environment,
  });
  t.after(async () => {
    await environment.close();
    host.close();
    await ctx.fiber.dispose();
    await f.contexts.close();
  });
  let observe;
  ctx.on('tools/execute', async (exec, next) => {
    await observe?.(exec);
    return next();
  });
  const controller = new AbortController();
  const run = (name = 'auriga_shared_resources', args = {}) =>
    ctx.tools.execute({
      name,
      callId: randomUUID(),
      arguments: args,
      agent: f.agent,
      signal: controller.signal,
    });
  return {
    ...f,
    ctx,
    environment,
    enterprise,
    host,
    controller,
    run,
    observe: (callback) => {
      observe = callback;
    },
  };
}

test(
  'actual shared ToolRuntime hook prepares public registry env for team task, never opens enterprise or enables Shell',
  { skip },
  async (t) => {
    const f = await setup(t),
      beforeKeys = Object.keys(process.env).sort();
    let execution;
    const declarations = f.ctx.shellEnv.list();
    assert.ok(declarations.some((entry) => entry.key === 'DSH_AURIGA_TOKEN'));
    assert.equal(
      declarations.some((entry) => entry.key === 'DSH_SESSION_ID'),
      false,
      'reserved ID belongs to public registry itself',
    );
    f.observe(async (exec) => {
      execution = exec;
      const env = f.ctx.shellEnv.collect(exec);
      assert.equal(env.DSH_SESSION_ID, f.binding.sessionId);
      assert.equal(env.DSH_AURIGA_ID, '');
      assert.equal(env.DSH_AURIGA_TOKEN, '');
      assert.ok(env.DSH_TEAM_TASK_CAPABILITY);
      const borrowed = await f.environment.open(exec);
      assert.equal(borrowed.company, false);
      await borrowed.close();
      assert.equal(f.active.size, 1);
    });
    assert.equal((await f.run()).isError, false);
    assert.equal(f.issued.length, 1);
    assert.deepEqual(f.issued[0].scopes, ['task.execute']);
    assert.equal(f.active.size, 0);
    assert.equal(f.ctx.shellEnv.collect(execution).DSH_AURIGA_TOKEN, '');
    await assert.rejects(f.environment.open(execution));
    assert.equal((await f.run('bash')).isError, true);
    assert.equal(f.issued.length, 1);
    assert.equal(
      JSON.stringify(Object.keys(process.env).sort()) ===
        JSON.stringify(beforeKeys),
      true,
    );
  },
);

test(
  'real registry receives ID/token, same helper anchor refreshes rotated material in an old session, all handles close once',
  { skip },
  async (t) => {
    const f = await setup(t, true),
      seen = [];
    let execution;
    f.observe(async (exec) => {
      execution = exec;
      const env = f.ctx.shellEnv.collect(exec);
      seen.push(env.DSH_AURIGA_TOKEN);
      assert.equal(env.DSH_AURIGA_ID, '00001234');
      assert.equal(f.active.size, 2);
      const call = {
        remoteAddress: '127.0.0.1',
        body: {
          sessionId: env.DSH_SESSION_ID,
          taskCapability: env.DSH_TEAM_TASK_CAPABILITY,
        },
      };
      assert.equal(
        (await f.enterprise.credentials(call)).token,
        env.DSH_AURIGA_TOKEN,
      );
      if (seen.length === 1) {
        f.state.version++;
        f.state.material = 'synthetic-rotated-material';
        assert.equal(
          (await f.enterprise.credentials(call)).token,
          'synthetic-rotated-material',
        );
      }
    });
    assert.equal((await f.run()).isError, false);
    assert.equal(f.active.size, 0);
    assert.equal(f.issued.length, 2);
    assert.equal((await f.run()).isError, false);
    assert.equal(f.active.size, 0);
    assert.equal(f.issued.length, 4);
    assert.deepEqual(seen, [
      'synthetic-company-material',
      'synthetic-rotated-material',
    ]);
    assert.equal(f.ctx.shellEnv.collect(execution).DSH_AURIGA_TOKEN, '');
  },
);

test(
  'original enterprise tool borrows the wrapper company handle instead of opening another task',
  { skip },
  async (t) => {
    const f = await setup(t, true);
    let count = 0;
    f.ports.enterprise.outbound = async () => {
      count++;
      return { statusCode: 200, headers: {}, body: '{}' };
    };
    f.state.outboundHosts = ['example.invalid'];
    assert.equal(
      (
        await f.run('auriga_enterprise_request', {
          url: 'https://example.invalid/',
        })
      ).isError,
      false,
    );
    assert.equal(count, 1);
    assert.equal(f.issued.length, 2);
    assert.equal(f.active.size, 0);
  },
);

test(
  'raw-disabled company policy exposes only empty pair while preserving ordinary and outbound tasks',
  { skip },
  async (t) => {
    const f = await setup(t, true);
    f.state.raw = false;
    f.observe(async (exec) => {
      const env = f.ctx.shellEnv.collect(exec);
      assert.equal(env.DSH_AURIGA_TOKEN, '');
      assert.equal(env.DSH_AURIGA_ID, '');
      assert.equal(f.active.size, 2);
    });
    assert.equal((await f.run()).isError, false);
    assert.equal(f.state.reads, 0);
    assert.deepEqual(f.issued[1].scopes, ['outbound']);
    assert.equal(f.active.size, 0);
  },
);

test(
  'a JSON-shaped agent cannot ask the actual ToolRuntime hook for another native task environment',
  { skip },
  async (t) => {
    const f = await setup(t, true);
    const result = await f.ctx.tools.execute({
      name: 'auriga_shared_resources',
      callId: randomUUID(),
      arguments: {},
      agent: { ...f.agent, session: { ...f.agent.session } },
      signal: f.controller.signal,
    });
    assert.equal(result.isError, true);
    assert.equal(f.issued.length, 0);
    assert.equal(f.active.size, 0);
  },
);

for (const kind of ['abort', 'revoke', 'invalid-platform'])
  test(
    `native ${kind} fences registry material and discards the ordinary tool outcome`,
    { skip },
    async (t) => {
      const f = await setup(t, true);
      f.observe(async (exec) => {
        assert.ok(f.ctx.shellEnv.collect(exec).DSH_AURIGA_TOKEN);
        if (kind === 'abort') f.controller.abort();
        else if (kind === 'invalid-platform') f.state.invalidPlatform = true;
        else
          f.host.revoke({
            schemaVersion: 1,
            tenantId: f.binding.tenantId,
            memberId: f.binding.memberId,
            placementId: f.binding.placementId,
            epoch: 2,
            revision: 2,
            authGeneration: 2,
            reason: 'logout',
            revokedAt: Math.floor(Date.now() / 1000),
          });
        if (kind !== 'invalid-platform')
          assert.equal(f.ctx.shellEnv.collect(exec).DSH_AURIGA_TOKEN, '');
      });
      assert.equal((await f.run()).isError, true);
      assert.equal(f.active.size, 0);
    },
  );
