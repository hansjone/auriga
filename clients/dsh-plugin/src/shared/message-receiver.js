// @ts-nocheck
import { randomUUID } from 'node:crypto';
import { createNativeHostPort } from '../network/host/native/host-port.js';
import { createDurableInbox } from '../network/durable/inbox.js';
import { openSharedInboxLedger } from './message-inbox-ledger.js';
import { fail } from './protocol.js';

/** Compose the existing durable inbox with current K authorization and the
 * public native HostPort. Pulling never manufactures a prompt/receipt result. */
export async function openSharedMessageReceiver(
  ctx,
  { scope, store, assertCurrent = () => {} },
) {
  const run = scope.run
    ? (operation) => scope.run(operation)
    : (operation) => operation();
  return run(async () => {
    const context = scope.context;
    const current = () => {
      assertCurrent();
      scope.assertCurrent();
    };
    const ledger = await openSharedInboxLedger({
      context,
      store,
      assertCurrent: current,
    });
    try {
      const host = await createNativeHostPort({ ctx });
      const capabilities = host.getDiagnostics().capabilities;
      if (
        !['source', 'durability', 'requests', 'prompt'].every(
          (key) => capabilities[key]?.available,
        )
      )
        fail('common.unavailable');
      const ready = {
        epoch: context.connection.epoch,
        bootId: context.bootId,
        channelNonce: context.connection.channelNonce,
      };
      const client = {
        frame: (type, fields) => ({
          app_version: '1.0',
          type,
          request_id: randomUUID(),
          epoch: ready.epoch,
          ...fields,
        }),
        request: (frame, expected, signal) =>
          scope.request(frame, expected, signal),
      };
      const inbox = createDurableInbox({
        ctx,
        ledger,
        host,
        client,
        ready,
        trust: scope.trust,
        assertCurrent: current,
      });
      const local = context.registration.local;
      let last = inbox.snapshot()[0] ?? {
        local,
        sessionId: context.binding.sessionId,
        enabled: false,
        pending: false,
        pendingEnabled: null,
      };
      const operation = (body) =>
        run(async () => {
          const result = await body();
          last = inbox.snapshot()[0] ?? last;
          return result;
        });
      return {
        context,
        ledger,
        snapshot: () => {
          assertCurrent();
          return scope.run ? { ...last } : (inbox.snapshot()[0] ?? { ...last });
        },
        failures: () => inbox.failures(),
        async observe() {
          return operation(async () => {
            await scope.refresh();
            current();
            return inbox.snapshot()[0];
          });
        },
        async reconcile(enabled, signal) {
          return operation(async () => {
            await scope.refresh();
            current();
            await inbox.recover();
            current();
            const observed = inbox.snapshot()[0];
            const confirmed = ledger.peek().durable.receive[local];
            if (
              (enabled
                ? observed?.enabled !== true
                : confirmed?.enabled === true) ||
              confirmed?.pending
            )
              await inbox.setReceive(local, enabled, signal);
            current();
            return inbox.snapshot()[0];
          });
        },
        async pull() {
          return operation(async () => {
            await scope.refresh();
            current();
            await inbox.recover();
            current();
            await inbox.pull();
            current();
          });
        },
        async fence() {
          await ledger.fenceReceive();
        },
        async close() {
          try {
            await ledger.close();
          } finally {
            await scope.close();
          }
        },
      };
    } catch (error) {
      await ledger.close();
      if (!scope.run) await scope.close().catch(() => {});
      throw error;
    }
  });
}
