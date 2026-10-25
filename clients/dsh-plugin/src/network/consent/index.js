/* global structuredClone */
import { join } from 'node:path';
import { randomUUID, createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { bounded, opaqueResponse } from './deadline.js';
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
    version: 2,
    identity: { ...identity },
    policyRevision: 0,
    lastNow: 0,
    defaults: { conversation: 'ask_each', execution: 'ask_each' },
    rules: [],
    requests: {},
    cooldowns: {},
    audit: [],
    outbox: [],
    traffic: {},
    auditSummary: { dropped: 0, digest: '' },
  };
}

/** Validate persisted authority before using any grant.
 * @param {unknown} value @param {import('./types.js').Identity} identity
 * @returns {import('./types.js').State} */
function restore(value, identity) {
  const s = /** @type {import('./types.js').State} */ (value);
  if (
    !s ||
    ![1, 2].includes(s.version) ||
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
    row.receivedAt ??= row.request.createdAt;
    if (['denied', 'expired', 'revoked'].includes(row.status))
      row.terminalAt ??= s.lastNow;
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
        Math.min(row.request.expiresAt, row.receivedAt + DAY_MS) ||
      !Number.isSafeInteger(row.receivedAt) ||
      row.receivedAt < 0 ||
      row.receivedAt > s.lastNow ||
      (row.terminalAt !== undefined &&
        (!Number.isSafeInteger(row.terminalAt) ||
          row.terminalAt < row.receivedAt ||
          row.terminalAt > s.lastNow)) ||
      (row.usedAt !== undefined &&
        (!Number.isSafeInteger(row.usedAt) || row.usedAt < row.receivedAt))
    )
      throw new Error('consent unavailable: invalid request');
    textId(row.decisionId);
  }
  if (Object.values(s.cooldowns).some((n) => !Number.isSafeInteger(n) || n < 0))
    throw new Error('consent unavailable: invalid cooldown');
  s.version = 2;
  s.traffic ??= {};
  s.auditSummary ??= { dropped: 0, digest: '' };
  if (
    Array.isArray(s.traffic) ||
    typeof s.traffic !== 'object' ||
    Object.values(s.traffic).some(
      (bucket) =>
        !bucket ||
        !Number.isSafeInteger(bucket.since) ||
        bucket.since < 0 ||
        bucket.since > s.lastNow ||
        !Number.isSafeInteger(bucket.count) ||
        bucket.count < 0,
    ) ||
    !Number.isSafeInteger(s.auditSummary.dropped) ||
    s.auditSummary.dropped < 0 ||
    !/^(?:[a-f0-9]{64})?$/.test(s.auditSummary.digest)
  )
    throw new Error('consent unavailable: invalid retention state');
  for (const event of s.outbox) {
    textId(event.id);
    if (
      !['decision', 'policy_changed'].includes(event.kind) ||
      !Number.isSafeInteger(event.at) ||
      event.at < 0 ||
      event.at > s.lastNow ||
      !Number.isSafeInteger(event.policyRevision) ||
      event.policyRevision < 0 ||
      event.policyRevision > s.policyRevision
    )
      throw new Error('consent unavailable: invalid outbox');
    checkLayer(event.layer);
    if (event.kind === 'decision') {
      textId(event.requestId);
      if (!['pending', 'allowed', 'denied'].includes(event.status ?? ''))
        throw new Error('consent unavailable: invalid outbox status');
    }
  }
  for (const entry of s.audit) {
    textId(entry.action);
    if (
      !Number.isSafeInteger(entry.at) ||
      entry.at < 0 ||
      entry.at > s.lastNow ||
      !Number.isSafeInteger(entry.policyRevision) ||
      entry.policyRevision < 0 ||
      entry.policyRevision > s.policyRevision
    )
      throw new Error('consent unavailable: invalid audit');
  }
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
  callbackTimeoutMs = 5000,
  actionTimeoutMs = 10_000,
  responseDelayMs = 10_500,
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
    maxSourceRecords: 32,
    maxUserRecords: 64,
    maxSourceTerminal: 16,
    maxUserTerminal: 32,
    maxRequestsPerMinute: 5,
    terminalRetentionMs: DAY_MS,
    maxAudit: 256,
    ...overrides,
  };
  for (const limit of [
    ...Object.values(limits),
    callbackTimeoutMs,
    actionTimeoutMs,
    responseDelayMs,
  ])
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

  /** @template T @param {(draft: import('./types.js').State, at: number) => Promise<T>|T} operation @param {(result: T) => Promise<unknown>} [afterCommit] @returns {Promise<T>} */
  function transaction(operation, afterCommit) {
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
      maintain(draft, at);
      const result = await operation(draft, at);
      expire(draft, currentTime(draft));
      maintain(draft, draft.lastNow);
      try {
        await handle.commit(draft);
      } catch (error) {
        poisoned = true;
        throw error;
      }
      state = draft;
      if (afterCommit) await afterCommit(result);
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
    if (
      (await bounded(() => authorizeLocal(context), callbackTimeoutMs)) !== true
    )
      throw new Error('authenticated local UI required');
  }

  /** @param {import('./types.js').Source} source */
  async function trusted(source) {
    if (source.tenant !== identity.tenant || source.realm !== identity.realm)
      throw new TypeError('invalid source realm/tenant');
    const verified = await bounded(
      () => resolveSource({ ...source }),
      callbackTimeoutMs,
    );
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
    const org = await bounded(
      () => organization({ ...source }),
      callbackTimeoutMs,
    );
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
    trimAudit(s);
  }

  /** @param {import('./types.js').State} s */
  function trimAudit(s) {
    while (s.audit.length > limits.maxAudit) {
      const entry = s.audit.shift();
      s.auditSummary.digest = createHash('sha256')
        .update(s.auditSummary.digest)
        .update(JSON.stringify(entry))
        .digest('hex');
      s.auditSummary.dropped++;
    }
  }

  /** @param {import('./types.js').RecordEntry} row */
  const terminal = (row) =>
    ['denied', 'expired', 'revoked'].includes(row.status) ||
    row.usedAt !== undefined;

  /** @param {import('./types.js').State} s @param {number} at */
  function maintain(s, at) {
    for (const [key, row] of Object.entries(s.requests)) {
      if (
        terminal(row) &&
        at >=
          Math.max(
            row.deadline,
            (row.terminalAt ?? row.usedAt ?? row.deadline) +
              limits.terminalRetentionMs,
          )
      ) {
        delete s.requests[key];
        s.outbox = s.outbox.filter(
          (e) =>
            !(
              e.kind === 'decision' &&
              e.layer === row.request.layer &&
              e.requestId === row.request.requestId
            ),
        );
      }
    }
    for (const [key, traffic] of Object.entries(s.traffic))
      if (at - traffic.since >= 60_000) delete s.traffic[key];
    trimAudit(s);
  }

  /** @param {import('./types.js').State} s @param {import('./types.js').VerifiedSource} source @param {number} at */
  function sourceLimited(s, source, at) {
    const rows = Object.values(s.requests);
    const nodes = rows.filter((r) => nodeKey(r.source) === nodeKey(source));
    const users = rows.filter((r) => userKey(r.source) === userKey(source));
    if (
      nodes.length >= limits.maxSourceRecords ||
      users.length >= limits.maxUserRecords ||
      nodes.filter(terminal).length >= limits.maxSourceTerminal ||
      users.filter(terminal).length >= limits.maxUserTerminal
    )
      return true;
    const keys = [nodeKey(source), userKey(source)];
    if (
      keys.some(
        (key) => (s.traffic[key]?.count ?? 0) >= limits.maxRequestsPerMinute,
      )
    )
      return true;
    // Source/user aggregation prevents rotating node IDs from growing this map.
    if (
      Object.keys(s.traffic).length + keys.filter((k) => !s.traffic[k]).length >
      limits.maxRecords * 2
    )
      return true;
    for (const key of keys) {
      const bucket = (s.traffic[key] ??= { since: at, count: 0 });
      bucket.count++;
    }
    return false;
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
    if (status === 'denied' || status === 'expired' || status === 'revoked')
      row.terminalAt ??= status === 'expired' ? row.deadline : at;
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
    maintain(s, at);
    row = s.requests[key];
    if (!row) {
      row = {
        request: structuredClone(input),
        source,
        receivedAt: at,
        deadline: Math.min(input.expiresAt, at + DAY_MS),
        status: 'pending',
        policyRevision: s.policyRevision,
        decisionId: '',
        localReason: '',
        manual: false,
        organizationRevision: org.revision,
      };
      if (input.expiresAt <= at || input.expiresAt > at + DAY_MS) {
        row.status = 'expired';
        row.localReason = 'invalid_deadline';
        return row;
      }
      if (
        sourceLimited(s, source, at) ||
        Object.keys(s.requests).length >= limits.maxRecords
      ) {
        row.status = 'denied';
        row.localReason = 'source_capacity';
        return row;
      }
      s.requests[key] = row;
    }
    if (
      row.status === 'denied' ||
      row.status === 'expired' ||
      row.usedAt !== undefined
    )
      return row;
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
    async request(input) {
      const copy = structuredClone(input);
      checkRequest(copy);
      return opaqueResponse(
        transaction((s, at) => receive(s, at, copy)),
        responseDelayMs,
      );
    },
    /** Private UI lookup, never return this row to the remote source.
     * @param {unknown} context @param {{layer: import('./types.js').Layer, requestId: string}} input */
    getRequest(context, input) {
      const copy = { ...input };
      return transaction(async (s) => {
        await local(context);
        checkLayer(copy.layer);
        textId(copy.requestId);
        return s.requests[JSON.stringify([copy.layer, copy.requestId])] ?? null;
      });
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
        if (
          (choice.choice === 'allow_once' ||
            choice.choice === 'allow_source') &&
          cooldownKeys(choice.layer, row.source).some(
            (key) => (s.cooldowns[key] ?? 0) > at,
          )
        )
          throw new Error('cooldown active: wait before allowing');
        if (
          row.status === 'denied' ||
          row.status === 'expired' ||
          row.usedAt !== undefined
        )
          throw new Error('terminal request');
        const source = await trusted(row.request.source);
        if (proofKey(source) !== proofKey(row.source))
          throw new Error('source ownership conflict');
        const org = await currentOrganization(source);
        at = currentTime(s);
        if (row.deadline <= at) throw new Error('terminal request');
        if (
          (choice.choice === 'allow_once' ||
            choice.choice === 'allow_source') &&
          cooldownKeys(choice.layer, source).some(
            (key) => (s.cooldowns[key] ?? 0) > at,
          )
        )
          throw new Error(
            'cooldown active: wait or explicitly clear cooldown before allowing',
          );
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
    /** @param {unknown} context */
    getAuditSummary(context) {
      return transaction(async (s) => {
        await local(context);
        return s.auditSummary;
      });
    },
    /** Recheck under the same lock as revocation immediately before an action.
     * The callback must separately check host grants and online platform admission.
     * It must not reenter this service. Consent is not execution admission.
     * @template T @param {import('./types.js').Request} input @param {(signal: AbortSignal) => Promise<T>} action */
    async withAuthorization(input, action) {
      const copy = structuredClone(input);
      checkRequest(copy);
      const until = performance.now() + responseDelayMs;
      const work = transaction(
        async (s, at) => {
          const row = await receive(s, at, copy);
          const execute =
            row.status === 'allowed' &&
            row.usedAt === undefined &&
            currentTime(s) < row.deadline &&
            performance.now() < until;
          if (execute) {
            row.usedAt = currentTime(s);
            audit(s, row.usedAt, 'authorization_consumed', {
              layer: copy.layer,
              requestId: copy.requestId,
            });
          }
          return { row, execute };
        },
        async ({ row, execute }) => {
          if (
            !execute ||
            row.status !== 'allowed' ||
            now() >= row.deadline ||
            performance.now() >= until
          )
            throw new Error('authorization required');
          await bounded(
            action,
            Math.min(actionTimeoutMs, Math.max(1, until - performance.now())),
          );
        },
      );
      return opaqueResponse(work, responseDelayMs, true);
    },
    async close() {
      closed = true;
      await tail;
      await handle.close();
    },
  };
}
