import { expect, it } from 'vitest';
import { File as NodeFile } from 'node:buffer';
import { webcrypto } from 'node:crypto';
import { gzipSync } from 'node:zlib';
import {
  parseResourceSource,
  finalizeResourceUpload,
  UploadParseError,
} from '../src/admin/resourceUploadParser';
import { tarText } from '../src/admin/resourceSkillPreview';
import { gunzipSync } from 'node:zlib';
Object.defineProperty(globalThis, 'File', {
  value: NodeFile,
  configurable: true,
});
Object.defineProperty(globalThis, 'crypto', {
  value: webcrypto,
  configurable: true,
});
export function folderFile(path: string, text: string) {
  const f = new NodeFile([text], path.split('/').at(-1)!) as unknown as File;
  Object.defineProperty(f, 'webkitRelativePath', { value: 'sample/' + path });
  return f;
}
export function pluginFile(entries: Record<string, string>, type = '0') {
  const blocks: Buffer[] = [];
  for (const [path, text] of Object.entries(entries)) {
    const data = Buffer.from(text),
      h = Buffer.alloc(512);
    h.write(path);
    h.write('0000644\0', 100);
    h.write('0000000\0', 108);
    h.write('0000000\0', 116);
    h.write(data.length.toString(8).padStart(11, '0') + '\0', 124);
    h.write('00000000000\0', 136);
    h.fill(32, 148, 156);
    h.write(type, 156);
    h.write('ustar\0', 257);
    h.write('00', 263);
    h.write(
      h
        .reduce((n, b) => n + b, 0)
        .toString(8)
        .padStart(6, '0') + '\0 ',
      148,
    );
    blocks.push(h, data, Buffer.alloc((512 - (data.length % 512)) % 512));
  }
  return new NodeFile(
    [gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]))],
    'plugin.tgz',
  ) as unknown as File;
}
const pkg = {
  name: '@example/sample_plugin',
  version: '2.0.0',
  description: 'Example',
  author: { name: 'Alice' },
  license: 'MIT',
  peerDependencies: { '@deepseek-ai/dsh': '^0.2.1-rc.3' },
  dsh: { bundle: { patch: 'index.js' } },
};
const plugin = (p: Record<string, unknown> = pkg, extra = {}) =>
  pluginFile({
    'package/package.json': JSON.stringify(p),
    'package/index.js': 'export default {}',
    ...extra,
  });
it('reads plugin npm metadata and generates exact artifact and manifest hashes', async () => {
  const source = await parseResourceSource([plugin()], 'plugin', 'Current');
  expect(source.manifest).toMatchObject({
    name: 'sample-plugin',
    version: '2.0.0',
    author: { displayName: 'Alice' },
    plugin: { packageName: pkg.name, dshVersionRange: '^0.2.1-rc.3' },
    signature: { mode: 'detached', algorithm: 'Ed25519' },
  });
  const ready = await finalizeResourceUpload(source);
  expect(ready.manifest.artifacts[0]!.sizeBytes).toBe(source.payload.size);
  expect(ready.metadata.expectedDigest).toMatch(/^sha256:[0-9a-f]{64}$/);
});
it('reads folded and quoted skill frontmatter, extracts version, and excludes advanced metadata from the snapshot', async () => {
  const source = await parseResourceSource(
    [
      folderFile(
        'SKILL.md',
        '---\nname: sample\ndescription: >-\n  Useful skill\n  for teams\nversion: 2.1.0\n---\nBody',
      ),
    ],
    'skill',
    'Current',
  );
  expect(source.manifest).toMatchObject({
    version: '2.1.0',
    description: 'Useful skill for teams',
    author: { displayName: 'Current' },
    license: 'UNLICENSED',
  });
  const texts = tarText(
    gunzipSync(new Uint8Array(await source.payload.arrayBuffer())),
  );
  expect(texts[0]!.text).not.toContain('version:');
  const again = await parseResourceSource([source.payload], 'skill', 'Current');
  expect(again.manifest.name).toBe('sample');
});
it('honors an embedded advanced manifest and replaces stale artifact metadata', async () => {
  const base = await parseResourceSource([plugin()], 'plugin', 'Current');
  const manifest = {
    ...base.manifest,
    name: 'custom-name',
    description: 'Custom',
    artifacts: [],
  };
  const source = await parseResourceSource(
    [plugin(pkg, { 'package/auriga.manifest.json': JSON.stringify(manifest) })],
    'plugin',
    'Current',
  );
  expect(source.manifest.name).toBe('custom-name');
  expect(source.manifest.description).toBe('Custom');
  expect(source.manifest.artifacts[0]!.sizeBytes).toBe(source.payload.size);
});
it.each([
  [() => pluginFile({ 'package/index.js': 'x' }), 'plugin', 'missingPackage'],
  [() => plugin({ ...pkg, name: '_bad' }), 'plugin', 'npmNameInvalid'],
  [
    () => pluginFile({ 'package/package.json': JSON.stringify(pkg) }, '2'),
    'plugin',
    'archiveUnsafe',
  ],
  [
    () => pluginFile({ '../package/package.json': JSON.stringify(pkg) }),
    'plugin',
    'pathUnsafe',
  ],
  [
    () => plugin({ ...pkg, scripts: { postinstall: 'sh x' } }),
    'plugin',
    'pluginScripts',
  ],
  [() => folderFile('assets/.DS_Store', 'x'), 'skill', 'missingSkill'],
  [
    () => folderFile('SKILL.md', 'no frontmatter'),
    'skill',
    'frontmatterInvalid',
  ],
  [
    () => folderFile('SKILL.md', '---\nname: Bad Name\ndescription: x\n---\nx'),
    'skill',
    'nameInvalid',
  ],
] as const)(
  'rejects malformed sources with a specific reason %#',
  async (make, kind, code) => {
    await expect(
      parseResourceSource([make()], kind, 'Current'),
    ).rejects.toMatchObject({ code });
  },
);
it('rejects system files, oversized entries, collisions, unsafe frontmatter and mismatched advanced metadata', async () => {
  const skill = folderFile(
    'SKILL.md',
    '---\nname: sample\ndescription: Test\n---\nBody',
  );
  for (const files of [
    [skill, folderFile('.DS_Store', 'x')],
    [skill, folderFile('assets/a.md', '\0')],
    [skill, folderFile('assets/A.md', 'a'), folderFile('assets/a.md', 'b')],
    [skill, folderFile('assets/large.txt', 'x'.repeat(2097153))],
  ]) {
    await expect(
      parseResourceSource(files, 'skill', 'Current'),
    ).rejects.toBeInstanceOf(UploadParseError);
  }
  await expect(
    parseResourceSource(
      [plugin(pkg, { 'package/auriga.manifest.json': '{}' })],
      'plugin',
      'Current',
    ),
  ).rejects.toMatchObject({ code: 'manifestInvalid' });
});
it('keeps plugin archive bytes unchanged and uses a locked compatible DSH default', async () => {
  const bytes = plugin({ ...pkg, peerDependencies: {} });
  const source = await parseResourceSource([bytes], 'plugin', 'Current');
  expect(await source.payload.arrayBuffer()).toEqual(await bytes.arrayBuffer());
  expect(
    source.manifest.kind === 'plugin' && source.manifest.plugin.dshVersionRange,
  ).toBe('^0.2.1-rc.3');
});
it('uses skill advanced metadata, synchronizes the editable preview, and keeps only supported payload files', async () => {
  const skill = folderFile(
    'SKILL.md',
    '---\nname: sample\ndescription: Original\nversion: 2.0.0\nmetadata:\n  category: docs\n---\nBody',
  );
  const original = await parseResourceSource([skill], 'skill', 'Current');
  const embedded = {
    ...original.manifest,
    name: 'advanced-skill',
    description: 'Advanced description',
    version: '3.0.0',
  };
  const source = await parseResourceSource(
    [skill, folderFile('auriga.manifest.json', JSON.stringify(embedded))],
    'skill',
    'Current',
  );
  const ready = await finalizeResourceUpload(source, {
    ...source.manifest,
    name: 'edited',
    version: '4.0.0',
  });
  const entries = tarText(
    gunzipSync(new Uint8Array(await ready.artifacts[0]!.arrayBuffer())),
  );
  expect(entries.map((e) => e.path)).toEqual(['SKILL.md']);
  expect(entries[0]!.text).toContain('name: "edited"');
  expect(entries[0]!.text).toContain('description: "Advanced description"');
  expect(entries[0]!.text).toContain('  category: docs');
  expect(entries[0]!.text).not.toContain('version:');
  expect(ready.manifest.version).toBe('4.0.0');
});
it('rejects malformed permissions and archive corruption without executing content', async () => {
  const source = await parseResourceSource([plugin()], 'plugin', 'Current');
  await expect(
    finalizeResourceUpload(source, {
      ...source.manifest,
      permissions: {
        tools: [],
        outboundHosts: ['https://example.com'],
        dataAccess: [],
      },
    }),
  ).rejects.toMatchObject({ code: 'permissionsInvalid' });
  await expect(
    parseResourceSource(
      [new NodeFile(['broken'], 'bad.tgz') as unknown as File],
      'plugin',
      'Current',
    ),
  ).rejects.toMatchObject({ code: 'archiveInvalid' });
  await expect(
    parseResourceSource(
      [pluginFile({ 'package/a': 'x' }, 'x')],
      'plugin',
      'Current',
    ),
  ).rejects.toMatchObject({ code: 'archiveUnsafe' });
  await expect(
    parseResourceSource(
      [
        folderFile(
          'SKILL.md',
          '---\nname: sample\nname: duplicate\ndescription: Test\n---\nBody',
        ),
      ],
      'skill',
      'Current',
    ),
  ).rejects.toMatchObject({ code: 'frontmatterInvalid' });
});
it('preserves a reference named SKILL.md without treating it as the root entry', async () => {
  const source = await parseResourceSource(
    [
      folderFile('SKILL.md', '---\nname: sample\ndescription: Test\n---\nBody'),
      folderFile(
        'references/SKILL.md',
        'Plain reference, without frontmatter.',
      ),
    ],
    'skill',
    'Current',
  );
  const entries = tarText(
    gunzipSync(new Uint8Array(await source.payload.arrayBuffer())),
  );
  expect(entries.find((e) => e.path === 'references/SKILL.md')!.text).toBe(
    'Plain reference, without frontmatter.',
  );
});
it('extracts optional skill author and license into metadata accepted by the existing upload API', async () => {
  const source = await parseResourceSource(
    [
      folderFile(
        'SKILL.md',
        '---\nname: sample\ndescription: Test\nauthor: Original author\nlicense: MIT\n---\nBody',
      ),
    ],
    'skill',
    'Current',
  );
  expect(source.manifest.author.displayName).toBe('Original author');
  expect(source.manifest.license).toBe('MIT');
  const entry = tarText(
    gunzipSync(new Uint8Array(await source.payload.arrayBuffer())),
  )[0]!;
  expect(entry.text).not.toContain('author:');
  expect(entry.text).not.toContain('license:');
});
