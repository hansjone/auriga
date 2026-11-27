/* global File, TextEncoder, TextDecoder, CompressionStream, ReadableStream, BufferSource */
import type { components } from '../../../shared/src/generated/resource';
import { sha256 } from './resourceHash';

type Manifest = components['schemas']['UnifiedManifest'];
export type CatalogKind = 'plugin' | 'skill';
export type PreparedUpload = {
  manifest: Manifest;
  file: File;
  artifacts: File[];
  metadata: components['schemas']['UnifiedUploadMetadata'];
};
const encoder = new TextEncoder();
const invalid = () => new Error('Invalid resource upload');
export async function prepareFolderUpload(template: File, payload: File) {
  if (!template.size || template.size > 262144) throw invalid();
  const manifest: Manifest = JSON.parse(
    new TextDecoder('utf-8', { fatal: true }).decode(
      await template.arrayBuffer(),
    ),
  );
  if (manifest.kind !== 'skill') throw invalid();
  manifest.artifacts = [
    {
      id: manifest.artifacts?.[0]?.id ?? 'payload',
      role: 'payload',
      digest: 'sha256:' + (await sha256(payload)),
      sizeBytes: payload.size,
      mediaType: 'application/gzip',
      os: 'any',
      arch: 'any',
    },
  ];
  const file = new File(
    [JSON.stringify(manifest, null, 2) + '\n'],
    'auriga-resource.json',
    { type: 'application/json' },
  );
  return prepareUpload(file, [payload], 'skill');
}
export async function prepareUpload(
  file: File,
  artifacts: File[],
  kind: CatalogKind,
): Promise<PreparedUpload> {
  if (!file.size || file.size > 262144 || artifacts.length !== 1)
    throw invalid();
  const raw = await file.arrayBuffer();
  const manifest: Manifest = JSON.parse(
    new TextDecoder('utf-8', { fatal: true }).decode(raw),
  );
  if (
    !manifest ||
    manifest.schemaVersion !== 1 ||
    manifest.kind !== kind ||
    !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(manifest.name) ||
    manifest.name.length > 64 ||
    !manifest.version ||
    !Array.isArray(manifest.artifacts) ||
    manifest.artifacts.length !== 1 ||
    manifest.signature?.mode !== 'detached' ||
    manifest.signature.algorithm !== 'Ed25519' ||
    (manifest.kind === 'plugin' && !manifest.plugin?.packageName) ||
    (manifest.kind === 'skill' && manifest.skill?.entry !== 'SKILL.md')
  )
    throw invalid();
  const artifact = manifest.artifacts[0]!,
    payload = artifacts[0]!;
  if (
    artifact.role !== 'payload' ||
    artifact.mediaType !== 'application/gzip' ||
    artifact.os !== 'any' ||
    artifact.arch !== 'any' ||
    !payload.size ||
    payload.size > (kind === 'plugin' ? 64 : 5) * 1024 * 1024 ||
    artifact.sizeBytes !== payload.size ||
    (kind === 'plugin' && !payload.name.toLowerCase().endsWith('.tgz')) ||
    artifact.digest !== 'sha256:' + (await sha256(payload))
  )
    throw invalid();
  return {
    manifest,
    file,
    artifacts: [...artifacts],
    metadata: {
      version: manifest.version,
      expectedDigest: 'sha256:' + (await sha256(file)),
    },
  };
}

// Browser directory selection strips exactly one root. No submitted content is executed.
export async function packSkillFolder(files: File[]): Promise<File> {
  if (!files.length || files.length > 512) throw invalid();
  const root = files[0]!.webkitRelativePath.split('/')[0];
  const seen = new Set<string>(),
    directories = new Set<string>();
  const entries: { path: string; file: File }[] = [];
  let size = 1024;
  for (const file of files) {
    const relative = file.webkitRelativePath;
    if (!root || !relative.startsWith(root + '/')) throw invalid();
    const path = relative.slice(root.length + 1),
      parts = path.split('/');
    const slash = path.lastIndexOf('/');
    const dot = path.lastIndexOf('.');
    const ext = dot > slash ? path.slice(dot).toLowerCase() : '';
    const allowedSkillPath =
      path === 'SKILL.md' ||
      [
        'requirements.txt',
        'pyproject.toml',
        'package.json',
        'uv.lock',
        'poetry.lock',
      ].includes(path) ||
      ((path.startsWith('references/') || path.startsWith('assets/')) &&
        [
          '.md',
          '.txt',
          '.json',
          '.yaml',
          '.yml',
          '.toml',
          '.ini',
          '.cfg',
          '.csv',
          '.sql',
        ].includes(ext)) ||
      (path.startsWith('scripts/') &&
        [
          '.py',
          '.sh',
          '.bash',
          '.ps1',
          '.js',
          '.mjs',
          '.cjs',
          '.ts',
          '.mts',
          '.cts',
          '.md',
          '.txt',
          '.json',
          '.yaml',
          '.yml',
          '.toml',
          '.ini',
          '.cfg',
          '.csv',
          '.sql',
          '.lock',
        ].includes(ext));
    if (
      parts.length > 8 ||
      encoder.encode(path).length > 240 ||
      parts.some(
        (p) =>
          !p ||
          p === '.' ||
          p === '..' ||
          /[\\:<>"|?*]/.test(p) ||
          [...p].some((c) => {
            const code = c.codePointAt(0) ?? 0;
            if (path.startsWith('scripts/')) {
              return !(
                (code >= 0x20 && code <= 0x7e) ||
                (code >= 0x4e00 && code <= 0x9fff)
              );
            }
            return code < 32 || (code >= 0x7f && code <= 0x9f);
          }) ||
          /[. ]$/.test(p) ||
          /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(p),
      ) ||
      !allowedSkillPath ||
      file.size > (path === 'SKILL.md' ? 262144 : 2 * 1024 * 1024)
    )
      throw invalid();
    const normalized = path
      .normalize('NFC')
      .toUpperCase()
      .toLowerCase()
      .normalize('NFC');
    if (seen.has(normalized)) throw invalid();
    seen.add(normalized);
    for (let i = 1; i < parts.length; i++)
      directories.add(
        parts
          .slice(0, i)
          .join('/')
          .normalize('NFC')
          .toUpperCase()
          .toLowerCase()
          .normalize('NFC'),
      );
    entries.push({ path, file });
    size += 512 + Math.ceil(file.size / 512) * 512;
    if (size > 20 * 1024 * 1024 || files.length + directories.size > 512)
      throw invalid();
  }
  if (
    !entries.some((e) => e.path === 'SKILL.md') ||
    [...directories].some((d) => seen.has(d))
  )
    throw invalid();
  const tar = new Uint8Array(size);
  let offset = 0;
  for (const { path, file } of entries.sort((a, b) =>
    a.path < b.path ? -1 : a.path > b.path ? 1 : 0,
  )) {
    const bytes = new Uint8Array(await file.arrayBuffer());
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
    if (text.includes('\0')) throw invalid();
    const header = tar.subarray(offset, offset + 512);
    const put = (s: string, at: number) => header.set(encoder.encode(s), at);
    const octal = (n: number, width: number) =>
      n.toString(8).padStart(width - 1, '0') + '\0';
    let name = path,
      prefix = '';
    if (encoder.encode(name).length > 100) {
      const split = Array.from(path.matchAll(/\//g), (m) => m.index).find(
        (i) =>
          encoder.encode(path.slice(i + 1)).length <= 100 &&
          encoder.encode(path.slice(0, i)).length <= 155,
      );
      if (split === undefined) throw invalid();
      prefix = path.slice(0, split);
      name = path.slice(split + 1);
    }
    put(name, 0);
    put('0000644\0', 100);
    put('0000000\0', 108);
    put('0000000\0', 116);
    put(octal(bytes.length, 12), 124);
    put(octal(0, 12), 136);
    put('        ', 148);
    put('0', 156);
    put('ustar\0', 257);
    put('00', 263);
    put(prefix, 345);
    put(
      header
        .reduce((n, b) => n + b, 0)
        .toString(8)
        .padStart(6, '0') + '\0 ',
      148,
    );
    tar.set(bytes, offset + 512);
    offset += 512 + Math.ceil(bytes.length / 512) * 512;
  }
  const stream = new ReadableStream<BufferSource>({
    start(c) {
      c.enqueue(tar);
      c.close();
    },
  });
  const gzip = await new Response(
    stream.pipeThrough(new CompressionStream('gzip')),
  ).arrayBuffer();
  if (gzip.byteLength > 5 * 1024 * 1024 || size > gzip.byteLength * 100)
    throw invalid();
  return new File([gzip], 'skill.tar.gz', { type: 'application/gzip' });
}
