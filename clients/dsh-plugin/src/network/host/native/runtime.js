import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { CODES, HostSeamError } from '../errors.js';

/** npm ci from deploy/compose/real-dsh/package-lock.json; byte SHA-256.
 * Functional artifact baseline, not loaded-module or isolation attestation. */
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

/** Single isolation decision point. User deferred all same-machine isolation
 * on 2026-10-25. Restore enforcement here only after a new user decision and
 * a verified host isolation implementation; never use a config/env bypass. */
export function getNativeIsolationDecision() {
  return Object.freeze({
    required: false,
    isolated: false,
    mode: 'development_unisolated',
    message: '未隔离（开发阶段）',
  });
}

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
 * This verifies functional artifacts, not an OS isolation boundary or module origin.
 * @param {import('./types.js').RuntimeArtifacts} input
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

/** Functional readiness remains mandatory despite deferred OS isolation.
 * Paths must come from the running Node host loader, not model/browser input.
 * @param {import('./types.js').RuntimeArtifacts} runtime */
export function createNativeGate(runtime) {
  const keys = [
    'manifestPath',
    'controllerPath',
    'persistencePath',
    'sessionPath',
    'agentLoopPath',
  ];
  if (
    !runtime ||
    !keys.every(
      (key) =>
        typeof (/** @type {any} */ (runtime)[key]) === 'string' &&
        /** @type {any} */ (runtime)[key].length > 0,
    )
  )
    throw incompatible();
  const files = Object.freeze({ ...runtime });
  return async () => {
    try {
      verifyRuntimeFingerprint(await readRuntimeFingerprint(files));
      const isolation = getNativeIsolationDecision();
      if (isolation.required && !isolation.isolated) throw incompatible();
    } catch {
      throw incompatible();
    }
  };
}

/** JSONL writer checks are functional durability checks, not isolation proof.
 * Exact pinned implementation fields: JSONL lib/index.js:26-46, :359-363,
 * :413-420 and :2372-2409. Preserve Cordis service receiver identities.
 * @param {{ctx:import('./types.js').NativeContext,runtime:import('./types.js').RuntimeArtifacts}} options
 * @returns {Promise<import('./types.js').HostOperationAuthority>} */
export async function createNativeAuthority({ ctx, runtime }) {
  const files = Object.freeze({ ...runtime });
  const assertReady = createNativeGate(files);
  await assertReady();
  let JsonlPersistence;
  try {
    ({ default: JsonlPersistence } = await import(
      pathToFileURL(files.persistencePath).href
    ));
  } catch {
    throw incompatible();
  }
  /** @param {any} value */
  const original = (value) => value?.[Symbol.for('cordis.original')] ?? value;
  return {
    assertReady,
    async assertDurableProvider(provider, id) {
      await assertReady();
      if (provider !== ctx.sessionPersistence) throw incompatible();
      const backend = original(provider);
      if (!(backend instanceof JsonlPersistence)) throw incompatible();
      const writer = backend.tracker?.writers?.get(id);
      if (
        !writer ||
        writer.id !== id ||
        writer.header?.id !== id ||
        writer.access !== 'write' ||
        writer.closing !== undefined ||
        original(writer.storage) !== backend ||
        !backend.tracker.openHandles.has(writer) ||
        typeof writer.drainLive !== 'function' ||
        typeof writer.flush !== 'function'
      )
        throw incompatible();
    },
  };
}
