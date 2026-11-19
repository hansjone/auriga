// @ts-nocheck
import { createFileLedgerStore } from '../network/host/store.js';
import { openSharedMessageReceiver } from './message-receiver.js';
import { canonical, fail, validate } from './protocol.js';
import { formatAddress } from '../network/host/address.js';

/** One serialized pool pump. Only a persisted, explicitly enabled K session
 * receives; no model tool and no owner account grants receiving permission. */
export function createSharedMessageManager(
  ctx,
  { runtime, ports, preferenceStore, inboxStore, intervalMs = 1000 },
) {
  const root = `/var/lib/auriga/pools/${ports.options.physicalInstanceId}/host`;
  const store =
    preferenceStore ??
    createFileLedgerStore({ dir: root + '/message-preferences' });
  const entries = new Map();
  const floors = new Map();
  let closed = false,
    failed = false,
    handle,
    pending,
    state,
    timer,
    pumping;
  let writes = Promise.resolve();
  const fenced = (b) => {
    const floor = floors.get(b.placementId);
    return (
      !!floor &&
      (b.epoch < floor.epoch ||
        b.revision < floor.revision ||
        b.authGeneration < floor.authGeneration)
    );
  };
  const open = () =>
    (pending ??= (async () => {
      handle = await store.open();
      const saved = await handle.load();
      state = saved ?? { version: 1, entries: {} };
      if (
        state.version !== 1 ||
        !state.entries ||
        typeof state.entries !== 'object' ||
        Array.isArray(state.entries) ||
        Object.keys(state).sort().join(',') !== 'entries,version'
      )
        fail('common.unavailable');
      for (const [id, entry] of Object.entries(state.entries)) {
        validate('SessionBinding', entry.binding);
        if (
          Object.keys(entry).sort().join(',') !== 'binding,enabled' ||
          id !== entry.binding.sessionId ||
          entry.binding.tenantId !== ports.options.tenantId ||
          entry.binding.physicalInstanceId !==
            ports.options.physicalInstanceId ||
          typeof entry.enabled !== 'boolean'
        )
          fail('common.unavailable');
      }
      if (saved === undefined) await handle.commit(state);
      if (closed) fail('common.unavailable');
    })().catch(async (error) => {
      failed = true;
      await handle?.close().catch(() => {});
      handle = undefined;
      throw error;
    }));
  const change = (binding, enabled) => {
    const work = writes.then(async () => {
      await open();
      if (closed || failed) fail('common.unavailable');
      const draft = JSON.parse(JSON.stringify(state));
      draft.entries[binding.sessionId] = {
        binding: JSON.parse(canonical(binding)),
        enabled,
      };
      try {
        await handle.commit(draft);
        state = draft;
      } catch (error) {
        failed = true;
        throw error;
      }
    });
    writes = work.catch(() => {});
    return work;
  };
  const desired = (binding) => {
    const found = state?.entries[binding.sessionId];
    return found && canonical(found.binding) === canonical(binding)
      ? found.enabled
      : false;
  };
  const serial = (entry, operation) => {
    const work = entry.tail.then(operation);
    entry.tail = work.catch(() => {});
    return work;
  };
  const assert = (entry) => {
    if (
      closed ||
      failed ||
      entry.controller.signal.aborted ||
      entry.paused ||
      fenced(entry.binding)
    )
      fail();
  };
  const ensure = async (binding) => {
    await open();
    validate('SessionBinding', binding);
    if (closed || failed || fenced(binding)) fail();
    const current = await runtime.session(binding.sessionId);
    if (canonical(current) !== canonical(binding))
      fail('shared.binding_mismatch');
    const agent = ctx.agents.get(binding.sessionId);
    if (!agent || ctx.sessions.get(binding.sessionId) !== agent.session)
      fail('common.unavailable');
    let entry = entries.get(binding.sessionId);
    if (
      entry &&
      (canonical(entry.binding) !== canonical(binding) || entry.agent !== agent)
    ) {
      entry.controller.abort();
      await entry.tail;
      await entry.receiver?.close();
      entries.delete(binding.sessionId);
      entry = undefined;
    }
    if (!entry) {
      entry = {
        binding: JSON.parse(canonical(binding)),
        agent,
        controller: new AbortController(),
        tail: Promise.resolve(),
        paused: false,
        last: null,
        error: null,
      };
      entries.set(binding.sessionId, entry);
    }
    if (!entry.receiver) {
      entry.opening ??= (async () => {
        const scope = await ports.openMessageInbox(
          binding,
          entry.controller.signal,
        );
        try {
          const receiver = await openSharedMessageReceiver(ctx, {
            scope,
            store: inboxStore?.(scope.context),
            assertCurrent: () => assert(entry),
          });
          assert(entry);
          entry.receiver = receiver;
          entry.last = receiver.snapshot();
        } catch (error) {
          await scope.close().catch(() => {});
          throw error;
        }
      })();
      try {
        await entry.opening;
      } catch (error) {
        entry.opening = undefined;
        entry.error = error.code ?? 'common.unavailable';
        throw error;
      }
    }
    return entry;
  };
  const view = (binding, entry) => {
    const want = desired(binding);
    let observed = entry?.last;
    try {
      observed = entry?.receiver?.snapshot() ?? observed;
    } catch {
      /* Last confirmed state is not a fresh grant. */
    }
    return {
      sessionId: binding.sessionId,
      address: entry?.receiver
        ? formatAddress({
            realm: entry.receiver.context.connection.realm,
            node: entry.receiver.context.connection.node,
            local: entry.receiver.context.registration.local,
          })
        : null,
      receiveEnabled: !entry?.paused && observed?.enabled === true,
      desiredReceiveEnabled: want,
      pending:
        !!observed?.pending ||
        want !== (observed?.enabled === true) ||
        !!entry?.paused,
      available: !!entry?.receiver && !entry.error,
      lastError: entry?.error ?? null,
    };
  };
  const reconcile = async (entry) => {
    const want = desired(entry.binding);
    // Normal enabled polling must not rewrite receive_mode or increase the
    // platform permission version. Only an explicit disable fences intake.
    if (!want) await entry.receiver.fence();
    entry.paused = false;
    entry.last = await entry.receiver.reconcile(want, entry.controller.signal);
    entry.error = null;
    return view(entry.binding, entry);
  };
  const pump = () => {
    if (closed || failed) return Promise.resolve();
    if (pumping) return pumping;
    pumping = (async () => {
      await open();
      const candidates = [
        ...new Set([...Object.keys(state.entries), ...entries.keys()]),
      ];
      for (const id of candidates) {
        if (closed) break;
        const preference = state.entries[id],
          existing = entries.get(id);
        if (
          !preference?.enabled &&
          !existing?.last?.enabled &&
          !existing?.last?.pending &&
          !existing?.paused
        )
          continue;
        const binding = preference?.binding ?? existing.binding;
        if (!ctx.agents.get(id) || fenced(binding)) continue;
        try {
          const entry = await ensure(binding);
          await serial(entry, async () => {
            await reconcile(entry);
            if (desired(binding)) await entry.receiver.pull();
            entry.last = entry.receiver.snapshot();
            if (entry.receiver.failures().length)
              entry.error = 'common.unavailable';
          });
        } catch (error) {
          const entry = entries.get(id);
          if (entry)
            entry.error = [
              'shared.revoked',
              'shared.binding_mismatch',
              'common.unavailable',
              'shared.signature_invalid',
              'shared.expired',
            ].includes(error.code)
              ? error.code
              : 'common.unavailable';
          ctx.logger?.warn?.('Auriga shared message receiving is unavailable', {
            sessionId: id,
            code: entry?.error ?? 'common.unavailable',
          });
          if (entry && (!entry.paused || entry.controller.signal.aborted)) {
            // A finite K task may end while explicit session intent remains.
            // Close it and reopen through fresh W/K authorization next time;
            // the stable native context and durable inbox are not reset.
            entry.controller.abort();
            await serial(entry, async () => {
              try {
                await entry.receiver?.close();
              } finally {
                if (entries.get(id) === entry) entries.delete(id);
              }
            }).catch(() => {});
          }
        }
      }
    })().finally(() => {
      pumping = undefined;
    });
    return pumping;
  };
  return {
    async start() {
      await open();
      if (closed || timer) return;
      timer = setInterval(() => {
        void pump().catch(() => {});
      }, intervalMs);
      timer.unref?.();
    },
    async status(binding) {
      const entry = await ensure(binding);
      return serial(entry, async () => {
        entry.last = await entry.receiver.observe();
        return view(binding, entry);
      });
    },
    async setReceive(binding, enabled) {
      if (typeof enabled !== 'boolean') fail('common.validation_failed');
      // Synchronously fence an already running recipient before any storage/RPC wait.
      const previous = entries.get(binding.sessionId);
      if (!enabled && previous?.receiver) previous.paused = true;
      await change(binding, enabled);
      if (!enabled && previous?.receiver) await previous.receiver.fence();
      const entry = await ensure(binding);
      return serial(entry, () => reconcile(entry));
    },
    pump,
    revoke(event) {
      const old = floors.get(event.placementId);
      floors.set(event.placementId, {
        epoch: Math.max(old?.epoch ?? 0, event.epoch),
        revision: Math.max(old?.revision ?? 0, event.revision),
        authGeneration: Math.max(
          old?.authGeneration ?? 0,
          event.authGeneration,
        ),
      });
      for (const entry of entries.values())
        if (fenced(entry.binding)) {
          entry.paused = true;
          entry.controller.abort();
        }
    },
    retire(agent) {
      const entry = entries.get(agent.session.id);
      if (entry?.agent !== agent) return;
      entry.paused = true;
      entry.controller.abort();
      // Keep the persisted intent, but never resume a cold archived session.
      void serial(entry, async () => {
        await entry.receiver?.close();
        if (entries.get(agent.session.id) === entry)
          entries.delete(agent.session.id);
      }).catch(() => {});
    },
    async close() {
      if (closed) return;
      closed = true;
      clearInterval(timer);
      for (const entry of entries.values()) entry.controller.abort();
      await pumping?.catch(() => {});
      await writes;
      await pending?.catch(() => {});
      for (const entry of entries.values()) {
        await entry.tail;
        await entry.opening?.catch(() => {});
        await entry.receiver?.close().catch(() => {});
      }
      await handle?.close();
      entries.clear();
    },
  };
}
