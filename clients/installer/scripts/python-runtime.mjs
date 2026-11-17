/** Fixed, wheel-only Windows Python. No resolver, pip, build backend or install hook. */
import { spawnSync } from 'node:child_process';
import {
  cpSync,
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { listFiles, sha256File } from '../runtime/verify.mjs';

const here = dirname(fileURLToPath(import.meta.url));
export const PYTHON_LOCK = join(here, '../locks/python-win-x64.json');
const HASH = /^[a-f0-9]{64}$/;
const normalized = (name) => name.toLowerCase().replace(/[-_.]+/g, '_');

export function validatePythonLock(lock) {
  if (!lock || typeof lock !== 'object') throw new Error('invalid Python pin');
  const filename = `cpython-${lock.version}+${lock.release}-x86_64-pc-windows-msvc-install_only.tar.gz`;
  const archive = lock.archive;
  if (
    lock.schema !== 1 ||
    lock.implementation !== 'CPython' ||
    !/^3\.12\.\d+$/.test(lock.version) ||
    !/^\d{8}$/.test(lock.release) ||
    lock.target !== 'win-x64-cp312' ||
    !archive ||
    archive.filename !== filename ||
    archive.url !==
      `https://github.com/astral-sh/python-build-standalone/releases/download/${lock.release}/${filename.replace('+', '%2B')}` ||
    !HASH.test(archive.sha256) ||
    !Number.isSafeInteger(archive.bytes) ||
    archive.bytes <= 0 ||
    !archive.license ||
    !Array.isArray(lock.wheels) ||
    !lock.wheels.length
  )
    throw new Error('invalid Python pin');
  const seen = new Set();
  for (const wheel of lock.wheels) {
    if (!wheel || typeof wheel !== 'object')
      throw new Error('invalid wheel pin');
    const name = normalized(wheel.name ?? '');
    const prefix = `${name}-${wheel.version}-`;
    const tags = wheel.filename?.slice(prefix.length);
    if (
      !/^[a-z0-9_]+$/.test(name) ||
      seen.has(name) ||
      !/^\d+(?:\.\d+)*(?:\.post\d+)?$/.test(wheel.version) ||
      !wheel.filename?.startsWith(prefix) ||
      !/^(?:cp312-cp312-win_amd64|py3-none-any|py2\.py3-none-any)\.whl$/.test(
        tags,
      ) ||
      !wheel.url?.startsWith('https://files.pythonhosted.org/packages/') ||
      new URL(wheel.url).pathname.split('/').pop() !== wheel.filename ||
      !HASH.test(wheel.sha256) ||
      !Number.isSafeInteger(wheel.bytes) ||
      wheel.bytes <= 0 ||
      typeof wheel.license !== 'string' ||
      !wheel.license
    )
      throw new Error(`invalid wheel pin: ${wheel.name}`);
    seen.add(name);
  }
  const pip = lock.pip;
  if (
    !pip ||
    pip.name !== 'pip' ||
    pip.version !== '26.3' ||
    pip.filename !== `pip-${pip.version}-py3-none-any.whl` ||
    !pip.url?.startsWith('https://files.pythonhosted.org/packages/') ||
    new URL(pip.url).pathname.split('/').pop() !== pip.filename ||
    !HASH.test(pip.sha256) ||
    !Number.isSafeInteger(pip.bytes) ||
    pip.bytes <= 0 ||
    pip.license !== 'MIT'
  )
    throw new Error('invalid pip pin');
  return lock;
}

export function readPythonLock(file = PYTHON_LOCK) {
  return validatePythonLock(JSON.parse(readFileSync(file, 'utf8')));
}

export function pythonInputs(lock) {
  validatePythonLock(lock);
  return [
    { ...lock.archive, directory: 'python' },
    ...lock.wheels.map((wheel) => ({ ...wheel, directory: 'python/wheels' })),
    { ...lock.pip, directory: 'python/pip' },
  ];
}

export function verifyPythonInputs(lock, directory) {
  const rows = pythonInputs(lock);
  for (const row of rows) {
    const path = join(directory, row.directory, row.filename);
    if (!existsSync(path))
      throw new Error(`missing Python input: ${row.filename}`);
    const info = lstatSync(path);
    if (
      !info.isFile() ||
      info.size !== row.bytes ||
      sha256File(path) !== row.sha256
    )
      throw new Error(`Python input digest mismatch: ${row.filename}`);
  }
  return rows;
}

async function fetchFile(url, path) {
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    if (new URL(url).protocol !== 'https:')
      throw new Error('Python downloads require HTTPS');
    const response = await globalThis.fetch(url, { redirect: 'manual' });
    if ([301, 302, 303, 307, 308].includes(response.status)) {
      await response.body?.cancel();
      const location = response.headers.get('location');
      if (!location)
        throw new Error('Python download redirect has no location');
      url = new URL(location, url).href;
      continue;
    }
    if (!response.ok)
      throw new Error(`Python download failed: ${response.status}`);
    writeFileSync(path, Buffer.from(await response.arrayBuffer()), {
      flag: 'wx',
    });
    return;
  }
  throw new Error('Too many Python download redirects');
}

/** Called only on connected preparation/build paths; offline code never calls this. */
export async function downloadPythonInputs(
  lock,
  directory,
  download = fetchFile,
) {
  for (const row of pythonInputs(lock)) {
    const path = join(directory, row.directory, row.filename);
    mkdirSync(dirname(path), { recursive: true });
    if (!existsSync(path)) await download(row.url, path);
    const info = lstatSync(path);
    if (
      !info.isFile() ||
      info.size !== row.bytes ||
      sha256File(path) !== row.sha256
    ) {
      // Online cache entries can be fetched again; offline verification never mutates inputs.
      if (info.isFile()) rmSync(path);
      throw new Error(`Python input digest mismatch: ${row.filename}`);
    }
  }
  verifyPythonInputs(lock, directory);
}

export function runtimeManifest(lock, lockSha256, installed = []) {
  return {
    schema: 1,
    implementation: lock.implementation,
    version: lock.version,
    release: lock.release,
    target: lock.target,
    lockSha256,
    python: { ...lock.archive, licenses: ['LICENSE.txt'] },
    packages: lock.wheels,
    installed,
  };
}

function run(command, args) {
  const result = spawnSync(command, args, {
    encoding: 'utf8',
    windowsHide: true,
    timeout: 180000,
    maxBuffer: 32 * 1024 * 1024,
  });
  if (result.status !== 0)
    throw new Error(
      `Python assembly failed (${result.status}): ${result.stderr || result.error || result.stdout}`,
    );
  return result.stdout;
}

export async function assemblePython({
  payload,
  inputs,
  offline = false,
  lockFile = PYTHON_LOCK,
}) {
  const lock = readPythonLock(lockFile);
  if (!offline) await downloadPythonInputs(lock, inputs);
  verifyPythonInputs(lock, inputs); // Every byte checked before invoking any runtime.
  const directory = join(payload, 'runtime/python');
  const staging = join(payload, 'runtime/python-extract');
  if (existsSync(directory) || existsSync(staging))
    throw new Error(
      'Python assembly requires a fresh payload runtime directory',
    );
  mkdirSync(staging, { recursive: true });
  const tar =
    process.platform === 'win32'
      ? join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/tar.exe')
      : 'tar';
  try {
    run(tar, [
      '-xf',
      join(inputs, 'python', lock.archive.filename),
      '-C',
      staging,
    ]);
    cpSync(join(staging, 'python'), directory, { recursive: true });
  } finally {
    rmSync(staging, { recursive: true });
  }
  // install_only includes build/debug artifacts and pip. Ship exactly our wheel set.
  for (const path of [
    'include',
    'libs',
    'Scripts',
    'Lib/site-packages',
    'Lib/ensurepip',
  ])
    rmSync(join(directory, path), { recursive: true, force: true });
  for (const path of listFiles(directory))
    if (path.endsWith('.pdb') || path.endsWith('.pyc'))
      rmSync(join(directory, path));
  const python = join(directory, 'python.exe');
  const bootstrap = join(payload, 'auriga/python');
  mkdirSync(bootstrap, { recursive: true });
  cpSync(
    join(inputs, 'python/pip', lock.pip.filename),
    join(bootstrap, lock.pip.filename),
  );
  writeFileSync(
    join(bootstrap, 'pip.json'),
    JSON.stringify(lock.pip, null, 2) + '\n',
  );
  cpSync(
    join(here, 'lib/bootstrap-pip.py'),
    join(bootstrap, 'bootstrap-pip.py'),
  );
  cpSync(
    join(here, 'lib/python-sitecustomize.py'),
    join(directory, 'Lib/sitecustomize.py'),
  );
  const installed = JSON.parse(
    run(python, [
      '-I',
      '-S',
      '-B',
      join(here, 'lib/install-wheels.py'),
      lockFile,
      join(inputs, 'python/wheels'),
      directory,
    ]),
  );
  // Real executables also work in bash and direct subprocess calls (unlike .cmd aliases).
  cpSync(python, join(directory, 'python3.exe'));
  run(python, [
    '-I',
    '-S',
    '-B',
    '-m',
    'compileall',
    '--invalidation-mode',
    'unchecked-hash',
    '-q',
    '-f',
    '-d',
    'auriga-python/Lib',
    join(directory, 'Lib'),
  ]);
  const manifest = runtimeManifest(lock, sha256File(lockFile), installed);
  writeFileSync(
    join(directory, 'runtime.json'),
    JSON.stringify(manifest, null, 2) + '\n',
  );
  return manifest;
}
