import test from 'node:test';
import assert from 'node:assert/strict';
import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
  symlinkSync,
} from 'node:fs';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { delimiter, dirname, join } from 'node:path';
import {
  pythonInputs,
  readPythonLock,
  validatePythonLock,
  verifyPythonInputs,
  runtimeManifest,
  assemblePython,
  downloadPythonInputs,
} from '../scripts/python-runtime.mjs';
import { smokePython } from '../scripts/python-smoke.mjs';
import { launchEnv, install, rollback, versionDir } from '../runtime/core.mjs';
import {
  lockIdentity,
  ROOT,
  sealInputs,
  verifyInputs,
} from '../../../scripts/client-release/inputs.mjs';
import { buildPayload, tempBase } from './helpers.mjs';
import { sha256File, verifyPackage } from '../runtime/verify.mjs';
import { listFiles } from '../runtime/verify.mjs';
import { protectPythonDirectory } from '../runtime/python-protection.mjs';

function smallInputsLock() {
  const lock = readPythonLock();
  for (const row of [lock.archive, ...lock.wheels, lock.pip]) {
    row.bytes = 1;
    row.sha256 = createHash('sha256').update('x').digest('hex');
  }
  return lock;
}

test('Python online download follows HTTPS redirects and refuses HTTP before sending a request', async (t) => {
  const base = tempBase(t);
  const previous = globalThis.fetch;
  t.after(() => (globalThis.fetch = previous));
  let requests = 0;
  globalThis.fetch = async (_url, options) => {
    assert.equal(options.redirect, 'manual');
    requests += 1;
    return {
      status: 302,
      headers: { get: () => 'http://example.com/insecure' },
    };
  };
  await assert.rejects(
    downloadPythonInputs(smallInputsLock(), join(base, 'bad')),
    /require HTTPS/,
  );
  assert.equal(requests, 1);
  requests = 0;
  globalThis.fetch = async (_url, options) => {
    assert.equal(options.redirect, 'manual');
    requests += 1;
    if (requests === 1)
      return {
        status: 302,
        headers: { get: () => 'https://example.com/approved-redirect' },
      };
    return { status: 200, ok: true, arrayBuffer: async () => Buffer.from('x') };
  };
  await downloadPythonInputs(smallInputsLock(), join(base, 'good'));
  assert.equal(requests, 16);
});

test('online Python caches remove corrupt archives and wheels while offline verification preserves inputs', async (t) => {
  const base = tempBase(t);
  const lock = smallInputsLock();
  const rows = pythonInputs(lock);
  for (const row of rows) {
    mkdirSync(join(base, row.directory), { recursive: true });
    writeFileSync(join(base, row.directory, row.filename), 'x');
  }
  for (const row of [rows[0], rows[1]]) {
    const path = join(base, row.directory, row.filename);
    writeFileSync(path, 'bad');
    assert.throws(() => verifyPythonInputs(lock, base), /digest mismatch/);
    assert.equal(existsSync(path), true);
    await assert.rejects(
      downloadPythonInputs(lock, base, () => {
        throw new Error('unexpected download');
      }),
      /digest mismatch/,
    );
    assert.equal(existsSync(path), false);
    writeFileSync(path, 'x');
  }
});

test('Python lock fixes install_only CPython and the complete upstream wheel set', () => {
  const lock = readPythonLock();
  assert.equal(lock.version, '3.12.14');
  assert.equal(lock.release, '20260901');
  assert.equal(lock.wheels.length, 13);
  assert.equal(pythonInputs(lock).length, 15);
  assert.ok(
    lockIdentity().locks['clients/installer/locks/python-win-x64.json'],
  );
});

test('a Python pin or wheel lock change rejects reuse of the previous devkit', (t) => {
  const base = tempBase(t);
  const source = join(base, 'source');
  const kit = join(base, 'kit');
  mkdirSync(kit);
  writeFileSync(join(kit, 'input'), 'pinned');
  for (const path of [
    ...Object.keys(lockIdentity().locks),
    'scripts/client-release/build-node.json',
  ]) {
    mkdirSync(dirname(join(source, path)), { recursive: true });
    cpSync(join(ROOT, path), join(source, path));
  }
  const sealed = sealInputs(kit, [], source);
  const path = join(source, 'clients/installer/locks/python-win-x64.json');
  const original = readFileSync(path, 'utf8');
  for (const change of [
    (l) => (l.archive.sha256 = '0'.repeat(64)),
    (l) => (l.wheels[0].sha256 = '0'.repeat(64)),
    (l) => (l.pip.sha256 = '0'.repeat(64)),
  ]) {
    const lock = JSON.parse(original);
    change(lock);
    writeFileSync(path, JSON.stringify(lock));
    assert.throws(
      () =>
        verifyInputs(kit, sealed.manifestSha256, source, { checkTools: false }),
      /依赖锁不匹配/,
    );
  }
});

test('Python pin validation rejects wrong release, hash, URL and incompatible/source wheels', () => {
  for (const change of [
    (l) => (l.release = 'latest'),
    (l) => (l.archive.sha256 = ''),
    (l) => (l.archive.url = 'https://example.com/python.tar.gz'),
    (l) => (l.wheels[0].filename = 'numpy-2.3.5.tar.gz'),
    (l) => (l.wheels[0].filename = 'numpy-2.3.5-cp313-cp313-win_amd64.whl'),
    (l) => l.wheels.push(l.wheels[0]),
    (l) => (l.pip.sha256 = ''),
    (l) => (l.pip.filename = 'pip-26.3.tar.gz'),
  ]) {
    const lock = readPythonLock();
    change(lock);
    assert.throws(
      () => validatePythonLock(lock),
      /Python pin|wheel pin|pip pin/,
    );
  }
});

test('offline Python inputs fail closed without making a network request', async (t) => {
  const base = tempBase(t);
  const previous = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = () => {
    requests += 1;
    throw new Error('network forbidden');
  };
  t.after(() => (globalThis.fetch = previous));
  assert.throws(
    () => verifyPythonInputs(readPythonLock(), base),
    /missing Python input/,
  );
  await assert.rejects(
    assemblePython({
      payload: join(base, 'payload'),
      inputs: base,
      offline: true,
    }),
    /missing Python input/,
  );
  assert.equal(requests, 0);
});

test(
  'real offline Python assembly, licenses and all aliases import pinned office packages',
  {
    skip: process.platform !== 'win32' || !process.env.AURIGA_PYTHON_INPUTS,
  },
  async (t) => {
    const payload = join(tempBase(t), 'payload');
    const previous = globalThis.fetch;
    let requests = 0;
    globalThis.fetch = () => {
      requests += 1;
      throw new Error('network forbidden');
    };
    t.after(() => (globalThis.fetch = previous));
    await assemblePython({
      payload,
      inputs: process.env.AURIGA_PYTHON_INPUTS,
      offline: true,
    });
    assert.equal(smokePython(payload).result, 'ok');
    const tempDirectory = join(dirname(payload), 'canonical-temp');
    const tempAlias = join(dirname(payload), 'temp-alias');
    mkdirSync(tempDirectory);
    symlinkSync(tempDirectory, tempAlias, 'junction');
    const previousTemp = { TEMP: process.env.TEMP, TMP: process.env.TMP };
    const errors = [];
    const previousError = console.error;
    try {
      process.env.TEMP = tempAlias;
      process.env.TMP = tempAlias;
      console.error = (message) => errors.push(message);
      assert.equal(smokePython(payload).result, 'ok');
      assert.deepEqual(
        errors,
        [],
        'smoke must prepare the personal environment under aliased TEMP',
      );
    } finally {
      console.error = previousError;
      for (const [key, value] of Object.entries(previousTemp)) {
        if (value === undefined) delete process.env[key];
        else process.env[key] = value;
      }
    }
    const runtime = join(payload, 'runtime/python');
    assert.equal(existsSync(join(runtime, 'Lib/ensurepip')), false);
    assert.equal(existsSync(join(runtime, 'py.exe')), false);
    for (const name of ['ensurepip', 'pip']) {
      const result = spawnSync(
        join(runtime, 'python.exe'),
        ['-I', '-m', name],
        { windowsHide: true, encoding: 'utf8' },
      );
      assert.notEqual(result.status, 0);
      assert.match(result.stderr, /No module named/);
    }
    const pyc = readFileSync(
      join(
        runtime,
        'Lib/site-packages/docx/__pycache__/__init__.cpython-312.pyc',
      ),
    );
    assert.equal(pyc.readUInt32LE(4), 1); // PEP 552 unchecked-hash, supplied by the fixed interpreter.
    protectPythonDirectory(payload);
    const before = listFiles(runtime);
    const unguarded = spawnSync(
      join(runtime, 'python.exe'),
      [
        '-E',
        '-c',
        'import docx, pptx, openpyxl, pandas, PIL, lxml, xlsxwriter; print("ok")',
      ],
      {
        encoding: 'utf8',
        windowsHide: true,
      },
    );
    assert.equal(unguarded.status, 0, unguarded.stderr);
    assert.deepEqual(listFiles(runtime), before);
    const write = spawnSync(
      join(runtime, 'python.exe'),
      [
        '-E',
        '-c',
        'from pathlib import Path; import sys; Path(sys.prefix,"extra.pyc").write_bytes(b"cache")',
      ],
      {
        encoding: 'utf8',
        windowsHide: true,
      },
    );
    assert.notEqual(write.status, 0);
    assert.match(write.stderr, /PermissionError/);
    assert.equal(requests, 0);
  },
);

test('Python archive and wheel digests are checked before extraction', (t) => {
  const base = tempBase(t);
  const lock = readPythonLock();
  for (const row of pythonInputs(lock)) {
    mkdirSync(join(base, row.directory), { recursive: true });
    writeFileSync(join(base, row.directory, row.filename), 'tampered');
  }
  assert.throws(
    () => verifyPythonInputs(lock, base),
    /Python input digest mismatch/,
  );
  // Keep all other checks passing so this specifically exercises the wheel check.
  const rows = pythonInputs(lock);
  for (const row of [lock.archive, ...lock.wheels, lock.pip]) {
    row.bytes = 8;
    row.sha256 = sha256File(
      join(
        base,
        rows.find((r) => r.filename === row.filename).directory,
        row.filename,
      ),
    );
  }
  lock.wheels[0].sha256 = '0'.repeat(64);
  assert.throws(
    () => verifyPythonInputs(lock, base),
    /Python input digest mismatch.*numpy/,
  );
});

test('runtime manifest carries exact source hashes, versions and licenses', () => {
  const lock = readPythonLock();
  const manifest = runtimeManifest(lock, 'a'.repeat(64));
  assert.equal(manifest.lockSha256, 'a'.repeat(64));
  assert.deepEqual(manifest.packages, lock.wheels);
  assert.equal(manifest.python.sha256, lock.archive.sha256);
  assert.ok(manifest.packages.every((p) => p.license && p.sha256));
});

test('Python environment follows installed version and rollback without mutating parent', (t) => {
  const base = tempBase(t);
  const root = join(base, 'app');
  const dataHome = join(base, 'home');
  const a = buildPayload(base, {
    name: 'a',
    pluginVersion: '1.0.0',
    pythonContent: 'python-a',
  });
  const b = buildPayload(base, {
    name: 'b',
    pluginVersion: '1.0.1',
    pythonContent: 'python-b',
  });
  const options = { root, dataHome, smoke: () => {} };
  install({ ...options, payload: a.dir });
  install({ ...options, payload: b.dir });
  const parent = {
    Path: 'system-path',
    PYTHONHOME: 'external',
    PYTHONPATH: 'external',
    pYtHoNpYcAcHePrEfIx: 'external-cache',
    PYTHONPLATLIBDIR: 'external-lib',
    PYTHONINSPECT: '1',
  };
  const env = launchEnv(versionDir(root, b.id), dataHome, parent);
  const pathKey = Object.keys(env).find((key) => key.toLowerCase() === 'path');
  const pythonRoot = join(versionDir(root, b.id), 'runtime/python');
  assert.equal(
    env[pathKey].split(delimiter)[0],
    process.platform === 'win32' ? pythonRoot : join(pythonRoot, 'bin'),
  );
  assert.equal(env.PYTHONHOME, undefined);
  assert.equal(env.PYTHONPATH, undefined);
  assert.deepEqual(
    Object.keys(env)
      .filter((key) => /^python/i.test(key))
      .sort(),
    ['PYTHONDONTWRITEBYTECODE', 'PYTHONNOUSERSITE'],
  );
  assert.equal(parent.PYTHONHOME, 'external');
  assert.equal(
    readFileSync(
      join(versionDir(root, b.id), 'runtime/python/python.exe'),
      'utf8',
    ),
    'python-b',
  );
  rollback({ ...options });
  assert.equal(
    readFileSync(
      join(versionDir(root, a.id), 'runtime/python/python.exe'),
      'utf8',
    ),
    'python-a',
  );
  writeFileSync(join(b.dir, 'runtime/python/python.exe'), 'tampered');
  assert.throws(
    () => verifyPackage(b.dir),
    /checksum mismatch: runtime\/python\/python.exe/,
  );
});
