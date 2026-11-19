// @ts-nocheck
import { executionSession, openSharedExecution } from './task-context.js';
import { fail } from './protocol.js';

const variables = Object.freeze({
  DSH_AURIGA_ID: { description: 'Current employee ID for this native task.' },
  DSH_AURIGA_TOKEN: {
    description: 'Current company token; never print or persist.',
  },
  DSH_TEAM_TASK_CAPABILITY: {
    description: 'Current task capability for the loopback helper.',
  },
  DSH_TEAM_BASE: { description: 'Owned loopback enterprise endpoint.' },
  DSH_TEAM_HELPERS: { description: 'Packaged Python helper directory.' },
  DSH_WEB_URL: { description: 'Local helper origin.' },
});
const empty = () =>
  Object.fromEntries(Object.keys(variables).map((key) => [key, '']));

/** Native ToolRuntime opens authority asynchronously before the public
 * ShellEnvRegistry synchronously collects it. No browser ID or process.env. */
export function installSharedExecutionEnvironment(ctx, { runtime, ports }) {
  if (
    typeof ctx.shellEnv?.register !== 'function' ||
    typeof ctx.on !== 'function'
  )
    fail('common.unavailable');
  const rows = new Map();
  const revocations = new Map();
  let serial = 0;
  let closed = false,
    closing;
  const key = (exec) => exec.token ?? exec;
  const check = (row, exec) => {
    if (
      closed ||
      !row.active ||
      row.execution !== exec ||
      exec.signal?.aborted ||
      row.controller.signal.aborted ||
      executionSession(ctx, exec) !== row.sessionId
    )
      fail();
    const event = revocations.get(row.binding?.placementId);
    if (
      event &&
      event.serial > row.serial &&
      row.binding.epoch <= event.epoch &&
      row.binding.revision <= event.revision &&
      row.binding.authGeneration <= event.authGeneration
    )
      fail();
  };
  const closeRow = (row, settled = false) => {
    if (settled) {
      row.running = false;
      row.settle?.();
    }
    if (row.closing) return row.closing;
    row.active = false;
    row.controller.abort();
    if (rows.get(key(row.execution)) === row) rows.delete(key(row.execution));
    row.signal?.removeEventListener('abort', row.abort);
    row.closing = (async () => {
      try {
        await row.ready;
      } catch {
        /* Failed opening still owns its own compensation. */
      } finally {
        if (row.running)
          await new Promise((resolve) => {
            row.settle = resolve;
          });
        await row.task?.close();
      }
    })();
    return row.closing;
  };
  const borrow = (exec) => {
    const row = rows.get(key(exec));
    if (!row) return;
    check(row, exec);
    return row.task;
  };
  const detachEnv = ctx.shellEnv.register({
    name: 'auriga-shared-execution',
    variables,
    resolve(exec) {
      const row = rows.get(key(exec));
      if (!row) return empty();
      check(row, exec);
      const env = row.task?.env;
      return Object.fromEntries(
        Object.keys(variables).map((name) => [name, env?.[name] ?? '']),
      );
    },
  });
  const detachHook = ctx.on('tools/execute', async (exec, next) => {
    if (closed) fail();
    const prior = rows.get(key(exec));
    if (prior) {
      check(prior, exec);
      return next();
    }
    const sessionId = executionSession(ctx, exec),
      controller = new AbortController();
    const row = {
      execution: exec,
      sessionId,
      controller,
      active: true,
      serial,
      signal: exec.signal,
      running: true,
    };
    row.abort = () => {
      void closeRow(row).catch(() => {});
    };
    rows.set(key(exec), row);
    row.ready = Promise.resolve()
      .then(() =>
        openSharedExecution({
          ctx,
          runtime,
          ports,
          raw: true,
          execution: exec,
        }),
      )
      .then((task) => {
        row.task = task;
        row.binding = task.binding;
        check(row, exec);
        return task;
      });
    exec.signal?.addEventListener('abort', row.abort, { once: true });
    try {
      await row.ready;
      check(row, exec);
      exec.signal = globalThis.AbortSignal.any(
        [row.signal, controller.signal, row.task.signal].filter(Boolean),
      );
      const result = await next();
      check(row, exec);
      await row.task.refresh();
      check(row, exec);
      return result;
    } finally {
      exec.signal = row.signal;
      await closeRow(row, true);
    }
  });
  const detachDisposed = ctx.on('agent/disposed', ({ agent }) => {
    for (const row of rows.values())
      if (row.execution.agent === agent) void closeRow(row).catch(() => {});
  });
  return {
    borrow,
    async open(execution) {
      const task = borrow(execution);
      if (!task) fail();
      // A caller inside the same native dispatch borrows its handle. Only the
      // outer hook owns Finish, preventing duplicate admission/early closure.
      return Object.freeze({
        binding: task.binding,
        company: task.company,
        get token() {
          return task.token;
        },
        get env() {
          return task.env;
        },
        refresh: () => task.refresh(),
        close: async () => {},
      });
    },
    revoke(event) {
      revocations.set(event.placementId, { ...event, serial: ++serial });
      for (const row of rows.values())
        if (
          row.binding?.placementId === event.placementId &&
          row.binding.epoch <= event.epoch &&
          row.binding.revision <= event.revision &&
          row.binding.authGeneration <= event.authGeneration
        )
          void closeRow(row).catch(() => {});
    },
    close() {
      if (closing) return closing;
      closed = true;
      detachHook();
      detachDisposed();
      detachEnv();
      closing = Promise.allSettled([...rows.values()].map(closeRow)).then(
        (results) => {
          const failure = results.find((entry) => entry.status === 'rejected');
          if (failure) throw failure.reason;
        },
      );
      return closing;
    },
  };
}
