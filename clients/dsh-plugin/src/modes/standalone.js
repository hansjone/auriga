import { createStandaloneCore } from '../identity/core.js';
import { createMemoryStore } from '../identity/store.js';
import {
  hashPassword,
  generateTotpSecret,
  totpCode,
  verifyPassword,
  verifyTotp,
} from '../identity/crypto.js';

/**
 * Standalone mode: local emergency + pluggable JS connectors, single owner.
 * Does not contact the Auriga platform.
 *
 * @param {{
 *   config?: {
 *     connectors?: Array<{
 *       type: string,
 *       id?: string,
 *       module?: string,
 *       config?: Record<string, unknown>,
 *     }>,
 *     callbackBaseUrl?: string,
 *     secure?: boolean,
 *     sameSite?: 'Strict' | 'Lax' | 'None',
 *     sessionTtlSeconds?: number,
 *     owner?: { providers?: string[], subject?: string, email?: string },
 *   },
 *   store?: ReturnType<typeof createMemoryStore>,
 *   commitAdmission?: (input: import('../identity/owner-admission.js').AdmissionInput) => Promise<boolean>,
 *   now?: () => Date,
 *   allowLocalHttp?: boolean,
 *   fetchImpl?: typeof fetch,
 *   lookupAll?: (hostname: string) => Promise<Array<{ address: string, family: number }>>,
 *   importModule?: (specifier: string) => Promise<unknown>,
 * }} [options]
 */
export async function createStandalone(options = {}) {
  const core = await createStandaloneCore({
    config: options.config ?? { connectors: [] },
    store: options.store,
    commitAdmission: options.commitAdmission,
    now: options.now,
    allowLocalHttp: options.allowLocalHttp,
    fetchImpl: options.fetchImpl,
    lookupAll: options.lookupAll,
    importModule: options.importModule,
  });
  return {
    mode: 'standalone',
    ...core,
  };
}

/**
 * Convenience for tests / installers: build emergency connector config.
 * Uses Argon2id (same memory/time as platform IAM by default).
 *
 * @param {string} username
 * @param {string} password
 * @param {{ argon2?: { t?: number, m?: number, p?: number }, totpSecret?: string }} [opts]
 */
export function enrollEmergency(username, password, opts = {}) {
  const totpSecret = opts.totpSecret ?? generateTotpSecret();
  return {
    username,
    passwordHash: hashPassword(password, opts.argon2),
    totpSecret,
  };
}

export {
  createStandaloneCore,
  createMemoryStore,
  hashPassword,
  generateTotpSecret,
  totpCode,
  verifyPassword,
  verifyTotp,
};

/** Placeholder used by selectMode until createStandalone is awaited by the host. */
export function standalone() {
  return {
    mode: 'standalone',
    /**
     * Hosts should call this to build the full auth runtime.
     * @param {Parameters<typeof createStandalone>[0]} [options]
     */
    create: (options) => createStandalone(options),
  };
}
