/* global AbortController */
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { join } from 'node:path';
import { currentNativeProcess } from '../native-process.mjs';
export const name = 'auriga-send4-observer';
export const inject = ['webServer', 'connection'];

// Test control only, inside the real CLI. It delegates public native services;
// it never provides a controller/agent, invokes a tool definition or accepts mail.
export function apply(parent) {
  return parent.inject(
    [
      'tools',
      'sessions',
      'agents',
      'sessionController',
      'sessionPersistence',
      'workspaceRegistry',
      'profileContext',
    ],
    async (ctx) => {
      const processIdentity = await currentNativeProcess();
      const owners = new Map(),
        events = [],
        prompts = [];
      const refs = new WeakMap();
      const disposeOwners = async () => {
        for (const owner of owners.values()) await owner.dispose();
        assert.ok(
          events.filter((v) => v.event === 'disposed').length >= owners.size,
        );
        owners.clear();
      };
      let ref = 0;
      const identity = (value) => {
        if (!refs.has(value)) refs.set(value, ++ref);
        return refs.get(value);
      };
      const describe = (agent) => ({
        id: agent.id,
        agent: identity(agent),
        session: identity(agent.session),
        current: ctx.agents.get(agent.id) === agent,
        sessionCurrent: ctx.sessions.get(agent.id) === agent.session,
        ordinary: agent.session.header.origin === undefined,
        cwd: agent.session.header.cwd,
      });
      ctx.on('agent/created', ({ agent }) =>
        events.push({ event: 'created', ...describe(agent) }),
      );
      ctx.on('agent/disposed', ({ agent }) =>
        events.push({ event: 'disposed', id: agent.id }),
      );
      ctx.on('tools/execute', async (execution, next) => {
        events.push({
          event: 'execute',
          callId: execution.callId,
          name: execution.name,
          ...describe(execution.agent),
        });
        return next();
      });
      const controller = ctx.sessionController;
      const original = controller.prompt;
      controller.prompt = function (input, signal) {
        prompts.push({
          sessionId: input.sessionId,
          requestId: input.requestId,
        });
        return original.call(this, input, signal);
      };
      ctx.effect(() => () => {
        controller.prompt = original;
      });
      ctx.effect(() =>
        ctx.webServer.register({
          kind: 'exact',
          path: '/send4/control',
          async handler(req, res) {
            const reject = ctx.connection.requestRejection(req);
            if (reject !== undefined) {
              res.writeHead(reject);
              res.end();
              return;
            }
            if (
              req.method !== 'POST' ||
              req.headers.origin !== `http://${req.headers.host}`
            ) {
              res.writeHead(403);
              res.end();
              return;
            }
            let result;
            try {
              const chunks = [];
              let size = 0;
              for await (const chunk of req) {
                size += chunk.length;
                assert.ok(size <= 16384);
                chunks.push(chunk);
              }
              const command = JSON.parse(Buffer.concat(chunks).toString());
              if (command.op === 'capabilities') {
                result = {
                  join: !!ctx.tools.get('auriga_network_join'),
                  send: !!ctx.tools.get('auriga_send_message'),
                };
              } else if (command.op === 'create') {
                assert.match(command.id, /^send4-[a-z-]+$/);
                assert.ok(!owners.has(command.id));
                const owner = await ctx.agents.create({
                  sessionId: command.id,
                  meta: {
                    cwd: join(
                      process.env.AURIGA_SEND4_STATE,
                      command.other ? 'other-cwd' : 'session-cwd',
                    ),
                  },
                });
                owners.set(command.id, owner);
                const agent = ctx.agents.get(command.id);
                assert.ok(agent);
                assert.equal(await ctx.sessions.flush(agent.session), true);
                await ctx.sessionPersistence.flush();
                result = describe(agent);
              } else if (command.op === 'tool') {
                const agent = ctx.agents.get(command.session);
                assert.ok(agent);
                result = await ctx.tools.execute({
                  callId: command.callId,
                  name: command.name,
                  arguments: command.arguments ?? {},
                  agent,
                  signal: new AbortController().signal,
                });
              } else if (command.op === 'inspect') {
                const record = await controller.inspect(command.session);
                await ctx.sessionPersistence.flush();
                const reader = await ctx.sessionPersistence.open(
                  command.session,
                  'read',
                );
                try {
                  result = {
                    record,
                    persisted: await reader.read(),
                    events,
                    prompts,
                    profile: ctx.profileContext.dir,
                    ...processIdentity,
                  };
                } finally {
                  await reader.close();
                }
              } else if (command.op === 'stop') {
                result = { disposed: owners.size };
                await disposeOwners();
                res.once('finish', () => {
                  void ctx.root.fiber.dispose().catch(() => {
                    process.exitCode = 1;
                  });
                });
              } else throw new Error('unknown control');
              res.writeHead(200, { 'content-type': 'application/json' });
              res.end(JSON.stringify(result));
            } catch (error) {
              res.writeHead(500, { 'content-type': 'application/json' });
              res.end(
                JSON.stringify({
                  error: error.message,
                  name: error.name,
                  code: error.code,
                }),
              );
            }
          },
        }),
      );
      // Parent-owned IPC carries shutdown only, including before platform login.
      // Native HTTP admission remains unchanged; no business control uses IPC.
      ctx.effect(() => {
        let stopping = false;
        const stop = (message) => {
          if (message?.type !== 'send4-stop' || stopping) return;
          stopping = true;
          void (async () => {
            try {
              await disposeOwners();
              await ctx.root.fiber.dispose();
            } catch {
              process.exitCode = 1;
            } finally {
              if (process.connected) process.disconnect();
            }
          })();
        };
        process.on('message', stop);
        process.send?.({ type: 'send4-observer-ready' });
        return () => process.off('message', stop);
      });
    },
  );
}
