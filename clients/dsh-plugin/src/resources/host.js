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
    /** Native installation has no public pre-install veto. Reconcile user-installed
     * bundles through the public manager, preserving installation-provided L0.
     * @param {boolean} allowed @param {string[]} catalogNames */
    async enforcePersonalPolicy(allowed, catalogNames) {
      if (allowed) return { blocked: [], restartRequired: false };
      const rows = await manager().listBundles();
      if (!Array.isArray(rows)) return refuse('resource.apply_failed');
      const blocked = [];
      let restartRequired = false;
      for (const row of rows) {
        if (
          !row.installed ||
          catalogNames.includes(row.name) ||
          row.name === 'auriga-dsh'
        )
          continue;
        if (row.enabled)
          restartRequired =
            result(await manager().setBundleEnabled(row.name, false), row.name)
              .restartRequired || restartRequired;
        blocked.push(row.name);
      }
      return { blocked, restartRequired };
    },
    /** @param {string} name */
    async observe(name) {
      const rows = await manager().listBundles();
      if (!Array.isArray(rows)) return refuse('resource.apply_failed');
      const row = rows.find((r) => r.name === name);
      let active = false;
      if (
        row?.enabled &&
        !row.error &&
        Array.isArray(row.rows) &&
        row.rows.length > 0 &&
        typeof manager().listPlugins === 'function'
      ) {
        const inventory = await manager().listPlugins();
        active =
          Array.isArray(inventory) &&
          row.rows.every((/** @type {any} */ r) => {
            const matches = inventory.filter(
              (/** @type {any} */ p) => p.entryId === r.entryId,
            );
            return (
              r.entryId &&
              matches.length === 1 &&
              (!matches[0].enabled || matches[0].fiberPhase === 'active')
            );
          });
      }
      return row && !row.error
        ? {
            name: row.name,
            version: row.version,
            enabled: row.enabled,
            installed: row.installed,
            active,
          }
        : undefined;
    },
    /** @param {string} file @param {string} name */
    async install(file, name) {
      if (!isAbsolute(file) || !file.endsWith('.tgz'))
        return refuse('resource.apply_failed');
      const existing = (await manager().listBundles()).find(
        (/** @type {any} */ r) => r.name === name,
      );
      if (name === 'auriga-dsh' || (existing && !existing.installed))
        refuse('resource.apply_failed');
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
