import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  link,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { gunzipSync } from 'node:zlib';
import { prepareDirectory } from '../../src/resources/submission-package.js';
import { unpackSkill } from '../../src/resources/skill-package.js';

const metadata = {
  author: 'Alice',
  license: 'MIT',
  permissions: { tools: [], outboundHosts: [], dataAccess: [] },
};

async function fixture(t) {
  const cwd = await mkdtemp(join(tmpdir(), 'auriga-submit-'));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const put = async (name, bytes) => {
    const path = join(cwd, name);
    await mkdir(dirname(path), { recursive: true });
    await writeFile(
      path,
      typeof bytes === 'string' ? bytes : JSON.stringify(bytes),
    );
  };
  const pkg = {
    name: '@example/hello',
    version: '1.2.3',
    description: 'Hello bundle',
    files: ['src', 'cordis.patch.yml'],
    main: 'src/index.js',
    dsh: { bundle: { patch: './cordis.patch.yml' } },
    engines: { dsh: '>=0.2.1-rc.3' },
    scripts: { prepack: 'must not run' },
  };
  const plugin = async (extra = {}) => {
    await put('package.json', { ...pkg, ...extra });
    await put('src/index.js', 'export const name = "hello";');
    await put('cordis.patch.yml', '[]\n');
  };
  const prepare = (kind = 'plugin', extra = {}) =>
    prepareDirectory({ cwd, directory: '.', kind, metadata, ...extra });
  return { cwd, put, plugin, prepare };
}

test('scoped plugin uses explicit payload, a different catalog slug and frozen bytes', async (t) => {
  const { cwd, put, plugin, prepare } = await fixture(t);
  await plugin({
    dependencies: { '@example/library': '1.0.0' },
    bundledDependencies: true,
  });
  await put('node_modules/@example/library/package.json', {
    name: '@example/library',
    version: '1.0.0',
    files: ['index.js'],
  });
  await put('node_modules/@example/library/index.js', 'module.exports = 1;');
  await put('node_modules/not-bundled/package.json', {});
  await put('src/.env.local', 'excluded-sample');
  await put('src/tsconfig.json', 'excluded-sample');
  await put('src/credentials.json', 'excluded-sample');
  await put('unlisted.txt', 'excluded-sample');
  const { manifest, archive } = await prepare('plugin', {
    metadata: {
      ...metadata,
      name: 'catalog-name',
      tags: ['operations', 'network'],
    },
  });
  const tar = gunzipSync(archive).toString();
  assert.equal(manifest.name, 'catalog-name');
  assert.equal(manifest.plugin.packageName, '@example/hello');
  assert.deepEqual(manifest.plugin.tags, ['operations', 'network']);
  assert.equal(
    manifest.artifacts[0].digest,
    'sha256:' + createHash('sha256').update(archive).digest('hex'),
  );
  assert.match(tar, /package\/node_modules\/@example\/library\/index.js/);
  assert.doesNotMatch(tar, /not-bundled|excluded-sample|unlisted|prepack/);
  assert.match(await readFile(join(cwd, 'package.json'), 'utf8'), /prepack/);
  assert.deepEqual(
    (
      await prepare('plugin', {
        metadata: { ...metadata, name: 'catalog-name' },
      })
    ).archive,
    archive,
  );
});

test('rejects bad patches, native declarations and missing physical bundled dependencies', async (t) => {
  const { plugin, put, prepare } = await fixture(t);
  await plugin({ dsh: { bundle: { patch: '../cordis.patch.yml' } } });
  await assert.rejects(prepare(), /package.json.*patch/);
  await plugin({
    files: ['src'],
    dsh: { bundle: { patch: 'other.patch.yml' } },
  });
  await assert.rejects(prepare(), /other.patch.yml/);
  await plugin({ pnpm: { dangerouslyAllowAllBuilds: false } });
  await assert.rejects(prepare(), /package.json.*dangerouslyAllowAllBuilds/);
  await plugin({
    dependencies: { absent: '1.0.0' },
    bundledDependencies: ['absent'],
  });
  await assert.rejects(prepare(), /node_modules\/absent/);
  await put('node_modules/absent/package.json', {
    scripts: { install: 'must not run' },
  });
  await assert.rejects(
    prepare(),
    /node_modules\/absent\/package.json.*scripts/,
  );
  await plugin();
  await put('src/Binding.Gyp', '{}');
  await assert.rejects(prepare(), /src\/Binding.Gyp/);
});

test('skill validates metadata and preserves original text bytes', async (t) => {
  const { put, prepare } = await fixture(t);
  const skill =
    '---\r\nname: hello\r\ndescription: Greeting\r\n---\r\nHello\r\n';
  await put('SKILL.md', skill);
  await put('references/readme.txt', '﻿hello\r\n');
  await put('references/secret.txt', 'excluded-sample');
  await put('references/run.sh', 'excluded-sample');
  const { manifest, archive } = await prepare('skill', {
    metadata: { ...metadata, version: '1.0.0' },
  });
  const files = unpackSkill(archive, manifest);
  assert.equal(manifest.name, 'hello');
  assert.equal(files['SKILL.md'].toString(), skill);
  assert.equal(files['references/readme.txt'].toString(), '﻿hello\r\n');
  assert.deepEqual(Object.keys(files).sort(), [
    'SKILL.md',
    'references/readme.txt',
  ]);
  await assert.rejects(
    prepare('skill', {
      metadata: { ...metadata, version: '1.0.0', name: 'wrong' },
    }),
    /SKILL.md/,
  );
});

test('rejects traversal, symlinked directories and hard-linked selected files', async (t) => {
  const { cwd, plugin, prepare } = await fixture(t);
  await plugin();
  for (const directory of [
    '../outside',
    'test/../src',
    '\\outside',
    'C:src',
    cwd,
  ]) {
    await assert.rejects(prepare('plugin', { directory }), {
      code: 'common.validation_failed',
    });
  }
  await symlink(join(cwd, 'src'), join(cwd, 'linked'), 'junction');
  await assert.rejects(
    prepare('plugin', { directory: 'linked' }),
    /linked.*link/,
  );
  await link(join(cwd, 'cordis.patch.yml'), join(cwd, 'other.yml'));
  await assert.rejects(prepare(), /cordis.patch.yml.*link/);
});

test('plugin release patterns stay inside an explicit directory', async (t) => {
  const { plugin, put, prepare } = await fixture(t);
  await plugin({
    files: ['src/index.js', 'locale/*.json', 'cordis.patch.yml'],
  });
  await put('locale/strings.json', '{"hello":"world"}');
  await put('locale/config.local.json', '{"sample":"excluded"}');
  await put('locale/notes.txt', 'excluded');
  const unpacked = gunzipSync((await prepare()).archive).toString();
  assert.match(unpacked, /locale\/strings.json/);
  assert.doesNotMatch(unpacked, /locale\/config.local.json|locale\/notes.txt/);
  await plugin({ files: ['**/*.json', 'cordis.patch.yml'] });
  await assert.rejects(prepare(), /package.json.*explicit directory/);
});

test('source preflight rejects oversized skill files before packaging', async (t) => {
  const { put, prepare } = await fixture(t);
  await put('SKILL.md', '---\nname: hello\ndescription: Greeting\n---\n');
  await put('assets/large.txt', 'a'.repeat(2097153));
  await assert.rejects(
    prepare('skill', { metadata: { ...metadata, version: '1.0.0' } }),
    /assets\/large.txt.*size limit/,
  );
});

test('submission outbound hosts admit DNS labels and unambiguous bare IP literals', async (t) => {
  const { plugin, prepare } = await fixture(t);
  await plugin();
  const options = (outboundHosts) => ({
    metadata: {
      ...metadata,
      permissions: { ...metadata.permissions, outboundHosts },
    },
  });
  const hosts = ['example.com', '192.0.2.10', '2001:db8::5', '::1'];
  assert.deepEqual(
    (await prepare('plugin', options(hosts))).manifest.permissions
      .outboundHosts,
    hosts,
  );
  for (const malformed of [
    '127.1',
    '192.000.2.10',
    '192.0.2.256',
    '2001:::5',
    '[2001:db8::5]:80',
    'fe80::1%eth0',
    'https://example.com',
    'Example.com',
  ]) {
    await assert.rejects(
      prepare('plugin', options([malformed])),
      /outboundHosts/,
    );
  }
});
