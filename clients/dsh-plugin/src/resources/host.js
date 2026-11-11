import { isAbsolute } from 'node:path';
import { refuse } from './trust.js';
/** Public DSH PluginManager only. No Loader/profile internals or edits to DSH.
 * Roots and staging paths are never accepted from the browser.
 * @param {any} ctx */
export function resourceHost(ctx) {
  function manager() {
    const host = ctx.get?.('pluginManager');
    if (
      !host ||
      ![
        'listBundles',
        'installBundle',
        'setBundleEnabled',
        'removeBundle',
      ].every((key) => typeof host[key] === 'function')
    )
      return refuse('resource.apply_failed');
    return host;
  }
  /** @param {any} result @param {string} name */
  function result(result, name) {
    if (
      !result ||
      result.error ||
      result.target !== name ||
      !['applied', 'restart-required'].includes(result.application)
    )
      return refuse('resource.apply_failed');
    return { restartRequired: result.application === 'restart-required' };
  }
  return {
    /** @param {string} name */
    async observe(name) {
      const rows = await manager().listBundles();
      if (!Array.isArray(rows)) return refuse('resource.apply_failed');
      const row = rows.find((r) => r.name === name);
      return row && !row.error
        ? {
            name: row.name,
            version: row.version,
            enabled: row.enabled,
            installed: row.installed,
          }
        : undefined;
    },
    /** @param {string} file @param {string} name */
    async install(file, name) {
      if (!isAbsolute(file) || !file.endsWith('.tgz'))
        return refuse('resource.apply_failed');
      return result(
        await manager().installBundle('file:' + file, {
          enabled: false,
          approvedBuilds: [],
        }),
        name,
      );
    },
    /** @param {string} name @param {boolean} enabled */
    async enable(name, enabled) {
      return result(await manager().setBundleEnabled(name, enabled), name);
    },
    /** @param {string} name */
    async remove(name) {
      return result(await manager().removeBundle(name), name);
    },
  };
}
