import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CLIENT_TARGET,
  clientPlatform,
  hostClientTarget,
  supportedClientTargets,
} from './platform.mjs';
import {
  ROOT,
  lockIdentity,
  lockPathsForTarget,
  readBuildNodePin,
} from './inputs.mjs';

test('default client target stays win-x64', () => {
  assert.equal(CLIENT_TARGET, 'win-x64');
  assert.deepEqual(supportedClientTargets().sort(), ['linux-x64', 'win-x64']);
});

test('linux-x64 recipe names Node and setup archives', () => {
  const linux = clientPlatform('linux-x64');
  assert.equal(linux.os, 'linux');
  assert.equal(linux.nodeArchive('24.21.0'), 'node-v24.21.0-linux-x64.tar.xz');
  assert.equal(linux.nodeBin, 'bin/node');
  assert.equal(linux.buildNodeShaField, 'linuxX64TarSha256');
  assert.equal(
    linux.pythonLock,
    'clients/installer/locks/python-linux-x64.json',
  );
  assert.equal(linux.kitPrefix, 'auriga-client-linux');
  assert.equal(
    linux.setupName('0.2.0'),
    'auriga-desktop-0.2.0-linux-x64.tar.gz',
  );
  assert.equal(linux.stagedPackageName, 'desktop.tar.gz');
});

test('unknown target is rejected', () => {
  assert.throws(() => clientPlatform('darwin-arm64'), /尚未提供构建配方/);
});

test('host target matches this machine when supported', () => {
  if (
    (process.platform === 'win32' || process.platform === 'linux') &&
    process.arch === 'x64'
  ) {
    assert.equal(
      hostClientTarget(),
      process.platform === 'win32' ? 'win-x64' : 'linux-x64',
    );
  }
});

test('linux and windows kit lock identities diverge on python pin', () => {
  const winPaths = lockPathsForTarget('win-x64');
  const linuxPaths = lockPathsForTarget('linux-x64');
  assert.ok(winPaths.includes('clients/installer/locks/python-win-x64.json'));
  assert.ok(
    linuxPaths.includes('clients/installer/locks/python-linux-x64.json'),
  );
  assert.equal(
    winPaths.includes('clients/installer/locks/python-linux-x64.json'),
    false,
  );
  const win = lockIdentity(ROOT, 'win-x64');
  const linux = lockIdentity(ROOT, 'linux-x64');
  assert.notEqual(win.lockSha256, linux.lockSha256);
  assert.match(win.lockSha256, /^[a-f0-9]{64}$/);
  assert.match(linux.lockSha256, /^[a-f0-9]{64}$/);
  assert.equal(
    readBuildNodePin(ROOT, 'linux-x64').shaField,
    'linuxX64TarSha256',
  );
});
