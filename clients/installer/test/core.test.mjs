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
import { join } from 'node:path';
import {
  install,
  profileDir,
  readState,
  rollback,
  uninstall,
} from '../runtime/core.mjs';

function makePayload(base, id, pluginVersion) {
  const dir = join(base, `payload-${id}`);
  const modules = join(dir, 'auriga', 'plugin', 'node_modules');
  mkdirSync(join(modules, 'auriga-dsh'), { recursive: true });
  mkdirSync(join(modules, '@nats-io', 'nats-core'), { recursive: true });
  writeFileSync(
    join(modules, 'auriga-dsh', 'package.json'),
    JSON.stringify({ name: 'auriga-dsh', version: pluginVersion }),
  );
  writeFileSync(join(modules, '@nats-io', 'nats-core', 'package.json'), '{}');
  mkdirSync(join(dir, 'auriga', 'launcher'), { recursive: true });
  writeFileSync(join(dir, 'auriga', 'launcher', 'auriga-dsh.cmd'), `rem ${id}`);
  writeFileSync(join(dir, 'manifest.json'), JSON.stringify({ id }));
  return dir;
}

const noSmoke = () => {};

function fixture(t) {
  const base = mkdtempSync(join(tmpdir(), 'auriga-installer-'));
  t.after(() => rmSync(base, { recursive: true, force: true }));
  return { base, root: join(base, 'app'), dataHome: join(base, 'home') };
}

function pluginVersion(dataHome) {
  const file = join(profileDir(dataHome), 'node_modules', 'auriga-dsh', 'package.json');
  return JSON.parse(readFileSync(file, 'utf8')).version;
}

function put(f, id, version, extra = {}) {
  return install({
    payload: makePayload(f.base, id, version),
    root: f.root,
    dataHome: f.dataHome,
    smoke: noSmoke,
    ...extra,
  });
}

test('fresh install seeds profile, plugin and platform address', (t) => {
  const f = fixture(t);
  const result = put(f, 'a', '1.0.0', {
    platformOrigin: 'https://portal.example.com/ignored/path',
  });
  assert.deepEqual(result, { id: 'a', previous: null, upgraded: false });
  const profile = profileDir(f.dataHome);
  const manifest = JSON.parse(readFileSync(join(profile, 'package.json'), 'utf8'));
  assert.ok(manifest.dsh.profile.bundles.includes('auriga-dsh'));
  assert.ok(existsSync(join(profile, 'node_modules', '@nats-io', 'nats-core')));
  const patch = readFileSync(join(profile, 'cordis.patch.yml'), 'utf8');
  assert.match(patch, /id: auriga-dsh/);
  assert.match(patch, /"platformOrigin":"https:\/\/portal\.example\.com"/);
  assert.equal(readFileSync(join(f.root, 'current.txt'), 'utf8').trim(), 'a');
  assert.ok(existsSync(join(f.root, 'bin', 'auriga-dsh.cmd')));
});

test('upgrade keeps user data and settings, keeps previous version', (t) => {
  const f = fixture(t);
  put(f, 'a', '1.0.0', { platformOrigin: 'https://portal.example.com' });
  mkdirSync(join(f.dataHome, 'sessions'), { recursive: true });
  writeFileSync(join(f.dataHome, 'sessions', 's1.json'), 'user-session');
  const patchFile = join(profileDir(f.dataHome), 'cordis.patch.yml');
  writeFileSync(patchFile, readFileSync(patchFile, 'utf8') + '# user edit\n');

  const result = put(f, 'b', '1.1.0');
  assert.deepEqual(result, { id: 'b', previous: 'a', upgraded: true });
  assert.equal(readFileSync(join(f.dataHome, 'sessions', 's1.json'), 'utf8'), 'user-session');
  assert.match(readFileSync(patchFile, 'utf8'), /# user edit/);
  assert.equal(pluginVersion(f.dataHome), '1.1.0');
  assert.deepEqual(readState(f.root), { dataHome: f.dataHome, current: 'b', previous: 'a' });
  assert.ok(existsSync(join(f.root, 'versions', 'a')));
});

test('failed smoke check during upgrade rolls everything back', (t) => {
  const f = fixture(t);
  put(f, 'a', '1.0.0');
  assert.throws(
    () =>
      put(f, 'b', '1.1.0', {
        smoke: () => {
          throw new Error('boom');
        },
      }),
    /boom/,
  );
  assert.equal(readState(f.root).current, 'a');
  assert.equal(existsSync(join(f.root, 'versions', 'b')), false);
  assert.equal(pluginVersion(f.dataHome), '1.0.0');
});

test('rollback restores previous version and plugin files', (t) => {
  const f = fixture(t);
  put(f, 'a', '1.0.0');
  put(f, 'b', '1.1.0');
  assert.deepEqual(rollback({ root: f.root, smoke: noSmoke }), { current: 'a' });
  assert.equal(readFileSync(join(f.root, 'current.txt'), 'utf8').trim(), 'a');
  assert.equal(pluginVersion(f.dataHome), '1.0.0');
  assert.deepEqual(readState(f.root), { dataHome: f.dataHome, current: 'a', previous: 'b' });
  assert.match(readFileSync(join(f.root, 'bin', 'auriga-dsh.cmd'), 'utf8'), /rem a/);
});

test('rollback without previous version fails', (t) => {
  const f = fixture(t);
  put(f, 'a', '1.0.0');
  assert.throws(() => rollback({ root: f.root, smoke: noSmoke }), /no previous/);
});

test('uninstall removes the root, keeps data unless purged', (t) => {
  const f = fixture(t);
  put(f, 'a', '1.0.0');
  writeFileSync(join(f.dataHome, 'keep.txt'), 'x');
  assert.deepEqual(uninstall({ root: f.root }), { dataKept: true, dataHome: f.dataHome });
  assert.equal(existsSync(f.root), false);
  assert.ok(existsSync(join(profileDir(f.dataHome), 'package.json')));
  put(f, 'a', '1.0.0');
  uninstall({ root: f.root, purgeData: true });
  assert.equal(existsSync(profileDir(f.dataHome)), false);
  assert.ok(existsSync(join(f.dataHome, 'keep.txt')));
});

test('uninstall refuses a directory that is not an installation', (t) => {
  const f = fixture(t);
  mkdirSync(f.root, { recursive: true });
  assert.throws(() => uninstall({ root: f.root }), /not an installation/);
  assert.ok(existsSync(f.root));
});
