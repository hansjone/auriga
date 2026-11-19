// @ts-nocheck
import { createHash } from 'node:crypto';
import {
  constants,
  lstatSync,
  openSync,
  fstatSync,
  readFileSync,
  closeSync,
  readdirSync,
} from 'node:fs';
import { join, resolve, relative, isAbsolute, sep } from 'node:path';
import { canonical, fail, validate } from '../shared/protocol.js';
import { parseStrictJson } from '../resources/json.js';
import { schemaValid } from '../resources/schema.js';
import { validateArchive } from '../resources/archive.js';
import { unpackSkill } from '../resources/skill-package.js';
import { validatePrivateSkill } from '../resources/private-skills.js';

export const RESOURCE_ROOT = '/run/auriga/dedicated-resources';
export const sha = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');
const digest = /^sha256:[a-f0-9]{64}$/;
const uuid = /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/;
const copy = (value) => JSON.parse(JSON.stringify(value));

export function exact(value, keys) {
  if (
    !value ||
    typeof value !== 'object' ||
    Array.isArray(value) ||
    Object.keys(value).sort().join(',') !== [...keys].sort().join(',')
  )
    fail('common.validation_failed');
}
export function safeInteger(value, minimum = 0) {
  if (typeof value === 'string') {
    if (!/^(?:0|[1-9][0-9]*)$/.test(value)) fail('common.validation_failed');
    value = Number(value);
  }
  if (!Number.isSafeInteger(value) || value < minimum)
    fail('common.validation_failed');
  return value;
}
/** The Node UDS uses descriptor-driven safe integers. A Node-written protoJSON
 * view is normalized only at these exact typed fields, never by key guessing. */
export function normalizeView(input) {
  const view = copy(input);
  exact(view, [
    'schemaVersion',
    'binding',
    'viewId',
    'authorizationSetDigest',
    'source',
    'resources',
  ]);
  view.schemaVersion = safeInteger(view.schemaVersion, 1);
  view.binding = normalizeBinding(view.binding);
  if (!Array.isArray(view.resources) || view.resources.length > 256)
    fail('common.validation_failed');
  for (const row of view.resources) {
    exact(row, ['resourceId', 'digest', 'leaseId', 'expiresAt']);
    row.expiresAt = safeInteger(row.expiresAt);
  }
  validate('ResourceView', view);
  if (view.binding.mode !== 'dedicated') fail('shared.binding_mismatch');
  return view;
}
export function normalizeBinding(input) {
  const binding = copy(input);
  exact(binding, [
    'tenantId',
    'memberId',
    'placementId',
    'physicalInstanceId',
    'mode',
    'epoch',
    'revision',
  ]);
  binding.epoch = safeInteger(binding.epoch, 1);
  binding.revision = safeInteger(binding.revision, 1);
  if (binding.mode === 'DSH_INSTANCE_MODE_DEDICATED')
    binding.mode = 'dedicated';
  validate('Binding', binding);
  if (binding.mode !== 'dedicated') fail('shared.binding_mismatch');
  return binding;
}
export function sameBinding(left, right) {
  validate('Binding', left);
  validate('Binding', right);
  if (
    left.mode !== 'dedicated' ||
    right.mode !== 'dedicated' ||
    canonical(left) !== canonical(right)
  )
    fail('shared.binding_mismatch');
}
/** Only installation-owned paths are accepted. No symlinks at any level and no
 * caller-selected artifact path is part of the resource intent. */
export function readOwnedFile(file, maximum, root = undefined) {
  const absolute = resolve(file);
  if (!isAbsolute(file) || absolute !== file || file.includes('\0'))
    fail('resource.apply_failed');
  if (root) {
    const suffix = relative(resolve(root), absolute);
    if (suffix === '..' || suffix.startsWith('..' + sep) || isAbsolute(suffix))
      fail('resource.apply_failed');
  }
  const parts = absolute.split(sep);
  let current = parts.shift() + sep;
  for (const part of parts) {
    if (!part) continue;
    current = join(current, part);
    const info = lstatSync(current);
    if (info.isSymbolicLink()) fail('resource.apply_failed');
  }
  const before = lstatSync(absolute);
  if (
    !before.isFile() ||
    before.size > maximum ||
    (process.platform === 'linux' && before.mode & 0o022)
  )
    fail('resource.apply_failed');
  const fd = openSync(
    absolute,
    constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
  );
  try {
    const after = fstatSync(fd);
    if (
      after.dev !== before.dev ||
      after.ino !== before.ino ||
      after.size !== before.size
    )
      fail('resource.apply_failed');
    return readFileSync(fd);
  } finally {
    closeSync(fd);
  }
}
export function readIntent(
  file,
  binding,
  now = () => Math.floor(Date.now() / 1000),
) {
  const value = parseStrictJson(readOwnedFile(file, 1048576), true);
  exact(value, ['schemaVersion', 'view', 'resources']);
  if (value.schemaVersion !== 1) fail('common.validation_failed');
  value.view = normalizeView(value.view);
  sameBinding(value.view.binding, binding);
  if (
    !Array.isArray(value.resources) ||
    value.resources.length !== value.view.resources.length
  )
    fail('common.validation_failed');
  const seen = new Set();
  for (const row of value.resources) {
    exact(row, [
      'resourceId',
      'manifestDigest',
      'artifactDigest',
      'artifactBytes',
      'installationRevision',
    ]);
    if (
      !uuid.test(row.resourceId) ||
      !digest.test(row.manifestDigest) ||
      !digest.test(row.artifactDigest) ||
      seen.has(row.resourceId)
    )
      fail('common.validation_failed');
    seen.add(row.resourceId);
    row.artifactBytes = safeInteger(row.artifactBytes, 1);
    row.installationRevision = safeInteger(row.installationRevision, 1);
    if (row.artifactBytes > 67108864) fail('common.validation_failed');
    const match = value.view.resources.filter(
      (resource) => resource.resourceId === row.resourceId,
    );
    if (
      match.length !== 1 ||
      match[0].digest !== row.manifestDigest ||
      match[0].expiresAt <= now()
    )
      fail('shared.revoked');
  }
  const normalized = canonical(value);
  return { value, normalized, intentDigest: sha(Buffer.from(normalized)) };
}

export async function loadResources(intent, artifactRoot = RESOURCE_ROOT) {
  const resources = [],
    names = new Set();
  for (const row of intent.value.resources) {
    const manifestFile = join(
      artifactRoot,
      row.manifestDigest.slice(7),
      'manifest.json',
    );
    const manifestBytes = readOwnedFile(manifestFile, 262144, artifactRoot);
    if (sha(manifestBytes) !== row.manifestDigest)
      fail('resource.signature_invalid');
    const manifest = parseStrictJson(manifestBytes);
    if (
      !schemaValid('package', 'Manifest', manifest) ||
      !['plugin', 'skill'].includes(manifest.kind) ||
      manifest.artifacts.length !== 1
    )
      fail('resource.apply_failed');
    const artifact = manifest.artifacts[0];
    if (
      artifact.digest !== row.artifactDigest ||
      safeInteger(artifact.sizeBytes, 1) !== row.artifactBytes
    )
      fail('resource.signature_invalid');
    const artifactFile = join(
      artifactRoot,
      row.artifactDigest.slice(7),
      'payload.tgz',
    );
    const bytes = readOwnedFile(
      artifactFile,
      manifest.kind === 'skill' ? 5242880 : 67108864,
      artifactRoot,
    );
    if (bytes.length !== row.artifactBytes || sha(bytes) !== row.artifactDigest)
      fail('resource.signature_invalid');
    const name =
      manifest.kind === 'plugin' ? manifest.plugin.packageName : manifest.name;
    if (name === 'auriga-dsh' || names.has(name)) fail('resource.apply_failed');
    names.add(name);
    const resource = {
      ...row,
      kind: manifest.kind,
      name,
      version: manifest.version,
      artifactFile,
      manifest,
    };
    if (manifest.kind === 'plugin') {
      await validateArchive(bytes, { name, version: manifest.version });
    } else {
      const files = unpackSkill(bytes, {
        name,
        description: manifest.description,
      });
      const directory = join(
        artifactRoot,
        row.artifactDigest.slice(7),
        'skill',
      );
      const actual = [];
      function scan(where, prefix = '') {
        const info = lstatSync(where);
        if (!info.isDirectory() || info.isSymbolicLink())
          fail('resource.apply_failed');
        for (const entry of readdirSync(where, { withFileTypes: true })) {
          const key = prefix + entry.name;
          if (entry.isSymbolicLink()) fail('resource.apply_failed');
          if (entry.isDirectory()) scan(join(where, entry.name), key + '/');
          else if (entry.isFile()) actual.push(key);
          else fail('resource.apply_failed');
        }
      }
      scan(directory);
      if (canonical(actual.sort()) !== canonical(Object.keys(files).sort()))
        fail('resource.signature_invalid');
      for (const [key, expected] of Object.entries(files)) {
        if (
          !readOwnedFile(join(directory, key), 2097152, artifactRoot).equals(
            expected,
          )
        )
          fail('resource.signature_invalid');
      }
      const text = files['SKILL.md'].toString('utf8');
      const metadata = validatePrivateSkill(text);
      resource.skill = {
        name,
        description: manifest.description,
        source: 'custom',
        provider: 'auriga-dedicated-catalog',
        invocation: {
          modelInvocable: metadata['disable-model-invocation'] !== true,
          userInvocable: metadata['user-invocable'] !== false,
        },
        path: join(directory, 'SKILL.md'),
        resourceBase: { kind: 'directory', path: directory },
        content: text.replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, ''),
        ...(metadata.whenToUse === undefined
          ? {}
          : { whenToUse: metadata.whenToUse }),
      };
    }
    resources.push(resource);
  }
  return resources;
}
