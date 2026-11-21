// @ts-nocheck
import { createHash } from 'node:crypto';
import { readFileSync, realpathSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { canonical } from '../shared/protocol.js';

export const SDK_INSTALLATION_ANCHOR = '/opt/dsh/package.json';
export const DEDICATED_MODULE =
  'file:///opt/auriga-dsh/src/dedicated/plugin.js';
export const DEDICATED_PROFILE_DIR =
  '/home/dsh/.host/.dsh/profiles/auriga-dedicated';
export const DEDICATED_PNPM_CLI =
  '/opt/auriga-tools/pnpm/node_modules/pnpm/bin/pnpm.cjs';
export const DEDICATED_BASELINE_DIGEST =
  'sha256:52ad8d928ddbde5972bbedfcb7d1ba3b6f3c851258feaf502ae285876661ca87';
const roles =
  `fs:fs-sandbox llm:llm system-prompt:system-prompt sessions:session session-projections:session-projection
session-persistence:session-persistence-jsonl session-query:session-query storage:storage storage-json:storage-json storage-domain:storage-domain
projection-cache:session-projection-cache working-directory:working-directory agents:agent tools:tools agent-loop:agent-loop
agent-presets:agent-preset-registry dedicated-preset:agent-preset default-model:agent-default-model skills:skill private-skills:skill-filesystem
tool-skill:tool-skill shell-env:shell-env commands:commands connection-credentials:credentials-local connection:client-connection
attachments:attachment-local file-upload:client-file-upload file-references:file-reference-local workspace:workspace directory-picker:host-directory-picker-browse
remote-access:api-remote-access typert:typert-registry typert-loader:typert-loader session-controller:api-session-controller workspace-controller:api-workspace-controller
gateway:api-gateway webserver:host-webserver modules:client-modules api-remotes:api-remotes ui-renderer:client-ui-renderer locale:client-locale
shortcuts:client-shortcuts ui-theme:client-ui-theme ui-settings:client-ui-settings ui-session:client-ui-session ui-layout:client-ui-layout resources:client-resources
ui-sidebar:client-ui-sidebar ui-sidebar-right:client-ui-sidebar-right ui-workspace:client-ui-workspace ui-conversation:client-ui-conversation
ui-input-trigger:client-ui-input-trigger ui-chat:client-ui-chat ui-tool:client-ui-tool ui-trajectory:client-ui-trajectory ui-attachment:client-ui-attachment
plugin-manager:plugin-manager subprocess:subprocess-local sandbox:sandbox-local sandbox-policy:sandbox-policy shell:bash-sandbox approval:user-approval
permission-presets:permission-presets user-questions:user-questions jobs:jobs-local tool-bash:tool-bash tool-jobs:tool-jobs tool-fs:tool-fs
tool-fs-search:tool-fs-search tool-working-directory:tool-working-directory job-controller:api-job-controller
ui-commands:client-ui-commands ui-approval:client-ui-approval ui-user-questions:client-ui-user-questions ui-jobs:client-ui-jobs ui-skill:client-ui-skill
ui-plugin-manager:client-ui-plugin-manager ui-settings-plugins:client-ui-settings-plugins ui-settings-shell:client-ui-settings-shell
ui-permission-presets:client-ui-permission-presets ui-agent-preset:client-ui-agent-preset frontend-static:host-frontend-static`
    .split(/\s+/)
    .map((pair) => {
      const [id, name] = pair.split(':');
      return Object.freeze({ id, name: '@deepseek-ai/dsh-' + name });
    });
export const DEDICATED_SDK_PACKAGES = Object.freeze(
  roles.map((row) => row.name),
);
const names = new Set(DEDICATED_SDK_PACKAGES);
export const DEDICATED_LAUNCHER_PACKAGES = Object.freeze([
  '@deepseek-ai/dsh-app-boot',
]);
const resolutionNames = new Set([...names, ...DEDICATED_LAUNCHER_PACKAGES]);
function denied() {
  throw new Error('Dedicated profile SDK resolution rejected');
}
const within = (root, target) => {
  const value = relative(root, target);
  return (
    value !== '' &&
    !isAbsolute(value) &&
    value !== '..' &&
    !value.startsWith('..' + sep)
  );
};
function freeze(value) {
  if (value && typeof value === 'object') {
    Object.values(value).forEach(freeze);
    Object.freeze(value);
  }
  return value;
}

/** Installation-only fixture seams; production has one fixed readonly anchor. */
export function createDedicatedInstalledSdkResolver({
  requireFrom = createRequire,
  realpath = realpathSync,
  readFile = readFileSync,
} = {}) {
  let installed;
  return (name) => {
    if (!resolutionNames.has(name)) denied();
    installed ??= requireFrom(SDK_INSTALLATION_ANCHOR);
    const root = realpath(dirname(SDK_INSTALLATION_ANCHOR)),
      file = realpath(installed.resolve(name));
    if (!within(root, file)) denied();
    let directory = dirname(file);
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
        realpath(resolve(directory, target)) !== file
      )
        denied();
      return pathToFileURL(file).href;
    }
    denied();
  };
}
export const resolveDedicatedSdkModule = createDedicatedInstalledSdkResolver();

function plain(value, depth = 0, count = { value: 0 }) {
  if (depth > 32 || ++count.value > 16384) denied();
  if (value === null || ['string', 'boolean'].includes(typeof value)) return;
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || Object.is(value, -0)) denied();
    return;
  }
  if (!value || typeof value !== 'object') denied();
  if (
    !Array.isArray(value) &&
    Object.getPrototypeOf(value) !== Object.prototype &&
    Object.getPrototypeOf(value) !== null
  )
    denied();
  for (const [key, item] of Object.entries(value)) {
    if (['__proto__', 'prototype', 'constructor', '__jsExpr'].includes(key))
      denied();
    plain(item, depth + 1, count);
  }
}

/** The sealed literal baseline permits only the Node-owned renderer fields.
 * This is not approval of arbitrary selected-resource or user patch layers. */
export function assertDedicatedRuntimeProfile(input) {
  plain(input);
  if (!Array.isArray(input) || input.length !== roles.length + 1) denied();
  const copy = JSON.parse(JSON.stringify(input));
  for (let i = 0; i < roles.length; i++)
    if (copy[i].id !== roles[i].id || copy[i].name !== roles[i].name) denied();
  const host = copy.at(-1),
    byId = new Map(copy.map((row) => [row.id, row]));
  if (
    host.id !== 'auriga-dsh' ||
    host.name !== DEDICATED_MODULE ||
    !/^\/var\/lib\/auriga\/pools\/[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}\/control\/dedicated-host\.json$/.test(
      host.config?.configFile,
    )
  )
    denied();
  host.config.configFile =
    '/var/lib/auriga/pools/00000000-0000-4000-8000-000000000001/control/dedicated-host.json';
  const listener = byId.get('webserver').config,
    model = byId.get('default-model').config;
  if (
    !['127.0.0.1', '0.0.0.0'].includes(listener?.host) ||
    !Number.isSafeInteger(listener.port) ||
    listener.port < 1 ||
    listener.port > 65535 ||
    typeof model?.model !== 'string' ||
    !model.model ||
    model.model.length > 128
  )
    denied();
  listener.host = '127.0.0.1';
  listener.port = 7860;
  model.model = 'gpt-6.1-sol';
  const digest =
    'sha256:' + createHash('sha256').update(canonical(copy)).digest('hex');
  if (digest !== DEDICATED_BASELINE_DIGEST) denied();
  return true;
}

export function createDedicatedProfileResolver({
  resolveModule = resolveDedicatedSdkModule,
} = {}) {
  return (rows) => {
    assertDedicatedRuntimeProfile(rows);
    return freeze(
      rows.map((row) => {
        const value = JSON.parse(JSON.stringify(row));
        if (names.has(value.name)) value.name = resolveModule(value.name);
        if (
          typeof value.name !== 'string' ||
          !value.name.startsWith('file:///')
        )
          denied();
        return value;
      }),
    );
  };
}
export const resolveDedicatedRuntimeProfile = createDedicatedProfileResolver();

/** Protect the immutable baseline after public bundle/user composition. Extra
 * rows still require Node's separate current signed-resource module authority. */
export function assertDedicatedBaselinePreserved(baseline, composed) {
  plain(composed);
  if (
    !Array.isArray(baseline) ||
    baseline.length !== roles.length + 1 ||
    !Array.isArray(composed)
  )
    denied();
  const required = new Map(baseline.map((row) => [row.id, row])),
    ids = new Set(),
    moduleNames = new Set(baseline.map((row) => row.name));
  for (const row of composed) {
    if (
      !row ||
      typeof row.id !== 'string' ||
      ids.has(row.id) ||
      typeof row.name !== 'string'
    )
      denied();
    ids.add(row.id);
    if (required.has(row.id)) {
      if (canonical(row) !== canonical(required.get(row.id))) denied();
    } else if (
      moduleNames.has(row.name) ||
      row.name.startsWith('@deepseek-ai/') ||
      row.name.startsWith('auriga-dsh') ||
      row.name.includes('/opt/auriga-dsh/')
    )
      denied();
  }
  for (const id of required.keys()) if (!ids.has(id)) denied();
  return true;
}

/** Resolve only a Node-approved resource's public module, from the fixed
 * writable profile installation. Approvals are internal verified-manifest
 * projections, never a browser/YAML file allowlist. */
export function createDedicatedResourceModuleResolver({
  requireFrom = createRequire,
  realpath = realpathSync,
  readFile = readFileSync,
} = {}) {
  let profileRequire;
  return (approval) => {
    if (!approval || typeof approval !== 'object' || Array.isArray(approval))
      denied();
    const { packageName, specifier, version } = approval;
    if (
      typeof packageName !== 'string' ||
      !/^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/.test(packageName) ||
      packageName.startsWith('@deepseek-ai/') ||
      packageName === 'auriga-dsh' ||
      typeof specifier !== 'string' ||
      (specifier !== packageName && !specifier.startsWith(packageName + '/')) ||
      /(?:^|\/)\.{1,2}(?:\/|$)|[\\?#%]/.test(specifier) ||
      typeof version !== 'string' ||
      !version
    )
      denied();
    profileRequire ??= requireFrom(DEDICATED_PROFILE_DIR + '/package.json');
    const root = realpath(join(DEDICATED_PROFILE_DIR, 'node_modules')),
      file = realpath(profileRequire.resolve(specifier));
    if (!within(root, file)) denied();
    let directory = dirname(file);
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
      if (manifest.name !== packageName || manifest.version !== version)
        denied();
      return pathToFileURL(file).href;
    }
    denied();
  };
}

export function createDedicatedResourceProfileResolver({
  resolveModule = createDedicatedResourceModuleResolver(),
} = {}) {
  return (baseline, composed, approvedRows) => {
    assertDedicatedBaselinePreserved(baseline, composed);
    if (!Array.isArray(approvedRows)) denied();
    const baseIds = new Set(baseline.map((row) => row.id)),
      approved = new Map();
    for (const row of approvedRows) {
      if (
        !row ||
        typeof row.id !== 'string' ||
        !row.id ||
        baseIds.has(row.id) ||
        approved.has(row.id) ||
        Object.keys(row).sort().join(',') !== 'id,packageName,specifier,version'
      )
        denied();
      approved.set(row.id, row);
    }
    return freeze(
      composed.map((row) => {
        const copy = JSON.parse(JSON.stringify(row));
        if (baseIds.has(row.id)) return copy;
        const approval = approved.get(row.id);
        if (
          !approval ||
          Object.keys(row).some(
            (key) =>
              !['id', 'name', 'config', 'disabled', 'inject'].includes(key),
          )
        )
          denied();
        const file = resolveModule(approval);
        if (
          typeof file !== 'string' ||
          !file.startsWith('file:///') ||
          (row.name !== approval.specifier && row.name !== file)
        )
          denied();
        copy.name = file;
        return copy;
      }),
    );
  };
}
export const resolveDedicatedResourceProfile =
  createDedicatedResourceProfileResolver();
