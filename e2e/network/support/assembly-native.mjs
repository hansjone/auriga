// Public rc.3 services only. No model adapter is registered and no prompt runs.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { defaultRuntime, hostPin } from './native-install.mjs';

export async function createNativeHost({
  directory,
  cwd,
  runtime = defaultRuntime,
}) {
  const require = createRequire(join(runtime, 'package.json'));
  assert.equal(
    require('@deepseek-ai/dsh/package.json').version,
    hostPin.version,
  );
  const load = (name) =>
    import(pathToFileURL(require.resolve('@deepseek-ai/' + name)).href);
  const { Context } = await load('cordis');
  const ctx = new Context();
  const fibers = [];
  const mount = async (name, config = {}) => {
    const mod = await load(name);
    const fiber = ctx.plugin(mod.default ?? mod, config);
    fibers.push(fiber);
    await fiber;
    return mod;
  };
  const events = [];
  const references = new WeakMap();
  let nextReference = 0;
  const reference = (object) => {
    if (!references.has(object)) references.set(object, ++nextReference);
    return references.get(object);
  };
  let closed = false;
  const close = async () => {
    if (closed) return;
    closed = true;
    // Agent factory first, then controller/readers, then storage backend.
    const failures = [];
    for (const fiber of fibers.reverse()) {
      try {
        await fiber.dispose();
      } catch (error) {
        failures.push(error);
      }
    }
    if (failures.length)
      throw new AggregateError(failures, 'native teardown failed');
  };
  try {
    await mkdir(cwd, { recursive: true });
    await mount('dsh-typert-registry');
    await mount('dsh-llm');
    await mount('dsh-session');
    await mount('dsh-session-projection');
    await mount('dsh-system-prompt');
    await mount('dsh-tools');
    await mount('dsh-agent');
    await mount('dsh-session-persistence-jsonl', {
      root: join(directory, 'sessions'),
      compression: 'none',
    });
    await mount('dsh-session-query-sqlite', {
      path: join(directory, 'query.sqlite'),
      openAt: 'never',
    });
    await mount('dsh-storage');
    await mount('dsh-storage-json', { root: join(directory, 'storage') });
    await mount('dsh-storage-domain', { backend: 'json' });
    await mount('dsh-workspace');
    await mount('dsh-attachment-local', { dshHome: join(directory, 'dsh') });
    await mount('dsh-commands');
    await mount('dsh-credentials-local', {
      dshHome: join(directory, 'dsh'),
      watch: false,
    });
    await mount('dsh-client-connection', { trustedHosts: [] });
    await mount('dsh-client-file-upload');
    await mount('dsh-fs-local', { cwd });
    await mount('dsh-agent-default-model', {
      provider: 'unconfigured',
      model: 'unconfigured',
    });
    await mount('dsh-api-remote-access');
    await mount('dsh-api-session-controller', { nativeOpen: false });
    await mount('dsh-agent-loop', { agents: [] });
    assert.deepEqual(ctx.llm.listProviders(), []);
    const live = new Map(),
      owners = new Map();
    ctx.on('agent/created', ({ agent }) => {
      live.set(agent.id, agent);
      events.push({
        event: 'agent/created',
        id: agent.id,
        agentObject: reference(agent),
        sessionObject: reference(agent.session),
        ordinary: agent.session.header.origin === undefined,
        current: ctx.agents.get(agent.id) === agent,
        sessionCurrent: ctx.sessions.get(agent.id) === agent.session,
      });
    });
    ctx.on('agent/disposed', ({ agent }) => {
      live.delete(agent.id);
      events.push({
        event: 'agent/disposed',
        id: agent.id,
        agentObject: reference(agent),
        sessionObject: reference(agent.session),
      });
    });
    ctx.on('tools/execute', async (execution, next) => {
      events.push({
        event: 'tools/execute',
        callId: execution.callId,
        name: execution.name,
        id: execution.agent.id,
        agentObject: reference(execution.agent),
        sessionObject: reference(execution.agent.session),
        current: ctx.agents.get(execution.agent.id) === execution.agent,
        sessionCurrent:
          ctx.sessions.get(execution.agent.id) === execution.agent.session,
      });
      return next();
    });
    ctx.on('tools/result', (execution, result) => {
      events.push({
        event: 'tools/result',
        callId: execution.callId,
        name: execution.name,
        id: execution.agent.id,
        agentObject: reference(execution.agent),
        sessionObject: reference(execution.agent.session),
        current: ctx.agents.get(execution.agent.id) === execution.agent,
        sessionCurrent:
          ctx.sessions.get(execution.agent.id) === execution.agent.session,
        isError: result.isError,
        status: result.value?.status,
      });
    });
    return {
      ctx,
      events,
      close,
      async create(id) {
        const owner = await ctx.agents.create({ sessionId: id, meta: { cwd } });
        owners.set(id, owner);
        const resolved = await ctx.sessionController.resolveAgent(id);
        assert.equal(resolved.error, undefined);
        assert.equal(resolved.agent, live.get(id));
        assert.equal(await ctx.sessions.flush(resolved.agent.session), true);
        await ctx.sessionPersistence.flush();
        const record = await ctx.sessionController.inspect(id);
        assert.equal(record.meta.id, id);
        assert.equal(record.meta.cwd, cwd);
        assert.equal(record.meta.origin, undefined);
        events.push({
          event: 'session/durable',
          id,
          agentObject: reference(resolved.agent),
          sessionObject: reference(resolved.agent.session),
          eventCount: record.events.length,
        });
        return resolved.agent;
      },
      async execute(agent, name = 'auriga_network_join') {
        return ctx.tools.execute({
          callId: randomUUID(),
          name,
          arguments: {},
          agent,
          signal: new AbortController().signal,
        });
      },
      async resume(id) {
        assert.equal(ctx.agents.get(id), undefined);
        const owner = await ctx.agents.resume({ resumeSessionId: id });
        owners.set(id, owner);
        assert.equal(
          (await ctx.sessionController.resolveAgent(id)).agent,
          owner.agent,
        );
        return owner.agent;
      },
      async dispose(id) {
        const owner = owners.get(id);
        assert.ok(owner, 'missing native session owner');
        await owner.dispose();
        owners.delete(id);
        assert.equal(
          ctx.agents.get(id),
          undefined,
          'native owner must drain and unregister its real agent',
        );
        assert.equal(
          ctx.sessions.get(id),
          undefined,
          'native owner must detach its real session',
        );
      },
    };
  } catch (error) {
    await close();
    throw error;
  }
}
