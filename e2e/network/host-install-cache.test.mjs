import test from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtemp,
  mkdir,
  readFile,
  writeFile,
  rm,
  symlink,
  readdir,
  access,
} from 'node:fs/promises';
import { join, resolve, relative, dirname } from 'node:path';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import http from 'node:http';
import os from 'node:os';
import {
  hostInstallOptions,
  hostInstallFailure,
  assertPublicHostLock,
  isolatedEnvironment as betaEnvironment,
  runLogged,
} from '../../scripts/beta-ui/plugin-dsh.mjs';
import {
  isolatedEnvironment,
  runNativeInstallStep,
} from './support/native-install.mjs';

const run = promisify(execFile);
const target = resolve('e2e/network/target');
async function fixture(t) {
  await mkdir(target, { recursive: true });
  const directory = await mkdtemp(join(target, 'host-cache-test-'));
  t.after(async () => {
    assert.equal(dirname(resolve(directory)), target);
    await rm(directory, { recursive: true, force: true });
  });
  return directory;
}

test('宿主缓存持久共享且默认在仓库外，隔离 profile/配置和 Node 钩子保持原行为', () => {
  const defaults = hostInstallOptions({});
  const fromRepo = relative(process.cwd(), defaults.npmCache);
  assert.ok(fromRepo.startsWith('..') || resolve(fromRepo) === fromRepo);
  assert.equal(defaults.timeoutMs, 300000);
  const cache = resolve('e2e/network/target/shared-public-cache');
  const source = {
    AURIGA_DSH_HOST_NPM_CACHE: cache,
    AURIGA_DSH_HOST_INSTALL_TIMEOUT_MS: '600000',
    npm_config_userconfig: 'real-user-config',
    NODE_OPTIONS: '--import=untrusted',
    NPM_TOKEN: 'not-a-real-token',
  };
  const first = hostInstallOptions(source);
  assert.equal(first.npmCache, cache);
  assert.equal(first.timeoutMs, 600000);
  assert.ok(first.args.includes('--prefer-offline'));
  assert.deepEqual(first.args.slice(-2), ['--cache', cache]);
  assert.ok(first.args.includes('--ignore-scripts'));
  for (const environment of [isolatedEnvironment, betaEnvironment]) {
    const a = environment(resolve('e2e/network/target/host-a'), source);
    const b = environment(resolve('e2e/network/target/host-b'), source);
    assert.notEqual(a.DSH_HOME, b.DSH_HOME);
    assert.notEqual(a.HOME, b.HOME);
    assert.notEqual(a.npm_config_userconfig, b.npm_config_userconfig);
    assert.notEqual(a.npm_config_cache, cache);
    for (const key of [
      'NODE_OPTIONS',
      'NPM_TOKEN',
      'AURIGA_DSH_HOST_NPM_CACHE',
    ])
      assert.equal(a[key], undefined);
  }
  assert.equal(hostInstallOptions(source).npmCache, first.npmCache);
  assert.equal(
    hostInstallOptions(source, { npmCache: target, timeoutMs: 900000 })
      .npmCache,
    target,
  );
  assert.throws(() =>
    hostInstallOptions({ AURIGA_DSH_HOST_NPM_CACHE: 'relative' }),
  );
  for (const value of ['0', '-1', 'NaN', '300000.5', '999999999999'])
    assert.throws(() =>
      hostInstallOptions({ AURIGA_DSH_HOST_INSTALL_TIMEOUT_MS: value }),
    );
});

test('缓存拒绝真实 DSH 根等父子重叠且校验前后哨兵不变', async (t) => {
  const state = await fixture(t);
  const home = join(state, 'home');
  const profile = join(home, '.dsh');
  const custom = join(state, 'custom', 'profile');
  await mkdir(profile, { recursive: true });
  await mkdir(custom, { recursive: true });
  await writeFile(join(profile, 'sentinel'), 'unchanged');
  await writeFile(join(custom, 'sentinel'), 'unchanged');
  for (const source of [
    { HOME: home },
    { USERPROFILE: home },
    { DSH_HOME: custom },
  ]) {
    const root = source.DSH_HOME ?? profile;
    for (const cache of [
      root,
      dirname(root),
      join(root, 'new-cache', 'leaf'),
    ]) {
      assert.throws(
        () => hostInstallOptions(source, { npmCache: cache }),
        /DSH.*profile/,
      );
    }
  }
  for (const root of [profile, custom]) {
    assert.deepEqual(await readdir(root), ['sentinel']);
    assert.equal(await readFile(join(root, 'sentinel'), 'utf8'), 'unchanged');
  }
});

test('普通缓存、home 内默认形状、同前缀目录和缺失末级可持久复用且校验不写入', async (t) => {
  const state = await fixture(t);
  const home = join(state, 'home');
  await mkdir(home);
  for (const cache of [
    join(home, '.cache', 'auriga', 'npm-dsh-host'),
    join(home, '.dsh-other'),
    join(state, 'ordinary'),
  ]) {
    const source = {
      HOME: home,
      USERPROFILE: home,
      DSH_HOME: join(home, 'missing', 'profile'),
    };
    assert.equal(
      hostInstallOptions(source, { npmCache: cache }).npmCache,
      cache,
    );
    await assert.rejects(access(cache), { code: 'ENOENT' });
    await mkdir(cache, { recursive: true });
    assert.equal(
      hostInstallOptions(source, { npmCache: cache }).npmCache,
      cache,
    );
  }
});

test('缓存拒绝链接本身、链接祖先和普通文件并保护链接目标哨兵', async (t) => {
  const state = await fixture(t);
  const profile = join(state, 'profile');
  const ordinary = join(state, 'ordinary');
  await mkdir(profile);
  await mkdir(ordinary);
  await writeFile(join(profile, 'sentinel'), 'unchanged');
  const file = join(state, 'file');
  await writeFile(file, 'unchanged');
  for (const [name, target] of [
    ['profile-link', profile],
    ['ordinary-link', ordinary],
  ]) {
    const link = join(state, name);
    await symlink(
      target,
      link,
      process.platform === 'win32' ? 'junction' : 'dir',
    );
    for (const cache of [link, join(link, 'missing', 'leaf')])
      assert.throws(
        () => hostInstallOptions({ DSH_HOME: profile }, { npmCache: cache }),
        /cache/,
      );
  }
  for (const cache of [file, join(file, 'missing')])
    assert.throws(() => hostInstallOptions({}, { npmCache: cache }));
  assert.equal(await readFile(file, 'utf8'), 'unchanged');
  assert.deepEqual(await readdir(profile), ['sentinel']);
  assert.deepEqual(await readdir(ordinary), []);
  assert.equal(await readFile(join(profile, 'sentinel'), 'utf8'), 'unchanged');
});

test('真实 profile 链接别名及缺失后缀按真实目录保护', async (t) => {
  const state = await fixture(t);
  const actual = join(state, 'actual');
  const alias = join(state, 'alias');
  await mkdir(actual);
  await symlink(
    actual,
    alias,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  for (const root of [alias, join(alias, 'missing-profile')]) {
    for (const cache of [
      actual,
      join(actual, 'missing-profile'),
      join(actual, 'missing-profile', 'cache'),
    ])
      assert.throws(
        () => hostInstallOptions({ DSH_HOME: root }, { npmCache: cache }),
        /DSH.*profile/,
      );
  }
  assert.deepEqual(await readdir(actual), []);
});

for (const kind of ['DSH_HOME', 'HOME', 'USERPROFILE', 'missing-suffix']) {
  test(`悬空受保护链接在缓存创建前拒绝：${kind}`, async (t) => {
    const state = await fixture(t);
    const home = join(state, 'home');
    await mkdir(home);
    const target = join(state, 'missing-target');
    const alias = join(
      home,
      kind === 'HOME' || kind === 'USERPROFILE' ? '.dsh' : 'alias',
    );
    await writeFile(join(home, 'sentinel'), 'unchanged');
    await symlink(
      target,
      alias,
      process.platform === 'win32' ? 'junction' : 'dir',
    );
    const source =
      kind === 'HOME' || kind === 'USERPROFILE'
        ? { [kind]: home }
        : {
            DSH_HOME:
              kind === 'missing-suffix'
                ? join(alias, 'missing-profile')
                : alias,
          };
    const before = await readdir(home);
    for (const cache of [
      target,
      join(target, 'missing-profile'),
      join(target, 'missing-profile', 'cache'),
    ]) {
      assert.throws(
        () => hostInstallOptions(source, { npmCache: cache }),
        /DSH.*profile/,
      );
      await assert.rejects(access(target), { code: 'ENOENT' });
      assert.deepEqual(await readdir(home), before);
      assert.equal(await readFile(join(home, 'sentinel'), 'utf8'), 'unchanged');
    }
  });
}

test('系统默认真实 DSH 根及祖先在无显式环境时仍拒绝', () => {
  const root = join(os.homedir(), '.dsh');
  for (const cache of [root, dirname(root), join(root, 'missing-cache')])
    assert.throws(
      () => hostInstallOptions({}, { npmCache: cache }),
      /DSH.*profile/,
    );
});

test(
  'Windows 大小写与盘符变化仍识别 profile 重叠',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const state = await fixture(t);
    const profile = join(state, 'MixedCaseProfile');
    await mkdir(profile);
    for (const cache of [
      profile.toUpperCase(),
      join(profile.toLowerCase(), 'missing'),
      dirname(profile).toUpperCase(),
    ])
      assert.throws(
        () => hostInstallOptions({ dsh_home: profile }, { npmCache: cache }),
        /DSH.*profile/,
      );
    assert.deepEqual(await readdir(profile), []);
  },
);

test('共享缓存只允许锁定公共 npm 包，拒绝私有来源或缺少 integrity', async () => {
  const lock = JSON.parse(
    await readFile(
      new URL('./support/dsh-runtime/package-lock.json', import.meta.url),
      'utf8',
    ),
  );
  assertPublicHostLock(lock);
  const name = Object.keys(lock.packages).find((key) => key);
  for (const change of [
    (entry) => (entry.resolved = 'https://private.example.com/package.tgz'),
    (entry) => delete entry.integrity,
  ]) {
    const bad = JSON.parse(JSON.stringify(lock));
    change(bad.packages[name]);
    assert.throws(() => assertPublicHostLock(bad));
  }
});

test('首装超时明确记录 signal/killed/期限/缓存，非超时错误不误报 timeout', async (t) => {
  const state = await fixture(t);
  const env = isolatedEnvironment(state);
  await assert.rejects(
    runNativeInstallStep('host-install', ['-e', 'setInterval(()=>{},1000)'], {
      state,
      env,
      timeoutMs: 100,
      npmCache: join(state, 'public-cache'),
    }),
    /timed out after 100 ms/,
  );
  const log = await readFile(join(state, 'host-install.log'), 'utf8');
  const metadata = JSON.parse(log.split('\n')[0]);
  assert.equal(metadata.exitCode, null);
  assert.equal(metadata.killed, true);
  assert.ok(metadata.signal);
  assert.equal(metadata.timeoutMs, 100);
  assert.equal(metadata.npmCache, join(state, 'public-cache'));
  assert.match(metadata.failure, /timed out/);
  await assert.rejects(
    runNativeInstallStep(
      'failed-install',
      ['-e', "console.error('first failure retained');process.exit(7)"],
      { state, env, timeoutMs: 5000 },
    ),
    /exit 7/,
  );
  assert.match(
    await readFile(join(state, 'failed-install.log'), 'utf8'),
    /first failure retained/,
  );
  assert.doesNotMatch(
    hostInstallFailure(
      {
        code: 'ERR_CHILD_PROCESS_STDIO_MAXBUFFER',
        killed: true,
        signal: 'SIGTERM',
      },
      100,
    ),
    /timed out/,
  );
  assert.match(
    hostInstallFailure({ code: 1, stderr: 'npm error code ENOTFOUND' }, 300000),
    /ENOTFOUND/,
  );
});

test('Beta 宿主安装同样记录超时原因，其他命令的原始版本日志保持不变', async (t) => {
  const state = await fixture(t),
    env = betaEnvironment(state);
  runLogged(
    process.execPath,
    ['-e', "console.log('11.28.6')"],
    state,
    env,
    'version',
  );
  assert.equal(await readFile(join(state, 'version.log'), 'utf8'), '11.28.6\n');
  assert.throws(
    () =>
      runLogged(
        process.execPath,
        ['-e', 'setInterval(()=>{},1000)'],
        state,
        env,
        'host-install',
        state,
        { timeoutMs: 100, npmCache: join(state, 'public-cache') },
      ),
    /timed out after 100 ms/,
  );
  const lines = (await readFile(join(state, 'host-install.log'), 'utf8'))
    .trim()
    .split('\n');
  const header = JSON.parse(lines[0]),
    outcome = JSON.parse(lines.at(-1));
  assert.equal(header.timeoutMs, 100);
  assert.equal(outcome.exitCode, null);
  assert.equal(outcome.errorCode, 'ETIMEDOUT');
  assert.ok(outcome.signal);
  assert.match(outcome.failure, /timed out/);
  assert.ok(outcome.durationMs >= 100);
});

test('两个全新 npm ci 目录复用同一公开包缓存，第二次断网仍可从完整性缓存安装', async (t) => {
  const state = await fixture(t);
  const packageRoot = join(state, 'tar-input');
  await mkdir(join(packageRoot, 'package'), { recursive: true });
  await writeFile(
    join(packageRoot, 'package/package.json'),
    JSON.stringify({
      name: 'auriga-cache-public-fixture',
      version: '1.0.0',
      main: 'index.js',
    }),
  );
  await writeFile(
    join(packageRoot, 'package/index.js'),
    "module.exports = 'public cache fixture';\n",
  );
  const tarball = join(state, 'package.tgz');
  await run('tar', ['-czf', tarball, '-C', packageRoot, 'package'], {
    windowsHide: true,
  });
  const bytes = await readFile(tarball);
  let downloads = 0;
  const server = http.createServer((req, res) => {
    assert.equal(req.headers.authorization, undefined);
    assert.equal(req.headers.cookie, undefined);
    downloads++;
    res.writeHead(200, { 'content-type': 'application/octet-stream' });
    res.end(bytes);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const url = `http://127.0.0.1:${server.address().port}/package.tgz`;
  const options = hostInstallOptions(
    {},
    { npmCache: join(state, 'shared-cache') },
  );
  const npm = join(
    dirname(process.execPath),
    'node_modules/npm/bin/npm-cli.js',
  );
  for (const name of ['first', 'second']) {
    const directory = join(state, name),
      env = isolatedEnvironment(directory);
    await mkdir(join(directory, 'config'), { recursive: true });
    for (const file of [env.npm_config_userconfig, env.npm_config_globalconfig])
      await writeFile(file, '');
    await writeFile(
      join(directory, 'package.json'),
      JSON.stringify({
        name: 'fixture-consumer',
        version: '1.0.0',
        private: true,
        dependencies: { 'auriga-cache-public-fixture': '1.0.0' },
      }),
    );
    await writeFile(
      join(directory, 'package-lock.json'),
      JSON.stringify({
        name: 'fixture-consumer',
        version: '1.0.0',
        lockfileVersion: 3,
        packages: {
          '': {
            name: 'fixture-consumer',
            version: '1.0.0',
            dependencies: { 'auriga-cache-public-fixture': '1.0.0' },
          },
          'node_modules/auriga-cache-public-fixture': {
            version: '1.0.0',
            resolved: url,
            integrity:
              'sha512-' + createHash('sha512').update(bytes).digest('base64'),
          },
        },
      }),
    );
    if (name === 'second')
      await new Promise((resolve) => server.close(resolve));
    await run(
      process.execPath,
      [npm, ...options.args, ...(name === 'second' ? ['--offline'] : [])],
      { cwd: directory, env, windowsHide: true, timeout: 30000 },
    );
    assert.equal(
      JSON.parse(
        await readFile(
          join(
            directory,
            'node_modules/auriga-cache-public-fixture/package.json',
          ),
          'utf8',
        ),
      ).version,
      '1.0.0',
    );
  }
  assert.equal(downloads, 1);
});
