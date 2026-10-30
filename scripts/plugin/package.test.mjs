import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  mkdtemp,
  mkdir,
  readFile,
  writeFile,
  rm,
  utimes,
} from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import { packagePlugin } from './package.mjs';
import { installLocalAdmission } from '../../clients/dsh-plugin/src/local/host.js';

test('missing host admission tells users the exact minimum DSH version', () => {
  assert.throws(
    () => installLocalAdmission({}, {}, { protocol: 'http:' }),
    /需要 DSH 0\.2\.1-rc\.3.*升级 DSH.*upgrade DSH/,
  );
});

test('package contains standalone runtime, version and matching digest; rebuild is identical', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-dist-'));
  try {
    const plugin = join(root, 'clients/dsh-plugin');
    for (const dir of ['src', 'ui', 'test', 'scripts'])
      await mkdir(join(plugin, dir), { recursive: true });
    const manifest = JSON.parse(
      await readFile(
        new URL('../../clients/dsh-plugin/package.json', import.meta.url),
      ),
    );
    await writeFile(join(plugin, 'package.json'), JSON.stringify(manifest));
    for (const file of [
      'src/plugin.js',
      'src/client.js',
      'LICENSE',
      'README.md',
      'cordis.patch.yml',
      'ui/auriga-team-panel.js',
      'ui/metadata.json',
      'test/private.js',
      'scripts/private.js',
    ])
      await writeFile(join(plugin, file), file);
    const original = await readFile(join(plugin, 'package.json'), 'utf8');
    const first = await packagePlugin({
      root,
      version: '1.2.3-rc.1',
      outDir: join(root, 'one'),
      build: false,
    });
    const bytes = await readFile(first.tarball);
    assert.equal(
      await readFile(first.checksum, 'utf8'),
      `${createHash('sha256').update(bytes).digest('hex')}  auriga-dsh-1.2.3-rc.1.tgz\n`,
    );
    const receipt = JSON.parse(await readFile(first.manifest));
    assert.equal(receipt.version, '1.2.3-rc.1');
    assert.equal(
      receipt.sha256,
      createHash('sha256').update(bytes).digest('hex'),
    );
    const extraction = join(root, 'extracted');
    await mkdir(extraction);
    const unpack = spawnSync('tar', ['-xzf', first.tarball, '-C', extraction], {
      encoding: 'utf8',
      windowsHide: true,
    });
    assert.equal(unpack.status, 0, unpack.stderr);
    const packed = JSON.parse(
      await readFile(join(extraction, 'package/package.json')),
    );
    assert.equal(packed.version, '1.2.3-rc.1');
    assert.equal(packed.engines.dsh, '>=0.2.1-rc.3');
    assert.equal(packed.dependencies.ws, manifest.dependencies.ws);
    assert.equal(packed.scripts, undefined);
    assert.equal(packed.devDependencies, undefined);
    for (const file of [
      'src/plugin.js',
      'src/client.js',
      'cordis.patch.yml',
      'ui/auriga-team-panel.js',
      'ui/metadata.json',
    ])
      assert.ok(receipt.files.includes(file), file);
    assert.ok(
      !receipt.files.some((file) =>
        /^(test|scripts|node_modules)\//.test(file),
      ),
    );
    await utimes(join(plugin, 'src/plugin.js'), new Date(), new Date());
    const second = await packagePlugin({
      root,
      version: '1.2.3-rc.1',
      outDir: join(root, 'two'),
      build: false,
    });
    assert.deepEqual(await readFile(second.tarball), bytes);
    assert.equal(
      await readFile(join(plugin, 'package.json'), 'utf8'),
      original,
    );
    await assert.rejects(
      packagePlugin({
        root,
        version: '1.2.3-rc.1',
        outDir: join(root, 'one'),
        build: false,
      }),
      /already exists/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('rejects invalid versions before building or writing', async () => {
  for (const version of ['', '../evil', 'v1.2.3', '01.2.3', '1.2', '1.2.3-01'])
    await assert.rejects(
      packagePlugin({
        version,
        root: resolve('absent'),
        outDir: resolve('absent'),
      }),
      /SemVer/,
    );
});

test('missing prepared UI fails without publishing partial assets', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-dist-'));
  try {
    await mkdir(join(root, 'clients/dsh-plugin'), { recursive: true });
    await writeFile(
      join(root, 'clients/dsh-plugin/package.json'),
      await readFile(
        new URL('../../clients/dsh-plugin/package.json', import.meta.url),
      ),
    );
    await assert.rejects(
      packagePlugin({
        root,
        version: '1.0.0',
        outDir: join(root, 'out'),
        build: false,
      }),
      /ENOENT/,
    );
    await assert.rejects(
      readFile(join(root, 'out/auriga-dsh-1.0.0.tgz')),
      /ENOENT/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('failed UI build propagates its exit code and publishes nothing', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-dist-'));
  try {
    const plugin = join(root, 'clients/dsh-plugin');
    await mkdir(join(plugin, 'scripts'), { recursive: true });
    await writeFile(
      join(plugin, 'package.json'),
      await readFile(
        new URL('../../clients/dsh-plugin/package.json', import.meta.url),
      ),
    );
    await writeFile(
      join(plugin, 'scripts/package-ui.mjs'),
      'process.exit(23);',
    );
    await assert.rejects(
      packagePlugin({ root, version: '1.0.0', outDir: join(root, 'out') }),
      /exit 23/,
    );
    await assert.rejects(
      readFile(join(root, 'out/auriga-dsh-1.0.0.tgz')),
      /ENOENT/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
