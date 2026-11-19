// @ts-nocheck
import { executionSession } from './task-context.js';
import { fail, same } from './protocol.js';

/** Trusted native lifetime owner. No JSON/session-name based parent lookup. */
export function createTurnAdmission(
  ctx,
  { lookup, begin, refresh, fence, fenceForeground = () => {}, finish },
) {
  const turns = new Map(),
    all = new Set();
  const leases = new WeakMap(),
    executions = new WeakMap(),
    owners = new WeakMap();
  const requestLocks = new WeakMap();
  let closed = false,
    closing;
  const live = (row, retained = false) => {
    if (
      closed ||
      (row.state !== 'active' && !(row.state === 'draining' && retained)) ||
      row.controller.signal.aborted ||
      row.signal?.aborted ||
      executionSession(ctx, row.execution) !== row.binding.sessionId
    )
      fail();
  };
  const notify = (row) => {
    if (!row.owner && row.count === 0) row.drained?.();
  };
  const stop = (row, endOwner = false) => {
    if (endOwner) row.owner = false;
    if (!row.closing) {
      row.state = 'closing';
      row.controller.abort();
      fence(row.execution);
      row.closing = (async () => {
        try {
          await row.ready;
        } catch {
          /* Begin compensation is still fenced by Finish. */
        }
        if (row.owner || row.count)
          await new Promise((resolve) => {
            row.drained = resolve;
            notify(row);
          });
        await finish(row.binding, row.execution);
        row.state = 'closed';
        row.signal?.removeEventListener('abort', row.abort);
        all.delete(row);
        if (turns.get(row.agent) === row) turns.delete(row.agent);
      })();
      // Native notifications are synchronous and contained. Retain the actual
      // rejected Promise as a barrier; observing it here does not clear it.
      row.closing.then(row.doneResolve, row.doneReject);
      void row.closing.catch(() => {});
    }
    notify(row);
    return row.closing;
  };
  const complete = (row) => {
    row.owner = false;
    if (!row.closing && row.retains > 0) {
      row.state = 'draining';
      fenceForeground(row.execution);
      notify(row);
      return row.done;
    }
    return stop(row, true);
  };
  const make = (binding, agent, signal, turn, owner) => {
    if (
      closed ||
      signal?.aborted ||
      executionSession(ctx, { agent }) !== binding.sessionId
    )
      fail();
    const controller = new AbortController();
    const row = {
      binding,
      agent,
      signal,
      turn,
      owner,
      count: 0,
      retains: 0,
      controller,
      state: 'active',
    };
    row.done = new Promise((resolve, reject) => {
      row.doneResolve = resolve;
      row.doneReject = reject;
    });
    void row.done.catch(() => {});
    row.execution = {
      agent,
      signal: globalThis.AbortSignal.any(
        [signal, controller.signal].filter(Boolean),
      ),
    };
    owners.set(row.execution, row);
    all.add(row);
    row.abort = () => {
      void stop(row).catch(() => {});
    };
    signal?.addEventListener('abort', row.abort, { once: true });
    row.ready = Promise.resolve()
      .then(() => begin(binding, row.execution))
      .then((token) => {
        row.token = token;
        live(row);
        return row;
      });
    void row.ready.catch(() => {
      void stop(row, true).catch(() => {});
    });
    return row;
  };
  const fresh = async (row, retained = false) => {
    await row.ready;
    live(row, retained);
    row.refreshing ??= Promise.resolve()
      .then(async () => {
        live(row, row.retains > 0);
        const token = await refresh(row.token, row.binding.sessionId);
        live(row, row.retains > 0);
        row.token = token;
        return token;
      })
      .finally(() => {
        row.refreshing = undefined;
      });
    const token = await row.refreshing;
    live(row, retained);
    return token;
  };
  const lease = (row, execution, own = false) => {
    live(row);
    row.count++;
    let ended = false,
      retains = 0;
    const value = Object.freeze({
      binding: row.binding,
      signal: row.execution.signal,
      get ordinaryToken() {
        return (ended && retains === 0) ||
          (row.state !== 'active' && !(row.state === 'draining' && retains > 0))
          ? ''
          : row.token;
      },
      refresh: () => {
        if (ended && retains === 0) fail();
        return fresh(row, retains > 0);
      },
      check() {
        if (ended && retains === 0) fail();
        live(row, retains > 0);
      },
      retain() {
        if (ended) fail();
        live(row, retains > 0);
        retains++;
        row.retains++;
        row.count++;
        let settled = false;
        return async () => {
          if (settled) return;
          settled = true;
          retains--;
          row.retains--;
          row.count--;
          if (row.state === 'draining' && row.retains === 0)
            void stop(row, true).catch(() => {});
          notify(row);
        };
      },
      close() {
        if (ended) return own ? row.closing : Promise.resolve();
        ended = true;
        row.count--;
        if (execution && executions.get(execution) === value)
          executions.delete(execution);
        if (own) return complete(row);
        notify(row);
        return row.owner ? Promise.resolve() : row.done;
      },
    });
    leases.set(value, {
      row,
      active: () => !ended || retains > 0,
      retained: () => retains > 0,
    });
    if (execution) executions.set(execution, value);
    return value;
  };
  const principal = (agent) => {
    if (ctx.agents.currentInitiator() !== agent) fail();
    return agent;
  };
  const api = {
    request({ agent, signal, turn }) {
      const next = (requestLocks.get(agent) ?? Promise.resolve()).then(
        async () => {
          principal(agent);
          if (!Number.isSafeInteger(turn) || turn < 1 || !signal) fail();
          const prior = turns.get(agent);
          if (prior && prior.turn === turn) {
            if (prior.signal !== signal) fail();
            await fresh(prior);
            return prior;
          }
          if (prior) await prior.done;
          const binding = await lookup(executionSession(ctx, { agent }));
          while ([...all].some((row) => row.agent === agent))
            await Promise.all(
              [...all]
                .filter((row) => row.agent === agent)
                .map((row) => row.done),
            );
          const row = make(binding, agent, signal, turn, true);
          turns.set(agent, row);
          await row.ready;
          return row;
        },
      );
      requestLocks.set(
        agent,
        next.then(
          () => undefined,
          () => undefined,
        ),
      );
      return next;
    },
    async acquire(binding, execution) {
      if (typeof execution?.token !== 'symbol') fail();
      const prior = executions.get(execution);
      if (prior) {
        prior.check();
        same(prior.binding, binding);
        return lease(leases.get(prior).row);
      }
      const row = turns.get(execution.agent);
      if (row) {
        principal(execution.agent);
        if (execution.signal !== row.signal) fail();
        await row.ready;
        live(row);
        same(row.binding, binding);
        return lease(row, execution);
      }
      while ([...all].some((item) => item.agent === execution.agent))
        await Promise.all(
          [...all]
            .filter((item) => item.agent === execution.agent)
            .map((item) => item.done),
        );
      const own = make(binding, execution.agent, execution.signal, null, false);
      await own.ready;
      return lease(own, execution, true);
    },
    async operation(binding, agent, signal) {
      // Explicit trusted background operation: never borrow another turn.
      while ([...all].some((row) => row.agent === agent))
        await Promise.all(
          [...all].filter((row) => row.agent === agent).map((row) => row.done),
        );
      const row = make(binding, agent, signal, null, false);
      await row.ready;
      return lease(row, undefined, true);
    },
    async requestLease(agent, signal) {
      principal(agent);
      const row = turns.get(agent);
      if (!row || row.signal !== signal) fail();
      await row.ready;
      return lease(row);
    },
    executionLease(execution) {
      const value = executions.get(execution);
      if (!value) fail();
      value.check();
      return lease(leases.get(value).row);
    },
    async parent(value, binding) {
      const proof = leases.get(value);
      if (!proof || !proof.active()) fail();
      same(proof.row.binding, binding);
      const token = await fresh(proof.row, proof.retained());
      if (!proof.active()) fail();
      live(proof.row, proof.retained());
      return { token, execution: proof.row.execution };
    },
    retained(value) {
      const proof = leases.get(value);
      return !!proof && proof.active() && proof.retained();
    },
    active(value) {
      return leases.get(value)?.active() === true;
    },
    async refreshParent(execution, values) {
      const row = owners.get(execution);
      if (!row) fail();
      const eligible = () =>
        [...values]
          .map((value) => leases.get(value))
          .find(
            (proof) =>
              proof?.row === row &&
              proof.active() &&
              (row.state === 'active' || proof.retained()),
          );
      let proof = eligible();
      if (!proof) fail();
      const token = await fresh(row, proof.retained());
      proof = eligible();
      if (!proof) fail();
      live(row, proof.retained());
      return token;
    },
    invalidate(execution) {
      const row = owners.get(execution);
      if (row) void stop(row).catch(() => {});
    },
    end(session, event) {
      if (event.type !== 'turn/end') return;
      for (const row of turns.values())
        if (row.agent.session === session && row.turn === event.data.turn)
          void complete(row).catch(() => {});
    },
    idle(agent) {
      const row = turns.get(agent);
      if (row) void complete(row).catch(() => {});
    },
    retire(agent) {
      for (const row of all)
        if (row.agent === agent) void stop(row, true).catch(() => {});
    },
    revoke(event) {
      for (const row of all)
        if (
          row.binding.placementId === event.placementId &&
          row.binding.epoch <= event.epoch &&
          row.binding.revision <= event.revision &&
          row.binding.authGeneration <= event.authGeneration
        )
          void stop(row, row.turn === null).catch(() => {});
    },
    close() {
      if (closing) return closing;
      closed = true;
      closing = Promise.allSettled(
        [...all].map((row) => stop(row, row.turn === null)),
      ).then((results) => {
        const error = results.find((result) => result.status === 'rejected');
        if (error) throw error.reason;
      });
      return closing;
    },
  };
  return api;
}
