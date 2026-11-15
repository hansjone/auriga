import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { readFile, lstat } from 'node:fs/promises';
import { join } from 'node:path';
import { atomicWriteJson } from '../utils/atomic-write.js';
import {
  loadOrCreateSecretKey,
  openSecret,
  sealSecret,
} from '../utils/secret-box.js';
import { createUacAdapter, fold } from './adapter.js';
import { readUacConfig } from './config.js';
import { qrPath } from './qr.js';

export const CHALLENGE_TTL_MS = 120000;
const MIN_SCAN_INTERVAL_MS = 2000;
const MAX_TRANSIENT_FAULTS = 3;
const FILE = 'uac-local.json';

/** Only the SHA-256 prefix of a token may appear in diagnostics. @param {string} token */
export const tokenHash = (token) =>
  createHash('sha256').update(token, 'utf8').digest('hex').slice(0, 12);

/**
 * Client-owned UAC identity, independent of the platform. The QR coordinates,
 * enterprise token and owner binding never leave this process except to the
 * private allowedDestinations. It admits only the single local DSH owner and
 * grants no platform authority: platform features still require the shell's
 * platform session.
 * @param {{directory:string,loadConfig?:typeof readUacConfig,createAdapter?:typeof createUacAdapter,
 *   now?:()=>number,setTimer?:(fn:()=>void,ms:number)=>any,clearTimer?:(t:any)=>void,
 *   log?:(level:string,message:string,fields:Record<string,unknown>)=>void}} options
 */
export function createLocalUacIdentity(options) {
  const { directory } = options;
  const now = options.now ?? Date.now;
  const setTimer =
    options.setTimer ??
    ((fn, ms) => {
      const t = setTimeout(fn, ms);
      t.unref?.();
      return t;
    });
  const clearTimer = options.clearTimer ?? clearTimeout;
  const log = options.log ?? (() => {});
  const path = join(directory, FILE);
  /** @type {any} */ let config = null;
  /** @type {any} */ let adapter = null;
  /** @type {'ready'|'unconfigured'|'invalid'|'storage'} */ let availability =
    'unconfigured';
  /** @type {Buffer|undefined} */ let key;
  /** @type {{version:1,owner:{empNo:string}|null,session:any}} */
  let state = { version: 1, owner: null, session: null };
  /** @type {any} */ let challenge;
  /** @type {{outcome:string,at:number}|undefined} */ let last;
  /** @type {any} */ let timer;
  let closed = false;
  let writing = Promise.resolve();

  const emit = (
    /** @type {string} */ level,
    /** @type {string} */ stage,
    /** @type {Record<string,unknown>} */ fields,
  ) => log(level, 'auriga local UAC ' + stage, { stage, ...fields });

  /** @template T @param {()=>T|Promise<T>} change */
  function mutate(change) {
    const job = writing.then(change);
    writing = job.then(
      () => {},
      () => {},
    );
    return job;
  }

  /** @param {typeof state} next */
  async function persist(next) {
    if (!key) throw new Error('local UAC storage unavailable');
    await atomicWriteJson(path, sealSecret(key, JSON.stringify(next)));
  }

  const ready = (async () => {
    try {
      config = await (options.loadConfig ?? readUacConfig)(directory);
      if (!config) return;
      adapter = (options.createAdapter ?? createUacAdapter)(
        config.adapter,
        config.provider,
      );
      adapter.validate();
    } catch {
      config = null;
      adapter = null;
      availability = 'invalid';
      emit('WARN', 'configuration', { result: 'invalid' });
      return;
    }
    try {
      let raw;
      try {
        const stat = await lstat(path);
        if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 65536)
          throw new Error();
        raw = await readFile(path, 'utf8');
      } catch (error) {
        if (/** @type {NodeJS.ErrnoException} */ (error).code !== 'ENOENT')
          throw error;
      }
      key = await loadOrCreateSecretKey(directory, {
        hasEncryptedData: raw !== undefined,
      });
      if (raw !== undefined) {
        const value = JSON.parse(openSecret(key, JSON.parse(raw)) ?? 'null');
        if (!validStoredState(value)) throw new Error();
        state = {
          version: 1,
          owner: value.owner ?? null,
          session: value.session ?? null,
        };
      }
      availability = 'ready';
      schedule();
    } catch {
      // Fail closed: an unreadable owner binding must never permit rebinding.
      availability = 'storage';
      state = { version: 1, owner: null, session: null };
      emit('ERROR', 'storage', { result: 'unavailable' });
    }
  })();

  function schedule() {
    clearTimer(timer);
    timer = undefined;
    const session = state.session;
    if (closed || !session) return;
    timer = setTimer(
      () => revalidate().catch(() => {}),
      Math.max(0, session.nextRevalidateAt - now()),
    );
  }

  /** @param {string} outcome @param {string} correlationId */
  async function signOut(outcome, correlationId) {
    const hash = state.session ? tokenHash(state.session.token) : undefined;
    state = { ...state, session: null };
    clearTimer(timer);
    timer = undefined;
    last = { outcome, at: now() };
    try {
      await persist(state);
    } catch (error) {
      availability = 'storage';
      emit('ERROR', 'storage', { result: 'unavailable', correlationId });
      throw error;
    }
    emit('INFO', 'session', {
      result: 'signed_out',
      reason: outcome,
      correlationId,
      ...(hash ? { tokenHash: hash } : {}),
    });
  }

  async function revalidate() {
    const session = state.session;
    if (closed || !session || !adapter) return;
    const correlationId = randomUUID();
    const policy = config.revalidation;
    const credential = {
      empNo: session.empNo,
      token: session.token,
      clientIP: session.clientIP,
    };
    emit('INFO', 'revalidate', {
      phase: 'start',
      strategy: policy.strategy,
      attempt: session.attempt ?? 0,
      maxRetries: policy.maxRetries,
      tokenHash: tokenHash(session.token),
      correlationId,
    });
    const department = policy.strategy === 'department';
    const result = department
      ? await adapter.department(credential, 'revalidate')
      : await adapter.verify(credential);
    return mutate(async () => {
      if (closed || state.session !== session) return;
      const updated = { ...session };
      if (department ? result.available : result.success) {
        if (!department) {
          if (fold(result.empNo) !== fold(session.empNo))
            return signOut('revalidation_rejected', correlationId);
          updated.token = result.token;
        }
        updated.attempt = 0;
        updated.verifiedAt = now();
        updated.nextRevalidateAt = now() + policy.intervalSeconds * 1000;
      } else if (department || result.failureClass !== 'upstream_fault') {
        emit('WARN', 'revalidate', {
          phase: 'end',
          result: 'rejected',
          failureClass: result.failureClass ?? 'upstream_fault',
          correlationId,
        });
        return signOut('revalidation_rejected', correlationId);
      } else {
        updated.attempt = session.attempt + 1;
        if (updated.attempt > policy.maxRetries) {
          emit('WARN', 'revalidate', {
            phase: 'end',
            result: 'exhausted',
            faultReason: result.faultReason,
            correlationId,
          });
          return signOut('revalidation_unreachable', correlationId);
        }
        updated.nextRevalidateAt = now() + policy.retryIntervalSeconds * 1000;
        emit('WARN', 'revalidate', {
          phase: 'end',
          result: 'retry',
          faultReason: result.faultReason,
          attempt: updated.attempt,
          correlationId,
        });
      }
      const next = { ...state, session: updated };
      try {
        await persist(next);
      } catch {
        return signOut('revalidation_storage', correlationId);
      }
      state = next;
      if (updated.attempt === 0)
        emit('INFO', 'revalidate', {
          phase: 'end',
          result: 'success',
          tokenHash: tokenHash(updated.token),
          correlationId,
        });
      schedule();
    });
  }

  function projectChallenge() {
    if (!challenge) return undefined;
    const stage =
      !terminal(challenge.state) && now() >= challenge.expiresAt
        ? 'expired'
        : challenge.state;
    return {
      id: challenge.id,
      state: stage,
      expiresAt: new Date(challenge.expiresAt).toISOString(),
      ...(challenge.failureCode ? { failureCode: challenge.failureCode } : {}),
      ...(['waiting', 'scanned'].includes(stage) ? { qr: challenge.qr } : {}),
      correlationId: challenge.correlationId,
    };
  }

  function status() {
    const session = state.session;
    return {
      available: availability !== 'unconfigured',
      ...(availability !== 'ready' && availability !== 'unconfigured'
        ? { reason: availability }
        : {}),
      state: session ? 'authenticated' : 'signed_out',
      ownerBound: !!state.owner,
      ...(session
        ? {
            empNo: session.empNo,
            loginAt: new Date(session.loginAt).toISOString(),
            verifiedAt: new Date(session.verifiedAt).toISOString(),
            revalidation: (session.attempt ?? 0) > 0 ? 'retrying' : 'ok',
          }
        : {}),
      ...(last ? { lastOutcome: last.outcome } : {}),
      ...(challenge ? { challenge: projectChallenge() } : {}),
    };
  }

  const terminal = (/** @type {string} */ s) =>
    !['waiting', 'scanned', 'verifying'].includes(s);

  async function start() {
    await ready;
    return mutate(beginChallenge);
  }

  function beginChallenge() {
    if (closed || availability !== 'ready' || state.session)
      throw failure('unavailable');
    if (challenge && !terminal(challenge.state) && now() < challenge.expiresAt)
      return projectChallenge();
    const coordinates = {
      x: randomBytes(16).toString('hex'),
      y: randomBytes(16).toString('hex'),
    };
    const correlationId = randomUUID();
    let qr;
    try {
      qr = qrPath(adapter.qrContent(coordinates));
    } catch {
      emit('WARN', 'challenge', { result: 'invalid', correlationId });
      throw failure('unavailable');
    }
    challenge = {
      id: randomUUID(),
      coordinates,
      state: 'waiting',
      expiresAt: now() + CHALLENGE_TTL_MS,
      qr,
      correlationId,
      nextScanAt: 0,
      faults: 0,
      inflight: undefined,
    };
    emit('INFO', 'challenge', {
      phase: 'start',
      result: 'waiting',
      correlationId,
    });
    return projectChallenge();
  }

  /** @param {any} current @param {string} next @param {string} [code] */
  function settle(current, next, code) {
    current.state = next;
    if (code) current.failureCode = code;
    emit(next === 'authenticated' ? 'INFO' : 'WARN', 'challenge', {
      phase: 'end',
      result: next,
      ...(code ? { failureCode: code } : {}),
      correlationId: current.correlationId,
    });
  }

  /** @param {any} current */
  async function scanOnce(current) {
    const correlationId = randomUUID();
    emit('DEBUG', 'scan', {
      phase: 'start',
      correlationId,
      parentCorrelationId: current.correlationId,
    });
    const result = await adapter.scan(current.coordinates);
    if (closed || challenge !== current || terminal(current.state)) return;
    emit('INFO', 'scan', {
      phase: 'end',
      result: result.state,
      ...(result.failureCode ? { failureCode: result.failureCode } : {}),
      correlationId,
      parentCorrelationId: current.correlationId,
    });
    if (now() >= current.expiresAt) return settle(current, 'expired');
    if (result.state === 'waiting' || result.state === 'scanned') {
      current.faults = 0;
      current.state = result.state;
      return;
    }
    if (result.state === 'unavailable') {
      if (++current.faults >= MAX_TRANSIENT_FAULTS)
        settle(current, 'unavailable', result.failureCode);
      return;
    }
    if (result.state !== 'verified')
      return settle(current, result.state, result.failureCode);
    current.state = 'verifying';
    // The owner binding is checked before any token is persisted.
    if (state.owner && fold(state.owner.empNo) !== fold(result.empNo))
      return settle(current, 'rejected', 'owner_mismatch');
    const verified = await adapter.verify({
      empNo: result.empNo,
      token: result.token,
      clientIP: result.clientIP,
    });
    if (closed || challenge !== current || terminal(current.state)) return;
    emit(verified.success ? 'INFO' : 'WARN', 'verify', {
      phase: 'end',
      result: verified.success ? 'success' : verified.failureClass,
      ...(verified.faultReason ? { faultReason: verified.faultReason } : {}),
      tokenHash: tokenHash(result.token),
      correlationId,
      parentCorrelationId: current.correlationId,
    });
    if (!verified.success)
      return settle(
        current,
        verified.failureClass === 'upstream_fault' ? 'unavailable' : 'rejected',
        verified.failureClass === 'upstream_fault'
          ? 'verify_unavailable'
          : 'token_rejected',
      );
    return mutate(async () => {
      if (closed || challenge !== current || terminal(current.state)) return;
      if (now() >= current.expiresAt) return settle(current, 'expired');
      if (state.owner && fold(state.owner.empNo) !== fold(verified.empNo))
        return settle(current, 'rejected', 'owner_mismatch');
      const at = now();
      const owner = state.owner ?? { empNo: verified.empNo };
      const next = {
        ...state,
        owner,
        session: {
          empNo: owner.empNo,
          token: verified.token,
          clientIP: result.clientIP ?? '',
          loginAt: at,
          verifiedAt: at,
          attempt: 0,
          nextRevalidateAt: at + config.revalidation.intervalSeconds * 1000,
        },
      };
      try {
        await persist(next);
      } catch {
        return settle(current, 'failed', 'storage_unavailable');
      }
      state = next;
      last = { outcome: 'signed_in', at };
      settle(current, 'authenticated');
      schedule();
    });
  }

  /** @param {string} id */
  async function poll(id) {
    await ready;
    const current = challenge;
    if (closed || !current || current.id !== id) throw failure('not_found');
    if (
      !current.inflight &&
      !terminal(current.state) &&
      now() >= current.expiresAt
    ) {
      settle(current, 'expired');
      return status();
    }
    if (
      ['waiting', 'scanned'].includes(current.state) &&
      !current.inflight &&
      now() >= current.nextScanAt
    ) {
      current.nextScanAt = now() + MIN_SCAN_INTERVAL_MS;
      current.inflight = scanOnce(current).finally(() => {
        current.inflight = undefined;
      });
    }
    if (current.inflight) await current.inflight;
    return status();
  }

  /** @param {string} id */
  async function cancel(id) {
    await ready;
    return mutate(() => {
      if (closed || challenge?.id !== id) throw failure('not_found');
      if (!terminal(challenge.state)) settle(challenge, 'cancelled');
      challenge = undefined;
      return status();
    });
  }

  async function logout() {
    await ready;
    return mutate(async () => {
      if (closed || availability !== 'ready') throw failure('unavailable');
      challenge = undefined;
      if (state.session) await signOut('user_logout', randomUUID());
      return status();
    });
  }

  return {
    ready,
    status,
    start,
    poll,
    cancel,
    logout,
    async close() {
      closed = true;
      challenge = undefined;
      clearTimer(timer);
      await ready;
      await writing;
      state = { version: 1, owner: null, session: null };
      key?.fill(0);
    },
  };
}

/** @param {any} stored */
function validStoredState(stored) {
  if (stored?.version !== 1) return false;
  const { owner, session } = stored;
  if (owner === null) return session === null;
  if (typeof owner?.empNo !== 'string' || !fold(owner.empNo)) return false;
  if (session === null) return true;
  return (
    session?.empNo === owner.empNo &&
    typeof session.token === 'string' &&
    !!session.token &&
    typeof session.clientIP === 'string' &&
    [session.loginAt, session.verifiedAt, session.nextRevalidateAt].every(
      (time) =>
        Number.isSafeInteger(time) && time >= 0 && time <= 8640000000000000,
    ) &&
    Number.isInteger(session.attempt) &&
    session.attempt >= 0
  );
}

/** @param {string} code */
function failure(code) {
  return Object.assign(new Error(code), { code });
}
