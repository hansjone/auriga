import {
  registerBaseTools,
  readDeviceLedger,
  connectionReason,
} from './base-tools.js';
import { open, constants } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import {
  createPlatform,
  platformConfig,
  PlatformError,
} from '../local/platform.js';
import { logEvent, newTraceparent } from '../observability/index.js';
import {
  createDeviceProvider,
  isDeviceRevocationError,
} from './device-provider.js';
import { createNetworkLifecycle } from './lifecycle.js';
import { networkIsolation } from './isolation.js';
import { parseStrictJson } from './trust/jcs.js';
import { deviceMemberName } from '../local/member-profile.js';
/* global AbortSignal */

/** One managed credential owns the same lifecycle/provider/runtime as native login.
 * Only renderer-owned file material can select identity or installation pins.
 * @param {{file:string,directory:string,tenantId:string,ownerId:string,instanceId:string,ctx:any,
 * authorizeLocal?:(context:unknown)=>Promise<boolean>,createPlatform?:typeof createPlatform,
 * createLifecycle?:typeof createNetworkLifecycle}} options */
export function createManagedNetwork(options) {
  let closed = false;
  let generation = 0;
  let raw = '';
  let rejected = '';
  /** @type {ReturnType<typeof createNetworkLifecycle>|undefined} */
  let lifecycle;
  /** @type {{accessToken:string,claims:any}|undefined} */
  let access;
  /** Resource-only server adapter; never exposed to the browser or public SDK.
   * @type {{config:{platformOrigin:string},getAccess:()=>Promise<string>}|undefined} */
  let resourceSession;
  /** @type {(()=>void)|undefined} */
  let detach;
  /** @type {import('./runtime-types.js').NetworkProvider|undefined} */
  let attached;
  /** @type {NodeJS.Timeout|undefined} */
  let timer;
  /** @type {Promise<void>|undefined} */
  let refreshing;
  let retryAt = 0;
  let failures = 0;
  /** @type {string|undefined} */
  let displayName;
  let profileRetryAt = 0;
  let materialUntil = 0;
  let interrupted = false;
  /** @type {string|null} */ let lastSuccessAt = null;
  /** @type {boolean|null} */
  let reachable = null;
  let failureReason = 'unknown';
  /** @type {any} */ let diagnosticConfig;
  /** @type {any} */ let verifiedCredential;
  let localDirectory = '';
  const disposeBase = options.ctx.tools?.register
    ? registerBaseTools({
        ctx: options.ctx,
        getState: () => ({
          origin: diagnosticConfig?.platformOrigin,
          reachable,
          lastSuccessAt,
          reason: failureReason,
          verified: !!access && raw !== rejected && !closed,
          expiresAt: access
            ? Math.min(access.claims.exp * 1000, materialUntil)
            : materialUntil || null,
          memberId: access?.claims.sub,
          displayName,
          network: lifecycle?.status,
          remoteAvailable: false,
        }),
        readLocal: () => readDeviceLedger(localDirectory, verifiedCredential),
        probe: (path, signal) => {
          if (!diagnosticConfig) throw new Error('unconfigured');
          return (options.createPlatform ?? createPlatform)(
            diagnosticConfig,
          ).request(path, { signal });
        },
      })
    : () => {};
  /** @type {AbortController|undefined} */
  let controller;
  let retiring = Promise.resolve();
  /** @type {Promise<void>|undefined} */
  let closing;
  /** @type {(renew?:boolean)=>Promise<{accessToken:string,claims:any}>} */
  let getDevice = async () => {
    throw new Error('managed credential unavailable');
  };

  async function readMaterial() {
    if (!isAbsolute(options.file))
      throw new Error('absolute credential file required');
    const file = await open(
      options.file,
      constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
    );
    try {
      const stat = await file.stat();
      if (
        !stat.isFile() ||
        stat.size > 65536 ||
        (process.platform !== 'win32' && (stat.mode & 0o077) !== 0)
      )
        throw new Error('private managed credential file required');
      return await file.readFile('utf8');
    } finally {
      await file.close();
    }
  }

  /** @param {string} next */
  function validate(next) {
    const value =
      /** @type {import('../generated/managed-device.js').ManagedDeviceCredential} */ (
        parseStrictJson(Buffer.from(next))
      );
    if (
      !value ||
      typeof value !== 'object' ||
      Array.isArray(value) ||
      Object.keys(value).sort().join(',') !==
        'credential,deviceId,expiresAt,instanceId,issuer,network,ownerId,platformOrigin,tenantId' ||
      value.tenantId !== options.tenantId ||
      value.ownerId !== options.ownerId ||
      value.instanceId !== options.instanceId ||
      ![value.tenantId, value.ownerId, value.instanceId, value.deviceId].every(
        (x) =>
          typeof x === 'string' &&
          /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(x),
      ) ||
      typeof value.credential !== 'string' ||
      !/^[A-Za-z0-9_-]{43}$/.test(value.credential) ||
      typeof value.expiresAt !== 'string' ||
      !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(
        value.expiresAt,
      ) ||
      !Number.isFinite(Date.parse(value.expiresAt))
    )
      throw new Error('invalid managed credential binding');
    const config = platformConfig({
      platformOrigin: value.platformOrigin,
      issuer: value.issuer,
      clientId: 'auriga-managed-network',
      providerId: 'managed',
      network: value.network,
    });
    return { value, config };
  }

  function retire() {
    resourceSession = undefined;
    generation++;
    controller?.abort();
    controller = undefined;
    access = undefined;
    profileRetryAt = 0;
    interrupted = false;
    const previous = lifecycle;
    lifecycle = undefined;
    detach = undefined;
    retiring = Promise.allSettled([retiring, previous?.close()]).then(() => {});
    return retiring;
  }

  /** @param {boolean} [force] */
  function refresh(force = false) {
    if (refreshing) return refreshing;
    const operation = Promise.resolve().then(async () => {
      if (closed) return;
      let sameVerifiedMaterial = false;
      try {
        const next = await readMaterial();
        if (closed) return;
        if (next === rejected) return;
        if (next === raw && materialUntil <= Date.now()) {
          rejected = raw;
          access = undefined;
          lifecycle?.logout();
          return;
        }
        if (
          next === raw &&
          Date.now() < retryAt &&
          !force &&
          (!access || access.claims.exp * 1000 > Date.now())
        )
          return;
        if (next !== raw || !lifecycle) {
          await retire();
          if (closed) return;
          const { value, config } = validate(next);
          diagnosticConfig = config;
          localDirectory = join(options.directory, 'network', value.deviceId);
          materialUntil = Date.parse(value.expiresAt);
          if (next !== raw) displayName = undefined;
          raw = next;
          rejected = '';
          if (materialUntil <= Date.now()) {
            rejected = raw;
            return;
          }
          const expected = generation;
          controller = new AbortController();
          const signal = controller.signal;
          const platform = (options.createPlatform ?? createPlatform)(config, {
            signal,
          });
          let pending;
          getDevice = async (renew = false) => {
            if (
              closed ||
              expected !== generation ||
              raw === rejected ||
              materialUntil <= Date.now()
            )
              throw new PlatformError(401, 'network.revoked');
            if (
              !renew &&
              access &&
              access.claims.exp * 1000 > Date.now() &&
              (access.claims.exp * 1000 > Date.now() + 30000 ||
                Date.now() < retryAt)
            )
              return access;
            const trace = newTraceparent();
            pending ??= (async () => {
              const response = await platform
                .request('/oauth2/token', {
                  signal,
                  method: 'POST',
                  form: true,
                  body: {
                    grant_type:
                      'urn:auriga:params:oauth:grant-type:managed-device',
                    client_id: 'auriga-managed-network',
                    instance_id: value.instanceId,
                    owner_id: value.ownerId,
                    credential: value.credential,
                  },
                })
                .catch((error) => {
                  // Only the credential authority can revoke the material; a
                  // JWKS denial is a verification outage, regardless of its code.
                  if (
                    !closed &&
                    expected === generation &&
                    isDeviceRevocationError(error)
                  ) {
                    rejected = raw;
                    interrupted = true;
                    lifecycle?.logout();
                    access = undefined;
                  }
                  throw error;
                });
              signal.throwIfAborted();
              if (
                response.token_type !== 'Bearer' ||
                response.scope !== 'auriga.connect' ||
                !Number.isSafeInteger(response.expires_in) ||
                response.expires_in < 1 ||
                response.expires_in > 300
              )
                throw new Error('invalid managed device response');
              const claims = await platform
                .verifyDevice(response.access_token, {
                  tid: value.tenantId,
                  sub: value.ownerId,
                  did: value.deviceId,
                })
                .catch((error) => {
                  if (!closed && expected === generation) {
                    access = undefined;
                    interrupted = true;
                    lifecycle?.logout();
                  }
                  // A key-server response is never the credential authority.
                  const status = /** @type {any} */ (error)?.status;
                  throw new PlatformError(
                    Number.isInteger(status) ? status : 503,
                  );
                });
              if (closed || expected !== generation)
                throw new PlatformError(401, 'network.revoked');
              access = { accessToken: response.access_token, claims };
              verifiedCredential = { claims };
              reachable = true;
              lastSuccessAt = new Date().toISOString();
              return access;
            })();
            try {
              return await pending;
            } catch (error) {
              if (closed || expected !== generation) throw error;
              const status = /** @type {any} */ (error)?.status;
              if (failures === 0)
                logEvent('WARN', 'Managed credential check failed', {
                  trace_id: trace.slice(3, 35),
                  classification: raw === rejected ? 'revoked' : 'temporary',
                  http_status: [401, 403, 429, 503].includes(status)
                    ? status
                    : 0,
                });
              if (
                status === 429 &&
                access &&
                access.claims.exp * 1000 > Date.now()
              ) {
                retryAt =
                  Date.now() +
                  Math.min(30000, 1000 * 2 ** Math.min(failures++, 5));
                return access;
              }
              if (status === 401 || status === 403) {
                interrupted = true;
                lifecycle?.logout();
                access = undefined;
              }
              throw error;
            } finally {
              pending = undefined;
            }
          };
          await getDevice();
          if (closed || expected !== generation) return;
          const resourceDevice = getDevice;
          resourceSession = {
            config: { platformOrigin: config.platformOrigin },
            async getAccess() {
              const device = await resourceDevice();
              signal.throwIfAborted();
              return device.accessToken;
            },
          };
          lifecycle = (options.createLifecycle ?? createNetworkLifecycle)({
            directory: join(options.directory, 'network', value.deviceId),
            ctx: options.ctx,
            installation: config.network,
            defaultProvider: createDeviceProvider({
              config,
              getDevice: async (signal) => {
                const device = await getDevice();
                signal.throwIfAborted();
                return device;
              },
            }),
            getAccess: async () => (await getDevice()).accessToken,
            authorizeLocal: options.authorizeLocal ?? (async () => false),
            onRevoked() {
              if (closed || expected !== generation || raw === rejected) return;
              // Gateway failure-grace exhaustion can also report revocation.
              // Fence first, then let the next IAM grant adjudicate the material.
              void retire().catch(() => {});
              interrupted = true;
              retryAt =
                Date.now() +
                Math.min(30000, 1000 * 2 ** Math.min(failures++, 5));
            },
          });
          if (attached) detach = lifecycle.attach(attached);
          interrupted = false;
          lifecycle.login();
        } else {
          validate(next);
          sameVerifiedMaterial = true;
          await getDevice(force);
          if (interrupted) {
            interrupted = false;
            lifecycle.login();
          }
        }
        if (Date.now() >= retryAt) {
          failures = 0;
          retryAt = 0;
        }
        if (Date.now() >= profileRetryAt && access) {
          profileRetryAt = Date.now() + 60000;
          const expected = generation;
          const { config } = validate(next);
          const signal = controller?.signal;
          const platform = (options.createPlatform ?? createPlatform)(config, {
            signal,
          });
          void deviceMemberName(
            platform,
            access.accessToken,
            options.ownerId,
            AbortSignal.any([
              ...(signal ? [signal] : []),
              AbortSignal.timeout(4000),
            ]),
          )
            .then((name) => {
              if (!closed && expected === generation) displayName = name;
            })
            .catch(() => {});
        }
      } catch (error) {
        reachable = false;
        failureReason = connectionReason(error);
        // Transport failure is not revocation. Keep only an unexpired verified
        // token for the unchanged valid file; never extend its deadline.
        if (
          sameVerifiedMaterial &&
          raw !== rejected &&
          access &&
          access.claims.exp * 1000 > Date.now() &&
          materialUntil > Date.now()
        ) {
          retryAt =
            Date.now() + Math.min(30000, 1000 * 2 ** Math.min(failures++, 5));
          return;
        }
        lifecycle?.logout();
        access = undefined;
        // Retry transient outage, but never resurrect a proven revoked file.
        if (raw !== rejected) await retire();
        retryAt =
          Date.now() + Math.min(30000, 1000 * 2 ** Math.min(failures++, 5));
      }
    });
    refreshing = operation.finally(() => {
      refreshing = undefined;
    });
    return refreshing;
  }

  return {
    get resourceSession() {
      return !closed &&
        raw !== rejected &&
        access &&
        access.claims.exp * 1000 > Date.now() &&
        materialUntil > Date.now()
        ? resourceSession
        : undefined;
    },
    get sessionRevoked() {
      return !!raw && (raw === rejected || materialUntil <= Date.now());
    },
    get sessionAvailable() {
      return (
        !closed &&
        raw !== rejected &&
        !!access &&
        access.claims.exp * 1000 > Date.now() &&
        materialUntil > Date.now()
      );
    },
    get consentAvailable() {
      return !closed && lifecycle?.consentAvailable === true;
    },
    get account() {
      return {
        subject: options.ownerId,
        ...(displayName ? { displayName } : {}),
      };
    },
    refresh,
    start() {
      if (timer || closed) return;
      void refresh();
      timer = setInterval(() => {
        void refresh();
      }, 1000);
      timer.unref();
    },
    /** @param {import('./runtime-types.js').NetworkProvider} provider */
    attach(provider) {
      attached = provider;
      detach?.();
      detach = lifecycle?.attach(provider);
      return () => {
        if (attached !== provider) return;
        attached = undefined;
        detach?.();
        detach = undefined;
      };
    },
    logout() {
      rejected = raw;
      lifecycle?.logout();
      access = undefined;
    },
    get status() {
      return rejected === raw || !lifecycle || interrupted
        ? {
            available: false,
            connected: false,
            state: 'unavailable',
            reason: rejected && rejected === raw ? 'revoked' : 'unavailable',
            isolation: networkIsolation,
          }
        : lifecycle.status;
    },
    consentUI(/** @type {unknown} */ context) {
      if (!lifecycle) throw new Error('managed network unavailable');
      return lifecycle.consentUI(context);
    },
    messagingUI(/** @type {unknown} */ context) {
      if (!lifecycle) throw new Error('managed network unavailable');
      return lifecycle.messagingUI(context);
    },
    close() {
      if (closing) return closing;
      closed = true;
      disposeBase();
      clearInterval(timer);
      // Retire immediately, even if an exchange/verification response is pending.
      closing = retire();
      return closing;
    },
  };
}
