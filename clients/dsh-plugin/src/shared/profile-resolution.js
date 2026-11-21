// @ts-nocheck
import { readFileSync, realpathSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

export const SDK_INSTALLATION_ANCHOR = '/opt/dsh/package.json';
export const SHARED_BROWSER_MODULE =
  'file:///opt/auriga-dsh/src/shared-browser/plugin.js';

export const SHARED_SDK_PACKAGES = Object.freeze(
  `fs-local llm system-prompt session session-projection
session-persistence-jsonl session-query storage storage-json storage-domain
session-projection-cache working-directory agent tools agent-loop
agent-preset-registry agent-preset agent-default-model skill skill-filesystem
tool-skill shell-env commands credentials-local client-connection attachment-local
client-file-upload file-reference-local workspace host-directory-picker-browse
api-remote-access typert-registry typert-loader api-session-controller
api-workspace-controller api-gateway host-webserver client-modules api-remotes
client-ui-renderer client-locale client-shortcuts client-ui-theme client-ui-settings
client-ui-session client-ui-layout client-resources client-ui-sidebar
client-ui-sidebar-right client-ui-workspace client-ui-conversation
client-ui-input-trigger client-ui-chat client-ui-tool client-ui-trajectory
client-ui-attachment client-ui-agent-preset host-frontend-static`
    .split(/\s+/)
    .map((name) => '@deepseek-ai/dsh-' + name),
);
const sdkNames = new Set(SHARED_SDK_PACKAGES);
const aurigaNames = new Set([
  'auriga-dsh/shared-ports',
  'auriga-dsh/shared',
  SHARED_BROWSER_MODULE,
]);

function denied() {
  throw new Error('Shared profile SDK resolution rejected');
}

function within(root, target) {
  const path = relative(root, target);
  return (
    path !== '' &&
    !isAbsolute(path) &&
    path !== '..' &&
    !path.startsWith('..' + sep)
  );
}

/** Internal Node builtin seams only. Production has no path/config/env input. */
export function createInstalledSdkResolver({
  requireFrom = createRequire,
  realpath = realpathSync,
  readFile = readFileSync,
} = {}) {
  let installedRequire;
  return (name) => {
    if (!sdkNames.has(name)) denied();
    installedRequire ??= requireFrom(SDK_INSTALLATION_ANCHOR);
    const root = realpath(dirname(SDK_INSTALLATION_ANCHOR));
    const module = realpath(installedRequire.resolve(name));
    if (!within(root, module)) denied();
    let directory = dirname(module);
    while (within(root, directory)) {
      let manifest;
      try {
        manifest = JSON.parse(
          readFile(join(directory, 'package.json'), 'utf8'),
        );
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
        directory = dirname(directory);
        continue;
      }
      if (manifest.name !== name) denied();
      const exported = manifest.exports?.['.'];
      // The installed rc.3 public roots use only types/default. Reject new
      // condition sets rather than silently selecting a different CJS class.
      const target =
        typeof exported === 'string'
          ? exported
          : exported &&
              Object.keys(exported).every((key) =>
                ['types', 'default'].includes(key),
              )
            ? exported.default
            : undefined;
      if (
        typeof target !== 'string' ||
        !target.startsWith('./') ||
        !within(directory, resolve(directory, target)) ||
        realpath(resolve(directory, target)) !== module
      )
        denied();
      return pathToFileURL(module).href;
    }
    denied();
  };
}

export const resolveInstalledSdkModule = createInstalledSdkResolver();

/** Normalize a previously validated literal profile before public app-boot.
 * The Agent still validates config, immutable mounts and the finite allowlist.
 * Absolute SDK public exports also make ClientModuleRegistry resolve against
 * the same installed SDK when the entry tree itself lives below /run.
 * @param {{resolveModule?:(name:string)=>string}} [options]
 */
export function createSharedProfileResolver({
  resolveModule = resolveInstalledSdkModule,
} = {}) {
  return (rows) => {
    if (!Array.isArray(rows) || rows.length === 0) denied();
    const names = new Set();
    const ids = new Set();
    let agentPresets = 0;
    const normalized = rows.map((row) => {
      const multiPreset = row?.name === '@deepseek-ai/dsh-agent-preset';
      if (
        !row ||
        typeof row.id !== 'string' ||
        row.id === '' ||
        ids.has(row.id) ||
        (!multiPreset && names.has(row.name)) ||
        (!sdkNames.has(row.name) && !aurigaNames.has(row.name))
      )
        denied();
      if (multiPreset) agentPresets++;
      names.add(row.name);
      ids.add(row.id);
      return {
        ...row,
        name: sdkNames.has(row.name) ? resolveModule(row.name) : row.name,
      };
    });
    // One empty default plus the four shell-stripped native ids.
    if (agentPresets !== 5) denied();
    for (const name of aurigaNames) if (!names.has(name)) denied();
    for (const name of sdkNames) if (!names.has(name)) denied();
    const ports = rows.findIndex(
      (row) => row.name === 'auriga-dsh/shared-ports',
    );
    const host = rows.findIndex((row) => row.name === 'auriga-dsh/shared');
    const browser = rows.findIndex((row) => row.name === SHARED_BROWSER_MODULE);
    if (ports >= host || host >= browser || rows[host].id !== 'auriga-dsh')
      denied();
    return normalized;
  };
}

export const resolveSharedRuntimeProfile = createSharedProfileResolver();
