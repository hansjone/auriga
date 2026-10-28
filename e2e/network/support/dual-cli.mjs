/* global fetch, AbortSignal */
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID, createHmac } from 'node:crypto';
import { createHostOutput } from '../../../scripts/beta-ui/plugin-dsh.mjs';
import { isolatedEnvironment } from './native-install.mjs';
import { waitUntil } from './assembly-process.mjs';

export function authenticatorCode(secret, now = Date.now()) {
  assert.match(secret, /^[A-Z2-7]{16,64}$/);
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567',
    bytes = [];
  let bits = 0,
    value = 0;
  for (const char of secret) {
    value = (value << 5) | alphabet.indexOf(char);
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((value >>> bits) & 255);
    }
  }
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(now / 30000)));
  const hash = createHmac('sha1', Buffer.from(bytes)).update(counter).digest(),
    offset = hash.at(-1) & 15;
  return String((hash.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(
    6,
    '0',
  );
}

// The CLI prints its URL before asynchronous profile injection/configuration
// finishes. Use the public settings lifecycle, never retry a failed login call.
export async function readyConnectProfile(
  request,
  connect,
  name,
  check = () => {},
) {
  const settings = await waitUntil(async () => {
    check();
    const value = await request('/auriga/settings/state');
    assert.equal(
      value.platformOrigin,
      connect.platformOrigin,
      name + ' profile platform',
    );
    assert.equal(
      value.deviceName,
      connect.deviceName ?? 'Auriga DSH',
      name + ' profile device',
    );
    if (!value.activeOrigin) return null;
    assert.equal(
      value.activeOrigin,
      connect.platformOrigin,
      name + ' active platform',
    );
    return value;
  }, name + ' configured profile never became active');
  const status = await request('/auriga/local-login/status');
  assert.equal(status.available, true, name + ' native login available');
  assert.equal(
    status.loggedIn,
    false,
    name + ' fresh profile must not be signed in',
  );
  return {
    settings,
    status: { available: status.available, loggedIn: status.loggedIn },
  };
}

export async function launchCli({
  root,
  state,
  runtime,
  ca,
  connect,
  artifacts,
  name,
}) {
  const run = promisify(execFile),
    env = {
      ...isolatedEnvironment(state),
      COREPACK_HOME: join(runtime, 'cache/corepack'),
      NODE_EXTRA_CA_CERTS: ca,
      AURIGA_SEND4_STATE: state,
      AURIGA_MODE: 'connect',
    };
  for (const dir of [
    'cwd',
    'dsh',
    'home',
    'config',
    'tmp',
    'cache',
    'session-cwd',
    'other-cwd',
  ])
    await mkdir(join(state, dir), { recursive: true });
  for (const file of [env.npm_config_userconfig, env.npm_config_globalconfig])
    await writeFile(file, '', { mode: 0o600 });
  const pkg = JSON.parse(
    await readFile(
      join(runtime, 'node_modules/@deepseek-ai/dsh/package.json'),
      'utf8',
    ),
  );
  assert.equal(pkg.version, '0.2.1-rc.3');
  const bin = join(runtime, 'node_modules/@deepseek-ai/dsh', pkg.bin.dsh);
  const invoke = async (args, label) => {
    const out = await run(process.execPath, [bin, ...args], {
      cwd: join(state, 'cwd'),
      env,
      windowsHide: true,
      timeout: 120000,
      maxBuffer: 8 * 1024 * 1024,
    });
    await writeFile(
      join(artifacts, `${name}-${label}.log`),
      out.stdout + out.stderr,
    );
  };
  await invoke(['--profile', 'web', '--dump-config'], 'profile');
  const profile = join(state, 'dsh/profiles/web');
  const file = join(profile, 'package.json');
  await writeFile(
    file,
    JSON.stringify({
      ...JSON.parse(await readFile(file, 'utf8')),
      packageManager: 'pnpm@11.28.6',
    }),
  );
  await invoke(
    [
      'plugin',
      '--profile',
      'web',
      'add',
      join(root, 'clients/dsh-plugin'),
      join(root, 'e2e/network/support/dual-observer'),
      '--ignore-scripts',
      '--store-dir',
      join(state, 'cache/pnpm'),
    ],
    'install',
  );
  await writeFile(
    join(profile, 'cordis.patch.yml'),
    '- id: auriga-dsh\n  disabled: false\n  config: ' +
      JSON.stringify({ connect }) +
      '\n',
  );
  // Like the proven Beta installer, record the effective configuration AFTER
  // plugin installation and the profile patch, rather than only the empty base.
  await invoke(['--profile', 'web', '--dump-config'], 'configured-profile');
  const child = spawn(
    process.execPath,
    [
      bin,
      '--profile',
      'web',
      '--host',
      '127.0.0.1',
      '--port',
      '0',
      '--no-open',
    ],
    {
      cwd: join(state, 'cwd'),
      env,
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
    },
  );
  let launch,
    closed,
    observerReady = false,
    logs = '';
  child.on('message', (message) => {
    if (message?.type === 'send4-observer-ready') observerReady = true;
  });
  const done = new Promise((resolve) =>
    child.once('close', (code, signal) => {
      closed = { code, signal };
      resolve(closed);
    }),
  );
  child.on('error', (error) => {
    closed = { error: error.code };
  });
  const capture = createHostOutput(
    (value) => {
      logs += value;
    },
    (url) => {
      launch ??= url;
    },
  );
  for (const stream of [child.stdout, child.stderr])
    stream.on('data', (chunk) => capture.write(chunk));
  let origin;
  const cookies = new Map();
  const accept = (response) => {
    for (const line of response.headers.getSetCookie()) {
      const pair = line.split(';')[0],
        at = pair.indexOf('=');
      cookies.set(pair.slice(0, at), pair.slice(at + 1));
    }
  };
  const request = async (path, body, csrf) => {
    const response = await fetch(origin + path, {
      method: body === undefined ? 'GET' : 'POST',
      redirect: 'manual',
      signal: AbortSignal.timeout(15000),
      headers: {
        cookie: [...cookies].map(([k, v]) => k + '=' + v).join('; '),
        ...(body === undefined
          ? {}
          : {
              origin,
              'content-type': 'application/json',
              ...(csrf ? { 'x-auriga-csrf': csrf } : {}),
            }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    accept(response);
    const text = await response.text();
    assert.equal(
      response.status,
      200,
      `${name} ${path}: ${response.status} ${text}`,
    );
    return JSON.parse(text);
  };
  const stop = async () => {
    try {
      if (!closed) {
        assert.ok(
          observerReady && child.connected,
          name + ' owned shutdown channel unavailable',
        );
        child.send({ type: 'send4-stop' });
        const ended = await Promise.race([
          done,
          new Promise((resolve) => {
            const timer = setTimeout(() => resolve(null), 10000);
            timer.unref();
          }),
        ]);
        if (!ended) {
          child.kill();
          await done;
          throw new Error(name + ' required forced stop');
        }
      }
      assert.deepEqual(
        closed,
        { code: 0, signal: null },
        name + ' CLI cleanup',
      );
    } catch (error) {
      if (!closed) {
        child.kill();
        await done;
      }
      throw error;
    } finally {
      capture.end();
      await writeFile(join(artifacts, name + '-host.log'), logs);
    }
  };
  try {
    await waitUntil(() => {
      if (closed) throw new Error(name + ' CLI exited; ' + logs.slice(-2000));
      return launch;
    }, name + ' CLI startup URL missing');
    origin = new URL(launch).origin;
    const response = await fetch(launch, {
      redirect: 'manual',
      signal: AbortSignal.timeout(5000),
    });
    accept(response);
    await response.arrayBuffer();
    const checkProcess = () => {
      if (closed) throw new Error(name + ' CLI exited before profile ready');
    };
    await waitUntil(() => {
      checkProcess();
      return observerReady;
    }, name + ' native profile providers unavailable');
    let readiness;
    if (connect) {
      readiness = await readyConnectProfile(
        request,
        connect,
        name,
        checkProcess,
      );
      await writeFile(
        join(artifacts, name + '-profile-ready.json'),
        JSON.stringify(
          {
            pid: child.pid,
            profile,
            ...readiness,
          },
          null,
          2,
        ),
      );
    }
    return {
      name,
      state,
      profile,
      pid: child.pid,
      origin,
      readiness,
      request,
      stop,
      ledgerFile: join(profile, 'auriga-connect/network/host/ledger.json'),
      ledger: async () =>
        JSON.parse(
          await readFile(
            join(profile, 'auriga-connect/network/host/ledger.json'),
            'utf8',
          ),
        ),
      async login(username, password) {
        let status = await request('/auriga/local-login/status');
        const begin = await request(
          '/auriga/local-login/login',
          { action: 'begin' },
          status.csrf,
        );
        assert.equal(begin.step.type, 'form');
        let result = await request(
          '/auriga/local-login/login',
          {
            action: 'advance',
            input: {
              type: 'form',
              formId: begin.step.formId,
              values: { username, password },
            },
          },
          status.csrf,
        );
        assert.equal(
          result.step.formId,
          'first_totp',
          'normal native login requires first authenticator enrollment',
        );
        const enrollment = result.step.fields.find(
          (field) => field.name === 'enrollment',
        )?.label.en;
        const secret = enrollment?.match(/([A-Z2-7]{32})$/)?.[1];
        assert.ok(
          secret,
          'normal enrollment secret must come from the production login form',
        );
        result = await request(
          '/auriga/local-login/login',
          {
            action: 'advance',
            input: {
              type: 'form',
              formId: 'first_totp',
              values: { otp: authenticatorCode(secret) },
            },
          },
          status.csrf,
        );
        assert.equal(result.step.type, 'complete');
        status = await waitUntil(async () => {
          const value = await request('/auriga/local-login/status');
          assert.equal(value.loggedIn, true);
          return value.network.connected && value.connected ? value : null;
        }, name + ' normal login/network not connected');
        return { subject: status.account.subject, network: status.network };
      },
      control: (command) => request('/send4/control', command),
      tool: (session, name, args = {}) =>
        request('/send4/control', {
          op: 'tool',
          session,
          name,
          arguments: args,
          callId: randomUUID(),
        }),
      async receive(local, enabled) {
        const status = await request('/auriga/local-login/status');
        return request(
          '/auriga/local-login/messaging/receive',
          { local, enabled },
          status.csrf,
        );
      },
    };
  } catch (error) {
    try {
      await stop();
    } catch (cleanup) {
      throw new AggregateError(
        [error, cleanup],
        name + ' startup and cleanup failed: ' + error.message,
      );
    }
    throw error;
  }
}
