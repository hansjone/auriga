import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { CODES, HostSeamError } from '../errors.js';

/** failure-validation §1.2; source evidence, not a claim of runtime validation. */
export const PINNED_HOST = Object.freeze({
  version: '0.2.1-rc.3',
  build: 'be72761ee1a6736ebcdf4f3739363b083ae53c98',
  dirty: false,
  controllerSha256:
    'a9801acb577b03053c5d5a3191ff555c5f1d791d0acddec113bea4511e33b386',
  persistenceSha256:
    '973dd78679b2661d6f492343bc22b124ae958aa597d3b36c698a2c650f909984',
});

export function incompatible() {
  return new HostSeamError(CODES.hostIncompatible);
}

/** Check only a fingerprint collected by trusted host code, never user config.
 * @param {unknown} value */
export function verifyRuntimeFingerprint(value) {
  if (!value || typeof value !== 'object') throw incompatible();
  for (const [key, expected] of Object.entries(PINNED_HOST)) {
    if (/** @type {Record<string, unknown>} */ (value)[key] !== expected)
      throw incompatible();
  }
  return true;
}

/** Paths and manifest identity must come from the same running host loader.
 * Node fs cannot read inside ASAR; Electron's host fs must collect those bytes.
 * This helper is for unpacked distributions, without loading their modules.
 * @param {{version: string, build: string, dirty: boolean, controllerPath: string, persistencePath: string}} input
 * @returns {Promise<import('./types.js').RuntimeFingerprint>} */
export async function readRuntimeFingerprint({
  version,
  build,
  dirty,
  controllerPath,
  persistencePath,
}) {
  try {
    const sources = await Promise.all([
      readFile(controllerPath),
      readFile(persistencePath),
    ]);
    const [controllerSha256, persistenceSha256] = sources.map((source) =>
      createHash('sha256').update(source).digest('hex'),
    );
    return { version, build, dirty, controllerSha256, persistenceSha256 };
  } catch {
    throw incompatible();
  }
}

/** No built-in permissive provider. Missing isolation remains unconfigured.
 * @param {import('./types.js').NativeSecurity} security */
export function createNativeGate(security) {
  if (
    !security ||
    !['inspectRuntime', 'assertIsolated', 'assertDurableProvider'].every(
      (key) => typeof (/** @type {any} */ (security)[key]) === 'function',
    )
  )
    throw incompatible();
  return async () => {
    try {
      verifyRuntimeFingerprint(await security.inspectRuntime());
      if ((await security.assertIsolated()) !== undefined) throw incompatible();
    } catch {
      throw incompatible();
    }
  };
}
