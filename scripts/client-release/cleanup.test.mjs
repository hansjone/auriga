/** 客户端依赖准备清理、PNPM_CONFIG_SYMLINK 隔离与二次清理异常保留测试。不联网。 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { join, toNamespacedPath } from 'node:path';
import { ROOT, lockIdentity } from './inputs.mjs';
import {
  assertWithinBoundary,
  cleanupAfterError,
  removeDirSafely,
} from './cleanup.mjs';
import {
  fetchPnpmStore,
  pnpmFetchEnv,
  stageWorkspaceLocks,
} from './prepare.mjs';
import { clientCacheEntry, clientFetch } from './site-deps.mjs';

function temporary(t) {
  mkdirSync(join(ROOT, 'clients/installer/target'), { recursive: true });
  const directory = mkdtempSync(
    join(ROOT, 'clients/installer/target/cleanup-test-'),
  );
  t.after(() =>
    rmSync(toNamespacedPath(directory), {
      recursive: true,
      force: true,
      maxRetries: 3,
      retryDelay: 50,
    }),
  );
  return directory;
}

const linkType = process.platform === 'win32' ? 'junction' : 'dir';

test('assertWithinBoundary 与 removeDirSafely：强制路径边界约束', (t) => {
  const base = temporary(t);
  const inside = join(base, 'kit', '_prepare');
  const outside = join(base, 'outside');
  mkdirSync(inside, { recursive: true });
  mkdirSync(outside, { recursive: true });
  writeFileSync(join(outside, 'keep.txt'), 'safe');

  assert.throws(() => assertWithinBoundary(inside, ''), /within/);
  assert.throws(() => removeDirSafely(base, { within: base }), /边界外/);
  assert.throws(
    () => removeDirSafely(outside, { within: join(base, 'kit') }),
    /边界外/,
  );
  assert.equal(readFileSync(join(outside, 'keep.txt'), 'utf8'), 'safe');

  removeDirSafely(inside, { within: join(base, 'kit') });
  assert.equal(existsSync(inside), false);
});

test('removeDirSafely：嵌套 web/web/.pnpm 虚拟 store 含悬空与外部 Junction 时安全清理且不触碰外部目标', (t) => {
  const base = temporary(t);
  const externalTarget = join(base, 'external-secret');
  mkdirSync(externalTarget, { recursive: true });
  writeFileSync(join(externalTarget, 'must-stay.txt'), 'intact');

  const kit = join(base, `auriga-client-windows-${'a'.repeat(64)}`);
  const work = join(kit, '_prepare');
  const pnpmDir = join(
    work,
    'workspace',
    'web',
    'web',
    'node_modules',
    '.pnpm',
  );
  const helperParser = join(
    pnpmDir,
    '@babel+helper-string-parser@7.29.7',
    'node_modules',
    '@babel',
    'helper-string-parser',
  );
  const helperValidator = join(
    pnpmDir,
    '@babel+helper-validator-identifier@7.29.7',
    'node_modules',
    '@babel',
    'helper-validator-identifier',
  );
  const babelTypes = join(
    pnpmDir,
    '@babel+types@7.29.8',
    'node_modules',
    '@babel',
  );
  mkdirSync(toNamespacedPath(helperParser), { recursive: true });
  mkdirSync(toNamespacedPath(helperValidator), { recursive: true });
  mkdirSync(toNamespacedPath(babelTypes), { recursive: true });
  writeFileSync(toNamespacedPath(join(helperParser, 'index.js')), 'ok');
  writeFileSync(toNamespacedPath(join(helperValidator, 'index.js')), 'ok');

  const juncParser = join(babelTypes, 'helper-string-parser');
  const juncValidator = join(babelTypes, 'helper-validator-identifier');
  const juncExternal = join(babelTypes, 'external-link');
  symlinkSync(
    toNamespacedPath(helperParser),
    toNamespacedPath(juncParser),
    linkType,
  );
  symlinkSync(
    toNamespacedPath(helperValidator),
    toNamespacedPath(juncValidator),
    linkType,
  );
  symlinkSync(
    toNamespacedPath(externalTarget),
    toNamespacedPath(juncExternal),
    linkType,
  );

  // 先删掉内部目标制造悬空 Junction，模拟 Windows rimraf 触发 ENOTEMPTY 的场景。
  rmSync(toNamespacedPath(helperParser), { recursive: true, force: true });
  rmSync(toNamespacedPath(helperValidator), { recursive: true, force: true });

  // 注入模拟 rmSync 首次报 ENOTEMPTY，迫使走自底向上 dismantleTreeSafely 分支。
  let rmCalls = 0;
  removeDirSafely(work, {
    within: kit,
    phase: 'prepare-workspace-cleanup',
    sleep() {},
    rm(p, opts) {
      rmCalls += 1;
      if (rmCalls === 1) {
        const err = new Error(`ENOTEMPTY: directory not empty, rmdir '${p}'`);
        err.code = 'ENOTEMPTY';
        err.syscall = 'rmdir';
        throw err;
      }
      return rmSync(p, opts);
    },
  });

  assert.equal(existsSync(toNamespacedPath(work)), false);
  assert.equal(
    readFileSync(join(externalTarget, 'must-stay.txt'), 'utf8'),
    'intact',
  );
});

test('removeDirSafely：目标自身是符号链接/Junction 时只删链接本身', (t) => {
  const base = temporary(t);
  const external = join(base, 'external-dir');
  const kit = join(base, 'kit');
  mkdirSync(external, { recursive: true });
  mkdirSync(kit, { recursive: true });
  writeFileSync(join(external, 'data.txt'), 'keep');
  const linkPath = join(kit, '_prepare');
  symlinkSync(external, linkPath, linkType);

  removeDirSafely(linkPath, {
    within: kit,
    phase: 'prepare-workspace-cleanup',
  });
  assert.equal(existsSync(linkPath), false);
  assert.equal(readFileSync(join(external, 'data.txt'), 'utf8'), 'keep');
});

test('removeDirSafely：瞬态 EBUSY/EPERM 有界退避重试成功；耗尽后保留阶段、路径与错误码', (t) => {
  const base = temporary(t);
  const kit = join(base, 'kit');
  const work = join(kit, '_prepare');
  mkdirSync(work, { recursive: true });
  writeFileSync(join(work, 'a.txt'), '1');

  const delays = [];
  let attempts = 0;
  removeDirSafely(work, {
    within: kit,
    phase: 'transient-test',
    maxRetries: 3,
    retryDelayMs: 50,
    sleep: (ms) => delays.push(ms),
    dismantle() {
      const err = new Error('EBUSY: still busy');
      err.code = 'EBUSY';
      throw err;
    },
    rm(p, opts) {
      attempts += 1;
      if (attempts <= 2) {
        const err = new Error('EBUSY: resource busy or locked');
        err.code = 'EBUSY';
        throw err;
      }
      return rmSync(p, opts);
    },
  });
  assert.equal(existsSync(work), false);
  assert.equal(attempts, 3);
  assert.deepEqual(delays, [50, 100]);

  // 持续不可删除：验证耗尽重试与错误上下文。
  const stuck = join(kit, '_stuck');
  mkdirSync(stuck, { recursive: true });
  const failDelays = [];
  assert.throws(
    () =>
      removeDirSafely(stuck, {
        within: kit,
        phase: 'stuck-phase',
        maxRetries: 2,
        retryDelayMs: 25,
        sleep: (ms) => failDelays.push(ms),
        dismantle() {
          const err = new Error('simulated dismantle EPERM');
          err.code = 'EPERM';
          throw err;
        },
        rm() {
          const err = new Error('simulated EPERM');
          err.code = 'EPERM';
          throw err;
        },
      }),
    (err) =>
      err.phase === 'stuck-phase' &&
      err.path === stuck &&
      err.code === 'EPERM' &&
      /stuck-phase/.test(err.message),
  );
  assert.deepEqual(failDelays, [25, 50]);

  // 不可重试错误（如 EINVAL）立即失败，不空等重试。
  const badDelays = [];
  assert.throws(
    () =>
      removeDirSafely(stuck, {
        within: kit,
        phase: 'non-retryable',
        maxRetries: 3,
        retryDelayMs: 50,
        sleep: (ms) => badDelays.push(ms),
        rm() {
          const err = new Error('simulated EINVAL');
          err.code = 'EINVAL';
          throw err;
        },
      }),
    (err) => err.code === 'EINVAL' && /non-retryable/.test(err.message),
  );
  assert.deepEqual(badDelays, []);
});

test('pnpmFetchEnv 与 fetchPnpmStore：隔离设置 PNPM_CONFIG_SYMLINK=false 且不污染原环境', (t) => {
  const base = temporary(t);
  const originalEnv = {
    PATH: 'x',
    pnpm_config_symlink: 'true',
    npm_config_symlink: 'true',
    npm_config_registry: 'https://registry.internal.example',
  };
  const next = pnpmFetchEnv(originalEnv);
  assert.equal(next.PNPM_CONFIG_SYMLINK, 'false');
  assert.equal('pnpm_config_symlink' in next, false);
  assert.equal('npm_config_symlink' in next, false);
  assert.equal(next.npm_config_registry, 'https://registry.internal.example');
  // 绝不修改调用方传入的 env。
  assert.equal(originalEnv.pnpm_config_symlink, 'true');
  assert.equal('PNPM_CONFIG_SYMLINK' in originalEnv, false);

  const kit = join(base, 'kit');
  const workspace = join(kit, '_prepare', 'workspace');
  const store = join(kit, 'pnpm-store');
  const refTarget = join(base, 'prev-virtual-store');
  mkdirSync(refTarget, { recursive: true });
  writeFileSync(join(refTarget, 'keep'), 'ok');
  mkdirSync(join(store, 'v11', 'projects'), { recursive: true });
  symlinkSync(refTarget, join(store, 'v11', 'projects', 'old'), linkType);

  let capturedEnv;
  let capturedArgs;
  fetchPnpmStore(
    kit,
    workspace,
    store,
    originalEnv,
    (_cmd, args, _cwd, env) => {
      capturedArgs = args;
      capturedEnv = env;
    },
  );
  assert.equal(capturedEnv.PNPM_CONFIG_SYMLINK, 'false');
  assert.ok(capturedArgs.includes('fetch'));
  assert.equal(existsSync(join(store, 'v11', 'projects', 'old')), false);
  assert.equal(readFileSync(join(refTarget, 'keep'), 'utf8'), 'ok');
});

test('stageWorkspaceLocks：按非默认 lockfileDir: web 仅暂存锁与清单，无源码自复制', (t) => {
  const base = temporary(t);
  const workspace = join(base, '_prepare', 'workspace');
  const { locks } = lockIdentity(ROOT);
  stageWorkspaceLocks(ROOT, locks, workspace);
  assert.equal(existsSync(join(workspace, 'pnpm-workspace.yaml')), true);
  assert.equal(existsSync(join(workspace, 'web', 'pnpm-lock.yaml')), true);
  assert.equal(existsSync(join(workspace, 'web', 'web')), false);
  assert.match(
    readFileSync(join(workspace, 'pnpm-workspace.yaml'), 'utf8'),
    /^lockfileDir:\s*web\b/m,
  );
});

test('cleanupAfterError 与 clientFetch：二次清理失败时保留原始异常且不发布半成品缓存', async (t) => {
  const orig = new Error('primary-prepare-failure');
  orig.code = 'EPRIMARY';
  assert.throws(
    () =>
      cleanupAfterError(
        orig,
        () => {
          throw new Error('secondary-enotempty');
        },
        { phase: 'client-fetch-staging-cleanup', path: 'D:\\staging' },
      ),
    (err) =>
      err.cause === orig &&
      err.originalError === orig &&
      err.code === 'EPRIMARY' &&
      /primary-prepare-failure/.test(err.message) &&
      /secondary-enotempty/.test(err.message) &&
      /client-fetch-staging-cleanup/.test(err.message),
  );

  // clientFetch：prepare 失败 + 二次 staging 清理也失败 -> 两者均报告且缓存无半成品。
  const cache = temporary(t);
  const { lockSha256 } = lockIdentity(ROOT);
  await assert.rejects(
    () =>
      clientFetch({
        cacheDir: cache,
        sourceRoot: ROOT,
        acceptNewDeps: true,
        filesSource: 'file:///D:/files',
        npmRegistry: 'https://npm.internal.example',
        log() {},
        async prepare() {
          throw new Error('original-fetch-error');
        },
        cleanupDir(_dir, opts) {
          if (opts.phase === 'client-fetch-staging-cleanup') {
            throw new Error('staging-rmdir-enotempty');
          }
        },
      }),
    (err) =>
      /original-fetch-error/.test(err.message) &&
      /staging-rmdir-enotempty/.test(err.message) &&
      /client-fetch-staging-cleanup/.test(err.message) &&
      err.originalError?.message === 'original-fetch-error' &&
      err.cleanupError?.message === 'staging-rmdir-enotempty',
  );
  assert.equal(existsSync(clientCacheEntry(cache, lockSha256)), false);
});
