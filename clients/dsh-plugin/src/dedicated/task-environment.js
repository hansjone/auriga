// @ts-nocheck
import { executionSession } from '../shared/task-context.js';
import { fail } from '../shared/protocol.js';

const keys = [
  'DSH_AURIGA_ID',
  'DSH_AURIGA_TOKEN',
  'DSH_TEAM_TASK_CAPABILITY',
  'DSH_TEAM_BASE',
  'DSH_TEAM_HELPERS',
  'DSH_WEB_URL',
];
const empty = Object.freeze(Object.fromEntries(keys.map((key) => [key, ''])));

/** The public tools/execute waterfall is asynchronous; shellEnv.collect is
 * synchronous. Only the exact native execution token can join the two. */
export function installDedicatedEnvironment(
  ctx,
  { runtime, resources, openTask, activeExecutions = new Set() },
) {
  if (!ctx.shellEnv?.register || !ctx.on || typeof openTask !== 'function')
    fail('common.unavailable');
  const rows = new Map();
  let closed = false;
  const keyOf = (execution) => {
    if (
      !execution?.token ||
      (typeof execution.token !== 'object' &&
        typeof execution.token !== 'symbol')
    )
      fail('shared.revoked');
    return execution.token;
  };
  const check = (row, execution) => {
    if (
      closed ||
      row.execution !== execution ||
      rows.get(row.key) !== row ||
      execution.signal?.aborted ||
      executionSession(ctx, execution) !== row.sessionId ||
      !resources.isCurrent()
    )
      fail('shared.revoked');
  };
  const release = async (row) => {
    if (rows.get(row.key) === row) rows.delete(row.key);
    activeExecutions.delete(row.execution);
    row.snapshot = empty;
    row.originalSignal?.removeEventListener('abort', row.abort);
    clearInterval(row.timer);
    row.detachJob?.();
    row.abortController.abort();
    // Public ToolRuntime freezes the execution when tools/result is emitted;
    // a retained native job must finish without mutating that frozen object.
    if (!Object.isFrozen(row.execution))
      row.execution.signal = row.originalSignal;
    await row.task?.close();
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
  const stop = async (row) => {
    if (row.stopping) return;
    row.stopping = true;
    row.abortController.abort();
    row.detachJob?.();
    row.detachJob = undefined;
    if (row.jobId) {
      try {
        await ctx.jobs.kill(
          row.jobId,
          row.sessionId,
          'Auriga task authority ended',
        );
      } catch {
        /* registry already settled */
      }
    }
    await release(row);
  };
  const retainJob = (row, result) => {
    const value = result?.value;
    if (
      result?.isError ||
      !['background', 'promoted'].includes(value?.kind) ||
      typeof value.jobId !== 'string'
    )
      return false;
    if (
      !ctx.jobs?.events?.subscribe ||
      typeof ctx.jobs.list !== 'function' ||
      typeof ctx.jobs.kill !== 'function'
    )
      fail('common.unavailable');
    const job = ctx.jobs
      .list(row.sessionId)
      .find((entry) => entry.id === value.jobId);
    if (!job || job.owner !== row.sessionId) fail('shared.revoked');
    if (['completed', 'killed', 'failed', 'stopping'].includes(job.status))
      return false;
    if (job.status !== 'running') fail('shared.revoked');
    row.jobId = job.id;
    row.detachJob = ctx.jobs.events.subscribe(
      { owner: row.sessionId },
      (event) => {
        if (
          event.job?.id === row.jobId &&
          ['stopping', 'settled', 'removed'].includes(event.type)
        )
          void stop(row).catch(() => {});
      },
    );
    return true;
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
    const originalSignal = execution.signal;
    const abortController = new AbortController();
    execution.signal = globalThis.AbortSignal.any(
      [originalSignal, abortController.signal].filter(Boolean),
    );
    const row = {
      key,
      execution,
      sessionId,
      snapshot: empty,
      task: undefined,
      abort: undefined,
      originalSignal,
      abortController,
      timer: undefined,
      detachJob: undefined,
      jobId: undefined,
    };
    rows.set(key, row);
    activeExecutions.add(execution);
    row.abort = () => {
      void stop(row).catch(() => {});
    };
    originalSignal?.addEventListener('abort', row.abort, { once: true });
    let retained = false;
    try {
      row.task = await openTask(execution);
      if (
        typeof row.task?.refresh !== 'function' ||
        typeof row.task?.company !== 'boolean'
      )
        fail('common.unavailable');
      check(row, execution);
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
      const result = await next();
      await resources.verify();
      await row.task.refresh();
      check(row, execution);
      retained = retainJob(row, result);
      return result;
    } finally {
      if (!retained) {
        execution.signal = originalSignal;
        await release(row);
      }
    }
  });
  const detachDisposed = ctx.on('agent/disposed', ({ agent }) => {
    for (const row of rows.values())
      if (row.execution.agent === agent) void stop(row).catch(() => {});
  });
  const installed = {
    activeExecutions,
    companyTask(execution) {
      const row = rows.get(keyOf(execution));
      if (!row) fail('shared.revoked');
      check(row, execution);
      if (!row.task.company) fail('shared.revoked');
      return row.task;
    },
    async close() {
      if (closed) return;
      closed = true;
      detachExecute();
      detachDisposed();
      detachEnv();
      await Promise.allSettled([...rows.values()].map(stop));
    },
  };
  return installed;
}
