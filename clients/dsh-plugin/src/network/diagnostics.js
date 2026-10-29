import { performance } from 'node:perf_hooks';

// This is a logging protocol, not a new status/observer surface. Never serialize
// an exception, request, identity or caller-supplied diagnostic object.
export const DIAGNOSTIC_PHASES = Object.freeze([
  'identity_prepare',
  'runtime_open',
  'ledger_open',
  'register_http',
  'register_validate',
  'ledger_restore',
  'trust_open',
  'trust_load',
  'trust_refresh',
  'keys_announcement_http',
  'keys_announcement_validate',
  'keys_http',
  'keys_validate',
  'ticket_http',
  'ticket_validate',
  'wss_connect',
  'bootstrap_ready',
  'normal_ready',
  'initial_commit',
  'durable_commit',
  'connection_receive',
  'running',
]);
export const DIAGNOSTIC_CODES = Object.freeze([
  'unknown',
  'ledger_missing',
  'ledger_locked',
  'control_unavailable',
  'response_invalid',
  'response_too_large',
  'ready_timeout',
  'http_error',
  'host.ledger_unavailable',
  'network.key_unavailable',
  'network.revoked',
  'network.protocol_error',
  'network.recovery_conflict',
  'network.unauthorized',
  'ticket_invalid',
  'ticket_expired',
  'unauthorized',
  'socket_incapable',
  'connection_lost',
  'connection_closed',
  'not_connected',
  'invalid_state',
  'closed',
  'timeout',
  'ready_failed',
  'stopped',
  'protocol_error',
  'socket_overflow',
  'subscription_error',
]);
export const DIAGNOSTIC_ERRNOS = Object.freeze([
  'EACCES',
  'EPERM',
  'EBUSY',
  'ENOSPC',
  'EIO',
  'EROFS',
  'EMFILE',
  'ENFILE',
  'ENOENT',
  'ECONNREFUSED',
  'ECONNRESET',
  'ETIMEDOUT',
  'ENOTFOUND',
  'EAI_AGAIN',
  'CERT_HAS_EXPIRED',
  'DEPTH_ZERO_SELF_SIGNED_CERT',
  'UNABLE_TO_VERIFY_LEAF_SIGNATURE',
  'ERR_TLS_CERT_ALTNAME_INVALID',
]);
/** @type {WeakMap<object, string>} */
const reasons = new WeakMap();
/** @type {WeakMap<object, string>} */
const errnos = new WeakMap();
/** @type {WeakMap<object, {phase:string,budget:number,start:number}>} */
const stages = new WeakMap();
/** Internal throw-site annotation; public error identity/code/cause stay intact.
 * @template T @param {T} error @param {string} code @returns {T} */
export function diagnosticReason(error, code) {
  if (error && typeof error === 'object' && DIAGNOSTIC_CODES.includes(code))
    reasons.set(error, code);
  return error;
}
/** @param {unknown} error @param {string} key */
function field(error, key) {
  try {
    return /** @type {any} */ (error)?.[key];
  } catch {
    return undefined;
  }
}
/** Socket errors must be classified before the SDK discards their code.
 * @param {unknown} event @returns {string} */
export function socketDiagnosticErrno(event) {
  const code = field(field(event, 'error'), 'code');
  return DIAGNOSTIC_ERRNOS.includes(code) ? code : 'none';
}
/** @template T @param {T} error @param {string} code @returns {T} */
export function diagnosticErrno(error, code) {
  if (error && typeof error === 'object' && DIAGNOSTIC_ERRNOS.includes(code))
    errnos.set(error, code);
  return error;
}
/** @param {number} value */
const integer = (value) =>
  Number.isSafeInteger(value) && value >= 0 ? Math.min(value, 2147483647) : 0;

/** One gate per login generation. Repeated identical failures are silent; up to
 * 24 distinct failure signatures and two milestones, regardless of retry count.
 * @param {any} ctx @param {number} generation */
export function createDiagnosticLog(ctx, generation = 0) {
  const seen = new Set();
  let failures = 0;
  /** @param {Record<string, any>} event */
  return (event) => {
    const signature = [
      event.event,
      event.phase,
      event.code,
      event.http_status,
      event.errno,
    ].join('|');
    if (seen.has(signature) || (event.event === 'failure' && failures >= 24))
      return;
    try {
      // A single string works with Cordis's normal console sink. No exception or
      // metadata argument can be inherited by a formatter. Logger absence/failure
      // must never become a business failure (including throwing context proxies).
      const logger = ctx?.logger;
      if (typeof logger?.warn !== 'function') return;
      logger.warn(
        'auriga_network_diagnostic ' +
          JSON.stringify({
            v: 1,
            ...event,
            generation: integer(generation),
          }),
      );
      seen.add(signature);
      if (event.event === 'failure') failures++;
    } catch {
      /* Diagnostics are best effort; original control flow wins. */
    }
  };
}

/** @param {{emit:ReturnType<typeof createDiagnosticLog>,valid:()=>boolean,attempt?:number,signal?:AbortSignal}} options */
export function createAttemptDiagnostics({ emit, valid, attempt = 0, signal }) {
  let failed = false;
  /** @type {boolean|null} */ let created = null;
  /** @param {string} phase @param {number} [budget] @param {()=>boolean} [current] */
  function scope(phase, budget = 0, current = () => true) {
    const start = performance.now();
    /** @param {unknown} error */
    function fail(error) {
      if (failed || !valid() || !current()) return;
      failed = true;
      const status = field(error, 'status'),
        rawCode = field(error, 'code');
      const httpStatus =
        Number.isInteger(status) && status >= 400 && status <= 599 ? status : 0;
      const internal =
        error && typeof error === 'object' ? reasons.get(error) : undefined;
      const stage =
        error && typeof error === 'object' ? stages.get(error) : undefined;
      const at = stage?.phase ?? phase;
      emit({
        event: 'failure',
        phase: DIAGNOSTIC_PHASES.includes(at) ? at : 'runtime_open',
        code:
          internal ??
          (DIAGNOSTIC_CODES.includes(rawCode)
            ? rawCode
            : httpStatus
              ? 'http_error'
              : 'unknown'),
        http_status: httpStatus,
        errno:
          (error && typeof error === 'object'
            ? errnos.get(error)
            : undefined) ??
          (DIAGNOSTIC_ERRNOS.includes(rawCode) ? rawCode : 'none'),
        elapsed_ms: integer(
          Math.floor(performance.now() - (stage?.start ?? start)),
        ),
        budget_ms: integer(stage?.budget ?? budget),
        attempt: integer(attempt),
        cancelled: signal?.aborted === true,
        created,
      });
    }
    return {
      fail,
      /** @template T @param {()=>T} operation @returns {T} */
      sync(operation) {
        try {
          return operation();
        } catch (error) {
          fail(error);
          throw error;
        }
      },
      /** @template T @param {()=>T|Promise<T>} operation @returns {Promise<T>} */
      async run(operation) {
        try {
          return await operation();
        } catch (error) {
          fail(error);
          throw error;
        }
      },
      /** Annotate provider failures, but let the owning current connection emit.
       * @template T @param {()=>T|Promise<T>} operation @returns {Promise<T>} */
      async deferred(operation) {
        try {
          return await operation();
        } catch (error) {
          if (error && typeof error === 'object' && !stages.has(error))
            stages.set(error, { phase, budget, start });
          throw error;
        }
      },
    };
  }
  return {
    scope,
    /** Only a successfully validated register result may set this flag.
     * @param {boolean} value */
    registered(value) {
      if (valid() && typeof value === 'boolean') created = value;
    },
    /** @param {'ledger_restored'|'ready'} event */
    milestone(event) {
      if (!valid()) return;
      emit({
        event,
        phase: event === 'ready' ? 'running' : 'ledger_restore',
        code: 'unknown',
        http_status: 0,
        errno: 'none',
        elapsed_ms: 0,
        budget_ms: 0,
        attempt: integer(attempt),
        cancelled: false,
        created,
      });
    },
  };
}
