import { networkInstallation } from '../network/installation.js';
import { normalizeOrigin } from '../settings/store.js';

/** Saved preferences own the platform origin. Loader metadata is a default for
 * that platform only; switching never sends its provider id or network pins to
 * another installation. Public provider discovery does not authenticate anyone.
 * @param {import('../generated/connect-config.js').ConnectConfig|undefined} loader
 * @param {string} origin
 * @param {{saved?:boolean}} [options] Only the profile store may assert a saved preference. */
export async function savedPlatformConfig(
  loader,
  origin,
  { saved = false } = {},
) {
  const platformOrigin = normalizeOrigin(origin);
  const same =
    loader && normalizeOrigin(loader.platformOrigin) === platformOrigin;
  const config = platformConfig({
    ...(same ? loader : {}),
    platformOrigin,
    issuer: same ? loader.issuer : platformOrigin,
    clientId: loader?.clientId ?? 'auriga-dsh',
    ...(loader?.deviceName ? { deviceName: loader.deviceName } : {}),
    providerId: same ? loader.providerId : 'discover',
    ...(saved && platformOrigin.startsWith('http:')
      ? { allowLocalHttp: true }
      : {}),
  });
  if (same) return config;
  // Discovery belongs to login, never offline local runtime construction.
  return config;
}

/** Installation-owned origin. No URL, issuer or redirect is accepted from UI/model input.
 * @param {any} input
 * @returns {Readonly<Omit<import('../generated/connect-config.js').ConnectConfig,'network'>> & {readonly network?:import('../network/runtime-types.js').NetworkInstallation}} */
export function platformConfig(input) {
  if (
    !input ||
    typeof input !== 'object' ||
    Array.isArray(input) ||
    Object.keys(input).some(
      (k) =>
        ![
          'platformOrigin',
          'issuer',
          'clientId',
          'providerId',
          'deviceName',
          'allowLocalHttp',
          'network',
        ].includes(k),
    )
  )
    throw new Error('invalid platform configuration');
  const url = new URL(normalizeOrigin(input.platformOrigin));
  if (
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/' ||
    (url.protocol !== 'https:' &&
      !(input.allowLocalHttp === true && url.protocol === 'http:'))
  )
    throw new Error('auriga-dsh: trusted HTTPS platform origin required');
  if (input.allowLocalHttp && url.protocol !== 'http:')
    throw new Error('auriga-dsh: explicit private HTTP origin required');
  if (
    ![input.clientId, input.providerId].every(
      (x) => typeof x === 'string' && x.length > 0 && x.length <= 128,
    ) ||
    typeof input.issuer !== 'string' ||
    (input.deviceName !== undefined &&
      (typeof input.deviceName !== 'string' ||
        input.deviceName.length < 1 ||
        input.deviceName.length > 64)) ||
    (input.allowLocalHttp !== undefined &&
      typeof input.allowLocalHttp !== 'boolean')
  )
    throw new Error('auriga-dsh: clientId, providerId and issuer required');
  const issuer = new URL(input.issuer);
  if (
    issuer.origin !== url.origin ||
    issuer.search ||
    issuer.hash ||
    issuer.username ||
    issuer.password
  )
    throw new Error('auriga-dsh: issuer must belong to platform origin');
  return Object.freeze({
    ...input,
    platformOrigin: url.origin,
    issuer: input.issuer.replace(/\/$/, ''),
    ...(Object.hasOwn(input, 'network')
      ? { network: networkInstallation(input.network) }
      : {}),
  });
}
