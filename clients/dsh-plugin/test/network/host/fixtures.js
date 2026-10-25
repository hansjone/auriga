// Fixtures for the transport-independent host seam. Nothing here touches a
// real DSH or user profile.
import { createHostAdapter } from '../../../src/network/host/adapter.js';
import { openLedger, digestOf } from '../../../src/network/host/ledger.js';
import { createMemoryLedgerStore } from '../../../src/network/host/store.js';

export const IDENTITY = {
  tenant: 'tenant-fixture',
  realm: '2',
  node: '64',
  nodeIncarnation: 'inc-1',
};
export const PEER_NODE = { realm: '2', node: '63' };

/** @param {string} local @param {string} [type] */
export const peer = (local, type = local === '0' ? 'system' : 'session') => ({
  version: 1,
  realm: '2',
  type,
  node: '63',
  local,
});
/** @param {string} local */
export const self = (local) => ({
  version: 1,
  realm: '2',
  type: local === '0' ? 'system' : 'session',
  node: '64',
  local,
});

export class Crash extends Error {
  constructor(point) {
    super(`crash at ${point}`);
    this.point = point;
  }
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

/**
 * Fake DSH host modelled on 0.2.1-rc.3 session-controller (source-read
 * behaviour, failure-validation §1.2). Items marked REAL-HOST need
 * confirmation against a real DSH build.
 *  - create: cwd = request.cwd ?? defaultCwd; an explicit existing ID with a
 *    different cwd throws session/conflict (index.js:755). Same ID + same cwd
 *    is modelled as returning the existing session (REAL-HOST).
 *  - prompt resumes a cold session (resolveAgent) without create; requestId
 *    dedup checks live inbox and history (:925/:949/:1128/:3218).
 *  - subagent-origin sessions reject the ordinary prompt (:128/:140/:394).
 *  - flush returns true only with a participant; it moves live → durable
 *    history (dsh-session :1951; persistence-jsonl). crash() drops live items.
 */
export function createFakeHost({ defaultCwd = 'C:/dsh/default' } = {}) {
  /** @type {Map<string, any>} */
  const sessions = new Map();
  const counts = { create: 0, prompt: 0, resume: 0, flush: 0 };
  const faults = {
    loseCreateResponse: false,
    createFails: false,
    promptThrowsAfterAccept: false,
    flushReturns: /** @type {boolean|null} */ (null),
    promptDelay: 0,
    /** @type {null|{ entered: () => void, release: Promise<void> }} */
    createGate: null,
  };
  let inflight = 0;
  let maxInflight = 0;
  /** @type {Map<string, number>} */
  const perSession = new Map();
  let maxPerSession = 0;

  function addSession(
    id,
    { cwd = defaultCwd, origin = 'user', participants = 1 } = {},
  ) {
    sessions.set(id, {
      id,
      cwd,
      origin,
      participants,
      live: [],
      history: [],
      loaded: false,
    });
  }

  return {
    counts,
    faults,
    sessions,
    addSession,
    get maxInflight() {
      return maxInflight;
    },
    get maxPerSession() {
      return maxPerSession;
    },
    /** History entries (durable) for a session. */
    history(id) {
      return sessions.get(id)?.history ?? [];
    },
    /** Host process death: in-memory prompts are lost, JSONL history stays. */
    crash() {
      for (const s of sessions.values()) {
        s.live = [];
        s.loaded = false;
      }
    },
    async getSession(id) {
      await tick();
      const s = sessions.get(id);
      return s ? { id: s.id, cwd: s.cwd, origin: s.origin } : null;
    },
    async createSession({ id, cwd, preset }) {
      await tick();
      if (faults.createGate) {
        const gate = faults.createGate;
        faults.createGate = null;
        gate.entered();
        await gate.release;
      }
      if (faults.createFails)
        throw Object.assign(new Error('create failed'), {
          code: 'session/error',
        });
      const effective = cwd ?? defaultCwd;
      const existing = sessions.get(id);
      if (existing) {
        if (existing.cwd !== effective)
          throw Object.assign(new Error('conflict'), {
            code: 'session/conflict',
          });
        return { id, cwd: existing.cwd };
      }
      counts.create++;
      addSession(id, { cwd: effective });
      sessions.get(id).preset = preset;
      if (faults.loseCreateResponse) {
        faults.loseCreateResponse = false;
        throw Object.assign(new Error('response lost'), {
          code: 'transport/lost',
        });
      }
      return { id, cwd: effective };
    },
    async prompt(id, { requestId, text, mode }) {
      const s = sessions.get(id);
      if (!s)
        throw Object.assign(new Error('missing'), {
          code: 'session/not_found',
        });
      if (s.origin === 'subagent')
        throw Object.assign(new Error('subagent'), {
          code: 'session/subagent',
        });
      if (!s.loaded) {
        s.loaded = true;
        counts.resume++;
      }
      if (
        s.live.some((x) => x.requestId === requestId) ||
        s.history.some((x) => x.requestId === requestId)
      )
        return { deduped: true };
      inflight++;
      perSession.set(id, (perSession.get(id) ?? 0) + 1);
      maxInflight = Math.max(maxInflight, inflight);
      maxPerSession = Math.max(maxPerSession, perSession.get(id));
      try {
        await new Promise((resolve) => setTimeout(resolve, faults.promptDelay));
        counts.prompt++;
        s.live.push({ requestId, text, mode });
      } finally {
        inflight--;
        perSession.set(id, perSession.get(id) - 1);
      }
      if (faults.promptThrowsAfterAccept) {
        faults.promptThrowsAfterAccept = false;
        throw new Error('prompt response lost');
      }
      return { accepted: true };
    },
    async hasPromptRequest(id, requestId) {
      await tick();
      const s = sessions.get(id);
      if (!s) return false;
      return (
        s.live.some((x) => x.requestId === requestId) ||
        s.history.some((x) => x.requestId === requestId)
      );
    },
    async flush(id) {
      await tick();
      counts.flush++;
      const s = sessions.get(id);
      if (!s || s.participants < 1) return false;
      if (faults.flushReturns !== null) {
        const v = faults.flushReturns;
        faults.flushReturns = null;
        if (!v) return false;
      }
      s.history.push(...s.live);
      s.live = [];
      return true;
    },
  };
}

/**
 * Fixture platform: the authoritative side of EXECUTION_ADMIT, LOCAL_REGISTER,
 * CONTACT_ASSIGN, ATTEMPT_BEGIN (≤5 automatic attempts) and RECEIPT commits,
 * plus a page transport with drop / duplicate / reorder injection.
 */
export function createFixturePlatform() {
  const state = {
    epoch: '5',
    highWater: 0n,
    registrations: new Map(),
    admissions: new Map(),
    attempts: new Map(),
    quarantined: new Set(),
    contacts: new Map(),
    committed: new Map(),
    calls: { admit: 0, register: 0, assign: 0, receipt: 0 },
  };
  const drop = { admit: 0, register: 0, assign: 0, receipt: 0 };
  let attemptSeq = 0;

  const network = {
    async admit(input) {
      state.calls.admit++;
      await tick();
      if (input.connectionEpoch !== state.epoch)
        return { status: 'denied', code: 'stale_epoch' };
      if (state.admissions.has(input.recipientId))
        return { status: 'denied', code: 'already_admitted' };
      const admissionId = `adm-${input.recipientId}`;
      state.admissions.set(input.recipientId, {
        admissionId,
        epoch: input.connectionEpoch,
        bootId: input.bootId,
      });
      if (drop.admit > 0) {
        drop.admit--;
        throw new Error('admit response lost');
      }
      return { status: 'admitted', admissionId };
    },
    async queryAdmission({ recipientId }) {
      const a = state.admissions.get(recipientId);
      return a
        ? { status: 'admitted', admissionId: a.admissionId }
        : { status: 'none' };
    },
    async registerLocal(entry) {
      state.calls.register++;
      await tick();
      const local = BigInt(entry.local);
      const existing = state.registrations.get(entry.local);
      let result;
      if (existing) {
        result =
          existing.opId === entry.opId &&
          existing.fingerprint === entry.fingerprint
            ? { status: 'registered', registrationId: existing.registrationId }
            : { status: 'conflict' };
      } else if (local <= state.highWater) {
        result = { status: 'conflict' };
      } else {
        const registrationId = `reg-${entry.local}`;
        state.registrations.set(entry.local, { ...entry, registrationId });
        state.highWater = local;
        result = { status: 'registered', registrationId };
      }
      if (drop.register > 0) {
        drop.register--;
        throw new Error('register response lost');
      }
      return result;
    },
    async assignContact({ contactId, assignedAddress, creationAttemptId }) {
      state.calls.assign++;
      await tick();
      const existing = state.contacts.get(contactId);
      let result;
      if (existing) {
        result =
          existing.assignedAddress.local === assignedAddress.local &&
          existing.creationAttemptId === creationAttemptId
            ? { status: 'assigned', creationPermit: false }
            : { status: 'denied' };
      } else {
        state.contacts.set(contactId, { assignedAddress, creationAttemptId });
        result = { status: 'assigned', creationPermit: true };
      }
      if (drop.assign > 0) {
        drop.assign--;
        throw new Error('assign response lost');
      }
      return result;
    },
  };

  /** ATTEMPT_BEGIN: platform-side counter, never refunded. */
  function attemptBegin(recipientId) {
    const n = (state.attempts.get(recipientId) ?? 0) + 1;
    state.attempts.set(recipientId, n);
    if (n > 5) {
      state.quarantined.add(recipientId);
      return null;
    }
    return `att-${recipientId}-${++attemptSeq}`;
  }

  /** RECEIPT → RECEIPT_COMMITTED, idempotent per (recipient, status). */
  function commitReceipts(receipts) {
    state.calls.receipt++;
    const committed = [];
    for (const r of receipts) {
      if (
        ![
          'host_accepted',
          'rejected',
          'quarantined',
          'outcome_unknown',
        ].includes(r.status)
      )
        continue;
      const prev = state.committed.get(r.recipientId);
      if (prev && prev.status !== r.status)
        throw new Error(`receipt conflict ${r.recipientId}`);
      state.committed.set(r.recipientId, r);
      committed.push(r.recipientId);
    }
    if (drop.receipt > 0) {
      drop.receipt--;
      throw new Error('receipt response lost');
    }
    return committed;
  }

  /**
   * Build a verified page; each recipient gets a fresh platform attempt.
   * Recipients over the attempt limit are withheld (platform quarantines).
   */
  function page(items, { epoch = state.epoch, bootId = 'boot-1' } = {}) {
    return {
      verified: true,
      tenant: IDENTITY.tenant,
      nodeIncarnation: IDENTITY.nodeIncarnation,
      epoch,
      bootId,
      items: items
        .map((item) => ({
          ...item,
          recipients: item.recipients
            .map((r) => ({
              ...r,
              attemptId: r.attemptId ?? attemptBegin(r.recipientId),
            }))
            .filter((r) => r.attemptId !== null),
        }))
        .filter((item) => item.recipients.length > 0),
    };
  }

  /**
   * One FETCH→deliver→RECEIPT cycle with transport faults.
   * opts.dropPage: the page never arrives. opts.duplicate: deliver twice.
   * opts.reorder: reverse item order.
   */
  async function pump(adapter, items, opts = {}) {
    if (opts.dropPage) return { delivered: false, receipts: [] };
    let list = opts.reorder ? [...items].reverse() : items;
    const receipts = await adapter.deliver(page(list, opts));
    if (opts.duplicate)
      receipts.push(...(await adapter.deliver(page(list, opts))));
    let committed = [];
    try {
      committed = commitReceipts(receipts);
      await adapter.markCommitted(committed);
    } catch {
      // Commit response lost: the adapter keeps pendingReceipts().
    }
    return { delivered: true, receipts, committed };
  }

  return { state, drop, network, attemptBegin, commitReceipts, page, pump };
}

let deliverySeq = 0;
/**
 * Unicast item to one local.
 * @param {object} o
 */
export function unicast({
  recipientId,
  local,
  from = peer('5'),
  threadId = 'T1',
  kind = 'reply',
  inReplyTo = 'req-1',
  text = 'hello',
  messageId,
  contactScope,
  threadSequence = '1',
}) {
  const message = {
    messageId: messageId ?? `msg-${recipientId}`,
    from,
    to: self(local),
    replyTo: from,
    threadId,
    threadSequence,
    inReplyTo,
    kind,
    intent: 'request',
    messageClass: 'unicast',
    senderKind: 'agent',
    body: { text },
  };
  if (contactScope) message.contactScope = contactScope;
  return {
    deliveryId: `dlv-${recipientId}-${++deliverySeq}`,
    mailSeq: String(deliverySeq),
    messageDigest: digestOf(message),
    message,
    recipients: [
      { recipientId, local, type: local === '0' ? 'system' : 'session' },
    ],
  };
}

/** Group item: one body, several locals in one node mail item. */
export function multicast({
  messageId,
  group = '23',
  locals,
  kind = 'message',
  text = 'group note',
}) {
  const message = {
    messageId,
    from: peer('5'),
    to: { version: 1, realm: '2', type: 'group', node: '0', local: group },
    replyTo: peer('5'),
    threadId: `group-${group}`,
    threadSequence: '1',
    inReplyTo: null,
    kind,
    intent: 'information',
    messageClass: 'multicast',
    senderKind: 'agent',
    body: { text },
  };
  return {
    deliveryId: `dlv-${messageId}`,
    mailSeq: String(++deliverySeq),
    messageDigest: digestOf(message),
    message,
    recipients: locals.map((local) => ({
      recipientId: `${messageId}:${local}`,
      local,
      type: local === '0' ? 'system' : 'session',
    })),
  };
}

/**
 * Plugin world: memory store + ledger + fake host + platform + adapter, with
 * crash points and restart (plugin and optionally host).
 */
export async function createWorld({
  store = createMemoryLedgerStore(),
  host = createFakeHost(),
  platform = createFixturePlatform(),
} = {}) {
  const crashPoints = new Map();
  const faults = {
    async at(point) {
      const n = crashPoints.get(point);
      if (n === undefined) return;
      if (n <= 1) crashPoints.delete(point);
      else crashPoints.set(point, n - 1);
      throw new Crash(point);
    },
  };
  const world = {
    store,
    host,
    platform,
    ledger: null,
    adapter: null,
    /** Arm a crash at a fault point (once by default). */
    crashAt(point, times = 1) {
      crashPoints.set(point, times);
    },
    async boot(initialize = false) {
      world.ledger = await openLedger({
        store,
        identity: IDENTITY,
        initialize,
      });
      world.adapter = createHostAdapter({
        ledger: world.ledger,
        host,
        network: platform.network,
        faults,
      });
      return world.adapter;
    },
    /** Plugin process death (and optionally the host's) then restart. */
    async restart({ hostCrash = false } = {}) {
      world.ledger.abandon();
      store.crash();
      if (hostCrash) host.crash();
      crashPoints.clear();
      return world.boot(false);
    },
    record(recipientId) {
      return world.ledger.read().recipients[recipientId];
    },
  };
  await world.boot(true);
  return world;
}

/**
 * Bind an existing host session (non-default cwd) and open a reply relation
 * to peer B1 for request `req-1` in thread T1.
 */
export async function boundSessionWithRelation(
  world,
  {
    hostSessionId = 'host-a1',
    cwd = 'D:/work/non-default',
    requestId = 'req-1',
    threadId = 'T1',
    peerLocal = '5',
    receiveMode = 'replies_only',
    executeMode = 'prompt',
  } = {},
) {
  if (!world.host.sessions.has(hostSessionId))
    world.host.addSession(hostSessionId, { cwd });
  const local = await world.adapter.bindExistingSession({
    opId: `bind:${hostSessionId}`,
    hostSessionId,
    cwd,
    receiveMode,
    executeMode,
  });
  await world.adapter.reserveOutboundRequest({
    requestId,
    local,
    threadId,
    peer: peer(peerLocal),
  });
  await world.adapter.activateOutboundRequest(requestId);
  return local;
}
