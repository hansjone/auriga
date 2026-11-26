/* global File, TextEncoder, TextDecoder, DecompressionStream */
import type { components } from '../../../shared/src/generated/resource';
import {
  packSkillFolder,
  prepareUpload,
  type CatalogKind,
} from './resourceCatalogUpload';
import { sha256 } from './resourceHash';

type Manifest = components['schemas']['UnifiedManifest'];
type Entry = { path: string; sizeBytes: number; bytes?: Uint8Array };
export type ResourceSource = {
  manifest: Manifest;
  payload: File;
  skillFiles?: File[];
  embedded: boolean;
  pluginVersion?: string;
  pluginPackageName?: string;
  engineRange?: string;
  warnings?: string[];
};
export class UploadParseError extends Error {
  constructor(
    public code: string,
    public path = '',
  ) {
    super(code);
  }
}
function fail(code: string, path = ''): never {
  throw new UploadParseError(code, path);
}
const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
// Kept in step with clients/installer/locks/dsh-0.2.1-rc.3.package-lock.json.
export const defaultDshRange = '^0.2.1-rc.3';
const semver =
  /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const namePattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const npmPattern = /^(?:@[a-z0-9-][a-z0-9._-]*\/)?[a-z0-9-][a-z0-9._-]*$/;
function npmName(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length <= 214 &&
    npmPattern.test(value) &&
    !['node_modules', 'favicon.ico'].includes(value)
  );
}
function text(bytes: Uint8Array, path: string) {
  try {
    return decoder.decode(bytes);
  } catch {
    return fail('textInvalid', path);
  }
}
function json(bytes: Uint8Array, path: string): Record<string, unknown> {
  try {
    const value = JSON.parse(text(bytes, path));
    if (!value || Array.isArray(value) || typeof value !== 'object')
      throw new Error();
    return value;
  } catch {
    return fail('jsonInvalid', path);
  }
}
function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
function entryBytes(entry: Entry) {
  if (!entry.bytes) fail('archiveInvalid', entry.path);
  return entry.bytes;
}
function validRange(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length > 0 &&
    value.length <= 128 &&
    (value === '*' ||
      value.split('||').every(
        (branch) =>
          branch.trim() &&
          branch
            .trim()
            .split(/\s+/)
            .every((term) =>
              semver.test(term.replace(/^(>=|<=|>|<|=|\^|~)/, '')),
            ),
      ))
  );
}
function safePath(path: string) {
  // eslint-disable-next-line no-control-regex
  const unsafeCharacters = /[\\:<>"|?*\x00-\x1f\x7f]/;
  if (
    !path ||
    path.startsWith('/') ||
    encoder.encode(path).length > 240 ||
    path
      .split('/')
      .some(
        (p) =>
          !p ||
          p === '.' ||
          p === '..' ||
          unsafeCharacters.test(p) ||
          /[. ]$/.test(p) ||
          /^(con|prn|aux|nul|conin\$|conout\$|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)/i.test(
            p,
          ),
      )
  )
    fail('pathUnsafe', path);
}
function systemFile(path: string) {
  return path
    .split('/')
    .some(
      (p) =>
        /^(?:\.DS_Store|Thumbs\.db|desktop\.ini|__MACOSX|\.git|node_modules)$/i.test(
          p,
        ) || p.startsWith('._'),
    );
}
async function archive(file: File, kind: CatalogKind): Promise<Entry[]> {
  if (
    !/\.(tgz|tar\.gz)$/i.test(file.name) ||
    (kind === 'plugin' && !/\.tgz$/i.test(file.name))
  )
    fail('archiveFormat');
  if (!file.size || file.size > (kind === 'plugin' ? 64 : 5) * 1024 * 1024)
    fail('sizeLimit');
  const limit = Math.min(
    (kind === 'plugin' ? 256 : 20) * 1024 * 1024,
    file.size * 100,
  );
  let raw: Uint8Array;
  try {
    const reader = file
      .stream()
      .pipeThrough(new DecompressionStream('gzip'))
      .getReader();
    const chunks: Uint8Array[] = [];
    let size = 0;
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        size += value.length;
        if (size > limit) fail('expandedLimit');
        chunks.push(value);
      }
    } finally {
      await reader.cancel();
    }
    raw = new Uint8Array(size);
    let at = 0;
    for (const chunk of chunks) {
      raw.set(chunk, at);
      at += chunk.length;
    }
  } catch (e) {
    if (e instanceof UploadParseError) throw e;
    return fail('archiveInvalid');
  }
  const entries: Entry[] = [],
    seen = new Map<string, boolean>();
  let offset = 0,
    count = 0,
    metadataSize = 0,
    metadataCount = 0;
  while (offset + 512 <= raw.length) {
    const h = raw.subarray(offset, offset + 512);
    if (h.every((b) => b === 0)) {
      if (
        raw.length % 512 ||
        raw.length - offset < 1024 ||
        !raw.subarray(offset).every((b) => b === 0)
      )
        fail('archiveInvalid');
      return entries;
    }
    if (++count > (kind === 'plugin' ? 4096 : 512)) fail('entryLimit');
    const str = (a: number, b: number) => {
      const part = h.subarray(a, b),
        zero = part.indexOf(0);
      if (zero >= 0 && part.subarray(zero).some((b) => b !== 0))
        fail('archiveInvalid');
      return text(zero < 0 ? part : part.subarray(0, zero), '');
    };
    const octal = (a: number, b: number) => {
      const value = text(h.subarray(a, b), '');
      if (!/^[ 0-7]*[\0 ]*$/.test(value)) fail('archiveInvalid');
      return parseInt(value.replace(/\0/g, '').trim() || '0', 8);
    };
    if (
      text(h.subarray(257, 263), '') !== 'ustar\0' ||
      text(h.subarray(263, 265), '') !== '00' ||
      h.subarray(500).some((b) => b !== 0)
    )
      fail('archiveFormat');
    const flag = h[156],
      directory = flag === 53;
    if (![0, 48, 53].includes(flag!)) fail('archiveUnsafe');
    const checksum = h.reduce(
      (sum, b, i) => sum + (i >= 148 && i < 156 ? 32 : b),
      0,
    );
    if (checksum !== octal(148, 156) || str(157, 257)) fail('archiveInvalid');
    for (const [a, b] of [
      [100, 108],
      [108, 116],
      [116, 124],
      [136, 148],
      [329, 337],
      [337, 345],
    ])
      octal(a!, b!);
    const prefix = str(345, 500),
      name = str(0, 100);
    const path = ((prefix ? prefix + '/' : '') + name).replace(
      directory ? /\/$/ : /$^/,
      '',
    );
    safePath(path);
    if (kind === 'plugin' && path !== 'package' && !path.startsWith('package/'))
      fail('pathUnsafe', path);
    if (
      kind === 'skill' &&
      directory &&
      path !== 'references' &&
      path !== 'assets' &&
      !path.startsWith('references/') &&
      !path.startsWith('assets/')
    )
      fail('skillFileInvalid', path);
    const key = path.normalize('NFC').toLowerCase();
    if (
      seen.has(key) ||
      [...seen].some(
        ([p, dir]) =>
          (!dir && key.startsWith(p + '/')) ||
          (!directory && p.startsWith(key + '/')),
      )
    )
      fail('pathCollision', path);
    seen.set(key, directory);
    const size = octal(124, 136),
      end = offset + 512 + size;
    if (
      !Number.isSafeInteger(size) ||
      (directory && size !== 0) ||
      end > raw.length
    )
      fail('archiveInvalid');
    if (kind === 'skill' && !directory && octal(100, 108) & 0o111)
      fail('archiveUnsafe');
    if (!directory) {
      if (kind === 'plugin' && path.endsWith('/package.json')) {
        metadataSize += size;
        metadataCount++;
        if (
          size > 1048576 ||
          metadataSize > 16 * 1024 * 1024 ||
          metadataCount > 512
        )
          fail('metadataLimit', path);
      }
      const capture =
        kind === 'skill' ||
        path.endsWith('/package.json') ||
        path === 'package/auriga.manifest.json';
      if (path === 'package/auriga.manifest.json' && size > 262144)
        fail('sizeLimit', path);
      // Only plugin metadata survives inspection; code/data entries retain paths
      // and sizes without allocating a second copy of their expanded bytes.
      entries.push({
        path,
        sizeBytes: size,
        ...(capture ? { bytes: raw.slice(offset + 512, end) } : {}),
      });
    }
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return fail('archiveInvalid');
}

// Parse the bounded scalar portion needed for the preview. Other allowed fields
// remain byte-for-byte in the skill body; complex name/description values fail explicitly.
function frontmatter(content: string) {
  const normalized = content.replace(/\r\n/g, '\n');
  const match = /^---\n([\s\S]*?)\n---\n/.exec(normalized);
  if (!match) fail('frontmatterInvalid');
  const lines = match[1]!.split('\n'),
    values: Record<string, string> = {},
    kept: string[] = [],
    seen = new Set<string>();
  for (let i = 0; i < lines.length;) {
    const line = lines[i]!;
    if (!line.trim() || line.trimStart().startsWith('#')) {
      kept.push(line);
      i++;
      continue;
    }
    const m = /^([A-Za-z][\w-]*):(?:\s+(.*))?$/.exec(line);
    if (!m || seen.has(m[1]!)) fail('frontmatterInvalid');
    const key = m[1]!;
    if (/^[&*!]\S/.test((m[2] ?? '').trim())) fail('frontmatterInvalid', key);
    seen.add(key);
    let j = i + 1;
    while (j < lines.length && (/^\s/.test(lines[j]!) || !lines[j]!.trim()))
      j++;
    if (
      ![
        'name',
        'description',
        'version',
        'author',
        'license',
        'whenToUse',
        'metadata',
        'disable-model-invocation',
        'user-invocable',
      ].includes(key)
    )
      fail('frontmatterInvalid', key);
    if (['name', 'description', 'version', 'author', 'license'].includes(key)) {
      let value = (m[2] ?? '').trim();
      if (/^[>|][-+]?$/.test(value)) {
        const block = lines.slice(i + 1, j),
          indent = Math.min(
            ...block
              .filter((s) => s.trim())
              .map((s) => /^ */.exec(s)![0].length),
          );
        value = block
          .map((s) => s.slice(indent))
          .join(value.startsWith('>') ? ' ' : '\n')
          .trim();
      } else if (value.startsWith('"')) {
        try {
          value = JSON.parse(value);
        } catch {
          fail('frontmatterInvalid', key);
        }
      } else if (value.startsWith("'")) {
        if (!value.endsWith("'")) fail('frontmatterInvalid', key);
        value = value.slice(1, -1).replace(/''/g, "'");
      } else {
        value = value.replace(/\s+#.*$/, '');
        if (
          /^[{[]/.test(value) ||
          (j > i + 1 && lines.slice(i + 1, j).some((s) => s.trim()))
        )
          fail('frontmatterInvalid', key);
      }
      if (typeof value !== 'string' || !value.trim())
        fail('frontmatterInvalid', key);
      values[key] = value;
    } else {
      if (
        lines
          .slice(i + 1, j)
          .some((line) => /^\s*(?:[^:#\n]+:\s*|-\s*)[&*!]\S/.test(line))
      )
        fail('frontmatterInvalid', key);
      kept.push(...lines.slice(i, j));
    }
    i = j;
  }
  if (!values.name || !values.description) fail('frontmatterInvalid');
  return {
    values,
    rest: kept.join('\n'),
    body: normalized.slice(match[0].length),
  };
}
function skillFile(path: string, content: string) {
  const f = new File([content], path.split('/').at(-1)!);
  Object.defineProperty(f, 'webkitRelativePath', { value: 'skill/' + path });
  return f;
}
function skillEntries(files: File[]) {
  if (!files.length || files.length > 512) fail('entryLimit');
  const root = files[0]!.webkitRelativePath?.split('/')[0];
  if (!root) fail('folderInvalid');
  return files.map((file) => {
    if (!file.webkitRelativePath.startsWith(root + '/')) fail('folderInvalid');
    return { path: file.webkitRelativePath.slice(root.length + 1), file };
  });
}
function validatePlugin(entries: Entry[]) {
  const metadata = entries.find((e) => e.path === 'package/package.json');
  if (!metadata) fail('missingPackage');
  const pkg = json(entryBytes(metadata), metadata.path);
  if (!npmName(pkg.name)) fail('npmNameInvalid');
  if (pkg.name === 'auriga-dsh' || pkg.name.startsWith('@deepseek-ai/'))
    fail('reservedPlugin');
  const files = new Set(entries.map((e) => e.path));
  for (const e of entries) {
    if (
      !e.path.startsWith('package/') ||
      /\/binding\.gyp$/i.test(e.path) ||
      systemFile(e.path.replace(/(?:^|\/)node_modules(?=\/)/g, ''))
    )
      fail('pathUnsafe', e.path);
    if (!e.path.endsWith('/package.json')) continue;
    const p = json(entryBytes(e), e.path),
      scripts = p.scripts;
    if (
      scripts != null &&
      (typeof scripts !== 'object' ||
        Array.isArray(scripts) ||
        Object.keys(scripts).length)
    )
      fail('pluginScripts', e.path);
    if (
      'gypfile' in p ||
      [
        'onlyBuiltDependencies',
        'allowBuilds',
        'onlyBuiltDependenciesFile',
        'dangerouslyAllowAllBuilds',
      ].some((k) => k in object(p.pnpm))
    )
      fail('pluginBuild', e.path);
    const bundled = p.bundledDependencies ?? p.bundleDependencies;
    for (const field of ['dependencies', 'optionalDependencies']) {
      if (
        p[field] != null &&
        (typeof p[field] !== 'object' || Array.isArray(p[field]))
      )
        fail('pluginDependencies', e.path);
      for (const dep of Object.keys(object(p[field]))) {
        if (
          !npmName(dep) ||
          !(
            bundled === true ||
            (Array.isArray(bundled) && bundled.includes(dep))
          ) ||
          !files.has(
            e.path.slice(0, -12) + 'node_modules/' + dep + '/package.json',
          )
        )
          fail('pluginDependencies', dep);
      }
    }
    for (const peer of Object.keys(object(p.peerDependencies)))
      if (!/^@deepseek-ai\/dsh(?:$|-.*)/.test(peer))
        fail('pluginDependencies', peer);
  }
  const patch = object(object(pkg.dsh).bundle).patch;
  if (typeof patch !== 'string') fail('pluginPatch');
  safePath(patch.replace(/^\.\//, ''));
  if (!files.has('package/' + patch.replace(/^\.\//, '')))
    fail('pluginPatch', patch);
  return pkg;
}
export async function parseResourceSource(
  files: File[],
  kind: CatalogKind,
  author: string,
): Promise<ResourceSource> {
  let entries: Entry[], payload: File, skillFiles: File[] | undefined;
  const folder = kind === 'skill' && files.some((f) => !!f.webkitRelativePath);
  if (folder) {
    const listed = skillEntries(files);
    if (!listed.some((e) => e.path === 'SKILL.md')) fail('missingSkill');
    let total = 0;
    for (const e of listed) {
      safePath(e.path);
      if (
        e.file.size >
        (e.path === 'SKILL.md' || e.path === 'auriga.manifest.json'
          ? 262144
          : 2097152)
      )
        fail('sizeLimit', e.path);
      total += e.file.size;
      if (total > 20 * 1024 * 1024) fail('expandedLimit');
    }
    entries = await Promise.all(
      listed.map(async (e) => ({
        path: e.path,
        sizeBytes: e.file.size,
        bytes: new Uint8Array(await e.file.arrayBuffer()),
      })),
    );
    payload = new File([], 'skill.tar.gz');
  } else {
    if (files.length !== 1) fail('selectOne');
    payload = files[0]!;
    entries = await archive(payload, kind);
  }
  let embedded: Record<string, unknown> | undefined;
  const advanced = entries.find(
    (e) =>
      e.path === (kind === 'plugin' ? 'package/' : '') + 'auriga.manifest.json',
  );
  if (advanced) {
    if (advanced.sizeBytes > 262144) fail('sizeLimit', advanced.path);
    embedded = json(entryBytes(advanced), advanced.path);
  }
  let base: Record<string, unknown>,
    pluginVersion: string | undefined,
    pluginPackageName: string | undefined,
    engineRange: string | undefined;
  const warnings: string[] = [];
  if (kind === 'plugin') {
    const pkg = validatePlugin(entries);
    pluginVersion = String(pkg.version);
    pluginPackageName = String(pkg.name);
    const engines = object(pkg.engines),
      peers = object(pkg.peerDependencies);
    engineRange = Object.hasOwn(engines, 'dsh')
      ? typeof engines.dsh === 'string'
        ? engines.dsh
        : ''
      : undefined;
    const derivedRange =
      engineRange ??
      peers['@deepseek-ai/dsh'] ??
      engines['@deepseek-ai/dsh'] ??
      defaultDshRange;
    const range = validRange(derivedRange) ? derivedRange : defaultDshRange;
    if (!validRange(derivedRange)) warnings.push('rangeDefaulted');
    base = {
      name: String(pkg.name)
        .split('/')
        .at(-1)!
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-|-$/g, ''),
      version: pkg.version,
      description: pkg.description ?? pkg.name,
      author: {
        displayName:
          typeof pkg.author === 'string'
            ? pkg.author
                .replace(/\s*<[^>]*>/g, '')
                .replace(/\s*\([^)]*\)/g, '')
                .trim() ||
              author ||
              'Author'
            : (object(pkg.author).name ?? (author || 'Author')),
      },
      license: pkg.license ?? 'UNLICENSED',
      plugin: { packageName: pkg.name, dshVersionRange: range },
    };
  } else {
    const skill = entries.find((e) => e.path === 'SKILL.md');
    if (!skill) fail('missingSkill');
    const fm = frontmatter(text(entryBytes(skill), skill.path));
    const seen = new Set<string>();
    skillFiles = [];
    for (const e of entries) {
      safePath(e.path);
      const key = e.path.normalize('NFC').toLowerCase();
      if (seen.has(key)) fail('pathCollision', e.path);
      seen.add(key);
      if (e.path === 'auriga.manifest.json') continue;
      if (systemFile(e.path)) fail('systemFile', e.path);
      if (
        e.path.split('/').length > 8 ||
        !(
          e.path === 'SKILL.md' ||
          /^(references|assets)\/.+\.(md|txt|json|yaml|yml)$/.test(e.path)
        )
      )
        fail('skillFileInvalid', e.path);
      if (e.sizeBytes > (e.path === 'SKILL.md' ? 262144 : 2097152))
        fail('sizeLimit', e.path);
      const content = text(entryBytes(e), e.path);
      // eslint-disable-next-line no-control-regex
      if (/[\x00-\x08\x0b\x0c\x0e-\x1f]/.test(content))
        fail('textInvalid', e.path);
      skillFiles.push(skillFile(e.path, content));
    }
    base = {
      name: fm.values.name,
      description: fm.values.description,
      version: fm.values.version ?? '1.0.0',
      author: { displayName: fm.values.author || author || 'Author' },
      license: fm.values.license || 'UNLICENSED',
      skill: {
        entry: 'SKILL.md',
        contentProfile: 'text-only',
        dshVersionRange: defaultDshRange,
      },
    };
  }
  const manifest = (embedded ?? {
    schemaVersion: 1,
    kind,
    ...base,
    permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    runtimes: [],
    signature: { mode: 'detached', algorithm: 'Ed25519' },
  }) as Manifest;
  if (embedded && (manifest.schemaVersion !== 1 || manifest.kind !== kind))
    fail('manifestInvalid');
  // Input widgets need strings even for legacy npm metadata. Preserve long
  // strings for correction and disclose any object/default conversion.
  const editableText = (value: unknown, fallback: string) => {
    if (typeof value === 'string') return value;
    warnings.push('metadataAdjusted');
    return typeof object(value).type === 'string'
      ? (object(value).type as string)
      : fallback;
  };
  manifest.name = editableText(manifest.name, '');
  manifest.version = editableText(manifest.version, '');
  manifest.description = editableText(manifest.description, '');
  manifest.author = {
    ...object(manifest.author),
    displayName: editableText(manifest.author?.displayName, author || 'Author'),
  };
  manifest.license = editableText(manifest.license, 'UNLICENSED');
  validateManifest(manifest, kind, true);
  if (
    manifest.kind === 'plugin' &&
    manifest.plugin.packageName !== pluginPackageName
  )
    fail('pluginMismatch');
  if (!namePattern.test(manifest.name) || manifest.name.length > 64)
    warnings.push('nameInvalid');
  if (!semver.test(manifest.version) || manifest.version.length > 128)
    warnings.push('versionInvalid');
  if (
    !manifest.description.trim() ||
    manifest.description.length > 1024 ||
    !manifest.author.displayName.trim() ||
    manifest.author.displayName.length > 128 ||
    !manifest.license.trim() ||
    manifest.license.length > 128
  )
    warnings.push('metadataInvalid');
  if (
    !validRange(
      manifest.kind === 'plugin'
        ? manifest.plugin.dshVersionRange
        : manifest.kind === 'skill'
          ? manifest.skill.dshVersionRange
          : '',
    )
  )
    warnings.push('rangeInvalid');
  const source: ResourceSource = {
    manifest,
    payload,
    skillFiles,
    embedded: !!embedded,
    pluginVersion,
    pluginPackageName,
    engineRange,
    warnings: [...new Set(warnings)],
  };
  const snapshot = await snapshotPayload(source, manifest);
  manifest.artifacts = [await payloadArtifact(snapshot)];
  return { ...source, payload: snapshot };
}
export function validateManifest(
  m: Manifest,
  kind: CatalogKind,
  preview = false,
) {
  if (!m || m.kind !== kind || m.schemaVersion !== 1) fail('manifestInvalid');
  const allowed = [
    'schemaVersion',
    'kind',
    'name',
    'version',
    'description',
    'author',
    'license',
    'permissions',
    'runtimes',
    'artifacts',
    'signature',
    kind,
  ];
  if (Object.keys(m).some((k) => !allowed.includes(k))) fail('manifestInvalid');
  if (
    !preview &&
    (typeof m.name !== 'string' ||
      !namePattern.test(m.name) ||
      m.name.length > 64)
  )
    fail('nameInvalid');
  if (
    !preview &&
    (typeof m.version !== 'string' ||
      m.version.length > 128 ||
      !semver.test(m.version))
  )
    fail('versionInvalid');
  for (const [value, max] of [
    [m.description, 1024],
    [m.author?.displayName, 128],
    [m.license, 128],
  ] as const)
    if (
      !preview &&
      (typeof value !== 'string' || !value.trim() || value.length > max)
    )
      fail('metadataInvalid');
  if (
    Object.keys(m.author).some((k) => k !== 'displayName') ||
    m.signature?.mode !== 'detached' ||
    m.signature.algorithm !== 'Ed25519' ||
    Object.keys(m.signature).length !== 2
  )
    fail('manifestInvalid');
  const part =
    m.kind === 'plugin' ? m.plugin : m.kind === 'skill' ? m.skill : undefined;
  if (!part || typeof part !== 'object' || Array.isArray(part))
    fail('manifestInvalid');
  if (
    (m.kind === 'plugin' && !npmName(m.plugin.packageName)) ||
    (m.kind === 'skill' &&
      (m.skill.entry !== 'SKILL.md' || m.skill.contentProfile !== 'text-only'))
  )
    fail('manifestInvalid');
  const partKeys =
    m.kind === 'plugin'
      ? ['packageName', 'dshVersionRange', 'tags']
      : ['entry', 'contentProfile', 'dshVersionRange'];
  if (Object.keys(part).some((k) => !partKeys.includes(k)))
    fail('manifestInvalid');
  if (m.kind === 'plugin' && m.plugin.tags !== undefined) {
    const tags = m.plugin.tags;
    if (
      !Array.isArray(tags) ||
      tags.length > 5 ||
      new Set(tags).size !== tags.length ||
      tags.some(
        (tag) =>
          typeof tag !== 'string' ||
          tag.length > 32 ||
          !/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(tag),
      )
    )
      fail('manifestInvalid');
  }
  const range = part.dshVersionRange;
  if (!preview && !validRange(range)) fail('rangeInvalid');
  const p = m.permissions;
  if (
    !p ||
    Object.keys(p).some(
      (k) => !['tools', 'outboundHosts', 'dataAccess'].includes(k),
    )
  )
    fail('permissionsInvalid');
  for (const [key, max, count] of [
    ['tools', 128, 32],
    ['outboundHosts', 253, 32],
    ['dataAccess', 128, 3],
  ] as const) {
    const values = p[key];
    if (
      !Array.isArray(values) ||
      values.length > count ||
      new Set(values).size !== values.length ||
      values.some((v) => typeof v !== 'string' || !v.trim() || v.length > max)
    )
      fail('permissionsInvalid');
  }
  if (
    p.dataAccess.some(
      (v) =>
        !['workspace_read', 'workspace_write', 'user_selected_files'].includes(
          v,
        ),
    ) ||
    p.outboundHosts.some(
      (h) =>
        h !== h.toLowerCase() ||
        !/^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/.test(
          h,
        ),
    )
  )
    fail('permissionsInvalid');
  if (!Array.isArray(m.runtimes) || m.runtimes.length > 8)
    fail('manifestInvalid');
  for (const runtime of m.runtimes) {
    if (
      !runtime ||
      !['node', 'python', 'java'].includes(runtime.kind) ||
      typeof runtime.versionRange !== 'string' ||
      !runtime.versionRange ||
      runtime.versionRange.length > 128 ||
      Object.keys(runtime).some((k) => !['kind', 'versionRange'].includes(k))
    )
      fail('manifestInvalid');
    const range = runtime.versionRange;
    if (
      range !== '*' &&
      range.split('||').some(
        (branch) =>
          !branch.trim() ||
          branch
            .trim()
            .split(/\s+/)
            .some(
              (term) => !semver.test(term.replace(/^(>=|<=|>|<|=|\^|~)/, '')),
            ),
      )
    )
      fail('rangeInvalid');
  }
}
export async function finalizeResourceUpload(
  source: ResourceSource,
  manifest = source.manifest,
) {
  const m = JSON.parse(JSON.stringify(manifest)) as Manifest;
  validateManifest(m, source.manifest.kind as CatalogKind);
  if (
    m.kind === 'plugin' &&
    (m.version !== source.pluginVersion ||
      m.plugin.packageName !== source.pluginPackageName)
  )
    fail('pluginMismatch');
  if (
    m.kind === 'plugin' &&
    source.engineRange !== undefined &&
    m.plugin.dshVersionRange !== source.engineRange
  )
    fail('rangeMismatch');
  const payload = await snapshotPayload(source, m);
  m.artifacts = [await payloadArtifact(payload)];
  const file = new File(
    [JSON.stringify(m, null, 2) + '\n'],
    'auriga-resource.json',
    { type: 'application/json' },
  );
  return prepareUpload(file, [payload], m.kind as CatalogKind);
}
async function snapshotPayload(source: ResourceSource, m: Manifest) {
  let payload = source.payload;
  if (source.skillFiles) {
    const files = await Promise.all(
      source.skillFiles.map(async (f) => {
        if (f.webkitRelativePath !== 'skill/SKILL.md') return f;
        const fm = frontmatter(await f.text());
        return skillFile(
          'SKILL.md',
          `---\nname: ${JSON.stringify(m.name)}\ndescription: ${JSON.stringify(m.description)}\n${fm.rest ? fm.rest + '\n' : ''}---\n${fm.body}`,
        );
      }),
    );
    try {
      payload = await packSkillFolder(files);
    } catch {
      return fail('skillSnapshotInvalid');
    }
  }
  return payload;
}
async function payloadArtifact(
  payload: File,
): Promise<Manifest['artifacts'][number]> {
  return {
    id: 'payload',
    role: 'payload',
    digest: 'sha256:' + (await sha256(payload)),
    sizeBytes: payload.size,
    mediaType: 'application/gzip',
    os: 'any',
    arch: 'any',
  };
}
