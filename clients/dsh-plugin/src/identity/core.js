import { randomToken } from './crypto.js';
import { fail } from './errors.js';
import { createSessionManager, parseCookieHeader } from './session.js';
import { createMemoryStore, normalizeRateLimitUser } from './store.js';
import { loadConnectors } from './registry.js';

const TX_TTL_MS = 5 * 60_000;
const STANDALONE_TENANT = '00000000-0000-7000-8000-000000000001';
/** Recent MFA window for linkIdentity (matches platform step-up order of magnitude). */
const MFA_RECENT_MS = 10 * 60_000;

/**
 * @typedef {{
 *   providers?: string[],
 *   subject?: string,
 *   email?: string,
 * }} OwnerPolicy
 */

/**
 * Standalone identity core: login transactions, owner binding, sessions, CSRF.
 *
 * @param {{
 *   config: {
 *     connectors?: Array<Record<string, unknown>>,
 *     callbackBaseUrl?: string,
 *     secure?: boolean,
 *     sameSite?: 'Strict' | 'Lax' | 'None',
 *     sessionTtlSeconds?: number,
 *     owner?: OwnerPolicy,
 *   },
 *   store?: ReturnType<typeof createMemoryStore>,
 *   now?: () => Date,
 *   allowLocalHttp?: boolean,
 *   fetchImpl?: typeof fetch,
 *   importModule?: (specifier: string) => Promise<unknown>,
 *   lookupAll?: (hostname: string) => Promise<Array<{ address: string, family: number }>>,
 * }} options
 */
export async function createStandaloneCore(options) {
  const now = options.now ?? (() => new Date());
  const store = options.store ?? createMemoryStore();
  const ownerPolicy = options.config.owner ?? null;
  const registry = await loadConnectors(
    {
      connectors:
        /** @type {Array<{ type: string, id?: string, module?: string, config?: Record<string, unknown> }>} */ (
          options.config.connectors ?? []
        ),
    },
    {
      allowLocalHttp: options.allowLocalHttp,
      fetchImpl: options.fetchImpl,
      lookupAll: options.lookupAll,
      now,
      importModule: options.importModule,
    },
  );
  const sessions = createSessionManager({
    secure: options.config.secure,
    sameSite: options.config.sameSite,
    sessionTtlSeconds: options.config.sessionTtlSeconds,
    now,
    store,
  });

  /** @type {Map<string, {
   *   txId: string,
   *   providerId: string,
   *   clientIp: string,
   *   createdAt: number,
   *   expiresAt: number,
   *   finished: boolean,
   *   busy: boolean,
   *   state: Record<string, unknown>|null,
   *   step: Record<string, unknown>|null,
   *   usernameHint?: string,
   * }>} */
  const transactions = new Map();

  const callbackBase =
    options.config.callbackBaseUrl ?? 'http://127.0.0.1/auriga/login/callback';

  /**
   * @param {string} providerId
   * @param {string} clientIp
   * @param {string} [username]
   */
  function checkRateLimit(providerId, clientIp, username) {
    const nowMs = now().getTime();
    if (!store.consumeRateLimit(`ip:${providerId}:${clientIp}`, nowMs, 20))
      return false;
    if (username) {
      const norm = normalizeRateLimitUser(username);
      if (!store.consumeRateLimit(`user:${providerId}:${norm}`, nowMs, 5))
        return false;
    }
    return true;
  }

  /**
   * @param {string} providerId
   * @param {{ subject: string, displayName?: string, email?: string }} profile
   * @param {{ type: string }} provider
   */
  function matchesOwnerPolicy(providerId, profile, provider) {
    if (!ownerPolicy) return true;
    if (ownerPolicy.providers?.length) {
      const allowed = ownerPolicy.providers;
      if (!allowed.includes(providerId) && !allowed.includes(provider.type))
        return false;
    }
    if (ownerPolicy.subject && ownerPolicy.subject !== profile.subject)
      return false;
    if (ownerPolicy.email) {
      const email = profile.email ?? '';
      if (email.toLowerCase() !== ownerPolicy.email.toLowerCase()) return false;
    }
    return true;
  }

  /**
   * @param {string} providerId
   * @param {{ subject: string, displayName?: string, email?: string }} profile
   * @param {{ type: string }} provider
   */
  function admit(providerId, profile, provider) {
    if (!matchesOwnerPolicy(providerId, profile, provider))
      return { ok: false, code: 'iam.admission_denied' };

    const claimed = store.claimOwner({
      providerId,
      subject: profile.subject,
      displayName: profile.displayName,
      email: profile.email,
    });
    if (claimed) {
      store.putIdentity(providerId, profile.subject);
      return { ok: true };
    }

    const owner = store.getOwner();
    if (!owner) return { ok: false, code: 'iam.admission_denied' };
    if (store.hasIdentity(providerId, profile.subject)) return { ok: true };
    if (owner.providerId === providerId && owner.subject === profile.subject) {
      store.putIdentity(providerId, profile.subject);
      return { ok: true };
    }
    return { ok: false, code: 'iam.admission_denied' };
  }

  /**
   * @param {Record<string, unknown>} step
   * @param {{ setCookie?: string[], csrf?: string }} [extra]
   */
  function toClientStep(step, extra = {}) {
    if (step.type === 'done') {
      return {
        step: { type: 'complete', redirectTo: '/' },
        ...extra,
      };
    }
    return { step, ...extra };
  }

  /**
   * @param {string} providerId
   * @param {{ clientIp?: string, userAgent?: string, locale?: string }} [meta]
   */
  async function beginLogin(providerId, meta = {}) {
    const provider = registry.get(providerId);
    if (!provider) return { step: fail('common.not_found') };
    const clientIp = meta.clientIp ?? '0.0.0.0';
    if (!checkRateLimit(providerId, clientIp))
      return { step: fail('common.rate_limited') };

    const txId = randomToken(16);
    const expiresAt = new Date(now().getTime() + TX_TTL_MS);
    /** @type {Record<string, unknown>} */
    const ctx = {
      txId,
      tenantId: STANDALONE_TENANT,
      providerId,
      purpose: 'login',
      locale: meta.locale ?? 'en',
      clientIp,
      userAgent: meta.userAgent ?? '',
      callbackUrl: `${callbackBase.replace(/\/$/, '')}/${providerId}`,
      state: null,
      expiresAt: expiresAt.toISOString(),
    };

    let config = { ...provider.config };
    if (provider.type === 'emergency') {
      config = { ...config, lastCounter: store.getEmergencyLastCounter() };
    }

    const result = await provider.connector.begin(ctx, config);
    const step = /** @type {Record<string, unknown>} */ (result.step);
    transactions.set(txId, {
      txId,
      providerId,
      clientIp,
      createdAt: now().getTime(),
      expiresAt: expiresAt.getTime(),
      finished: false,
      busy: false,
      state: /** @type {Record<string, unknown>|null} */ (result.state ?? null),
      step,
    });
    return { txId, ...toClientStep(step) };
  }

  /**
   * @param {string} txId
   * @param {Record<string, unknown>} input
   * @param {{ clientIp?: string }} [meta]
   */
  async function advanceLogin(txId, input, meta = {}) {
    const tx = transactions.get(txId);
    if (!tx) return { step: fail('iam.login_tx_expired') };
    if (tx.finished || tx.busy) return { step: fail('iam.login_tx_finished') };
    if (tx.expiresAt <= now().getTime()) {
      tx.finished = true;
      tx.state = null;
      return { step: fail('iam.login_tx_expired') };
    }

    // Claim the transaction before any await (prevents concurrent double-complete).
    tx.busy = true;

    const provider = registry.get(tx.providerId);
    if (!provider) {
      tx.finished = true;
      tx.busy = false;
      return { step: fail('common.not_found') };
    }

    const clientIp = meta.clientIp ?? tx.clientIp;
    let usernameHint = tx.usernameHint;
    if (
      input.type === 'form' &&
      input.values &&
      typeof input.values === 'object'
    ) {
      const u = /** @type {Record<string, unknown>} */ (input.values).username;
      if (typeof u === 'string' && u) usernameHint = u;
    }
    if (!checkRateLimit(tx.providerId, clientIp, usernameHint)) {
      tx.busy = false;
      return { step: fail('common.rate_limited') };
    }

    /** @type {Record<string, unknown>} */
    const ctx = {
      txId,
      tenantId: STANDALONE_TENANT,
      providerId: tx.providerId,
      purpose: 'login',
      locale: 'en',
      clientIp,
      callbackUrl: `${callbackBase.replace(/\/$/, '')}/${tx.providerId}`,
      state: tx.state,
      expiresAt: new Date(tx.expiresAt).toISOString(),
    };

    let config = { ...provider.config };
    if (provider.type === 'emergency') {
      config = { ...config, lastCounter: store.getEmergencyLastCounter() };
    }

    let result;
    try {
      result = await provider.connector.advance(ctx, config, input);
    } catch {
      tx.finished = true;
      tx.state = null;
      tx.busy = false;
      return { txId, step: fail('iam.connector_unavailable') };
    }

    const step = /** @type {Record<string, unknown>} */ (result.step);
    if (usernameHint) tx.usernameHint = usernameHint;
    tx.step = step;

    if (step.type === 'fail') {
      // Invalidate OIDC/stateful flows on failure so state cannot be retried.
      if (provider.type === 'oidc' || input.type === 'callback') {
        tx.finished = true;
        tx.state = null;
      } else if (result.state) {
        tx.state = /** @type {Record<string, unknown>} */ (result.state);
      }
      tx.busy = false;
      return { txId, step };
    }

    if (step.type === 'done') {
      tx.finished = true;
      tx.busy = false;
      tx.state = result.state
        ? /** @type {Record<string, unknown>} */ (result.state)
        : tx.state;
      const profile =
        /** @type {{ subject: string, displayName?: string, email?: string, mfaPerformed?: boolean, authTime?: string }} */ (
          step.profile
        );
      if (
        provider.type === 'emergency' &&
        tx.state &&
        typeof tx.state.lastCounter === 'number'
      )
        store.setEmergencyLastCounter(tx.state.lastCounter);

      const admission = admit(tx.providerId, profile, provider);
      if (!admission.ok) {
        tx.state = null;
        return { txId, step: fail(admission.code ?? 'iam.admission_denied') };
      }

      const issued = sessions.issue({
        providerId: tx.providerId,
        subject: profile.subject,
        displayName: profile.displayName,
        mfaPerformed: profile.mfaPerformed === true,
        authTime: profile.authTime,
      });
      tx.state = null;
      return {
        txId,
        step: { type: 'complete', redirectTo: '/' },
        setCookie: issued.setCookie,
        csrf: issued.csrf,
        session: issued.session,
      };
    }

    if (result.state)
      tx.state = /** @type {Record<string, unknown>} */ (result.state);
    tx.busy = false;
    return { txId, step };
  }

  /**
   * Link an identity under the owner. Requires the caller's owner session and
   * recent MFA (mfaAt within MFA_RECENT_MS).
   *
   * @param {string} providerId
   * @param {string} subject
   * @param {{ cookies: Record<string, string>, headers?: Record<string, string|undefined> }} auth
   */
  function linkIdentity(providerId, subject, auth) {
    const owner = store.getOwner();
    if (!owner) return { ok: false, code: 'common.precondition_failed' };

    const csrf = sessions.checkCsrf('POST', auth.cookies, auth.headers ?? {});
    if (!csrf.ok) return { ok: false, code: csrf.code };
    const session = csrf.session;
    if (!session) return { ok: false, code: 'common.unauthenticated' };
    if (
      session.providerId !== owner.providerId ||
      session.subject !== owner.subject
    )
      return { ok: false, code: 'common.forbidden' };

    const mfaAt = session.mfaAt ? new Date(String(session.mfaAt)).getTime() : 0;
    if (!mfaAt || now().getTime() - mfaAt > MFA_RECENT_MS)
      return { ok: false, code: 'common.forbidden' };

    store.putIdentity(providerId, subject);
    return { ok: true };
  }

  return {
    registry,
    store,
    sessions,
    beginLogin,
    advanceLogin,
    parseCookieHeader,
    /** @param {string} txId */
    getLoginTransaction(txId) {
      const tx = transactions.get(txId);
      if (!tx) return null;
      return {
        txId: tx.txId,
        providerId: tx.providerId,
        expiresAt: tx.expiresAt,
        finished: tx.finished,
        busy: tx.busy,
        state: tx.state,
        step: tx.step,
      };
    },
    listProviders() {
      return registry.list();
    },
    getOwner() {
      return store.getOwner();
    },
    linkIdentity,
  };
}
