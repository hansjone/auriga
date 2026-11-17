import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import {
  install,
  rollback,
  uninstall,
  versionDir,
  launch,
} from '../runtime/core.mjs';
import { verifyPackage } from '../runtime/verify.mjs';
import { buildPayload, tempBase } from './helpers.mjs';
import { systemTool } from '../runtime/system-tools.mjs';

test('Python unexpected files provide a stable diagnosis and reinstall repair instruction', (t) => {
  const payload = buildPayload(tempBase(t), { pythonContent: 'python' });
  mkdirSync(join(payload.dir, 'runtime/python/__pycache__'));
  writeFileSync(
    join(payload.dir, 'runtime/python/__pycache__/extra.pyc'),
    'cache',
  );
  assert.throws(
    () => verifyPackage(payload.dir),
    (error) => {
      assert.equal(error.code, 'desktop.python_runtime_modified');
      assert.equal(error.repair, 'reinstall');
      assert.match(error.message, /重新运行.*安装包/);
      assert.match(error.message, /extra\.pyc/);
      return true;
    },
  );
});

test('the normal launcher reports a Python repair path before starting DSH on an unexpected runtime file', async (t) => {
  const base = tempBase(t);
  const payload = buildPayload(base);
  const root = join(base, 'app');
  install({
    payload: payload.dir,
    root,
    dataHome: join(base, 'home'),
    smoke: () => {},
  });
  const runtime = join(versionDir(root, payload.id), 'runtime/python');
  mkdirSync(runtime, { recursive: true });
  writeFileSync(join(runtime, 'extra.pyc'), 'cache');
  await assert.rejects(launch({ root }), {
    code: 'desktop.python_runtime_modified',
    repair: 'reinstall',
  });
});

test(
  'Windows installed Python denies file writes and creation while upgrades, pruning, rollback and uninstall work',
  {
    skip: process.platform !== 'win32',
  },
  (t) => {
    const base = tempBase(t);
    const root = join(base, 'app');
    const dataHome = join(base, 'home');
    const options = { root, dataHome, smoke: () => {} };
    const versions = ['1.0.0', '1.0.1', '1.0.2'].map((version) =>
      buildPayload(base, {
        name: version,
        pluginVersion: version,
        pythonContent: 'python',
      }),
    );
    install({ ...options, payload: versions[0].dir });
    const runtime = join(versionDir(root, versions[0].id), 'runtime/python');
    const acl = spawnSync(
      systemTool('WindowsPowerShell/v1.0/powershell'),
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        '$acl=Get-Acl -LiteralPath $env:PYTHON_ACL_TEST; @{protected=$acl.AreAccessRulesProtected; owner=$acl.GetOwner([Security.Principal.SecurityIdentifier]).Value; current=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value} | ConvertTo-Json -Compress',
      ],
      {
        env: { ...process.env, PYTHON_ACL_TEST: runtime },
        windowsHide: true,
        encoding: 'utf8',
      },
    );
    assert.equal(acl.status, 0, acl.stderr);
    const security = JSON.parse(acl.stdout);
    assert.equal(security.protected, true);
    assert.equal(security.owner, security.current);
    assert.equal(readFileSync(join(runtime, 'python.exe'), 'utf8'), 'python');
    for (const operation of [
      () => writeFileSync(join(runtime, 'runtime.json'), '{}'),
      () => writeFileSync(join(runtime, 'python.exe'), 'append', { flag: 'a' }),
      () => writeFileSync(join(runtime, 'unlisted.pyc'), 'cache'),
      () => mkdirSync(join(runtime, '__pycache__')),
      () =>
        writeFileSync(
          join(runtime, 'Lib/site-packages/fixture.py'),
          'modified',
        ),
      () =>
        writeFileSync(join(runtime, 'Lib/site-packages/extra.pyc'), 'cache'),
    ])
      assert.throws(operation, /EPERM|EACCES/);
    const child = spawnSync(
      process.execPath,
      [
        '-e',
        'require("node:fs").writeFileSync(process.argv[1], "cache")',
        join(runtime, 'child.pyc'),
      ],
      {
        env: { ...process.env, PYTHONDONTWRITEBYTECODE: '' },
        encoding: 'utf8',
        windowsHide: true,
      },
    );
    assert.notEqual(child.status, 0);
    assert.equal(existsSync(join(runtime, 'child.pyc')), false);
    install({ ...options, payload: versions[1].dir });
    install({ ...options, payload: versions[2].dir });
    assert.equal(existsSync(versionDir(root, versions[0].id)), false);
    rollback({ root, smoke: () => {} });
    const previous = join(versionDir(root, versions[1].id), 'runtime/python');
    assert.throws(
      () => writeFileSync(join(previous, 'extra'), 'x'),
      /EPERM|EACCES/,
    );
    assert.deepEqual(uninstall({ root }).pending, []);
    assert.equal(existsSync(join(root, 'versions')), false);
  },
);
