import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { publicTurn } from './turn-public-fixture.mjs';

const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
if (process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1' && !root)
  throw new Error('Pinned public-host root is required');
const skip = !root && 'Requires the pinned rc.3 AgentLoop and ToolRuntime';
const deferred = () => {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
};

for (const cancellation of ['none', 'before-close', 'after-close'])
  test(
    'same-turn parallel bodies must really drain before environment close/Finish: cancellation=' +
      cancellation,
    { skip, timeout: 10000 },
    async (t) => {
      const f = await publicTurn(t);
      f.state.company = true;
      const modelEntered = deferred(),
        modelRelease = deferred();
      const entered = [deferred(), deferred()],
        release = [deferred(), deferred()];
      const settled = new Set(),
        taskIds = new Map();
      let nativeSignal;
      f.ctx.on('agent/request', async (payload, next) => {
        nativeSignal = payload.signal;
        return next();
      });
      f.state.model = async function* () {
        modelEntered.resolve();
        await modelRelease.promise;
        yield { type: 'text-delta', index: 0, text: 'done' };
        yield { type: 'finish', reason: { kind: 'stop' } };
      };
      f.ctx.tools.register({
        name: 'parallel_drain_fixture',
        description: 'parallel drain fixture',
        parameters: {
          type: 'object',
          properties: { index: { type: 'integer' } },
          required: ['index'],
          additionalProperties: false,
        },
        output: {
          schema: { type: 'string' },
          render: (_args, value) => [{ type: 'text', text: value }],
        },
        async execute({ index }, execution) {
          const task = await f.environment.open(execution);
          taskIds.set(index, f.ports.enterprise.taskId(task.token));
          entered[index].resolve();
          // Deliberately ignore cancellation until the real body settles.
          await release[index].promise;
          settled.add(index);
          return 'settled';
        },
      });
      const native = f.prompt();
      await modelEntered.promise;
      const executions = [0, 1].map((index) =>
        f.ctx.agents.withInitiator(f.agent, () =>
          f.ctx.tools.execute({
            name: 'parallel_drain_fixture',
            callId: 'parallel-call-' + index,
            arguments: { index },
            agent: f.agent,
            signal: nativeSignal,
          }),
        ),
      );
      await Promise.all(entered.map((item) => item.promise));
      const finishes = () =>
        f.state.events
          .filter((row) => row.operation === 'finishTask')
          .map((row) => row.body.taskId);
      const ordinaryId = f.state.events.find(
        (row) =>
          row.operation === 'beginTask' &&
          row.body.scopes.includes('task.execute'),
      ).body.taskId;
      let closing,
        closed = false;
      try {
        if (cancellation === 'before-close') f.agent.cancel({ kind: 'user' });
        closing = f.environment.close();
        void closing.then(
          () => {
            closed = true;
          },
          () => {},
        );
        if (cancellation === 'after-close') f.agent.cancel({ kind: 'user' });
        await setImmediate();
        await setImmediate();
        assert.equal(settled.size, 0);
        assert.deepEqual(finishes(), [], 'no pending body may Finish');
        assert.equal(
          closed,
          false,
          'close joins cancelled rows even after their active map entries were fenced',
        );
        release[0].resolve();
        await executions[0];
        await setImmediate();
        assert.deepEqual([...settled], [0]);
        assert.ok(finishes().includes(taskIds.get(0)));
        assert.equal(
          finishes().includes(taskIds.get(1)),
          false,
          'the pending sibling retains its own company child',
        );
        assert.equal(closed, false);
        modelRelease.resolve();
        await native;
        await setImmediate();
        assert.ok(
          f.state.active.has(ordinaryId),
          'the real turn has ended but its pending body is still charged',
        );
        assert.equal(finishes().includes(ordinaryId), false);
        release[1].resolve();
        await executions[1];
        await closing;
        await setImmediate();
        assert.deepEqual([...settled], [0, 1]);
        assert.equal(f.state.active.size, 0);
        assert.equal(
          finishes().filter((id) => id === taskIds.get(0)).length,
          1,
        );
        assert.equal(
          finishes().filter((id) => id === taskIds.get(1)).length,
          1,
        );
        assert.equal(finishes().filter((id) => id === ordinaryId).length, 1);
      } finally {
        for (const item of release) item.resolve();
        modelRelease.resolve();
        await Promise.allSettled(executions);
        await native;
        await closing;
      }
    },
  );
