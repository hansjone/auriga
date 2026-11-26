import test from 'node:test';
import assert from 'node:assert/strict';
import {
  chmodSync,
  existsSync,
  mkdirSync,
  readFileSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { ensureDataHomeWritable } from '../runtime/data-home-access.mjs';
import { install, profileDir, uninstall } from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

const noSmoke = () => {};

test('ensureDataHomeWritable clears read-only cordis.yml left from a kept profile', (t) => {
  const base = tempBase(t);
  const dataHome = join(base, 'home');
  const profile = join(dataHome, 'profiles', 'auriga');
  mkdirSync(profile, { recursive: true });
  const cordis = join(profile, 'cordis.yml');
  writeFileSync(cordis, '[]\n');
  chmodSync(cordis, 0o444);
  assert.equal(ensureDataHomeWritable(dataHome).repaired, true);
  writeFileSync(cordis, '# rewritten by dump-config\n');
  assert.equal(readFileSync(cordis, 'utf8'), '# rewritten by dump-config\n');
});

test('ensureDataHomeWritable is a no-op when data home is absent', (t) => {
  const base = tempBase(t);
  assert.deepEqual(ensureDataHomeWritable(join(base, 'missing-home')), {
    repaired: false,
  });
});

test('ensureDataHomeWritable does not seed profile dirs on an empty data home', (t) => {
  const base = tempBase(t);
  const dataHome = join(base, 'home');
  mkdirSync(dataHome);
  assert.equal(ensureDataHomeWritable(dataHome).repaired, true);
  assert.equal(existsSync(join(dataHome, 'profiles')), false);
});

test('ensureDataHomeWritable refuses a profile junction before mutating the outside target', (t) => {
  const base = tempBase(t);
  const dataHome = join(base, 'home');
  const outside = join(base, 'outside-profile');
  const profiles = join(dataHome, 'profiles');
  mkdirSync(profiles, { recursive: true });
  mkdirSync(outside);
  const outsideCordis = join(outside, 'cordis.yml');
  writeFileSync(outsideCordis, '[]\n');
  chmodSync(outsideCordis, 0o444);
  const beforeMode = statSync(outsideCordis).mode & 0o222;
  assert.equal(beforeMode, 0);
  const linkType =
    process.platform === 'win32' ? 'junction' : 'dir';
  symlinkSync(outside, join(profiles, 'auriga'), linkType);
  assert.throws(
    () => ensureDataHomeWritable(dataHome),
    /guardian path contains a link|guardian path alias/,
  );
  assert.equal(statSync(outsideCordis).mode & 0o222, 0);
  assert.equal(readFileSync(outsideCordis, 'utf8'), '[]\n');
});

test('keep-data uninstall then reinstall reuses profile despite read-only cordis.yml', (t) => {
  const base = tempBase(t);
  const root = join(base, 'app');
  const dataHome = join(base, 'home');
  const first = buildPayload(base, { name: 'a' });
  install({
    payload: first.dir,
    root,
    dataHome,
    smoke: noSmoke,
  });
  const session = join(dataHome, 'sessions', 'keep.json');
  mkdirSync(join(dataHome, 'sessions'), { recursive: true });
  writeFileSync(session, 'keep-me');
  const cordis = join(profileDir(dataHome), 'cordis.yml');
  writeFileSync(cordis, '[]\n');
  chmodSync(cordis, 0o444);

  assert.equal(uninstall({ root }).dataKept, true);
  assert.ok(existsSync(dataHome));
  assert.ok(existsSync(cordis));

  const second = buildPayload(base, { name: 'b', pluginVersion: '1.1.0' });
  let smoked = false;
  install({
    payload: second.dir,
    root,
    dataHome,
    smoke(dir, home) {
      smoked = true;
      // Mirror upstream dump-config: always rewrite cordis.yml.
      writeFileSync(join(profileDir(home), 'cordis.yml'), '# smoked\n');
      assert.ok(existsSync(join(dir, 'manifest.json')));
    },
  });
  assert.equal(smoked, true);
  assert.equal(readFileSync(session, 'utf8'), 'keep-me');
  assert.equal(readFileSync(cordis, 'utf8'), '# smoked\n');
});
