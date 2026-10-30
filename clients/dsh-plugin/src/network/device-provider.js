/* global AbortSignal */
import { createHash } from 'node:crypto';
import { platformConfig, PlatformError } from '../local/platform.js';
import { parseStrictJson } from './trust/jcs.js';
import { positiveVersion } from './trust/keyset.js';
import { diagnosticReason } from './diagnostics.js';

const alphabet = 'abcdefghijklmnopqrstuvwxyz234567';
const CLOCK_SKEW_MS = 60000;
/** Transient fetch failure only; validation and persistence errors never use it. */
export class DeviceControlUnavailable extends Error {}
/** Only proven credential invalidation may end the native login.
 * A network permission/quota denial says nothing about the model credential.
 * @param {unknown} error */
export function isDeviceRevocationError(error) {
  const value = /** @type {{status?:number,code?:string}|null} */ (error);
  return value?.status === 401 || value?.code === 'network.revoked';
}
/** @param {string} ticket */
function ingressFor(ticket) {
  let bits = 0,
    accumulator = 0,
    out = '';
  for (const byte of createHash('sha256')
    .update(ticket, 'ascii')
    .digest()
    .subarray(0, 16)) {
    accumulator = (accumulator << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      out += alphabet[(accumulator >>> bits) & 31];
    }
  }
  if (bits) out += alphabet[(accumulator << (5 - bits)) & 31];
  return out;
}
/** @param {any} value @param {string[]} fields */
const exact = (value, fields) =>
  value &&
  typeof value === 'object' &&
  !Array.isArray(value) &&
  Object.keys(value).sort().join(',') === fields.sort().join(',');
/** @param {any} value */
const uuid = (value) =>
  typeof value === 'string' &&
  /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(value);
/** Fixed native device control only; no UI/model identity, service/AI token or
 * frame-provided URL is accepted. Raw bytes reach strict JSON/JCS validation.
 * @param {{config:any,getDevice:(signal:AbortSignal)=>Promise<{accessToken:string,claims:{tid:string,did:string,sub:string}}>,now?:()=>number}} options
 * @returns {import('./runtime-types.js').DeviceNetworkProvider} */
export function createDeviceProvider({
  config: input,
  getDevice,
  now = Date.now,
}) {
  const config = platformConfig(input),
    installation = config.network;
  if (!installation) throw new Error('network installation required');
  /** @param {any} value @param {{tid:string,did:string}} claims @param {boolean} created @returns {import('./runtime-types.js').DeviceRegistration} */
  function registration(value, claims, created) {
    if (
      !exact(value, [
        'tenant',
        'realm',
        'node',
        'device',
        'systemAddress',
        ...(created ? ['created'] : []),
      ]) ||
      !uuid(value.tenant) ||
      !uuid(value.device) ||
      value.tenant !== claims.tid ||
      value.device !== claims.did ||
      value.realm !== installation?.realm ||
      !positiveVersion(value.realm) ||
      !positiveVersion(value.node) ||
      value.systemAddress !==
        `aur1:${BigInt(value.realm).toString(16)}.${BigInt(value.node).toString(16)}.0` ||
      (created && typeof value.created !== 'boolean')
    )
      throw diagnosticReason(
        new Error('invalid device registration'),
        'response_invalid',
      );
    return value;
  }
  return {
    kind: 'device-connection',
    async prepare({ signal, diagnostics }) {
      signal.throwIfAborted();
      const device = await getDevice(signal);
      signal.throwIfAborted();
      const expected = { ...device.claims };
      /** @template T @param {string} phase @param {()=>Promise<T>|T} operation @param {number} [budget] */
      const run = (phase, operation, budget = 0) =>
        diagnostics
          ? diagnostics.scope(phase, budget).deferred(operation)
          : operation();
      /** @param {string} path @param {unknown} body @param {AbortSignal} active */
      async function request(path, body, active) {
        active.throwIfAborted();
        const current = await getDevice(active);
        active.throwIfAborted();
        if (
          ['tid', 'did', 'sub'].some(
            (key) =>
              /** @type {any} */ (current.claims)[key] !==
              /** @type {any} */ (expected)[key],
          )
        )
          throw new PlatformError(401, 'network.revoked');
        const response = await fetch(new URL(path, config.platformOrigin), {
          method: body === undefined ? 'GET' : 'POST',
          credentials: 'omit',
          redirect: 'error',
          signal: AbortSignal.any([active, AbortSignal.timeout(15000)]),
          headers: {
            authorization: 'Bearer ' + current.accessToken,
            accept: 'application/json',
            ...(body === undefined
              ? {}
              : { 'content-type': 'application/json' }),
          },
          body: body === undefined ? undefined : JSON.stringify(body),
        }).catch(() => {
          active.throwIfAborted();
          throw diagnosticReason(
            new DeviceControlUnavailable('network control unavailable'),
            'control_unavailable',
          );
        });
        active.throwIfAborted();
        const parts = [];
        let size = 0;
        if (response.body) {
          const reader = response.body.getReader();
          try {
            for (;;) {
              const { done, value } = await reader.read().catch(() => {
                active.throwIfAborted();
                throw diagnosticReason(
                  new DeviceControlUnavailable('network control unavailable'),
                  'control_unavailable',
                );
              });
              active.throwIfAborted();
              if (done) break;
              size += value.byteLength;
              if (size > 65536) {
                await reader.cancel();
                throw diagnosticReason(
                  new Error('network control response too large'),
                  'response_too_large',
                );
              }
              parts.push(value);
            }
          } finally {
            reader.releaseLock();
          }
        }
        if (!response.ok) {
          let code;
          try {
            const problem = parseStrictJson(Buffer.concat(parts));
            if (problem?.code === 'network.revoked') code = problem.code;
          } catch {
            // An unreadable error body never upgrades a denial to revocation.
          }
          throw new PlatformError(response.status, code);
        }
        active.throwIfAborted();
        return Buffer.concat(parts);
      }
      /** @type {import('./runtime-types.js').DeviceRegistration|undefined} */
      let registered;
      let registering = false;
      return {
        kind: 'device-connection',
        async submit(body, active) {
          return request('/api/ai/v1/network/submit', body, active);
        },
        async register(active) {
          if (registering)
            throw new Error('device registration already consumed');
          registering = true;
          const bytes = await run(
            'register_http',
            () => request('/api/iam/v1/network/register', {}, active),
            15000,
          );
          const result = await run('register_validate', () =>
            registration(parseStrictJson(bytes), expected, true),
          );
          active.throwIfAborted();
          registered = Object.freeze(result);
          return result;
        },
        url: installation.wssUrl,
        origin: config.platformOrigin,
        roots: installation.roots,
        async refresh(trust, active) {
          const announcement = await run(
            'keys_announcement_http',
            () =>
              request(
                '/api/collab/v1/network/keys/announcement',
                undefined,
                active,
              ),
            15000,
          );
          await run('keys_announcement_validate', () =>
            trust.announce(announcement),
          );
          active.throwIfAborted();
          const keys = await run(
            'keys_http',
            () => request('/api/collab/v1/network/keys', undefined, active),
            15000,
          );
          await run('keys_validate', () => trust.update(keys));
          active.throwIfAborted();
          trust.assertUsable();
        },
        async getTicket(active) {
          if (!registered) throw new Error('device registration required');
          const identity = registered;
          const bytes = await run(
            'ticket_http',
            () =>
              request(
                '/api/iam/v1/network/tickets',
                { node: identity.node },
                active,
              ),
            15000,
          );
          return run('ticket_validate', () => {
            const raw = parseStrictJson(bytes);
            registration(raw.identity, expected, false);
            const ingress =
              typeof raw.ticket === 'string' &&
              /^[A-Za-z0-9_-]{43}$/.test(raw.ticket)
                ? ingressFor(raw.ticket)
                : '';
            const shard =
              alphabet[createHash('sha256').update(ingress).digest()[0] & 31];
            const expires = Date.parse(raw.expiresAt),
              connectionExpires = Date.parse(raw.connectionExpiresAt),
              currentTime = now();
            if (
              !exact(raw, [
                'identity',
                'ticket',
                'serverAudience',
                'expiresAt',
                'connectionExpiresAt',
                'ingress',
                'upSubject',
                'downSubject',
                'replyPrefix',
                'protocolVersion',
              ]) ||
              raw.identity.node !== identity.node ||
              raw.identity.systemAddress !== identity.systemAddress ||
              !ingress ||
              raw.ingress !== ingress ||
              raw.upSubject !== `auriga.net.up.${shard}.${ingress}` ||
              raw.downSubject !== `auriga.net.down.${ingress}` ||
              raw.replyPrefix !== `auriga.net.reply.${ingress}` ||
              raw.protocolVersion !== 1 ||
              typeof raw.serverAudience !== 'string' ||
              !/^[A-Za-z0-9._:-]{1,256}$/.test(raw.serverAudience) ||
              ![raw.expiresAt, raw.connectionExpiresAt].every(
                (v) =>
                  typeof v === 'string' &&
                  /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(v),
              ) ||
              !Number.isFinite(expires) ||
              expires <= currentTime - CLOCK_SKEW_MS ||
              expires > currentTime + 30000 + CLOCK_SKEW_MS ||
              !Number.isFinite(connectionExpires) ||
              connectionExpires < expires ||
              connectionExpires <= currentTime - CLOCK_SKEW_MS ||
              connectionExpires > currentTime + 3600000 + CLOCK_SKEW_MS
            )
              throw diagnosticReason(
                new Error('invalid device network ticket'),
                'response_invalid',
              );
            active.throwIfAborted();
            return {
              auth: {
                token: JSON.stringify({
                  tenant: identity.tenant,
                  node: identity.node,
                  device: identity.device,
                  ticket: raw.ticket,
                }),
              },
              expiresAtMs: connectionExpires,
              subjects: {
                upSubject: raw.upSubject,
                downSubject: raw.downSubject,
                inboxPrefix: raw.replyPrefix,
              },
            };
          });
        },
      };
    },
  };
}
