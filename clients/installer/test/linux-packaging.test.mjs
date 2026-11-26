import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { clientPlatform } from '../../../scripts/client-release/platform.mjs';
import { packagedPythonBin } from '../scripts/python-runtime.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const linux = join(here, '../linux');

function read(rel) {
  return readFileSync(join(linux, rel), 'utf8');
}

test('linux install shell and launchers keep XDG defaults and payload layout', () => {
  const install = read('install.sh');
  assert.match(install, /^#!\/usr\/bin\/env bash/m);
  assert.match(install, /XDG_DATA_HOME/);
  assert.match(install, /XDG_OPT_HOME|\$HOME\/\.local\/opt/);
  assert.match(install, /auriga-desktop/);
  assert.match(install, /payload/);

  const launcher = read('launcher/auriga-desktop');
  assert.match(launcher, /^#!\/usr\/bin\/env bash/m);
  assert.match(launcher, /upstream\/node\/bin\/node/);

  for (const rel of [
    'launcher/uninstall.sh',
    'launcher/rollback.sh',
    'launcher/shell-integration.sh',
    'tray/auriga-desktop-tray',
  ]) {
    assert.ok(existsSync(join(linux, rel)), rel);
    assert.match(read(rel), /^#!/m);
  }
});

test('linux-x64 platform recipe matches installer package naming', () => {
  const platform = clientPlatform('linux-x64');
  assert.equal(platform.packageMedia, 'tar.gz');
  assert.equal(
    platform.setupName('1.2.3'),
    'auriga-desktop-1.2.3-linux-x64.tar.gz',
  );
  assert.equal(platform.nodeBin, 'bin/node');
  assert.equal(
    packagedPythonBin('/runtime/python', 'linux-x64-cp312'),
    join('/runtime/python', 'bin', 'python3'),
  );
  assert.equal(
    packagedPythonBin('/runtime/python', 'win-x64-cp312'),
    join('/runtime/python', 'python.exe'),
  );
});

test(
  'linux personal python paths use bin and lib/python3.12',
  { skip: process.platform !== 'linux' },
  async () => {
    const { pythonUserPaths } = await import('../runtime/python-user.mjs');
    const paths = pythonUserPaths('/tmp/auriga-data');
    assert.match(paths.bin, /[/\\]bin$/);
    assert.match(paths.site, /lib\/python3\.12\/site-packages$/);
    assert.equal(paths.config.endsWith('pip.conf'), true);
  },
);
