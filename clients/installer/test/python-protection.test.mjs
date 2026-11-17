import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
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

const probeSource = fileURLToPath(
  new URL('./windows-python-access.cs', import.meta.url),
);

function assertNodeWriteDenials(runtime) {
  const result = spawnSync(
    systemTool('WindowsPowerShell/v1.0/powershell'),
    [
      '-NoProfile',
      '-NonInteractive',
      '-Command',
      `
    $ErrorActionPreference='Stop'
    Add-Type -Path $env:PYTHON_ACL_PROBE_SOURCE
    $identity=[Security.Principal.WindowsIdentity]::GetCurrent()
    $administrator=(New-Object Security.Principal.WindowsPrincipal($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if ([PythonAccessProbe]::Privilege('SeRestorePrivilege') -ge 0) { [PythonAccessProbe]::RemoveRestore() }
    & $env:PYTHON_ACL_NODE $env:PYTHON_ACL_CHECKS $env:PYTHON_ACL_TEST
    if ($LASTEXITCODE -ne 0) { throw 'Node write-denial assertions failed' }
    @{administrator=$administrator; restore=[PythonAccessProbe]::Privilege('SeRestorePrivilege')} | ConvertTo-Json -Compress
    `,
    ],
    {
      env: {
        ...process.env,
        PYTHON_ACL_PROBE_SOURCE: probeSource,
        PYTHON_ACL_NODE: process.execPath,
        PYTHON_ACL_CHECKS: fileURLToPath(
          new URL('./python-write-checks.mjs', import.meta.url),
        ),
        PYTHON_ACL_TEST: runtime,
      },
      windowsHide: true,
      encoding: 'utf8',
      timeout: 30000,
    },
  );
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const proof = JSON.parse(result.stdout.trim().split(/\r?\n/).at(-1));
  assert.equal(proof.restore, -1);
  return proof;
}

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
        `
        $ErrorActionPreference='Stop'
        $identity=[Security.Principal.WindowsIdentity]::GetCurrent()
        $sid=$identity.User
        Add-Type -Path $env:PYTHON_ACL_PROBE_SOURCE
        $file=Join-Path $env:PYTHON_ACL_TEST 'runtime.json'
        $native=@{restore=[PythonAccessProbe]::Privilege('SeRestorePrivilege'); backup=[PythonAccessProbe]::Privilege('SeBackupPrivilege'); nodeRestore=[PythonAccessProbe]::PrivilegeForProcess([int]$env:PYTHON_ACL_PARENT_PID,'SeRestorePrivilege'); ordinaryWrite=[PythonAccessProbe]::OpenForWrite($file,$false,$false,$false); backupWrite=[PythonAccessProbe]::OpenForWrite($file,$true,$false,$false)}
        $native.ordinaryAppend=[PythonAccessProbe]::OpenForWrite((Join-Path $env:PYTHON_ACL_TEST 'python.exe'),$false,$false,$true)
        $native.ordinaryCreate=[PythonAccessProbe]::OpenForWrite((Join-Path $env:PYTHON_ACL_TEST 'native-extra.pyc'),$false,$true,$false)
        $native.directoryWithAmbientPrivileges=[PythonAccessProbe]::MakeDirectory((Join-Path $env:PYTHON_ACL_TEST 'native-cache'))
        $native.ordinaryNestedWrite=[PythonAccessProbe]::OpenForWrite((Join-Path $env:PYTHON_ACL_TEST 'Lib/site-packages/fixture.py'),$false,$false,$false)
        $native.ordinaryNestedCreate=[PythonAccessProbe]::OpenForWrite((Join-Path $env:PYTHON_ACL_TEST 'Lib/site-packages/native-extra.pyc'),$false,$true,$false)
        $acl=[IO.Directory]::GetAccessControl($env:PYTHON_ACL_TEST)
        $paths=@('', 'runtime.json', 'python.exe', 'Lib/site-packages/fixture.py')
        $details=@(foreach($relative in $paths) {
          $path=if($relative){Join-Path $env:PYTHON_ACL_TEST $relative}else{$env:PYTHON_ACL_TEST}
          $security=if([IO.Directory]::Exists($path)){[IO.Directory]::GetAccessControl($path)}else{[IO.File]::GetAccessControl($path)}
          @{path=$relative; protected=$security.AreAccessRulesProtected; canonical=$security.AreAccessRulesCanonical; rules=@($security.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier]) | ForEach-Object {
            @{subject=if($_.IdentityReference.Equals($sid)){'current-user'}elseif($_.IdentityReference.Value -eq 'S-1-5-18'){'SYSTEM'}else{'other'}; type=$_.AccessControlType.ToString(); rights=$_.FileSystemRights.ToString(); inherited=$_.IsInherited}
          })}
        })
        @{protected=$acl.AreAccessRulesProtected; owner=$acl.GetOwner([Security.Principal.SecurityIdentifier]).Value; current=$sid.Value; administrator=(New-Object Security.Principal.WindowsPrincipal($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator); details=$details; native=$native; privileges=@(& (Join-Path $env:SystemRoot 'System32/whoami.exe') /priv | Select-String 'SeBackupPrivilege|SeRestorePrivilege|SeTakeOwnershipPrivilege' | ForEach-Object {$_.Line.Trim()})} | ConvertTo-Json -Depth 6 -Compress
        `,
      ],
      {
        env: {
          ...process.env,
          PYTHON_ACL_TEST: runtime,
          PYTHON_ACL_PARENT_PID: String(process.pid),
          PYTHON_ACL_PROBE_SOURCE: probeSource,
        },
        windowsHide: true,
        encoding: 'utf8',
      },
    );
    assert.equal(acl.status, 0, acl.stderr);
    const security = JSON.parse(acl.stdout);
    t.diagnostic(
      JSON.stringify({
        administrator: security.administrator,
        native: security.native,
        details: security.details,
        privileges: security.privileges,
      }),
    );
    assert.equal(security.protected, true);
    assert.equal(security.owner, security.current);
    assert.equal(readFileSync(join(runtime, 'python.exe'), 'utf8'), 'python');
    for (const [name, error] of Object.entries(security.native))
      if (name.startsWith('ordinary'))
        assert.equal(error, 5, `${name} must return ERROR_ACCESS_DENIED`);
    const nodeProof = assertNodeWriteDenials(runtime);
    assert.equal(nodeProof.administrator, security.administrator);
    t.diagnostic(`Node deny checks ${JSON.stringify(nodeProof)}`);
    assert.equal(existsSync(join(runtime, 'child.pyc')), false);
    install({ ...options, payload: versions[1].dir });
    install({ ...options, payload: versions[2].dir });
    assert.equal(existsSync(versionDir(root, versions[0].id)), false);
    rollback({ root, smoke: () => {} });
    const previous = join(versionDir(root, versions[1].id), 'runtime/python');
    assertNodeWriteDenials(previous);
    assert.deepEqual(uninstall({ root }).pending, []);
    assert.equal(existsSync(join(root, 'versions')), false);
  },
);

test(
  'Windows restore privilege backup writes are rejected by the normal launcher and repairable by reinstall',
  {
    skip: process.platform !== 'win32',
  },
  async (t) => {
    const base = tempBase(t);
    const payload = buildPayload(base, { pythonContent: 'python' });
    const root = join(base, 'app');
    const options = {
      root,
      dataHome: join(base, 'home'),
      payload: payload.dir,
      smoke: () => {},
    };
    install(options);
    const version = versionDir(root, payload.id);
    const result = spawnSync(
      systemTool('WindowsPowerShell/v1.0/powershell'),
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        `
    $ErrorActionPreference='Stop'
    Add-Type -Path $env:PYTHON_ACL_PROBE_SOURCE
    $file=Join-Path $env:PYTHON_ACL_TEST 'runtime.json'
    $extra=Join-Path $env:PYTHON_ACL_TEST 'backup-extra.pyc'
    $original=[PythonAccessProbe]::Privilege('SeRestorePrivilege')
    try {
      if ($original -ge 0) { [PythonAccessProbe]::SetRestore($false) }
      $disabled=[PythonAccessProbe]::OpenForWrite($file,$true,$false,$false)
      if ($original -ge 0) { [PythonAccessProbe]::SetRestore($true) }
      @{restoreAvailable=($original -ge 0); disabledBackupWrite=$disabled; ordinaryWrite=[PythonAccessProbe]::OpenForWrite($file,$false,$false,$false); backupWrite=[PythonAccessProbe]::OpenForWrite($file,$true,$false,$false); backupCreate=[PythonAccessProbe]::OpenForWrite($extra,$true,$true,$false)} | ConvertTo-Json -Compress
    } finally { if ($original -ge 0) { [PythonAccessProbe]::SetRestore(($original -band 2) -ne 0) } }
    `,
      ],
      {
        env: {
          ...process.env,
          PYTHON_ACL_PROBE_SOURCE: probeSource,
          PYTHON_ACL_TEST: join(version, 'runtime/python'),
        },
        windowsHide: true,
        encoding: 'utf8',
        timeout: 30000,
      },
    );
    assert.equal(result.status, 0, result.stderr);
    const proof = JSON.parse(result.stdout);
    t.diagnostic(`Native backup proof ${JSON.stringify(proof)}`);
    assert.equal(proof.ordinaryWrite, 5);
    assert.equal(proof.disabledBackupWrite, 5);
    assert.equal(proof.backupWrite, proof.restoreAvailable ? 0 : 5);
    assert.equal(proof.backupCreate, proof.restoreAvailable ? 0 : 5);
    if (proof.restoreAvailable) {
      assert.throws(() => verifyPackage(version), {
        code: 'desktop.python_runtime_modified',
        repair: 'reinstall',
      });
      await assert.rejects(launch({ root }), {
        code: 'desktop.python_runtime_modified',
        repair: 'reinstall',
      });
      install(options);
      assert.doesNotThrow(() => verifyPackage(version));
      assert.equal(
        existsSync(join(version, 'runtime/python/backup-extra.pyc')),
        false,
      );
    }
  },
);
