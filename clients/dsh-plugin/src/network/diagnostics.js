import { performance } from 'node:perf_hooks';
import { spawn } from 'node:child_process';

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
// fs.write is asynchronous, but a full pipe can retain its libuv request until
// process exit. Isolate that request: this process has its own hard lifetime and
// exits on IPC disconnect even while its one filesystem write is outstanding.
const WRITER = `
const { write } = require('node:fs');
// process.exit can wait for a blocked filesystem worker during native teardown.
// The private writer has no durable state: terminate it at the OS boundary.
const stop = () => process.kill(process.pid, 'SIGKILL');
process.on('disconnect', stop);
process.on('error', stop);
process.on('uncaughtException', stop);
setTimeout(stop, 2000);
let busy = false;
process.on('message', (line) => {
  if (busy || typeof line !== 'string' || Buffer.byteLength(line) > 2049) return stop();
  busy = true;
  const bytes = Buffer.from(line);
  function part(offset) {
    write(2, bytes, offset, bytes.length - offset, null, (error, count) => {
      if (error || !count) return stop();
      const next = offset + count;
      if (next < bytes.length) return part(next);
      busy = false;
      process.send(1, (error) => { if (error) stop(); });
    });
  }
  try { part(0); } catch { stop(); }
});
`;
/** @typedef {{line:string,owner:any,live:()=>boolean}} Output */
/** At most 32 records including the single in-flight record: 65568 bytes. */
/** @type {Output[]} */ let outputQueue = [];
/** @type {undefined|{child:import('node:child_process').ChildProcess,current?:Output,stopping:boolean,idle?:ReturnType<typeof setTimeout>,kill:()=>void}} */
let writer;
/** @type {ReturnType<typeof setTimeout>|undefined} */ let writerWake;
let nextWriterAt = 0;
const outputOwners = new WeakSet();
function stopWriter() {
  if (!writer || writer.stopping) return;
  writer.stopping = true;
  writer.current = undefined;
  clearTimeout(writer.idle);
  writer.kill();
}
function cancelOutput(/** @type {any} */ owner) {
  outputQueue = outputQueue.filter((item) => item.owner !== owner);
  if (writer?.current?.owner === owner) stopWriter();
  if (!outputQueue.length) {
    clearTimeout(writerWake);
    writerWake = undefined;
  }
}
function pumpOutput() {
  try {
    outputQueue = outputQueue.filter((item) => item.live());
    if (!outputQueue.length || writer?.stopping || writer?.current) return;
    if (!writer) {
      const delay = nextWriterAt - Date.now();
      if (delay > 0) {
        writerWake ??= setTimeout(() => {
          writerWake = undefined;
          pumpOutput();
        }, delay);
        writerWake.unref();
        return;
      }
      nextWriterAt = Date.now() + 1000; // Resource throttle, not a business retry.
      const child = spawn(
        process.execPath,
        ['--input-type=commonjs', '-e', WRITER],
        {
          stdio: ['ignore', 'ignore', 2, 'ipc'],
          env: {}, // No NODE_OPTIONS, preloads, credentials or execArgv inheritance.
          windowsHide: true,
          shell: false,
        },
      );
      const state = {
        child,
        current: /** @type {Output|undefined} */ (undefined),
        stopping: false,
        idle: /** @type {ReturnType<typeof setTimeout>|undefined} */ (
          undefined
        ),
        kill() {
          try {
            child.kill('SIGKILL');
          } catch {
            /* Best effort; child has a lease. */
          }
        },
      };
      writer = state;
      process.once('exit', state.kill);
      child.on('error', () => {
        if (writer === state) stopWriter();
      });
      child.on('message', (message) => {
        if (writer !== state || state.stopping || message !== 1) return;
        state.current = undefined;
        pumpOutput();
        if (!state.current && !state.stopping) {
          state.idle = setTimeout(stopWriter, 100);
          state.idle.unref();
        }
      });
      child.once('close', () => {
        process.removeListener('exit', state.kill);
        clearTimeout(state.idle);
        if (writer !== state) return;
        // An unacknowledged record may be partial. Drop the backlog on failure;
        // never replay bytes or errors into a new writer/generation.
        if (state.current) outputQueue = [];
        writer = undefined;
        pumpOutput();
      });
      child.unref();
      child.channel?.unref();
    }
    clearTimeout(writer.idle);
    const current = outputQueue.shift();
    writer.current = current;
    if (!current) return;
    const state = writer;
    state.child.send(current.line, (error) => {
      if (error && writer === state) stopWriter();
    });
  } catch {
    outputQueue = [];
    stopWriter();
  }
}
function queueOutput(/** @type {Output} */ item) {
  if (!outputOwners.has(item.owner)) {
    item.owner.effect(
      () => () => cancelOutput(item.owner),
      'auriga-dsh: diagnostic output owner',
    );
    outputOwners.add(item.owner);
  }
  if (outputQueue.length + (writer?.current ? 1 : 0) < 32) {
    outputQueue.push(item);
    pumpOutput();
  }
  // Admission (including deliberate pressure loss) consumes the original gate.
  // It cannot synchronously promise delivery or loosen per-generation limits.
}
/** The context inherits only an opaque token, never the writer/permit itself.
 * @type {WeakMap<object, {readonly active:boolean,send:(line:string,owner:any)=>boolean}>} */
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
  /** @type {undefined|{line:string,owner:any,accepted:boolean}} */ let pending;
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
      send(/** @type {string} */ line, /** @type {any} */ owner) {
        if (!active || pending) return false;
        ctx.fiber?.assertActive?.();
        const permit = { line, owner, accepted: false };
        pending = permit;
        try {
          logger.warn(line);
        } finally {
          if (pending === permit) pending = undefined;
        }
        return permit.accepted;
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
          queueOutput({
            line: permit.line + '\n',
            owner: permit.owner,
            live: () => {
              try {
                ctx.fiber?.assertActive?.();
                permit.owner.fiber?.assertActive?.();
                return active;
              } catch {
                return false;
              }
            },
          });
          permit.accepted = true;
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
        if (!sink.send(line, ctx)) return;
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
