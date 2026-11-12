import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { uptime } from 'node:os';
import { catalogIdentity } from './catalog-api.js';
import { desktopSkills } from './desktop-skills.js';
import { canonicalize } from './json.js';
import { uuid, refuse } from './trust.js';
/** Awaited before identity publication; a losing lock cannot become a successful account switch.
 * @param {{home?:string,desktop?:ReturnType<typeof desktopSkills>,timeout?:number}} [options] */
export function createCatalogIdentityFence({
  home = process.env.DSH_HOME,
  desktop,
  timeout = 30000,
} = {}) {
  let fingerprint = 'unseen',
    tail = Promise.resolve(),
    closed = false;
  /** @param {any} next */
  function setIdentity(next) {
    const result = tail.then(async () => {
      if (closed) throw new Error('resource identity fence closed');
      if (!home || !existsSync(join(home, 'auriga/guardian/state.json')))
        return;
      if (
        next &&
        !['tenantId', 'ownerId', 'hostId'].every((k) => uuid(next[k]))
      )
        refuse('common.unauthenticated');
      const key = canonicalize(next ?? null);
      if (key === fingerprint) return;
      const end = Math.floor(uptime() * 1000) + timeout;
      for (;;) {
        if (closed) throw new Error('resource identity fence closed');
        try {
          const writer = desktop ?? desktopSkills({ home });
          writer.startup();
          writer.switchIdentity(next);
          fingerprint = key;
          return;
        } catch (error) {
          if (
            /** @type {any} */ (error).code !== 'EEXIST' ||
            Math.floor(uptime() * 1000) >= end
          )
            throw error;
          await delay(50);
        }
      }
    });
    tail = result.catch(() => {});
    return result;
  }
  /** @param {any} runtime */
  async function fromRuntime(runtime) {
    if (runtime?.resourceIdentity) return;
    let next;
    try {
      next = runtime ? catalogIdentity(runtime) : undefined;
    } catch {
      next = undefined;
    }
    await setIdentity(next);
  }
  return {
    setIdentity,
    fromRuntime,
    close() {
      closed = true;
    },
  };
}
