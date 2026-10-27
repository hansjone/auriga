import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { createNativeHost } from './support/assembly-native.mjs';
import {
  createNativeHostPort,
  registrationSession,
} from '../../clients/dsh-plugin/src/network/host/native/host-port.js';
import { createNativeSourceResolver } from '../../clients/dsh-plugin/src/network/host/native/source-context.js';
import { currentNativeProcess } from './support/native-process.mjs';

const processRecord = await currentNativeProcess();
async function cleanup(native, directory, label) {
  await native?.close();
  await rm(directory, { recursive: true, force: true });
  if (process.env.AURIGA_NATIVE_EVIDENCE)
    await writeFile(
      join(process.env.AURIGA_NATIVE_EVIDENCE, label + '.json'),
      JSON.stringify(
        {
          process: processRecord,
          closed: true,
          removed: true,
          events: native?.events ?? [],
        },
        null,
        2,
      ),
    );
}

const options = (directory) => ({
  directory,
  cwd: join(directory, 'cwd'),
  ...(process.env.AURIGA_DSH_RUNTIME
    ? { runtime: process.env.AURIGA_DSH_RUNTIME }
    : {}),
});

test('rc.3 public controller and factory create ordinary real agents; ToolRuntime supplies callback and JSONL barrier', async () => {
  const directory = await mkdtemp(
    new URL('./target/native-case-', import.meta.url),
  );
  let native;
  try {
    native = await createNativeHost(options(directory));
    const created = await native.ctx.sessionController.create({
      sessionId: 'controller-created',
      cwd: join(directory, 'cwd'),
    });
    assert.equal(created.sessionId, 'controller-created');
    assert.equal(
      (await native.ctx.sessionController.resolveAgent(created.sessionId))
        .agent,
      native.ctx.agents.get(created.sessionId),
    );
    const agent = await native.create('ordinary');
    let scoped;
    const injection = native.ctx.inject(
      [
        'tools',
        'sessions',
        'sessionController',
        'agents',
        'sessionPersistence',
        'workspaceRegistry',
      ],
      (ctx) => {
        scoped = ctx;
      },
    );
    await injection;
    assert.ok(scoped, 'real production host injection must activate');
    const host = await createNativeHostPort({ ctx: scoped });
    assert.equal(
      (await native.ctx.sessionController.resolveAgent(agent.id)).agent,
      agent,
    );
    assert.equal(agent.session.header.origin, undefined);
    assert.equal(await host.flush(agent.id), true);
    assert.equal((await host.getSession(agent.id)).cwd, join(directory, 'cwd'));
    const off = scoped.tools.register({
      name: 'native_probe',
      description: 'Observe a real public callback',
      parameters: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      output: {
        schema: { type: 'boolean' },
        render: (_args, value) => [{ type: 'text', text: String(value) }],
      },
      async execute(_args, execution) {
        assert.equal(execution.agent, agent);
        await registrationSession(scoped, execution).verify();
        return true;
      },
    });
    try {
      assert.equal((await native.execute(agent, 'native_probe')).value, true);
    } finally {
      off();
      await injection.dispose();
    }
    assert.deepEqual(native.ctx.llm.listProviders(), []);
    await native.dispose(agent.id);
  } finally {
    await cleanup(native, directory, 'ordinary');
  }
});

test('real tool callback rejects closed, archived, replaced and mismatched native objects', async () => {
  const directory = await mkdtemp(
    new URL('./target/native-lifecycle-', import.meta.url),
  );
  let native;
  try {
    native = await createNativeHost(options(directory));
    const { ctx } = native;
    const old = await native.create('generation');
    const binding = {
      local: '1',
      bindingId: 'local-binding',
      hostSessionId: old.id,
      cwd: old.session.header.cwd,
      state: 'bound',
      lifecycle: 'open',
      kind: 'session',
    };
    const resolve = await createNativeSourceResolver({
      ctx,
      lookupBinding: async () => binding,
    });
    const off = ctx.tools.register({
      name: 'source_probe',
      description: 'Exercise the production resolver from a real callback',
      parameters: { type: 'object', properties: {} },
      output: {
        schema: { type: 'string' },
        render: (_args, value) => [{ type: 'text', text: value }],
      },
      async execute(_args, execution) {
        try {
          return (await resolve(execution)).hostSessionId;
        } catch (error) {
          if (error.code !== 'network.host_incompatible') throw error;
          return error.code;
        }
      },
    });
    const accepted = async (agent) => {
      const result = await native.execute(agent, 'source_probe');
      assert.equal(result.isError, false);
      assert.equal(result.value, old.id);
    };
    const rejected = async (agent, message) => {
      const result = await native.execute(agent, 'source_probe');
      assert.equal(result.isError, false);
      assert.equal(result.value, 'network.host_incompatible', message);
    };
    try {
      await accepted(old);
      await ctx.workspaceRegistry.archiveSession(old.id);
      await rejected(old, 'archived native object must be rejected');
      await ctx.workspaceRegistry.unarchiveSession(old.id);
      await accepted(old);
      await native.dispose(old.id);
      // Check without calling resolveAgent externally (which would resume it).
      await rejected(old, 'closed native object must be rejected');
      const current = await native.resume(old.id);
      assert.notEqual(current, old);
      assert.notEqual(current.session, old.session);
      await accepted(current);
      await rejected(
        old,
        'stale native object must not resolve after real agent/session replacement',
      );
      const other = await native.create('other');
      const mismatch = new Proxy(current, {
        get(target, key) {
          return key === 'session' ? other.session : Reflect.get(target, key);
        },
      });
      await rejected(mismatch, 'session identity mismatch must be rejected');
      assert.ok(
        native.events.some(
          (event) =>
            event.event === 'tools/result' &&
            event.current &&
            event.sessionCurrent &&
            !event.isError,
        ),
      );
      assert.deepEqual(ctx.llm.listProviders(), []);
    } finally {
      off();
    }
  } finally {
    await cleanup(native, directory, 'lifecycle');
  }
});
