import { lstat, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parseStrictJson } from '../../network/trust/jcs.js';

/** Platform defaults (contracts/auth/uac-control.schema.json UacRevalidationPolicy). */
export const DEFAULT_REVALIDATION = Object.freeze({
  strategy: 'token-verify',
  intervalSeconds: 7200,
  retryIntervalSeconds: 30,
  maxRetries: 3,
});

const LIMIT = 65536;

/** @param {string} path */
async function readPrivate(path) {
  let stat;
  try {
    stat = await lstat(path);
  } catch (error) {
    if (/** @type {NodeJS.ErrnoException} */ (error).code === 'ENOENT')
      return undefined;
    throw new Error('invalid UAC configuration');
  }
  // Same placement rule as enterprise.json: a regular private file only.
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > LIMIT)
    throw new Error('invalid UAC configuration');
  return parseStrictJson(await readFile(path), {
    maxBytes: LIMIT,
    maxDepth: 16,
  });
}

/** @param {any} input */
export function revalidationPolicy(input) {
  if (input === undefined) return { ...DEFAULT_REVALIDATION };
  if (!input || typeof input !== 'object' || Array.isArray(input))
    throw new Error('invalid UAC configuration');
  const allowed = [
    'strategy',
    'intervalSeconds',
    'retryIntervalSeconds',
    'maxRetries',
  ];
  if (Object.keys(input).some((key) => !allowed.includes(key)))
    throw new Error('invalid UAC configuration');
  const policy = { ...DEFAULT_REVALIDATION, ...input };
  const range = (/** @type {unknown} */ v, min = 0, max = 0) =>
    Number.isInteger(v) &&
    /** @type {number} */ (v) >= min &&
    /** @type {number} */ (v) <= max;
  if (
    !['token-verify', 'department'].includes(policy.strategy) ||
    !range(policy.intervalSeconds, 60, 86400) ||
    !range(policy.retryIntervalSeconds, 5, 300) ||
    !range(policy.maxRetries, 0, 10)
  )
    throw new Error('invalid UAC configuration');
  return policy;
}

/**
 * Protected local enterprise configuration, placed by the installer/deployer
 * beside `enterprise.json` under the profile's private Auriga directory:
 *   uac/adapter.json   — byte-for-byte the platform UacConfiguredAdapter file
 *   uac/provider.json  — {baseUrl, systemCode, revalidation?}
 * Missing directory or files means local UAC is not offered (no error).
 * @param {string} directory private Auriga state directory
 * @returns {Promise<{adapter:any,provider:{baseUrl:string,systemCode:string},revalidation:typeof DEFAULT_REVALIDATION}|null>}
 */
export async function readUacConfig(directory) {
  const root = join(directory, 'uac');
  try {
    const stat = await lstat(root);
    if (!stat.isDirectory() || stat.isSymbolicLink())
      throw new Error('invalid UAC configuration');
  } catch (error) {
    if (/** @type {NodeJS.ErrnoException} */ (error).code === 'ENOENT')
      return null;
    throw new Error('invalid UAC configuration');
  }
  const adapter = await readPrivate(join(root, 'adapter.json'));
  const provider = await readPrivate(join(root, 'provider.json'));
  if (adapter === undefined && provider === undefined) return null;
  if (
    !adapter ||
    typeof adapter !== 'object' ||
    Array.isArray(adapter) ||
    !provider ||
    typeof provider !== 'object' ||
    Array.isArray(provider) ||
    Object.keys(provider).some(
      (key) => !['baseUrl', 'systemCode', 'revalidation'].includes(key),
    ) ||
    typeof provider.baseUrl !== 'string' ||
    typeof provider.systemCode !== 'string'
  )
    throw new Error('invalid UAC configuration');
  return {
    adapter,
    provider: { baseUrl: provider.baseUrl, systemCode: provider.systemCode },
    revalidation: revalidationPolicy(provider.revalidation),
  };
}
