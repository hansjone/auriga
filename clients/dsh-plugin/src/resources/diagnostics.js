import { logEvent, newTraceparent } from '../observability/index.js';

const statuses = {
  'common.unauthenticated': 401,
  'common.forbidden': 403,
  'common.validation_failed': 400,
  'common.not_found': 404,
  'common.conflict': 409,
  'common.precondition_failed': 409,
  'common.rate_limited': 429,
  'common.unavailable': 503,
  'resource.signature_invalid': 422,
  'resource.digest_mismatch': 422,
  'resource.level_locked': 403,
  'resource.state_conflict': 409,
  'resource.storage_unavailable': 503,
  'resource.apply_failed': 503,
};
const reasons = new Set([
  'guardian_missing',
  'trust_roots_missing',
  'signature_invalid',
  'platform_unreachable',
  'storage_unavailable',
  'state_conflict',
  'runtime_unavailable',
  'installer_unavailable',
  'unauthenticated',
  'forbidden',
  'validation_failed',
  'not_found',
  'rate_limited',
  'apply_failed',
  'internal_error',
]);
const stages = new Set([
  'catalog',
  'plugin',
  'storage',
  'trust_roots',
  'download',
  'verification',
  'platform',
  'runtime',
  'apply',
]);
/** Local diagnostic reasons supplement existing contract error codes; no raw exception is returned.
 * @param {string} code @param {string} reason @param {string} stage */
export function resourceFailure(code, reason, stage) {
  return Object.assign(new Error(code), { code, reason, stage });
}
/** @template T @param {string} stage @param {()=>Promise<T>} work */
export async function resourceStage(stage, work) {
  try {
    return await work();
  } catch (error) {
    const e = /** @type {any} */ (
      error instanceof Error ? error : new Error('resource operation failed')
    );
    if (!e.stage) e.stage = stage;
    // The strict verifier deliberately throws fixed rule names rather than HTTP errors.
    if (stage === 'verification' && !e.code)
      e.code = 'resource.signature_invalid';
    throw e;
  }
}
/** @param {any} error @param {string} stage @param {typeof logEvent} [log] */
export function diagnoseResource(error, stage, log = logEvent) {
  const code = Object.hasOwn(statuses, error?.code)
    ? error.code
    : 'common.unavailable';
  stage = stages.has(error?.stage)
    ? error.stage
    : stages.has(stage)
      ? stage
      : 'catalog';
  const reason = reasons.has(error?.reason)
    ? error.reason
    : stage === 'trust_roots'
      ? 'trust_roots_missing'
      : ['resource.signature_invalid', 'resource.digest_mismatch'].includes(
            code,
          )
        ? 'signature_invalid'
        : [
              'common.conflict',
              'common.precondition_failed',
              'resource.state_conflict',
            ].includes(code)
          ? 'state_conflict'
          : code === 'resource.storage_unavailable'
            ? 'storage_unavailable'
            : code === 'resource.apply_failed'
              ? 'apply_failed'
              : code === 'resource.level_locked'
                ? 'forbidden'
                : code !== 'common.unavailable'
                  ? code.slice('common.'.length)
                  : ['platform', 'download'].includes(stage)
                    ? 'platform_unreachable'
                    : 'internal_error';
  const trace_id = newTraceparent().slice(3, 35);
  const body = { code, reason, stage, trace_id };
  // Only finite metadata: exception messages/stacks may contain resource content or credentials.
  log('WARN', 'Resource operation failed', body);
  return {
    status: statuses[/** @type {keyof typeof statuses} */ (code)],
    body,
  };
}
