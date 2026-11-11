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
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { satisfiesMin } from '../scripts/lib/semver-lite.mjs';
import { findSecrets, listFiles, sha256File } from '../scripts/lib/files.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const installer = resolve(here, '..');
const readJson = (file) => JSON.parse(readFileSync(file, 'utf8'));

test('upstream.json pins an exact dsh version the plugin accepts', () => {
  const upstream = readJson(join(installer, 'upstream.json'));
  const plugin = readJson(join(installer, '..', 'dsh-plugin', 'package.json'));
  assert.match(upstream.dsh, /^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/);
  assert.match(upstream.node, /^\d+\.\d+\.\d+$/);
  assert.equal(satisfiesMin(upstream.dsh, plugin.engines.dsh), true);
});

test('committed upstream lock resolves exactly the pinned dsh with integrity', () => {
  const upstream = readJson(join(installer, 'upstream.json'));
  const file = join(installer, 'locks', `dsh-${upstream.dsh}.package-lock.json`);
  assert.ok(existsSync(file), 'lock for the default upstream must be committed');
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
    writeFileSync(join(dir, 'ok.txt'), 'platformOrigin https://portal.example.com');
    assert.deepEqual(findSecrets(dir), []);
    writeFileSync(join(dir, 'a.txt'), '-----BEGIN PRIVATE KEY-----\nabc');
    writeFileSync(join(dir, 'b.txt'), 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123456789');
    writeFileSync(join(dir, 'c.txt'), 'AURIGA_INSTANCE_TOKEN=abcdefgh12345678');
    writeFileSync(join(dir, '.env'), 'X=1');
    const hits = findSecrets(dir).join('\n');
    for (const name of ['a.txt', 'b.txt', 'c.txt', '.env']) assert.match(hits, new RegExp(name.replace('.', '\\.')));
    mkdirSync(join(dir, 'node_modules'));
    writeFileSync(join(dir, 'node_modules', 'x.key'), 'x');
    assert.equal(findSecrets(dir, (p) => p.includes('/node_modules/')).some((h) => h.startsWith('node_modules')), false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

// Built package checks run when INSTALLER_STAGE (a built package directory) is set.
const stage = process.env.INSTALLER_STAGE;
test('built package: manifest, checksums, version lock and contents', { skip: !stage }, () => {
  const payload = join(stage, 'payload');
  const manifest = readJson(join(payload, 'manifest.json'));
  assert.equal(manifest.id, `${manifest.plugin.version}-dsh${manifest.upstream.version}`);
  assert.equal(manifest.target, 'win-x64');
  for (const required of [
    'upstream/node/node.exe',
    'auriga/plugin/auriga-dsh.tgz',
    'auriga/plugin/node_modules/auriga-dsh/package.json',
    'auriga/plugin/node_modules/auriga-dsh/ui/auriga-team-panel.js',
    'auriga/plugin/node_modules/auriga-dsh/cordis.patch.yml',
    'auriga/runtime/core.mjs',
    'auriga/runtime/cli.mjs',
    'auriga/launcher/auriga-dsh.cmd',
    'auriga/launcher/uninstall.ps1',
    'upstream/app/package-lock.json',
  ]) {
    assert.ok(existsSync(join(payload, required)), `missing ${required}`);
  }
  for (const file of ['install.cmd', 'install.ps1', 'README.txt'])
    assert.ok(existsSync(join(stage, file)), `missing ${file}`);

  // Every listed file matches its checksum; nothing of ours is unlisted.
  for (const f of manifest.files) assert.equal(sha256File(join(payload, f.path)), f.sha256, f.path);
  const listed = new Set(manifest.files.map((f) => f.path));
  const unlisted = listFiles(payload).filter(
    (p) =>
      p !== 'manifest.json' &&
      !p.startsWith('upstream/app/node_modules/') &&
      !p.startsWith('auriga/plugin/node_modules/') &&
      !listed.has(p),
  );
  assert.deepEqual(unlisted, []);

  // Version lock: installed upstream equals the declared version and lock integrity.
  const dsh = readJson(join(payload, 'upstream/app/node_modules/@deepseek-ai/dsh/package.json'));
  assert.equal(dsh.version, manifest.upstream.version);
  const lock = readJson(join(payload, 'upstream/app/package-lock.json'));
  assert.equal(lock.packages['node_modules/@deepseek-ai/dsh'].integrity, manifest.upstream.integrity);
  assert.equal(sha256File(join(payload, 'upstream/app/package-lock.json')), manifest.upstream.lockSha256);
  const plugin = readJson(join(payload, 'auriga/plugin/node_modules/auriga-dsh/package.json'));
  assert.equal(plugin.version, manifest.plugin.version);
  assert.equal(satisfiesMin(manifest.upstream.version, plugin.engines.dsh), true);
  assert.equal(sha256File(join(payload, 'auriga/plugin/auriga-dsh.tgz')), manifest.plugin.tgzSha256);
});

test('built package: no secrets in our files', { skip: !stage }, () => {
  const hits = findSecrets(stage, (p) => p.includes('/node_modules/'));
  assert.deepEqual(hits, []);
});
