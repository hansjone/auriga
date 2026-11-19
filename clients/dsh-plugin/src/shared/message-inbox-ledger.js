// @ts-nocheck
import { createFileLedgerStore } from '../network/host/store.js';
import { checkDurable, fenceDurable } from '../network/durable/state.js';
import { canonical, fail, validate } from './protocol.js';
import { assertRegistration, u64 } from '../network/source-control/frames.js';

const copy = (value) => JSON.parse(JSON.stringify(value));
const immutable = (value) => {
  if (value && typeof value === 'object') {
    Object.values(value).forEach(immutable);
    Object.freeze(value);
  }
  return value;
};

/** The platform has already allocated this local. This is an atomic inbox
 * projection for the ORIGINAL DurableInbox, never an owner/local allocator. */
export async function openSharedInboxLedger({
  context,
  store,
  assertCurrent = () => {},
}) {
  context.check();
  validate('SessionBinding', context.binding);
  assertRegistration(context.registration);
  const binding = context.binding;
  const registration = context.registration;
  const native = context.nativeBinding;
  const connection = context.connection;
  const mailbox = context.mailbox;
  if (
    !mailbox ||
    Object.keys(mailbox).sort().join(',') !==
      'committedCursor,headSeq,receiveEnabled,receiveVersion' ||
    typeof mailbox.receiveEnabled !== 'boolean' ||
    !u64(mailbox.receiveVersion) ||
    !u64(mailbox.headSeq, false) ||
    !u64(mailbox.committedCursor, false) ||
    BigInt(mailbox.committedCursor) > BigInt(mailbox.headSeq)
  )
    fail('common.unavailable');
  if (
    registration.local !== native?.local ||
    native.hostSessionId !== binding.sessionId ||
    native.bindingId !== context.nativeWitness.binding_id ||
    typeof native.cwd !== 'string' ||
    !native.cwd ||
    registration.realm !== connection.realm ||
    registration.node !== connection.node ||
    !u64(context.producerEpoch)
  )
    fail('shared.binding_mismatch');
  const authority = immutable(
    copy({
      contextId: context.contextId,
      binding,
      registration,
      native,
      producerEpoch: context.producerEpoch,
      connection: Object.fromEntries(
        ['realm', 'node', 'incarnation', 'epoch', 'bootId', 'channelNonce'].map(
          (key) => [key, connection[key]],
        ),
      ),
    }),
  );
  const identity = immutable({
    tenant: binding.tenantId,
    realm: connection.realm,
    node: connection.node,
    nodeIncarnation: connection.incarnation,
  });
  const handle = await (
    store ??
    createFileLedgerStore({
      dir: `/var/lib/auriga/pools/${binding.physicalInstanceId}/host/message-inboxes/${context.contextId}`,
    })
  ).open();
  let closed = false,
    failed = false;
  let tail = Promise.resolve();
  let state;
  const check = (value) => {
    if (
      !value ||
      value.version !== 1 ||
      canonical(value.authority) !== canonical(authority) ||
      !Number.isSafeInteger(value.state?.revision) ||
      value.state.revision < 0
    )
      fail('common.unavailable');
    const body = value.state;
    if (
      body.version !== 2 ||
      Object.keys(body).sort().join(',') !==
        'bindings,durable,identity,locals,revision,version' ||
      !body.durable ||
      body.durable.producerEpoch !== context.producerEpoch ||
      canonical(body.identity) !== canonical(identity) ||
      Object.keys(body.bindings ?? {}).join(',') !== registration.local ||
      Object.keys(body.locals ?? {}).join(',') !== registration.local
    )
      fail('shared.binding_mismatch');
    const nativeRecord = body.bindings[registration.local];
    if (
      !['receive_none', 'thread_messages'].includes(nativeRecord.receiveMode) ||
      canonical({ ...nativeRecord, receiveMode: 'receive_none' }) !==
        canonical({
          local: registration.local,
          bindingId: native.bindingId,
          hostSessionId: binding.sessionId,
          cwd: native.cwd,
          preset: null,
          kind: 'session',
          state: 'bound',
          lifecycle: 'open',
          receiveMode: 'receive_none',
          contactId: null,
          groups: [],
          executeMode: 'prompt',
        }) ||
      canonical(body.locals[registration.local]) !==
        canonical({
          local: registration.local,
          state: 'registered',
          addressType: 'session',
          opId: registration.allocation_op_id,
          registrationId: registration.registration_id,
          registrationRevision: registration.registration_revision,
          fingerprint: registration.allocation_fingerprint.slice(7),
        })
    )
      fail('shared.binding_mismatch');
    checkDurable(body);
    return immutable(value);
  };
  const current = (reducing = false) => {
    if (closed || failed) fail('common.unavailable');
    if (!reducing) assertCurrent();
  };
  try {
    const saved = await handle.load();
    current();
    if (saved !== undefined) state = check(saved);
    else {
      // A missing cache cannot erase an accepted message or advance a cursor.
      // Initialize only from a proven unused, never-enabled platform mailbox.
      if (
        !mailbox ||
        mailbox.receiveEnabled !== false ||
        mailbox.receiveVersion !== '1' ||
        mailbox.headSeq !== '0' ||
        mailbox.committedCursor !== '0'
      )
        fail('common.unavailable');
      const local = registration.local;
      state = check({
        version: 1,
        authority,
        state: {
          version: 2,
          revision: 0,
          identity,
          bindings: {
            [local]: {
              local,
              bindingId: native.bindingId,
              hostSessionId: binding.sessionId,
              cwd: native.cwd,
              preset: null,
              kind: 'session',
              state: 'bound',
              lifecycle: 'open',
              receiveMode: 'receive_none',
              contactId: null,
              groups: [],
              executeMode: 'prompt',
            },
          },
          locals: {
            [local]: {
              local,
              state: 'registered',
              addressType: 'session',
              opId: registration.allocation_op_id,
              registrationId: registration.registration_id,
              registrationRevision: registration.registration_revision,
              fingerprint: registration.allocation_fingerprint.slice(7),
            },
          },
          durable: {
            producerEpoch: context.producerEpoch,
            resumeCursor: '0',
            submissions: {},
            receive: {},
            inbound: {},
            terminal: {},
          },
        },
      });
      await handle.commit(state);
      current();
    }
  } catch (error) {
    await handle.close();
    throw error;
  }
  function update(mutate, fence = () => {}, reducing = false) {
    const work = tail.then(async () => {
      current(reducing);
      fence();
      const draft = copy(state);
      const result = mutate(draft.state);
      if (result && typeof result.then === 'function')
        fail('common.validation_failed');
      if (draft.state.revision !== state.state.revision)
        fail('common.validation_failed');
      fenceDurable(state.state, draft.state);
      draft.state.revision++;
      check(draft);
      try {
        current(reducing);
        fence();
        await handle.commit(draft);
        current(reducing);
        fence();
      } catch (error) {
        failed = true;
        throw error;
      }
      state = immutable(draft);
      return result;
    });
    tail = work.catch(() => {});
    return work;
  }
  return {
    identity,
    peek: () => state.state,
    read: () => copy(state.state),
    assertAvailable: () => current(),
    update: (mutate, fence) => update(mutate, fence),
    // Denial alone is safe even when the latest network observation expired.
    // Callers cannot supply a mutation or bypass authorization for other writes.
    fenceReceive: () =>
      update(
        (draft) => {
          draft.bindings[registration.local].receiveMode = 'receive_none';
        },
        () => {},
        true,
      ),
    async close() {
      if (closed) return;
      closed = true;
      await tail;
      await handle.close();
    },
  };
}
