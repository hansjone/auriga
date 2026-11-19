import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { publicTurn } from './turn-public-fixture.mjs';
import { openSharedExecution } from '../../src/shared/task-context.js';

const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
if (process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1' && !root)
  throw new Error('Pinned public-host root is required');
const skip =
  !root && 'Requires the pinned rc.3 AgentLoop/ToolRuntime public composition';
const deferred = () => {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
};
const begin = (f) =>
  f.state.events
    .filter((row) => row.operation === 'beginTask')
    .map((row) => row.body);
const ordinary = (f) =>
  begin(f).filter((row) => row.scopes.includes('task.execute'));

test(
  'an independent actual operation can enqueue a native prompt without racing its own ordinary admission',
  { skip, timeout: 10000 },
  async (t) => {
    const f = await publicTurn(t);
    let native;
    f.state.model = async function* () {
      yield { type: 'text-delta', index: 0, text: 'done' };
      yield { type: 'finish', reason: { kind: 'stop' } };
    };
    f.ctx.tools.register({
      name: 'finite_operation_fixture',
      description: 'finite operation',
      parameters: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      output: {
        schema: { type: 'string' },
        render: (_args, value) => [{ type: 'text', text: value }],
      },
      async execute() {
        native = f.prompt('queued by an independent operation');
        await setImmediate();
        assert.equal(ordinary(f).length, 1);
        assert.equal(
          f.state.models,
          0,
          'the native request waits for the real operation to finish',
        );
        return 'accepted';
      },
    });
    const result = await f.ctx.tools.execute({
      name: 'finite_operation_fixture',
      callId: 'finite-operation',
      arguments: {},
      agent: f.agent,
      signal: new AbortController().signal,
    });
    assert.equal(result.isError, false);
    await native;
    await setImmediate();
    assert.equal(ordinary(f).length, 2);
    assert.equal(f.state.models, 1);
    assert.equal(f.state.active.size, 0);
  },
);

test(
  'actual AgentLoop model→tool→model uses one ordinary turn parent; AVAILABLE never passes the enterprise getter as parent',
  { skip, timeout: 10000 },
  async (t) => {
    const f = await publicTurn(t);
    f.state.company = true;
    let envSeen;
    f.ctx.tools.register({
      name: 'auriga_turn_fixture',
      description: 'finite fixture',
      parameters: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      output: {
        schema: { type: 'string' },
        render: (_args, value) => [{ type: 'text', text: value }],
      },
      async execute(_args, execution) {
        envSeen = f.ctx.shellEnv.collect(execution);
        const task = await f.environment.open(execution);
        assert.equal(
          task.retain,
          undefined,
          'the shared borrowed helper never exposes a background retain API',
        );
        assert.ok(
          f.state.tokens.get(task.token).scopes.includes('credentials'),
        );
        assert.equal(envSeen.DSH_AURIGA_ID, '00001234');
        // The message lane consumes the actual private ToolRuntime execution.
        const message = await f.ports.downstream.taskForSession(
          f.binding,
          execution.signal,
          {
            audience: 'auriga.shared.message',
            scope: 'message.send',
            execution,
          },
        );
        await message.close();
        return 'done';
      },
    });
    f.state.model = async function* () {
      if (f.state.models === 1) {
        yield {
          type: 'block-end',
          index: 0,
          block: {
            type: 'tool-call',
            id: 'fixture-call',
            name: 'auriga_turn_fixture',
            arguments: '{}',
          },
        };
        yield { type: 'finish', reason: { kind: 'tool-calls' } };
      } else {
        yield { type: 'text-delta', index: 0, text: 'done' };
        yield { type: 'finish', reason: { kind: 'stop' } };
      }
    };
    await f.prompt();
    await setImmediate();
    assert.equal(f.state.models, 2);
    assert.equal(ordinary(f).length, 1);
    const rootTask = ordinary(f)[0].taskId;
    for (const child of begin(f).filter(
      (row) => !row.scopes.includes('task.execute'),
    ))
      assert.equal(f.state.tokens.get(child.parentCapability).taskId, rootTask);
    assert.ok(begin(f).some((row) => row.scopes.includes('credentials')));
    assert.ok(begin(f).some((row) => row.scopes.includes('message.send')));
    assert.equal(f.state.active.size, 0);
  },
);

test(
  'real turn/end fences synchronously while explicit Finish acknowledgement remains pending, and the next turn waits',
  { skip, timeout: 10000 },
  async (t) => {
    const f = await publicTurn(t),
      entered = deferred(),
      release = deferred();
    f.state.model = async function* () {
      yield { type: 'text-delta', index: 0, text: 'done' };
      yield { type: 'finish', reason: { kind: 'stop' } };
    };
    let closedAtEvent = false,
      late;
    f.state.beforeFinish = async (body) => {
      if (body.taskId === ordinary(f)[0].taskId) {
        entered.resolve();
        await release.promise;
      }
    };
    f.ctx.on('session/event', (_session, event) => {
      if (event.type === 'turn/end') {
        closedAtEvent = true;
        const model = begin(f).find((row) =>
          row.scopes.includes('model.invoke'),
        );
        late = f.ports.downstream.issue(
          f.binding,
          model.taskId,
          model.audience,
          model.scopes,
        );
        void late.catch(() => {});
      }
    });
    await f.prompt();
    await entered.promise;
    assert.equal(closedAtEvent, true);
    await assert.rejects(late);
    assert.equal(f.state.active.size, 1);
    const second = f.prompt('next');
    await setImmediate();
    assert.equal(ordinary(f).length, 1);
    release.resolve();
    await second;
    await setImmediate();
    assert.equal(ordinary(f).length, 2);
    assert.equal(f.state.active.size, 0);
  },
);

test(
  'actual cancelled ToolRuntime body stays charged until it settles and the native finally ends the turn',
  { skip, timeout: 10000 },
  async (t) => {
    const f = await publicTurn(t),
      entered = deferred(),
      release = deferred();
    f.state.company = true;
    let execution;
    f.ctx.tools.register({
      name: 'auriga_turn_fixture',
      description: 'finite fixture',
      parameters: {
        type: 'object',
        properties: {},
        additionalProperties: false,
      },
      output: {
        schema: { type: 'string' },
        render: (_args, value) => [{ type: 'text', text: value }],
      },
      async execute(_args, exec) {
        execution = exec;
        entered.resolve();
        await release.promise;
        return 'late';
      },
    });
    f.state.model = async function* () {
      yield {
        type: 'block-end',
        index: 0,
        block: {
          type: 'tool-call',
          id: 'cancelled-call',
          name: 'auriga_turn_fixture',
          arguments: '{}',
        },
      };
      yield { type: 'finish', reason: { kind: 'tool-calls' } };
    };
    const running = f.prompt();
    try {
      await entered.promise;
      f.agent.cancel({ kind: 'user' });
      assert.equal(f.ctx.shellEnv.collect(execution).DSH_AURIGA_TOKEN, '');
      await setImmediate();
      assert.equal(ordinary(f).length, 1);
      assert.ok(f.state.active.has(ordinary(f)[0].taskId));
      assert.equal(
        f.state.events.filter((row) => row.operation === 'finishTask').length,
        0,
      );
    } finally {
      release.resolve();
    }
    await running;
    await setImmediate();
    assert.equal(f.state.active.size, 0);
  },
);

for (const invalid of ['empty', 'task', 'binding'])
  test(
    'an actual native turn retains closing on invalid Finish echo: ' + invalid,
    { skip, timeout: 10000 },
    async (t) => {
      const f = await publicTurn(t);
      f.state.expectCloseFailure = /common.unavailable|shared.binding_mismatch/;
      f.state.model = async function* () {
        yield { type: 'text-delta', index: 0, text: 'done' };
        yield { type: 'finish', reason: { kind: 'stop' } };
      };
      f.state.finishEcho = (body) => {
        if (body.taskId !== ordinary(f)[0].taskId) {
          f.state.active.delete(body.taskId);
          return { fenced: true, binding: body.binding, taskId: body.taskId };
        }
        return invalid === 'empty'
          ? {}
          : invalid === 'task'
            ? { fenced: true, binding: body.binding, taskId: 'wrong-task' }
            : {
                fenced: true,
                binding: { ...body.binding, memberId: 'wrong-member' },
                taskId: body.taskId,
              };
      };
      await f.prompt();
      await setImmediate();
      assert.equal(f.state.active.size, 1);
      await f.prompt('after unknown finish');
      assert.equal(ordinary(f).length, 1);
      assert.equal(f.state.models, 1);
      assert.equal(f.state.active.size, 1);
    },
  );

test(
  'a revoked root rejects late model bytes even while Node still reports its tasks active',
  { skip, timeout: 10000 },
  async (t) => {
    const f = await publicTurn(t),
      entered = deferred(),
      release = deferred(),
      messages = [];
    f.ctx.on('session/event', (_session, event) => {
      if (event.type === 'assistant/message') messages.push(event.data);
    });
    f.state.model = async function* () {
      entered.resolve();
      await release.promise;
      yield { type: 'text-delta', index: 0, text: 'STALE_MODEL_BYTES' };
      yield { type: 'finish', reason: { kind: 'stop' } };
    };
    const pending = f.prompt();
    try {
      await entered.promise;
      f.ports.disconnect(f.binding);
      assert.ok(f.state.active.size > 0);
      assert.equal(
        f.state.events.some((row) => row.operation === 'finishTask'),
        false,
      );
    } finally {
      release.resolve();
    }
    await pending;
    await setImmediate();
    assert.equal(JSON.stringify(messages).includes('STALE_MODEL_BYTES'), false);
    assert.equal(f.state.active.size, 0);
  },
);

test(
  'a prior native turn signal or JSON-shaped tool handle cannot borrow the next actual turn parent',
  { skip, timeout: 10000 },
  async (t) => {
    const f = await publicTurn(t),
      entered = deferred(),
      release = deferred(),
      signals = [];
    f.ctx.on('agent/request', async (payload, next) => {
      signals.push(payload.signal);
      return next();
    });
    f.state.model = async function* () {
      if (f.state.models === 2) {
        entered.resolve();
        await release.promise;
      }
      yield { type: 'text-delta', index: 0, text: 'done' };
      yield { type: 'finish', reason: { kind: 'stop' } };
    };
    await f.prompt();
    await setImmediate();
    const pending = f.prompt('next');
    try {
      await entered.promise;
      await f.ctx.agents.withInitiator(f.agent, async () => {
        await assert.rejects(
          f.ports.downstream.taskForSession(f.binding, undefined, {
            audience: 'auriga.shared.model',
            scope: 'model.invoke',
            nativeSignal: signals[0],
          }),
        );
        await assert.rejects(
          f.ports.downstream.taskForSession(f.binding, undefined, {
            audience: 'auriga.shared.message',
            scope: 'message.send',
            execution: { agent: f.agent, token: Symbol('unregistered') },
          }),
        );
      });
      await assert.rejects(
        f.ports.issueTask(f.binding, { agent: f.agent }, ['outbound'], {
          ordinaryToken: 'forged',
        }),
      );
      assert.equal(ordinary(f).length, 2);
    } finally {
      release.resolve();
    }
    await pending;
  },
);

test(
  'an unknown ordinary Begin plus empty Finish confirmation blocks replacement roots',
  { skip, timeout: 10000 },
  async (t) => {
    const f = await publicTurn(t);
    f.state.expectCloseFailure = /common.unavailable/;
    f.state.beforeBegin = async (body) => {
      f.state.active.set(body.taskId, body);
      throw new Error('synthetic unknown Begin');
    };
    f.state.finishEcho = () => ({});
    await f.prompt();
    await setImmediate();
    assert.equal(f.state.active.size, 1);
    f.state.beforeBegin = undefined;
    await f.prompt('retry after unknown');
    assert.equal(ordinary(f).length, 1);
    assert.equal(f.state.models, 0);
    assert.equal(f.state.active.size, 1);
  },
);

for (const revoke of [false, true])
  test(
    'trusted retained execution keeps the actual native parent through turn/end and settles exactly once: revoke=' +
      revoke,
    { skip, timeout: 10000 },
    async (t) => {
      const f = await publicTurn(t, { toolEnvironment: false });
      f.state.company = true;
      let task, settle;
      f.ctx.on('tools/execute', async (execution, next) => {
        task = await openSharedExecution({
          ctx: f.ctx,
          runtime: f.runtime,
          ports: f.ports,
          execution,
          raw: true,
        });
        // Explicit internal retention tests the common lifetime primitive. The
        // dedicated integration must separately prove real LocalJobRegistry
        // registered/session-event causality before it may call this method.
        settle = task.retain();
        return next();
      });
      f.ctx.tools.register({
        name: 'retained_fixture',
        description: 'internal lifecycle fixture',
        parameters: {
          type: 'object',
          properties: {},
          additionalProperties: false,
        },
        output: {
          schema: { type: 'string' },
          render: (_args, value) => [{ type: 'text', text: value }],
        },
        execute: async () => 'started',
      });
      f.state.model = async function* () {
        if (f.state.models === 1) {
          yield {
            type: 'block-end',
            index: 0,
            block: {
              type: 'tool-call',
              id: 'retained-call',
              name: 'retained_fixture',
              arguments: '{}',
            },
          };
          yield { type: 'finish', reason: { kind: 'tool-calls' } };
        } else {
          yield { type: 'text-delta', index: 0, text: 'done' };
          yield { type: 'finish', reason: { kind: 'stop' } };
        }
      };
      await f.prompt();
      let next;
      try {
        assert.equal(ordinary(f).length, 1);
        assert.ok(f.state.active.has(ordinary(f)[0].taskId));
        if (revoke) {
          f.ports.disconnect(f.binding);
          await assert.rejects(task.refresh());
          assert.equal(task.env, undefined);
          assert.ok(f.state.active.size > 0);
        } else {
          assert.equal((await task.refresh()).DSH_AURIGA_ID, '00001234');
          const waitingClose = task.close();
          assert.equal(task.close(), waitingClose);
          assert.equal(
            task.env.DSH_AURIGA_ID,
            '00001234',
            'close waits for the real retained terminal event',
          );
          next = f.prompt('wait for retained work');
          await setImmediate();
          assert.equal(ordinary(f).length, 1);
          assert.equal(f.state.models, 2);
        }
      } finally {
        await settle();
        await settle();
        await task.close();
      }
      await next;
      await setImmediate();
      assert.equal(f.state.active.size, 0);
      assert.equal(ordinary(f).length, revoke ? 1 : 2);
    },
  );
