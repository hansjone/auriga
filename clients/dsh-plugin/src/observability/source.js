import { AsyncLocalStorage } from 'node:async_hooks';
import { logEvent, newTraceparent, publicationContext } from './index.js';

/** Observational context only: no identity, grant, payload or ledger state.
 * @typedef {{trace_id:string,attempt_id?:string,stage:string,reason?:string,authorization_round?:string,rpc_count:number,rpc?:ReturnType<typeof publicationContext>}} Context */
/** @type {AsyncLocalStorage<Context>} */ const contexts =
  new AsyncLocalStorage();
/** @type {WeakMap<Promise<unknown>, Context>} */ const tasks = new WeakMap();
/** @returns {Context} */
function fresh() {
  return {
    trace_id: newTraceparent().slice(3, 35),
    stage: 'start',
    rpc_count: 0,
  };
}
/** @param {string} event @param {Record<string,unknown>} [fields] @param {unknown} [error] */
export function sourceEvent(event, fields = {}, error) {
  try {
    const c = contexts.getStore() ?? fresh();
    if (error && c.rpc) {
      logEvent(
        'ERROR',
        'Source RPC failed',
        { ...c.rpc, source_work_trace_id: c.trace_id, stage: c.stage },
        error,
      );
      c.rpc = undefined;
    }
    logEvent(
      error ? 'ERROR' : c.reason ? 'WARN' : 'INFO',
      'Source ' + event,
      {
        trace_id: c.trace_id,
        attempt_id: c.attempt_id,
        stage: c.stage,
        reason: c.reason,
        authorization_round: c.authorization_round,
        direct_rpc_count: c.rpc_count,
        ...fields,
      },
      error,
    );
  } catch {
    /* Observation cannot change a business result. */
  }
}
/** @param {string} stage */
export function sourceStage(stage) {
  const c = contexts.getStore();
  const previous = c?.stage ?? 'unknown';
  if (c) {
    c.stage = stage;
    c.reason = undefined;
  }
  return previous;
}
/** Short-circuit annotation at the original guard; always returns true.
 * @param {string} reason @returns {true} */
export function sourceReject(reason) {
  const c = contexts.getStore();
  if (c) c.reason = reason;
  sourceEvent('guard rejected', { reason });
  return true;
}
/** @param {string} pass */
export function sourceAuthorization(pass) {
  const c = contexts.getStore();
  if (c) c.authorization_round = pass;
  sourceStage('authorization');
  sourceEvent('authorization started');
}
/** A direct prepare can reuse its enclosing tool execution, never another execution.
 * @template T @param {()=>T} action @param {boolean} [reuse] @returns {T} */
export function observeTool(action, reuse = false) {
  if (reuse && contexts.getStore()?.attempt_id) return action();
  let c;
  try {
    c = fresh();
    c.attempt_id = c.trace_id;
  } catch {
    return action();
  }
  return contexts.run(c, () => {
    sourceEvent('tool attempt started');
    return action();
  });
}
/** Each queued control action is independent, even when pump runs inside a tool.
 * @param {()=>Promise<unknown>} action */
export function observeSourceWork(action) {
  let c;
  try {
    c = fresh();
  } catch {
    return action();
  }
  const task = contexts.run(c, () => {
    sourceEvent('control started');
    return action();
  });
  tasks.set(task, c);
  return task;
}
/** Links only promises actually awaited by idle(), including pre-existing work.
 * @param {Promise<unknown>[]} pending */
export function sourceWaiting(pending) {
  if (!contexts.getStore()?.attempt_id) return;
  for (const task of pending) {
    const c = tasks.get(task);
    if (c)
      sourceEvent('control task awaited', {
        relation: 'waited_for',
        source_work_trace_id: c.trace_id,
      });
  }
}
/** Report the original finalizer rejection in that exact work's context.
 * @param {Promise<unknown>} task @param {unknown} error */
export function sourceTaskFailed(task, error) {
  const c = tasks.get(task);
  if (c)
    contexts.run(c, () =>
      sourceEvent(
        'control cleanup failed',
        { stage: 'control_cleanup' },
        error,
      ),
    );
  else
    sourceEvent('control cleanup failed', { stage: 'control_cleanup' }, error);
}
/** The exact existing frame supplies the RPC trace, without changing its bytes.
 * @param {{request_id?:unknown,type?:unknown}} frame @param {string} event @param {unknown} [error] */
export function sourceRpc(frame, event, error) {
  try {
    const c = contexts.getStore();
    const fields = publicationContext(
      Buffer.from(
        JSON.stringify({
          request_id: frame.request_id,
          type: frame.type,
        }),
      ),
    );
    if (!fields) return;
    if (c) {
      if (event === 'started') {
        c.rpc_count++;
        c.rpc = fields;
      } else c.rpc = undefined;
    }
    logEvent(
      error ? 'ERROR' : 'INFO',
      'Source RPC ' + event,
      {
        ...fields,
        source_work_trace_id: c?.trace_id,
        stage: 'rpc',
        outcome: event === 'started' ? 'awaiting_response' : event,
      },
      error,
    );
  } catch {
    /* The frame and RPC are owned by the original control path. */
  }
}
