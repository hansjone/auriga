import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign, createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { join } from 'node:path';
import {
  existsSync,
  writeFileSync,
  readFileSync,
  readdirSync,
  mkdirSync,
  renameSync,
} from 'node:fs';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { clearTimeout } from 'node:timers';
import { rmSync } from 'node:fs';
import {
  changeUpdateState,
  cleanupDownloads,
  downloadLock,
  readUpdateState,
} from '../runtime/update-state.mjs';
import { downloadPackage } from '../runtime/update.mjs';
import {
  canonical,
  verifyRelease,
  RELEASE_KID,
  releaseDigest,
} from '../runtime/release.mjs';
import {
  createUpdater,
  platformUrl,
  applyPending,
  approvePending,
} from '../runtime/update.mjs';
import {
  install,
  readState,
  profileDir,
  desktopSession,
  uninstall,
  writeAtomic,
} from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';
import {
  desktopPlatform,
  hostDesktopTarget,
} from '../runtime/desktop-platform.mjs';

const keys = generateKeyPairSync('ed25519');
const now = Date.parse('2026-11-12T00:00:00Z');
function signed(changes = {}) {
  const body = {
    schema: 1,
    product: 'Auriga Desktop',
    version: '2.0.0',
    packageId: 'package-2',
    components: {
      dshVersion: '0.2.1-rc.3',
      nodeVersion: '24.0.0',
      pluginVersion: '2.0.0',
    },
    channel: 'stable',
    platform: hostDesktopTarget(),
    sizeBytes: 3,
    sha256: createHash('sha256').update('zip').digest('hex'),
    minimumVersion: '1.0.0',
    publishedAt: '2026-11-11T00:00:00Z',
    notAfter: '2026-12-01T00:00:00Z',
    sequence: 2,
    notes: '新版本',
    ...changes,
  };
  return {
    ...body,
    signature: {
      alg: 'Ed25519',
      kid: RELEASE_KID,
      value: sign(null, Buffer.from(canonical(body)), keys.privateKey).toString(
        'base64url',
      ),
    },
  };
}
const verify = (value, options = {}) =>
  verifyRelease(value, {
    publicKey: keys.publicKey,
    now,
    currentVersion: '1.0.0',
    ...options,
  });
test('签名、篡改、降级、到期、未来、序号重放均拒绝', () => {
  assert.equal(verify(signed()).version, '2.0.0');
  assert.throws(() => verify({ ...signed(), notes: '篡改' }), /signature/);
  assert.throws(
    () =>
      verify(signed(), { publicKey: generateKeyPairSync('ed25519').publicKey }),
    /signature/,
  );
  assert.throws(() => verify(signed({ version: '0.9.0' })), /downgrade/);
  assert.equal(
    verify(signed({ minimumVersion: '1.5.0' })).minimumVersion,
    '1.5.0',
  );
  assert.throws(() => verify(signed({ minimumVersion: '3.0.0' })), /minimum/);
  assert.throws(
    () => verify(signed({ notAfter: '2026-11-11T01:00:00Z' })),
    /expired/,
  );
  assert.throws(
    () => verify(signed({ publishedAt: '2026-11-13T00:00:00Z' })),
    /future/,
  );
  assert.throws(() => verify(signed(), { highestSequence: 3 }), /replay/);
  assert.throws(
    () =>
      verify(signed(), { highestSequence: 2, acceptedDigest: '0'.repeat(64) }),
    /replay/,
  );
  assert.equal(
    verify(signed(), { highestSequence: 2, acceptedDigest: signed().sha256 })
      .sequence,
    2,
  );
  assert.throws(() => verifyRelease(signed()), /untrusted/);
});
test('更新只连接固定 HTTPS origin', () => {
  assert.equal(
    platformUrl('https://example.com', 'latest'),
    'https://example.com/api/resource/v1/desktop/latest',
  );
  for (const origin of [
    'http://example.com',
    'https://example.com/path',
    'https://alice@example.com',
    'https://example.com/',
  ])
    assert.throws(() => platformUrl(origin, 'latest'));
});

test('同一安装目录拒绝同时启动第二个 DSH，退出后可重开', (t) => {
  const base = tempBase(t);
  const session = desktopSession(base);
  assert.throws(() => desktopSession(base), /已在运行/);
  session.child(process.pid);
  session.close();
  const reopened = desktopSession(base);
  reopened.close();
});

test('另一个进程延迟返回的已签名旧发行不能回退水位或覆盖设置', async (t) => {
  const f = fixture(t);
  const origin = 'https://example.com';
  const runtime = new URL('../runtime/update.mjs', import.meta.url).href;
  const publicKey = keys.publicKey.export({ type: 'spki', format: 'pem' });
  const args = { root: f.root, origin, publicKey };
  const worker = spawn(
    process.execPath,
    [
      '--input-type=module',
      '-e',
      `import { once } from 'node:events';import { createUpdater } from ${JSON.stringify(runtime)};
    const updater=createUpdater({...(${JSON.stringify(args)}),now:()=>${now},request: async()=>{const response=once(process,'message');process.send('requested');return Response.json((await response)[0]);}});
    try {await updater.check();process.send('accepted');} catch(error) {process.send(error.message);} finally {process.disconnect();}`,
    ],
    { stdio: ['ignore', 'ignore', 'inherit', 'ipc'] },
  );
  t.after(() => worker.kill());
  assert.equal((await once(worker, 'message'))[0], 'requested');
  const newer = signed({
    sequence: 3,
    publishedAt: '2026-11-11T12:00:00Z',
  });
  const tray = createUpdater({
    ...args,
    now: () => now,
    request: async () => Response.json(newer),
  });
  await tray.check();
  tray.configure(false);
  const result = once(worker, 'message');
  worker.send(signed());
  assert.match((await result)[0], /replay/);
  const state = readUpdateState(f.root);
  assert.equal(state.channelWatermarks.stable.highestSequence, 3);
  assert.equal(state.available.sequence, 3);
  assert.equal(state.enabled, false);
});

test('启动第二个更新器不会把仍在下载的进度覆盖为取消', (t) => {
  const f = fixture(t);
  const release = downloadLock(f.root);
  try {
    changeUpdateState(f.root, (state) => ({
      ...state,
      phase: 'downloading',
      downloadedBytes: 2,
    }));
    const second = createUpdater({
      root: f.root,
      origin: 'https://example.com',
      publicKey: keys.publicKey,
    });
    assert.equal(second.status().phase, 'downloading');
    assert.equal(second.status().downloadedBytes, 2);
  } finally {
    release();
  }
  createUpdater({
    root: f.root,
    origin: 'https://example.com',
    publicKey: keys.publicKey,
  });
  assert.equal(readUpdateState(f.root).phase, 'cancelled');
});

test('旧版全局水位迁入 stable，继续拒绝重放', async (t) => {
  const f = fixture(t);
  const file = join(f.root, 'desktop-update.json');
  writeFileSync(
    file,
    JSON.stringify({
      enabled: true,
      highestSequence: 7,
      highestPublishedAt: '2026-11-11T00:00:00Z',
      highestVersion: '2.0.0',
      acceptedDigest: signed().sha256,
    }),
  );
  const updater = createUpdater({
    root: f.root,
    origin: 'https://example.com',
    publicKey: keys.publicKey,
    now: () => now,
    request: async () => Response.json(signed()),
  });
  await assert.rejects(updater.check(), /replay/);
  assert.equal(
    readUpdateState(f.root).channelWatermarks.stable.highestSequence,
    7,
  );
});

function fixture(
  t,
  {
    badSha = false,
    badId = false,
    interrupted = false,
    smokeFails = false,
  } = {},
) {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'data');
  const old = buildPayload(base, { name: 'old' }),
    next = buildPayload(base, { name: 'new', pluginVersion: '2.0.0' });
  install({
    root,
    dataHome,
    payload: old.dir,
    platformOrigin: 'https://example.com',
    smoke: () => {},
  });
  writeFileSync(join(profileDir(dataHome), 'user-data.txt'), 'keep');
  const manifest = signed({
    packageId: badId ? 'wrong-id' : next.id,
    sha256: badSha ? '0'.repeat(64) : signed().sha256,
  });
  let attempts = 0;
  const updater = createUpdater({
    root,
    defer: false,
    origin: 'https://example.com',
    publicKey: keys.publicKey,
    now: () => now,
    request: async (url) => {
      if (/\/latest(?:\?|$)/.test(url))
        return new Response(JSON.stringify(manifest));
      attempts++;
      if (interrupted && attempts <= 3) return new Response('zi');
      return new Response('zip');
    },
    extract: () => next.dir,
    smoke: () => {
      if (smokeFails) throw new Error('smoke failed');
    },
  });
  return { updater, root, dataHome, old, next };
}
test('成功更新保留数据并提示重启', async (t) => {
  const f = fixture(t);
  await f.updater.check();
  assert.equal(f.updater.status().available.version, '2.0.0');
  assert.equal((await f.updater.update()).restartRequired, true);
  assert.equal(readState(f.root).current, f.next.id);
  assert.equal(
    readFileSync(join(profileDir(f.dataHome), 'user-data.txt'), 'utf8'),
    'keep',
  );
});
for (const bad of ['badSha', 'badId', 'smokeFails'])
  test(bad + ' 拒绝并保持旧版且不留新版本目录', async (t) => {
    const f = fixture(t, { [bad]: true });
    await f.updater.check();
    await assert.rejects(f.updater.update());
    assert.equal(
      readUpdateState(f.root).lastAttempt.result,
      bad === 'smokeFails' ? 'rolled_back' : 'failed',
    );
    assert.equal(readUpdateState(f.root).lastAttempt.targetVersion, '2.0.0');
    assert.equal(readState(f.root).current, f.old.id);
    assert.equal(existsSync(join(f.root, 'versions', f.next.id)), false);
    assert.equal(
      readdirSync(f.root).some((n) => n.startsWith('.desktop-download-')),
      false,
    );
  });
test('下载中断后重试成功，关闭可选更新后不再提示', async (t) => {
  const f = fixture(t, { interrupted: true });
  await f.updater.check();
  await assert.rejects(f.updater.update(), /interrupted/);
  await f.updater.update();
  assert.equal(readUpdateState(f.root).lastAttempt.result, 'succeeded');
  assert.equal(readState(f.root).current, f.next.id);
  f.updater.configure(false);
  assert.equal(f.updater.status().enabled, false);
  await f.updater.check();
  assert.equal(f.updater.status().available, null);
});

test('真实子进程被杀后锁失效，下次更新成功并清理残留', async (t) => {
  const f = fixture(t);
  const module = new URL('../runtime/update-state.mjs', import.meta.url).href;
  const child = spawn(process.execPath, [
    '--input-type=module',
    '-e',
    `import { downloadLock } from ${JSON.stringify(module)}; downloadLock(${JSON.stringify(f.root)}); console.log('ready'); setInterval(()=>{},1000);`,
  ]);
  t.after(() => {
    if (child.exitCode === null) child.kill();
  });
  await once(child.stdout, 'data');
  const stale = join(f.root, '.desktop-download-killed');
  mkdirSync(stale);
  writeFileSync(join(stale, 'partial'), 'bytes');
  assert.equal(cleanupDownloads(f.root), false); // live owner is protected
  child.kill();
  await once(child, 'exit');
  await f.updater.check();
  await f.updater.update();
  assert.equal(readState(f.root).current, f.next.id);
  assert.equal(existsSync(stale), false);
  assert.equal(existsSync(join(f.root, '.desktop-update-lock')), false);
});

test('启动前清理空锁等崩溃残留；卸载删除下载残留且保留用户数据', (t) => {
  const f = fixture(t);
  writeFileSync(join(f.root, '.desktop-update-lock'), '');
  mkdirSync(join(f.root, '.desktop-download-empty'));
  cleanupDownloads(f.root, { stopped: true });
  const release = downloadLock(f.root);
  const lock = JSON.parse(
    readFileSync(join(f.root, '.desktop-update-lock'), 'utf8'),
  );
  assert.equal(lock.pid, process.pid);
  assert.ok(lock.createdAt > 0);
  release();
  mkdirSync(join(f.root, '.desktop-download-uninstall'));
  uninstall({ root: f.root });
  assert.equal(existsSync(f.root), false);
  assert.equal(
    readFileSync(join(profileDir(f.dataHome), 'user-data.txt'), 'utf8'),
    'keep',
  );
});

test('Range 续传拼接完整包；忽略 Range 时安全重下；错误 Range 拒绝', async (t) => {
  const base = tempBase(t);
  for (const mode of ['resume', 'ignored', 'invalid']) {
    const file = join(base, mode);
    let attempts = 0;
    const request = async (_url, init) => {
      attempts++;
      if (attempts === 1) return new Response('zi');
      assert.equal(init.headers.Range, 'bytes=2-');
      if (mode === 'ignored') return new Response('zip');
      return new Response('p', {
        status: 206,
        headers: {
          'Content-Range': mode === 'invalid' ? 'bytes 1-2/3' : 'bytes 2-2/3',
        },
      });
    };
    if (mode === 'invalid')
      await assert.rejects(
        downloadPackage(request, 'https://example.com', file, 3),
        /content range/,
      );
    else {
      await downloadPackage(request, 'https://example.com', file, 3);
      assert.equal(readFileSync(file, 'utf8'), 'zip');
    }
    assert.equal(attempts, 2);
  }
});

test('同版本同包更高序号续期不会重复更新，周期检查不重新哈希安装树', async (t) => {
  const f = fixture(t);
  await f.updater.check();
  await f.updater.update();
  // Damage a file to detect accidental full hashing in a routine check.
  writeFileSync(
    join(f.root, 'versions', f.next.id, 'auriga/runtime/core.mjs'),
    'changed',
  );
  const updater = createUpdater({
    root: f.root,
    origin: 'https://example.com',
    publicKey: keys.publicKey,
    now: () => now,
    request: async () =>
      new Response(
        JSON.stringify(
          signed({
            sequence: 3,
            packageId: f.next.id,
            notAfter: '2027-01-01T00:00:00Z',
          }),
        ),
      ),
  });
  await updater.check();
  assert.equal(updater.status().available, null);
  assert.equal(
    JSON.parse(readFileSync(join(f.root, 'desktop-update.json')))
      .channelWatermarks.stable.highestSequence,
    3,
  );
});

test('HTTP requires confirmed literal IP binding; domain HTTP stays forbidden', () => {
  const origin = 'http://192.168.2.10:8080'; // gitleaks:allow
  const binding = { accessMode: 'ip', confirmedOrigin: origin };
  assert.equal(
    platformUrl(origin, 'latest', binding),
    origin + '/api/resource/v1/desktop/latest',
  );
  for (const policy of [
    {},
    { ...binding, accessMode: 'domain' },
    { ...binding, confirmedOrigin: 'http://192.168.2.11:8080' }, // gitleaks:allow
  ])
    assert.throws(() => platformUrl(origin, 'latest', policy));
  assert.throws(() =>
    platformUrl('http://example.com', 'latest', {
      accessMode: 'ip',
      confirmedOrigin: 'http://example.com',
    }),
  );
  assert.throws(
    () => verify({ ...signed(), publishedAt: '2026-11-10T00:00:00Z' }),
    /signature/,
  );
  assert.throws(
    () =>
      verify(signed({ sequence: 5 }), {
        highestPublishedAt: '2026-11-11T01:00:00Z',
      }),
    /replay/,
  );
});

test('HTTP install confirmation persists once and updater uses it', async (t) => {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'home');
  const payload = buildPayload(base);
  const origin = 'http://192.168.2.10:8080'; // gitleaks:allow
  assert.throws(() =>
    install({
      root,
      dataHome,
      payload: payload.dir,
      platformOrigin: origin,
      smoke: () => {},
    }),
  );
  assert.equal(existsSync(root), false);
  install({
    root,
    dataHome,
    payload: payload.dir,
    platformOrigin: origin,
    platformBinding: { accessMode: 'ip', confirmedOrigin: origin },
    smoke: () => {},
  });
  const updater = createUpdater({
    root,
    origin,
    now: () => now,
    publicKey: keys.publicKey,
    request: async (url) => {
      assert.equal(
        url,
        origin +
          `/api/resource/v1/desktop/latest?platform=${hostDesktopTarget()}`,
      );
      return new Response(JSON.stringify(signed()));
    },
  });
  await updater.check();
  assert.equal(updater.status().available.version, '2.0.0');
  assert.throws(
    () => createUpdater({ root, origin: 'http://192.168.2.11:8080' }), // gitleaks:allow
    /binding/,
  );
});

test('a higher sequence cannot roll back a previously seen version', () => {
  assert.throws(
    () => verify(signed({ sequence: 5 }), { highestVersion: '3.0.0' }),
    /downgrade/,
  );
});

test('签名渠道、最低版本、灰度和精确来源回滚授权', () => {
  const policy = { schema: 2, rolloutPercentage: 25, rollbackFrom: null };
  assert.throws(
    () => verify(signed({ ...policy, channel: 'beta' })),
    /malformed/,
  );
  assert.equal(
    verify(signed({ ...policy, channel: 'beta' }), { channel: 'beta' }).channel,
    'beta',
  );
  for (const rolloutPercentage of [-1, 101, 0.5])
    assert.throws(
      () => verify(signed({ ...policy, rolloutPercentage })),
      /malformed|canonical domain/,
    );
  assert.throws(
    () =>
      verify(signed({ ...policy, version: '0.9.0', minimumVersion: '0.8.0' })),
    /downgrade/,
  );
  const rollback = signed({
    ...policy,
    version: '0.9.0',
    minimumVersion: '0.8.0',
    rollbackFrom: '1.0.0',
  });
  assert.equal(verify(rollback, { highestVersion: '3.0.0' }).version, '0.9.0');
  assert.throws(
    () => verify(rollback, { currentVersion: '2.0.0' }),
    /downgrade/,
  );
  assert.throws(() => verify(rollback, { highestSequence: 3 }), /replay/);
  const { signature, ...body } = signed(policy);
  assert.ok(signature);
  assert.throws(
    () =>
      verify(signed({ ...policy, minimumVersion: '2.0.0' }), {
        highestSequence: 2,
        acceptedDigest: body.sha256,
        acceptedEnvelope: releaseDigest(body),
      }),
    /replay/,
  );
});

function resumedUpdater(f, request, extra = {}) {
  return createUpdater({
    root: f.root,
    origin: 'https://example.com',
    publicKey: keys.publicKey,
    now: () => now,
    request,
    extract: () => f.next.dir,
    smoke: () => {},
    defer: false,
    ...extra,
  });
}

test('取消保留部分字节，刷新状态仍显示下载中，重建更新器后跨进程 Range 续传', async (t) => {
  const f = fixture(t);
  const manifest = signed({ packageId: f.next.id });
  let started;
  const reached = new Promise((resolve) => {
    started = resolve;
  });
  const first = resumedUpdater(f, async (url, init) => {
    if (/\/latest(?:\?|$)/.test(url))
      return new Response(JSON.stringify(manifest));
    return {
      ok: true,
      status: 200,
      body: (async function* () {
        yield Buffer.from('zi');
        started();
        await new Promise((resolve, reject) => {
          init.signal.addEventListener(
            'abort',
            () => reject(init.signal.reason),
            { once: true },
          );
        });
      })(),
    };
  });
  await first.check();
  const downloading = first.update();
  await reached;
  assert.equal(first.status().downloading, true);
  assert.equal(first.status().downloadedBytes, 2);
  assert.equal(first.status().available.version, '2.0.0');
  first.cancel();
  await assert.rejects(downloading);
  assert.equal(first.status().phase, 'cancelled');
  let range;
  const second = resumedUpdater(f, async (url, init) => {
    if (/\/latest(?:\?|$)/.test(url))
      return new Response(JSON.stringify(manifest));
    range = init.headers.Range;
    return new Response('p', {
      status: 206,
      headers: { 'Content-Range': 'bytes 2-2/3' },
    });
  });
  assert.equal(second.status().available.version, '2.0.0');
  await second.update();
  assert.equal(range, 'bytes=2-');
  assert.equal(readState(f.root).current, f.next.id);
  assert.equal(
    readFileSync(join(profileDir(f.dataHome), 'user-data.txt'), 'utf8'),
    'keep',
  );
});

test('下载完成后按阶段上报校验进度，而不是笼统 verifying', async (t) => {
  const f = fixture(t);
  const manifest = signed({ packageId: f.next.id });
  const stages = [];
  const updater = resumedUpdater(
    f,
    async (url) =>
      /\/latest(?:\?|$)/.test(url)
        ? new Response(JSON.stringify(manifest))
        : new Response('zip'),
    {
      defer: true,
      onState: (state) => {
        if (state.phase === 'verifying' && state.stage)
          stages.push(state.stage);
      },
    },
  );
  await updater.check();
  await updater.update();
  assert.deepEqual(stages, [
    'verify-archive',
    'extract',
    'verify-package',
    'check-release',
  ]);
  assert.equal(updater.status().restartRequired, true);
});

test('强制最低版本绕过零灰度及检查偏好，不能关闭或切渠道逃避', async (t) => {
  const f = fixture(t);
  const manifest = signed({
    packageId: f.next.id,
    schema: 2,
    minimumVersion: '2.0.0',
    rolloutPercentage: 0,
    rollbackFrom: null,
  });
  const updater = resumedUpdater(
    f,
    async () => new Response(JSON.stringify(manifest)),
  );
  updater.configure(false);
  await updater.check();
  assert.equal(updater.status().required, true);
  assert.equal(updater.status().available.version, '2.0.0');
  assert.throws(() => updater.configure(false), /mandatory/);
  assert.throws(() => updater.setChannel('beta'), /channel/);
});

test('普通零灰度不提供更新，下载完成时撤回不会安装', async (t) => {
  const f = fixture(t);
  let withdrawn = false;
  let manifest = signed({
    packageId: f.next.id,
    schema: 2,
    rolloutPercentage: 0,
    rollbackFrom: null,
  });
  const updater = resumedUpdater(f, async (url) => {
    if (/\/latest(?:\?|$)/.test(url))
      return withdrawn
        ? new Response('', { status: 404 })
        : new Response(JSON.stringify(manifest));
    withdrawn = true;
    return new Response('zip');
  });
  await updater.check();
  assert.equal(updater.status().available, null);
  manifest = signed({
    packageId: f.next.id,
    schema: 2,
    sequence: 3,
    rolloutPercentage: 100,
    rollbackFrom: null,
  });
  await updater.check();
  await assert.rejects(updater.update(), /withdrawn/);
  assert.equal(readState(f.root).current, f.old.id);
  assert.equal(existsSync(join(f.root, '.desktop-resume')), false);
});

test('下载后确认新的有效签名已替代发行，丢弃原包', async (t) => {
  const f = fixture(t);
  let downloaded = false;
  const updater = resumedUpdater(f, async (url) => {
    if (/\/latest(?:\?|$)/.test(url))
      return new Response(
        JSON.stringify(
          signed({ packageId: f.next.id, sequence: downloaded ? 3 : 2 }),
        ),
      );
    downloaded = true;
    return new Response('zip');
  });
  await updater.check();
  await assert.rejects(updater.update(), /replaced/);
  assert.equal(existsSync(join(f.root, '.desktop-resume')), false);
  assert.equal(readState(f.root).current, f.old.id);
});

test('启动检查并在4至8小时内带抖动重排，关闭取消定时器', async (t) => {
  const f = fixture(t);
  let calls = 0,
    callback,
    delay,
    cleared;
  const timer = { unref() {} };
  const updater = resumedUpdater(
    f,
    async () => {
      calls++;
      return new Response(JSON.stringify(signed({ packageId: f.next.id })));
    },
    {
      random: () => 0.25,
      schedule: (fn, ms) => {
        callback = fn;
        delay = ms;
        return timer;
      },
      unschedule: (x) => {
        cleared = x;
      },
    },
  );
  const stop = updater.start();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(calls, 1);
  assert.equal(delay, 5 * 60 * 60 * 1000);
  await callback();
  assert.equal(calls, 2);
  stop();
  assert.equal(cleared, timer);
});

test('仅见过失败或灰度版本的客户端可接受更高序号的签名回滚替代版，但不能借此降低已装版本', () => {
  const policy = signed({
    schema: 2,
    rolloutPercentage: 100,
    rollbackFrom: '3.0.0',
    sequence: 5,
  });
  assert.equal(
    verify(policy, { highestVersion: '3.0.0', highestSequence: 4 }).version,
    '2.0.0',
  );
  assert.throws(
    () =>
      verify(policy, {
        currentVersion: '2.5.0',
        highestVersion: '3.0.0',
        highestSequence: 4,
      }),
    /downgrade/,
  );
  assert.throws(
    () => verify(policy, { highestVersion: '4.0.0', highestSequence: 4 }),
    /downgrade/,
  );
});

for (const firstChannel of ['stable', 'beta']) {
  for (const mandatory of [false, true]) {
    test(`${firstChannel} 未命中灰度后，另一渠道仍可普通或强制升级 (${mandatory})`, async (t) => {
      const f = fixture(t);
      const secondChannel = firstChannel === 'stable' ? 'beta' : 'stable';
      const releases = {
        [firstChannel]: signed({
          schema: 2,
          channel: firstChannel,
          version: '3.0.0',
          sequence: 9,
          rolloutPercentage: 0,
          rollbackFrom: null,
        }),
        [secondChannel]: signed({
          schema: 2,
          channel: secondChannel,
          packageId: f.next.id,
          sequence: 2,
          minimumVersion: mandatory ? '2.0.0' : '1.0.0',
          rolloutPercentage: mandatory ? 0 : 100,
          rollbackFrom: null,
        }),
      };
      const request = async (url) => {
        if (url.includes('/latest')) {
          const channel = new URL(url).searchParams.get('channel') ?? 'stable';
          return new Response(JSON.stringify(releases[channel]));
        }
        return new Response('zip');
      };
      const updater = resumedUpdater(f, request);
      updater.setChannel(firstChannel);
      await updater.check();
      assert.equal(updater.status().available, null);
      const stateFile = join(f.root, 'desktop-update.json');
      assert.equal(
        JSON.parse(readFileSync(stateFile)).channelWatermarks[firstChannel],
        undefined,
      );
      updater.setChannel(secondChannel);
      const restarted = resumedUpdater(f, request);
      await restarted.check();
      assert.equal(restarted.status().available.version, '2.0.0');
      assert.equal(restarted.status().required, mandatory);
      await restarted.update();
      assert.equal(readState(f.root).current, f.next.id);
      restarted.setChannel(firstChannel);
      releases[firstChannel] = signed({
        schema: 2,
        channel: firstChannel,
        sequence: 1,
        packageId: f.next.id,
        rolloutPercentage: 100,
        rollbackFrom: null,
      });
      await restarted.check();
      assert.equal(
        JSON.parse(readFileSync(stateFile)).channelWatermarks[firstChannel]
          .highestSequence,
        1,
      );
      restarted.setChannel(secondChannel);
      releases[secondChannel] = signed({
        schema: 2,
        channel: secondChannel,
        packageId: f.next.id,
        sequence: 1,
        rolloutPercentage: 100,
        rollbackFrom: null,
      });
      await assert.rejects(restarted.check(), /replay/);
    });
  }
}

test('已接受的 beta 水位独立保留，关闭可选更新时未接受的清单不推进水位', async (t) => {
  const f = fixture(t);
  let manifest = signed({
    schema: 2,
    channel: 'beta',
    version: '3.0.0',
    sequence: 9,
    rolloutPercentage: 100,
    rollbackFrom: null,
  });
  const updater = resumedUpdater(
    f,
    async () => new Response(JSON.stringify(manifest)),
  );
  updater.setChannel('beta');
  updater.configure(false);
  await updater.check();
  assert.equal(updater.status().available, null);
  assert.equal(
    JSON.parse(readFileSync(join(f.root, 'desktop-update.json')))
      .channelWatermarks.beta,
    undefined,
  );
  updater.configure(true);
  await updater.check();
  updater.setChannel('stable');
  manifest = signed({ packageId: f.next.id });
  await updater.check();
  assert.equal(updater.status().available.version, '2.0.0');
  updater.setChannel('beta');
  manifest = signed({
    schema: 2,
    channel: 'beta',
    version: '3.0.0',
    sequence: 8,
    rolloutPercentage: 100,
    rollbackFrom: null,
  });
  await assert.rejects(updater.check(), /replay/);
});

for (const failure of ['network', 'unavailable']) {
  test(`下载后在线复核 ${failure} 保留完整缓存，下次不重下`, async (t) => {
    const f = fixture(t);
    const manifest = signed({ packageId: f.next.id });
    let latestCalls = 0,
      packages = 0,
      offline = true;
    const updater = resumedUpdater(f, async (url) => {
      if (/\/latest(?:\?|$)/.test(url)) {
        latestCalls++;
        if (latestCalls > 1 && offline) {
          if (failure === 'network') throw new Error('network timeout');
          return new Response('', { status: 503 });
        }
        return new Response(JSON.stringify(manifest));
      }
      packages++;
      return new Response('zip');
    });
    await updater.check();
    await assert.rejects(updater.update(), /timeout|unavailable/);
    assert.equal(
      readFileSync(
        join(f.root, '.desktop-resume', desktopPlatform().stagedPackageName),
        'utf8',
      ),
      'zip',
    );
    assert.equal(readState(f.root).current, f.old.id);
    offline = false;
    await updater.update();
    assert.equal(packages, 1);
    assert.equal(readState(f.root).current, f.next.id);
  });

  test(`启动复核 ${failure} 保留排队包，下次联网后正常安装`, async (t) => {
    const f = fixture(t);
    const manifest = signed({ packageId: f.next.id });
    const updater = resumedUpdater(
      f,
      async (url) =>
        /\/latest(?:\?|$)/.test(url)
          ? new Response(JSON.stringify(manifest))
          : new Response('zip'),
      { defer: true },
    );
    await updater.check();
    await updater.update();
    const options = {
      publicKey: keys.publicKey,
      now,
      smoke: () => {},
      extract: () => f.next.dir,
    };
    assert.equal(readUpdateState(f.root).lastAttempt.result, undefined);
    await assert.rejects(
      applyPending(f.root, {
        ...options,
        request: async () => {
          if (failure === 'network') throw new Error('network timeout');
          return new Response('', { status: 503 });
        },
      }),
      /timeout|unavailable/,
    );
    assert.equal(
      readFileSync(
        join(f.root, 'desktop-pending', desktopPlatform().stagedPackageName),
        'utf8',
      ),
      'zip',
    );
    assert.equal(readState(f.root).current, f.old.id);
    await applyPending(f.root, {
      ...options,
      request: async () => new Response(JSON.stringify(manifest)),
    });
    assert.equal(readUpdateState(f.root).lastAttempt.result, 'succeeded');
    assert.equal(readState(f.root).current, f.next.id);
  });
}

for (const reason of ['withdrawn', 'replaced']) {
  test(`启动复核明确 ${reason} 丢弃排队包`, async (t) => {
    const f = fixture(t);
    const manifest = signed({ packageId: f.next.id });
    const updater = resumedUpdater(
      f,
      async (url) =>
        /\/latest(?:\?|$)/.test(url)
          ? new Response(JSON.stringify(manifest))
          : new Response('zip'),
      { defer: true },
    );
    await updater.check();
    await updater.update();
    await assert.rejects(
      applyPending(f.root, {
        publicKey: keys.publicKey,
        now,
        smoke: () => {},
        extract: () => f.next.dir,
        request: async () =>
          reason === 'withdrawn'
            ? new Response('', { status: 404 })
            : new Response(
                JSON.stringify(signed({ sequence: 3, packageId: f.next.id })),
              ),
      }),
      /withdrawn|replaced/,
    );
    assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
    assert.equal(readState(f.root).current, f.old.id);
    assert.equal(
      readUpdateState(f.root).failure.code,
      reason === 'withdrawn'
        ? 'desktop.release_withdrawn'
        : 'desktop.release_replaced',
    );
  });
}

for (const failure of ['interrupted', 'temporary', 'persistent', 'smoke']) {
  test(`pending recovery retains signed input across ${failure} and retries safely`, async (t) => {
    const f = fixture(t);
    const manifest = signed({ packageId: f.next.id });
    const request = async (url) =>
      /\/latest(?:\?|$)/.test(url)
        ? new Response(JSON.stringify(manifest))
        : new Response('zip');
    const updater = resumedUpdater(f, request, { defer: true });
    await updater.check();
    await updater.update();
    const scratch = join(f.root, 'desktop-pending', 'apply');
    mkdirSync(scratch);
    writeFileSync(join(scratch, 'interrupted-output'), 'incomplete');
    let attempts = 0;
    const events = [];
    const options = {
      publicKey: keys.publicKey,
      now,
      request,
      onState: (event) => events.push(event),
      extract: (_archive, target) => {
        attempts++;
        mkdirSync(target); // Regression: old applyPending hit EEXIST here.
        if (
          failure === 'persistent' ||
          (failure === 'temporary' && attempts === 1)
        )
          throw Object.assign(new Error('token=private-content'), {
            code: 'EPERM',
            path: 'private-user-path',
          });
        return f.next.dir;
      },
      smoke: () => {
        if (failure === 'smoke')
          throw Object.assign(new Error('private content'), {
            code: 'desktop.smoke_failed',
          });
      },
    };
    if (['persistent', 'smoke'].includes(failure)) {
      await assert.rejects(applyPending(f.root, options));
      assert.equal(readState(f.root).current, f.old.id);
      assert.equal(
        readFileSync(
          join(f.root, 'desktop-pending', desktopPlatform().stagedPackageName),
          'utf8',
        ),
        'zip',
      );
      const saved = readUpdateState(f.root);
      assert.equal(
        saved.lastAttempt.result,
        failure === 'smoke' ? 'rolled_back' : 'failed',
      );
      assert.equal(saved.lastAttempt.targetVersion, '2.0.0');
      assert.equal(
        saved.failure.code,
        failure === 'smoke' ? 'desktop.smoke_failed' : 'EPERM',
      );
      const log = readFileSync(join(f.root, 'desktop-update.log'), 'utf8');
      assert.doesNotMatch(log, /private|token=/);
      await applyPending(f.root, {
        ...options,
        smoke: () => {},
        extract: () => f.next.dir,
      });
    } else await applyPending(f.root, options);
    assert.equal(
      attempts,
      failure === 'temporary' ? 2 : failure === 'persistent' ? 4 : 1,
    );
    assert.equal(readState(f.root).current, f.next.id);
    assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
    assert.equal(
      readFileSync(join(profileDir(f.dataHome), 'user-data.txt'), 'utf8'),
      'keep',
    );
    assert.ok(events.some((event) => event.stage === 'extract'));
  });
}

test('pending signature and archive hash failures discard unusable input', async (t) => {
  for (const mode of ['signature', 'hash']) {
    const f = fixture(t);
    const manifest = signed({ packageId: f.next.id });
    const request = async (url) =>
      /\/latest(?:\?|$)/.test(url)
        ? new Response(JSON.stringify(manifest))
        : new Response('zip');
    const updater = resumedUpdater(f, request, { defer: true });
    await updater.check();
    await updater.update();
    if (mode === 'signature')
      writeFileSync(
        join(f.root, 'desktop-pending/release.json'),
        JSON.stringify({ ...manifest, notes: 'tampered' }),
      );
    else
      writeFileSync(
        join(f.root, 'desktop-pending', desktopPlatform().stagedPackageName),
        'tampered',
      );
    await assert.rejects(
      applyPending(f.root, { publicKey: keys.publicKey, now, request }),
    );
    assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
    assert.equal(readState(f.root).current, f.old.id);
    assert.equal(
      readUpdateState(f.root).failure.code,
      mode === 'signature'
        ? 'desktop.release_invalid'
        : 'desktop.archive_invalid',
    );
  }
});

test('a committed transaction with leftover pending input preserves the previous version', async (t) => {
  const f = fixture(t);
  const manifest = signed({ packageId: f.next.id });
  const request = async (url) =>
    /\/latest(?:\?|$)/.test(url)
      ? new Response(JSON.stringify(manifest))
      : new Response('zip');
  const updater = resumedUpdater(f, request, { defer: true });
  await updater.check();
  await updater.update();
  install({
    root: f.root,
    dataHome: f.dataHome,
    payload: f.next.dir,
    smoke: () => {},
  });
  assert.equal(readState(f.root).previous, f.old.id);
  await applyPending(f.root, {
    publicKey: keys.publicKey,
    now,
    request,
    extract: () => assert.fail('already committed'),
  });
  assert.equal(readState(f.root).previous, f.old.id);
  assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
});

for (const lockTarget of ['scratch', 'launcher']) {
  for (const persistent of [false, true]) {
    test(
      `Windows native file lock on ${lockTarget} (${persistent ? 'persistent' : 'temporary'})`,
      { skip: process.platform !== 'win32' },
      async (t) => {
        const f = fixture(t);
        const manifest = signed({ packageId: f.next.id });
        const request = async (url) =>
          /\/latest(?:\?|$)/.test(url)
            ? new Response(JSON.stringify(manifest))
            : new Response('zip');
        const updater = resumedUpdater(f, request, { defer: true });
        await updater.check();
        await updater.update();
        const scratch = join(f.root, 'desktop-pending/apply');
        mkdirSync(scratch);
        const locked =
          lockTarget === 'launcher'
            ? join(f.root, 'bin', 'auriga-desktop.cmd')
            : join(scratch, 'locked');
        writeFileSync(locked, 'scratch');
        const script = join(f.root, 'lock.ps1');
        writeFileSync(
          script,
          `param([string]$File)\n$f=[IO.File]::Open($File,'Open','Read','None')\nWrite-Output 'locked'\n[Console]::ReadLine() | Out-Null\n$f.Dispose()\n`,
        );
        const child = spawn(
          'powershell.exe',
          ['-NoProfile', '-File', script, locked],
          { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] },
        );
        const exited = once(child, 'exit');
        let releaseTimer;
        let releaseRequested = false;
        const releaseLock = () => {
          clearTimeout(releaseTimer);
          releaseTimer = undefined;
          if (releaseRequested || child.exitCode !== null) return;
          releaseRequested = true;
          child.stdin.end('\n');
        };
        try {
          await once(child.stdout, 'data');
          assert.throws(
            () => rmSync(locked),
            (error) => {
              t.diagnostic(
                `first native lock error code=${error.code} syscall=${error.syscall} category=${lockTarget}`,
              );
              return ['EPERM', 'EBUSY', 'EACCES'].includes(error.code);
            },
          );
          const options = {
            publicKey: keys.publicKey,
            now,
            request,
            smoke: () => {},
            extract: () => f.next.dir,
          };
          if (persistent) {
            await assert.rejects(applyPending(f.root, options), (error) =>
              ['EPERM', 'EBUSY', 'EACCES'].includes(error.code),
            );
            assert.ok(
              existsSync(
                join(
                  f.root,
                  'desktop-pending',
                  desktopPlatform().stagedPackageName,
                ),
              ),
            );
            assert.equal(readState(f.root).current, f.old.id);
          } else releaseTimer = setTimeout(releaseLock, 400);
          if (persistent) releaseLock();
          if (persistent) await exited;
          await applyPending(f.root, options);
          assert.equal(readState(f.root).current, f.next.id);
        } finally {
          releaseLock();
          await exited;
          rmSync(script, { force: true });
        }
      },
    );
  }
}

for (const missing of [
  'empty',
  'release.json',
  desktopPlatform().stagedPackageName,
]) {
  for (const entry of ['launch', 'check']) {
    test(`incomplete pending (${missing}) recovers through ${entry} and discovers releases`, async (t) => {
      const f = fixture(t);
      const manifest = signed({ packageId: f.next.id });
      const request = async (url) =>
        /\/latest(?:\?|$)/.test(url)
          ? new Response(JSON.stringify(manifest))
          : new Response('zip');
      const updater = resumedUpdater(f, request, { defer: true });
      await updater.check();
      await updater.update();
      const queued = join(f.root, 'desktop-pending');
      if (missing === 'empty') {
        rmSync(queued, { recursive: true });
        mkdirSync(queued);
      } else rmSync(join(queued, missing));
      if (entry === 'launch') {
        assert.equal(
          await applyPending(f.root, {
            publicKey: keys.publicKey,
            now,
            request,
          }),
          false,
        );
        assert.equal(
          readUpdateState(f.root).failure.code,
          'desktop.pending_incomplete',
        );
      }
      const status = await updater.check();
      assert.equal(existsSync(queued), false);
      assert.equal(status.available.version, '2.0.0');
      await updater.update();
      assert.ok(existsSync(join(queued, desktopPlatform().stagedPackageName)));
    });
  }
}

async function pausePending(f) {
  const manifest = signed({ packageId: f.next.id });
  const request = async (url) =>
    /\/latest(?:\?|$)/.test(url)
      ? new Response(JSON.stringify(manifest))
      : new Response('zip');
  const updater = resumedUpdater(f, request, { defer: true });
  await updater.check();
  await updater.update();
  let attempts = 0;
  const options = {
    publicKey: keys.publicKey,
    now,
    request,
    extract: () => {
      attempts++;
      throw Object.assign(new Error('private extractor output'), {
        code: 'desktop.extract_failed',
      });
    },
  };
  for (let i = 0; i < 3; i++)
    await assert.rejects(applyPending(f.root, options));
  return { manifest, request, options, attempts: () => attempts };
}

test('incomplete input cleanup preserves the installation ownership gate', async (t) => {
  const f = fixture(t);
  const queued = join(f.root, 'desktop-pending');
  mkdirSync(queued);
  const marker = join(f.root, '.auriga-desktop-install');
  const original = readFileSync(marker);
  rmSync(marker);
  try {
    await assert.rejects(f.updater.check(), /installation missing/);
    assert.equal(existsSync(queued), true);
  } finally {
    writeFileSync(marker, original);
  }
});

test('three deterministic failures persist across launches and require explicit manual retry', async (t) => {
  const f = fixture(t);
  const manifest = signed({ packageId: f.next.id });
  const request = async (url) =>
    /\/latest(?:\?|$)/.test(url)
      ? new Response(JSON.stringify(manifest))
      : new Response('zip');
  const updater = resumedUpdater(f, request, { defer: true });
  await updater.check();
  await updater.update();
  let smokeCalls = 0;
  const options = {
    publicKey: keys.publicKey,
    now,
    request,
    extract: () => f.next.dir,
    smoke: () => {
      smokeCalls++;
      throw Object.assign(new Error('smoke'), { code: 'desktop.smoke_failed' });
    },
  };
  for (let i = 1; i <= 3; i++) {
    await assert.rejects(applyPending(f.root, options));
    assert.equal(readUpdateState(f.root).applyRetry.failures, i);
  }
  for (let i = 0; i < 2; i++)
    assert.equal(await applyPending(f.root, options), false);
  assert.equal(smokeCalls, 3);
  const restarted = resumedUpdater(f, request);
  assert.equal(restarted.status().manualRetryRequired, true);
  assert.equal(restarted.status().phase, 'awaiting-retry');
  await restarted.check();
  assert.equal(restarted.status().applyFailures, 3);
  assert.equal(restarted.status().failure.code, 'desktop.smoke_failed');
  assert.ok(
    existsSync(
      join(f.root, 'desktop-pending', desktopPlatform().stagedPackageName),
    ),
  );
  approvePending(f.root);
  assert.equal(restarted.status().manualRetryRequired, false);
  await applyPending(f.root, { ...options, smoke: () => {} });
  assert.equal(readState(f.root).current, f.next.id);
  assert.equal(restarted.status().applyFailures, 0);
});

for (const change of ['withdrawn', 'replacement']) {
  for (const entry of ['launch', 'check']) {
    test(`paused deterministic extraction resets on ${change} through ${entry}`, async (t) => {
      const f = fixture(t);
      const paused = await pausePending(f);
      assert.equal(await applyPending(f.root, paused.options), false);
      assert.equal(paused.attempts(), 3);
      const next = signed({ packageId: f.next.id, sequence: 3 });
      const request = async () =>
        change === 'withdrawn'
          ? new Response('', { status: 404 })
          : new Response(JSON.stringify(next));
      if (entry === 'launch')
        await assert.rejects(
          applyPending(f.root, { ...paused.options, request }),
          /withdrawn|replaced/,
        );
      else await resumedUpdater(f, request).check();
      assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
      assert.equal(readUpdateState(f.root).applyRetry, null);
      if (change === 'replacement' && entry === 'check')
        assert.equal(resumedUpdater(f, request).status().available.sequence, 3);
    });
  }
}

test('each extraction retry rechecks the durable archive hash', async (t) => {
  const f = fixture(t);
  const manifest = signed({ packageId: f.next.id });
  const request = async (url) =>
    /\/latest(?:\?|$)/.test(url)
      ? new Response(JSON.stringify(manifest))
      : new Response('zip');
  const updater = resumedUpdater(f, request, { defer: true });
  await updater.check();
  await updater.update();
  let extractions = 0;
  await assert.rejects(
    applyPending(f.root, {
      publicKey: keys.publicKey,
      now,
      request,
      extract: (archive) => {
        extractions++;
        writeFileSync(archive, 'tampered');
        throw Object.assign(new Error('busy'), { code: 'EPERM' });
      },
    }),
    { code: 'desktop.archive_invalid' },
  );
  assert.equal(extractions, 1);
  assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
});

test('update diagnostics rotate at 256 KiB and uninstall removes both files', async (t) => {
  const f = fixture(t);
  const log = join(f.root, 'desktop-update.log');
  writeFileSync(log, 'x'.repeat(256 * 1024));
  await pausePending(f);
  assert.equal(readFileSync(log + '.1').length, 256 * 1024);
  assert.ok(readFileSync(log).length < 256 * 1024);
  assert.equal(existsSync(log + '.2'), false);
  uninstall({ root: f.root });
  assert.equal(existsSync(log), false);
  assert.equal(existsSync(log + '.1'), false);
});

test('defer queue rename retries transient EPERM and keeps resume if publish still fails', async (t) => {
  const f = fixture(t);
  const manifest = signed({ packageId: f.next.id });
  const request = async (url) =>
    /\/latest(?:\?|$)/.test(url)
      ? new Response(JSON.stringify(manifest))
      : new Response('zip');
  let attempts = 0;
  const updater = resumedUpdater(f, request, {
    defer: true,
    queueRenameDelays: [1, 1],
    rename(from, to) {
      attempts++;
      if (attempts <= 2) {
        throw Object.assign(new Error('filter hold'), { code: 'EPERM' });
      }
      return renameSync(from, to);
    },
  });
  await updater.check();
  await updater.update();
  assert.equal(attempts, 3);
  assert.equal(
    existsSync(join(f.root, 'desktop-pending', 'release.json')),
    true,
  );
  assert.equal(existsSync(join(f.root, '.desktop-resume')), false);
  assert.equal(updater.status().restartRequired, true);

  rmSync(join(f.root, 'desktop-pending'), { recursive: true, force: true });
  changeUpdateState(f.root, (state) => ({
    ...state,
    phase: 'idle',
    available: manifest,
    applyRetry: null,
  }));
  let denied = 0;
  const blocked = resumedUpdater(f, request, {
    defer: true,
    queueRenameDelays: [1, 1],
    rename() {
      denied++;
      throw Object.assign(new Error('still denied'), { code: 'EPERM' });
    },
  });
  await assert.rejects(blocked.update(), { code: 'EPERM' });
  assert.equal(denied, 3);
  assert.equal(
    readFileSync(
      join(f.root, '.desktop-resume', desktopPlatform().stagedPackageName),
      'utf8',
    ),
    'zip',
  );
  assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
});

test('writeAtomic retries transient EPERM/EBUSY renames and cleans temp files on failure', (t) => {
  const base = tempBase(t);
  const target = join(base, 'current.txt');
  let attempts = 0;
  writeAtomic(target, 'v2\n', {
    retries: 5,
    rename(from, to) {
      attempts++;
      if (attempts <= 2) {
        throw Object.assign(new Error('transient lock'), {
          code: attempts === 1 ? 'EPERM' : 'EBUSY',
        });
      }
      writeFileSync(to, readFileSync(from));
      rmSync(from);
    },
  });
  assert.equal(attempts, 3);
  assert.equal(readFileSync(target, 'utf8'), 'v2\n');
  assert.deepEqual(readdirSync(base), ['current.txt']);

  assert.throws(
    () =>
      writeAtomic(target, 'v3\n', {
        retries: 2,
        rename() {
          throw Object.assign(new Error('still denied'), { code: 'EPERM' });
        },
      }),
    { code: 'EPERM' },
  );
  assert.deepEqual(readdirSync(base), ['current.txt']);
});

test('已排队更新在服务端撤回时 check() 自动清理；重启瞬间遇更高正常版本允许完成切换并入队新版', async (t) => {
  const f = fixture(t);
  const v2 = signed({ packageId: f.next.id, version: '2.0.0', sequence: 2 });
  const v3 = signed({
    packageId: 'package-3',
    version: '2.1.0',
    sequence: 3,
    minimumVersion: '1.0.0',
  });
  let mode = 'v2';
  const request = async (url) => {
    if (/\/latest(?:\?|$)/.test(url)) {
      if (mode === 'withdrawn') return new Response('', { status: 404 });
      return new Response(JSON.stringify(mode === 'v3' ? v3 : v2));
    }
    return new Response('zip');
  };
  const updater = resumedUpdater(f, request, { defer: true });
  await updater.check();
  await updater.update();
  assert.equal(updater.status().restartRequired, true);
  assert.equal(updater.status().pendingVersion, '2.0.0');

  // 服务端撤回（404）后，后台 check() 自动清理待装队列
  mode = 'withdrawn';
  const afterWithdraw = await updater.check();
  assert.equal(afterWithdraw.restartRequired, false);
  assert.equal(existsSync(join(f.root, 'desktop-pending')), false);

  // 重新排队 v2，重启瞬间服务端刚发布更高的正常迭代 v2.1.0 -> 允许先完成 v2 切换，并把 v2.1.0 记入 available
  mode = 'v2';
  await updater.check();
  await updater.update();
  mode = 'v3';
  const applied = await applyPending(f.root, {
    publicKey: keys.publicKey,
    now,
    request,
    smoke: () => {},
    extract: () => f.next.dir,
  });
  assert.equal(applied, true);
  assert.equal(readState(f.root).current, f.next.id);
  assert.equal(readUpdateState(f.root).available.version, '2.1.0');
});

test('重启瞬间撞上回滚源或提高 minimumVersion 严格拒绝；撞上零灰度新版不绕过灰度写入 available', async (t) => {
  // 1. 回滚源与提高 minimumVersion 均拒绝
  for (const kind of ['rollback', 'minVersion']) {
    const f = fixture(t);
    const v2 = signed({
      schema: 2,
      rolloutPercentage: 100,
      rollbackFrom: null,
      packageId: f.next.id,
      version: '2.0.0',
      sequence: 2,
    });
    const replacement =
      kind === 'rollback'
        ? signed({
            schema: 2,
            rolloutPercentage: 100,
            rollbackFrom: '2.0.0',
            packageId: 'package-3',
            version: '2.1.0',
            sequence: 3,
          })
        : signed({
            schema: 2,
            rolloutPercentage: 100,
            rollbackFrom: null,
            packageId: 'package-3',
            version: '2.1.0',
            minimumVersion: '2.1.0',
            sequence: 3,
          });
    let switched = false;
    const request = async (url) => {
      if (/\/latest(?:\?|$)/.test(url))
        return new Response(JSON.stringify(switched ? replacement : v2));
      return new Response('zip');
    };
    const updater = resumedUpdater(f, request, { defer: true });
    await updater.check();
    await updater.update();
    switched = true;
    await assert.rejects(
      applyPending(f.root, {
        publicKey: keys.publicKey,
        now,
        request,
        smoke: () => {},
        extract: () => f.next.dir,
      }),
      /replaced/,
    );
    assert.equal(existsSync(join(f.root, 'desktop-pending')), false);
    assert.equal(readState(f.root).current, f.old.id);
  }

  // 2. 撞上零灰度更高正常版：允许完成当前 v2 切换，但不绕过灰度把 v2.1.0 写入 available
  const f = fixture(t);
  const v2 = signed({
    schema: 2,
    rolloutPercentage: 100,
    rollbackFrom: null,
    packageId: f.next.id,
    version: '2.0.0',
    sequence: 2,
  });
  const v3ZeroRollout = signed({
    schema: 2,
    rolloutPercentage: 0,
    rollbackFrom: null,
    packageId: 'package-3',
    version: '2.1.0',
    minimumVersion: '1.0.0',
    sequence: 3,
  });
  let switched = false;
  const request = async (url) => {
    if (/\/latest(?:\?|$)/.test(url))
      return new Response(JSON.stringify(switched ? v3ZeroRollout : v2));
    return new Response('zip');
  };
  const updater = resumedUpdater(f, request, { defer: true });
  await updater.check();
  await updater.update();
  switched = true;
  assert.equal(
    await applyPending(f.root, {
      publicKey: keys.publicKey,
      now,
      request,
      smoke: () => {},
      extract: () => f.next.dir,
    }),
    true,
  );
  assert.equal(readState(f.root).current, f.next.id);
  assert.equal(readUpdateState(f.root).available, null);
});
