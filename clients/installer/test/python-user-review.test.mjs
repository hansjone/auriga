import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tempBase, buildPayload } from './helpers.mjs';
import { install, launchEnv } from '../runtime/core.mjs';
import {
  cleanPythonEnv,
  configurePythonIndex,
  preparePythonUser,
  pythonUserPaths,
  resetPythonUser,
  pythonUserFailureReason,
} from '../runtime/python-user.mjs';

for (const mode of ['path', 'import']) {
  test(
    `personal ${mode} .pth path stays behind built-in imports`,
    { skip: process.platform !== 'win32' },
    (t) => {
      const base = tempBase(t);
      const hook = fileURLToPath(
        new URL('../scripts/lib/python-sitecustomize.py', import.meta.url),
      );
      const source = String.raw`
import pathlib, site, sys
root = pathlib.Path(sys.argv[1])
managed = root / 'managed'
bundled = root / 'bundled'
personal_site = managed / 'Lib' / 'site-packages'
builtin_site = bundled / 'Lib' / 'site-packages'
extra = managed / 'extra'
for folder in (personal_site, builtin_site, extra):
    folder.mkdir(parents=True)
(managed / 'auriga-python.json').write_text('{}')
(builtin_site / 'docx.py').write_text('SOURCE = "builtin"')
(extra / 'docx.py').write_text('SOURCE = "personal"')
line = str(extra) if sys.argv[3] == 'path' else 'import sys; sys.path.insert(0, ' + repr(str(extra)) + ')'
(personal_site / 'collision.pth').write_text(line + '\n')
sys.prefix = str(managed)
sys.base_prefix = str(bundled)
sys.path.insert(0, str(builtin_site))
site.addsitedir(str(personal_site))
exec(compile(pathlib.Path(sys.argv[2]).read_text(), sys.argv[2], 'exec'))
assert sys.path.index(str(extra)) > sys.path.index(str(builtin_site)), sys.path
import docx
assert docx.SOURCE == 'builtin', docx.__file__
print('builtin wins')
`;
      assert.equal(
        execFileSync('python', ['-I', '-B', '-c', source, base, hook, mode], {
          windowsHide: true,
          encoding: 'utf8',
        }).trim(),
        'builtin wins',
      );
    },
  );
}

test('reset recovers corrupt settings offline and clears cached packages', (t) => {
  const home = join(tempBase(t), 'home');
  const paths = pythonUserPaths(home);
  configurePythonIndex(home, 'https://forgejo.example.com/simple');
  mkdirSync(paths.site, { recursive: true });
  writeFileSync(join(paths.site, 'old.py'), 'old');
  mkdirSync(join(paths.root, 'cache'));
  writeFileSync(join(paths.root, 'cache/cached.whl'), 'cache');
  writeFileSync(paths.settings, '{broken');
  assert.deepEqual(resetPythonUser(home), { reset: true });
  assert.equal(existsSync(paths.environment), false);
  assert.equal(existsSync(join(paths.root, 'cache')), false);
  assert.deepEqual(JSON.parse(readFileSync(paths.settings, 'utf8')), {
    schema: 1,
    indexUrl: null,
  });
  assert.match(readFileSync(paths.config, 'utf8'), /no-index = true/);
});

test('managed Python rejects an ABI change before touching existing extensions', (t) => {
  const base = tempBase(t);
  const dir = join(base, 'runtime');
  const home = join(base, 'home');
  const paths = pythonUserPaths(home);
  mkdirSync(join(dir, 'auriga/python'), { recursive: true });
  writeFileSync(join(dir, 'auriga/python/pip.json'), '{}');
  mkdirSync(join(dir, 'runtime/python'), { recursive: true });
  writeFileSync(
    join(dir, 'runtime/python/runtime.json'),
    JSON.stringify({ version: '3.13.1', packages: [] }),
  );
  mkdirSync(paths.site, { recursive: true });
  writeFileSync(join(paths.site, 'extension.cp312-win_amd64.pyd'), 'old ABI');
  assert.throws(
    () => preparePythonUser(dir, home),
    (error) => error.reason === 'python_user.abi_mismatch',
  );
  assert.equal(
    readFileSync(join(paths.site, 'extension.cp312-win_amd64.pyd'), 'utf8'),
    'old ABI',
  );
});

test('managed Python removes the inherited venv launcher override case-insensitively', () => {
  const parent = {
    PATH: 'existing',
    __PYVENV_LAUNCHER__: 'external',
    __pyvenv_launcher__: 'external',
  };
  assert.deepEqual(cleanPythonEnv(parent), { PATH: 'existing' });
  assert.equal(parent.__PYVENV_LAUNCHER__, 'external');
});

test('managed Python refuses a mismatched marker ABI before rebuilding old extensions', (t) => {
  const base = tempBase(t);
  const dir = join(base, 'runtime');
  const home = join(base, 'home');
  const paths = pythonUserPaths(home);
  mkdirSync(join(dir, 'auriga/python'), { recursive: true });
  writeFileSync(
    join(dir, 'auriga/python/pip.json'),
    JSON.stringify({
      filename: 'pip.whl',
      sha256: createHash('sha256').update('wheel').digest('hex'),
    }),
  );
  writeFileSync(join(dir, 'auriga/python/pip.whl'), 'wheel');
  mkdirSync(join(dir, 'runtime/python'), { recursive: true });
  writeFileSync(
    join(dir, 'runtime/python/runtime.json'),
    JSON.stringify({ version: '3.12.14', packages: [] }),
  );
  mkdirSync(paths.site, { recursive: true });
  writeFileSync(
    join(paths.environment, 'auriga-python.json'),
    JSON.stringify({ abi: 'cp313' }),
  );
  writeFileSync(join(paths.site, 'old.pyd'), 'preserved');
  assert.throws(
    () => preparePythonUser(dir, home),
    (error) => error.reason === 'python_user.abi_mismatch',
  );
  assert.equal(readFileSync(join(paths.site, 'old.pyd'), 'utf8'), 'preserved');
  assert.deepEqual(
    JSON.parse(
      readFileSync(join(paths.environment, 'auriga-python.json'), 'utf8'),
    ),
    { abi: 'cp313' },
  );
});

test('Python diagnostics expose finite reasons and hide paths and credentials', (t) => {
  assert.equal(
    pythonUserFailureReason({ reason: 'python_user.abi_mismatch' }),
    'python_user.abi_mismatch',
  );
  assert.equal(
    pythonUserFailureReason({ code: 'EACCES', path: 'private-path' }),
    'python_user.access_denied',
  );
  assert.equal(
    pythonUserFailureReason({ reason: 'private-path credential' }),
    'python_user.unavailable',
  );
  const base = tempBase(t);
  const root = join(base, 'app');
  const home = join(base, 'home');
  install({
    root,
    dataHome: home,
    payload: buildPayload(base).dir,
    smoke: () => {},
  });
  const cli = fileURLToPath(new URL('../runtime/cli.mjs', import.meta.url));
  const result = spawnSync(
    process.execPath,
    [
      cli,
      'python',
      'configure',
      '--root',
      root,
      '--index',
      'https://alice:private-credential@example.com/simple',
    ],
    { encoding: 'utf8', windowsHide: true },
  );
  assert.equal(result.status, 1);
  assert.match(result.stderr, /\[python_user\.[a-z_]+\]/);
  assert.doesNotMatch(result.stderr, /private-credential|alice|https:|\.dev/);

  const dir = join(base, 'bad-runtime');
  mkdirSync(join(dir, 'auriga/python'), { recursive: true });
  writeFileSync(join(dir, 'auriga/python/pip.json'), '{}');
  mkdirSync(join(dir, 'runtime/python'), { recursive: true });
  writeFileSync(join(dir, 'runtime/python/runtime.json'), '{broken');
  const errors = [];
  const previous = console.error;
  console.error = (message) => errors.push(message);
  try {
    launchEnv(dir, home, { PATH: '' });
  } finally {
    console.error = previous;
  }
  assert.match(errors.join('\n'), /\[python_user\.invalid_state\]/);
  assert.doesNotMatch(errors.join('\n'), /bad-runtime|\.dev/);
});

test(
  'tray reset preserves stopped state and uses sanitized failure reasons',
  { skip: process.platform !== 'win32' },
  (t) => {
    const base = tempBase(t);
    const trayFile = fileURLToPath(
      new URL('../windows/tray/Tray.cs', import.meta.url),
    );
    const tray = readFileSync(trayFile, 'utf8');
    const reset = tray.slice(
      tray.indexOf('    void ResetPython()'),
      tray.indexOf('    void ToggleAutoDownload()'),
    );
    assert.match(
      reset,
      /bool wasRunning = launcher != null && !launcher\.HasExited/,
    );
    assert.match(reset, /if \(wasRunning\) StartLauncher\(\)/);
    assert.doesNotMatch(reset, /e\.Message|Log\([^\n]*\berror\b/);
    const harness = join(base, 'review.cs');
    const binary = join(base, 'review.exe');
    writeFileSync(
      harness,
      `class PythonReviewHarness {
    static void Main() {
      System.Console.WriteLine(Desktop.PythonFailureReason("private-path [python_user.access_denied] credential"));
      System.Console.WriteLine(Desktop.PythonFailureReason("[python_user.private_credential]"));
      System.Console.WriteLine(Desktop.PythonFailureReason(null));
    }
  }`,
    );
    const compiler = join(
      process.env.SystemRoot ?? 'C:\\Windows',
      'Microsoft.NET/Framework64/v4.0.30319/csc.exe',
    );
    execFileSync(
      compiler,
      [
        '/nologo',
        '/codepage:65001',
        '/target:exe',
        '/main:PythonReviewHarness',
        '/r:System.Windows.Forms.dll',
        '/r:System.Drawing.dll',
        '/r:System.Web.Extensions.dll',
        `/out:${binary}`,
        harness,
        trayFile,
        fileURLToPath(
          new URL('../windows/launcher/DesktopShortcut.cs', import.meta.url),
        ),
      ],
      { windowsHide: true },
    );
    assert.deepEqual(
      execFileSync(binary, [], { encoding: 'utf8', windowsHide: true })
        .trim()
        .split(/\r?\n/),
      [
        'python_user.access_denied',
        'python_user.reset_failed',
        'python_user.reset_failed',
      ],
    );
  },
);
