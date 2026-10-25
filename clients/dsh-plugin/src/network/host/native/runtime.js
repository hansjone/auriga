import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { CODES, HostSeamError } from '../errors.js';

/** npm ci from deploy/compose/real-dsh/package-lock.json; byte SHA-256.
 * Diagnostic baseline only, not loaded-module or isolation attestation. */
export const PINNED_HOST = Object.freeze({
  version: '0.2.0-rc.2',
  controllerSha256:
    'fb0f7b96130f595db20809eeb77a195b05f4a039f931d1e67692f1f74a4dd269',
  persistenceSha256:
    '0845707017acc2b4a8a75eab2244fa3fd88587b8094ab32014321dce7ca1e31b',
  sessionSha256:
    '87ea85e2fb5318bf1f826db9a1c88c1b26d3ea328027a7f32b211880c4d62b9d',
  agentLoopSha256:
    '00c4814d63f3d1e2754eb09144832d19b30499d6ea444730473f740b587948c2',
});

// Deny-by-default provenance registry. No public enrolment or caller-supplied
// issuer. Isolation integration is outside this task and remains disabled.
const nativeSecurityHandles = new WeakSet();

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
 * Diagnostic only: this does not issue a security handle or prove loaded code.
 * @param {{manifestPath: string, controllerPath: string, persistencePath: string, sessionPath: string, agentLoopPath: string}} input
 * @returns {Promise<import('./types.js').RuntimeFingerprint>} */
export async function readRuntimeFingerprint({
  manifestPath,
  controllerPath,
  persistencePath,
  sessionPath,
  agentLoopPath,
}) {
  try {
    const [manifest, ...sources] = await Promise.all([
      readFile(manifestPath),
      readFile(controllerPath),
      readFile(persistencePath),
      readFile(sessionPath),
      readFile(agentLoopPath),
    ]);
    const { version } = JSON.parse(manifest.toString('utf8'));
    // The published CLI manifest has version, not desktop build/dirty fields.
    if (typeof version !== 'string') throw incompatible();
    const [
      controllerSha256,
      persistenceSha256,
      sessionSha256,
      agentLoopSha256,
    ] = sources.map((source) =>
      createHash('sha256').update(source).digest('hex'),
    );
    return {
      version,
      controllerSha256,
      persistenceSha256,
      sessionSha256,
      agentLoopSha256,
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
  };
}
