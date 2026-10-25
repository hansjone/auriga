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
  // dsh-session implements flush. No independently verified digest is available.
  sessionSha256: null,
});

// Deny-by-default provenance registry. No public enrolment or caller-supplied
// issuer: the read-only host has no authenticated isolation-handle constructor.
// A host integration must supply that constructor in a separately reviewed change.
const nativeSecurityHandles = new WeakSet();

export function incompatible() {
  return new HostSeamError(CODES.hostIncompatible);
}

/** Check only a fingerprint collected by trusted host code, never user config.
 * @param {unknown} value */
export function verifyRuntimeFingerprint(value) {
  if (!value || typeof value !== 'object') throw incompatible();
  for (const [key, expected] of Object.entries(PINNED_HOST)) {
    if (expected === null) throw incompatible();
    if (/** @type {Record<string, unknown>} */ (value)[key] !== expected)
      throw incompatible();
  }
  return true;
}

/** Paths and manifest identity must come from the same running host loader.
 * Node fs cannot read inside ASAR; Electron's host fs must collect those bytes.
 * This helper is for unpacked distributions, without loading their modules.
 * Diagnostic only: this does not issue a security handle or prove loaded code.
 * @param {{manifestPath: string, controllerPath: string, persistencePath: string, sessionPath: string}} input
 * @returns {Promise<import('./types.js').RuntimeFingerprint>} */
export async function readRuntimeFingerprint({
  manifestPath,
  controllerPath,
  persistencePath,
  sessionPath,
}) {
  try {
    const [manifest, ...sources] = await Promise.all([
      readFile(manifestPath),
      readFile(controllerPath),
      readFile(persistencePath),
      readFile(sessionPath),
    ]);
    const { version, build, dirty } = JSON.parse(manifest.toString('utf8'));
    if (
      typeof version !== 'string' ||
      typeof build !== 'string' ||
      typeof dirty !== 'boolean'
    )
      throw incompatible();
    const [controllerSha256, persistenceSha256, sessionSha256] = sources.map(
      (source) => createHash('sha256').update(source).digest('hex'),
    );
    return {
      version,
      build,
      dirty,
      controllerSha256,
      persistenceSha256,
      sessionSha256,
    };
  } catch {
    throw incompatible();
  }
}

/** No built-in permissive provider. Missing isolation remains unconfigured.
 * @param {import('./types.js').NativeSecurity} security */
export function createNativeGate(security) {
  // Matching methods/constants, a copied object, a Proxy or a prototype are
  // never proof of provenance. Current host evidence cannot mint this brand.
  if (!nativeSecurityHandles.has(security)) throw incompatible();
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

/** Preserve genuine service receivers/private fields instead of spreading a
 * class instance into a structural clone. Only branded handles reach here.
 * @param {import('./types.js').NativeSecurity} security
 * @returns {import('./types.js').HostOperationAuthority} */
export function createNativeAuthority(security) {
  const assertReady = createNativeGate(security);
  return {
    assertReady,
    inspectRuntime: () => security.inspectRuntime(),
    assertIsolated: () => security.assertIsolated(),
    assertDurableProvider: (provider, id) =>
      security.assertDurableProvider(provider, id),
    hasPromptRequest: (id, requestId) =>
      security.hasPromptRequest
        ? security.hasPromptRequest(id, requestId)
        : Promise.resolve('unverified'),
  };
}
