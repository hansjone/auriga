import { logEvent, newTraceparent } from '../observability/index.js';
import { getSystemErrorMap } from 'node:util';

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
  'trust_roots_invalid',
  'signature_invalid',
  'platform_unreachable',
  'storage_unavailable',
  'host_busy',
  'state_conflict',
  'runtime_unavailable',
  'installer_unavailable',
  'unauthenticated',
  'forbidden',
  'validation_failed',
  'not_found',
  'rate_limited',
  'apply_failed',
  'plugin_install_network',
  'plugin_install_timeout',
  'plugin_install_dependency',
  'plugin_install_disk_full',
  'plugin_install_permission',
  'plugin_install_build_blocked',
  'plugin_install_incompatible',
  'plugin_install_cancelled',
  'plugin_not_active',
  'internal_error',
]);
const nativeInstallReasons = new Map([
  ['network', 'plugin_install_network'],
  ['timeout', 'plugin_install_timeout'],
  ['not-found', 'plugin_install_dependency'],
  ['no-matching-version', 'plugin_install_dependency'],
  ['disk-full', 'plugin_install_disk_full'],
  ['permission', 'plugin_install_permission'],
  ['build-blocked', 'plugin_install_build_blocked'],
  ['integrity', 'signature_invalid'],
  ['pnpm-missing', 'installer_unavailable'],
]);

/** Classify the public DSH result without copying pnpm output, paths or URLs.
 * @param {any} outcome @param {string} phase @param {string} [requestId] */
export function nativePluginFailure(outcome, phase, requestId) {
  const reason =
    outcome?.application === 'cancelled'
      ? 'plugin_install_cancelled'
      : outcome?.error?.code === 'incompatible-version'
        ? 'plugin_install_incompatible'
        : (nativeInstallReasons.get(outcome?.packageResult?.kind) ??
          'apply_failed');
  return Object.assign(
    resourceFailure('resource.apply_failed', reason, 'plugin'),
    {
      nativePluginOperation: true,
      phase,
      traceId: requestId?.replaceAll('-', ''),
    },
  );
}

/** @param {any} value */
export function validPluginDiagnostic(value) {
  return (
    !!value &&
    reasons.has(value.reason) &&
    /^[a-f0-9]{32}$/.test(value.trace_id ?? '')
  );
}
const stages = new Set([
  'catalog',
  'policy_cache',
  'plugin',
  'storage',
  'trust_roots',
  'download',
  'verification',
  'platform',
  'runtime',
  'apply',
]);
const errorNames = new Set([
  'Error',
  'TypeError',
  'RangeError',
  'ReferenceError',
  'SyntaxError',
  'AggregateError',
  'AbortError',
  'TimeoutError',
]);
const systemCodes = new Set(
  Array.from(getSystemErrorMap().values(), ([code]) => code),
);
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
  const hostBusy = error?.hostLockOccupied === true;
  stage = stages.has(error?.stage)
    ? error.stage
    : stages.has(stage)
      ? stage
      : 'catalog';
  const code = hostBusy
    ? 'common.conflict'
    : stage === 'policy_cache'
      ? 'resource.storage_unavailable'
      : Object.hasOwn(statuses, error?.code)
        ? error.code
        : 'common.unavailable';
  const suppliedReason = hostBusy ? 'host_busy' : error?.reason;
  const reason = reasons.has(suppliedReason)
    ? suppliedReason
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
  const trace_id = /^[a-f0-9]{32}$/.test(error?.traceId ?? '')
    ? error.traceId
    : newTraceparent().slice(3, 35);
  const body = { code, reason, stage, trace_id };
  // Only finite metadata: exception messages/stacks may contain resource content or credentials.
  log('WARN', 'Resource operation failed', {
    ...body,
    ...(errorNames.has(error?.name) ? { error_type: error.name } : {}),
    ...(systemCodes.has(error?.code) ? { error_code: error.code } : {}),
  });
  return {
    status: statuses[/** @type {keyof typeof statuses} */ (code)],
    body,
  };
}
