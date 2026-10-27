import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import {
  parseArguments,
  assertStatePath,
  isolatedEnvironment,
  connectionPatch,
  claimState,
  stopOwnedProcess,
} from '../../scripts/beta-ui/plugin-dsh.mjs';

const base = path.resolve('.dev/plugin-dsh');
function temporary(t) {
  fs.mkdirSync(base, { recursive: true });
  const dir = fs.mkdtempSync(path.join(base, 'guard-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('arguments reject missing, duplicate, unknown and unsafe launch values', () => {
  for (const args of [
    [],
    ['launch'],
    ['launch', '--state', 'relative'],
    ['launch', '--state', base, '--state', base],
    ['stop', '--state', base, '--port', '1'],
    ['launch', '--state', base, '--port', '18080'],
    ['launch', '--state', base, '--profile', 'desktop'],
  ]) {
    assert.throws(() => parseArguments(args));
  }
  assert.equal(
    parseArguments(['launch', '--state', base, '--dsh-package', base]).port,
    0,
  );
});

test('state rejects real DSH home, ancestors, default profile and repository source', () => {
  const user = path.resolve('example-user');
  const source = {
    USERPROFILE: user,
    HOME: user,
    DSH_HOME: path.join(user, 'custom-harness'),
  };
  for (const dir of [
    user,
    path.dirname(user),
    path.join(user, '.dsh', 'profiles', 'test'),
    path.join(user, 'custom-harness', 'fresh'),
    path.resolve('clients/dsh-plugin'),
  ]) {
    assert.throws(() => assertStatePath(dir, source));
  }
  assert.equal(
    assertStatePath(path.join(base, 'fresh'), source),
    path.join(base, 'fresh'),
  );
});

test('fresh claim refuses existing directories and symlink ancestors without modifying targets', (t) => {
  const dir = temporary(t);
  const target = path.join(dir, 'target');
  fs.mkdirSync(target);
  fs.writeFileSync(path.join(target, 'sentinel'), 'unchanged');
  assert.throws(() => claimState(target));
  const link = path.join(dir, 'link');
  fs.symlinkSync(
    target,
    link,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  assert.throws(() => claimState(path.join(link, 'fresh')));
  assert.deepEqual(fs.readdirSync(target), ['sentinel']);
  fs.unlinkSync(link);
  const fresh = path.join(dir, 'fresh');
  claimState(fresh);
  assert.ok(fs.existsSync(fresh));
  assert.throws(() => claimState(fresh));
});

test('child environment drops real profiles, credentials, patches and Node injection', () => {
  const source = {
    Path: 'tools',
    SystemRoot: 'windows',
    DSH_HOME: 'real-profile',
    dsh_home: 'another-real-profile',
    AURIGA_MODE: 'managed',
    AURIGA_INSTANCE_TOKEN_FILE: 'secret',
    DEEPSEEK_API_KEY: 'secret',
    NODE_OPTIONS: '--import=unsafe',
    USERPROFILE: 'real-user',
    npm_config_userconfig: 'real-npmrc',
    PSModulePath: 'foreign',
  };
  const env = isolatedEnvironment(base, source);
  for (const key of [
    'AURIGA_INSTANCE_TOKEN_FILE',
    'DEEPSEEK_API_KEY',
    'NODE_OPTIONS',
    'PSModulePath',
    'dsh_home',
  ])
    assert.equal(env[key], undefined);
  for (const key of [
    'DSH_HOME',
    'USERPROFILE',
    'HOME',
    'APPDATA',
    'LOCALAPPDATA',
    'TMP',
    'TEMP',
    'XDG_CONFIG_HOME',
    'npm_config_userconfig',
  ])
    assert.ok(env[key].startsWith(base + path.sep), key);
  assert.equal(env.AURIGA_MODE, 'connect');
  assert.equal(env.Path, 'tools');
  assert.equal(source.USERPROFILE, 'real-user');
});

test('Beta patch is enabled and contains explicit loopback HTTP plus native client, no credentials', () => {
  const patch = connectionPatch('team-provider');
  assert.match(patch, /auriga-beta-native/);
  assert.match(patch, /allowLocalHttp/);
  assert.match(patch, /127\.0\.0\.1:18080/);
  assert.match(patch, /team-provider/);
  assert.doesNotMatch(patch, /password|secret|token|network|wssUrl/);
});

test('stop refuses reused PID and never kills unrelated process', async () => {
  const calls = [];
  await assert.rejects(
    stopOwnedProcess(
      { pid: 42, identity: 'old' },
      {
        identity: () => 'new',
        kill: (pid) => calls.push(pid),
      },
    ),
    /PID/,
  );
  assert.deepEqual(calls, []);
  let alive = true;
  await stopOwnedProcess(
    { pid: 42, identity: 'ours' },
    {
      identity: () => (alive ? 'ours' : null),
      kill: (pid) => {
        calls.push(pid);
        alive = false;
      },
    },
  );
  assert.deepEqual(calls, [42]);
  await stopOwnedProcess(null);
});
