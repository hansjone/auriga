// @ts-nocheck
import { AsyncLocalStorage } from 'node:async_hooks';
import { fail } from '../shared/protocol.js';
import { executionSession } from '../shared/task-context.js';

const scope = new AsyncLocalStorage();
const terminal = (status) => ['completed', 'killed', 'failed'].includes(status);
const sameJob = (left, right) =>
  left?.id === right?.id &&
  left?.owner === right?.owner &&
  left?.kind === right?.kind &&
  left?.startedAt === right?.startedAt;

/** Cross-witness real registry registrations with the native Session log and
 * this exact asynchronous native execution. A result JSON is never authority.
 * rc.3 job views deliberately omit toolCall; only job/registered carries it. */
export function createDedicatedExecutionJobs(
  ctx,
  { execution, task, check, stop },
) {
  const owner = Object.freeze({ execution });
  const sessionId = executionSession(ctx, execution),
    registry = ctx.jobs;
  const proofs = new Map(),
    jobs = new Map(),
    waiters = new Set();
  let accepting = false,
    settle,
    settling,
    closed = false;
  const available =
    !!registry?.events?.subscribe &&
    ['get', 'list', 'kill'].every(
      (name) => typeof registry[name] === 'function',
    );
  const current = (id) => {
    const value = registry.get(id, sessionId);
    const listed = registry.list(sessionId).filter((entry) => entry.id === id);
    if (listed.length !== 1 || !sameJob(value, listed[0]))
      fail('shared.revoked');
    return value;
  };
  const notify = () => {
    if ([...jobs.values()].every((entry) => entry.ended)) {
      for (const done of waiters) done();
      waiters.clear();
    }
  };
  const markEnded = (entry) => {
    entry.ended = true;
    notify();
  };
  const revoke = () => {
    try {
      stop();
    } catch {
      /* stop retains its drain */
    }
  };
  const detachSession = ctx.on('session/event', (session, event) => {
    if (
      closed ||
      !accepting ||
      scope.getStore() !== owner ||
      event.type !== 'job/registered'
    )
      return;
    if (
      session !== execution.agent.session ||
      ctx.sessions.get(sessionId) !== session
    )
      return;
    const data = event.data;
    if (
      typeof data?.jobId !== 'string' ||
      data.owner !== sessionId ||
      !Object.isFrozen(event) ||
      !Object.isFrozen(data) ||
      typeof data.toolCall?.callId !== 'string' ||
      data.toolCall.callId !== execution.callId ||
      data.toolCall.rootCallId !== execution.rootCallId ||
      proofs.has(data.jobId) ||
      proofs.size >= 32
    )
      return;
    proofs.set(data.jobId, {
      event,
      kind: data.kind,
      startedAt: data.startedAt,
    });
  });
  const detachJobs = available
    ? registry.events.subscribe({ owner: sessionId }, (event) => {
        if (closed) return;
        if (event.type === 'registered') {
          if (scope.getStore() !== owner) return;
          // Track even an invalid registration from this execution until the real
          // process settles. Rejecting authority cannot release its live budget.
          const entry = {
            view: Object.freeze({
              id: event.job.id,
              owner: event.job.owner,
              kind: event.job.kind,
              startedAt: event.job.startedAt,
            }),
            ended: false,
            trusted: false,
          };
          jobs.set(event.job.id, entry);
          try {
            const actual = current(event.job.id),
              proof = proofs.get(actual.id);
            if (
              !sameJob(actual, event.job) ||
              !accepting ||
              !proof ||
              actual.owner !== sessionId ||
              proof.kind !== actual.kind ||
              proof.startedAt !== actual.startedAt ||
              actual.status !== 'running'
            )
              fail('shared.revoked');
            check();
            if (!settle) {
              if (typeof task.retain !== 'function') fail('common.unavailable');
              settle = task.retain();
              if (typeof settle !== 'function') fail('common.unavailable');
            }
            entry.trusted = true;
          } catch {
            revoke();
          }
          return;
        }
        const entry = jobs.get(event.job?.id);
        if (!entry || !sameJob(entry.view, event.job)) return;
        if (event.type === 'stopping') {
          revoke();
          return;
        }
        if (
          !['settled', 'removed'].includes(event.type) ||
          !terminal(event.job.status)
        )
          return;
        try {
          if (event.type === 'settled') {
            const actual = current(event.job.id);
            if (!sameJob(actual, event.job) || !terminal(actual.status))
              fail('shared.revoked');
          } else if (
            registry.list(sessionId).some((job) => job.id === event.job.id)
          )
            fail('shared.revoked');
          markEnded(entry);
        } catch {
          revoke();
        }
      })
    : undefined;
  return {
    async run(next) {
      accepting = true;
      try {
        return await scope.run(owner, next);
      } finally {
        accepting = false;
      }
    },
    handoff(result) {
      if (
        result?.isError ||
        !['background', 'promoted'].includes(result?.value?.kind)
      )
        return false;
      const id = result.value.jobId,
        entry = jobs.get(id);
      if (!available || typeof id !== 'string' || !entry?.trusted || !settle)
        fail('shared.revoked');
      const actual = current(id);
      if (!sameJob(entry.view, actual) || actual.owner !== sessionId)
        fail('shared.revoked');
      if (terminal(actual.status)) {
        markEnded(entry);
        return false;
      }
      if (actual.status !== 'running' || entry.ended) fail('shared.revoked');
      check();
      return true;
    },
    kill() {
      for (const entry of jobs.values()) {
        if (entry.ended) continue;
        try {
          const actual = current(entry.view.id);
          if (!sameJob(actual, entry.view)) continue;
          if (terminal(actual.status)) {
            markEnded(entry);
            continue;
          }
          if (actual.status === 'running')
            registry.kill(actual.id, sessionId, 'Auriga task authority ended');
        } catch {
          /* No inferred settlement after an unknown registry failure. */
        }
      }
    },
    async settle() {
      settling ??= (async () => {
        if ([...jobs.values()].some((entry) => !entry.ended))
          await new Promise((resolve) => waiters.add(resolve));
        await settle?.();
      })();
      return settling;
    },
    close() {
      closed = true;
      detachJobs?.();
      detachSession();
      proofs.clear();
    },
  };
}
