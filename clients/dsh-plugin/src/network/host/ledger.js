/* global structuredClone */
import { randomBytes, createHash } from 'node:crypto';
import { CODES, HostSeamError } from './errors.js';
import { isUint64String } from './address.js';

const LEDGER_VERSION = 1;
const UINT64_MAX = (1n << 64n) - 1n;
const MAX_AUDIT = 256;

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
 *   purpose: string, state: 'reserved'|'registered'|'abandoned'|'conflict',
 *   registrationId: string|null,
 * }} LocalRegistration
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
 *   identityConflict: string|null,
 *   localHighWater: string,
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
 * }} LedgerState
 */

/** @param {LedgerIdentity} identity @returns {LedgerState} */
function emptyState(identity) {
  return {
    version: LEDGER_VERSION,
    identity: { ...identity },
    revision: 0,
    identityConflict: null,
    localHighWater: '0',
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
    s.version !== LEDGER_VERSION ||
    !Number.isSafeInteger(s.revision) ||
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
  return s;
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
  let tail = Promise.resolve();
  let closed = false;

  /**
   * @template T
   * @param {(draft: LedgerState) => T} mutate  must be synchronous
   * @returns {Promise<T>}
   */
  function update(mutate) {
    const run = tail.then(async () => {
      if (closed) throw new HostSeamError(CODES.ledgerUnavailable);
      const draft = structuredClone(state);
      const result = mutate(draft);
      draft.revision = state.revision + 1;
      await handle.commit(draft);
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
 * @param {{ opId: string, purpose: string, mappingDigest: string }} input
 * @returns {Promise<LocalRegistration>}
 */
export async function reserveLocal(ledger, { opId, purpose, mappingDigest }) {
  return ledger.update((draft) => {
    if (draft.identityConflict)
      throw new HostSeamError(CODES.recoveryConflict, 'identity conflict');
    const existing = draft.locals[draft.registrations[opId] ?? ''];
    if (existing) return existing;
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
      fingerprint: createHash('sha256')
        .update(`${opId}\0${local}\0${nonce}\0${mappingDigest}`)
        .digest('hex'),
      purpose,
      state: 'reserved',
      registrationId: null,
    };
    draft.localHighWater = local;
    draft.locals[local] = entry;
    draft.registrations[opId] = local;
    draft.localQueue.push(opId);
    return entry;
  });
}

/**
 * Drive the queue head through LOCAL_REGISTER. A lost response leaves the
 * head in place; retrying uses the same op/local/fingerprint.
 * @param {Ledger} ledger
 * @param {string} opId
 * @param {(entry: LocalRegistration) => Promise<{ status: 'registered', registrationId: string }|{ status: 'conflict' }>} registerLocal
 * @returns {Promise<LocalRegistration>}
 */
export async function registerReservedLocal(ledger, opId, registerLocal) {
  const local = ledger.peek().registrations[opId];
  const entry = local === undefined ? undefined : ledger.peek().locals[local];
  if (!entry) throw new HostSeamError(CODES.protocolError, 'unknown op');
  if (entry.state === 'registered') return structuredClone(entry);
  if (entry.state !== 'reserved')
    throw new HostSeamError(CODES.localAllocationConflict, entry.state);
  const result = await registerLocal(structuredClone(entry));
  const outcome = await ledger.update((draft) => {
    const current = draft.locals[entry.local];
    if (result?.status === 'registered' && result.registrationId) {
      current.state = 'registered';
      current.registrationId = result.registrationId;
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
}
