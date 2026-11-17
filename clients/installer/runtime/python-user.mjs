/** Per-user Python state. Only packaged, verified base interpreters create it. */
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { assertLocalPath, durableJson, durableWrite } from './host-lock.mjs';
import { acquireOperationLock } from './operation-lock.mjs';
import { sha256File } from './verify.mjs';

export function pythonUserPaths(dataHome) {
  const root = join(resolve(dataHome), 'python-user');
  const environment = join(root, 'cp312');
  return {
    root,
    environment,
    bin: join(environment, 'Scripts'),
    site: join(environment, 'Lib/site-packages'),
    config: join(root, 'pip.conf'),
    constraints: join(root, 'constraints.txt'),
    settings: join(resolve(dataHome), 'auriga/python.json'),
  };
}

function indexUrl(value) {
  if (value === null) return null;
  if (typeof value !== 'string' || /[\s\\]/.test(value))
    throw new Error('Python 索引地址无效');
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error('Python 索引地址无效');
  }
  if (
    !['https:', 'http:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    /%0[ad]/i.test(value)
  )
    throw new Error('Python 索引须为无凭据、查询串及片段的 HTTP(S) 地址');
  return url.href;
}

function writeConfig(paths, index) {
  assertLocalPath(paths.root);
  assertLocalPath(paths.config);
  mkdirSync(paths.root, { recursive: true });
  durableWrite(
    paths.config,
    [
      '[global]',
      `no-index = ${index ? 'false' : 'true'}`,
      ...(index ? [`index-url = ${index}`] : []),
      'extra-index-url =',
      'find-links =',
      ...(index?.startsWith('http:')
        ? [`trusted-host = ${new URL(index).hostname}`]
        : []),
      'retries = 0',
      'timeout = 5',
      'disable-pip-version-check = true',
      'no-input = true',
      'only-binary = :all:',
      '[install]',
      'user = false',
      `prefix = ${paths.environment}`,
      'target =',
      'extra-index-url =',
      'find-links =',
      '',
    ].join('\n'),
  );
}

export function configurePythonIndex(dataHome, value) {
  const index = indexUrl(value);
  const paths = pythonUserPaths(dataHome);
  assertLocalPath(paths.root);
  const release = acquireOperationLock(join(paths.root, 'prepare.lock'));
  try {
    assertLocalPath(paths.settings);
    mkdirSync(dirname(paths.settings), { recursive: true });
    durableJson(paths.settings, { schema: 1, indexUrl: index });
    writeConfig(paths, index);
    return { configured: Boolean(index) };
  } finally {
    release();
  }
}

function configuredIndex(paths) {
  assertLocalPath(paths.settings);
  if (!existsSync(paths.settings)) return null;
  const settings = JSON.parse(readFileSync(paths.settings, 'utf8'));
  if (settings.schema !== 1) throw new Error('Python 索引设置版本无效');
  return indexUrl(settings.indexUrl);
}

function run(python, args, env) {
  const result = spawnSync(python, args, {
    env,
    windowsHide: true,
    encoding: 'utf8',
    timeout: 60000,
  });
  if (result.status !== 0)
    throw new Error(
      '个人 Python 环境创建失败，请在托盘设置中重置个人 Python 环境',
    );
}

/** No inherited Python/pip settings can redirect a managed install. */
export function cleanPythonEnv(base) {
  return Object.fromEntries(
    Object.entries(base).filter(([key]) => !/^(python|pip_)/i.test(key)),
  );
}

export function preparePythonUser(dir, dataHome, base = process.env) {
  const bootstrap = join(dir, 'auriga/python');
  if (!existsSync(join(bootstrap, 'pip.json'))) return null;
  const paths = pythonUserPaths(dataHome);
  assertLocalPath(paths.root);
  const release = acquireOperationLock(join(paths.root, 'prepare.lock'));
  try {
    assertLocalPath(paths.environment);
    assertLocalPath(paths.site);
    const pip = JSON.parse(readFileSync(join(bootstrap, 'pip.json'), 'utf8'));
    const wheel = join(bootstrap, pip.filename);
    if (sha256File(wheel) !== pip.sha256) throw new Error('pip wheel 校验失败');
    const python = join(dir, 'runtime/python/python.exe');
    const marker = join(paths.environment, 'auriga-python.json');
    assertLocalPath(marker);
    let previous;
    try {
      previous = JSON.parse(readFileSync(marker, 'utf8'));
    } catch {
      /* Recreate incomplete state. */
    }
    const identity = { runtime: resolve(dir), pipSha256: pip.sha256 };
    const env = { ...cleanPythonEnv(base), PYTHONDONTWRITEBYTECODE: '1' };
    if (
      JSON.stringify(previous) !== JSON.stringify(identity) ||
      !existsSync(join(paths.bin, 'python.exe'))
    ) {
      for (const file of [
        'pyvenv.cfg',
        'Scripts/python.exe',
        'Scripts/python3.exe',
        'Scripts/pythonw.exe',
      ])
        assertLocalPath(join(paths.environment, file));
      run(
        python,
        [
          '-I',
          '-B',
          '-m',
          'venv',
          '--without-pip',
          '--system-site-packages',
          paths.environment,
        ],
        env,
      );
      cpSync(join(paths.bin, 'python.exe'), join(paths.bin, 'python3.exe'));
      run(
        python,
        [
          '-I',
          '-S',
          '-B',
          join(bootstrap, 'bootstrap-pip.py'),
          wheel,
          paths.environment,
          pip.sha256,
        ],
        env,
      );
      durableJson(marker, identity);
    }
    run(
      join(paths.bin, 'python.exe'),
      ['-I', '-B', '-c', 'import sys; assert sys.prefix != sys.base_prefix'],
      env,
    );
    // A damaged pip does not block launching DSH or importing the built-in packages.
    const index = configuredIndex(paths);
    writeConfig(paths, index);
    assertLocalPath(paths.constraints);
    const manifest = JSON.parse(
      readFileSync(join(dir, 'runtime/python/runtime.json'), 'utf8'),
    );
    durableWrite(
      paths.constraints,
      [
        ...manifest.packages.map((p) => `${p.name}==${p.version}`),
        `pip==${pip.version}`,
        '',
      ].join('\n'),
    );
    return { ...paths, index };
  } finally {
    release();
  }
}

export function resetPythonUser(dataHome) {
  const paths = pythonUserPaths(dataHome);
  assertLocalPath(paths.root);
  const release = acquireOperationLock(join(paths.root, 'prepare.lock'));
  try {
    const target = resolve(paths.environment);
    if (!target.startsWith(resolve(paths.root) + sep))
      throw new Error('个人 Python 环境路径无效');
    assertLocalPath(target);
    rmSync(target, { recursive: true, force: true });
    writeConfig(paths, configuredIndex(paths));
    return { reset: true };
  } finally {
    release();
  }
}
