import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  appendFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { makeExe } from '../scripts/make-exe.mjs';
import { tempBase } from './helpers.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const windows = process.env.SystemRoot ?? 'C:\\Windows';

test(
  'Framework SFX long TEMP: wizard, silent, cancellation, unsafe paths and cleanup',
  { skip: process.platform !== 'win32', timeout: 180000 },
  (t) => {
    const base = tempBase(t);
    const longTemp = join(
      base,
      // Keep the legacy test root below its directory limit so its first
      // failure occurs in the deep package tree, even under a longer D: cache.
      'default-temp-longer-than-real-user-profile'
        .repeat(2)
        .slice(0, Math.max(1, 180 - base.length - 1)),
    );
    mkdirSync(longTemp);
    const env = {
      ...process.env,
      TEMP: longTemp,
      TMP: longTemp,
      AURIGA_REGISTRY_KEY: `AurigaSfxTest-${Date.now()}`,
      SFX_TEST_RESULT: join(base, 'result'),
    };
    const run = (exe, args, timeout = 60000) =>
      spawnSync(exe, args, { env, encoding: 'utf8', timeout });
    const stage = join(base, 'stage');
    const deep = join(
      stage,
      'payload',
      ...Array(9).fill('deep-dependency-directory'),
      'module.js',
    );
    mkdirSync(dirname(deep), { recursive: true });
    writeFileSync(deep, 'deep-file');
    for (let i = 0; i < 20; i++)
      writeFileSync(join(stage, `file-${i}`), 'fixture');
    writeFileSync(
      join(stage, 'install.ps1'),
      `param([string]$InstallDir, [switch]$Progress, [string]$CancelFile, [switch]$NoDesktopShortcut, [int]$ExitCode=0)
if (!(Test-Path -LiteralPath (Join-Path $PSScriptRoot 'payload'))) { exit 9 }
Write-Output '@@progress {"stage":"verify","fraction":1}'
[IO.File]::WriteAllText($env:SFX_TEST_RESULT, 'installed')
if ($env:SFX_FAIL) {
  # Same shape as a real PowerShell terminating error: the message comes first and the
  # "+ FullyQualifiedErrorId" bookkeeping last. The wizard must surface the message.
  [Console]::Error.WriteLine('Exception calling "GetFullPath" with "1" argument(s): "Illegal characters in path."')
  [Console]::Error.WriteLine('At C:\\pkg\\install.ps1:23 char:1')
  [Console]::Error.WriteLine('+ FullyQualifiedErrorId : ArgumentException')
  exit 7
}
exit $ExitCode
`,
    );
    const exe = makeExe({ stage, out: join(base, 'setup.exe') });
    const harness = join(base, 'paths.exe');
    const compile = run(
      join(windows, 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'),
      [
        '/nologo',
        '/codepage:65001',
        '/target:exe',
        '/main:SfxPaths',
        `/out:${harness}`,
        '/r:System.Windows.Forms.dll',
        '/r:System.Drawing.dll',
        '/r:System.IO.Compression.dll',
        '/r:System.IO.Compression.FileSystem.dll',
        '/r:System.Web.Extensions.dll',
        resolve(here, '../windows/sfx/Stub.cs'),
        join(here, 'windows-sfx-paths.cs'),
      ],
    );
    assert.equal(compile.status, 0, compile.stdout + compile.stderr);
    // Same payload and trailer, alternate entry point for exercising the real Wizard.
    const packed = readFileSync(exe);
    const size = Number(packed.readBigInt64LE(packed.length - 8));
    appendFileSync(harness, packed.subarray(packed.length - 16 - size));
    const target = () =>
      join(longTemp, 'auriga-desktop-setup-' + 'a'.repeat(32), 'pkg');
    const legacy = run(harness, ['legacy', exe, target()]);
    assert.equal(legacy.status, 1, legacy.stdout + legacy.stderr);
    assert.match(legacy.stdout, /PathTooLongException/);
    t.diagnostic(`legacy first error: ${legacy.stdout.trim()}`);
    for (const mode of ['modern', 'cancel']) {
      const result = run(harness, [mode, exe, target()]);
      assert.equal(
        result.status,
        mode === 'cancel' ? 2 : 0,
        result.stdout + result.stderr,
      );
      assert.match(result.stdout, /cleaned=True/);
      assert.equal(existsSync(target()), false);
      t.diagnostic(`${mode}: ${result.stdout.trim()}`);
    }
    const unsafe = run(harness, ['unsafe', join(base, 'unsafe')]);
    assert.equal(unsafe.status, 0, unsafe.stdout + unsafe.stderr);
    for (const code of [0, 7]) {
      const result = run(exe, ['/VERYSILENT', '-ExitCode', String(code)]);
      assert.equal(result.status, code, result.stdout + result.stderr);
    }
    const wizard = run(harness, ['wizard', join(base, 'isolated-install')]);
    assert.equal(wizard.status, 0, wizard.stdout + wizard.stderr);
    assert.match(wizard.stdout, /wizardExit=0 progress=1000/);
    t.diagnostic(wizard.stdout.trim());
    assert.equal(readFileSync(env.SFX_TEST_RESULT, 'utf8'), 'installed');
    // Regression: a root with a trailing separator used to escape the closing quote on the
    // install.ps1 command line, so PowerShell received `D:\X"` and died in GetFullPath with
    // ArgumentException ("路径中具有非法字符") -- the wizard then reported a failure with no
    // usable log. The argument must survive quoting and reach install.ps1 unchanged.
    const trailing = run(harness, [
      'wizard',
      join(base, 'isolated-install-trailing') + '\\',
    ]);
    assert.equal(trailing.status, 0, trailing.stdout + trailing.stderr);
    assert.match(trailing.stdout, /wizardExit=0 progress=1000 openLog=False/);
    t.diagnostic(trailing.stdout.trim());
    assert.equal(readFileSync(env.SFX_TEST_RESULT, 'utf8'), 'installed');
    env.SFX_FAIL = '1';
    const failedWizard = run(harness, [
      'wizard',
      join(base, 'isolated-install'),
    ]);
    assert.equal(
      failedWizard.status,
      7,
      failedWizard.stdout + failedWizard.stderr,
    );
    assert.match(failedWizard.stdout, /wizardExit=7/);
    assert.match(failedWizard.stdout, /openLog=True/);
    // The failure page must show the real error and a usable log path, not the
    // "+ FullyQualifiedErrorId" tail with an empty log location.
    assert.match(failedWizard.stdout, /Illegal characters in path/);
    assert.doesNotMatch(failedWizard.stdout, /安装日志：\s*$/m);
    assert.doesNotMatch(failedWizard.stdout, /安装失败：\s*\+/);
    t.diagnostic(`failed finish text: ${failedWizard.stdout.trim()}`);
    assert.equal(
      readdirSync(longTemp).some(
        (name) =>
          name.startsWith('auriga-desktop-setup-') &&
          name !== 'auriga-desktop-setup-' + 'a'.repeat(32),
      ),
      false,
    );
    // Optional exact release regression: publisher EXE is opened as data, never executed.
    if (process.env.AURIGA_SFX_RELEASE_EXE) {
      const release = resolve(process.env.AURIGA_SFX_RELEASE_EXE);
      assert.equal(
        createHash('sha256').update(readFileSync(release)).digest('hex'),
        '73c042a53f22c4852e03051c78724709cd6e0adbd8e212cee5f1505ffdbaf0de',
      );
      const result = run(harness, ['modern', release, target()], 300000);
      assert.equal(result.status, 0, result.stdout + result.stderr);
      assert.match(result.stdout, /files=39646 bytes=729098089/);
      t.diagnostic(result.stdout.trim());
    }
  },
);
