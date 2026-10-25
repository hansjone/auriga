import { generateKeyPairSync, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { jcs } from '../../../../../contracts/test/network-lib.mjs';
import { openTrustStore } from '../../../src/network/trust/store.js';

const root = generateKeyPairSync('ed25519');
export const testRoots = [
  {
    kid: 'test-offline-root',
    public_key_spki: root.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  },
];
export function signedKeyset(payload) {
  return {
    keyset: payload,
    root_auth: {
      alg: 'Ed25519',
      kid: testRoots[0].kid,
      signature: sign(
        null,
        Buffer.from('Auriga.NetworkKeyset.v1\0' + jcs(payload)),
        root.privateKey,
      ).toString('base64url'),
    },
  };
}
// Convenience for existing payload-based fixtures; the real store only sees
// the contract's root-signed wrapper, including during refresh and restart.
export async function openTestTrust(options) {
  const store = await openTrustStore({
    ...options,
    roots: testRoots,
    config: options.config && signedKeyset(options.config),
    refresh:
      options.refresh &&
      (async (request) => signedKeyset(await options.refresh(request))),
  });
  return { ...store, update: (payload) => store.update(signedKeyset(payload)) };
}
