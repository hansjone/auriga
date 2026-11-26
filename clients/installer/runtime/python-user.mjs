/** Per-user Python state. Only packaged, verified base interpreters create it. */
import { spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { assertLocalPath, durableJson, durableWrite } from './host-lock.mjs';
import { acquireOperationLock } from './operation-lock.mjs';
import { sha256File } from './verify.mjs';

const PYTHON_ABI = 'cp312';
const PREPARE_STAGES = [
  'bootstrap_path',
  'runtime_manifest',
  'root_path',
  'prepare_lock',
  'environment_path',
  'pip_wheel',
  'marker_read',
  'venv_paths',
  'venv_create',
  'alias_copy',
  'pip_bootstrap',
  'marker_write',
  'interpreter_probe',
  'index_config',
  'constraints',
];

export function pythonUserFailureStage(error) {
  return PREPARE_STAGES.includes(error?.pythonUserStage)
    ? error.pythonUserStage
    : 'unknown';
}

/** A finite diagnostic vocabulary prevents paths or subprocess output entering logs. */
export function pythonUserFailureReason(error) {
  if (
    [
      'python_user.abi_mismatch',
      'python_user.bootstrap_digest_mismatch',
      'python_user.environment_failed',
      'python_user.timeout',
    ].includes(error?.reason)
  )
    return error.reason;
  if (['EACCES', 'EPERM'].includes(error?.code))
    return 'python_user.access_denied';
  if (error?.code === 'ENOENT') return 'python_user.file_missing';
  if (error?.code === 'desktop.in_use') return 'python_user.busy';
  if (error?.message === 'guardian path alias') return 'python_user.path_alias';
  if (error?.message === 'guardian path contains a link')
    return 'python_user.path_link';
  if (['ERR_INVALID_ARG_TYPE', 'ERR_INVALID_ARG_VALUE'].includes(error?.code))
    return 'python_user.invalid_argument';
  if (error instanceof SyntaxError) return 'python_user.invalid_state';
  return 'python_user.unavailable';
}

export function pythonUserPaths(dataHome) {
  const root = join(resolve(dataHome), 'python-user');
  const environment = join(root, PYTHON_ABI);
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

function writeIfChanged(file, content) {
  try {
    if (existsSync(file) && readFileSync(file, 'utf8') === content) return;
  } catch {
    /* Rewrite unreadable file. */
  }
  durableWrite(file, content);
}

function writeConfig(paths, index) {
  assertLocalPath(paths.root);
  assertLocalPath(paths.config);
  mkdirSync(paths.root, { recursive: true });
  writeIfChanged(
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

function retargetVenvConfig(pyvenvCfg, pythonDir, pythonExe) {
  if (!existsSync(pyvenvCfg)) return false;
  const raw = readFileSync(pyvenvCfg, 'utf8');
  if (!/^home\s*=/im.test(raw)) return false;
  let updated = raw.replace(/^home\s*=.*$/im, `home = ${pythonDir}`);
  if (/^executable\s*=/im.test(updated))
    updated = updated.replace(
      /^executable\s*=.*$/im,
      `executable = ${pythonExe}`,
    );
  if (updated !== raw) durableWrite(pyvenvCfg, updated);
  return true;
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
    throw Object.assign(
      new Error('个人 Python 环境创建失败，请在托盘设置中重置个人 Python 环境'),
      {
        reason:
          result.error?.code === 'ETIMEDOUT'
            ? 'python_user.timeout'
            : 'python_user.environment_failed',
      },
    );
}

/** No inherited Python/pip settings can redirect a managed install. */
export function cleanPythonEnv(base) {
  return Object.fromEntries(
    Object.entries(base).filter(
      ([key]) =>
        !/^(python|pip_)/i.test(key) &&
        key.toUpperCase() !== '__PYVENV_LAUNCHER__',
    ),
  );
}

export function preparePythonUser(dir, dataHome, base = process.env) {
  let stage = 'bootstrap_path';
  let release;
  try {
    const bootstrap = join(dir, 'auriga/python');
    if (!existsSync(join(bootstrap, 'pip.json'))) return null;
    stage = 'runtime_manifest';
    const manifest = JSON.parse(
      readFileSync(join(dir, 'runtime/python/runtime.json'), 'utf8'),
    );
    const versionParts = /^(\d+)\.(\d+)\.\d+$/.exec(manifest.version);
    if (
      !versionParts ||
      `cp${versionParts[1]}${versionParts[2]}` !== PYTHON_ABI
    )
      throw Object.assign(
        new Error('个人 Python 环境 ABI 不兼容，请使用匹配版本的客户端'),
        { reason: 'python_user.abi_mismatch' },
      );
    const paths = pythonUserPaths(dataHome);
    stage = 'root_path';
    assertLocalPath(paths.root);
    stage = 'prepare_lock';
    release = acquireOperationLock(join(paths.root, 'prepare.lock'));
    stage = 'environment_path';
    assertLocalPath(paths.environment);
    assertLocalPath(paths.site);
    stage = 'pip_wheel';
    const pip = JSON.parse(readFileSync(join(bootstrap, 'pip.json'), 'utf8'));
    const wheel = join(bootstrap, pip.filename);
    const pythonDir = join(dir, 'runtime/python');
    const python = join(pythonDir, 'python.exe');
    stage = 'marker_read';
    const marker = join(paths.environment, 'auriga-python.json');
    assertLocalPath(marker);
    let previous;
    try {
      previous = JSON.parse(readFileSync(marker, 'utf8'));
    } catch {
      /* Recreate incomplete state. */
    }
    if (previous?.abi && previous.abi !== PYTHON_ABI)
      throw Object.assign(
        new Error('个人 Python 环境 ABI 不兼容，请重置后重试'),
        { reason: 'python_user.abi_mismatch' },
      );
    const identity = {
      runtime: resolve(dir),
      pipSha256: pip.sha256,
      abi: PYTHON_ABI,
    };
    const env = { ...cleanPythonEnv(base), PYTHONDONTWRITEBYTECODE: '1' };
    const envReady = existsSync(join(paths.bin, 'python.exe'));
    const exactMatch =
      envReady && JSON.stringify(previous) === JSON.stringify(identity);
    let needsProbe = !exactMatch;
    if (!exactMatch) {
      // Fast path on version upgrade: when ABI and pip wheel are unchanged,
      // retarget pyvenv.cfg to the new bundled Python instead of recreating the venv.
      const canRetarget =
        envReady &&
        previous?.abi === PYTHON_ABI &&
        previous?.pipSha256 === pip.sha256 &&
        existsSync(join(paths.environment, 'pyvenv.cfg'));
      let retargeted = false;
      if (canRetarget) {
        stage = 'venv_paths';
        assertLocalPath(join(paths.environment, 'pyvenv.cfg'));
        retargeted = retargetVenvConfig(
          join(paths.environment, 'pyvenv.cfg'),
          pythonDir,
          python,
        );
        if (retargeted) {
          stage = 'marker_write';
          durableJson(marker, identity);
        }
      }
      if (!retargeted) {
        stage = 'pip_wheel';
        if (sha256File(wheel) !== pip.sha256)
          throw Object.assign(new Error('pip wheel 校验失败'), {
            reason: 'python_user.bootstrap_digest_mismatch',
          });
        stage = 'venv_paths';
        for (const file of [
          'pyvenv.cfg',
          'Scripts/python.exe',
          'Scripts/python3.exe',
          'Scripts/pythonw.exe',
        ])
          assertLocalPath(join(paths.environment, file));
        stage = 'venv_create';
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
        stage = 'alias_copy';
        cpSync(join(paths.bin, 'python.exe'), join(paths.bin, 'python3.exe'));
        stage = 'pip_bootstrap';
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
        stage = 'marker_write';
        durableJson(marker, identity);
      }
    }
    if (needsProbe) {
      stage = 'interpreter_probe';
      run(
        join(paths.bin, 'python.exe'),
        ['-I', '-B', '-c', 'import sys; assert sys.prefix != sys.base_prefix'],
        env,
      );
    }
    // A damaged pip does not block launching DSH or importing the built-in packages.
    stage = 'index_config';
    const index = configuredIndex(paths);
    writeConfig(paths, index);
    stage = 'constraints';
    assertLocalPath(paths.constraints);
    writeIfChanged(
      paths.constraints,
      [
        ...manifest.packages.map((p) => `${p.name}==${p.version}`),
        `pip==${pip.version}`,
        '',
      ].join('\n'),
    );
    return { ...paths, index };
  } catch (error) {
    error.pythonUserStage = stage;
    throw error;
  } finally {
    release?.();
  }
}

export function resetPythonUser(dataHome) {
  const paths = pythonUserPaths(dataHome);
  assertLocalPath(paths.root);
  const release = acquireOperationLock(join(paths.root, 'prepare.lock'));
  try {
    assertLocalPath(paths.settings);
    let index;
    try {
      index = configuredIndex(paths);
    } catch (error) {
      if (error.code) throw error;
      index = null;
    }
    const targets = [paths.environment, join(paths.root, 'cache')].map((path) =>
      resolve(path),
    );
    for (const target of targets) {
      if (!target.startsWith(resolve(paths.root) + sep))
        throw new Error('个人 Python 环境路径无效');
      assertLocalPath(target);
    }
    mkdirSync(dirname(paths.settings), { recursive: true });
    durableJson(paths.settings, { schema: 1, indexUrl: index });
    for (const target of targets)
      rmSync(target, { recursive: true, force: true });
    writeConfig(paths, index);
    return { reset: true };
  } finally {
    release();
  }
}
