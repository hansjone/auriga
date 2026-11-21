// @ts-nocheck
import { executionSession } from '../shared/task-context.js';
import { fail } from '../shared/protocol.js';
import { createDedicatedExecutionJobs } from './execution-jobs.js';

const keys = [
  'DSH_AURIGA_ID',
  'DSH_AURIGA_TOKEN',
  'DSH_TEAM_TASK_CAPABILITY',
  'DSH_TEAM_BASE',
  'DSH_TEAM_HELPERS',
  'DSH_WEB_URL',
];
const empty = Object.freeze(Object.fromEntries(keys.map((key) => [key, ''])));

/** tools/execute is asynchronous while ShellEnvRegistry.collect is synchronous.
 * Only the exact opaque native execution can read its current prepared row. */
export function installDedicatedEnvironment(
  ctx,
  { runtime, resources, openTask, resolveTaskId, activeExecutions = new Set() },
) {
  if (!ctx.shellEnv?.register || !ctx.on || typeof openTask !== 'function')
    fail('common.unavailable');
  const rows = new Map();
  let closed = false,
    closing;
  const keyOf = (execution) => {
    if (
      !execution?.token ||
      !['object', 'symbol'].includes(typeof execution.token)
    )
      fail('shared.revoked');
    return execution.token;
  };
  const check = (row, execution) => {
    if (
      closed ||
      !row.active ||
      row.execution !== execution ||
      rows.get(row.key) !== row ||
      execution.signal?.aborted ||
      row.task?.signal?.aborted ||
      executionSession(ctx, execution) !== row.sessionId ||
      !resources.isCurrent()
    )
      fail('shared.revoked');
  };
  const snapshot = (row) => {
    for (const name of keys)
      if (
        row.task.env?.[name] !== undefined &&
        typeof row.task.env[name] !== 'string'
      )
        fail('common.unavailable');
    row.snapshot = Object.freeze(
      Object.fromEntries(
        keys.map((name) => [name, row.task.env?.[name] ?? '']),
      ),
    );
  };
  const release = (row) => {
    row.closing ??= (async () => {
      try {
        await row.ready;
      } catch {
        /* opening owns its compensation */
      }
      await row.bodyDone;
      // A stop is only a fence, never process-settlement proof. Return the
      // private retain after all native jobs settle; then confirm child and
      // ordinary Finish. An unknown Finish keeps this barrier and budget held.
      await row.jobs?.settle();
      row.active = false;
      row.snapshot = empty;
      clearInterval(row.timer);
      row.originalSignal?.removeEventListener('abort', row.abort);
      row.task?.signal?.removeEventListener('abort', row.abort);
      row.abortController.abort();
      if (!Object.isFrozen(row.execution) && !row.fenced)
        row.execution.signal = row.originalSignal;
      await row.task?.close();
      row.jobs?.close();
      if (rows.get(row.key) === row) rows.delete(row.key);
      activeExecutions.delete(row.execution);
    })();
    return row.closing;
  };
  const stop = (row) => {
    row.fenced = true;
    row.active = false;
    row.snapshot = empty;
    clearInterval(row.timer);
    row.abortController.abort();
    row.jobs?.kill();
    return release(row);
  };
  const detachEnv = ctx.shellEnv.register({
    name: 'auriga-dedicated-current-task',
    variables: Object.fromEntries(
      keys.map((key) => [
        key,
        {
          description:
            key.includes('TOKEN') || key.includes('CAPABILITY')
              ? 'Current task secret; never print or persist.'
              : 'Current trusted dedicated task context.',
        },
      ]),
    ),
    resolve(execution) {
      const row = rows.get(execution?.token);
      if (!row) return { ...empty };
      check(row, execution);
      return { ...row.snapshot };
    },
  });
  const detachExecute = ctx.on('tools/execute', async (execution, next) => {
    const key = keyOf(execution);
    if (rows.has(key)) fail('common.conflict');
    const sessionId = executionSession(ctx, execution);
    await resources.verify();
    await runtime.session(sessionId);
    const originalSignal = execution.signal,
      abortController = new AbortController();
    const row = {
      key,
      execution,
      sessionId,
      active: true,
      fenced: false,
      snapshot: empty,
      originalSignal,
      abortController,
      task: undefined,
      ready: undefined,
      jobs: undefined,
      closing: undefined,
      timer: undefined,
      abort: undefined,
    };
    rows.set(key, row);
    activeExecutions.add(execution);
    row.ready = Promise.resolve()
      .then(() => openTask(execution))
      .then((task) => {
        row.task = task;
        return task;
      });
    row.abort = () => {
      void stop(row).catch(() => {});
    };
    originalSignal?.addEventListener('abort', row.abort, { once: true });
    let retained = false;
    try {
      await row.ready;
      if (
        typeof row.task?.refresh !== 'function' ||
        typeof row.task?.company !== 'boolean'
      )
        fail('common.unavailable');
      check(row, execution);
      // The common turn authority must first see the original native signal.
      // Afterwards this exact mutable execution may fuse our private fence.
      execution.signal = globalThis.AbortSignal.any(
        [originalSignal, abortController.signal, row.task.signal].filter(
          Boolean,
        ),
      );
      if (typeof resolveTaskId === 'function')
        row.taskId = await resolveTaskId(row.task.token);
      check(row, execution);
      row.task.signal?.addEventListener('abort', row.abort, { once: true });
      row.jobs = createDedicatedExecutionJobs(ctx, {
        execution,
        task: row.task,
        check: () => check(row, execution),
        stop: row.abort,
      });
      snapshot(row);
      let refreshing = false;
      row.timer = setInterval(() => {
        if (refreshing) return;
        refreshing = true;
        void (async () => {
          await resources.verify();
          await row.task.refresh();
          check(row, execution);
          snapshot(row);
        })()
          .catch(() => stop(row))
          .finally(() => {
            refreshing = false;
          })
          .catch(() => {});
      }, 1000);
      row.timer.unref?.();
      let bodyEnded;
      row.bodyDone = new Promise((resolve) => {
        bodyEnded = resolve;
      });
      let result;
      try {
        result = await row.jobs.run(next);
      } finally {
        bodyEnded();
      }
      await resources.verify();
      await row.task.refresh();
      check(row, execution);
      retained = row.jobs.handoff(result);
      return result;
    } catch (error) {
      void stop(row).catch(() => {});
      throw error;
    } finally {
      if (retained) {
        // The foreground returns while the private row observes real terminal
        // events. Its task lease and active execution stay held throughout.
        void release(row).catch(() => {});
      } else {
        if (!row.fenced) execution.signal = originalSignal;
        row.jobs?.kill();
        await release(row);
      }
    }
  });
  const detachDisposed = ctx.on('agent/disposed', ({ agent }) => {
    for (const row of rows.values())
      if (row.execution.agent === agent) void stop(row).catch(() => {});
  });
  return {
    activeExecutions,
    companyTask(execution) {
      const row = rows.get(keyOf(execution));
      if (!row) fail('shared.revoked');
      check(row, execution);
      if (!row.task.company) fail('shared.revoked');
      return row.task;
    },
    async helperContext(sessionId, { token, taskId } = {}) {
      if (typeof resolveTaskId !== 'function') fail('common.unavailable');
      await resources.verify();
      const id = token === undefined ? taskId : await resolveTaskId(token);
      if (typeof id !== 'string' || !id) fail('shared.revoked');
      const row = [...rows.values()].find(
        (entry) =>
          entry.sessionId === sessionId &&
          entry.taskId === id &&
          entry.task?.company,
      );
      if (!row) fail('shared.revoked');
      check(row, row.execution);
      return { signal: row.execution.signal };
    },
    close() {
      closing ??= (async () => {
        closed = true;
        detachExecute();
        detachDisposed();
        detachEnv();
        const result = await Promise.allSettled([...rows.values()].map(stop));
        const failed = result.find((entry) => entry.status === 'rejected');
        if (failed) throw failed.reason;
      })();
      return closing;
    },
  };
}
