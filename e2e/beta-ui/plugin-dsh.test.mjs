import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fork, spawn } from 'node:child_process';
import { generateKeyPairSync } from 'node:crypto';
import { privatePath, waitUntil } from '../../scripts/beta-ui/runner.mjs';
import {
  parseArguments,
  assertStatePath,
  isolatedEnvironment,
  connectionPatch,
  claimState,
  stopOwnedProcess,
  assertHostCapabilities,
  assertRuntimeLock,
  verifyPackageManagerVersion,
  buildEnvironment,
  createHostOutput,
  discoverProvider,
} from '../../scripts/beta-ui/plugin-dsh.mjs';
import { validateConnect } from '../../scripts/beta-ui/demo-network-send-config.mjs';

const base = path.resolve('.dev/plugin-dsh');
function temporary(t) {
  fs.mkdirSync(base, { recursive: true });
  const dir = fs.mkdtempSync(path.join(base, 'guard-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('arguments reject missing, duplicate, unknown and unsafe launch values', () => {
  for (const args of [
    [],
    ['launch'],
    ['launch', '--state', 'relative'],
    ['launch', '--state', base, '--state', base],
    ['stop', '--state', base, '--port', '1'],
    ['launch', '--state', base, '--port', '18080'],
    ['launch', '--state', base, '--profile', 'desktop'],
    ['launch', '--state', base, '--dsh-package', base],
  ]) {
    assert.throws(() => parseArguments(args));
  }
  assert.equal(parseArguments(['launch', '--state', base]).port, 0);
  assert.equal(
    parseArguments([
      'launch',
      '--state',
      base,
      '--connect-config',
      'public.json',
    ]).connectConfig,
    'public.json',
  );
  assert.throws(() =>
    parseArguments([
      'stop',
      '--state',
      base,
      '--connect-config',
      'public.json',
    ]),
  );
});

test('selected installation pins survive profile patch and provider discovery uses its verified HTTPS origin', async (t) => {
  const connect = validateConnect({
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    providerId: 'team',
    clientId: 'auriga-dsh',
    network: {
      realm: '1',
      wssUrl: 'wss://network.example.com/ws',
      roots: [
        {
          kid: 'root-1',
          public_key_spki: generateKeyPairSync('ed25519')
            .publicKey.export({ type: 'spki', format: 'der' })
            .toString('base64url'),
        },
      ],
    },
  });
  const patch = connectionPatch('team', connect);
  assert.deepEqual(JSON.parse(patch.split('config: ')[1]).connect, connect);
  assert.ok(!patch.includes('allowLocalHttp'));
  const original = globalThis.fetch;
  t.after(() => {
    globalThis.fetch = original;
  });
  let id = 'team';
  globalThis.fetch = async (url, options) => {
    assert.equal(url, 'https://portal.example.com/api/iam/v1/login/providers');
    assert.equal(options.redirect, 'error');
    return {
      ok: true,
      json: async () => ({
        items: [{ id, connectorType: 'team-account', primaryStep: 'form' }],
      }),
    };
  };
  assert.equal(await discoverProvider(connect), 'team');
  const oldTls = process.env.NODE_TLS_REJECT_UNAUTHORIZED;
  try {
    process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
    await assert.rejects(discoverProvider(connect), /禁止跳过 TLS/);
  } finally {
    if (oldTls === undefined) delete process.env.NODE_TLS_REJECT_UNAUTHORIZED;
    else process.env.NODE_TLS_REJECT_UNAUTHORIZED = oldTls;
  }
  id = 'other';
  await assert.rejects(discoverProvider(connect), /身份源.*不一致/);
  globalThis.fetch = async () => {
    throw new Error('untrusted TLS');
  };
  await assert.rejects(discoverProvider(connect), /untrusted TLS/);
});

test('host stdout/stderr parse split launch URL in memory and never persist query tickets', async (t) => {
  const dir = temporary(t);
  const secret = 'fake-' + Date.now() + '-launch-credential';
  const ticket = 'fake-' + Date.now() + '-ticket';
  const url = 'http://127.0.0.1:12345/?token=' + secret;
  const file = path.join(dir, 'dsh.log');
  const urls = [];
  const collectors = [0, 1].map(() =>
    createHostOutput(
      (line) => fs.appendFileSync(file, line),
      (value) => urls.push(value),
    ),
  );
  const code = `process.stdout.write(${JSON.stringify(url.slice(0, -10))});
    setTimeout(() => {process.stdout.write(${JSON.stringify(url.slice(-10) + '\n')});
    process.stderr.write(${JSON.stringify('error: https://example.com/path?%74icket=' + ticket + '&access_token=' + secret + '#token=' + ticket)});}, 10);`;
  const child = spawn(process.execPath, ['-e', code], {
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  for (const [i, stream] of [child.stdout, child.stderr].entries()) {
    stream.on('data', (chunk) => collectors[i].write(chunk));
    stream.on('end', () => collectors[i].end());
  }
  const exit = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  });
  assert.equal(exit, 0);
  assert.equal(urls[0], url);
  assert.match(fs.readFileSync(file, 'utf8'), /redacted/);
  for (const name of fs.readdirSync(dir)) {
    const contents = fs.readFileSync(path.join(dir, name), 'utf8');
    assert.ok(!contents.includes(secret) && !contents.includes(ticket), name);
  }
});

test('build environment keeps approved tool caches and certificates while isolating user config and secrets', () => {
  const env = buildEnvironment(base, {
    Path: 'tools',
    SystemRoot: 'windows',
    AURIGA_PNPM_STORE: 'approved-store',
    COREPACK_HOME: 'approved-corepack',
    NODE_EXTRA_CA_CERTS: 'approved-cert',
    NODE_OPTIONS: '--import=untrusted',
    OPENAI_API_KEY: 'api-secret',
    DEEPSEEK_API_KEY: 'api-secret',
    DSH_HOME: 'real-dsh',
    HOME: 'real-user',
    npm_config_userconfig: 'real-npmrc',
    npm_config_globalconfig: 'real-global-npmrc',
    PSModulePath: 'foreign-modules',
    AURIGA_INSTANCE_TOKEN_FILE: 'real-token',
  });
  for (const key of [
    'NODE_OPTIONS',
    'OPENAI_API_KEY',
    'DEEPSEEK_API_KEY',
    'PSModulePath',
    'AURIGA_INSTANCE_TOKEN_FILE',
  ])
    assert.equal(env[key], undefined, key);
  for (const key of [
    'DSH_HOME',
    'HOME',
    'USERPROFILE',
    'npm_config_userconfig',
    'npm_config_globalconfig',
  ])
    assert.ok(env[key].startsWith(base + path.sep), key);
  assert.equal(env.AURIGA_PNPM_STORE, 'approved-store');
  assert.equal(env.COREPACK_HOME, 'approved-corepack');
  assert.equal(env.NODE_EXTRA_CA_CERTS, 'approved-cert');
  assert.equal(env.Path, 'tools');
});

test(
  'real detached relay keeps launch tickets out of every StateDir file and exits with its host',
  { skip: process.platform !== 'win32', timeout: 20000 },
  async (t) => {
    const dir = temporary(t);
    privatePath(dir, true);
    fs.mkdirSync(path.join(dir, 'workspace'));
    const bin = path.join(dir, 'fake-host.mjs');
    fs.writeFileSync(
      bin,
      `const url='http://127.0.0.1:12345/?token='+process.env.FAKE_HOST_TICKET;
      process.stdout.write(url.slice(0,-5));
      setTimeout(()=>process.stdout.write(url.slice(-5)+'\\n'),10);
      process.stderr.write('https://example.com/?ticket='+process.env.FAKE_HOST_EXTRA_TICKET+'\\n');
      setInterval(()=>{},1000);`,
    );
    const secret = 'fake-' + Date.now() + '-launch-ticket';
    const extra = 'fake-' + Date.now() + '-other-ticket';
    const worker = fork(
      path.resolve('scripts/beta-ui/plugin-dsh-output.mjs'),
      [dir, bin],
      {
        cwd: dir,
        env: {
          ...isolatedEnvironment(dir),
          FAKE_HOST_TICKET: secret,
          FAKE_HOST_EXTRA_TICKET: extra,
        },
        windowsHide: true,
        execArgv: [],
        stdio: ['ignore', 'ignore', 'ignore', 'ipc'],
      },
    );
    let host, url, failure;
    let closed = false;
    worker.on('message', (message) => {
      if (message.type === 'started') host = message.host;
      if (message.type === 'url') url = message.url;
      if (message.type === 'failure') failure = true;
    });
    worker.once('exit', () => {
      closed = true;
    });
    try {
      await waitUntil(
        () => {
          assert.ok(!failure && !closed, 'relay exited before startup');
          return host && url;
        },
        'relay did not provide host and URL',
        10000,
      );
      assert.equal(url, 'http://127.0.0.1:12345/?token=' + secret);
      worker.disconnect();
      const scan = (directory) => {
        for (const entry of fs.readdirSync(directory, {
          withFileTypes: true,
        })) {
          const file = path.join(directory, entry.name);
          if (entry.isDirectory()) scan(file);
          else {
            const content = fs.readFileSync(file, 'utf8');
            assert.ok(
              !content.includes(secret) && !content.includes(extra),
              file,
            );
          }
        }
      };
      scan(dir);
      assert.match(
        fs.readFileSync(path.join(dir, 'dsh.log'), 'utf8'),
        /redacted/,
      );
    } finally {
      if (!host && fs.existsSync(path.join(dir, 'host-process.json')))
        host = JSON.parse(
          fs.readFileSync(path.join(dir, 'host-process.json')),
        ).host;
      await stopOwnedProcess(host);
      if (!closed) {
        try {
          await waitUntil(() => closed, 'relay did not exit with host', 5000);
        } finally {
          if (!closed) worker.kill();
        }
      }
    }
  },
);

test('selected host fails explicitly if a required public connection capability is absent', () => {
  const connection = { admit() {}, authorizeIndex() {}, isTrusted() {} };
  assert.doesNotThrow(() => assertHostCapabilities(connection));
  for (const key of Object.keys(connection)) {
    const missing = { ...connection, [key]: undefined };
    assert.throws(
      () => assertHostCapabilities(missing),
      new RegExp('DSH.*connection\\.' + key),
    );
    assert.throws(
      () => assertHostCapabilities({ ...connection, [key]: true }),
      new RegExp(key),
    );
  }
  assert.throws(() => assertHostCapabilities(undefined), /connection\.admit/);
});

test('runtime lock rejects changed host version or integrity and all dependencies are registry pinned', () => {
  const lock = JSON.parse(
    fs.readFileSync(
      new URL(
        '../../scripts/beta-ui/dsh-runtime/package-lock.json',
        import.meta.url,
      ),
    ),
  );
  assert.doesNotThrow(() => assertRuntimeLock(lock));
  for (const key of ['version', 'integrity']) {
    const bad = JSON.parse(JSON.stringify(lock));
    bad.packages['node_modules/@deepseek-ai/dsh'][key] = 'changed';
    assert.throws(() => assertRuntimeLock(bad), /integrity/);
  }
  for (const [name, entry] of Object.entries(lock.packages)) {
    if (!name) continue;
    assert.match(entry.resolved, /^https:\/\/registry\.npmjs\.org\//, name);
    assert.match(entry.integrity, /^sha512-/, name);
  }
});

test('state rejects real DSH home, ancestors, default profile and repository source', () => {
  const user = path.resolve('example-user');
  const source = {
    USERPROFILE: user,
    HOME: user,
    DSH_HOME: path.join(user, 'custom-harness'),
  };
  for (const dir of [
    user,
    path.dirname(user),
    path.join(user, '.dsh', 'profiles', 'test'),
    path.join(user, 'custom-harness', 'fresh'),
    path.resolve('clients/dsh-plugin'),
  ]) {
    assert.throws(() => assertStatePath(dir, source));
  }
  assert.equal(
    assertStatePath(path.join(base, 'fresh'), source),
    path.join(base, 'fresh'),
  );
});

test('fresh claim refuses existing directories and symlink ancestors without modifying targets', (t) => {
  const dir = temporary(t);
  const target = path.join(dir, 'target');
  fs.mkdirSync(target);
  fs.writeFileSync(path.join(target, 'sentinel'), 'unchanged');
  assert.throws(() => claimState(target));
  const link = path.join(dir, 'link');
  fs.symlinkSync(
    target,
    link,
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  assert.throws(() => claimState(path.join(link, 'fresh')));
  assert.deepEqual(fs.readdirSync(target), ['sentinel']);
  fs.unlinkSync(link);
  const fresh = path.join(dir, 'fresh');
  claimState(fresh);
  assert.ok(fs.existsSync(fresh));
  assert.throws(() => claimState(fresh));
});

test('child environment drops real profiles, credentials, patches and Node injection', () => {
  const source = {
    Path: 'tools',
    SystemRoot: 'windows',
    DSH_HOME: 'real-profile',
    dsh_home: 'another-real-profile',
    AURIGA_MODE: 'managed',
    AURIGA_INSTANCE_TOKEN_FILE: 'secret',
    DEEPSEEK_API_KEY: 'secret',
    NODE_OPTIONS: '--import=unsafe',
    USERPROFILE: 'real-user',
    npm_config_userconfig: 'real-npmrc',
    PSModulePath: 'foreign',
    COREPACK_DEFAULT_TO_LATEST: '1',
    COREPACK_ENABLE_PROJECT_SPEC: '0',
  };
  const env = isolatedEnvironment(base, source);
  for (const key of [
    'AURIGA_INSTANCE_TOKEN_FILE',
    'DEEPSEEK_API_KEY',
    'NODE_OPTIONS',
    'PSModulePath',
    'dsh_home',
  ])
    assert.equal(env[key], undefined);
  for (const key of [
    'DSH_HOME',
    'USERPROFILE',
    'HOME',
    'APPDATA',
    'LOCALAPPDATA',
    'TMP',
    'TEMP',
    'XDG_CONFIG_HOME',
    'npm_config_userconfig',
  ])
    assert.ok(env[key].startsWith(base + path.sep), key);
  assert.equal(env.AURIGA_MODE, 'connect');
  assert.equal(env.COREPACK_DEFAULT_TO_LATEST, '0');
  assert.equal(env.COREPACK_ENABLE_PROJECT_SPEC, '1');
  assert.equal(env.Path, 'tools');
  assert.equal(source.USERPROFILE, 'real-user');
});

test('isolated pnpm must match the exact repository packageManager and rejects latest or mismatched versions', () => {
  const pm = JSON.parse(
    fs.readFileSync(path.resolve('package.json')),
  ).packageManager;
  assert.equal(verifyPackageManagerVersion(pm, '11.28.6\r\n'), '11.28.6');
  for (const actual of ['12.15.0', '', '11.28.6\n12.15.0', 'pnpm@11.28.6'])
    assert.throws(() => verifyPackageManagerVersion(pm, actual), /pnpm/);
  for (const requested of ['pnpm@latest', 'pnpm@^11.28.6', 'npm@11.28.6'])
    assert.throws(
      () => verifyPackageManagerVersion(requested, '11.28.6'),
      /pnpm/,
    );
});

test('Beta patch is enabled and contains explicit loopback HTTP plus native client, no credentials', () => {
  const patch = connectionPatch('team-provider');
  assert.match(patch, /auriga-beta-native/);
  assert.match(patch, /allowLocalHttp/);
  assert.match(patch, /127\.0\.0\.1:18080/);
  assert.match(patch, /team-provider/);
  assert.doesNotMatch(patch, /password|secret|token|network|wssUrl/);
});

test('stop refuses reused PID and never kills unrelated process', async () => {
  const calls = [];
  await assert.rejects(
    stopOwnedProcess(
      { pid: 42, identity: 'old' },
      {
        identity: () => 'new',
        kill: (pid) => calls.push(pid),
      },
    ),
    /PID/,
  );
  assert.deepEqual(calls, []);
  let alive = true;
  await stopOwnedProcess(
    { pid: 42, identity: 'ours' },
    {
      identity: () => (alive ? 'ours' : null),
      kill: (pid) => {
        calls.push(pid);
        alive = false;
      },
    },
  );
  assert.deepEqual(calls, [42]);
  await stopOwnedProcess(null);
});
