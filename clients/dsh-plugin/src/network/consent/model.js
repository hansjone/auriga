import { isUint64String } from '../host/address.js';

export const DAY_MS = 86_400_000;
export const LAYERS = /** @type {const} */ (['conversation', 'execution']);
export const MODES = ['ask_each', 'same_org', 'allowlist', 'deny_all'];
export const CHOICES = ['allow_once', 'allow_source', 'deny', 'block'];

/** @param {unknown} value @returns {asserts value is string} */
export function textId(value) {
  if (typeof value !== 'string' || !value || value.length > 256)
    throw new TypeError('invalid identifier');
}

/** @param {unknown} value @returns {asserts value is import('./types.js').Layer} */
export function checkLayer(value) {
  if (value !== 'conversation' && value !== 'execution')
    throw new TypeError('invalid layer');
}

/** @param {import('./types.js').Source} source */
export function checkSource(source) {
  if (!source || Object.keys(source).sort().join(',') !== 'node,realm,tenant')
    throw new TypeError('invalid source');
  textId(source.tenant);
  if (
    !isUint64String(source.realm) ||
    source.realm === '0' ||
    !isUint64String(source.node) ||
    source.node === '0'
  )
    throw new TypeError('invalid source');
}

/** @param {import('./types.js').Request} r */
export function checkRequest(r) {
  if (
    !r ||
    Object.keys(r).sort().join(',') !==
      'bytes,createdAt,digest,expiresAt,layer,requestId,source'
  )
    throw new TypeError('request metadata only');
  checkLayer(r.layer);
  textId(r.requestId);
  checkSource(r.source);
  if (
    !/^[a-f0-9]{64}$/.test(r.digest) ||
    !Number.isSafeInteger(r.bytes) ||
    r.bytes < 0 ||
    !Number.isSafeInteger(r.createdAt) ||
    r.createdAt < 0 ||
    !Number.isSafeInteger(r.expiresAt) ||
    r.expiresAt <= r.createdAt
  )
    throw new TypeError('invalid request metadata');
}

/** @param {import('./types.js').Source} s */
export const nodeKey = (s) => JSON.stringify([s.tenant, s.realm, s.node]);
/** @param {import('./types.js').VerifiedSource} s */
export const userKey = (s) => JSON.stringify([s.tenant, s.userId]);
/** @param {import('./types.js').Request} r */
export const requestKey = (r) => JSON.stringify([r.layer, r.requestId]);
/** @param {import('./types.js').VerifiedSource} s */
export const proofKey = (s) =>
  JSON.stringify([nodeKey(s), s.userId, s.ownershipRevision]);
/** @param {import('./types.js').Layer} layer @param {import('./types.js').VerifiedSource} s */
export const cooldownKeys = (layer, s) =>
  [nodeKey(s), userKey(s)].map((key) => JSON.stringify([layer, key]));

/** @param {import('./types.js').Rule} rule @param {import('./types.js').VerifiedSource} s */
export function matches(rule, s) {
  return rule.scope === 'source_user'
    ? userKey(rule.source) === userKey(s)
    : rule.choice === 'block'
      ? nodeKey(rule.source) === nodeKey(s)
      : proofKey(rule.source) === proofKey(s);
}

/** Deterministic intersection: organization cannot turn a local wait/deny into allow.
 * @param {import('./types.js').State} state
 * @param {import('./types.js').RecordEntry} row
 * @param {import('./types.js').Organization} org
 * @param {number} at
 * @returns {{status: import('./types.js').Status, reason: string}}
 */
export function evaluate(state, row, org, at) {
  const layer = row.request.layer;
  const rules = state.rules.filter(
    (r) => r.layer === layer && matches(r, row.source),
  );
  if (row.deadline <= at) return { status: 'expired', reason: 'expired' };
  if (!org.available || org[layer] === 'deny_all')
    return { status: 'denied', reason: 'organization' };
  if (rules.some((r) => r.choice === 'block'))
    return { status: 'denied', reason: 'block' };
  if (state.defaults[layer] === 'deny_all')
    return { status: 'denied', reason: 'deny_all' };
  if (
    cooldownKeys(layer, row.source).some((k) => (state.cooldowns[k] ?? 0) > at)
  )
    return { status: 'denied', reason: 'cooldown' };
  if (row.manual) return { status: 'allowed', reason: 'allow_once' };
  if (
    org[layer] !== 'ask_each' &&
    (rules.some((r) => r.choice === 'allow_source') ||
      (state.defaults[layer] === 'same_org' && org.sameOrganization))
  )
    return { status: 'allowed', reason: 'automatic' };
  return { status: 'pending', reason: 'ask_each' };
}

/** Uniform privacy projection, intentionally no timestamps, IDs or reasons.
 * It is not a durable platform submission or HostAccepted receipt.
 * @param {{status: import('./types.js').Status}} result
 * @param {boolean} canViewStatus must be determined by platform authorization
 */
export function publicReceipt(result, canViewStatus) {
  if (canViewStatus !== true) return { status: 'request_submitted' };
  if (
    result.status === 'denied' ||
    result.status === 'expired' ||
    result.status === 'revoked'
  )
    return { status: 'target_unavailable' };
  return {
    status:
      result.status === 'pending' || result.status === 'allowed'
        ? result.status
        : 'target_unavailable',
  };
}
