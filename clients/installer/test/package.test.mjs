import test from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { satisfiesMin } from '../runtime/semver-lite.mjs';
import { computeId, isPinnedPath, verifyPackage } from '../runtime/verify.mjs';
import { scanForSecrets } from '../scripts/build.mjs';
import { spawnSync } from 'node:child_process';
import { findSecrets, listFiles, sha256File } from '../scripts/lib/files.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const installer = resolve(here, '..');
const readJson = (file) => JSON.parse(readFileSync(file, 'utf8'));

function assertPackageId(manifest) {
  assert.match(
    manifest.id,
    new RegExp(
      `^${manifest.bundleVersion}-dsh${manifest.upstream.version}-[0-9a-f]{12}$`.replace(
        /\./g,
        '\\.',
      ),
    ),
  );
}

test('package ID uses the independent Desktop version, not the plugin version', (t) => {
  const { manifest } = buildPayload(tempBase(t), { pluginVersion: '0.1.15' });
  manifest.bundleVersion = '0.1.0';
  manifest.id = computeId(manifest);
  assertPackageId(manifest);
  assert.throws(() =>
    assertPackageId({
      ...manifest,
      id: manifest.id.replace(/^0\.1\.0/, '0.1.15'),
    }),
  );
});

test('upstream.json pins an exact dsh version the plugin accepts', () => {
  const upstream = readJson(join(installer, 'upstream.json'));
  const plugin = readJson(join(installer, '..', 'dsh-plugin', 'package.json'));
  assert.match(upstream.dsh, /^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/);
  assert.match(upstream.node, /^\d+\.\d+\.\d+$/);
  assert.equal(satisfiesMin(upstream.dsh, plugin.engines.dsh), true);
});

test('committed upstream lock resolves exactly the pinned dsh with integrity', () => {
  const upstream = readJson(join(installer, 'upstream.json'));
  const file = join(
    installer,
    'locks',
    `dsh-${upstream.dsh}.package-lock.json`,
  );
  assert.ok(
    existsSync(file),
    'lock for the default upstream must be committed',
  );
  const lock = readJson(file);
  const entry = lock.packages['node_modules/@deepseek-ai/dsh'];
  assert.equal(entry.version, upstream.dsh);
  assert.match(entry.integrity, /^sha512-/);
  assert.equal(upstream.dshIntegrity, entry.integrity);
  for (const [path, pkg] of Object.entries(lock.packages)) {
    if (path === '') continue;
    assert.ok(pkg.integrity, `${path} has no integrity`);
    assert.match(pkg.resolved, /^https:\/\/registry\.npmjs\.org\//, path);
  }
});

test('secret scanner flags keys, tokens and credential files', () => {
  const dir = mkdtempSync(join(tmpdir(), 'auriga-secrets-'));
  try {
    writeFileSync(
      join(dir, 'ok.txt'),
      'platformOrigin https://portal.example.com',
    );
    assert.deepEqual(findSecrets(dir), []);
    // Synthetic samples are assembled at run time so the source holds no
    // secret-shaped literal.
    const dashes = '-'.repeat(5);
    writeFileSync(
      join(dir, 'a.txt'),
      `${dashes}BEGIN ${'PRIVATE'} KEY${dashes}\nabc`,
    );
    writeFileSync(
      join(dir, 'b.txt'),
      ['Authorization:', 'Bearer', 'x'.repeat(40)].join(' '),
    );
    writeFileSync(
      join(dir, 'c.txt'),
      ['AURIGA', 'INSTANCE', 'TOKEN'].join('_') + '=' + 'y'.repeat(16),
    );
    writeFileSync(join(dir, '.env'), 'X=1');
    const hits = findSecrets(dir).join('\n');
    for (const name of ['a.txt', 'b.txt', 'c.txt', '.env'])
      assert.match(hits, new RegExp(name.replace('.', '\\.')));
    mkdirSync(join(dir, 'node_modules'));
    writeFileSync(join(dir, 'node_modules', 'x.key'), 'x');
    assert.equal(
      findSecrets(dir, (p) => p.includes('/node_modules/')).some((h) =>
        h.startsWith('node_modules'),
      ),
      false,
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

// Built package checks run when INSTALLER_STAGE (a built package directory) is set.
const stage = process.env.INSTALLER_STAGE;
test(
  'built package: manifest, checksums, version lock and contents',
  { skip: !stage },
  () => {
    const payload = join(stage, 'payload');
    const manifest = readJson(join(payload, 'manifest.json'));
    assert.equal(manifest.id, computeId(manifest));
    assertPackageId(manifest);
    // The install-time verification accepts the built package as is.
    verifyPackage(payload);
    assert.equal(manifest.target, 'win-x64');
    for (const required of [
      'upstream/node/node.exe',
      'auriga/plugin/auriga-dsh.tgz',
      'auriga/plugin/node_modules/auriga-dsh/package.json',
      'auriga/plugin/node_modules/auriga-dsh/ui/auriga-team-panel.js',
      'auriga/plugin/node_modules/auriga-dsh/cordis.patch.yml',
      'auriga/runtime/core.mjs',
      'auriga/runtime/cli.mjs',
      'auriga/launcher/auriga-desktop.cmd',
      'auriga/launcher/uninstall.ps1',
      'upstream/app/package-lock.json',
    ]) {
      assert.ok(existsSync(join(payload, required)), `missing ${required}`);
    }
    for (const file of ['install.cmd', 'install.ps1', 'README.txt'])
      assert.ok(existsSync(join(stage, file)), `missing ${file}`);

    // Every listed file matches its checksum; nothing of ours is unlisted.
    for (const f of manifest.files)
      assert.equal(sha256File(join(payload, f.path)), f.sha256, f.path);
    const listed = new Set(manifest.files.map((f) => f.path));
    const unlisted = listFiles(payload).filter(
      (p) => p !== 'manifest.json' && !isPinnedPath(p) && !listed.has(p),
    );
    assert.deepEqual(unlisted, []);

    // Version lock: installed upstream equals the declared version and lock integrity.
    const dsh = readJson(
      join(payload, 'upstream/app/node_modules/@deepseek-ai/dsh/package.json'),
    );
    assert.equal(dsh.version, manifest.upstream.version);
    const lock = readJson(join(payload, 'upstream/app/package-lock.json'));
    assert.equal(
      lock.packages['node_modules/@deepseek-ai/dsh'].integrity,
      manifest.upstream.integrity,
    );
    assert.equal(
      sha256File(join(payload, 'upstream/app/package-lock.json')),
      manifest.upstream.lockSha256,
    );
    const plugin = readJson(
      join(payload, 'auriga/plugin/node_modules/auriga-dsh/package.json'),
    );
    assert.equal(plugin.version, manifest.plugin.version);
    assert.equal(
      satisfiesMin(manifest.upstream.version, plugin.engines.dsh),
      true,
    );
    assert.equal(
      sha256File(join(payload, 'auriga/plugin/auriga-dsh.tgz')),
      manifest.plugin.tgzSha256,
    );
  },
);

test(
  'built package: product name, licences, pnpm and trimming',
  { skip: !stage },
  () => {
    const payload = join(stage, 'payload');
    const manifest = readJson(join(payload, 'manifest.json'));
    assert.equal(manifest.product, 'Auriga Desktop');
    assert.match(basename(stage), /^auriga-desktop-.+-win-x64$/);
    for (const f of [
      'LICENSE.dsh.txt',
      'LICENSE.node.txt',
      'LICENSE.auriga-dsh.txt',
      'NOTICE.txt',
    ])
      assert.ok(existsSync(join(payload, 'licenses', f)), f);
    assert.match(
      readFileSync(join(payload, 'licenses', 'LICENSE.dsh.txt'), 'utf8'),
      /MIT License/,
    );
    const notice = readFileSync(
      join(payload, 'licenses', 'NOTICE.txt'),
      'utf8',
    );
    assert.match(notice, /not an official DeepSeek product/);
    assert.equal(notice.includes('DeepSeek Harness'), false);
    assert.equal(
      manifest.pnpm.version,
      readJson(join(installer, 'upstream.json')).pnpm,
    );
    assert.match(manifest.pnpm.integrity, /^sha512-/);
    assert.ok(
      existsSync(
        join(
          payload,
          'tools',
          'pnpm',
          'node_modules',
          'pnpm',
          'bin',
          'pnpm.cjs',
        ),
      ),
    );
    assert.ok(existsSync(join(payload, 'tools', 'bin', 'pnpm.cmd')));
    assert.ok(
      manifest.size.afterTrim < manifest.size.upstreamAndPluginBeforeTrim,
    );
    const stray = listFiles(
      join(payload, 'upstream', 'app', 'node_modules'),
    ).filter((f) => /\.(map|d\.ts)$/.test(f));
    assert.deepEqual(stray, []);
  },
);

test('built package: no secrets in our files', { skip: !stage }, () => {
  const hits = findSecrets(stage, (p) => p.includes('/node_modules/'));
  assert.deepEqual(hits, []);
});

// ---------------------------------------------------------- scan policy

const token = (n) => ['Bearer', 'z'.repeat(n)].join(' ');

test('secret scan does not skip large files of ours', () => {
  const dir = mkdtempSync(join(tmpdir(), 'auriga-scan-'));
  try {
    writeFileSync(
      join(dir, 'big.js'),
      'a'.repeat(3 * 1024 * 1024) + ' ' + token(40),
    );
    assert.match(findSecrets(dir).join(' | '), /big\.js: matches/);
    const tooBig = findSecrets(dir, () => false, 1024).join(' | ');
    assert.match(tooBig, /too large to scan/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('build scan covers our plugin files and the expanded plugin tarball', () => {
  const base = mkdtempSync(join(tmpdir(), 'auriga-scan-'));
  try {
    const stage = join(base, 'stage');
    const plugin = join(stage, 'payload/auriga/plugin/node_modules');
    mkdirSync(join(plugin, 'auriga-dsh/src'), { recursive: true });
    mkdirSync(join(plugin, 'ws'), { recursive: true });
    // A third-party dependency may contain anything: pinned elsewhere.
    writeFileSync(join(plugin, 'ws/sample.js'), token(40));
    writeFileSync(join(plugin, 'auriga-dsh/src/ok.js'), 'export {}');
    const pack = join(base, 'pack');
    mkdirSync(join(pack, 'package'), { recursive: true });
    writeFileSync(join(pack, 'package/index.js'), 'export {}');
    const tgz = join(base, 'plugin.tgz');
    const tar =
      process.platform === 'win32'
        ? join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'tar.exe')
        : 'tar';
    const made = spawnSync(tar, ['-czf', tgz, '-C', pack, 'package']);
    assert.equal(made.status, 0, 'tar available');
    const work = join(base, 'work');
    mkdirSync(work);
    scanForSecrets(stage, tgz, work);

    // Secret inside our plugin directory: rejected.
    writeFileSync(join(plugin, 'auriga-dsh/src/leak.js'), token(40));
    assert.throws(() => scanForSecrets(stage, tgz, work), /leak\.js/);
    rmSync(join(plugin, 'auriga-dsh/src/leak.js'));

    // Secret only inside the tarball: rejected.
    writeFileSync(join(pack, 'package/leak.js'), token(40));
    spawnSync(tar, ['-czf', tgz, '-C', pack, 'package']);
    assert.throws(() => scanForSecrets(stage, tgz, work), /leak\.js/);
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});
