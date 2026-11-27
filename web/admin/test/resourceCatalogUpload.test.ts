import { expect, it } from 'vitest';
import { File as NodeFile } from 'node:buffer';
import { webcrypto } from 'node:crypto';
import { gunzipSync } from 'node:zlib';
import {
  prepareUpload,
  packSkillFolder,
  prepareFolderUpload,
} from '../src/admin/resourceCatalogUpload';
import { parseResourceSource } from '../src/admin/resourceUploadParser';
import { tarText } from '../src/admin/resourceSkillPreview';

Object.defineProperty(globalThis, 'crypto', {
  value: webcrypto,
  configurable: true,
});
Object.defineProperty(globalThis, 'File', {
  value: NodeFile,
  configurable: true,
});
const file = (path: string, text: string) => {
  const f = new NodeFile([text], path.split('/').at(-1)!) as unknown as File;
  Object.defineProperty(f, 'webkitRelativePath', { value: path });
  return f;
};
const manifest = (
  kind = 'skill',
  digest = 'sha256:' + 'a'.repeat(64),
  size = 2,
) => ({
  schemaVersion: 1,
  kind,
  name: 'sample',
  version: '1.0.0',
  description: 'Sample',
  author: { displayName: 'Author' },
  license: 'MIT',
  permissions: { tools: [], outboundHosts: [], dataAccess: [] },
  runtimes: [],
  artifacts: [
    {
      id: 'payload',
      role: 'payload',
      digest,
      sizeBytes: size,
      mediaType: 'application/gzip',
      os: 'any',
      arch: 'any',
    },
  ],
  signature: { mode: 'detached', algorithm: 'Ed25519' },
  ...(kind === 'skill'
    ? {
        skill: {
          entry: 'SKILL.md',
          contentProfile: 'text-only',
          dshVersionRange: '*',
        },
      }
    : { plugin: { packageName: '@example/sample', dshVersionRange: '*' } }),
});
it('packs a folder into bounded POSIX ustar/gzip with SKILL.md at root', async () => {
  const payload = await packSkillFolder([
    file(
      'sample/SKILL.md',
      '---\nname: sample\ndescription: Sample\n---\nText',
    ),
    file('sample/references/a.txt', 'Reference'),
  ]);
  const raw = gunzipSync(new Uint8Array(await payload.arrayBuffer()));
  expect(new TextDecoder().decode(raw.subarray(257, 263))).toBe('ustar\0');
  expect(tarText(raw).map((f) => f.path)).toEqual([
    'SKILL.md',
    'references/a.txt',
  ]);
  expect(raw.subarray(-1024).every((b) => b === 0)).toBe(true);
});
it('freezes a folder snapshot and writes the actual packed digest into a new manifest', async () => {
  const payload = await packSkillFolder([
    file(
      'sample/SKILL.md',
      '---\nname: sample\ndescription: Sample\n---\nText',
    ),
  ]);
  const template = file('auriga-resource.json', JSON.stringify(manifest()));
  const prepared = await prepareFolderUpload(template, payload);
  const actual =
    'sha256:' +
    Buffer.from(
      await webcrypto.subtle.digest('SHA-256', await payload.arrayBuffer()),
    ).toString('hex');
  expect(prepared.manifest.artifacts[0]!.digest).toBe(actual);
  expect(prepared.manifest.artifacts[0]!.sizeBytes).toBe(payload.size);
  expect(JSON.parse(await template.text()).artifacts[0].digest).not.toBe(
    actual,
  );
  expect(JSON.parse(await prepared.file.text()).artifacts[0].digest).toBe(
    actual,
  );
});
it('packs skill folder containing scripts and root dependency manifest and parses back through archive entry', async () => {
  const archive = await packSkillFolder([
    file(
      'sample/SKILL.md',
      '---\nname: sample\ndescription: Sample\n---\nBody',
    ),
    file('sample/requirements.txt', 'requests>=2.32\n'),
    file('sample/scripts/run.py', 'print("ok")\n'),
  ]);
  expect(archive.size).toBeGreaterThan(0);
  const parsed = await parseResourceSource([archive], 'skill', 'Alice');
  expect(parsed.manifest.name).toBe('sample');
  expect(parsed.skillFiles?.map((f) => f.webkitRelativePath).sort()).toEqual([
    'skill/SKILL.md',
    'skill/requirements.txt',
    'skill/scripts/run.py',
  ]);
});
it.each([
  '../SKILL.md',
  'other/a.md',
  'Scripts/a.py',
  'scripts/a.exe',
  'assets/a.js',
  'assets/CON.txt',
  'assets/a..txt/../x.md',
])('rejects unsafe folder path %s', async (path) => {
  await expect(
    packSkillFolder([
      file('sample/SKILL.md', 'Text'),
      file('sample/' + path, 'Bad'),
    ]),
  ).rejects.toThrow();
});
it('rejects case collisions, mixed roots, binary text and missing entry', async () => {
  for (const files of [
    [
      file('s/SKILL.md', 'Text'),
      file('s/assets/A.md', 'a'),
      file('s/assets/a.md', 'b'),
    ],
    [file('s/SKILL.md', 'Text'), file('t/assets/a.md', 'a')],
    [file('s/SKILL.md', '\0')],
    [file('s/assets/a.md', 'a')],
  ])
    await expect(packSkillFolder(files)).rejects.toThrow();
});
it('derives exact manifest hash and rejects mismatched artifact bytes and npm extensions', async () => {
  const bytes = file('payload.tgz', 'ok');
  const hash =
    'sha256:' +
    Buffer.from(
      await webcrypto.subtle.digest('SHA-256', await bytes.arrayBuffer()),
    ).toString('hex');
  const raw = file(
    'auriga-resource.json',
    JSON.stringify(manifest('plugin', hash), null, 2),
  );
  const prepared = await prepareUpload(raw, [bytes], 'plugin');
  expect(prepared.manifest.kind).toBe('plugin');
  expect(prepared.metadata.expectedDigest).toBe(
    'sha256:' +
      Buffer.from(
        await webcrypto.subtle.digest('SHA-256', await raw.arrayBuffer()),
      ).toString('hex'),
  );
  await expect(
    prepareUpload(raw, [file('payload.tgz', 'no')], 'plugin'),
  ).rejects.toThrow();
  await expect(
    prepareUpload(raw, [file('payload.gz', 'ok')], 'plugin'),
  ).rejects.toThrow();
  await expect(prepareUpload(raw, [bytes], 'skill')).rejects.toThrow();
});
