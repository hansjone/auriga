import { AdapterError } from './guard.js';

/** One rotating refresh family, one queue. The durable pending fence is written
 * BEFORE transport: a crash/lost response/failed save can never replay it.
 * @param {{vault:any, platform:any, config:any, now?:()=>number,
 * onPlatformAccess?:(claims:any)=>void, onUncertain?:()=>void}} options */
export function createRefreshBroker({
  vault,
  platform,
  config,
  now = Date.now,
  onPlatformAccess = () => {},
  onUncertain = () => {},
}) {
  let queue = Promise.resolve();
  /** @type {Map<string,Promise<any>>} */
  const flights = new Map();
  let lifetime = new AbortController(),
    aiLifetime = new AbortController();
  /** @type {import('./types.js').AccessToken | undefined} */
  let ai;
  let blocked = false,
    denied = false;
  /** @type {any} */
  let identity;
  /** @type {string} */
  let state = 'not_requested';
  const fail = () => new AdapterError('REAUTH_REQUIRED');
  const credential = () => {
    const saved = vault.snapshot().credential;
    if (
      blocked ||
      !saved ||
      saved.refreshPending ||
      (saved.platformOrigin !== undefined &&
        saved.platformOrigin !== config.platformOrigin) ||
      (saved.issuer !== undefined && saved.issuer !== config.issuer) ||
      (saved.clientId !== undefined && saved.clientId !== config.clientId) ||
      (saved.claims.iss !== undefined && saved.claims.iss !== config.issuer) ||
      (identity &&
        ['tid', 'sub', 'did'].some((k) => identity[k] !== saved.claims[k]))
    ) {
      invalidate('reauth_required');
      throw fail();
    }
    identity ??= {
      tid: saved.claims.tid,
      sub: saved.claims.sub,
      did: saved.claims.did,
    };
    return saved;
  };
  /** @param {string} reason */
  function invalidate(reason) {
    blocked = true;
    state = reason;
    ai = undefined;
    lifetime.abort();
    aiLifetime.abort();
  }
  /** @param {()=>Promise<any>} work */
  function enqueue(work) {
    const result = queue.then(work);
    queue = result.then(
      () => {},
      () => {},
    );
    return result;
  }
  /** @param {'ai'|'platform'} resource */
  function acquire(resource) {
    const flight = flights.get(resource);
    if (flight) return flight;
    const signal = lifetime.signal;
    const assert = () => {
      if (signal.aborted) throw fail();
    };
    const job = enqueue(async () => {
      assert();
      const saved = credential();
      if (resource === 'ai') {
        if (denied) throw new AdapterError('AI_NOT_AUTHORIZED');
        if (ai && ai.expiresAt > now() + 30000) return ai;
      } else if (saved.claims.exp * 1000 > now() + 30000)
        return saved.accessToken;
      // Read inside the serial job, never capture a refresh before queueing.
      const matches = (/** @type {any} */ current) =>
        current &&
        current.refreshToken === saved.refreshToken &&
        ['tid', 'sub', 'did'].every(
          (k) => current.claims[k] === saved.claims[k],
        );
      try {
        await vault.update((/** @type {any} */ s) => {
          assert();
          if (!matches(s.credential) || s.credential.refreshPending)
            throw fail();
          s.credential.refreshPending = true;
        });
        assert();
        let tokens;
        try {
          tokens = await platform.request('/oauth2/token', {
            method: 'POST',
            form: true,
            signal,
            body: {
              grant_type: 'refresh_token',
              client_id: config.clientId,
              refresh_token: saved.refreshToken,
              ...(resource === 'ai'
                ? { resource: 'urn:auriga:ai-gateway' }
                : {}),
            },
          });
        } catch (error) {
          const e = /** @type {any} */ (error);
          // An explicit OAuth rejection has no successful exchange to replay.
          // AI invalid_grant also means missing model permission, so never
          // claim Team/logout failed merely because this resource was denied.
          if (
            resource !== 'ai' ||
            ![400, 401].includes(e.status) ||
            ![
              'invalid_grant',
              'invalid_scope',
              'invalid_target',
              'invalid_request',
              'invalid_client',
              'unauthorized_client',
            ].includes(e.code)
          )
            throw error;
          assert();
          await vault.update((/** @type {any} */ s) => {
            assert();
            if (!matches(s.credential)) throw fail();
            s.credential.refreshPending = false;
          });
          aiLifetime.abort();
          aiLifetime = new AbortController();
          ai = undefined;
          denied = true;
          state = 'not_authorized';
          throw new AdapterError('AI_NOT_AUTHORIZED');
        }
        assert();
        if (
          tokens.token_type?.toLowerCase() !== 'bearer' ||
          tokens.scope !== 'auriga.connect' ||
          typeof tokens.refresh_token !== 'string' ||
          !tokens.refresh_token ||
          tokens.refresh_token === saved.refreshToken
        )
          throw fail();
        const claims = await (resource === 'ai'
          ? platform.verifyAi(tokens.access_token, saved.claims)
          : platform.verifyDevice(tokens.access_token, saved.claims));
        assert();
        const expiresAt = claims.exp * 1000;
        if (
          expiresAt <= now() ||
          (resource === 'ai' &&
            (!Number.isSafeInteger(tokens.expires_in) ||
              tokens.expires_in < 1 ||
              tokens.expires_in > 300 ||
              expiresAt > now() + tokens.expires_in * 1000 + 1000))
        )
          throw fail();
        await vault.update((/** @type {any} */ s) => {
          assert();
          if (!matches(s.credential)) throw fail();
          // Keep the platform access/claims when only AI rotates the family.
          s.credential = {
            ...s.credential,
            refreshToken: tokens.refresh_token,
            refreshPending: false,
            platformOrigin: config.platformOrigin,
            issuer: config.issuer,
            clientId: config.clientId,
            ...(resource === 'platform'
              ? { accessToken: tokens.access_token, claims }
              : {}),
          };
        });
        assert();
        if (expiresAt <= now()) throw fail();
        if (resource === 'platform') {
          onPlatformAccess(claims);
          return tokens.access_token;
        }
        ai = Object.freeze({
          token: tokens.access_token,
          expiresAt,
          signal: aiLifetime.signal,
        });
        state = 'token_ready';
        return ai;
      } catch (error) {
        if (error instanceof AdapterError && error.code === 'AI_NOT_AUTHORIZED')
          throw error;
        invalidate('reauth_required');
        onUncertain();
        throw fail();
      }
    });
    flights.set(resource, job);
    void job
      .finally(() => {
        if (flights.get(resource) === job) flights.delete(resource);
      })
      .catch(() => {});
    return job;
  }
  return {
    getPlatformAccess: () => acquire('platform'),
    getAiAccess: () => acquire('ai'),
    invalidate,
    /** Login commits share the queue; only an actual successful native login
     * may replace the durable uncertain fence, never a cached resume.
     * @param {()=>Promise<void>} write */
    commitLogin(write) {
      return enqueue(async () => {
        await write();
        invalidate('not_requested');
        lifetime = new AbortController();
        aiLifetime = new AbortController();
        blocked = false;
        denied = false;
        identity = undefined;
      });
    },
    get status() {
      return {
        state,
        reauthenticationRequired:
          blocked || !!vault.snapshot().credential?.refreshPending,
      };
    },
  };
}
