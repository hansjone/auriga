/* global structuredClone */
import { randomBytes, createHash } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { CODES, HostSeamError } from './errors.js';
import { isUint64String } from './address.js';
import { createKeyedMutex } from './keyed-mutex.js';
import { checkSources, fenceSourceChanges } from '../source-control/state.js';

/** @type {WeakMap<Ledger, ReturnType<typeof createKeyedMutex>>} */
const registerMutexes = new WeakMap();
/** @param {Ledger} ledger */
function registrationLocks(ledger) {
  let locks = registerMutexes.get(ledger);
  if (!locks) {
    locks = createKeyedMutex();
    registerMutexes.set(ledger, locks);
  }
  return locks;
}

const LEDGER_VERSION = 2;
const UINT64_MAX = (1n << 64n) - 1n;
const MAX_AUDIT = 256;
const RECEIPT_FIELDS = /** @type {const} */ ([
  'recipients',
  'inbox',
  'results',
  'bodies',
  'audit',
]);

/**
 * @typedef {{ tenant: string, realm: string, node: string, nodeIncarnation: string }} LedgerIdentity
 *
 * Recipient stages (protocol §6): RECEIVED → ADMIT_REQUESTED → ADMITTED →
 * INVOCATION_STARTED → HOST_ACCEPTED, plus INBOX_ACCEPTED for inbox_only /
 * system intake and the local terminal REJECTED / QUARANTINED / OUTCOME_UNKNOWN.
 * @typedef {'RECEIVED'|'ADMIT_REQUESTED'|'ADMITTED'|'INVOCATION_STARTED'|'HOST_ACCEPTED'|'INBOX_ACCEPTED'|'REJECTED'|'QUARANTINED'|'OUTCOME_UNKNOWN'} RecipientState
 *
 * @typedef {{
 *   recipientId: string, deliveryId: string, messageId: string, messageDigest: string,
 *   tenant: string, nodeIncarnation: string,
 *   targetAddress: import('./address.js').Address,
 *   assignedAddress: import('./address.js').Address|null,
 *   local: string, bindingId: string|null, hostSessionId: string|null,
 *   hostRequestId: string, stage: 'system'|'session',
 *   state: RecipientState, acceptedBy: 'system'|'session'|null,
 *   acceptanceMode: 'prompt'|'inbox_only'|null,
 *   attempts: string[], admissionId: string|null, admissionEpoch: string|null,
 *   code: string|null, localReason: string|null,
 *   platformCommitted: boolean, updatedAt: string,
 * }} RecipientRecord
 *
 * @typedef {{
 *   local: string, bindingId: string, hostSessionId: string, cwd: string, preset: string|null,
 *   kind: 'session'|'subagent', state: 'reserved'|'creating'|'bound'|'unknown',
 *   lifecycle: 'open'|'archived'|'closed'|'deleted',
 *   receiveMode: 'replies_only'|'thread_messages'|'receive_none',
 *   contactId: string|null, groups: string[],
 *   executeMode: 'prompt'|'inbox_only',
 * }} Binding
 *
 * @typedef {{
 *   id: string, targetLocal: string, peer: string|null, peerNode: string,
 *   threadId: string, kinds: string[], openRequests: string[],
 *   state: 'reserved'|'active'|'revoked', revision: number,
 *   introContactId: string|null,
 * }} Relation
 *
 * @typedef {{
 *   opId: string, local: string, nonce: string, fingerprint: string,
 *   addressType: 'session'|'subagent', expectedRegistrationRevision: string,
 *   registrationRevision?: string,
 *   purpose: string, mappingDigest: string, state: 'reserved'|'registered'|'abandoned'|'conflict',
 *   registrationId: string|null,
 * }} LocalRegistration
 * @typedef {{status:'registered', registrationId:string, opId:string, local:string,
 *   addressType:'session'|'subagent', fingerprint:string, registrationRevision:string,
 *   localHighWater:string}|{status:'conflict'}} RegistrationResult
 *
 * @typedef {{
 *   contactId: string, recipientId: string, from: import('./address.js').Address,
 *   threadId: string, state: 'awaiting_session'|'reserved'|'assign_requested'|'creation_started'|'session_ready'|'unknown'|'denied',
 *   local: string|null, hostSessionId: string|null, cwd: string|null, preset: string|null,
 *   creationAttemptId: string|null,
 * }} ContactRecord
 *
 * @typedef {{
 *   version: number, identity: LedgerIdentity, revision: number,
 *   receiptRevision?: number,
 *   identityConflict: string|null,
 *   localHighWater: string,
 *   registrationRevision?: string,
 *   localQueue: string[],
 *   locals: Record<string, LocalRegistration>,
 *   registrations: Record<string, string>,
 *   bindings: Record<string, Binding>,
 *   relations: Record<string, Relation>,
 *   contacts: Record<string, ContactRecord>,
 *   bodies: Record<string, { digest: string, body: unknown }>,
 *   inbox: Record<string, { recipientId: string, local: string, messageId: string, acceptedAt: string }>,
 *   recipients: Record<string, RecipientRecord>,
 *   results: Record<string, { resultId: string, recipientId: string, status: string, sent: boolean }>,
 *   audit: Array<{ at: string, recipientId: string, reason: string }>,
 *   inboundPolicy?: import('../policy/inbound-policy.js').InboundPolicyState,
 *   sourceControl?: import('../source-control/types.js').SourceState,
 * }} LedgerState
 */

/** @param {LedgerIdentity} identity @returns {LedgerState} */
function emptyState(identity) {
  return {
    version: LEDGER_VERSION,
    identity: { ...identity },
    revision: 0,
    receiptRevision: 0,
    identityConflict: null,
    localHighWater: '0',
    registrationRevision: '0',
    localQueue: [],
    locals: {},
    registrations: {},
    bindings: {},
    relations: {},
    contacts: {},
    bodies: {},
    inbox: {},
    recipients: {},
    results: {},
    audit: [],
  };
}

/** @param {unknown} value @param {LedgerIdentity} identity @returns {LedgerState} */
function checkState(value, identity) {
  const s = /** @type {LedgerState} */ (value);
  if (
    !s ||
    typeof s !== 'object' ||
    ![1, LEDGER_VERSION, 3].includes(s.version) ||
    !Number.isSafeInteger(s.revision) ||
    (s.receiptRevision !== undefined &&
      (!Number.isSafeInteger(s.receiptRevision) ||
        s.receiptRevision < 0 ||
        s.receiptRevision > s.revision ||
        (s.receiptRevision === 0 &&
          RECEIPT_FIELDS.some(
            (field) => Object.keys(s[field] ?? {}).length,
          )))) ||
    !isUint64String(s.localHighWater) ||
    !s.identity ||
    s.identity.tenant !== identity.tenant ||
    s.identity.realm !== identity.realm ||
    s.identity.node !== identity.node ||
    s.identity.nodeIncarnation !== identity.nodeIncarnation
  )
    throw new HostSeamError(
      CODES.ledgerUnavailable,
      'ledger identity mismatch',
    );
  checkAllocationState(s);
  checkSources(s);
  if (Object.values(s.locals).some((entry) => !entry.addressType))
    s.identityConflict = CODES.recoveryConflict;
  else if (s.registrationRevision === undefined) {
    if (Object.keys(s.locals).length)
      s.identityConflict = CODES.recoveryConflict;
    else s.registrationRevision = '0';
  }
  if (
    Object.values(s.bindings).some(
      (b) => s.locals[b.local]?.addressType !== b.kind,
    )
  )
    s.identityConflict = CODES.recoveryConflict;
  return s;
}

/** Missing counter/queue/mapping evidence cannot be repaired by inspecting
 * live sessions or by lifting the counter to the platform high-water mark.
 * @param {LedgerState} s */
function checkAllocationState(s) {
  const invalid = () => {
    throw new HostSeamError(
      CODES.ledgerUnavailable,
      'allocation log incomplete',
    );
  };
  if (
    !s.locals ||
    typeof s.locals !== 'object' ||
    Array.isArray(s.locals) ||
    !s.registrations ||
    typeof s.registrations !== 'object' ||
    Array.isArray(s.registrations) ||
    !Array.isArray(s.localQueue) ||
    s.localQueue.length > 1
  )
    invalid();
  let maximum = 0n;
  let registrationRevision = 0n;
  let legacy = false;
  for (const [local, entry] of Object.entries(s.locals)) {
    if (
      !isUint64String(local) ||
      local === '0' ||
      !entry ||
      entry.local !== local ||
      typeof entry.opId !== 'string' ||
      !entry.opId ||
      typeof entry.purpose !== 'string' ||
      !entry.purpose ||
      typeof entry.mappingDigest !== 'string' ||
      !entry.mappingDigest ||
      typeof entry.nonce !== 'string' ||
      !entry.nonce ||
      !['reserved', 'registered', 'abandoned', 'conflict'].includes(
        entry.state,
      ) ||
      s.registrations[entry.opId] !== local
    )
      invalid();
    const typed =
      entry.addressType === 'session' || entry.addressType === 'subagent';
    legacy ||= !typed;
    if ('addressType' in entry && !typed) invalid();
    if (typed && !isUint64String(entry.expectedRegistrationRevision)) invalid();
    if (
      entry.fingerprint !==
      createHash('sha256')
        .update(
          `${entry.opId}\0${local}\0${entry.nonce}\0${entry.mappingDigest}` +
            (typed ? `\0${entry.addressType}` : ''),
        )
        .digest('hex')
    )
      invalid();
    const unsettled = entry.state === 'reserved' || entry.state === 'conflict';
    if (unsettled !== (s.localQueue[0] === entry.opId)) invalid();
    if (
      !unsettled &&
      (typeof entry.registrationId !== 'string' || !entry.registrationId)
    )
      invalid();
    if (typed && !unsettled) {
      if (
        !isUint64String(entry.registrationRevision) ||
        BigInt(entry.registrationRevision ?? '0') !==
          BigInt(entry.expectedRegistrationRevision) + 1n
      )
        invalid();
      if (BigInt(entry.registrationRevision ?? '0') > registrationRevision)
        registrationRevision = BigInt(entry.registrationRevision ?? '0');
    }
    if (BigInt(local) > maximum) maximum = BigInt(local);
  }
  if (
    maximum.toString() !== s.localHighWater ||
    Object.keys(s.registrations).length !== Object.keys(s.locals).length ||
    s.localQueue.some((id) => !s.locals[s.registrations[id]])
  )
    invalid();
  if (
    !legacy &&
    (Object.keys(s.locals).length || s.registrationRevision !== undefined)
  ) {
    if (s.registrationRevision !== registrationRevision.toString()) invalid();
    const head = s.locals[s.registrations[s.localQueue[0]]];
    if (head && head.expectedRegistrationRevision !== s.registrationRevision)
      invalid();
  }
}

/** Caller owns this one writer handle through connection shutdown. Never probe
 * outside its lock or open the same path again to discover an incarnation.
 * @param {import('./store.js').LedgerStoreHandle} handle
 * @param {{tenant:string,realm:string,node:string,created:boolean}} registration */
export async function loadConnectionLedger(handle, registration) {
  const loaded = await handle.load();
  /** @type {LedgerState|undefined} */
  let state;
  let consumed = false;
  const invalid = () =>
    new HostSeamError(CODES.ledgerUnavailable, 'connection ledger unavailable');
  /** @param {LedgerIdentity} identity */
  const identityValid = (identity) =>
    identity &&
    identity.tenant === registration.tenant &&
    identity.realm === registration.realm &&
    identity.node === registration.node &&
    ['realm', 'node'].every(
      (key) =>
        isUint64String(/** @type {any} */ (identity)[key]) &&
        /** @type {any} */ (identity)[key] !== '0',
    ) &&
    typeof identity.nodeIncarnation === 'string' &&
    /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(identity.nodeIncarnation);
  if (loaded !== undefined) {
    const value = /** @type {LedgerState} */ (loaded);
    if (
      !identityValid(value?.identity) ||
      !Number.isSafeInteger(value.revision) ||
      value.revision < 0 ||
      ![
        'bindings',
        'relations',
        'contacts',
        'bodies',
        'inbox',
        'recipients',
        'results',
      ].every((key) => {
        const map = /** @type {any} */ (value)[key];
        return map && typeof map === 'object' && !Array.isArray(map);
      }) ||
      !Array.isArray(value.audit)
    )
      throw invalid();
    state = checkState(value, value.identity);
    if (state.identityConflict) throw invalid();
  } else if (registration.created !== true) throw invalid();
  return {
    get state() {
      return state;
    },
    /** Adopt the already loaded snapshot and writer; never reopen the store.
     * @returns {Ledger} */
    adopt() {
      if (!state || consumed) throw invalid();
      consumed = true;
      return ledgerFromHandle(handle, state);
    },
    async initialize(/** @type {LedgerIdentity} */ identity) {
      if (
        consumed ||
        state ||
        loaded !== undefined ||
        registration.created !== true ||
        !identityValid(identity)
      )
        throw invalid();
      consumed = true; // Includes uncertain writes: this permission is never replayable.
      const initial = emptyState(identity);
      await handle.commit(initial);
      state = initial;
      consumed = false;
    },
  };
}

/**
 * Open the durable host ledger. Mutations are serialized in-process and each
 * one commits a full snapshot before its result becomes visible; a failed
 * commit leaves the in-memory view unchanged and surfaces the error.
 *
 * @param {{
 *   store: { open(): Promise<import('./store.js').LedgerStoreHandle> },
 *   identity: LedgerIdentity,
 *   initialize?: boolean,
 *   now?: () => Date,
 * }} options
 */
export async function openLedger({
  store,
  identity,
  initialize = false,
  now = () => new Date(),
}) {
  for (const key of /** @type {const} */ (['realm', 'node']))
    if (!isUint64String(identity?.[key]) || identity[key] === '0')
      throw new HostSeamError(CODES.protocolError, 'invalid identity');
  if (!identity.tenant || !identity.nodeIncarnation)
    throw new HostSeamError(CODES.protocolError, 'invalid identity');
  const handle = await store.open();
  /** @type {LedgerState} */
  let state;
  try {
    const loaded = await handle.load();
    if (loaded === undefined) {
      // A missing ledger is never silently recreated: only an explicit
      // first-install initialization may start from local=1.
      if (!initialize)
        throw new HostSeamError(CODES.ledgerUnavailable, 'ledger missing');
      state = emptyState(identity);
      await handle.commit(state);
    } else {
      state = checkState(loaded, identity);
    }
  } catch (error) {
    await handle.close();
    throw error;
  }
  return ledgerFromHandle(handle, state, now);
}

/** Same writer and same snapshot for bootstrap and all later capabilities.
 * @param {import('./store.js').LedgerStoreHandle} handle
 * @param {LedgerState} state @param {()=>Date} [now] */
function ledgerFromHandle(handle, state, now = () => new Date()) {
  const identity = state.identity;
  let tail = Promise.resolve();
  let closed = false;
  let commitFailed = false;

  /**
   * @template T
   * @param {(draft: LedgerState) => T} mutate  must be synchronous
   * @param {()=>void} [fence] checked inside the queue and after durable commit
   * @returns {Promise<T>}
   */
  function update(mutate, fence = () => {}) {
    const run = tail.then(async () => {
      fence();
      if (closed || commitFailed)
        throw new HostSeamError(CODES.ledgerUnavailable);
      const draft = structuredClone(state);
      const result = mutate(draft);
      // File commits, local registration and source operations are not message
      // receipt checkpoints. Track receipt history under this same durable
      // writer, including removal of evidence; it can never become zero again.
      // An older ledger without the witness must not acquire a guessed zero.
      if (draft.receiptRevision !== state.receiptRevision)
        throw new HostSeamError(
          CODES.recoveryConflict,
          'immutable receipt revision',
        );
      if (
        state.receiptRevision !== undefined &&
        RECEIPT_FIELDS.some(
          (field) => !isDeepStrictEqual(state[field], draft[field]),
        )
      )
        draft.receiptRevision = state.receiptRevision + 1;
      fenceSourceChanges(state, draft);
      for (const [local, original] of Object.entries(state.locals)) {
        const next = draft.locals[local];
        for (const key of /** @type {const} */ ([
          'opId',
          'local',
          'nonce',
          'fingerprint',
          'mappingDigest',
          'purpose',
          'addressType',
          'expectedRegistrationRevision',
        ]))
          if (!next || next[key] !== original[key])
            throw new HostSeamError(
              CODES.localAllocationConflict,
              'immutable local witness',
            );
      }
      if (
        state.identityConflict &&
        draft.identityConflict !== state.identityConflict
      )
        throw new HostSeamError(CODES.recoveryConflict);
      draft.revision = state.revision + 1;
      try {
        fence();
        await handle.commit(draft);
        fence();
      } catch (error) {
        // Rename/fsync may have committed even when the call failed. Never
        // overwrite an uncertain durable snapshot with our old memory view.
        commitFailed = true;
        throw error;
      }
      state = draft;
      return result;
    });
    tail = run.then(
      () => {},
      () => {},
    );
    return run;
  }

  return {
    identity: { ...identity },
    /** Deep copy of the committed view. */
    read() {
      return structuredClone(state);
    },
    /** Synchronous peek at committed state; callers must not mutate it. */
    peek() {
      return state;
    },
    update,
    assertAvailable() {
      if (closed || commitFailed)
        throw new HostSeamError(CODES.ledgerUnavailable);
    },
    nowIso() {
      return now().toISOString();
    },
    async close() {
      closed = true;
      await tail;
      await handle.close();
    },
    /** Test-only: model process death (no lock release, no flush). */
    abandon() {
      closed = true;
      const h = /** @type {{ abandon?: () => void }} */ (handle);
      h.abandon?.();
    },
  };
}

/** @typedef {Awaited<ReturnType<typeof openLedger>>} Ledger */

/** @param {LedgerState} draft @param {string} recipientId @param {string} reason @param {string} at */
export function pushAudit(draft, recipientId, reason, at) {
  draft.audit.push({ at, recipientId, reason });
  if (draft.audit.length > MAX_AUDIT)
    draft.audit.splice(0, draft.audit.length - MAX_AUDIT);
}

/** @param {string} prefix */
export function newId(prefix) {
  return `${prefix}-${randomBytes(12).toString('hex')}`;
}

/** @param {unknown} value */
export function digestOf(value) {
  return createHash('sha256').update(JSON.stringify(value)).digest('hex');
}

/**
 * Reserve the next local in the node-level single queue (protocol §1:
 * "预留即登记"). The counter advances and is committed before the number is
 * exposed; it never moves backwards, and a reserved-but-unused number is
 * burned. Only the queue head may be reserved while an earlier op is
 * unsettled.
 *
 * @param {Ledger} ledger
 * @param {{ opId: string, purpose: string, mappingDigest: string, addressType:'session'|'subagent' }} input
 * @returns {Promise<LocalRegistration>}
 */
export async function reserveLocal(
  ledger,
  { opId, purpose, mappingDigest, addressType },
) {
  if (
    !opId ||
    !purpose ||
    !mappingDigest ||
    !['session', 'subagent'].includes(addressType)
  )
    throw new HostSeamError(CODES.protocolError, 'allocation intent required');
  return ledger.update((draft) => {
    if (draft.identityConflict)
      throw new HostSeamError(CODES.recoveryConflict, 'identity conflict');
    const existing = draft.locals[draft.registrations[opId] ?? ''];
    if (existing) {
      if (
        existing.purpose !== purpose ||
        existing.mappingDigest !== mappingDigest ||
        existing.addressType !== addressType
      )
        throw new HostSeamError(
          CODES.localAllocationConflict,
          'mapping mismatch',
        );
      return structuredClone(existing);
    }
    const head = draft.localQueue[0];
    if (head !== undefined)
      throw new HostSeamError(CODES.localQueueBusy, 'local queue busy');
    const next = BigInt(draft.localHighWater) + 1n;
    if (next > UINT64_MAX)
      throw new HostSeamError(CODES.localAllocationConflict, 'exhausted');
    const local = next.toString();
    const nonce = randomBytes(16).toString('hex');
    /** @type {LocalRegistration} */
    const entry = {
      opId,
      local,
      nonce,
      addressType,
      expectedRegistrationRevision: draft.registrationRevision ?? '0',
      fingerprint: createHash('sha256')
        .update(`${opId}\0${local}\0${nonce}\0${mappingDigest}\0${addressType}`)
        .digest('hex'),
      purpose,
      mappingDigest,
      state: 'reserved',
      registrationId: null,
    };
    draft.localHighWater = local;
    draft.locals[local] = entry;
    draft.registrations[opId] = local;
    draft.localQueue.push(opId);
    return structuredClone(entry);
  });
}

/**
 * Drive the queue head through LOCAL_REGISTER. A lost response leaves the
 * head in place; retrying uses the same op/local/fingerprint.
 * @param {Ledger} ledger
 * @param {string} opId
 * @param {(entry: LocalRegistration) => Promise<RegistrationResult>} registerLocal
 * @returns {Promise<LocalRegistration>}
 */
export async function registerReservedLocal(ledger, opId, registerLocal) {
  return registrationLocks(ledger).run('register', async () => {
    ledger.assertAvailable();
    if (ledger.peek().identityConflict)
      throw new HostSeamError(CODES.recoveryConflict);
    const local = ledger.peek().registrations[opId];
    const entry = local === undefined ? undefined : ledger.peek().locals[local];
    if (!entry) throw new HostSeamError(CODES.protocolError, 'unknown op');
    if (entry.state === 'registered') return structuredClone(entry);
    if (entry.state !== 'reserved')
      throw new HostSeamError(CODES.localAllocationConflict, entry.state);
    if (ledger.peek().localQueue[0] !== opId)
      throw new HostSeamError(CODES.localQueueBusy);
    const result = await registerLocal(structuredClone(entry));
    if (!(
      result?.status === 'conflict' ||
      (result?.status === 'registered' &&
        typeof result.registrationId === 'string' &&
        /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(result.registrationId) &&
        result.local === entry.local &&
        result.opId === entry.opId &&
        result.addressType === entry.addressType &&
        result.fingerprint === entry.fingerprint &&
        isUint64String(result.registrationRevision) &&
        result.registrationRevision !== '0' &&
        BigInt(result.registrationRevision) ===
          BigInt(entry.expectedRegistrationRevision) + 1n &&
        isUint64String(result.localHighWater) &&
        BigInt(result.localHighWater) >= BigInt(entry.local))
    ))
      throw new HostSeamError(
        CODES.protocolError,
        'unknown registration response',
      );
    const outcome = await ledger.update((draft) => {
      const current = draft.locals[entry.local];
      if (current.state !== 'reserved' || draft.localQueue[0] !== opId)
        throw new HostSeamError(CODES.recoveryConflict);
      if (result?.status === 'registered' && result.registrationId) {
        current.state = 'registered';
        current.registrationId = result.registrationId;
        current.registrationRevision = result.registrationRevision;
        draft.registrationRevision = result.registrationRevision;
        draft.localQueue = draft.localQueue.filter((id) => id !== opId);
        return structuredClone(current);
      }
      // A different op/fingerprint already owns this number, or it is not above
      // the platform high-water mark: stop new allocation for this identity.
      current.state = 'conflict';
      draft.identityConflict = CODES.localAllocationConflict;
      return null;
    });
    if (!outcome)
      throw new HostSeamError(CODES.localAllocationConflict, 'conflict');
    return outcome;
  });
}
