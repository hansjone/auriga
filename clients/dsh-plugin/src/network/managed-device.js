import { open, constants } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import {
  createPlatform,
  platformConfig,
  PlatformError,
} from '../local/platform.js';
import { createDeviceProvider } from './device-provider.js';
import { createNetworkLifecycle } from './lifecycle.js';
import { networkIsolation } from './isolation.js';
import { parseStrictJson } from './trust/jcs.js';

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
  /** @type {()=>Promise<{accessToken:string,claims:any}>} */
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
      !Number.isFinite(Date.parse(value.expiresAt)) ||
      Date.parse(value.expiresAt) <= Date.now()
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

  async function retire() {
    generation++;
    access = undefined;
    const previous = lifecycle;
    lifecycle = undefined;
    detach = undefined;
    await previous?.close();
  }

  /** @param {boolean} [force] */
  function refresh(force = false) {
    if (refreshing) return refreshing;
    const operation = Promise.resolve().then(async () => {
      if (closed) return;
      try {
        const next = await readMaterial();
        if (closed) return;
        if (next === rejected) return;
        if (next === raw && Date.now() < retryAt && !force) return;
        if (next !== raw || !lifecycle) {
          await retire();
          if (closed) return;
          const { value, config } = validate(next);
          raw = next;
          rejected = '';
          const expected = generation;
          const platform = (options.createPlatform ?? createPlatform)(config);
          let pending;
          getDevice = async () => {
            if (closed || expected !== generation)
              throw new PlatformError(401, 'network.revoked');
            if (access && access.claims.exp * 1000 > Date.now() + 30000)
              return access;
            pending ??= (async () => {
              const response = await platform.request('/oauth2/token', {
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
              });
              if (
                response.token_type !== 'Bearer' ||
                response.scope !== 'auriga.connect' ||
                !Number.isSafeInteger(response.expires_in) ||
                response.expires_in < 1 ||
                response.expires_in > 300
              )
                throw new Error('invalid managed device response');
              const claims = await platform.verifyDevice(
                response.access_token,
                {
                  tid: value.tenantId,
                  sub: value.ownerId,
                  did: value.deviceId,
                },
              );
              if (closed || expected !== generation)
                throw new PlatformError(401, 'network.revoked');
              access = { accessToken: response.access_token, claims };
              return access;
            })();
            try {
              return await pending;
            } catch (error) {
              const status = /** @type {any} */ (error)?.status;
              if (status === 401 || status === 403) {
                rejected = raw;
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
              rejected = raw;
              access = undefined;
            },
          });
          if (attached) detach = lifecycle.attach(attached);
          lifecycle.login();
        } else {
          validate(next);
          if (force) access = undefined;
          await getDevice();
        }
        failures = 0;
        retryAt = 0;
      } catch {
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
      return rejected === raw || !lifecycle
        ? {
            available: false,
            connected: false,
            state: 'unavailable',
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
    async close() {
      if (closed) return;
      closed = true;
      clearInterval(timer);
      // Retire immediately, even if an exchange/verification response is pending.
      await retire();
    },
  };
}
