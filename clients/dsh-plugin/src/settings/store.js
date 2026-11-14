import { mkdir, readFile, writeFile, rename, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';

/** Platform preferences never carry login credentials.
 * HTTP is limited to explicit loopback or RFC1918/ULA literals, never DNS names.
 * @param {unknown} input */
export function normalizeOrigin(input) {
  if (typeof input !== 'string' || input.length > 2048)
    throw new Error('invalidOrigin');
  let url;
  try {
    url = new URL(input.trim());
  } catch {
    throw new Error('invalidOrigin');
  }
  const rawHost = input
    .trim()
    .match(/^https?:\/\/(\[[^\]]+\]|[^/:?#]+)(?::\d+)?\/?$/i)?.[1]
    ?.toLowerCase();
  if (
    !rawHost ||
    rawHost !== url.hostname ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/'
  )
    throw new Error('invalidOrigin');
  const host = url.hostname;
  const octets = host.split('.').map(Number);
  const ipv4 = /^\d+\.\d+\.\d+\.\d+$/.test(host);
  const local =
    host === 'localhost' ||
    host === '[::1]' ||
    (ipv4 &&
      (octets[0] === 127 ||
        octets[0] === 10 ||
        (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) ||
        (octets[0] === 192 && octets[1] === 168))) ||
    /^\[f[cd][\da-f]{2}:/i.test(host);
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && local))
    throw new Error('httpsRequired');
  return url.origin;
}

/** Probe only the current shell installation through its fixed operation.
 * A provider response proves reachability, without granting platform authority.
 * @param {string} origin @param {{origin?:string,broker?:any}} [connection] */
export async function probe(origin, connection = {}) {
  const platform = normalizeOrigin(origin);
  if (platform !== connection.origin || !connection.broker)
    return { reachable: false, reason: 'unavailable' };
  const { broker } = connection;
  try {
    const response = await broker.call('provider_diagnostic', {});
    if (response.statusCode < 200 || response.statusCode >= 300)
      return { reachable: false, reason: 'http', status: response.statusCode };
    return typeof response.body?.available === 'boolean'
      ? { reachable: true }
      : { reachable: false, reason: 'invalidPlatform' };
  } catch (error) {
    const value = /** @type {any} */ (error);
    return {
      reachable: false,
      ...(Number.isInteger(value?.status)
        ? { reason: 'http', status: value.status }
        : { reason: value?.name === 'TimeoutError' ? 'timeout' : 'network' }),
    };
  }
}

/** @typedef {{platformOrigin:string,deployment:'local'|'cloud'}} Preferences */
/** @param {unknown} input @returns {Preferences} */
function validate(input) {
  const value = /** @type {Preferences} */ (input);
  if (
    !value ||
    typeof value !== 'object' ||
    Object.keys(value).sort().join(',') !== 'deployment,platformOrigin' ||
    !['local', 'cloud'].includes(value.deployment)
  )
    throw new Error('invalidSettings');
  return {
    platformOrigin:
      value.platformOrigin === '' ? '' : normalizeOrigin(value.platformOrigin),
    deployment: value.deployment,
  };
}

/** @param {string} directory @param {string} [initialOrigin] */
export async function createSettingsStore(directory, initialOrigin = '') {
  const path = join(directory, 'settings.json');
  /** @type {Preferences} */
  let state = {
    platformOrigin: initialOrigin ? normalizeOrigin(initialOrigin) : '',
    deployment: 'cloud',
  };
  let hasSavedSettings = false;
  try {
    state = validate(JSON.parse(await readFile(path, 'utf8')));
    hasSavedSettings = true;
  } catch (error) {
    if (/** @type {NodeJS.ErrnoException} */ (error).code !== 'ENOENT')
      throw error;
  }
  let pending = Promise.resolve();
  /** @type {Set<(next:Preferences)=>Promise<void>>} */
  const listeners = new Set();
  /** @type {Set<(next:Preferences)=>Promise<void>>} */
  const beforeSave = new Set();
  return {
    snapshot: () => ({ ...state }),
    get hasSavedSettings() {
      return hasSavedSettings;
    },
    /** @param {(next:Preferences)=>Promise<void>} listener
     * @param {(next:Preferences)=>Promise<void>} [invalidate] */
    subscribe(listener, invalidate) {
      listeners.add(listener);
      if (invalidate) beforeSave.add(invalidate);
      return () => {
        listeners.delete(listener);
        if (invalidate) beforeSave.delete(invalidate);
      };
    },
    /** @param {unknown} input */
    save(input) {
      const operation = pending.then(async () => {
        const next = validate(input);
        for (const invalidate of beforeSave) await invalidate({ ...next });
        await mkdir(directory, { recursive: true });
        const temp = path + '.' + randomUUID();
        try {
          await writeFile(temp, JSON.stringify(next), {
            mode: 0o600,
            flag: 'wx',
          });
          await rename(temp, path);
          state = next;
          hasSavedSettings = true;
          for (const listener of listeners) await listener({ ...next });
        } finally {
          await rm(temp, { force: true });
        }
      });
      pending = operation.catch(() => {});
      return operation;
    },
  };
}
