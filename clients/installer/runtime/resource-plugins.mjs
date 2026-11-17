import { existsSync, readFileSync, lstatSync } from 'node:fs';
import { assertLocalPath } from './host-lock.mjs';
import { managedPluginNames } from './resource-observer.mjs';
import { parseStrictJson } from './json.mjs';

/** Launcher-owned profile configuration is public DSH input. Mutations use only
 * `dsh plugin remove`, after the owned DSH tree has stopped. */
export function resourcePluginControl({ home, profileFile, remove, trust }) {
  function dependencies() {
    assertLocalPath(profileFile);
    if (!existsSync(profileFile)) return {};
    if (
      !lstatSync(profileFile).isFile() ||
      lstatSync(profileFile).size > 2097152
    )
      throw new Error('invalid plugin profile');
    const manifest = parseStrictJson(readFileSync(profileFile), true);
    return { ...manifest.dependencies, ...manifest.optionalDependencies };
  }
  const installed = () => {
    const deps = dependencies();
    // With no non-core profile dependencies, no managed plugin can survive even
    // if an unrelated skill ledger is corrupt. Preserve private-only recovery.
    if (Object.keys(deps).every((name) => name === 'auriga-dsh')) return [];
    return managedPluginNames(
      home,
      typeof trust === 'function' ? trust() : trust,
    ).filter((name) => Object.hasOwn(deps, name));
  };
  return {
    pluginsCleared: () => installed().length === 0,
    async clearPlugins() {
      const names = installed();
      if (names.includes('auriga-dsh'))
        throw new Error('catalog cannot remove the core plugin');
      if (names.length) await remove(names);
      if (installed().length)
        throw new Error('managed plugins remain installed');
    },
  };
}
