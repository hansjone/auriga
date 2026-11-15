import { releaseVersion } from './release-version.mjs';
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
    for (const dir of ['src', 'ui', 'test', 'scripts', 'skills'])
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
      'skills/agent-auth.js',
      'test/private.js',
      'scripts/private.js',
    ])
      await writeFile(join(plugin, file), file);
    const brokerArtifact = join(root, 'broker');
    await mkdir(join(brokerArtifact, 'windows'), { recursive: true });
    const files = [];
    for (const path of [
      'windows/shell-helper.exe',
      'desktop.mjs',
      'broker.mjs',
    ]) {
      await writeFile(join(brokerArtifact, path), path);
      files.push({
        path,
        sha256: createHash('sha256').update(path).digest('hex'),
      });
    }
    await writeFile(
      join(brokerArtifact, 'broker-manifest.json'),
      JSON.stringify({ version: 1, files }),
    );
    const original = await readFile(join(plugin, 'package.json'), 'utf8');
    const first = await packagePlugin({
      root,
      version: '1.2.3-rc.1',
      outDir: join(root, 'one'),
      build: false,
      brokerArtifact,
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
    const unpack = spawnSync(
      process.platform === 'win32'
        ? join(process.env.SystemRoot, 'System32', 'tar.exe')
        : 'tar',
      ['-xzf', first.tarball, '-C', extraction],
      {
        encoding: 'utf8',
        windowsHide: true,
      },
    );
    assert.equal(unpack.status, 0, unpack.stderr);
    const packed = JSON.parse(
      await readFile(join(extraction, 'package/package.json')),
    );
    assert.deepEqual(
      await readFile(
        join(extraction, 'package/shell-auth/broker-manifest.json'),
      ),
      await readFile(join(brokerArtifact, 'broker-manifest.json')),
    );
    assert.deepEqual(
      await readFile(
        join(extraction, 'package/shell-auth/windows/shell-helper.exe'),
      ),
      await readFile(join(brokerArtifact, 'windows/shell-helper.exe')),
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
      'skills/agent-auth.js',
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
      brokerArtifact,
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
        brokerArtifact,
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

test('Desktop release selects real plugin version and rejects CI placeholder', () => {
  assert.equal(releaseVersion('desktop-v1.2.3', '0.4.0'), '0.4.0');
  assert.throws(
    () => releaseVersion('desktop-v1.2.3', '0.0.0-ci'),
    /placeholder/,
  );
  assert.equal(releaseVersion('plugin-v0.5.0', '0.4.0'), '0.5.0');
  assert.equal(releaseVersion('feature', '0.4.0'), '0.0.0-ci');
});

test('manual official release also rejects placeholder plugin version', () => {
  assert.equal(releaseVersion('main', '0.4.0', 'workflow_dispatch'), '0.4.0');
  assert.throws(
    () => releaseVersion('main', '0.0.0-ci', 'workflow_dispatch'),
    /placeholder/,
  );
  assert.throws(
    () => releaseVersion('plugin-v0.0.0-ci', '0.4.0'),
    /placeholder/,
  );
});
