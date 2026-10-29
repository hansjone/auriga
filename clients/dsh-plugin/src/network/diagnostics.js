import { performance } from 'node:perf_hooks';
import { writeSync } from 'node:fs';

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

const LOGGER_NAME = 'auriga-network-diagnostic';
const PREFIX = 'auriga_network_diagnostic ';
const SINK = Symbol('auriga network diagnostic sink');
/** The context inherits only an opaque token, never the writer/permit itself.
 * @type {WeakMap<object, {readonly active:boolean,send:(line:string)=>boolean}>} */
const sinks = new WeakMap();
const EVENT_KEYS = [
  'v',
  'event',
  'phase',
  'code',
  'http_status',
  'errno',
  'elapsed_ms',
  'budget_ms',
  'generation',
  'attempt',
  'cancelled',
  'created',
];
/** @param {Record<string, any>} event */
function encodeEvent(event) {
  if (
    Object.keys(event).length !== EVENT_KEYS.length ||
    !EVENT_KEYS.every((key) => Object.hasOwn(event, key)) ||
    event.v !== 1 ||
    !['failure', 'ledger_restored', 'ready'].includes(event.event) ||
    !DIAGNOSTIC_PHASES.includes(event.phase) ||
    !DIAGNOSTIC_CODES.includes(event.code) ||
    !(event.errno === 'none' || DIAGNOSTIC_ERRNOS.includes(event.errno)) ||
    !(
      Number.isInteger(event.http_status) &&
      (event.http_status === 0 ||
        (event.http_status >= 400 && event.http_status <= 599))
    ) ||
    !['elapsed_ms', 'budget_ms', 'generation', 'attempt'].every(
      (key) =>
        Number.isInteger(event[key]) &&
        event[key] >= 0 &&
        event[key] <= 2147483647,
    ) ||
    typeof event.cancelled !== 'boolean' ||
    !(event.created === null || typeof event.created === 'boolean')
  )
    return;
  const line = PREFIX + JSON.stringify(event);
  return Buffer.byteLength(line, 'utf8') <= 2048 ? line : undefined;
}

/** Install once on the plugin context. Descendants inherit its private symbol;
 * sibling plugin instances own separate permits and never duplicate each other.
 * Cordis's default exporter only buffers, and its default level excludes warn.
 * @param {any} ctx */
export function installNetworkDiagnosticSink(ctx) {
  /** @type {undefined|(()=>unknown)} */ let remove;
  /** @type {undefined|{line:string,delivered:boolean}} */ let pending;
  let active = true;
  try {
    if (sinks.get(ctx?.[SINK])?.active) return;
    const service = ctx?.logger;
    if (
      typeof service !== 'function' ||
      typeof service.exporter !== 'function' ||
      typeof ctx.effect !== 'function'
    )
      return;
    const logger = service(LOGGER_NAME);
    const sink = {
      get active() {
        return active;
      },
      /** The permit exists only during this exact producer call. */
      send(/** @type {string} */ line) {
        if (!active || pending) return false;
        ctx.fiber?.assertActive?.();
        const permit = { line, delivered: false };
        pending = permit;
        try {
          logger.warn(line);
        } finally {
          if (pending === permit) pending = undefined;
        }
        return permit.delivered;
      },
    };
    remove = service.exporter({
      levels: { default: -1, [LOGGER_NAME]: 2 },
      export(/** @type {any} */ message) {
        try {
          if (
            !active ||
            !pending ||
            message.name !== LOGGER_NAME ||
            message.type !== 'warn' ||
            !Array.isArray(message.args) ||
            message.args.length !== 1 ||
            message.args[0] !== pending.line
          )
            return;
          const permit = pending;
          pending = undefined; // One use: synchronous replay cannot duplicate it.
          // A bounded synchronous fd write also catches closed-pipe failures;
          // no global stderr listener, formatter, metadata or raw log fallback.
          const line = permit.line + '\n';
          permit.delivered =
            writeSync(2, line) === Buffer.byteLength(line, 'utf8');
        } catch {
          /* A diagnostic sink cannot change network control flow. */
        }
      },
    });
    ctx.effect(
      () => () => {
        active = false;
        pending = undefined;
      },
      'auriga-dsh: network diagnostic sink',
    );
    const token = Object.freeze({});
    sinks.set(token, sink);
    Object.defineProperty(ctx, SINK, { value: token, configurable: true });
  } catch {
    active = false;
    pending = undefined;
    try {
      Promise.resolve(remove?.()).catch(() => {});
    } catch {
      /* No fallback to raw logs. */
    }
  }
}

/** One gate per login generation. Repeated identical failures are silent; up to
 * 24 distinct failure signatures and two milestones, regardless of retry count.
 * @param {any} ctx @param {number} generation */
export function createDiagnosticLog(ctx, generation = 0) {
  const seen = new Set();
  let failures = 0;
  // Capture the owning installation. An old emitter cannot use a newly installed
  // sink after its plugin has been disposed/reloaded.
  /** @type {any} */ let sink;
  try {
    sink = sinks.get(ctx?.[SINK]);
  } catch {
    /* Non-Cordis library consumers remain supported. */
  }
  /** @param {Record<string, any>} event */
  return (event) => {
    try {
      const line = encodeEvent({
        v: 1,
        ...event,
        generation: integer(generation),
      });
      if (!line) return;
      const signature = [
        event.event,
        event.phase,
        event.code,
        event.http_status,
        event.errno,
      ].join('|');
      if (seen.has(signature) || (event.event === 'failure' && failures >= 24))
        return;
      if (sink) {
        if (!sink.active) return;
        ctx.fiber?.assertActive?.();
        if (!sink.send(line)) return;
      } else {
        // Library consumers may supply their own logger. This path promises no
        // stdio sink; the actual plugin installs the public Cordis exporter above.
        const logger = ctx?.logger;
        if (typeof logger?.warn !== 'function') return;
        logger.warn(line);
      }
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
