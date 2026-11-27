import { isAbsolute } from 'node:path';
import { randomUUID } from 'node:crypto';
import { refuse } from './trust.js';
import { nativePluginFailure } from './diagnostics.js';
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
  /** @param {any} result @param {string} name @param {string} [phase] @param {string} [requestId] */
  function result(result, name, phase = 'enable', requestId) {
    if (
      !result ||
      result.error ||
      result.target !== name ||
      !['applied', 'restart-required'].includes(result.application)
    )
      throw nativePluginFailure(result, phase, requestId);
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
    /** A missing or corrupt package may still be selected in the native profile.
     * @param {string} name */
    async selected(name) {
      const rows = await manager().listBundles();
      if (!Array.isArray(rows)) return refuse('resource.apply_failed');
      return rows.some((row) => row.name === name && row.enabled === true);
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
        if (Array.isArray(inventory)) {
          const members = /** @type {{entryId?: string}[]} */ (row.rows);
          const entries = members.map((r) =>
            r.entryId
              ? inventory.filter(
                  (/** @type {any} */ p) => p.entryId === r.entryId,
                )
              : [],
          );
          active =
            entries.some(([entry]) => entry?.enabled === true) &&
            entries.every(
              ([entry, duplicate]) =>
                entry &&
                !duplicate &&
                (!entry.enabled || entry.fiberPhase === 'active'),
            );
        }
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
    /** @param {string} file @param {string} name @param {(phase:string,attempt?:{index:number,total:number})=>void} [progress] */
    async install(file, name, progress) {
      if (!isAbsolute(file) || !file.endsWith('.tgz'))
        return refuse('resource.apply_failed');
      const existing = (await manager().listBundles()).find(
        (/** @type {any} */ r) => r.name === name,
      );
      if (name === 'auriga-dsh' || (existing && !existing.installed))
        refuse('resource.apply_failed');
      const requestId = randomUUID();
      const dispose =
        progress &&
        ctx.on?.('plugin-manager/install-state', (/** @type {any} */ event) => {
          if (event?.requestId !== requestId) return;
          if (event.phase === 'applying') progress('checking_installation');
          if (event.phase === 'installing') {
            const { index, total } = event.attempt ?? {};
            const attempt =
              Number.isInteger(index) &&
              Number.isInteger(total) &&
              index > 0 &&
              index <= total &&
              total <= 100
                ? { index, total }
                : undefined;
            progress('installing_dependencies', attempt);
          }
        });
      try {
        return result(
          await manager().installBundle('file:' + file, {
            enabled: false,
            approvedBuilds: [],
            requestId,
          }),
          name,
          'install',
          requestId,
        );
      } catch (error) {
        if (/** @type {any} */ (error)?.nativePluginOperation) throw error;
        throw nativePluginFailure(undefined, 'install', requestId);
      } finally {
        dispose?.();
      }
    },
    /** @param {string} name @param {boolean} enabled */
    async enable(name, enabled) {
      return result(await manager().setBundleEnabled(name, enabled), name);
    },
    /** @param {string} name */
    async remove(name) {
      return result(await manager().removeBundle(name), name, 'remove');
    },
  };
}
