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
import { readLocalBrowserCookieSets } from './browser-cookies.js';
import { readUacConfig } from './config.js';
import { qrPath } from './qr.js';
import { uacTransportReason } from './http.js';

export const CHALLENGE_TTL_MS = 120000;
const MIN_SCAN_INTERVAL_MS = 2000;
const MAX_TRANSIENT_FAULTS = 3;
const FILE = 'uac-local.json';

/** Only the SHA-256 prefix of a token may appear in diagnostics. @param {string} token */
export const tokenHash = (token) =>
  createHash('sha256').update(token, 'utf8').digest('hex').slice(0, 12);

/**
 * The encrypted vault stores only the owner binding. Active credentials and
 * challenges end with the process, including after a failed storage operation.
 * Platform features still require the shell's platform session.
 * @param {{directory:string,loadConfig?:typeof readUacConfig,createAdapter?:typeof createUacAdapter,
 *   writeOwner?:typeof atomicWriteJson,
 *   readBrowserCookies?:typeof import('./browser-cookies.js').readLocalBrowserCookies,
 *   readBrowserCookieSets?:typeof readLocalBrowserCookieSets,
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
  /** @type {'ready'|'unconfigured'|'invalid'|'storage'|'owner_domain'} */ let availability =
    'unconfigured';
  /** @type {Buffer|undefined} */ let key;
  /** @type {string} */ let identityDomain;
  /** @type {{empNo:string,identityDomain:string|null}|null} */ let owner =
    null;
  /** @type {{generation:string,empNo:string,token:string,clientIP:string,department?:string,displayName?:string,avatarUrl?:string,authMethod?:'sso'|'qr',loginAt:number,verifiedAt:number,attempt:number,nextRevalidateAt:number}|null} */
  let session = null;
  /** @type {Set<()=>void>} */ const identityListeners = new Set();
  function notifyIdentity() {
    for (const listener of identityListeners) {
      try {
        listener();
      } catch {
        /* The identity remains authoritative when a consumer is unavailable. */
      }
    }
  }
  /** @type {any} */ let challenge;
  /** @type {{outcome:string,at:number}|undefined} */ let last;
  /** @type {any} */ let timer;
  let ssoSuppressed = false;
  let ssoAttempted = false;
  /** @type {string|undefined} */ let ssoDiagnostic;
  /** @type {string|undefined} */ let configurationDiagnostic;
  /** @type {Promise<boolean>|undefined} */ let ssoInflight;
  let closed = false;
  let operations = Promise.resolve();
  const readBrowserCookies = options.readBrowserCookies;

  const emit = (
    /** @type {string} */ level,
    /** @type {string} */ stage,
    /** @type {Record<string,unknown>} */ fields,
  ) => log(level, 'auriga local UAC ' + stage, { stage, ...fields });

  /** @template T @param {()=>T|Promise<T>} change */
  function mutate(change) {
    const job = operations.then(change);
    operations = job.then(
      () => {},
      () => {},
    );
    return job;
  }

  /** @param {{empNo:string,identityDomain:string}} binding */
  async function persistOwner(binding) {
    if (!key) throw new Error('local UAC storage unavailable');
    const value = sealSecret(
      key,
      JSON.stringify({ version: 2, owner: binding }),
    );
    await (options.writeOwner ?? atomicWriteJson)(path, value);
  }

  const ready = (async () => {
    try {
      config = await (options.loadConfig ?? readUacConfig)(directory);
      if (!config) {
        emit('INFO', 'configuration', { result: 'unconfigured' });
        return;
      }
      adapter = (options.createAdapter ?? createUacAdapter)(
        config.adapter,
        config.provider,
      );
      adapter.validate();
      identityDomain = adapter.identityDomain();
    } catch (error) {
      config = null;
      adapter = null;
      availability = 'invalid';
      const reason = uacTransportReason(error);
      if (reason !== 'transport_error') configurationDiagnostic = reason;
      emit('WARN', 'configuration', {
        result: 'invalid',
        ...(configurationDiagnostic ? { reason: configurationDiagnostic } : {}),
      });
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
        owner = readOwner(value);
      }
      if (owner && owner.identityDomain !== identityDomain) {
        availability = 'owner_domain';
        emit('WARN', 'configuration', { result: 'owner_domain_mismatch' });
        return;
      }
      availability = 'ready';
      // Silently attempt local browser SSO cookie login when configured,
      // bounded so slow DPAPI/network checks never stall /auriga/uac/status.
      if (adapter.ssoCookieSpec?.()) {
        let ssoTimer;
        try {
          await Promise.race([
            trySsoInternal(),
            new Promise((resolve) => {
              ssoTimer = setTimeout(resolve, 4000);
              ssoTimer.unref?.();
            }),
          ]);
        } finally {
          clearTimeout(ssoTimer);
        }
      } else {
        emit('INFO', 'sso', {
          phase: 'end',
          result: 'skipped',
          reason: 'cookies_unconfigured',
        });
      }
    } catch {
      // Fail closed: an unreadable owner binding must never permit rebinding.
      availability = 'storage';
      emit('ERROR', 'storage', { result: 'unavailable' });
    }
  })();

  /** Local browser SSO never blocks QR login; only fixed diagnostic reasons leave it. */
  async function trySsoInternal() {
    if (
      closed ||
      availability !== 'ready' ||
      session ||
      ssoSuppressed ||
      ssoAttempted
    )
      return !!session;
    const spec = adapter?.ssoCookieSpec?.();
    if (!spec) return false;
    if (ssoInflight) return ssoInflight;
    ssoAttempted = true;
    ssoInflight = (async () => {
      const correlationId = randomUUID();
      try {
        let maps = /** @type {Map<string,string>[]} */ ([]);
        let v20Detected = false;
        let unreadable = false;
        if (readBrowserCookies && !options.readBrowserCookieSets) {
          const single = await readBrowserCookies({
            domain: spec.domain,
            names: spec.names,
            now,
          });
          if (single instanceof Map && single.size) maps = [single];
        } else {
          const result = await (
            options.readBrowserCookieSets ?? readLocalBrowserCookieSets
          )({
            domain: spec.domain,
            names: spec.names,
            now,
          });
          maps = result.maps;
          v20Detected = result.v20Detected;
          unreadable = !!result.unreadable;
        }
        if (closed || session || ssoSuppressed) return !!session;
        let cred = null;
        for (const map of maps) {
          cred = adapter.extractSsoCredentials?.(map);
          if (cred) break;
        }
        if (!cred) {
          ssoDiagnostic = v20Detected
            ? 'v20_app_bound'
            : unreadable
              ? 'browser_store_unreadable'
              : 'no_readable_cookies';
          emit('INFO', 'sso', {
            phase: 'end',
            result: 'fallback_qr',
            reason: ssoDiagnostic,
            correlationId,
          });
          return false;
        }
        if (owner && fold(owner.empNo) !== fold(cred.empNo)) return false;
        emit('INFO', 'sso', {
          phase: 'start',
          source: cred.source,
          correlationId,
        });
        const verified = await adapter.verify({
          empNo: cred.empNo,
          token: cred.token,
          clientIP: cred.clientIP,
        });
        if (closed || session || ssoSuppressed) return !!session;
        if (!verified.success) {
          ssoDiagnostic =
            verified.failureClass === 'upstream_fault'
              ? uacTransportReason({ code: verified.faultReason })
              : 'token_rejected';
          emit('INFO', 'sso', {
            phase: 'end',
            result: verified.failureClass ?? 'rejected',
            reason: ssoDiagnostic,
            correlationId,
          });
          return false;
        }
        const deptResult = await adapter
          .department(
            {
              empNo: verified.empNo,
              token: verified.token,
              clientIP: cred.clientIP,
            },
            'login_info',
          )
          .catch(() => ({ available: false }));
        if (closed || session || ssoSuppressed) return !!session;
        return await mutate(async () => {
          if (closed || session || ssoSuppressed) return !!session;
          if (owner && fold(owner.empNo) !== fold(verified.empNo)) return false;
          const at = now();
          if (!owner) {
            const binding = { empNo: verified.empNo, identityDomain };
            try {
              await persistOwner(binding);
            } catch {
              return false;
            }
            owner = binding;
          }
          if (closed) return false;
          session = {
            generation: randomUUID(),
            empNo: owner.empNo,
            token: verified.token,
            clientIP: cred.clientIP ?? '',
            ...(deptResult?.available && deptResult.department
              ? {
                  department: deptResult.department,
                  ...(deptResult.displayName
                    ? { displayName: deptResult.displayName }
                    : {}),
                  ...(deptResult.avatarUrl
                    ? { avatarUrl: deptResult.avatarUrl }
                    : {}),
                }
              : {}),
            authMethod: 'sso',
            loginAt: at,
            verifiedAt: at,
            attempt: 0,
            nextRevalidateAt: at + config.revalidation.intervalSeconds * 1000,
          };
          notifyIdentity();
          last = { outcome: 'signed_in', at };
          ssoDiagnostic = undefined;
          emit('INFO', 'sso', {
            phase: 'end',
            result: 'authenticated',
            tokenHash: tokenHash(verified.token),
            correlationId,
          });
          schedule();
          return true;
        });
      } catch {
        ssoDiagnostic = 'sso_unavailable';
        emit('WARN', 'sso', {
          phase: 'end',
          result: 'error',
          reason: ssoDiagnostic,
          correlationId,
        });
        return false;
      } finally {
        ssoInflight = undefined;
      }
    })();
    return ssoInflight;
  }

  function schedule() {
    clearTimer(timer);
    timer = undefined;
    if (closed || !session) return;
    timer = setTimer(
      () => revalidate().catch(() => {}),
      Math.max(0, session.nextRevalidateAt - now()),
    );
  }

  /** @param {string} outcome @param {string} correlationId */
  function signOut(outcome, correlationId) {
    const hash = session ? tokenHash(session.token) : undefined;
    session = null;
    notifyIdentity();
    challenge = undefined;
    clearTimer(timer);
    timer = undefined;
    last = { outcome, at: now() };
    emit('INFO', 'session', {
      result: 'signed_out',
      reason: outcome,
      correlationId,
      ...(hash ? { tokenHash: hash } : {}),
    });
  }

  async function revalidate() {
    const current = session;
    if (closed || !current || !adapter) return;
    const correlationId = randomUUID();
    const policy = config.revalidation;
    const credential = {
      empNo: current.empNo,
      token: current.token,
      clientIP: current.clientIP,
    };
    emit('INFO', 'revalidate', {
      phase: 'start',
      strategy: policy.strategy,
      attempt: current.attempt,
      maxRetries: policy.maxRetries,
      tokenHash: tokenHash(current.token),
      correlationId,
    });
    const department = policy.strategy === 'department';
    const result = department
      ? await adapter.department(credential, 'revalidate')
      : await adapter.verify(credential);
    return mutate(() => {
      if (closed || session !== current) return;
      const updated = { ...current };
      if (department ? result.available : result.success) {
        if (!department) {
          if (fold(result.empNo) !== fold(current.empNo))
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
        updated.attempt = current.attempt + 1;
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
      session = updated;
      notifyIdentity();
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
    return {
      available: availability !== 'unconfigured',
      ...(availability !== 'ready' && availability !== 'unconfigured'
        ? { reason: availability }
        : {}),
      state: session ? 'authenticated' : 'signed_out',
      ownerBound: !!owner,
      ...(session
        ? {
            empNo: session.empNo,
            ...(session.department ? { department: session.department } : {}),
            ...(session.displayName
              ? { displayName: session.displayName }
              : {}),
            ...(session.avatarUrl ? { avatarUrl: session.avatarUrl } : {}),
            ...(session.authMethod ? { authMethod: session.authMethod } : {}),
            loginAt: new Date(session.loginAt).toISOString(),
            verifiedAt: new Date(session.verifiedAt).toISOString(),
            revalidation: (session.attempt ?? 0) > 0 ? 'retrying' : 'ok',
          }
        : {}),
      ...(last ? { lastOutcome: last.outcome } : {}),
      ...(!session && ssoDiagnostic ? { ssoDiagnostic } : {}),
      ...(configurationDiagnostic ? { configurationDiagnostic } : {}),
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
    if (closed || availability !== 'ready' || session)
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
    // A scan for a different employee must not send its token for verification.
    if (owner && fold(owner.empNo) !== fold(result.empNo))
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
          ? verified.faultReason &&
            uacTransportReason({ code: verified.faultReason }) !==
              'transport_error'
            ? verified.faultReason
            : 'verify_unavailable'
          : 'token_rejected',
      );
    const deptResult = await adapter
      .department(
        {
          empNo: verified.empNo,
          token: verified.token,
          clientIP: result.clientIP,
        },
        'login_info',
      )
      .catch(() => ({ available: false }));
    if (closed || challenge !== current || terminal(current.state)) return;
    return mutate(async () => {
      if (closed || challenge !== current || terminal(current.state)) return;
      if (now() >= current.expiresAt) return settle(current, 'expired');
      if (owner && fold(owner.empNo) !== fold(verified.empNo))
        return settle(current, 'rejected', 'owner_mismatch');
      const at = now();
      if (!owner) {
        const binding = { empNo: verified.empNo, identityDomain };
        try {
          await persistOwner(binding);
        } catch {
          return settle(current, 'failed', 'storage_unavailable');
        }
        owner = binding;
      }
      if (closed) return;
      session = {
        generation: randomUUID(),
        empNo: owner.empNo,
        token: verified.token,
        clientIP: result.clientIP ?? '',
        ...(deptResult?.available && deptResult.department
          ? {
              department: deptResult.department,
              ...(deptResult.displayName
                ? { displayName: deptResult.displayName }
                : {}),
              ...(deptResult.avatarUrl
                ? { avatarUrl: deptResult.avatarUrl }
                : {}),
            }
          : {}),
        authMethod: 'qr',
        loginAt: at,
        verifiedAt: at,
        attempt: 0,
        nextRevalidateAt: at + config.revalidation.intervalSeconds * 1000,
      };
      notifyIdentity();
      ssoSuppressed = false;
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
    return mutate(() => {
      if (closed || availability !== 'ready') throw failure('unavailable');
      challenge = undefined;
      ssoSuppressed = true;
      if (session) signOut('user_logout', randomUUID());
      return status();
    });
  }

  function credentials() {
    if (closed || !session) return null;
    return {
      empNo: session.empNo,
      token: session.token,
      updatedAt: new Date(session.verifiedAt).toISOString(),
    };
  }

  return {
    ready,
    status,
    credentials,
    proofCredentials() {
      if (closed || !session) return null;
      return {
        generation: session.generation,
        empNo: session.empNo,
        token: session.token,
      };
    },
    subscribe(/** @type {()=>void} */ listener) {
      identityListeners.add(listener);
      return () => {
        identityListeners.delete(listener);
      };
    },
    start,
    poll,
    cancel,
    logout,
    async close() {
      closed = true;
      notifyIdentity();
      identityListeners.clear();
      challenge = undefined;
      clearTimer(timer);
      await ready;
      await operations;
      owner = null;
      session = null;
      key?.fill(0);
    },
  };
}

/** @param {any} stored */
function readOwner(stored) {
  if (stored?.version === 1 && stored.owner === null && stored.session === null)
    return null;
  const binding = stored?.owner;
  if (
    typeof binding?.empNo !== 'string' ||
    !fold(binding.empNo) ||
    binding.empNo.length > 128
  )
    throw new Error('invalid UAC owner binding');
  // Keep an old binding reserved without assigning its employee to today's
  // configuration. A version-one vault has no evidence of the original domain.
  if (stored.version === 1)
    return { empNo: binding.empNo, identityDomain: null };
  if (
    stored.version !== 2 ||
    typeof binding.identityDomain !== 'string' ||
    !/^[a-f0-9]{64}$/.test(binding.identityDomain)
  )
    throw new Error('invalid UAC owner binding');
  return { empNo: binding.empNo, identityDomain: binding.identityDomain };
}

/** @param {string} code */
function failure(code) {
  return Object.assign(new Error(code), { code });
}
