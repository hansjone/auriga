import test from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  renameSync,
  statSync,
  symlinkSync,
} from 'node:fs';
import { execFile, spawnSync } from 'node:child_process';
import { promisify } from 'node:util';
import { performance } from 'node:perf_hooks';
import { createServer } from 'node:http';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { tempBase, buildPayload } from './helpers.mjs';
import { assemblePython, readPythonLock } from '../scripts/python-runtime.mjs';
import {
  launchEnv,
  install,
  rollback,
  pythonCommand,
  openVersionSession,
} from '../runtime/core.mjs';
import {
  listFiles,
  sha256File,
  computeId,
  verifyPackage,
  isPinnedPath,
} from '../runtime/verify.mjs';
import { protectPythonDirectory } from '../runtime/python-protection.mjs';
import {
  configurePythonIndex,
  preparePythonUser,
  pythonUserPaths,
  resetPythonUser,
} from '../runtime/python-user.mjs';

test('personal Python index writes only its pip.conf and defaults to offline', (t) => {
  const home = join(tempBase(t), 'home');
  const paths = pythonUserPaths(home);
  configurePythonIndex(home, null);
  assert.match(readFileSync(paths.config, 'utf8'), /no-index = true/);
  configurePythonIndex(
    home,
    'https://forgejo.example.com/api/packages/owner/pypi/simple',
  );
  const text = readFileSync(paths.config, 'utf8');
  assert.match(text, /no-index = false/);
  assert.match(text, /index-url = https:\/\/forgejo.example.com/);
  assert.match(text, /retries = 0/);
  assert.match(text, /timeout = 5/);
  assert.equal(
    JSON.parse(readFileSync(paths.settings, 'utf8')).indexUrl,
    'https://forgejo.example.com/api/packages/owner/pypi/simple',
  );
  for (const bad of [
    'https://user:secret@example.com/simple',
    'file:///C:/wheels',
    'https://example.com/\n[install]',
    'https://example.com/?secret=x',
  ]) {
    assert.throws(() => configurePythonIndex(home, bad), /索引/);
  }
});

test('personal Python reset deletes only the environment and keeps index settings', (t) => {
  const home = join(tempBase(t), 'home');
  const paths = pythonUserPaths(home);
  configurePythonIndex(home, 'https://forgejo.example.com/simple');
  mkdirSync(paths.site, { recursive: true });
  writeFileSync(join(paths.site, 'broken.py'), 'broken');
  resetPythonUser(home);
  assert.equal(existsSync(paths.environment), false);
  assert.match(readFileSync(paths.config, 'utf8'), /forgejo.example.com/);
  assert.equal(preparePythonUser(join(home, 'absent-runtime'), home), null);
});

test('installer upgrade and rollback retain personal packages and index settings', (t) => {
  const base = tempBase(t);
  const root = join(base, 'app');
  const home = join(base, 'home');
  const options = { root, dataHome: home, smoke: () => {} };
  install({ ...options, payload: buildPayload(base, { name: 'a' }).dir });
  const paths = pythonUserPaths(home);
  configurePythonIndex(home, 'https://forgejo.example.com/simple');
  mkdirSync(paths.site, { recursive: true });
  writeFileSync(join(paths.site, 'personal.py'), 'retained');
  install({
    ...options,
    payload: buildPayload(base, { name: 'b', pluginVersion: '1.0.1' }).dir,
  });
  rollback(options);
  assert.equal(
    readFileSync(join(paths.site, 'personal.py'), 'utf8'),
    'retained',
  );
  assert.match(readFileSync(paths.settings, 'utf8'), /forgejo.example.com/);
});

const execute = promisify(execFile);

test('Python settings CLI uses installation locks and unsupported reset preserves packages', (t) => {
  const base = tempBase(t);
  const root = join(base, 'app');
  const home = join(base, 'home');
  install({
    root,
    dataHome: home,
    payload: buildPayload(base).dir,
    smoke: () => {},
  });
  const result = spawnSync(
    process.execPath,
    [
      fileURLToPath(new URL('../runtime/cli.mjs', import.meta.url)),
      'python',
      'configure',
      '--root',
      root,
      '--index',
      'https://forgejo.example.com/simple',
    ],
    { windowsHide: true, encoding: 'utf8' },
  );
  assert.equal(result.status, 0, result.stderr);
  assert.equal(JSON.parse(result.stdout).configured, true);
  const paths = pythonUserPaths(home);
  mkdirSync(paths.site, { recursive: true });
  writeFileSync(join(paths.site, 'keep.py'), 'retained');
  assert.throws(
    () => pythonCommand(root, 'reset'),
    /此版本未提供个人 Python 环境/,
  );
  assert.equal(readFileSync(join(paths.site, 'keep.py'), 'utf8'), 'retained');
  const session = openVersionSession(root, { launching: true });
  try {
    assert.throws(
      () => pythonCommand(root, 'configure', { index: null }),
      (error) => error.code === 'desktop.in_use',
    );
  } finally {
    session.close();
  }
});

test('personal Python rejects a corrupt bootstrap wheel before creating its venv', (t) => {
  const base = tempBase(t);
  const dir = join(base, 'payload');
  mkdirSync(join(dir, 'auriga/python'), { recursive: true });
  mkdirSync(join(dir, 'runtime/python'), { recursive: true });
  writeFileSync(
    join(dir, 'runtime/python/runtime.json'),
    JSON.stringify({ version: '3.12.14', packages: [] }),
  );
  writeFileSync(
    join(dir, 'auriga/python/pip.json'),
    JSON.stringify({ filename: 'pip.whl', sha256: '0'.repeat(64) }),
  );
  writeFileSync(join(dir, 'auriga/python/pip.whl'), 'corrupt');
  const home = join(base, 'home');
  assert.throws(() => preparePythonUser(dir, home), /pip wheel 校验失败/);
  assert.equal(existsSync(pythonUserPaths(home).environment), false);
});

test('reset refuses a linked personal environment and preserves its target', (t) => {
  const base = tempBase(t);
  const home = join(base, 'home');
  const paths = pythonUserPaths(home);
  const foreign = join(base, 'foreign');
  mkdirSync(foreign);
  writeFileSync(join(foreign, 'keep'), 'retained');
  mkdirSync(paths.root, { recursive: true });
  symlinkSync(
    foreign,
    paths.environment,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  assert.throws(() => resetPythonUser(home), /path contains a link/);
  assert.equal(readFileSync(join(foreign, 'keep'), 'utf8'), 'retained');
});

function sealFixture(payload) {
  const manifest = payload.manifest;
  manifest.files = listFiles(payload.dir)
    .filter((p) => p !== 'manifest.json' && !isPinnedPath(p))
    .map((p) => ({
      path: p,
      size: statSync(join(payload.dir, p)).size,
      sha256: sha256File(join(payload.dir, p)),
    }));
  manifest.id = computeId(manifest);
  writeFileSync(join(payload.dir, 'manifest.json'), JSON.stringify(manifest));
}

test(
  'real managed venv installs offline and from a configured index while preserving the base',
  {
    skip: process.platform !== 'win32' || !process.env.AURIGA_PYTHON_INPUTS,
    timeout: 600000,
  },
  async (t) => {
    const base = tempBase(t);
    const payload = buildPayload(base);
    const home = join(base, 'home');
    await assemblePython({
      payload: payload.dir,
      inputs: process.env.AURIGA_PYTHON_INPUTS,
      offline: true,
    });
    sealFixture(payload);
    verifyPackage(payload.dir);
    const runtime = join(payload.dir, 'runtime/python');
    protectPythonDirectory(payload.dir);
    const before = listFiles(runtime).map((p) => [
      p,
      sha256File(join(runtime, p)),
    ]);
    const paths = pythonUserPaths(home);
    const appData = join(base, 'appdata');
    mkdirSync(join(appData, 'pip'), { recursive: true });
    writeFileSync(
      join(appData, 'pip/pip.ini'),
      `[install]\ntarget = ${join(base, 'forbidden-target')}\n`,
    );
    const parent = {
      ...process.env,
      APPDATA: appData,
      PYTHONHOME: 'external',
      PYTHONPATH: 'external',
      PIP_TARGET: join(base, 'forbidden-target'),
      PIP_INDEX_URL: 'https://example.com/unused',
      PIP_USER: '1',
    };
    let env = launchEnv(payload.dir, home, parent);
    const python = join(paths.bin, 'python.exe');
    assert.equal(existsSync(python), true);
    assert.equal(env.PYTHONHOME, undefined);
    assert.equal(env.PIP_TARGET, undefined);
    assert.equal(parent.PIP_USER, '1');
    const run = (args) =>
      execute(python, args, {
        env,
        windowsHide: true,
        timeout: 15000,
        maxBuffer: 1024 * 1024,
      });
    const imports =
      'import sys, pip, docx, pptx, openpyxl, pandas, PIL, lxml, xlsxwriter; assert sys.prefix != sys.base_prefix; print(pip.__version__)';
    assert.equal((await run(['-c', imports])).stdout.trim(), '26.3');
    assert.equal(
      JSON.parse(
        readFileSync(join(paths.environment, 'auriga-python.json'), 'utf8'),
      ).abi,
      'cp312',
    );
    const pthPackages = join(paths.environment, 'pth-packages');
    mkdirSync(pthPackages);
    writeFileSync(
      join(pthPackages, 'docx.py'),
      'raise RuntimeError("personal .pth shadowed builtin")',
    );
    for (const line of [
      pthPackages,
      `import sys; sys.path.insert(0, ${JSON.stringify(pthPackages)})`,
    ]) {
      writeFileSync(join(paths.site, 'collision.pth'), line + '\n');
      assert.ok(
        (
          await run(['-c', 'import docx; print(docx.__file__)'])
        ).stdout.includes(runtime),
      );
    }
    assert.equal(
      (
        await execute(join(paths.bin, 'python3.exe'), ['-c', imports], {
          env,
          windowsHide: true,
        })
      ).stdout.trim(),
      '26.3',
    );
    const basePip = spawnSync(
      join(runtime, 'python.exe'),
      ['-I', '-m', 'pip'],
      { windowsHide: true, encoding: 'utf8' },
    );
    assert.notEqual(basePip.status, 0);
    assert.match(basePip.stderr, /No module named pip/);
    assert.equal(existsSync(join(runtime, 'Lib/ensurepip')), false);

    const wheels = join(base, 'wheels');
    mkdirSync(wheels);
    const docxVersion = readPythonLock().wheels.find(
      (w) => w.name === 'python-docx',
    ).version;
    const source = String.raw`import pathlib, zipfile, sys
root=pathlib.Path(sys.argv[1])
for name, version, module in [('auriga_fixture', '1.0', 'auriga_fixture'), ('python_docx', '${docxVersion}', 'docx'), ('python_docx', '0.0.1', 'docx')]:
    info=f'{name}-{version}.dist-info'
    with zipfile.ZipFile(root / f'{name}-{version}-py3-none-any.whl', 'w') as z:
        z.writestr(f'{module}/__init__.py', 'VALUE = "personal"')
        z.writestr(info+'/METADATA', f'Metadata-Version: 2.1\nName: {name}\nVersion: {version}\n')
        z.writestr(info+'/WHEEL', 'Wheel-Version: 1.0\nGenerator: fixture\nRoot-Is-Purelib: true\nTag: py3-none-any\n')
        z.writestr(info+'/RECORD', '')
`;
    await execute(
      join(runtime, 'python.exe'),
      ['-I', '-B', '-c', source, wheels],
      { windowsHide: true },
    );
    const wheel = join(wheels, 'auriga_fixture-1.0-py3-none-any.whl');
    await run(['-m', 'pip', 'install', wheel]);
    assert.match(
      (
        await run([
          '-c',
          'import auriga_fixture; print(auriga_fixture.VALUE, auriga_fixture.__file__)',
        ])
      ).stdout,
      /personal.*python-user/,
    );
    assert.equal(existsSync(join(base, 'forbidden-target')), false);

    // The packaged library wins even when a personal wheel uses its import name.
    await run([
      '-m',
      'pip',
      'install',
      '--ignore-installed',
      '--no-deps',
      join(wheels, `python_docx-${docxVersion}-py3-none-any.whl`),
    ]);
    assert.ok(
      (await run(['-c', 'import docx; print(docx.__file__)'])).stdout.includes(
        runtime,
      ),
    );
    await assert.rejects(
      run([
        '-m',
        'pip',
        'install',
        join(wheels, 'python_docx-0.0.1-py3-none-any.whl'),
      ]),
      (e) => /ResolutionImpossible|conflicting dependencies/.test(e.stderr),
    );
    const uninstall = await run([
      '-m',
      'pip',
      'uninstall',
      '-y',
      'python-docx',
    ]);
    assert.match(
      uninstall.stdout,
      /outside environment|No files were found to uninstall/,
    );
    assert.ok(
      (await run(['-c', 'import docx; print(docx.__file__)'])).stdout.includes(
        runtime,
      ),
    );

    const start = performance.now();
    await assert.rejects(
      run(['-m', 'pip', 'install', 'auriga-missing-package']),
      (e) => /No matching distribution/.test(e.stderr),
    );
    assert.ok(
      performance.now() - start < 10000,
      'unconfigured index must fail promptly',
    );

    let requests = 0;
    const server = createServer((req, res) => {
      requests += 1;
      if (req.url === '/simple/auriga-fixture/') {
        res.setHeader('Content-Type', 'text/html');
        res.end('<a href="/auriga_fixture-1.0-py3-none-any.whl">fixture</a>');
      } else if (req.url === '/auriga_fixture-1.0-py3-none-any.whl') {
        res.end(readFileSync(wheel));
      } else {
        res.writeHead(404);
        res.end();
      }
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    t.after(() => new Promise((resolve) => server.close(resolve)));
    configurePythonIndex(
      home,
      `http://127.0.0.1:${server.address().port}/simple`,
    );
    env = launchEnv(payload.dir, home, parent);
    await run([
      '-m',
      'pip',
      'install',
      '--force-reinstall',
      'auriga-fixture==1.0',
    ]);
    assert.ok(requests >= 2);

    const moved = join(base, 'payload-next');
    renameSync(payload.dir, moved);
    env = launchEnv(moved, home, parent);
    assert.equal(
      (
        await run(['-c', 'import auriga_fixture; print(auriga_fixture.VALUE)'])
      ).stdout.trim(),
      'personal',
    );
    assert.match(
      readFileSync(join(paths.environment, 'pyvenv.cfg'), 'utf8'),
      /payload-next/,
    );
    renameSync(moved, payload.dir);
    env = launchEnv(payload.dir, home, parent);
    assert.equal(
      (
        await run(['-c', 'import auriga_fixture; print(auriga_fixture.VALUE)'])
      ).stdout.trim(),
      'personal',
    );
    writeFileSync(join(paths.site, 'pip/__init__.py'), 'broken syntax !');
    assert.equal(
      (await run(['-c', 'import docx; print("office ok")'])).stdout.trim(),
      'office ok',
    );
    writeFileSync(python, 'broken executable');
    const fallback = launchEnv(payload.dir, home, parent);
    assert.equal(fallback.PIP_CONFIG_FILE, undefined);
    assert.equal(
      (
        await execute('python', ['-c', 'import docx; print("fallback ok")'], {
          env: fallback,
          windowsHide: true,
        })
      ).stdout.trim(),
      'fallback ok',
    );
    resetPythonUser(home);
    env = launchEnv(payload.dir, home, parent);
    assert.equal(
      (await run(['-m', 'pip', '--version'])).stdout.startsWith('pip 26.3'),
      true,
    );
    assert.equal(existsSync(join(paths.site, 'auriga_fixture')), false);
    assert.match(readFileSync(paths.config, 'utf8'), /127.0.0.1/);
    assert.deepEqual(
      listFiles(runtime).map((p) => [p, sha256File(join(runtime, p))]),
      before,
    );
    verifyPackage(payload.dir);
  },
);
