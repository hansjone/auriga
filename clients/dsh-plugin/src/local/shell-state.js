import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';

const ZERO = '00000000-0000-0000-0000-000000000000';
const uuid = (/** @type {any} */ v) =>
  typeof v === 'string' &&
  /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(v);
const states = new Set([
  'signed_out',
  'authenticating',
  'authenticated',
  'unreachable',
  'reauth_required',
  'revoked',
]);
const reasons = new Set([
  'user_action',
  'expired',
  'offline',
  'source_disabled',
  'account_changed',
  'shell_restarted',
  'revalidation_rejected',
  'revalidation_unreachable',
  'user_logout',
  'admin_disabled',
  'member_disabled',
]);
const fail = (code = 'common.unauthenticated') =>
  Object.assign(new Error(code), { code });
const identity = (/** @type {any} */ s) => JSON.stringify(s?.identity ?? null);

/** Consumes only an OS-authenticated C transport. It must never be mounted as
 * ordinary DSH HTTP or accept a browser-selected pipe, URL, identity or token.
 * Transport.send resolves one ShellAuthEvent; subscribe registers events and
 * disconnect atomically on that same authenticated connection.
 * @param {{transport:any,now?:()=>number,monotonic?:()=>number,
 * onChange?:(snapshot:any)=>void,onInvalidate?:()=>void}} options */
export function createShellState(options) {
  const now = options.now ?? Date.now;
  const mono = options.monotonic ?? (() => performance.now());
  /** @type {any} */ let snapshot;
  let until = 0,
    deadline = 0,
    generation = 0,
    closed = false,
    floor = 0;
  /** @type {any} */ let timer;
  /** @type {any} */ let unsubscribe;
  /** @type {string|undefined} */ let epoch;
  let lastRevision = -1,
    lastExpiry = 0,
    connection = 0;
  /** @type {string|undefined} */ let subscription;
  /** @type {Set<()=>void>} */ const invalidations = new Set();
  const notifyInvalidation = () => {
    for (const callback of invalidations) callback();
    options.onInvalidate?.();
  };
  function invalidate() {
    generation++;
    clearTimeout(timer);
    snapshot = undefined;
    until = deadline = 0;
    notifyInvalidation();
  }
  function current() {
    if (snapshot && (now() >= until || mono() >= deadline)) invalidate();
    return snapshot ? JSON.parse(JSON.stringify(snapshot)) : undefined;
  }
  function accept(/** @type {any} */ value, discover = false) {
    const ttl = Date.parse(value?.expiresAt) - now();
    if (
      closed ||
      value?.protocolVersion !== 1 ||
      !uuid(value.shellEpoch) ||
      !Number.isSafeInteger(value.revision) ||
      value.revision < 0 ||
      !states.has(value.state) ||
      !(ttl > 0 && ttl <= 30000)
    )
      throw fail('common.validation_failed');
    if (
      value.state === 'authenticated' &&
      (!value.identity ||
        !['tenantId', 'memberId', 'deviceId', 'providerId'].every((key) =>
          uuid(value.identity[key]),
        ))
    )
      throw fail('common.validation_failed');
    if (
      ['signed_out', 'authenticating', 'revoked'].includes(value.state) &&
      value.identity
    )
      throw fail('common.validation_failed');
    if (value.state !== 'authenticated' && value.uacLoginInfo)
      throw fail('common.validation_failed');
    if (!discover && epoch && value.shellEpoch !== epoch) return;
    if (!discover && value.revision < Math.max(lastRevision, floor)) return;
    if (!discover && !snapshot && Date.parse(value.expiresAt) <= lastExpiry)
      return;
    if (snapshot && value.shellEpoch !== snapshot.shellEpoch && !discover)
      return;
    if (snapshot && value.shellEpoch === snapshot.shellEpoch) {
      if (value.revision < Math.max(snapshot.revision, floor)) return;
      if (
        value.revision === snapshot.revision &&
        (value.state !== snapshot.state ||
          identity(value) !== identity(snapshot))
      )
        throw fail('common.conflict');
    }
    if (value.revision < floor && !discover) return;
    epoch = value.shellEpoch;
    lastRevision = value.revision;
    lastExpiry = Date.parse(value.expiresAt);
    const changed =
      !snapshot ||
      snapshot.shellEpoch !== value.shellEpoch ||
      snapshot.revision !== value.revision ||
      identity(snapshot) !== identity(value);
    if (changed) {
      generation++;
      notifyInvalidation();
    }
    // Projection intentionally drops unknown fields, including accidentally
    // supplied credentials; consumers permit future response extensions.
    snapshot = {
      protocolVersion: 1,
      shellEpoch: value.shellEpoch,
      revision: value.revision,
      state: value.state,
      expiresAt: value.expiresAt,
    };
    if (value.identity) {
      snapshot.identity = {};
      for (const key of [
        'tenantId',
        'memberId',
        'deviceId',
        'providerId',
        'empNo',
        'displayName',
      ])
        if (typeof value.identity[key] === 'string')
          snapshot.identity[key] = value.identity[key];
    }
    if (value.identity?.uacSessionBinding)
      snapshot.identity.uacSessionBinding = JSON.parse(
        JSON.stringify(value.identity.uacSessionBinding),
      );
    if (reasons.has(value.reason)) snapshot.reason = value.reason;
    const info = value.uacLoginInfo;
    if (
      info?.purpose === 'login_info' &&
      ['pending', 'available', 'unavailable'].includes(info.status)
    ) {
      snapshot.uacLoginInfo = { purpose: 'login_info', status: info.status };
      if (
        info.status === 'available' &&
        typeof info.department === 'string' &&
        Number.isFinite(Date.parse(info.updatedAt))
      )
        Object.assign(snapshot.uacLoginInfo, {
          department: info.department,
          updatedAt: info.updatedAt,
        });
    }
    until = Date.parse(value.expiresAt);
    deadline = mono() + ttl;
    clearTimeout(timer);
    timer = setTimeout(() => {
      current();
    }, ttl);
    timer.unref();
    options.onChange?.(current());
  }
  function envelope(/** @type {string} */ action, discover = false) {
    const s = current();
    if (!discover && !s) throw fail('common.unavailable');
    return {
      protocolVersion: 1,
      requestId: randomUUID(),
      shellEpoch: discover ? ZERO : s.shellEpoch,
      expectedRevision: discover ? 0 : s.revision,
      action,
    };
  }
  async function receive(/** @type {any} */ command, discover = false) {
    const g = generation;
    const event = await options.transport.send(command);
    if (closed || (!discover && generation !== g))
      throw fail('common.conflict');
    if (event?.requestId !== command.requestId)
      throw fail('common.validation_failed');
    if (event.type === 'snapshot' || event.type === 'changed')
      accept(event.snapshot, discover);
    else if (event.type !== 'ack') throw fail('common.unavailable');
    return event;
  }
  return {
    current,
    subscribeInvalidation(/** @type {()=>void} */ callback) {
      invalidations.add(callback);
      return () => {
        invalidations.delete(callback);
      };
    },
    async connect() {
      const c = ++connection;
      unsubscribe?.();
      subscription = undefined;
      invalidate();
      floor = 0;
      if (closed || !options.transport) return;
      const g = generation;
      try {
        const command = envelope('snapshot', true);
        const event = await options.transport.send(command);
        if (closed || g !== generation || c !== connection) return;
        if (event?.requestId !== command.requestId || event.type !== 'snapshot')
          throw fail('common.validation_failed');
        accept(event.snapshot, true);
        const subscribe = envelope('subscribe');
        subscription = subscribe.requestId;
        unsubscribe = options.transport.subscribe(
          (/** @type {any} */ e) => {
            if (closed || c !== connection || e?.requestId !== subscription)
              return;
            try {
              if (e.type === 'snapshot' || e.type === 'changed')
                accept(e.snapshot);
              else if (e.type === 'error') invalidate();
            } catch {
              invalidate();
            }
          },
          () => {
            if (c !== connection) return;
            subscription = undefined;
            invalidate();
          },
        );
        await receive(subscribe);
      } catch {
        invalidate();
      }
    },
    async command(/** @type {string} */ action) {
      if (!['open_login', 'cancel_login', 'logout'].includes(action))
        throw fail('common.validation_failed');
      const command = envelope(action);
      if (action === 'logout') {
        floor = command.expectedRevision + 1;
        invalidate();
        // Keep the request's original epoch/revision; do not discover another
        // identity while the logout command is in flight.
        const g = generation;
        const event = await options.transport.send(command);
        if (
          closed ||
          g !== generation ||
          event?.requestId !== command.requestId
        )
          throw fail('common.conflict');
        if (['snapshot', 'changed'].includes(event.type))
          accept(event.snapshot);
        return;
      }
      await receive(command);
    },
    /** Capture a generation before any asynchronous platform operation.
     * Calling check before effects and before publishing rejects late results. */
    fence() {
      const s = current(),
        g = generation;
      if (s?.state !== 'authenticated') throw fail();
      return () => {
        if (g !== generation || current()?.state !== 'authenticated')
          throw fail();
      };
    },
    disconnect() {
      connection++;
      unsubscribe?.();
      subscription = undefined;
      invalidate();
    },
    close() {
      connection++;
      closed = true;
      unsubscribe?.();
      subscription = undefined;
      invalidate();
    },
  };
}
