/* global structuredClone */
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { createFileLedgerStore } from '../host/store.js';
import {
  DAY_MS,
  LAYERS,
  MODES,
  CHOICES,
  textId,
  checkLayer,
  checkSource,
  checkRequest,
  nodeKey,
  userKey,
  requestKey,
  proofKey,
  cooldownKeys,
  evaluate,
  matches,
} from './model.js';
export { publicReceipt } from './model.js';

/** Reuse the host atomic writer, never share its ledger.json with consent.
 * @param {Parameters<typeof createFileLedgerStore>[0]} options */
export function createFileConsentStore(options) {
  return createFileLedgerStore({
    ...options,
    dir: join(options.dir, 'consent'),
  });
}

/** @param {import('./types.js').Identity} identity @returns {import('./types.js').State} */
function empty(identity) {
  return {
    version: 1,
    identity: { ...identity },
    policyRevision: 0,
    lastNow: 0,
    defaults: { conversation: 'ask_each', execution: 'ask_each' },
    rules: [],
    requests: {},
    cooldowns: {},
    audit: [],
    outbox: [],
  };
}

/** Validate persisted authority before using any grant.
 * @param {unknown} value @param {import('./types.js').Identity} identity
 * @returns {import('./types.js').State} */
function restore(value, identity) {
  const s = /** @type {import('./types.js').State} */ (value);
  if (
    !s ||
    s.version !== 1 ||
    !s.identity ||
    !['tenant', 'realm', 'node', 'nodeIncarnation'].every(
      (k) =>
        s.identity[/** @type {keyof typeof identity} */ (k)] ===
        identity[/** @type {keyof typeof identity} */ (k)],
    ) ||
    !Number.isSafeInteger(s.policyRevision) ||
    s.policyRevision < 0 ||
    !Number.isSafeInteger(s.lastNow) ||
    s.lastNow < 0 ||
    !s.defaults ||
    !LAYERS.every((l) => MODES.includes(s.defaults[l])) ||
    !Array.isArray(s.rules) ||
    !s.requests ||
    Array.isArray(s.requests) ||
    !s.cooldowns ||
    Array.isArray(s.cooldowns) ||
    !Array.isArray(s.audit) ||
    !Array.isArray(s.outbox)
  )
    throw new Error('consent unavailable: invalid snapshot or identity');
  for (const rule of s.rules) {
    textId(rule.id);
    checkLayer(rule.layer);
    if (
      !['source_node', 'source_user'].includes(rule.scope) ||
      !['allow_source', 'block'].includes(rule.choice) ||
      !Number.isSafeInteger(rule.policyRevision) ||
      rule.policyRevision < 0 ||
      rule.policyRevision > s.policyRevision
    )
      throw new Error('consent unavailable: invalid rule');
    checkVerified(rule.source, rule.source);
  }
  for (const [key, row] of Object.entries(s.requests)) {
    checkRequest(row.request);
    checkVerified(row.source, row.request.source);
    if (
      key !== requestKey(row.request) ||
      !['pending', 'allowed', 'denied', 'expired', 'revoked'].includes(
        row.status,
      ) ||
      !Number.isSafeInteger(row.policyRevision) ||
      row.policyRevision < 0 ||
      row.policyRevision > s.policyRevision ||
      typeof row.manual !== 'boolean' ||
      row.deadline !==
        Math.min(row.request.expiresAt, row.request.createdAt + DAY_MS)
    )
      throw new Error('consent unavailable: invalid request');
    textId(row.decisionId);
  }
  if (Object.values(s.cooldowns).some((n) => !Number.isSafeInteger(n) || n < 0))
    throw new Error('consent unavailable: invalid cooldown');
  return s;
}

/** @param {import('./types.js').VerifiedSource} s @param {import('./types.js').Source} expected */
function checkVerified(s, expected) {
  if (!s || nodeKey(s) !== nodeKey(expected))
    throw new TypeError('invalid verified source');
  checkSource({ tenant: s.tenant, realm: s.realm, node: s.node });
  textId(s.userId);
  textId(s.ownershipRevision);
}

/** Local authority only. Do not register these methods as MCP/model tools.
 * Every operation is serialized; failed/uncertain commits poison this instance.
 * @param {import('./types.js').Options} options */
export async function openConsent({
  store,
  identity,
  resolveSource,
  organization,
  authorizeLocal,
  now = Date.now,
  limits: overrides = {},
}) {
  identity = { ...identity };
  checkSource({
    tenant: identity.tenant,
    realm: identity.realm,
    node: identity.node,
  });
  textId(identity.nodeIncarnation);
  for (const port of [resolveSource, organization, authorizeLocal])
    if (typeof port !== 'function')
      throw new TypeError('trusted consent ports required');
  const limits = {
    maxPending: 100,
    maxPendingBytes: 1_048_576,
    maxRecords: 10_000,
    ...overrides,
  };
  for (const limit of Object.values(limits))
    if (!Number.isSafeInteger(limit) || limit < 1)
      throw new RangeError('invalid consent limit');
  const handle = await store.open();
  /** @type {import('./types.js').State} */
  let state;
  try {
    const loaded = await handle.load();
    state = loaded === undefined ? empty(identity) : restore(loaded, identity);
    if (loaded === undefined) await handle.commit(state);
  } catch (error) {
    await handle.close();
    throw error;
  }
  let closed = false;
  let poisoned = false;
  let tail = Promise.resolve();

  /** @param {import('./types.js').State} draft */
  function currentTime(draft) {
    const at = now();
    if (!Number.isSafeInteger(at) || at < draft.lastNow)
      throw new Error('consent unavailable: clock rollback');
    draft.lastNow = at;
    return at;
  }

  /** @template T @param {(draft: import('./types.js').State, at: number) => Promise<T>|T} operation @returns {Promise<T>} */
  function transaction(operation) {
    const run = tail.then(async () => {
      if (closed || poisoned) throw new Error('consent unavailable');
      const at = now();
      if (!Number.isSafeInteger(at) || at < state.lastNow)
        throw new Error('consent unavailable: clock rollback');
      const draft = structuredClone(state);
      draft.lastNow = at;
      for (const [key, until] of Object.entries(draft.cooldowns))
        if (until <= at) delete draft.cooldowns[key];
      expire(draft, at);
      const result = await operation(draft, at);
      expire(draft, currentTime(draft));
      try {
        await handle.commit(draft);
      } catch (error) {
        poisoned = true;
        throw error;
      }
      state = draft;
      return structuredClone(result);
    });
    tail = run.then(
      () => {},
      () => {},
    );
    return run;
  }

  /** @param {unknown} context */
  async function local(context) {
    if ((await authorizeLocal(context)) !== true)
      throw new Error('authenticated local UI required');
  }

  /** @param {import('./types.js').Source} source */
  async function trusted(source) {
    if (source.tenant !== identity.tenant || source.realm !== identity.realm)
      throw new TypeError('invalid source realm/tenant');
    const verified = await resolveSource({ ...source });
    checkVerified(verified, source);
    // Copy only authority fields; never persist callback extras or display names.
    return {
      ...source,
      userId: verified.userId,
      ownershipRevision: verified.ownershipRevision,
    };
  }

  /** @param {import('./types.js').VerifiedSource} source */
  async function currentOrganization(source) {
    const org = await organization({ ...source });
    if (!org || org.available !== true)
      return {
        available: false,
        revision: '',
        sameOrganization: false,
        conversation: /** @type {const} */ ('deny_all'),
        execution: /** @type {const} */ ('deny_all'),
      };
    textId(org.revision);
    if (
      typeof org.sameOrganization !== 'boolean' ||
      !LAYERS.every((l) => ['allow', 'ask_each', 'deny_all'].includes(org[l]))
    )
      throw new TypeError('invalid organization policy');
    return org;
  }

  /** @param {import('./types.js').State} s @param {number} at @param {string} action @param {unknown} detail */
  function audit(s, at, action, detail) {
    s.audit.push({ at, action, policyRevision: s.policyRevision, detail });
  }

  /** @param {import('./types.js').State} s @param {number} at @param {import('./types.js').RecordEntry} row @param {import('./types.js').Status} status @param {string} reason */
  function transition(s, at, row, status, reason) {
    if (
      row.status === status &&
      row.localReason === reason &&
      row.decisionId &&
      row.policyRevision === s.policyRevision
    )
      return;
    row.status = status;
    row.localReason = reason;
    row.policyRevision = s.policyRevision;
    row.decisionId = randomUUID();
    // Audit retains history; the transport outbox must not replay a superseded
    // approval after a local revocation committed before reconnect.
    s.outbox = s.outbox.filter(
      (e) =>
        !(
          e.kind === 'decision' &&
          e.layer === row.request.layer &&
          e.requestId === row.request.requestId
        ),
    );
    s.outbox.push({
      id: row.decisionId,
      kind: 'decision',
      policyRevision: s.policyRevision,
      at,
      layer: row.request.layer,
      requestId: row.request.requestId,
      status: status === 'expired' || status === 'revoked' ? 'denied' : status,
    });
    audit(s, at, 'decision', { ...row });
  }

  /** @param {import('./types.js').State} s @param {number} at */
  function expire(s, at) {
    for (const row of Object.values(s.requests))
      if (
        row.status !== 'denied' &&
        row.status !== 'expired' &&
        row.deadline <= at
      )
        transition(s, at, row, 'expired', 'expired');
  }

  /** @param {import('./types.js').State} s @param {number} at @param {import('./types.js').Layer} layer @param {unknown} detail @param {(row: import('./types.js').RecordEntry) => boolean} [affected] */
  function changePolicy(s, at, layer, detail, affected = () => true) {
    if (!Number.isSafeInteger(s.policyRevision + 1))
      throw new Error('revision exhausted');
    s.policyRevision++;
    s.outbox = s.outbox.filter(
      (e) => !(e.kind === 'policy_changed' && e.layer === layer),
    );
    s.outbox.push({
      id: randomUUID(),
      kind: 'policy_changed',
      policyRevision: s.policyRevision,
      at,
      layer,
    });
    audit(s, at, 'policy_changed', { layer, change: detail });
    for (const row of Object.values(s.requests)) {
      if (
        row.request.layer === layer &&
        row.status === 'allowed' &&
        affected(row)
      ) {
        row.manual = false;
        transition(s, at, row, 'revoked', 'policy_changed');
      }
    }
  }

  /** @param {import('./types.js').State} s @param {import('./types.js').RecordEntry} row */
  function overQuota(s, row) {
    const pending = Object.values(s.requests).filter(
      (r) => r !== row && r.status === 'pending',
    );
    return (
      pending.length >= limits.maxPending ||
      pending.reduce((n, r) => n + r.request.bytes, row.request.bytes) >
        limits.maxPendingBytes ||
      pending.filter((r) => nodeKey(r.source) === nodeKey(row.source)).length >=
        1 ||
      pending.filter((r) => userKey(r.source) === userKey(row.source)).length >=
        3
    );
  }

  /** @param {import('./types.js').State} s @param {number} at @param {import('./types.js').Request} input */
  async function receive(s, at, input) {
    checkRequest(input);
    if (input.createdAt > at) throw new TypeError('future request');
    const key = requestKey(input);
    let row = s.requests[key];
    if (
      row &&
      (nodeKey(row.request.source) !== nodeKey(input.source) ||
        row.request.digest !== input.digest ||
        row.request.createdAt !== input.createdAt ||
        row.request.expiresAt !== input.expiresAt ||
        row.request.bytes !== input.bytes)
    )
      throw new Error('request conflict');
    const source = await trusted(input.source);
    const org = await currentOrganization(source);
    at = currentTime(s);
    expire(s, at);
    if (!row) {
      if (Object.keys(s.requests).length >= limits.maxRecords)
        throw new Error('consent capacity exhausted');
      row = {
        request: structuredClone(input),
        source,
        deadline: Math.min(input.expiresAt, input.createdAt + DAY_MS),
        status: 'pending',
        policyRevision: s.policyRevision,
        decisionId: '',
        localReason: '',
        manual: false,
        organizationRevision: org.revision,
      };
      s.requests[key] = row;
    }
    if (row.status === 'denied' || row.status === 'expired') return row;
    if (proofKey(row.source) !== proofKey(source)) row.manual = false;
    row.source = source;
    row.organizationRevision = org.revision;
    let outcome = evaluate(s, row, org, at);
    if (outcome.status === 'pending' && overQuota(s, row))
      outcome = { status: 'denied', reason: 'quota' };
    transition(s, at, row, outcome.status, outcome.reason);
    return row;
  }

  return {
    /** @param {import('./types.js').Request} input */
    request(input) {
      const copy = structuredClone(input);
      return transaction((s, at) => receive(s, at, copy));
    },
    /** @param {unknown} context */
    listPending(context) {
      return transaction(async (s, at) => {
        await local(context);
        for (const row of Object.values(s.requests))
          if (row.status === 'pending') await receive(s, at, row.request);
        return Object.values(s.requests).filter((r) => r.status === 'pending');
      });
    },
    /** @param {unknown} context */
    getSettings(context) {
      return transaction(async (s) => {
        await local(context);
        return {
          defaults: s.defaults,
          rules: s.rules,
          policyRevision: s.policyRevision,
        };
      });
    },
    /** @param {unknown} context @param {{layer: import('./types.js').Layer, requestId: string, choice: import('./types.js').Choice, sourceScope?: import('./types.js').SourceScope, expectedRevision: number}} input */
    decide(context, input) {
      const choice = { ...input };
      return transaction(async (s, at) => {
        await local(context);
        checkLayer(choice.layer);
        if (choice.expectedRevision !== s.policyRevision)
          throw new Error('policy revision conflict');
        if (!CHOICES.includes(choice.choice))
          throw new TypeError('invalid choice');
        const row =
          s.requests[JSON.stringify([choice.layer, choice.requestId])];
        if (!row) throw new Error('unknown request');
        if (row.status === 'denied' || row.status === 'expired')
          throw new Error('terminal request');
        const source = await trusted(row.request.source);
        if (proofKey(source) !== proofKey(row.source))
          throw new Error('source ownership conflict');
        const org = await currentOrganization(source);
        at = currentTime(s);
        if (row.deadline <= at) throw new Error('terminal request');
        if (choice.choice === 'allow_source' || choice.choice === 'block') {
          if (
            choice.sourceScope !== 'source_node' &&
            choice.sourceScope !== 'source_user'
          )
            throw new TypeError('explicit source scope required');
          changePolicy(s, at, choice.layer, choice, (entry) =>
            choice.sourceScope === 'source_user'
              ? userKey(entry.source) === userKey(source)
              : nodeKey(entry.source) === nodeKey(source),
          );
          const scope = choice.sourceScope;
          s.rules = s.rules.filter(
            (r) =>
              !(
                r.layer === choice.layer &&
                r.scope === scope &&
                matches(r, source)
              ),
          );
          s.rules.push({
            id: randomUUID(),
            layer: choice.layer,
            scope,
            source,
            choice: choice.choice,
            policyRevision: s.policyRevision,
          });
          audit(s, at, 'rule_saved', s.rules[s.rules.length - 1]);
        }
        row.manual =
          choice.choice === 'allow_once' || choice.choice === 'allow_source';
        row.policyRevision = s.policyRevision;
        const outcome = evaluate(s, row, org, at);
        if (choice.choice === 'deny' || choice.choice === 'block') {
          for (const key of cooldownKeys(choice.layer, source))
            s.cooldowns[key] = at + DAY_MS;
          transition(s, at, row, 'denied', choice.choice);
        } else transition(s, at, row, outcome.status, outcome.reason);
        audit(s, at, 'local_choice', choice);
        return row;
      });
    },
    /** @param {unknown} context @param {import('./types.js').Layer} layer @param {import('./types.js').DefaultMode} mode */
    setDefault(context, layer, mode) {
      return transaction(async (s, at) => {
        await local(context);
        checkLayer(layer);
        if (!MODES.includes(mode)) throw new TypeError('invalid default');
        s.defaults[layer] = mode;
        changePolicy(s, at, layer, { mode });
        return s.policyRevision;
      });
    },
    /** Remove a persistent allow/block, preserving explicit rejection cooldowns.
     * @param {unknown} context @param {{layer: import('./types.js').Layer, source: import('./types.js').Source, sourceScope: import('./types.js').SourceScope}|{layer: import('./types.js').Layer, ruleId: string}} input */
    revoke(context, input) {
      const copy = structuredClone(input);
      return transaction(async (s, at) => {
        await local(context);
        checkLayer(copy.layer);
        if ('ruleId' in copy) {
          textId(copy.ruleId);
          const rule = s.rules.find(
            (r) => r.id === copy.ruleId && r.layer === copy.layer,
          );
          if (!rule) return s.policyRevision;
          s.rules = s.rules.filter((r) => r.id !== copy.ruleId);
          changePolicy(s, at, copy.layer, copy, (entry) =>
            rule.scope === 'source_user'
              ? userKey(entry.source) === userKey(rule.source)
              : nodeKey(entry.source) === nodeKey(rule.source),
          );
          return s.policyRevision;
        }
        checkSource(copy.source);
        if (!['source_node', 'source_user'].includes(copy.sourceScope))
          throw new TypeError('invalid source scope');
        const source = await trusted(copy.source);
        s.rules = s.rules.filter(
          (r) =>
            !(
              r.layer === copy.layer &&
              r.scope === copy.sourceScope &&
              (r.scope === 'source_node'
                ? nodeKey(r.source) === nodeKey(source)
                : userKey(r.source) === userKey(source))
            ),
        );
        changePolicy(s, at, copy.layer, copy, (entry) =>
          copy.sourceScope === 'source_user'
            ? userKey(entry.source) === userKey(source)
            : nodeKey(entry.source) === nodeKey(source),
        );
        return s.policyRevision;
      });
    },
    /** @param {unknown} context */
    listOutbox(context) {
      return transaction(async (s) => {
        await local(context);
        return s.outbox;
      });
    },
    /** @param {unknown} context @param {string[]} ids */
    acknowledgeOutbox(context, ids) {
      const delivered = new Set(ids);
      return transaction(async (s) => {
        await local(context);
        s.outbox = s.outbox.filter((e) => !delivered.has(e.id));
      });
    },
    /** @param {unknown} context */
    listAudit(context) {
      return transaction(async (s) => {
        await local(context);
        return s.audit;
      });
    },
    /** Recheck under the same lock as revocation immediately before an action.
     * The callback must separately check host grants and online platform admission.
     * It must not reenter this service. Consent is not execution admission.
     * @template T @param {import('./types.js').Request} input @param {() => Promise<T>} action */
    async withAuthorization(input, action) {
      const copy = structuredClone(input);
      // Hold the serial queue across both durable recheck and action start.
      const run = tail.then(async () => {
        if (closed || poisoned) throw new Error('consent unavailable');
        const at = now();
        if (!Number.isSafeInteger(at) || at < state.lastNow)
          throw new Error('consent unavailable: clock rollback');
        const draft = structuredClone(state);
        draft.lastNow = at;
        expire(draft, at);
        const row = await receive(draft, at, copy);
        try {
          await handle.commit(draft);
        } catch (error) {
          poisoned = true;
          throw error;
        }
        state = draft;
        if (row.status !== 'allowed' || currentTime(draft) >= row.deadline)
          throw new Error('authorization required');
        return action();
      });
      tail = run.then(
        () => {},
        () => {},
      );
      return run;
    },
    async close() {
      closed = true;
      await tail;
      await handle.close();
    },
  };
}
