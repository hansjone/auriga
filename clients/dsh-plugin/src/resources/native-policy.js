import { createHash } from 'node:crypto';
import { existsSync, lstatSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { assertLocalPath, durableJson } from './host-lock.js';
import { parseStrictJson, canonicalize } from './json.js';
import { exact, refuse, uuid } from './trust.js';

/** Authenticated tenant policy cache, bound to the platform and verified owner/device.
 * Missing cache uses the platform default; an existing unreadable cache denies.
 * @param {{home:string,who:any,origin:string}} options */
export function nativePluginPolicy({ home, who, origin }) {
  if (![who.tenantId, who.ownerId, who.hostId].every(uuid))
    refuse('common.unauthenticated');
  const scope = {
    schemaVersion: 1,
    ...who,
    platformOrigin: new URL(origin).origin,
  };
  const platform = createHash('sha256')
    .update(scope.platformOrigin)
    .digest('hex');
  const file = join(
    home,
    'auriga/native-plugin-policy',
    platform,
    who.tenantId,
    who.ownerId,
    who.hostId + '.json',
  );
  /** @type {string|undefined} */ let saved;
  let allowed = true;
  try {
    assertLocalPath(file);
    if (existsSync(file)) {
      if (!lstatSync(file).isFile() || lstatSync(file).size > 65536)
        throw new Error('invalid native policy');
      const value = parseStrictJson(readFileSync(file), true);
      const { allowPersonalPlugins, ...identity } = value;
      if (
        !exact(value, [...Object.keys(scope), 'allowPersonalPlugins']) ||
        canonicalize(identity) !== canonicalize(scope) ||
        typeof allowPersonalPlugins !== 'boolean'
      )
        throw new Error('invalid native policy');
      allowed = allowPersonalPlugins;
      saved = canonicalize(value);
    }
  } catch {
    allowed = false;
  }
  return {
    read: () => allowed,
    /** Called only after the complete authenticated installation list validates.
     * @param {boolean} value */
    write(value) {
      if (typeof value !== 'boolean') refuse('common.validation_failed');
      const record = { ...scope, allowPersonalPlugins: value };
      const text = canonicalize(record);
      if (saved !== text) {
        assertLocalPath(file);
        mkdirSync(dirname(file), { recursive: true });
        durableJson(file, record);
        saved = text;
      }
      allowed = value;
    },
  };
}
