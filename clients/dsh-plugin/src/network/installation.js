import { positiveVersion, publicKey } from './trust/keyset.js';

/** @typedef {import('./runtime-types.js').NetworkInstallation} NetworkInstallation */
/** Static installation validation only: no discovery, I/O or authorization.
 * @param {unknown} input @returns {NetworkInstallation} */
export function networkInstallation(input) {
  try {
    const value =
      /** @type {import('../generated/connect-config.js').ConnectNetwork} */ (
        input
      );
    if (
      !value ||
      typeof value !== 'object' ||
      Array.isArray(value) ||
      Object.keys(value).sort().join(',') !== 'realm,roots,wssUrl' ||
      !positiveVersion(value.realm) ||
      typeof value.wssUrl !== 'string' ||
      !/^wss:\/\/[^/?#@\s]+(?:\/[^?#\s]*)?$/.test(value.wssUrl) ||
      !Array.isArray(value.roots) ||
      value.roots.length < 1 ||
      value.roots.length > 16
    )
      throw new Error();
    const url = new URL(value.wssUrl);
    if (
      url.protocol !== 'wss:' ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    )
      throw new Error();
    const kids = new Set();
    const roots = Array.from(value.roots, (root) => {
      if (
        !root ||
        typeof root !== 'object' ||
        Array.isArray(root) ||
        Object.keys(root).sort().join(',') !== 'kid,public_key_spki' ||
        typeof root.kid !== 'string' ||
        !/^[A-Za-z0-9._-]{1,128}$/.test(root.kid) ||
        kids.has(root.kid)
      )
        throw new Error();
      publicKey(root.public_key_spki);
      kids.add(root.kid);
      return Object.freeze({
        kid: root.kid,
        public_key_spki: root.public_key_spki,
      });
    });
    return Object.freeze({
      realm: value.realm,
      wssUrl: value.wssUrl,
      roots: Object.freeze(roots),
    });
  } catch {
    throw new Error('auriga-dsh: invalid network installation');
  }
}
