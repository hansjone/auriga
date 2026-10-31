/* global AbortSignal */
import { randomBytes, createHash, timingSafeEqual } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { platformConfig, createPlatform, LoginJar } from './platform.js';
import { createVault } from './vault.js';
import { createTeam } from './team.js';
import { createRefreshBroker } from '../ai/refresh-broker.js';
import { createModelLifecycle } from '../ai/installation.js';
import { createNetworkLifecycle } from '../network/lifecycle.js';
import { createDeviceProvider } from '../network/device-provider.js';
import {
  createHttpResponseTracker,
  installHttpResponseTracking,
} from './host.js';

export const SESSION_COOKIE = 'auriga_local_session';
export const ATTEMPT_COOKIE = 'auriga_local_attempt';
const random = () => randomBytes(32).toString('base64url');
const hash = (/** @type {string} */ s) =>
  createHash('sha256').update(s).digest('hex');
const equal = (/** @type {unknown} */ a, /** @type {unknown} */ b) =>
  typeof a === 'string' &&
  typeof b === 'string' &&
  a.length === b.length &&
  Buffer.byteLength(a) === Buffer.byteLength(b) &&
  timingSafeEqual(Buffer.from(a), Buffer.from(b));
/** @param {{headers:{cookie?:string}}} request @param {string} name */
export function cookie(request, name) {
  const rows = (request.headers.cookie ?? '')
    .split(';')
    .map((x) => x.trim())
    .filter((x) => x.startsWith(name + '='));
  return rows.length === 1 ? rows[0].slice(name.length + 1) : undefined;
}

/** Publish the commit outcome before starting any asynchronous write, so
 * cancellation cannot compensate a backing session while its device is landing.
 * @param {any} attempt @param {()=>Promise<void>} write */
export function persistNativeAttempt(attempt, write) {
  if (attempt.commit) throw new Error('native device commit already started');
  const result = Promise.resolve().then(write);
  attempt.commit = result.then(
    () => true,
    () => false,
  );
  return result;
}

/** A committed device owns its backing beyond a temporary Team failure or
 * browser cancellation. Explicit logout independently tombstones/revokes it.
 * @param {any} attempt @param {()=>Promise<void>} compensate */
export function abandonNativeAttempt(attempt, compensate) {
  if (attempt.abandoning) return attempt.abandoning;
  attempt.version++;
  attempt.controller.abort();
  attempt.abandoning = (async () => {
    try {
      if (!(await (attempt.commit ?? false))) await compensate();
    } finally {
      attempt.jar.clear();
      attempt.verifier = undefined;
      attempt.device = undefined;
    }
  })();
  return attempt.abandoning;
}

/** Browser sessions are process-local. Restart can never revive a revoked old bearer from disk.
 * @param {{config:import('../generated/connect-config.js').ConnectConfig,directory:string,ctx:any}} options */
export async function createConnectRuntime({ config: input, directory, ctx }) {
  const config = platformConfig(input),
    platform = createPlatform(config),
    vault = await createVault(directory);
  /** @type {Map<string,any>} */
  const sessions = new Map(),
    attempts = new Map(),
    sockets = new Map();
  const expiryTimer = setInterval(() => {
    let renew = false;
    for (const row of sessions.values()) {
      if (row.expiresAt <= Date.now() || row.accessExpiresAt <= Date.now())
        killSession(row);
      else if (row.accessExpiresAt <= Date.now() + 30000) renew = true;
    }
    // Identity renewal belongs to live browser sessions, even if Team never
    // starts. The broker owns refresh single-flight and failure-closed cleanup.
    if (renew && !closed) void getAccess().catch(() => {});
  }, 1000);
  expiryTimer.unref();
  /** @type {boolean} */
  let closed = false,
    disposed = false,
    generation = 0,
    online = false;
  /** @type {ReturnType<typeof createTeam>|undefined} */
  let team;
  /** @type {ReturnType<typeof createTeam>|undefined} */
  let startingTeam;
  let communication = 'idle';
  /** @type {Promise<void>|undefined} */
  let teamStarting;
  /** @type {Promise<void>|undefined} */
  let revoking;
  async function drainRevocations() {
    if (closed) return;
    if (revoking) return revoking;
    revoking = (async () => {
      for (const entry of vault.snapshot().revocations ?? []) {
        if (
          entry.platformOrigin !== config.platformOrigin ||
          entry.clientId !== config.clientId
        )
          continue;
        try {
          await platform.request('/oauth2/revoke', {
            method: 'POST',
            form: true,
            body: { client_id: entry.clientId, token: entry.token },
            signal: AbortSignal.timeout(3000),
          });
          if (closed) return;
          await vault.update((s) => {
            s.revocations = (s.revocations ?? []).filter(
              (/** @type {any} */ r) => r.id !== entry.id,
            );
          });
        } catch {
          return;
        }
      }
    })();
    try {
      await revoking;
    } finally {
      revoking = undefined;
    }
  }
  const revokeTimer = setInterval(() => {
    void drainRevocations().catch(() => {});
  }, 10000);
  revokeTimer.unref();
  void drainRevocations().catch(() => {});
  const active = (g = generation) => {
    if (closed || g !== generation)
      throw new Error('local connection superseded');
  };
  let finalizing = Promise.resolve();
  const resolve = (/** @type {any} */ request) => {
    if (closed) return null;
    const bearer = cookie(request, SESSION_COOKIE);
    if (!bearer || bearer.length !== 43) return null;
    const row = sessions.get(hash(bearer));
    if (
      !row ||
      row.expiresAt <= Date.now() ||
      row.accessExpiresAt <= Date.now()
    )
      return null;
    return row;
  };
  const killSession = (/** @type {any} */ row) => {
    sessions.delete(row.key);
    httpResponses.close(row.key);
    for (const socket of sockets.get(row.key) ?? []) socket.destroy();
    sockets.delete(row.key);
  };
  const httpResponses = createHttpResponseTracker(resolve);
  const network = createNetworkLifecycle({
    directory: join(directory, 'network'),
    installation: config.network,
    defaultProvider: config.network
      ? createDeviceProvider({
          config,
          async getDevice(signal) {
            const g = generation,
              saved = vault.snapshot().credential;
            const accessToken = await getAccess();
            active(g);
            signal.throwIfAborted();
            if (!saved) throw new Error('device credential required');
            const claims = await platform.verifyDevice(
              accessToken,
              saved.claims,
            );
            active(g);
            signal.throwIfAborted();
            if (
              ['tid', 'sub', 'did'].some(
                (key) =>
                  vault.snapshot().credential?.claims[key] !== claims[key],
              )
            )
              throw new Error('device credential changed');
            return { accessToken, claims };
          },
        })
      : undefined,
    ctx,
    getAccess,
    authorizeLocal: async (request) => !!resolve(request),
    onRevoked: () => {
      broker.invalidate('revoked');
      models.logout();
      for (const row of sessions.values()) killSession(row);
      team?.close();
      startingTeam?.close();
      team = undefined;
      online = false;
      communication = 'unavailable';
    },
  });
  const broker = createRefreshBroker({
    vault,
    platform,
    config,
    onPlatformAccess(claims) {
      for (const row of sessions.values())
        row.accessExpiresAt = claims.exp * 1000;
    },
    onUncertain() {
      models.logout();
      network.logout();
      team?.close();
      startingTeam?.close();
      team = undefined;
      online = false;
      communication = 'unavailable';
      for (const row of sessions.values()) killSession(row);
    },
  });
  const models = createModelLifecycle({
    broker,
    origin: config.platformOrigin,
  });
  /** @type {(()=>void)|undefined} */
  let stopHttpTracking;
  /** @type {Promise<void>|undefined} */
  let networkClosing;
  async function getAccess() {
    active();
    return broker.getPlatformAccess();
  }
  /** @param {any} tokens @param {any} [expected] */
  async function validateTokens(tokens, expected) {
    if (
      tokens.token_type?.toLowerCase() !== 'bearer' ||
      !tokens.refresh_token ||
      typeof tokens.refresh_token !== 'string' ||
      tokens.scope !== 'auriga.connect'
    )
      throw new Error('invalid device token response');
    const claims = await platform.verifyDevice(tokens.access_token, expected);
    return {
      accessToken: tokens.access_token,
      refreshToken: tokens.refresh_token,
      claims,
      platformOrigin: config.platformOrigin,
      issuer: config.issuer,
      clientId: config.clientId,
    };
  }
  function attemptFor(/** @type {any} */ request) {
    const id = cookie(request, ATTEMPT_COOKIE),
      row = attempts.get(id);
    if (!row || row.expiresAt <= Date.now())
      throw new Error('login attempt expired');
    return row;
  }
  const setCookie = (
    /** @type {string} */ name,
    /** @type {string} */ value,
    /** @type {number|undefined} */ maxAge = undefined,
  ) =>
    `${name}=${value}; Path=/; HttpOnly; SameSite=Strict${ctx.webServer.protocol === 'https:' ? '; Secure' : ''}${maxAge === undefined ? '' : '; Max-Age=' + maxAge}`;
  async function abandon(/** @type {any} */ row) {
    return abandonNativeAttempt(row, async () => {
      if (!row.transactionId) return;
      // Only an uncommitted attempt compensates its own cookie/CSRF session.
      await platform
        .request(
          row.completed
            ? '/api/iam/v1/logout'
            : '/api/iam/v1/login/transactions/' +
                encodeURIComponent(row.transactionId),
          {
            method: row.completed ? 'POST' : 'DELETE',
            body: row.completed ? {} : undefined,
            jar: row.jar,
            signal: AbortSignal.timeout(3000),
          },
        )
        .catch(() => {});
    });
  }
  async function startTeam(assertAttempt = () => {}) {
    if (team) {
      await team.verifyOnline();
      assertAttempt();
      return;
    }
    if (
      !ctx.tools?.register ||
      !ctx.sessionController?.create ||
      !ctx.sessionController?.prompt ||
      !ctx.sessions?.get ||
      !ctx.sessions?.flush
    )
      throw new Error('rc.3 tools and sessionController required');
    const g = generation;
    let starting = true;
    const candidate = createTeam({
      platform,
      vault,
      getAccess,
      assertActive: () => {
        active(g);
        if (starting) assertAttempt();
      },
      ctx,
      label: config.deviceName ?? 'Auriga DSH',
      onState(state) {
        if (closed || generation !== g) return;
        online = state.connected;
        communication = state.connected
          ? team
            ? 'ready'
            : 'connecting'
          : 'unavailable';
        if (state.unauthorized) {
          team?.close();
          team = undefined;
        }
      },
    });
    startingTeam = candidate;
    try {
      await candidate.start();
      active(g);
      assertAttempt();
      starting = false;
      team = candidate;
      communication = 'ready';
    } catch (error) {
      candidate.close();
      throw error;
    } finally {
      if (startingTeam === candidate) startingTeam = undefined;
    }
  }
  function connectTeam(verify = false) {
    if (teamStarting || closed || (!verify && team && online)) return;
    if (team && !online) {
      team.close();
      team = undefined;
    }
    const g = generation;
    communication = 'connecting';
    teamStarting = startTeam()
      .catch(() => {
        if (!closed && generation === g) {
          online = false;
          communication = 'unavailable';
        }
      })
      .finally(() => {
        teamStarting = undefined;
      });
  }
  function issueBrowserSession(
    /** @type {any} */ row,
    /** @type {any} */ request,
    /** @type {any} */ credential,
  ) {
    const bearer = random(),
      key = hash(bearer),
      session = {
        key,
        subject: credential.claims.sub,
        tenantId: credential.claims.tid,
        deviceId: credential.claims.did,
        csrf: random(),
        expiresAt: Date.now() + 1800000,
        accessExpiresAt: credential.claims.exp * 1000,
      };
    sessions.set(key, session);
    models.login();
    // Native identity is verified and durably owned before session issuance.
    // Independent N1 must not wait for legacy Team/MCP availability.
    network.login();
    connectTeam(true);
    row.jar.clear();
    row.verifier = undefined;
    row.device = undefined;
    attempts.delete(cookie(request, ATTEMPT_COOKIE));
    return {
      step: { type: 'complete', redirectTo: '/' },
      setCookie: [
        setCookie(SESSION_COOKIE, bearer),
        setCookie(ATTEMPT_COOKIE, '', 0),
      ],
    };
  }
  /** Trusted settings changes also use the normal tombstone/revocation path.
   * @param {any} [row] */
  async function clearAuthentication(row) {
    const owns = (/** @type {any} */ credential) =>
      !row ||
      (credential?.claims.sub === row.subject &&
        credential?.claims.tid === row.tenantId &&
        credential?.claims.did === row.deviceId);
    if (!owns(vault.snapshot().credential))
      throw new Error('local device ownership changed');
    generation++;
    broker.invalidate('signed_out');
    models.logout();
    network.logout();
    const abandoned = [...attempts.values()];
    attempts.clear();
    for (const attempt of abandoned) attempt.controller.abort();
    for (const session of sessions.values()) killSession(session);
    // Close access immediately; persist the tombstone before reporting logout success.
    team?.close();
    startingTeam?.close();
    team = undefined;
    online = false;
    communication = 'idle';
    try {
      await vault.update((s) => {
        if (!owns(s.credential))
          throw new Error('local device ownership changed');
        if (s.credential) {
          s.revocations ??= [];
          if (s.revocations.length >= 256)
            throw new Error('revocation queue full');
          s.revocations.push({
            id: random(),
            platformOrigin:
              s.credential.platformOrigin ??
              new URL(s.credential.claims.iss).origin,
            clientId: s.credential.clientId ?? config.clientId,
            token: s.credential.refreshToken,
          });
        }
        s.credential = null;
        s.endpoint = null;
        s.threads = {};
        s.deliveries = {};
        s.pendingOpens = {};
        s.openOutcomes = {};
      });
    } catch {
      closed = true;
      clearInterval(expiryTimer);
      clearInterval(revokeTimer);
      for (const value of sessions.values()) killSession(value);
      throw new Error('logout persistence failed');
    }
    for (const attempt of abandoned) await abandon(attempt);
    await drainRevocations();
  }
  return {
    resetAuthentication: clearAuthentication,
    config,
    resolve,
    getAccess,
    vault,
    attachModels: models.attach,
    get modelStatus() {
      return models.status;
    },
    attachNetwork: network.attach,
    consentUI: network.consentUI,
    messagingUI: network.messagingUI,
    get networkStatus() {
      return network.status;
    },
    /** @param {import('node:http').IncomingMessage} request @param {import('node:http').ServerResponse} response */
    bindHttpResponse(request, response) {
      return httpResponses.bind(request, response);
    },
    installHttpTracking() {
      if (stopHttpTracking)
        throw new Error('native HTTP tracking already installed');
      const remove = installHttpResponseTracking(ctx, (req, res) =>
        httpResponses.bind(req, res),
      );
      let removed = false;
      stopHttpTracking = () => {
        if (removed) return;
        removed = true;
        remove();
        stopHttpTracking = undefined;
      };
      return stopHttpTracking;
    },
    isReady() {
      return !!team && online && !closed;
    },
    checkCsrf(/** @type {any} */ request) {
      const token = request.headers['x-auriga-csrf'];
      const row =
        resolve(request) ?? attempts.get(cookie(request, ATTEMPT_COOKIE));
      return !!row && equal(token, row.csrf);
    },
    status(/** @type {any} */ request) {
      const row = resolve(request);
      return {
        available: true,
        loggedIn: !!row,
        connected: !!row && !!team && online,
        communication: row ? communication : 'idle',
        models: models.status,
        network: row
          ? network.status
          : { available: false, connected: false, state: 'idle' },
        account: row ? { subject: row.subject } : null,
        csrf: row?.csrf,
        remoteRevocation: (vault.snapshot().revocations ?? []).length
          ? 'pending'
          : 'complete',
        connection: row
          ? {
              endpointId: vault.snapshot().endpoint,
              received: Object.keys(vault.snapshot().deliveries).filter(
                (id) => vault.snapshot().deliveries[id].accepted,
              ).length,
              unsupported: Object.values(vault.snapshot().deliveries).filter(
                (/** @type {any} */ delivery) =>
                  delivery.status === 'unsupported',
              ).length,
              pendingSourceOpens: Object.keys(
                vault.snapshot().pendingOpens ?? {},
              ).length,
              unknownSourceOpens: Object.keys(
                vault.snapshot().openOutcomes ?? {},
              ).length,
            }
          : null,
      };
    },
    prepare(/** @type {any} */ request) {
      active();
      const current = resolve(request);
      if (current) {
        // An authenticated page/status refresh is the explicit retry trigger;
        // communication retries never hold up identity or mint a new session.
        connectTeam();
        return { body: this.status(request) };
      }
      let id = cookie(request, ATTEMPT_COOKIE),
        row = attempts.get(id);
      if (!row || row.expiresAt <= Date.now()) {
        for (const [key, value] of attempts)
          if (value.expiresAt <= Date.now()) {
            value.controller.abort();
            value.jar.clear();
            attempts.delete(key);
          }
        if (attempts.size >= 32) throw new Error('too many login attempts');
        id = random();
        row = {
          csrf: random(),
          jar: new LoginJar(),
          controller: new AbortController(),
          version: 0,
          expiresAt: Date.now() + 300000,
        };
        attempts.set(id, row);
      }
      return {
        setCookie: setCookie(ATTEMPT_COOKIE, id ?? '', 300),
        body: {
          ...this.status(request),
          csrf: row.csrf,
          resumeAvailable:
            !!vault.snapshot().credential &&
            !broker.status.reauthenticationRequired,
        },
      };
    },
    async login(/** @type {any} */ request, /** @type {any} */ input) {
      const row = attemptFor(request),
        g = generation;
      if (row.busy) throw new Error('login attempt busy');
      row.busy = true;
      const version = ++row.version;
      const check = () => {
        active(g);
        if (
          row.version !== version ||
          row.controller.signal.aborted ||
          row.expiresAt <= Date.now()
        )
          throw new Error('login attempt superseded');
      };
      try {
        if (input.action === 'resume') {
          const saved = vault.snapshot().credential;
          if (!saved) throw new Error('no device credential');
          const token = await getAccess();
          check();
          await platform.verifyDevice(token, saved.claims);
          check();
          return issueBrowserSession(row, request, vault.snapshot().credential);
        }
        let transaction;
        if (input.action === 'begin') {
          if (row.transactionId)
            throw new Error('restart login with a new attempt');
          row.verifier = randomBytes(48).toString('base64url');
          transaction = await platform.request(
            '/api/iam/v1/login/transactions',
            {
              method: 'POST',
              jar: row.jar,
              signal: row.controller.signal,
              body: {
                providerId: config.providerId,
                purpose: 'login',
                returnTo: '/',
                nativeConnect: {
                  clientId: config.clientId,
                  codeChallenge: createHash('sha256')
                    .update(row.verifier)
                    .digest('base64url'),
                  codeChallengeMethod: 'S256',
                  ...(config.deviceName
                    ? { deviceName: config.deviceName }
                    : {}),
                },
              },
            },
          );
          check();
          if (
            !/^[A-Za-z0-9_-]{1,128}$/.test(transaction.id) ||
            typeof transaction.nativeDevice?.deviceCode !== 'string' ||
            !transaction.nativeDevice.deviceCode ||
            transaction.nativeDevice.deviceCode.length > 2048 ||
            !Number.isFinite(Date.parse(transaction.nativeDevice.expiresAt)) ||
            !Number.isSafeInteger(transaction.nativeDevice.interval) ||
            transaction.nativeDevice.interval < 1 ||
            transaction.nativeDevice.interval > 60
          )
            throw new Error('native login response incomplete');
          row.transactionId = transaction.id;
          row.device = transaction.nativeDevice;
          row.nextPollAt = Date.now() + row.device.interval * 1000;
        } else if (
          input.action === 'advance' &&
          row.transactionId &&
          input.input?.type === 'form'
        ) {
          transaction = await platform.request(
            '/api/iam/v1/login/transactions/' +
              encodeURIComponent(row.transactionId) +
              '/advance',
            {
              method: 'POST',
              jar: row.jar,
              signal: row.controller.signal,
              body: input.input,
            },
          );
          check();
        } else throw new Error('invalid login input');
        if (
          transaction.id !== row.transactionId ||
          !['form', 'fail', 'complete'].includes(transaction.step?.type)
        )
          throw new Error('unsupported native login step');
        if (transaction.step.type !== 'complete')
          return { step: transaction.step };
        if (
          transaction.status !== 'completed' ||
          Date.parse(row.device.expiresAt) <= Date.now()
        )
          throw new Error('native login not complete');
        row.completed = true;
        // One exchange only. A lost response requires another real login, never token replay.
        let tokens,
          interval = row.device.interval;
        for (;;) {
          check();
          const wait = Math.max(0, row.nextPollAt - Date.now());
          if (wait)
            await delay(wait, undefined, { signal: row.controller.signal });
          check();
          try {
            tokens = await platform.request('/oauth2/token', {
              method: 'POST',
              form: true,
              jar: row.jar,
              signal: row.controller.signal,
              body: {
                grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
                client_id: config.clientId,
                device_code: row.device.deviceCode,
                code_verifier: row.verifier,
              },
            });
            break;
          } catch (error) {
            const code = /** @type {any} */ (error).code;
            if (!['authorization_pending', 'slow_down'].includes(code))
              throw error;
            if (code === 'slow_down') interval += 5;
            row.nextPollAt = Date.now() + interval * 1000;
            if (
              row.nextPollAt >=
              Math.min(row.expiresAt, Date.parse(row.device.expiresAt))
            )
              throw new Error('native device grant expired');
          }
        }
        check();
        const credential = await validateTokens(tokens);
        check();
        const finish = finalizing.then(async () => {
          check();
          await broker.commitLogin(() =>
            persistNativeAttempt(row, () =>
              vault.update((state) => {
                check();
                const old = state.credential;
                if (
                  old &&
                  ((old.platformOrigin !== undefined &&
                    old.platformOrigin !== config.platformOrigin) ||
                    old.claims.iss !== config.issuer ||
                    (old.clientId !== undefined &&
                      old.clientId !== config.clientId))
                )
                  throw new Error(
                    'platform binding changed; platform switching is unavailable',
                  );
                if (
                  old &&
                  (old.claims.tid !== credential.claims.tid ||
                    old.claims.sub !== credential.claims.sub)
                )
                  throw new Error('local owner conflict');
                if (old && old.claims.did !== credential.claims.did) {
                  if (
                    !old.refreshPending &&
                    !broker.status.reauthenticationRequired
                  )
                    throw new Error(
                      'sign out existing device before replacing connection',
                    );
                  // A complete fresh native login may recover an uncertain
                  // exchange for the SAME owner, retaining remote compensation.
                  state.revocations ??= [];
                  if (state.revocations.length >= 256)
                    throw new Error('revocation queue full');
                  state.revocations.push({
                    id: random(),
                    platformOrigin: config.platformOrigin,
                    clientId: config.clientId,
                    token: old.refreshToken,
                  });
                  state.endpoint = null;
                  state.threads = {};
                  state.deliveries = {};
                  state.pendingOpens = {};
                  state.openOutcomes = {};
                }
                state.credential = credential;
              }),
            ),
          );
          check();
        });
        finalizing = finish.catch(() => {});
        await finish;
        check();
        return issueBrowserSession(row, request, credential);
      } catch (error) {
        await abandon(row);
        attempts.delete(cookie(request, ATTEMPT_COOKIE));
        throw error;
      } finally {
        row.busy = false;
      }
    },
    async cancelAttempt(/** @type {any} */ request) {
      const id = cookie(request, ATTEMPT_COOKIE),
        row = attempts.get(id);
      if (!row) return;
      attempts.delete(id);
      await abandon(row);
    },
    bindSocket(/** @type {any} */ request) {
      const row = resolve(request);
      if (!row) return false;
      if (request.headers.upgrade?.toLowerCase() !== 'websocket') return true;
      let set = sockets.get(row.key);
      if (!set) sockets.set(row.key, (set = new Set()));
      const socket = request.socket;
      if (socket && !set.has(socket)) {
        set.add(socket);
        socket.once('close', () => set.delete(socket));
      }
      return true;
    },
    async logout(/** @type {any} */ request) {
      const row = resolve(request);
      if (!row) {
        await this.cancelAttempt(request);
        return {
          setCookie: setCookie(ATTEMPT_COOKIE, '', 0),
          body: this.status(request),
        };
      }
      await clearAuthentication(row);
      return {
        setCookie: [
          setCookie(SESSION_COOKIE, '', 0),
          setCookie(ATTEMPT_COOKIE, '', 0),
        ],
        body: {
          available: true,
          loggedIn: false,
          connected: false,
          account: null,
          remoteRevocation: (vault.snapshot().revocations ?? []).length
            ? 'pending'
            : 'complete',
        },
      };
    },
    close() {
      if (disposed) return networkClosing;
      disposed = true;
      closed = true;
      broker.invalidate('closed');
      models.close();
      stopHttpTracking?.();
      clearInterval(expiryTimer);
      clearInterval(revokeTimer);
      generation++;
      networkClosing = network.close();
      team?.close();
      startingTeam?.close();
      for (const value of sessions.values()) killSession(value);
      for (const row of attempts.values()) {
        row.controller.abort();
        row.jar.clear();
      }
      attempts.clear();
      vault.close();
      return networkClosing;
    },
  };
}
