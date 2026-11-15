/* global fetch, Response */
import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter, once } from 'node:events';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { generateKeyPairSync } from 'node:crypto';
import { existsSync, readdirSync, writeFileSync } from 'node:fs';
import { setTimeout as delay } from 'node:timers/promises';
import { join } from 'node:path';
import {
  CancelledError,
  install,
  platformStatus,
  profileDir,
  readState,
  uninstall,
} from '../runtime/core.mjs';
import { createControl } from '../runtime/tray-control.mjs';
import { superviseDsh } from '../runtime/desktop-supervision.mjs';
import { shellAuthFailureReason } from '../runtime/shell-auth.mjs';
import { desktopInfo, updateCommand } from '../runtime/desktop-control.mjs';
import { idleRequest } from '../runtime/update.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

const noSmoke = () => {};

test('install reports ordered steps with copy fractions for the wizard', (t) => {
  const base = tempBase(t);
  const p = buildPayload(base, { name: 'a' });
  const seen = [];
  install({
    payload: p.dir,
    root: join(base, 'app'),
    dataHome: join(base, 'home'),
    smoke: noSmoke,
    progress: (stage, fraction) => seen.push([stage, fraction]),
  });
  const stages = [...new Set(seen.map(([stage]) => stage))];
  assert.deepEqual(stages, [
    'verify',
    'copy',
    'verify-copy',
    'profile',
    'smoke',
    'commit',
    'done',
  ]);
  const copy = seen.filter(([stage]) => stage === 'copy').map(([, f]) => f);
  assert.ok(copy.length > 1);
  assert.deepEqual(
    copy,
    [...copy].sort((a, b) => a - b),
  );
  assert.equal(copy.at(-1), 1);
});

for (const at of ['verify', 'copy', 'verify-copy', 'profile', 'smoke'])
  test(`cancelling a first install at ${at} leaves no installation or data behind`, (t) => {
    const base = tempBase(t);
    const root = join(base, 'app'),
      dataHome = join(base, 'home');
    const p = buildPayload(base, { name: 'a' });
    let stop = false;
    assert.throws(
      () =>
        install({
          payload: p.dir,
          root,
          dataHome,
          smoke: noSmoke,
          progress: (stage) => {
            if (stage === at) stop = true;
          },
          cancelled: () => stop,
        }),
      CancelledError,
    );
    assert.equal(existsSync(root), false);
    assert.equal(existsSync(dataHome), false);
  });

test('cancelled first install keeps a pre-existing user folder and data untouched', (t) => {
  const base = tempBase(t);
  const root = join(base, 'app'),
    dataHome = join(base, 'home');
  const p = buildPayload(base, { name: 'a' });
  install({ payload: p.dir, root, dataHome, smoke: noSmoke });
  writeFileSync(join(profileDir(dataHome), 'conversation.json'), 'keep');
  // A second root that already exists (empty) and shares nothing.
  const other = join(base, 'other');
  const otherHome = join(base, 'other-home');
  writeFileSync(join(base, 'placeholder'), '');
  let stop = false;
  assert.throws(
    () =>
      install({
        payload: p.dir,
        root: other,
        dataHome: otherHome,
        smoke: noSmoke,
        progress: (stage) => {
          if (stage === 'smoke') stop = true;
        },
        cancelled: () => stop,
      }),
    CancelledError,
  );
  assert.equal(existsSync(other), false);
  assert.equal(readState(root).dataHome, dataHome);
});

test('cancelling an upgrade rolls back to the old version with user data intact', (t) => {
  const base = tempBase(t);
  const root = join(base, 'app'),
    dataHome = join(base, 'home');
  const a = buildPayload(base, { name: 'a' });
  install({ payload: a.dir, root, dataHome, smoke: noSmoke });
  writeFileSync(join(profileDir(dataHome), 'conversation.json'), 'keep');
  const b = buildPayload(base, { name: 'b', pluginVersion: '1.1.0' });
  let stop = false;
  assert.throws(
    () =>
      install({
        payload: b.dir,
        root,
        dataHome,
        smoke: noSmoke,
        progress: (stage) => {
          if (stage === 'smoke') stop = true;
        },
        cancelled: () => stop,
      }),
    CancelledError,
  );
  assert.equal(readState(root).current, a.id);
  assert.deepEqual(readdirSync(join(root, 'versions')), [a.id]);
  assert.ok(existsSync(join(profileDir(dataHome), 'conversation.json')));
  assert.ok(existsSync(join(root, '.auriga-desktop-install')));
});

test('tray control: stop line, end of stdin and named commands', async () => {
  const input = new EventEmitter();
  const written = [];
  const control = createControl({ input, write: (s) => written.push(s) });
  let logins = 0;
  control.on('login', () => logins++);
  input.emit('data', 'log');
  input.emit('data', 'in\r\nunknown\n');
  assert.equal(logins, 1);
  assert.equal(control.requested, false);
  control.report({ event: 'platform', state: 'signed_out' });
  assert.deepEqual(written, [
    '@@auriga {"event":"platform","state":"signed_out"}\n',
  ]);
  input.emit('data', 'stop\n');
  assert.equal(control.requested, true);
  const crashed = new EventEmitter();
  const second = createControl({ input: crashed, write: () => {} });
  crashed.emit('end');
  assert.equal(second.requested, true);
});

test('supervisor: an owner stop request ends DSH and returns a clean exit', async (t) => {
  let child,
    starts = 0,
    stops = 0,
    requested = false;
  t.after(() => child?.kill());
  const tree = {
    attach() {},
    refresh() {},
    async stopTree() {
      stops++;
      if (child && child.exitCode === null && child.signalCode === null) {
        const done = once(child, 'exit');
        child.kill();
        await done;
      }
    },
  };
  const running = superviseDsh({
    tree,
    supervisor: { reconcile: async () => ({}) },
    session: { child() {} },
    notify() {},
    poll: () => delay(10),
    stopRequested: () => requested,
    start: () => {
      starts++;
      child = spawn(process.execPath, ['-e', 'setInterval(()=>{},1000)'], {
        stdio: 'ignore',
      });
      return child;
    },
  });
  await delay(150);
  requested = true;
  assert.equal(await running, 0);
  assert.equal(starts, 1);
  assert.ok(stops >= 1);
  assert.ok(child.exitCode !== null || child.signalCode !== null);
  assert.equal(
    await superviseDsh({
      tree,
      supervisor: {},
      session: {},
      notify() {},
      stopRequested: () => true,
      start: () => assert.fail('must not start after a stop request'),
    }),
    0,
  );
});

test('platform status exposes only state and fixed reason codes', () => {
  assert.deepEqual(platformStatus(undefined, 'origin_rejected'), {
    state: 'unavailable',
    reason: 'origin_rejected',
  });
  assert.deepEqual(
    platformStatus({
      snapshot: () => ({
        state: 'authenticated',
        identity: { tenantId: 't', memberId: 'm' },
        shellEpoch: 'e',
      }),
    }),
    { state: 'authenticated' },
  );
  assert.deepEqual(
    platformStatus({
      snapshot: () => ({ state: 'unreachable', reason: 'offline' }),
    }),
    { state: 'unreachable', reason: 'offline' },
  );
  assert.deepEqual(
    platformStatus({
      snapshot: () => {
        throw new Error('secret config');
      },
    }),
    { state: 'unavailable', reason: 'snapshot_failed' },
  );
  const cases = [
    [new Error('trusted HTTPS platform origin required'), 'origin_rejected'],
    [new Error('Windows shell helper required'), 'helper_missing'],
    [
      new Error('Command failed: C:\\x\\shell-helper.exe secure C:\\y'),
      'acl_failed',
    ],
    [new Error('reparse path rejected'), 'reparse_rejected'],
    [
      Object.assign(new Error('x'), { code: 'ERR_MODULE_NOT_FOUND' }),
      'component_missing',
    ],
    [new Error('token=abc secret'), 'unavailable'],
  ];
  for (const [error, code] of cases)
    assert.equal(shellAuthFailureReason(error), code);
});

test('idle transport is origin bound, omits credentials and forbids redirects', async () => {
  const seen = [];
  const request = idleRequest('https://example.com', {
    fetcher: async (url, init) => {
      seen.push([url, init]);
      return new Response('{}', { status: 200 });
    },
  });
  await assert.rejects(request('https://elsewhere.example.com/x'), /untrusted/);
  const response = await request('https://example.com/api', {});
  const chunks = [];
  for await (const chunk of response.body) chunks.push(chunk);
  assert.equal(Buffer.concat(chunks).toString(), '{}');
  assert.equal(seen[0][1].credentials, 'omit');
  assert.equal(seen[0][1].redirect, 'error');
});

test('idle transport aborts a stalled body after the idle window only', async (t) => {
  const server = createServer((req, res) => {
    res.write('a');
    setTimeout(() => res.write('b'), 50);
    // Never ends: the idle timer must fire after the last chunk.
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const response = await idleRequest(origin, { idleMs: 300 })(`${origin}/x`);
  const received = [];
  await assert.rejects(async () => {
    for await (const chunk of response.body)
      received.push(Buffer.from(chunk).toString());
  });
  assert.deepEqual(received.join(''), 'ab');
});

test('tray status and update commands use the packaged key and verified origin', async (t) => {
  const base = tempBase(t);
  const root = join(base, 'app'),
    dataHome = join(base, 'home');
  const keys = generateKeyPairSync('ed25519');
  const publicPem = keys.publicKey.export({ type: 'spki', format: 'pem' });
  const a = buildPayload(base, { name: 'a', releasePublicKey: publicPem });
  install({
    payload: a.dir,
    root,
    dataHome,
    smoke: noSmoke,
    platformOrigin: 'https://example.com',
  });
  const info = desktopInfo(root);
  assert.equal(info.version, '1.0.0');
  assert.equal(info.platformOrigin, 'https://example.com');
  assert.equal(info.updatesConfigured, true);
  assert.equal(info.components.dsh, '0.2.1-rc.3');
  assert.equal(info.update.available, null);
  assert.equal(info.logs, join(dataHome, 'auriga', 'logs'));
  const printed = [];
  const urls = [];
  await updateCommand(root, 'check', {
    publicKey: keys.publicKey,
    print: (value) => printed.push(value),
    request: async (url) => {
      urls.push(url);
      return { ok: false, status: 404 };
    },
  });
  assert.deepEqual(urls, [
    'https://example.com/api/resource/v1/desktop/latest',
  ]);
  assert.equal(printed[0].available, null);
  await updateCommand(root, 'configure', {
    enabled: false,
    publicKey: keys.publicKey,
    print: (value) => printed.push(value),
  });
  assert.equal(printed[1].enabled, false);
  assert.equal(desktopInfo(root).update.enabled, false);
  await assert.rejects(
    updateCommand(root, 'check', {
      publicKey: keys.publicKey,
      print() {},
      request: async () => ({
        ok: true,
        status: 200,
        body: [Buffer.from(JSON.stringify({ version: '9.9.9' }))],
      }),
    }),
    /signature/,
  );
});

test('unconfigured platform address refuses update commands with a fixed code', async (t) => {
  const base = tempBase(t);
  const root = join(base, 'app');
  const a = buildPayload(base, { name: 'a' });
  install({
    payload: a.dir,
    root,
    dataHome: join(base, 'home'),
    smoke: noSmoke,
  });
  assert.equal(desktopInfo(root).updatesConfigured, false);
  await assert.rejects(updateCommand(root, 'check', { print() {} }), {
    code: 'desktop.update_unconfigured',
  });
  assert.equal(typeof fetch, 'function');
});

test(
  'uninstall with program files in use changes nothing and reports desktop.in_use',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const base = tempBase(t);
    const root = join(base, 'app'),
      dataHome = join(base, 'home');
    const a = buildPayload(base, { name: 'a' });
    install({ payload: a.dir, root, dataHome, smoke: noSmoke });
    // A running process whose working directory is inside the version blocks the rename.
    const holder = spawn(process.execPath, ['-e', 'setInterval(()=>{},1000)'], {
      cwd: join(root, 'versions', a.id),
      stdio: 'ignore',
    });
    t.after(() => holder.kill());
    await delay(300);
    assert.throws(() => uninstall({ root, purgeData: true }), {
      code: 'desktop.in_use',
    });
    assert.equal(readState(root).current, a.id);
    assert.ok(existsSync(join(root, 'versions', a.id, 'manifest.json')));
    assert.ok(existsSync(profileDir(dataHome)));
    holder.kill();
    await once(holder, 'exit');
    await delay(200);
    assert.equal(uninstall({ root }).pending.length, 0);
    assert.equal(existsSync(root), false);
  },
);
