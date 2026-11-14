import test from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join, parse } from 'node:path';
import {
  CrashError,
  install,
  launchEnv,
  nodeBin,
  profileDir,
  readState,
  recover,
  rollback,
  uninstall,
} from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

const noSmoke = () => {};

function fixture(t) {
  const base = tempBase(t);
  return { base, root: join(base, 'app'), dataHome: join(base, 'home') };
}

const text = (file) => readFileSync(file, 'utf8');
const pluginVersion = (dataHome) =>
  JSON.parse(
    text(
      join(profileDir(dataHome), 'node_modules', 'auriga-dsh', 'package.json'),
    ),
  ).version;
const launcher = (root) => text(join(root, 'bin', 'auriga-desktop.cmd'));
const profileManifest = (dataHome) =>
  text(join(profileDir(dataHome), 'package.json'));

function put(f, options, extra = {}) {
  const p = buildPayload(f.base, options);
  const result = install({
    payload: p.dir,
    root: f.root,
    dataHome: f.dataHome,
    smoke: noSmoke,
    ...extra,
  });
  return { ...result, payload: p };
}

/** What a user-visible installation looks like, for before/after comparison. */
function snapshot(f) {
  return {
    state: readState(f.root),
    currentTxt: existsSync(join(f.root, 'current.txt'))
      ? text(join(f.root, 'current.txt')).trim()
      : null,
    versions: readdirSync(join(f.root, 'versions')).sort(),
    launcher: existsSync(join(f.root, 'bin')) ? launcher(f.root) : null,
    rollbackCmd: existsSync(join(f.root, 'bin', 'rollback.cmd'))
      ? text(join(f.root, 'bin', 'rollback.cmd'))
      : null,
    txn: existsSync(join(f.root, 'txn.json')),
    profile: profileManifest(f.dataHome),
    plugin: pluginVersion(f.dataHome),
    userFile: text(join(profileDir(f.dataHome), 'user-setting.txt')),
    tarball: text(join(profileDir(f.dataHome), '.auriga', 'auriga-dsh.tgz')),
  };
}

/** Install version A and add user customisations to the profile. */
function installA(f) {
  const a = put(f, { name: 'a', pluginVersion: '1.0.0' });
  const profile = profileDir(f.dataHome);
  writeFileSync(join(profile, 'user-setting.txt'), 'keep-me');
  const manifest = JSON.parse(profileManifest(f.dataHome));
  manifest.dependencies['user-plugin'] = '^1.0.0';
  writeFileSync(join(profile, 'package.json'), JSON.stringify(manifest));
  return a;
}

test('fresh install seeds profile, plugin, ownership marker and platform address', (t) => {
  const f = fixture(t);
  const result = put(
    f,
    { name: 'a' },
    { platformOrigin: 'https://portal.example.com' },
  );
  assert.deepEqual(result, {
    id: result.payload.id,
    previous: null,
    upgraded: false,
    payload: result.payload,
  });
  const profile = profileDir(f.dataHome);
  const manifest = JSON.parse(profileManifest(f.dataHome));
  assert.ok(manifest.dsh.profile.bundles.includes('auriga-dsh'));
  assert.equal(
    manifest.dependencies['auriga-dsh'],
    'file:./.auriga/auriga-dsh.tgz',
  );
  assert.ok(existsSync(join(profile, 'node_modules', '@nats-io', 'nats-core')));
  const row = JSON.parse(
    text(join(profile, 'cordis.patch.yml')).split('config: ')[1],
  );
  assert.deepEqual(row.connect, {
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: 'discover',
  });
  assert.equal(text(join(f.root, 'current.txt')).trim(), result.id);
  assert.ok(existsSync(join(f.root, '.auriga-desktop-install')));
  assert.equal(existsSync(join(f.root, 'txn.json')), false);
});

test('upgrade keeps user data and settings and keeps the previous version', (t) => {
  const f = fixture(t);
  const a = installA(f);
  mkdirSync(join(f.dataHome, 'sessions'), { recursive: true });
  writeFileSync(join(f.dataHome, 'sessions', 's1.json'), 'user-session');
  const patchFile = join(profileDir(f.dataHome), 'cordis.patch.yml');
  writeFileSync(patchFile, text(patchFile) + '# user edit\n');
  const b = put(f, { name: 'b', pluginVersion: '1.1.0' });
  assert.equal(b.upgraded, true);
  assert.equal(b.previous, a.id);
  assert.equal(text(join(f.dataHome, 'sessions', 's1.json')), 'user-session');
  assert.match(text(patchFile), /# user edit/);
  assert.equal(pluginVersion(f.dataHome), '1.1.0');
  assert.equal(
    JSON.parse(profileManifest(f.dataHome)).dependencies['user-plugin'],
    '^1.0.0',
  );
  assert.deepEqual(readState(f.root), {
    dataHome: f.dataHome,
    current: b.id,
    previous: a.id,
  });
});

test('components with equal versions but different content get different ids and replace the old ones', (t) => {
  const f = fixture(t);
  const a = put(f, { name: 'a', nodeContent: 'node-1', coreContent: 'core-1' });
  const nodeOnly = buildPayload(f.base, {
    name: 'a2',
    nodeContent: 'node-2',
    coreContent: 'core-1',
  });
  const coreOnly = buildPayload(f.base, {
    name: 'a3',
    nodeContent: 'node-1',
    coreContent: 'core-2',
  });
  const pnpmOnly = buildPayload(f.base, { name: 'a4', pnpmVersion: '11.29.0' });
  assert.notEqual(nodeOnly.id, a.id);
  assert.notEqual(coreOnly.id, a.id);
  assert.notEqual(pnpmOnly.id, a.id);
  assert.match(a.id, /^1\.0\.0-dsh0\.2\.1-rc\.3-[0-9a-f]{12}$/);
  const upgraded = install({
    payload: nodeOnly.dir,
    root: f.root,
    dataHome: f.dataHome,
    smoke: noSmoke,
  });
  assert.equal(upgraded.upgraded, true);
  assert.equal(
    text(join(f.root, 'versions', nodeOnly.id, 'upstream', 'node', 'node.exe')),
    'node-2',
  );
});

// ---------------------------------------------------------- fault injection

const POINTS = [
  'after-backup',
  'after-apply',
  'launcher-file',
  'after-launchers',
  'after-install-json',
  'after-current-txt',
];

for (const point of POINTS) {
  test(`upgrade failing at ${point} returns to the exact pre-upgrade state`, (t) => {
    const f = fixture(t);
    installA(f);
    const before = snapshot(f);
    assert.throws(
      () =>
        put(
          f,
          { name: 'b', pluginVersion: '1.1.0' },
          {
            fault(p) {
              if (p === point) throw new Error(`injected ${p}`);
            },
          },
        ),
      /injected/,
    );
    assert.deepEqual(snapshot(f), before);
  });

  test(`crash at ${point} is repaired by recover()`, (t) => {
    const f = fixture(t);
    installA(f);
    const before = snapshot(f);
    const b = buildPayload(f.base, { name: 'b', pluginVersion: '1.1.0' });
    assert.throws(
      () =>
        install({
          payload: b.dir,
          root: f.root,
          dataHome: f.dataHome,
          smoke: noSmoke,
          fault(p) {
            if (p === point) throw new CrashError(`crash ${p}`);
          },
        }),
      CrashError,
    );
    assert.equal(recover(f.root), true);
    if (point === 'after-current-txt') {
      // Both pointers were already written: the change is final.
      assert.equal(readState(f.root).current, b.id);
      assert.equal(existsSync(join(f.root, 'txn.json')), false);
    } else {
      assert.deepEqual(snapshot(f), before);
    }
  });
}

test('a crash between the two pointer writes never leaves mixed pointers', (t) => {
  const f = fixture(t);
  const a = installA(f);
  const b = buildPayload(f.base, { name: 'b', pluginVersion: '1.1.0' });
  assert.throws(
    () =>
      install({
        payload: b.dir,
        root: f.root,
        dataHome: f.dataHome,
        smoke: noSmoke,
        fault(p) {
          if (p === 'after-install-json') throw new CrashError('power loss');
        },
      }),
    CrashError,
  );
  // install.json already says B, current.txt still says A: the journal exists.
  assert.equal(readState(f.root).current, b.id);
  assert.equal(text(join(f.root, 'current.txt')).trim(), a.id);
  assert.ok(existsSync(join(f.root, 'txn.json')));
  recover(f.root);
  assert.equal(readState(f.root).current, a.id);
  assert.equal(text(join(f.root, 'current.txt')).trim(), a.id);
});

test('failed smoke check during upgrade restores everything', (t) => {
  const f = fixture(t);
  installA(f);
  const before = snapshot(f);
  assert.throws(
    () =>
      put(
        f,
        { name: 'b', pluginVersion: '1.1.0' },
        {
          smoke() {
            throw new Error('boom');
          },
        },
      ),
    /boom/,
  );
  assert.deepEqual(snapshot(f), before);
});

test('profile is fully backed up before it is modified', (t) => {
  const f = fixture(t);
  installA(f);
  const original = profileManifest(f.dataHome);
  let seen;
  put(
    f,
    { name: 'b', pluginVersion: '1.1.0' },
    {
      fault(p) {
        if (p !== 'after-backup') return;
        seen = {
          profileUntouched: profileManifest(f.dataHome) === original,
          complete: existsSync(
            join(
              f.dataHome,
              'profile-backups',
              'auriga',
              readState(f.root).current,
              '.complete',
            ),
          ),
        };
      },
    },
  );
  assert.deepEqual(seen, { profileUntouched: true, complete: true });
});

test('an unfinished backup is never used for restoring', (t) => {
  const f = fixture(t);
  const a = installA(f);
  // Leftover from a crash while copying: no .complete marker, final name absent.
  const partial = join(
    f.dataHome,
    'profile-backups',
    'auriga',
    `${a.id}.partial`,
  );
  mkdirSync(join(partial, 'profile'), { recursive: true });
  writeFileSync(join(partial, 'profile', 'package.json'), '{"broken":true}');
  const before = snapshot(f);
  assert.throws(
    () =>
      put(
        f,
        { name: 'b', pluginVersion: '1.1.0' },
        {
          smoke() {
            throw new Error('boom');
          },
        },
      ),
    /boom/,
  );
  // The new complete backup replaced the stale partial one and was used.
  assert.deepEqual(snapshot(f), before);
});

test('first install failing leaves no pointers, profile or version behind', (t) => {
  const f = fixture(t);
  for (const point of POINTS) {
    assert.throws(
      () =>
        put(
          f,
          { name: 'a' },
          {
            fault(p) {
              if (p === point) throw new Error(`injected ${p}`);
            },
          },
        ),
      /injected/,
    );
    assert.equal(existsSync(join(f.root, 'install.json')), false, point);
    assert.equal(existsSync(join(f.root, 'current.txt')), false, point);
    assert.equal(existsSync(join(f.root, 'bin')), false, point);
    assert.equal(existsSync(profileDir(f.dataHome)), false, point);
    assert.deepEqual(readdirSync(join(f.root, 'versions')), [], point);
    assert.equal(existsSync(join(f.root, 'txn.json')), false, point);
  }
});

// ---------------------------------------------------------------- rollback

test('rollback restores previous version, plugin files and launchers', (t) => {
  const f = fixture(t);
  const a = installA(f);
  const b = put(f, { name: 'b', pluginVersion: '1.1.0' });
  assert.deepEqual(rollback({ root: f.root, smoke: noSmoke }), {
    current: a.id,
  });
  assert.equal(text(join(f.root, 'current.txt')).trim(), a.id);
  assert.equal(pluginVersion(f.dataHome), '1.0.0');
  assert.match(launcher(f.root), /rem a/);
  assert.deepEqual(readState(f.root), {
    dataHome: f.dataHome,
    current: a.id,
    previous: b.id,
  });
  // And forward again.
  assert.deepEqual(rollback({ root: f.root, smoke: noSmoke }), {
    current: b.id,
  });
  assert.equal(pluginVersion(f.dataHome), '1.1.0');
});

test('rollback keeps settings changed after the upgrade', (t) => {
  const f = fixture(t);
  installA(f);
  put(f, { name: 'b', pluginVersion: '1.1.0' });
  const settings = join(profileDir(f.dataHome), 'auriga-settings');
  mkdirSync(settings, { recursive: true });
  writeFileSync(join(settings, 'settings.json'), '{"platformOrigin":"x"}');
  rollback({ root: f.root, smoke: noSmoke });
  assert.equal(text(join(settings, 'settings.json')), '{"platformOrigin":"x"}');
});

test('rollback failing at the smoke check restores plugin files and pointers', (t) => {
  const f = fixture(t);
  installA(f);
  put(f, { name: 'b', pluginVersion: '1.1.0' });
  const before = snapshot(f);
  assert.throws(
    () =>
      rollback({
        root: f.root,
        smoke() {
          throw new Error('smoke broke');
        },
      }),
    /smoke broke/,
  );
  assert.deepEqual(snapshot(f), before);
  assert.equal(pluginVersion(f.dataHome), '1.1.0');
});

for (const point of POINTS) {
  test(`rollback failing at ${point} leaves version and plugin consistent`, (t) => {
    const f = fixture(t);
    installA(f);
    put(f, { name: 'b', pluginVersion: '1.1.0' });
    const before = snapshot(f);
    assert.throws(
      () =>
        rollback({
          root: f.root,
          smoke: noSmoke,
          fault(p) {
            if (p === point) throw new Error(`injected ${p}`);
          },
        }),
      /injected/,
    );
    assert.deepEqual(snapshot(f), before);
  });
}

test('rollback without previous version fails', (t) => {
  const f = fixture(t);
  put(f, { name: 'a' });
  assert.throws(
    () => rollback({ root: f.root, smoke: noSmoke }),
    /no previous/,
  );
});

// --------------------------------------------------------- layout & ownership

test('data directory inside the install directory is refused before any write', (t) => {
  const f = fixture(t);
  const p = buildPayload(f.base, { name: 'a' });
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: f.root,
        dataHome: join(f.root, 'home'),
        smoke: noSmoke,
      }),
    /inside the install directory/,
  );
  assert.equal(existsSync(f.root), false);
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: f.root,
        dataHome: f.root,
        smoke: noSmoke,
      }),
    /inside the install directory/,
  );
});

test('install directory inside or above the data directory is refused', (t) => {
  const f = fixture(t);
  const p = buildPayload(f.base, { name: 'a' });
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: join(f.dataHome, 'app'),
        dataHome: f.dataHome,
        smoke: noSmoke,
      }),
    /inside the data directory/,
  );
});

test('a drive or filesystem root is refused as install directory', (t) => {
  const f = fixture(t);
  const p = buildPayload(f.base, { name: 'a' });
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: parse(tmpdir()).root,
        dataHome: f.dataHome,
        smoke: noSmoke,
      }),
    /drive or filesystem root/,
  );
});

test('a non-empty directory without our marker is refused and left untouched', (t) => {
  const f = fixture(t);
  mkdirSync(f.root, { recursive: true });
  writeFileSync(join(f.root, 'precious.txt'), 'user file');
  const p = buildPayload(f.base, { name: 'a' });
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: f.root,
        dataHome: f.dataHome,
        smoke: noSmoke,
      }),
    /not empty and is not an Auriga Desktop installation/,
  );
  assert.deepEqual(readdirSync(f.root), ['precious.txt']);
  assert.throws(() => uninstall({ root: f.root }), /not an Auriga Desktop/);
  assert.equal(text(join(f.root, 'precious.txt')), 'user file');
});

test('uninstall removes only what we own and keeps user data and foreign files', (t) => {
  const f = fixture(t);
  put(f, { name: 'a' });
  writeFileSync(join(f.dataHome, 'keep.txt'), 'x');
  writeFileSync(join(f.root, 'notes.txt'), 'user note in install dir');
  mkdirSync(join(f.root, 'extra'), { recursive: true });
  assert.deepEqual(uninstall({ root: f.root }), {
    dataKept: true,
    dataHome: f.dataHome,
  });
  assert.deepEqual(readdirSync(f.root).sort(), ['extra', 'notes.txt']);
  assert.ok(existsSync(join(profileDir(f.dataHome), 'package.json')));
  assert.equal(text(join(f.dataHome, 'keep.txt')), 'x');
});

test('uninstall never deletes a data directory that ended up inside the install directory', (t) => {
  const f = fixture(t);
  put(f, { name: 'a' });
  // Corrupted/legacy state pointing the data home inside the install dir.
  const inside = join(f.root, 'home');
  mkdirSync(inside, { recursive: true });
  writeFileSync(join(inside, 'session.json'), 'precious');
  const state = readState(f.root);
  writeFileSync(
    join(f.root, 'install.json'),
    JSON.stringify({ ...state, dataHome: inside }),
  );
  uninstall({ root: f.root });
  assert.equal(text(join(inside, 'session.json')), 'precious');
});

test('purge removes our marked profile only and refuses unmarked directories', (t) => {
  const f = fixture(t);
  put(f, { name: 'a' });
  writeFileSync(join(f.dataHome, 'keep.txt'), 'x');
  uninstall({ root: f.root, purgeData: true });
  assert.equal(existsSync(profileDir(f.dataHome)), false);
  assert.equal(text(join(f.dataHome, 'keep.txt')), 'x');

  const g = fixture(t);
  put(g, { name: 'a' });
  writeFileSync(join(profileDir(g.dataHome), 'user-file.txt'), 'u');
  // A profile that is not ours (no marker): never removed.
  rmSync(join(profileDir(g.dataHome), '.auriga-desktop'));
  assert.throws(
    () => uninstall({ root: g.root, purgeData: true }),
    /refusing to remove unmarked directory/,
  );
  assert.equal(text(join(profileDir(g.dataHome), 'user-file.txt')), 'u');
  assert.ok(existsSync(join(g.root, 'install.json')));
});

// --------------------------------------------------------- verification

test('install rejects a corrupted package before copying anything', (t) => {
  const f = fixture(t);
  const p = buildPayload(f.base, { name: 'a' });
  writeFileSync(join(p.dir, 'auriga/runtime/core.mjs'), 'tampered');
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: f.root,
        dataHome: f.dataHome,
        smoke: noSmoke,
      }),
    /checksum mismatch|size mismatch/,
  );
  assert.equal(existsSync(join(f.root, 'versions')), false);
});

const NEGATIVE = [
  [
    'unlisted extra file',
    (p) => writeFileSync(join(p.dir, 'auriga/runtime/extra.mjs'), 'x'),
    /unlisted file/,
  ],
  [
    'missing listed file',
    (p) => rmSync(join(p.dir, 'licenses/NOTICE.txt')),
    /listed file missing/,
  ],
  [
    'id that does not match the component digest',
    (p) => {
      const m = JSON.parse(text(join(p.dir, 'manifest.json')));
      m.id = m.id.replace(/.$/, m.id.endsWith('0') ? '1' : '0');
      writeFileSync(join(p.dir, 'manifest.json'), JSON.stringify(m));
    },
    /component digest/,
  ],
  [
    'manifest without files',
    (p) => writeFileSync(join(p.dir, 'manifest.json'), '{"schema":1}'),
    /malformed/,
  ],
  [
    'unsafe path in the manifest',
    (p) => {
      const m = JSON.parse(text(join(p.dir, 'manifest.json')));
      m.files[0].path = '../escape';
      writeFileSync(join(p.dir, 'manifest.json'), JSON.stringify(m));
    },
    /unsafe manifest path|component digest/,
  ],
  [
    'upstream version different from the manifest',
    (p) =>
      writeFileSync(
        join(p.dir, 'upstream/app/node_modules/@deepseek-ai/dsh/package.json'),
        '{"version":"0.2.0-rc.2"}',
      ),
    /upstream dsh is 0\.2\.0-rc\.2/,
  ],
];

for (const [name, mutate, pattern] of NEGATIVE) {
  test(`install rejects ${name}`, (t) => {
    const f = fixture(t);
    const p = buildPayload(f.base, { name: 'a' });
    mutate(p);
    assert.throws(
      () =>
        install({
          payload: p.dir,
          root: f.root,
          dataHome: f.dataHome,
          smoke: noSmoke,
        }),
      pattern,
    );
    assert.equal(existsSync(join(f.root, 'install.json')), false);
  });
}

test('install rejects a plugin whose engines.dsh the upstream does not satisfy', (t) => {
  const f = fixture(t);
  const p = buildPayload(f.base, { name: 'a', enginesDsh: '>=9.0.0' });
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: f.root,
        dataHome: f.dataHome,
        smoke: noSmoke,
      }),
    /plugin requires dsh >=9\.0\.0/,
  );
});

test('a corrupted installed version is replaced when the package is installed again', (t) => {
  const f = fixture(t);
  const a = put(f, { name: 'a' });
  writeFileSync(
    join(f.root, 'versions', a.id, 'auriga', 'runtime', 'core.mjs'),
    'bit rot',
  );
  const again = put(f, { name: 'a' });
  assert.equal(again.upgraded, false);
  assert.equal(
    text(join(f.root, 'versions', a.id, 'auriga', 'runtime', 'core.mjs')),
    'core',
  );
});

test('launch environment: own data home, bundled node and pnpm first on PATH', () => {
  const dir = join(tmpdir(), 'v');
  const env = launchEnv(dir, join(tmpdir(), 'h'), { PATH: 'base', Foo: '1' });
  assert.equal(env.DSH_HOME, join(tmpdir(), 'h'));
  assert.equal(env.npm_config_store_dir, join(tmpdir(), 'h', 'pnpm-store'));
  const parts = env.PATH.split(delimiter);
  assert.equal(parts[0], join(dir, 'tools', 'bin'));
  assert.equal(parts[1], dirname(nodeBin(dir)));
  assert.equal(parts[2], 'base');
  assert.equal(env.Foo, '1');
});

// ------------------------------------------------- launchers and data directory

test('entry points exist at every moment of a launcher change (also after a crash in the middle)', (t) => {
  const f = fixture(t);
  installA(f);
  const entries = ['auriga-desktop.cmd', 'rollback.cmd'];
  const present = () =>
    entries.every((name) => existsSync(join(f.root, 'bin', name)));
  const seen = [];
  const b = buildPayload(f.base, { name: 'b', pluginVersion: '1.1.0' });
  assert.throws(
    () =>
      install({
        payload: b.dir,
        root: f.root,
        dataHome: f.dataHome,
        smoke: noSmoke,
        fault(p) {
          seen.push([p, present()]);
          if (p === 'launcher-file') throw new CrashError('crash mid-way');
        },
      }),
    CrashError,
  );
  assert.ok(seen.length > 0);
  assert.deepEqual(
    seen.filter(([, ok]) => !ok),
    [],
  );
  // Crashed with the first launcher already new and the second still old:
  // every entry point still exists and recover() puts the old set back.
  assert.match(launcher(f.root), /rem b/);
  assert.ok(present());
  recover(f.root);
  assert.ok(present());
  assert.match(launcher(f.root), /rem a/);
  assert.equal(text(join(f.root, 'bin', 'rollback.cmd')), 'rem rollback a');
});

test('a different data directory is refused for an existing installation', (t) => {
  const f = fixture(t);
  const a = installA(f);
  const before = snapshot(f);
  const other = join(f.base, 'elsewhere');
  const b = buildPayload(f.base, { name: 'b', pluginVersion: '1.1.0' });
  assert.throws(
    () =>
      install({
        payload: b.dir,
        root: f.root,
        dataHome: other,
        smoke: noSmoke,
      }),
    /already uses the data directory.*moving data is not supported/,
  );
  assert.deepEqual(snapshot(f), before);
  assert.equal(existsSync(other), false);
  assert.equal(readState(f.root).current, a.id);
  // The same directory written differently is fine.
  const again = install({
    payload: b.dir,
    root: f.root,
    dataHome: join(f.dataHome, '..', 'home'),
    smoke: noSmoke,
  });
  assert.equal(again.upgraded, true);
});

// --------------------------------------------- distributed upstream/pnpm files

const TREE_TAMPER = [
  [
    'a modified upstream DSH file',
    (p) =>
      writeFileSync(
        join(p.dir, 'upstream/app/node_modules/@deepseek-ai/dsh/index.js'),
        'process.exit(0) // tampered',
      ),
  ],
  [
    'an extra file in the upstream tree',
    (p) => writeFileSync(join(p.dir, 'upstream/app/node_modules/evil.js'), 'x'),
  ],
  [
    'a removed upstream file',
    (p) =>
      rmSync(
        join(p.dir, 'upstream/app/node_modules/@deepseek-ai/dsh/package.json'),
      ),
  ],
  [
    'a modified pnpm file',
    (p) =>
      writeFileSync(
        join(p.dir, 'tools/pnpm/node_modules/pnpm/package.json'),
        '{"name":"pnpm","version":"11.28.5","x":1}',
      ),
  ],
  [
    'an extra file in the pnpm tree',
    (p) => writeFileSync(join(p.dir, 'tools/pnpm/node_modules/evil.js'), 'x'),
  ],
];

for (const [name, mutate] of TREE_TAMPER) {
  test(`install rejects ${name} and leaves nothing behind`, (t) => {
    const f = fixture(t);
    const rebuilt = buildPayload(f.base, { name: 'a' });
    mutate(rebuilt);
    assert.throws(
      () =>
        install({
          payload: rebuilt.dir,
          root: f.root,
          dataHome: f.dataHome,
          smoke: noSmoke,
        }),
      /distributed files differ from the manifest|upstream dsh package missing/,
    );
    assert.equal(existsSync(join(f.root, 'install.json')), false);
    const versions = join(f.root, 'versions');
    assert.deepEqual(existsSync(versions) ? readdirSync(versions) : [], []);
  });
}

test('an upstream file changed after installation is replaced when the package is installed again', (t) => {
  const f = fixture(t);
  const a = put(f, { name: 'a' });
  writeFileSync(
    join(
      f.root,
      'versions',
      a.id,
      'upstream/app/node_modules/@deepseek-ai/dsh/package.json',
    ),
    '{"version":"0.2.1-rc.3","tampered":true}',
  );
  put(f, { name: 'a' });
  assert.equal(
    JSON.parse(
      text(
        join(
          f.root,
          'versions',
          a.id,
          'upstream/app/node_modules/@deepseek-ai/dsh/package.json',
        ),
      ),
    ).tampered,
    undefined,
  );
});

test('active shared state prevents rollback into a version without guardian', (t) => {
  const f = fixture(t);
  put(f, { name: 'a' });
  put(f, { name: 'b', pluginVersion: '1.1.0' });
  const before = readState(f.root);
  const directory = join(f.dataHome, 'auriga', 'guardian');
  mkdirSync(directory, { recursive: true });
  writeFileSync(
    join(directory, 'state.json'),
    JSON.stringify({ guardian: { state: 'active' } }),
  );
  assert.throws(() => rollback({ root: f.root, smoke: noSmoke }));
  assert.deepEqual(readState(f.root), before);
  writeFileSync(
    join(directory, 'state.json'),
    JSON.stringify({ guardian: { state: 'private_only' } }),
  );
  rollback({ root: f.root, smoke: noSmoke });
  assert.equal(readState(f.root).current, before.previous);
});
